package models

import (
	"context"
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
