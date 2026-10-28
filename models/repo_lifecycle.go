// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Unknwon/com"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/log"
)

// RepositoryState is where a repository is in its storage lifecycle. Only
// ready repositories are visible to users and transports.
type RepositoryState string

const (
	RepositoryProvisioning RepositoryState = "provisioning"
	RepositoryReady        RepositoryState = "ready"
	RepositoryDeleting     RepositoryState = "deleting"
)

const (
	// stagingDirName is the directory under the repository root, and so on
	// the same volume, where repositories are initialized before being
	// moved to their ID path. Entries are named "<ID>-<StorageNonce>".
	stagingDirName = ".staging"
	// storageMarker, inside .hg, names the allocation that owns a storage
	// directory. Storage is removed only when its marker matches the row.
	storageMarker = "gogs-storage"
)

var ErrNotFreshInstall = errors.New("fresh install required: this release does not upgrade or migrate an existing Gogs installation")

var validRepoName = regexp.MustCompile(`^[\w.-]{1,100}$`)

// lifecycleTestHook lets tests stop the lifecycle at a stage boundary as a
// crash would; it is always nil outside tests.
var lifecycleTestHook func(stage string)

func lifecycleStage(stage string) {
	if lifecycleTestHook != nil {
		lifecycleTestHook(stage)
	}
}

func isValidRepoName(name string) bool {
	return validRepoName.MatchString(name) && name != "." && name != ".." && IsLegalName(name)
}

// ValidateRepositoryRoot checks the operator-configured repository root.
// On Windows it must be ASCII: Mercurial cannot read the lock of a killed
// push under other paths, leaving the repository unwritable.
func ValidateRepositoryRoot(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("repository root %q must be an absolute path", root)
	}
	if runtime.GOOS == "windows" {
		for i := 0; i < len(root); i++ {
			if root[i] >= utf8.RuneSelf {
				return fmt.Errorf("repository root %q must contain only ASCII characters on Windows", root)
			}
		}
	}
	return hg.CheckPath(root)
}

// RepositoryStoragePath returns where the repository of given ID is stored.
func RepositoryStoragePath(root string, repoID int64) (string, error) {
	if repoID <= 0 {
		return "", fmt.Errorf("invalid repository ID %d", repoID)
	}
	if err := ValidateRepositoryRoot(root); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Clean(root), strconv.FormatInt(repoID, 10)), nil
}

func stagingPath(root string, repo *Repository) string {
	return filepath.Join(filepath.Clean(root), stagingDirName, fmt.Sprintf("%d-%s", repo.Id, repo.StorageNonce))
}

func storageMarkerContent(repo *Repository) []byte {
	return []byte(fmt.Sprintf("%d %s\n", repo.Id, repo.StorageNonce))
}

// checkPlainChild verifies that dir, an entry of parent, is a directory and
// not a symbolic link, junction or other reparse point, and that it resolves
// to that entry of parent. Missing dir is reported as fs.ErrNotExist.
func checkPlainChild(parent, dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	// Junctions are ModeIrregular, symbolic links ModeSymlink: both rejected.
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%s is not a plain directory (%v)", dir, fi.Mode().Type())
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if realDir != filepath.Join(realParent, filepath.Base(dir)) {
		return fmt.Errorf("%s resolves outside %s", dir, parent)
	}
	return nil
}

// removeOwnedStorage removes the storage directory of repo if its marker
// matches. A missing directory is not an error; one that is not repo's is.
func removeOwnedStorage(root string, repo *Repository) error {
	path, err := RepositoryStoragePath(root, repo.Id)
	if err != nil {
		return err
	}
	if err = checkPlainChild(root, path); errors.Is(err, fs.ErrNotExist) {
		return removeStaging(root, repo)
	} else if err != nil {
		return err
	}
	dotHg := filepath.Join(path, ".hg")
	if err = checkPlainChild(path, dotHg); err != nil {
		return fmt.Errorf("storage of repository %d is not owned by it: %v", repo.Id, err)
	}
	marker := filepath.Join(dotHg, storageMarker)
	fi, err := os.Lstat(marker)
	if err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("storage of repository %d has no ownership marker: %v", repo.Id, err)
	}
	if b, err := os.ReadFile(marker); err != nil {
		return err
	} else if !bytes.Equal(b, storageMarkerContent(repo)) {
		return fmt.Errorf("storage %s belongs to another allocation", path)
	}

	// A partial RemoveAll could delete the marker and strand a retry, so
	// move the storage to its staging name, which proves ownership, first.
	if err = removeStaging(root, repo); err != nil {
		return err
	} else if _, err = ensureStagingRoot(root); err != nil {
		return err
	} else if err = os.Rename(path, stagingPath(root, repo)); err != nil {
		return err
	}
	return removeStaging(root, repo)
}

func ensureStagingRoot(root string) (string, error) {
	stagingRoot := filepath.Join(filepath.Clean(root), stagingDirName)
	if err := os.MkdirAll(stagingRoot, 0700); err != nil {
		return "", err
	}
	return stagingRoot, checkPlainChild(root, stagingRoot)
}

// removeStaging removes the staging directory of repo, which only this
// allocation can have created since its name carries the row's nonce.
func removeStaging(root string, repo *Repository) error {
	stagingRoot := filepath.Join(filepath.Clean(root), stagingDirName)
	if err := checkPlainChild(root, stagingRoot); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	staging := stagingPath(root, repo)
	if err := checkPlainChild(stagingRoot, staging); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.RemoveAll(staging)
}

type CreateHgRepositoryOptions struct {
	OwnerID           int64
	Name, Description string
	Private           bool
}

// CreateHgRepository creates an empty Mercurial repository. The row stays
// provisioning, and invisible, until the storage is in place; a failure
// removes only what this call allocated.
func CreateHgRepository(ctx context.Context, runner *hg.Runner, root string, opts CreateHgRepositoryOptions) (*Repository, error) {
	if err := ValidateRepositoryRoot(root); err != nil {
		return nil, err
	}
	if !isValidRepoName(opts.Name) {
		return nil, ErrRepoNameIllegal
	}
	u, err := GetUserById(opts.OwnerID)
	if err != nil {
		return nil, err
	}
	if has, err := IsRepositoryExist(u, opts.Name); err != nil {
		return nil, err
	} else if has {
		return nil, ErrRepoAlreadyExist
	}

	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	if len(opts.Description) > 255 {
		opts.Description = opts.Description[:255]
	}
	repo := &Repository{
		OwnerId:      u.Id,
		Owner:        u,
		Name:         opts.Name,
		LowerName:    strings.ToLower(opts.Name),
		Description:  opts.Description,
		IsPrivate:    opts.Private,
		State:        RepositoryProvisioning,
		StorageNonce: hex.EncodeToString(nonce),
	}
	if _, err = x.Insert(repo); err != nil {
		return nil, err
	}
	lifecycleStage("allocated")

	if err = provisionStorage(ctx, runner, root, repo); err == nil {
		lifecycleStage("moved")
		err = publishRepository(u, repo)
	}
	if err != nil {
		if err2 := discardProvisioning(root, repo); err2 != nil {
			log.Error(4, "Clean up failed creation of repository %d: %v", repo.Id, err2)
		}
		return nil, err
	}
	return repo, nil
}

func provisionStorage(ctx context.Context, runner *hg.Runner, root string, repo *Repository) error {
	final, err := RepositoryStoragePath(root, repo.Id)
	if err != nil {
		return err
	}
	stagingRoot, err := ensureStagingRoot(root)
	if err != nil {
		return err
	}
	staging := stagingPath(root, repo)
	if err = hg.CheckPath(staging); err != nil {
		return err
	}
	// init creates the store only; there is never a working copy.
	if err = runner.Run(ctx, "", []string{"init", staging}, nil, nil); err != nil {
		return err
	}
	if err = checkPlainChild(stagingRoot, staging); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(staging, ".hg", storageMarker), storageMarkerContent(repo), 0600); err != nil {
		return err
	}
	lifecycleStage("initialized")

	// Rename would replace an empty directory on Unix.
	if _, err = os.Lstat(final); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage path %s already exists: %v", final, err)
	}
	return os.Rename(staging, final)
}

// publishRepository makes a provisioned repository ready together with the
// owner's access rows and counters, which only ready repositories have.
func publishRepository(u *User, repo *Repository) (err error) {
	var t *Team
	members := []*User{u}
	if u.IsOrganization() {
		if t, err = u.GetOwnerTeam(); err != nil {
			return err
		} else if err = t.GetMembers(); err != nil {
			return err
		}
		members = t.Members
	}

	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}
	if err = setRepositoryState(sess, repo.Id, RepositoryProvisioning, RepositoryReady); err != nil {
		sess.Rollback()
		return err
	}
	for _, m := range members {
		if _, err = sess.Insert(&Access{UserId: m.Id, RepoId: repo.Id, Mode: WRITABLE}); err != nil {
			sess.Rollback()
			return err
		}
	}
	if _, err = sess.Exec("UPDATE `user` SET num_repos = num_repos + 1 WHERE id = ?", u.Id); err != nil {
		sess.Rollback()
		return err
	}
	if t != nil {
		t.RepoIds += "$" + com.ToStr(repo.Id) + "|"
		t.NumRepos++
		if _, err = sess.Id(t.Id).AllCols().Update(t); err != nil {
			sess.Rollback()
			return err
		}
	}
	if err = sess.Commit(); err != nil {
		return err
	}
	repo.State = RepositoryReady

	for _, m := range members {
		if err = WatchRepo(m.Id, repo.Id, true); err != nil {
			log.Error(4, "WatchRepo: %v", err)
		}
	}
	if err = NewRepoAction(u, repo); err != nil {
		log.Error(4, "NewRepoAction: %v", err)
	}
	return nil
}

func setRepositoryState(e Engine, repoID int64, from, to RepositoryState) error {
	res, err := e.Exec("UPDATE `repository` SET state = ? WHERE id = ? AND state = ?", to, repoID, from)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return fmt.Errorf("repository %d is no longer %s", repoID, from)
	}
	return nil
}

// discardProvisioning removes an allocation that never became ready.
// Storage at its ID path that is not marked as this allocation's is left.
func discardProvisioning(root string, repo *Repository) error {
	if claimed, err := forkReserved(x, repo.Id); err != nil {
		return err
	} else if claimed {
		return ErrForkPolicy
	}

	if err := removeStaging(root, repo); err != nil {
		return err
	}
	if err := removeOwnedStorage(root, repo); err != nil {
		log.Warn("Leaving storage of unfinished repository %d in place: %v", repo.Id, err)
	}
	_, err := x.Where("id = ?", repo.Id).And("state = ?", RepositoryProvisioning).Delete(new(Repository))
	return err
}

// DrainRepository ends all sessions of a repository and keeps new ones from
// starting before it returns.
type DrainRepository func(context.Context, int64) error

// DeleteHgRepository deletes a ready repository: it is hidden first, its
// sessions drained, its storage removed, then its rows. Any failure leaves
// it hidden in the deleting state, and calling again resumes.
func DeleteHgRepository(ctx context.Context, root string, repoID int64, drain DrainRepository, cleanup ...RepositoryCollaborationCleanup) error {
	if _, err := RepositoryStoragePath(root, repoID); err != nil {
		return err
	}
	repo := new(Repository)
	if has, err := x.Id(repoID).Get(repo); err != nil {
		return err
	} else if !has {
		return ErrRepoNotExist
	}
	lifecycleStage("loaded")
	switch repo.State {
	case RepositoryReady:
		if err := tombstoneReviewSafe(repoID); err != nil {
			return err
		}
	case RepositoryDeleting:
	default:
		return fmt.Errorf("repository %d is %q and cannot be deleted", repoID, repo.State)
	}
	lifecycleStage("tombstoned")

	// No database transaction is open while sessions drain.
	if err := drain(ctx, repoID); err != nil {
		return fmt.Errorf("drain repository %d: %v", repoID, err)
	}
	lifecycleStage("drained")
	return finishDeletion(ctx, root, repoID, cleanup...)
}

func finishDeletion(ctx context.Context, root string, repoID int64, cleanup ...RepositoryCollaborationCleanup) error {
	rows, err := RepositoryPullDeletion(ctx, repoID)
	if err != nil {
		return err
	}
	if len(rows) > 0 && (len(cleanup) != 1 || cleanup[0] == nil) {
		return ErrReviewReserved
	}
	if reserved, err := RepositoryReviewReserved(repoID); err != nil {
		return err
	} else if reserved {
		return ErrReviewReserved
	}
	// Re-read after the tombstone: a transfer may have committed after the
	// caller loaded the row, and none can commit once it is deleting.
	repo := new(Repository)
	if has, err := x.Id(repoID).Get(repo); err != nil {
		return err
	} else if !has || repo.State != RepositoryDeleting {
		return fmt.Errorf("repository %d is no longer being deleted", repoID)
	}
	if err := removeOwnedStorage(root, repo); err != nil {
		return err
	}
	lifecycleStage("storage-removed")
	if len(cleanup) == 1 && cleanup[0] != nil {
		if err := cleanup[0](ctx, repoID); err != nil {
			return err
		}
	}
	lifecycleStage("collaboration-cleaned")
	return deleteRepositoryRows(repo)
}

// ReconcileHgRepositories finishes or undoes lifecycle operations a crash
// interrupted. It must run before transports start, since it completes
// deletions without draining. Nothing it cannot prove it owns is removed;
// unknown entries in the root are logged.
func ReconcileHgRepositories(ctx context.Context, root string, cleanup ...RepositoryCollaborationCleanup) error {
	if err := ValidateRepositoryRoot(root); err != nil {
		return err
	}
	repos := make([]*Repository, 0, 10)
	if err := x.Find(&repos); err != nil {
		return err
	}

	var errs []error
	known := make(map[string]bool, len(repos))
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch repo.State {
		case RepositoryProvisioning:
			if err := discardProvisioning(root, repo); err != nil {
				errs = append(errs, fmt.Errorf("discard unfinished repository %d: %v", repo.Id, err))
			}
		case RepositoryDeleting:
			if err := finishDeletion(ctx, root, repo.Id, cleanup...); err != nil {
				errs = append(errs, fmt.Errorf("finish deleting repository %d: %v", repo.Id, err))
			}
		case RepositoryReady:
			known[strconv.FormatInt(repo.Id, 10)] = true
			if path, _ := RepositoryStoragePath(root, repo.Id); checkPlainChild(root, path) != nil {
				log.Error(4, "Storage of repository %d is missing or invalid: %s", repo.Id, path)
			}
		default:
			log.Warn("Repository %d has unknown state %q; left unchanged", repo.Id, repo.State)
		}
	}

	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.Join(errs...)
	} else if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != stagingDirName && !known[e.Name()] {
			log.Warn("Unknown entry %s in repository root left in place", filepath.Join(root, e.Name()))
		}
	}
	if staged, err := os.ReadDir(filepath.Join(root, stagingDirName)); err == nil {
		for _, e := range staged {
			log.Warn("Unknown staging entry %s left in place", filepath.Join(root, stagingDirName, e.Name()))
		}
	}
	return errors.Join(errs...)
}

// CheckFreshInstall rejects a database that already holds data or a
// repository root that is not empty, without changing either.
func CheckFreshInstall(root string) error {
	e, err := getEngine()
	if err != nil {
		return err
	}
	defer e.Close()
	tables, err := e.DBMetas()
	if err != nil {
		return err
	}
	for _, t := range tables {
		var one int
		err := e.DB().QueryRow("SELECT 1 FROM " + e.Quote(t.Name) + " LIMIT 1").Scan(&one)
		if err == nil {
			return fmt.Errorf("%w: database table %s already contains data", ErrNotFreshInstall, t.Name)
		} else if err != sql.ErrNoRows {
			return err
		}
	}

	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%w: repository root %s is not empty", ErrNotFreshInstall, root)
	}
	return nil
}
