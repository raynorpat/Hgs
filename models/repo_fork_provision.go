package models

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
)

// ForkProvisioning is allocation identity, never a reusable permission grant.
// Its unique source claim is coordinated with review reservations and lifecycle
// mutations by locking ready repository rows before either kind is inserted.
type ForkProvisioning struct {
	ID            string `xorm:"pk 'id' VARCHAR(64)"`
	ActorID       int64
	SourceID      int64  `xorm:"unique 'source_id'"`
	RepositoryID  int64  `xorm:"unique 'repository_id'"`
	StorageNonce  string `xorm:"VARCHAR(32)"`
	SourceOwnerID int64
	SourcePrivate bool
	Failed        bool
}

func PendingForkProvisioning() ([]ForkProvisioning, error) {
	var rows []ForkProvisioning
	err := x.Find(&rows)
	return rows, err
}
func forkReserved(e Engine, id int64) (bool, error) {
	return e.Where("source_id = ? OR repository_id = ?", id, id).Get(new(ForkProvisioning))
}
func forkOwner(e Engine, actor, ownerID int64, locked bool) (*User, error) {
	if _, err := enabledPolicyAccount(e, actor, locked); err != nil {
		return nil, err
	}
	var owner User
	ok, err := policyGet(e, locked, "user", "id = ?", &owner, ownerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredential
	}
	if ownerID == actor {
		return &owner, nil
	}
	if !owner.IsOrganization() {
		return nil, ErrInvalidCredential
	}
	ok, err = policyGet(e, locked, "org_user", "uid = ? AND org_id = ? AND is_owner = ?", new(OrgUser), actor, ownerID, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredential
	}
	return &owner, nil
}
func ForkOwnerAllowed(actor, owner int64) error {
	_, err := forkOwner(x, actor, owner, false)
	return err
}

func lockForkSource(sess Engine, sourceID int64) ([]int64, error) {
	ancestors, err := forkAncestors(sess, sourceID, true)
	if err != nil {
		return nil, err
	}
	ids := append([]int64{sourceID}, ancestors...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if err := lockReadyReviewRepository(sess.(*xorm.Session), id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func BeginForkProvisioning(actor, sourceID int64, opts CreateHgRepositoryOptions) (ForkProvisioning, *Repository, error) {
	var op ForkProvisioning
	if !isValidRepoName(opts.Name) {
		return op, nil, ErrRepoNameIllegal
	}
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return op, nil, err
	}
	defer sess.Rollback()
	// SQLite must take the writer lock before any read snapshot.
	if DbCfg.Type == "sqlite3" {
		if err := lockReadyReviewRepository(sess, sourceID); err != nil {
			return op, nil, err
		}
	}
	if _, err := lockForkSource(sess, sourceID); err != nil {
		return op, nil, err
	}
	if reserved, err := sess.Id(sourceID).Get(new(ReviewReservation)); err != nil {
		return op, nil, err
	} else if reserved {
		return op, nil, ErrReviewReserved
	}
	if reserved, err := forkReserved(sess, sourceID); err != nil {
		return op, nil, err
	} else if reserved {
		return op, nil, ErrForkPolicy
	}
	access, err := repositoryAccessFor(sess, actor, sourceID, true)
	if err != nil {
		return op, nil, err
	}
	if !access.Read {
		return op, nil, ErrInvalidCredential
	}
	owner, err := forkOwner(sess, actor, opts.OwnerID, true)
	if err != nil {
		return op, nil, err
	}
	var source Repository
	if _, err = policyGet(sess, true, "repository", "id = ?", &source, sourceID); err != nil {
		return op, nil, err
	}
	if has, err := sess.Where("owner_id = ? AND lower_name = ?", owner.Id, strings.ToLower(opts.Name)).Get(new(Repository)); err != nil {
		return op, nil, err
	} else if has {
		return op, nil, ErrRepoAlreadyExist
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return op, nil, err
	}
	if len(opts.Description) > 255 {
		opts.Description = opts.Description[:255]
	}
	repo := &Repository{OwnerId: owner.Id, Owner: owner, Name: opts.Name, LowerName: strings.ToLower(opts.Name), Description: opts.Description, IsPrivate: source.IsPrivate, State: RepositoryProvisioning, StorageNonce: hex.EncodeToString(nonce)}
	if _, err = sess.Insert(repo); err != nil {
		return op, nil, err
	}
	op = ForkProvisioning{ID: "fork-" + repo.StorageNonce, ActorID: actor, SourceID: sourceID, RepositoryID: repo.Id, StorageNonce: repo.StorageNonce, SourceOwnerID: source.OwnerId, SourcePrivate: source.IsPrivate}
	family := sourceID
	var parent ForkRelation
	if has, err := sess.Id(sourceID).Get(&parent); err != nil {
		return op, nil, err
	} else if has {
		family = parent.FamilyID
	}
	if _, err = sess.Insert(&ForkRelation{RepositoryID: repo.Id, SourceID: sourceID, FamilyID: family}, &op); err != nil {
		return op, nil, err
	}
	if err = sess.Commit(); err != nil {
		return op, nil, err
	}
	return op, repo, nil
}

// PrepareForkStorage only writes the invisible allocation. Source recovery is
// deliberately absent: a reader cannot mutate an upstream journal or lock.
func PrepareForkStorage(ctx context.Context, r *hg.Runner, root string, op ForkProvisioning) error {
	repo, err := forkAllocation(op)
	if err != nil {
		return err
	}
	source, err := RepositoryStoragePath(root, op.SourceID)
	if err != nil {
		return err
	}
	final, err := RepositoryStoragePath(root, repo.Id)
	if err != nil {
		return err
	}
	stagingRoot, err := ensureStagingRoot(root)
	if err != nil {
		return err
	}
	staging := stagingPath(root, repo)
	if err = r.CloneVisible(ctx, source, staging); err != nil {
		return err
	}
	lifecycleStage("fork-copied")
	if err = checkPlainChild(stagingRoot, staging); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(staging, ".hg", storageMarker), storageMarkerContent(repo), 0600); err != nil {
		return err
	}
	lifecycleStage("fork-initialized")
	if _, err = os.Lstat(final); !os.IsNotExist(err) {
		return fmt.Errorf("fork destination already exists: %v", err)
	}
	if err = os.Rename(staging, final); err != nil {
		return err
	}
	lifecycleStage("fork-moved")
	return nil
}
func forkAllocation(op ForkProvisioning) (*Repository, error) {
	var current ForkProvisioning
	ok, err := x.Id(op.ID).Get(&current)
	if err != nil {
		return nil, err
	}
	if !ok || current != op || op.SourceID <= 0 || op.RepositoryID <= 0 || op.SourceID == op.RepositoryID || len(op.StorageNonce) != 32 || op.ID != "fork-"+op.StorageNonce {
		return nil, ErrForkPolicy
	}
	if _, err = hex.DecodeString(op.StorageNonce); err != nil {
		return nil, ErrForkPolicy
	}
	repo := new(Repository)
	ok, err = x.Id(op.RepositoryID).Get(repo)
	if err != nil {
		return nil, err
	}
	if !ok || repo.State != RepositoryProvisioning || repo.StorageNonce != op.StorageNonce {
		return nil, ErrForkPolicy
	}
	var rel ForkRelation
	ok, err = x.Id(repo.Id).Get(&rel)
	if err != nil {
		return nil, err
	}
	if !ok || rel.SourceID != op.SourceID {
		return nil, ErrForkPolicy
	}
	return repo, nil
}

// PublishFork checks live policy in the same short transaction as readiness.
// Current locking reads pin all facts granting authority until commit. Native
// preparation and cleanup never run inside this transaction.
func PublishFork(ctx context.Context, root string, op ForkProvisioning) (*Repository, error) {
	repo, err := forkAllocation(op)
	if err != nil {
		return nil, err
	}
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return nil, err
	}
	defer sess.Rollback()
	if DbCfg.Type == "sqlite3" {
		if err = lockReadyReviewRepository(sess, op.SourceID); err != nil {
			return nil, err
		}
	}
	if _, err = lockForkSource(sess, op.SourceID); err != nil {
		return nil, err
	}
	var current ForkProvisioning
	ok, err := policyGet(sess, true, "fork_provisioning", "id = ?", &current, op.ID)
	if err != nil {
		return nil, err
	}
	if !ok || current != op || current.Failed {
		return nil, ErrForkPolicy
	}
	var source Repository
	ok, err = policyGet(sess, true, "repository", "id = ? AND state = ?", &source, op.SourceID, RepositoryReady)
	if err != nil {
		return nil, err
	}
	if !ok || source.OwnerId != op.SourceOwnerID || source.IsPrivate != op.SourcePrivate {
		return nil, ErrForkPolicy
	}
	access, err := repositoryAccessFor(sess, op.ActorID, op.SourceID, true)
	if err != nil {
		return nil, err
	}
	if !access.Read {
		return nil, ErrInvalidCredential
	}
	owner, err := forkOwner(sess, op.ActorID, repo.OwnerId, true)
	if err != nil {
		return nil, err
	}
	// Reload allocation under lock; never use stale mutable owner/privacy metadata.
	var allocation Repository
	ok, err = policyGet(sess, true, "repository", "id = ? AND state = ?", &allocation, repo.Id, RepositoryProvisioning)
	if err != nil {
		return nil, err
	}
	if !ok || allocation.StorageNonce != op.StorageNonce || allocation.OwnerId != owner.Id || allocation.IsPrivate != source.IsPrivate {
		return nil, ErrForkPolicy
	}
	members := []int64{owner.Id}
	var team Team
	if owner.IsOrganization() {
		ok, err = policyGet(sess, true, "team", "org_id = ? AND lower_name = ?", &team, owner.Id, strings.ToLower(OWNER_TEAM))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrForkPolicy
		}
		var rows []OrgUser
		if DbCfg.Type == "sqlite3" {
			err = sess.Where("org_id = ? AND is_owner = ?", owner.Id, true).Find(&rows)
		} else {
			err = sess.Sql("SELECT * FROM org_user WHERE org_id = ? AND is_owner = ? ORDER BY uid FOR UPDATE", owner.Id, true).Find(&rows)
		}
		if err != nil {
			return nil, err
		}
		members = nil
		for _, row := range rows {
			members = append(members, row.Uid)
		}
	}
	if err = checkPreparedForkStorage(root, repo); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = setRepositoryState(sess, repo.Id, RepositoryProvisioning, RepositoryReady); err != nil {
		return nil, err
	}
	for _, id := range members {
		if _, err = sess.Insert(&Access{UserId: id, RepoId: repo.Id, Mode: WRITABLE}); err != nil {
			return nil, err
		}
	}
	if _, err = sess.Exec("UPDATE `user` SET num_repos = num_repos + 1 WHERE id = ?", owner.Id); err != nil {
		return nil, err
	}
	if owner.IsOrganization() {
		team.RepoIds += fmt.Sprintf("$%d|", repo.Id)
		team.NumRepos++
		if _, err = sess.Id(team.Id).Cols("repo_ids", "num_repos").Update(&team); err != nil {
			return nil, err
		}
	}
	if _, err = sess.Id(op.ID).Delete(new(ForkProvisioning)); err != nil {
		return nil, err
	}
	if err = sess.Commit(); err != nil {
		return nil, err
	}
	repo.State = RepositoryReady
	repo.Owner = owner
	return repo, nil
}

func RollbackForkProvisioning(root string, op ForkProvisioning) error {
	repo, err := forkAllocation(op)
	if err != nil {
		return err
	}
	if err = removeStaging(root, repo); err != nil {
		return err
	}
	// Unknown final storage is preserved and the typed claim retained for operator
	// inspection; a restart must never guess or erase another allocation.
	if err = removeOwnedStorage(root, repo); err != nil {
		return err
	}
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	if _, err = sess.Id(repo.Id).And("state = ? AND storage_nonce = ?", RepositoryProvisioning, op.StorageNonce).Delete(new(Repository)); err != nil {
		return err
	}
	if _, err = sess.Id(repo.Id).Delete(new(ForkRelation)); err != nil {
		return err
	}
	if _, err = sess.Id(op.ID).Delete(new(ForkProvisioning)); err != nil {
		return err
	}
	return sess.Commit()
}

func ForkRepositoryStatus(ctx context.Context, actor, repoID int64) (RepositoryState, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var repo Repository
	has, err := x.Id(repoID).Get(&repo)
	if err != nil {
		return "", err
	}
	if !has {
		return "", ErrRepoNotExist
	}
	if repo.State == RepositoryReady {
		a, err := RepositoryAccessFor(actor, repoID)
		if err != nil {
			return "", err
		}
		if !a.Read {
			return "", ErrRepoNotExist
		}
		return repo.State, nil
	}
	var op ForkProvisioning
	has, err = x.Where("repository_id = ?", repoID).Get(&op)
	if err != nil {
		return "", err
	}
	if !has || op.Failed || actor != op.ActorID {
		return "", ErrRepoNotExist
	}
	a, err := RepositoryAccessFor(actor, op.SourceID)
	if err != nil || !a.Read {
		return "", ErrRepoNotExist
	}
	if err = ForkOwnerAllowed(actor, repo.OwnerId); err != nil {
		return "", ErrRepoNotExist
	}
	return repo.State, nil
}

// FailForkProvisioning reports a stopped worker while retaining uncertain owned
// cleanup for startup/operator reconciliation. This is an outcome, not a grant.
func FailForkProvisioning(op ForkProvisioning) error {
	_, err := x.Id(op.ID).And("repository_id = ? AND storage_nonce = ?", op.RepositoryID, op.StorageNonce).Cols("failed").Update(&ForkProvisioning{Failed: true})
	return err
}

// The marker is written only after native CloneVisible completes and verifies.
// Final readiness cannot be called on an allocation with no completed store.
func checkPreparedForkStorage(root string, repo *Repository) error {
	path, err := RepositoryStoragePath(root, repo.Id)
	if err != nil {
		return err
	}
	if err = checkPlainChild(root, path); err != nil {
		return err
	}
	dotHg := filepath.Join(path, ".hg")
	if err = checkPlainChild(path, dotHg); err != nil {
		return err
	}
	if err = checkPlainChild(dotHg, filepath.Join(dotHg, "store")); err != nil {
		return err
	}
	marker := filepath.Join(dotHg, storageMarker)
	info, err := os.Lstat(marker)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 128 {
		return ErrForkPolicy
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, storageMarkerContent(repo)) {
		return ErrForkPolicy
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != ".hg" {
		return ErrForkPolicy
	}
	return hg.CheckReviewPublicationQuiescent(path)
}
