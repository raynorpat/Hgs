package models

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestForkSelectedSourceAndAccess(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "origin-owner", true)
	actor := newAccessTestUser(t, "fork-owner", true)
	source := newAccessTestRepo(t, owner, "private-source", true)
	fork := newAccessTestRepo(t, actor, "private-fork", true)
	child := newAccessTestRepo(t, actor, "child", false)
	for _, r := range []ForkRelation{{RepositoryID: fork.Id, SourceID: source.Id, FamilyID: source.Id}, {RepositoryID: child.Id, SourceID: fork.Id, FamilyID: source.Id}} {
		if _, err := x.Insert(&r); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{fork.Id, child.Id} {
		a, err := RepositoryAccessFor(actor.Id, id)
		if err != nil || a.Read || a.Write || a.Admin {
			t.Fatalf("ancestor denial %d: %+v %v", id, a, err)
		}
	}
	if _, err := x.Insert(&Access{UserId: actor.Id, RepoId: source.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	a, err := RepositoryAccessFor(actor.Id, child.Id)
	if err != nil || !a.Admin {
		t.Fatalf("authorized child: %+v %v", a, err)
	}
	ids, err := ForkAncestors(child.Id)
	if err != nil || len(ids) != 2 || ids[0] != fork.Id || ids[1] != source.Id {
		t.Fatalf("ancestors %v %v", ids, err)
	}
	if _, err = x.Insert(&ForkRelation{RepositoryID: source.Id, SourceID: child.Id, FamilyID: source.Id}); err != nil {
		t.Fatal(err)
	}
	if a, err = RepositoryAccessFor(actor.Id, child.Id); err == nil && a.Read {
		t.Fatal("cycle readable")
	}
}

func TestCatalogPrivateAncestry(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "catalog-origin", true)
	actor := newAccessTestUser(t, "catalog-forker", true)
	source := newAccessTestRepo(t, owner, "source", true)
	fork := newAccessTestRepo(t, actor, "fork", true)
	x.Insert(&ForkRelation{RepositoryID: fork.Id, SourceID: source.Id, FamilyID: source.Id})
	x.Insert(&Access{UserId: actor.Id, RepoId: source.Id, Mode: READABLE})
	if err := RecordRepositoryVisit(context.Background(), actor.Id, fork.Id, time.Now()); err != nil {
		t.Fatal(err)
	}
	x.Where("user_id = ? AND repo_id = ?", actor.Id, source.Id).Delete(new(Access))
	recent, err := RecentRepositories(context.Background(), actor.Id)
	if err != nil || len(recent) != 0 {
		t.Fatalf("private ancestry recents: %v %v", recent, err)
	}
	page, err := ListAccessibleRepositories(context.Background(), actor.Id, CatalogQuery{OwnerID: actor.Id})
	if err != nil || page.Total != 0 || len(page.Repositories) != 0 {
		t.Fatalf("private ancestry catalog: %+v %v", page, err)
	}
	rows, err := FilterReadableRepositories(actor.Id, []*Repository{fork})
	if err != nil || len(rows) != 0 {
		t.Fatalf("profile/org/team filter: %v %v", rows, err)
	}
}

func TestForkLifecycleRestrictions(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "life-origin", true)
	source := newAccessTestRepo(t, owner, "source", true)
	fork := newAccessTestRepo(t, owner, "fork", true)
	x.Insert(&ForkRelation{RepositoryID: fork.Id, SourceID: source.Id, FamilyID: source.Id})
	source.IsPrivate = false
	if err := UpdateRepository(source); err == nil {
		t.Fatal("parent visibility changed")
	}
	fork.IsPrivate = false
	if err := UpdateRepository(fork); err == nil {
		t.Fatal("private fork declassified")
	}
	if err := tombstoneReviewSafe(source.Id); err == nil {
		t.Fatal("parent tombstoned")
	}
}

func TestForkProvisioningRecovery(t *testing.T) {
	reload := newTestDatabase(t)
	owner := newAccessTestUser(t, "recovery-owner", true)
	source := newAccessTestRepo(t, owner, "source", true)
	op, repo, err := BeginForkProvisioning(owner.Id, source.Id, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "unfinished"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetRepositoryById(repo.Id); err != ErrRepoNotExist {
		t.Fatalf("provisioning visible: %v", err)
	}
	reload()
	root := newLifecycleRoot(t)
	if err := RollbackForkProvisioning(root, op); err != nil {
		t.Fatal(err)
	}
	pending, err := PendingForkProvisioning()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending: %v %v", pending, err)
	}
	if _, err := GetForkRelation(repo.Id); err == nil {
		t.Fatal("orphan relation")
	}
	if err := tombstoneReviewSafe(source.Id); err != nil {
		t.Fatalf("source fence retained: %v", err)
	}
}

func TestForkSafeLeafTransfer(t *testing.T) {
	newTestDatabase(t)
	origin := newAccessTestUser(t, "transfer-origin", true)
	actor := newAccessTestUser(t, "transfer-actor", true)
	recipient := newAccessTestUser(t, "transfer-recipient", true)
	source := newAccessTestRepo(t, origin, "source", true)
	fork := newAccessTestRepo(t, actor, "fork", true)
	fork.Owner = actor
	x.Insert(&ForkRelation{RepositoryID: fork.Id, SourceID: source.Id, FamilyID: source.Id})
	x.Insert(&Access{UserId: actor.Id, RepoId: source.Id, Mode: READABLE})
	if err := TransferOwnership(actor, recipient.Name, fork); err == nil {
		t.Fatal("transfer to ancestor-denied recipient")
	}
	x.Insert(&Access{UserId: recipient.Id, RepoId: source.Id, Mode: READABLE})
	if err := TransferOwnership(actor, recipient.Name, fork); err != nil {
		t.Fatal(err)
	}
	stored, err := GetRepositoryById(fork.Id)
	if err != nil || stored.OwnerId != recipient.Id || !stored.IsPrivate {
		t.Fatalf("transfer %+v %v", stored, err)
	}
	rel, err := GetForkRelation(fork.Id)
	if err != nil || rel.SourceID != source.Id {
		t.Fatalf("lineage %+v %v", rel, err)
	}
	x.Where("user_id = ? AND repo_id = ?", recipient.Id, source.Id).Delete(new(Access))
	access, err := RepositoryAccessFor(recipient.Id, fork.Id)
	if err != nil || access.Read {
		t.Fatalf("post-transfer revocation %+v %v", access, err)
	}
}

func TestForkOrganizationOwnerAndTransfer(t *testing.T) {
	newTestDatabase(t)
	origin := newAccessTestUser(t, "org-origin", true)
	actor := newAccessTestUser(t, "org-actor", true)
	other := newAccessTestUser(t, "org-other", true)
	org := &User{Name: "fork-org", Email: "fork-org@example.test", IsActive: true, Type: ORGANIZATION}
	if _, err := CreateOrganization(org, actor); err != nil {
		t.Fatal(err)
	}
	source := newAccessTestRepo(t, origin, "source", true)
	x.Insert(&Access{UserId: actor.Id, RepoId: source.Id, Mode: READABLE})
	if _, _, err := BeginForkProvisioning(other.Id, source.Id, CreateHgRepositoryOptions{OwnerID: org.Id, Name: "denied"}); err == nil {
		t.Fatal("nonowner provisioned organization fork")
	}
	op, repo, err := BeginForkProvisioning(actor.Id, source.Id, CreateHgRepositoryOptions{OwnerID: org.Id, Name: "orgfork"})
	if err != nil {
		t.Fatal(err)
	}
	// A removed destination owner must fail the final current-policy gate.
	x.Where("uid = ? AND org_id = ?", actor.Id, org.Id).Cols("is_owner").Update(&OrgUser{IsOwner: false})
	if _, err = PublishFork(context.Background(), newLifecycleRoot(t), op); err == nil {
		t.Fatal("revoked owner published")
	}
	root := newLifecycleRoot(t)
	if err = RollbackForkProvisioning(root, op); err != nil {
		t.Fatal(err)
	}
	x.Where("uid = ? AND org_id = ?", actor.Id, org.Id).Cols("is_owner").Update(&OrgUser{IsOwner: true})
	fork := newAccessTestRepo(t, actor, "transfer-leaf", true)
	fork.Owner = actor
	x.Insert(&ForkRelation{RepositoryID: fork.Id, SourceID: source.Id, FamilyID: source.Id})
	team, err := org.GetOwnerTeam()
	if err != nil {
		t.Fatal(err)
	}
	if err = team.AddMember(other.Id); err != nil {
		t.Fatal(err)
	}
	if err = TransferOwnership(actor, org.Name, fork); err == nil {
		t.Fatal("org transfer granted ancestor-denied member")
	}
	x.Insert(&Access{UserId: other.Id, RepoId: source.Id, Mode: READABLE})
	if err = TransferOwnership(actor, org.Name, fork); err != nil {
		t.Fatal(err)
	}
	if a, err := RepositoryAccessFor(other.Id, fork.Id); err != nil || !a.Admin {
		t.Fatalf("permitted org transfer %+v %v", a, err)
	}
	_ = repo
}

func TestForkCannotPublishUnpreparedAllocation(t *testing.T) {
	newTestDatabase(t)
	actor := newAccessTestUser(t, "unprepared-actor", true)
	source := newAccessTestRepo(t, actor, "source", true)
	op, repo, err := BeginForkProvisioning(actor.Id, source.Id, CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "unfinished"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PublishFork(context.Background(), newLifecycleRoot(t), op); err == nil {
		t.Fatal("unprepared allocation became ready")
	}
	if _, err = GetRepositoryById(repo.Id); err != ErrRepoNotExist {
		t.Fatalf("unprepared allocation visible: %v", err)
	}
}

// Stop actual model mutations at the same pre-transaction boundary that allowed
// a stale member writer to overwrite a completed transfer's team metadata.
func TestForkOrganizationMembershipTransferBoundary(t *testing.T) {
	newTestDatabase(t)
	actor := newAccessTestUser(t, "boundary-actor", true)
	other := newAccessTestUser(t, "boundary-member", true)
	org := &User{Name: "boundary-org", Email: "boundary@example.test", IsActive: true, Type: ORGANIZATION}
	if _, err := CreateOrganization(org, actor); err != nil {
		t.Fatal(err)
	}
	source := newAccessTestRepo(t, actor, "source", true)
	fork := newAccessTestRepo(t, actor, "leaf", true)
	fork.Owner = actor
	if _, err := x.Insert(&ForkRelation{RepositoryID: fork.Id, SourceID: source.Id, FamilyID: source.Id}); err != nil {
		t.Fatal(err)
	}
	old := newAccessTestRepo(t, org, "existing", true)
	team, err := org.GetOwnerTeam()
	if err != nil {
		t.Fatal(err)
	}
	if err = team.AddRepository(old); err != nil {
		t.Fatal(err)
	}
	if err = AddAccess(&Access{UserId: other.Id, RepoId: source.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	hit := false
	lifecycleTestHook = func(stage string) {
		if stage != "team-member-before-transaction" {
			return
		}
		hit = true
		if err := TransferOwnership(actor, org.Name, fork); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { lifecycleTestHook = nil })
	if err = AddTeamMember(org.Id, team.Id, other.Id); err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("boundary not reached")
	}
	var membership OrgUser
	if ok, err := x.Where("uid = ? AND org_id = ?", other.Id, org.Id).Get(&membership); err != nil || !ok || !membership.IsOwner || membership.NumTeams != 1 {
		t.Fatalf("AddOrgUser/team outcome %+v %v", membership, err)
	}
	orgAfter, err := GetUserById(org.Id)
	if err != nil || orgAfter.NumMembers != 2 {
		t.Fatalf("organization membership counter %+v %v", orgAfter, err)
	}
	stored, err := org.GetOwnerTeam()
	if err != nil {
		t.Fatal(err)
	}
	if stored.NumRepos != 2 || stored.NumMembers != 2 || !strings.Contains(stored.RepoIds, fmt.Sprintf("$%d|", old.Id)) || !strings.Contains(stored.RepoIds, fmt.Sprintf("$%d|", fork.Id)) {
		t.Fatalf("stale membership overwrote transferred repository metadata: %+v", stored)
	}
	for _, id := range []int64{old.Id, fork.Id} {
		var access Access
		ok, err := x.Where("user_id = ? AND repo_id = ?", other.Id, id).Get(&access)
		if err != nil || !ok || access.Mode != WRITABLE {
			t.Fatalf("new member missing repository access %d: %+v %v", id, access, err)
		}
	}
	current, err := GetRepositoryById(fork.Id)
	if err != nil || current.OwnerId != org.Id || !current.IsPrivate {
		t.Fatalf("transfer state %+v %v", current, err)
	}
	rel, err := GetForkRelation(fork.Id)
	if err != nil || rel.SourceID != source.Id || rel.FamilyID != source.Id {
		t.Fatalf("lineage %+v %v", rel, err)
	}
	if access, err := RepositoryAccessFor(other.Id, fork.Id); err != nil || !access.Admin {
		t.Fatalf("current effective access %+v %v", access, err)
	}
}

// All interleavings below invoke the actual model writers. The seams pause only
// between statements; they do not replace membership, access or transfer logic.
func TestForkOrganizationTransferAuthorityBoundary(t *testing.T) {
	for _, scenario := range []string{"member-first", "revoke-first", "ordinary-member-first", "member-while-authorized", "revoke-while-authorized"} {
		t.Run(scenario, func(t *testing.T) {
			newTestDatabase(t)
			actor := newAccessTestUser(t, "race-actor", true)
			other := newAccessTestUser(t, "race-other", true)
			org := &User{Name: "race-org", Email: "race@example.test", IsActive: true, Type: ORGANIZATION}
			if _, err := CreateOrganization(org, actor); err != nil {
				t.Fatal(err)
			}
			source := newAccessTestRepo(t, actor, "ancestor", true)
			leaf := newAccessTestRepo(t, actor, "leaf", true)
			leaf.Owner = actor
			ordinary := scenario == "ordinary-member-first"
			if !ordinary {
				if _, err := x.Insert(&ForkRelation{RepositoryID: leaf.Id, SourceID: source.Id, FamilyID: source.Id}); err != nil {
					t.Fatal(err)
				}
			}
			old := newAccessTestRepo(t, org, "existing", true)
			team, err := org.GetOwnerTeam()
			if err != nil {
				t.Fatal(err)
			}
			if err = team.AddRepository(old); err != nil {
				t.Fatal(err)
			}
			// This precondition is deliberately already committed. AddTeamMember's
			// existing AddOrgUser side effect is separate from its team transaction.
			if err = AddOrgUser(org.Id, other.Id); err != nil {
				t.Fatal(err)
			}
			revoke := strings.HasPrefix(scenario, "revoke")
			if scenario != "member-first" && !ordinary {
				if err = AddAccess(&Access{UserId: other.Id, RepoId: source.Id, Mode: READABLE}); err != nil {
					t.Fatal(err)
				}
			}
			if revoke {
				if err = AddTeamMember(org.Id, team.Id, other.Id); err != nil {
					t.Fatal(err)
				}
			}
			mutate := func() error {
				if revoke {
					return DeleteAccess(&Access{UserId: other.Id, RepoId: source.Id})
				}
				return AddTeamMember(org.Id, team.Id, other.Id)
			}
			held := strings.HasSuffix(scenario, "while-authorized")
			target := "transfer-before-transaction"
			if held {
				target = "transfer-authorized"
			}
			hit := false
			result := make(chan error, 1)
			var early *error
			lifecycleTestHook = func(stage string) {
				if stage != target {
					return
				}
				hit = true
				if !held {
					if err := mutate(); err != nil {
						t.Fatal(err)
					}
					return
				}
				started := make(chan struct{})
				go func() { close(started); result <- mutate() }()
				<-started
				// With transfer's SQLite write transaction open, the real competing
				// writer must block or fail. A successful commit here is invalid.
				select {
				case err := <-result:
					early = &err
					t.Logf("competing writer returned before release: %v", err)
					if err == nil {
						t.Error("competing mutation committed through transfer authority")
					}
				case <-time.After(150 * time.Millisecond):
					t.Log("competing writer remained pending until transfer authority was released")
				}
			}
			t.Cleanup(func() { lifecycleTestHook = nil })
			err = TransferOwnership(actor, org.Name, leaf)
			denied := scenario == "member-first" || scenario == "revoke-first"
			if denied {
				if err != ErrForkPolicy {
					t.Fatalf("unsafe transfer: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !hit {
				t.Fatal("authority seam not reached")
			}
			// Finish/retry any SQLite lock loser after the transaction ends. A retry
			// performs the complete real mutation with a fresh team/access snapshot.
			if held {
				var mutationErr error
				if early != nil {
					mutationErr = *early
				} else {
					select {
					case mutationErr = <-result:
					case <-time.After(5 * time.Second):
						t.Fatal("competing model mutation did not finish")
					}
				}
				t.Logf("competing writer outcome after release: %v", mutationErr)
				if mutationErr != nil {
					if err = mutate(); err != nil {
						t.Fatalf("contention retry: %v (first %v)", err, mutationErr)
					}
				}
			}
			current, err := GetRepositoryById(leaf.Id)
			if err != nil {
				t.Fatal(err)
			}
			wantOwner := org.Id
			if denied {
				wantOwner = actor.Id
			}
			if current.OwnerId != wantOwner || !current.IsPrivate {
				t.Fatalf("ownership/privacy %+v", current)
			}
			if !ordinary {
				rel, err := GetForkRelation(leaf.Id)
				if err != nil || rel.SourceID != source.Id || rel.FamilyID != source.Id {
					t.Fatalf("lineage %+v %v", rel, err)
				}
			}
			stored, err := org.GetOwnerTeam()
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 2
			if denied {
				wantCount = 1
			}
			if stored.NumRepos != wantCount || stored.NumMembers != 2 || !strings.Contains(stored.RepoIds, fmt.Sprintf("$%d|", old.Id)) || strings.Contains(stored.RepoIds, fmt.Sprintf("$%d|", leaf.Id)) == denied {
				t.Fatalf("team metadata %+v", stored)
			}
			var ou OrgUser
			if ok, err := x.Where("uid = ? AND org_id = ?", other.Id, org.Id).Get(&ou); err != nil || !ok || !ou.IsOwner || ou.NumTeams != 1 {
				t.Fatalf("membership outcome %+v %v", ou, err)
			}
			reloadedOrg, err := GetUserById(org.Id)
			if err != nil || reloadedOrg.NumMembers != 2 {
				t.Fatalf("AddOrgUser precondition outcome %+v %v", reloadedOrg, err)
			}
			var raw Access
			has, err := x.Where("user_id = ? AND repo_id = ?", other.Id, leaf.Id).Get(&raw)
			if err != nil || has == denied {
				t.Fatalf("transfer grant rollback: has=%v err=%v", has, err)
			}
			a, err := RepositoryAccessFor(other.Id, leaf.Id)
			if err != nil {
				t.Fatal(err)
			}
			wantRead := !denied && !revoke
			if a.Read != wantRead || a.Write != wantRead || a.Admin != wantRead {
				t.Fatalf("current effective access %+v wantRead=%v", a, wantRead)
			}
			if a, err := RepositoryAccessFor(other.Id, old.Id); err != nil || !a.Admin {
				t.Fatalf("existing repository access %+v %v", a, err)
			}
		})
	}
}
