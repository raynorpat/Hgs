// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
)

// TestMain doubles as a fake Mercurial executable: the runner invokes the test
// binary with hg-style arguments instead of -test.* flags.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(fakeHg(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeHg implements just "hg init <path>": the store layout without a
// working copy, as Mercurial creates it.
func fakeHg(args []string) int {
	if len(args) != 2 || args[0] != "init" {
		fmt.Fprintf(os.Stderr, "fake hg: unsupported arguments %q\n", args)
		return 255
	}
	if _, err := os.Lstat(args[1]); err == nil {
		fmt.Fprintf(os.Stderr, "abort: repository %s already exists!\n", args[1])
		return 255
	}
	if err := os.MkdirAll(filepath.Join(args[1], ".hg", "store"), 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 255
	}
	if err := os.WriteFile(filepath.Join(args[1], ".hg", "requires"), []byte("revlogv1\nstore\n"), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 255
	}
	return 0
}

func newFakeHgRunner(t *testing.T) *hg.Runner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// crashAt makes the lifecycle stop dead at stage, as a killed process would:
// no error path or deferred cleanup runs.
type lifecycleCrash string

func crashAt(t *testing.T, stage string) {
	t.Helper()
	lifecycleTestHook = func(s string) {
		if s == stage {
			panic(lifecycleCrash(s))
		}
	}
	t.Cleanup(func() { lifecycleTestHook = nil })
}

func runUntilCrash(t *testing.T, stage string, f func()) {
	t.Helper()
	crashed := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(lifecycleCrash); !ok {
					panic(r)
				}
				crashed = true
			}
		}()
		f()
	}()
	lifecycleTestHook = nil
	if !crashed {
		t.Fatalf("lifecycle never reached stage %q", stage)
	}
}

func newLifecycleRoot(t *testing.T) string {
	t.Helper()
	root := setting.RepoRootPath
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func createTestHgRepo(t *testing.T, r *hg.Runner, root string, owner *User, name string) *Repository {
	t.Helper()
	repo, err := CreateHgRepository(context.Background(), r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: name, Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func storagePath(t *testing.T, root string, id int64) string {
	t.Helper()
	p, err := RepositoryStoragePath(root, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

// assertHidden checks that no user-facing lookup, list or transport
// authorization can see a repository that is not ready.
func assertHidden(t *testing.T, owner *User, id int64, name string) {
	t.Helper()
	if _, err := GetRepositoryByName(owner.Id, name); err != ErrRepoNotExist {
		t.Errorf("GetRepositoryByName(%s): %v, want ErrRepoNotExist", name, err)
	}
	if _, err := GetRepositoryById(id); err != ErrRepoNotExist {
		t.Errorf("GetRepositoryById(%d): %v, want ErrRepoNotExist", id, err)
	}
	if _, err := RepositoryAccessFor(owner.Id, id); err != ErrRepoNotExist {
		t.Errorf("RepositoryAccessFor(%d): %v, want ErrRepoNotExist", id, err)
	}
	repos, err := GetRepositories(owner.Id, true)
	if err != nil {
		t.Fatal(err)
	}
	found, err := SearchRepositoryByName(SearchOption{Keyword: name, Limit: 10, Private: true})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := GetRecentUpdatedRepositories(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range append(append(repos, found...), recent...) {
		if r.Id == id {
			t.Errorf("repository %d listed while not ready", id)
		}
	}
}

func TestRepositoryProvisioningRecovery(t *testing.T) {
	reload := newTestDatabase(t)
	root := newLifecycleRoot(t)
	r := newFakeHgRunner(t)
	ctx := context.Background()
	owner := newAccessTestUser(t, "alice", true)

	repo := createTestHgRepo(t, r, root, owner, "demo")
	if repo.State != RepositoryReady {
		t.Fatalf("state %q, want ready", repo.State)
	}
	if got, err := GetRepositoryByName(owner.Id, "demo"); err != nil || got.Id != repo.Id {
		t.Fatalf("lookup: %+v, %v", got, err)
	}
	if got, err := RepositoryAccessFor(owner.Id, repo.Id); err != nil || !got.Admin {
		t.Fatalf("owner access: %+v, %v", got, err)
	}
	if u, _ := GetUserById(owner.Id); u.NumRepos != 1 {
		t.Fatalf("num_repos %d, want 1", u.NumRepos)
	}
	if names := dirNames(t, storagePath(t, root, repo.Id)); len(names) != 1 || names[0] != ".hg" {
		t.Fatalf("storage holds %q, want only .hg", names)
	}
	if names := dirNames(t, filepath.Join(root, stagingDirName)); len(names) != 0 {
		t.Fatalf("staging left %q", names)
	}
	if _, err := CreateHgRepository(ctx, r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "DEMO"}); err != ErrRepoAlreadyExist {
		t.Fatalf("duplicate name: %v", err)
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "new", "x.git", strings.Repeat("a", 101)} {
		if _, err := CreateHgRepository(ctx, r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: name}); err != ErrRepoNameIllegal {
			t.Errorf("name %q: %v, want ErrRepoNameIllegal", name, err)
		}
	}

	// Directories reconciliation does not own must survive every pass.
	unrelated := []string{
		filepath.Join(root, "999"),
		filepath.Join(root, "notes"),
		filepath.Join(root, stagingDirName, "42-0123456789abcdef0123456789abcdef"),
	}
	for _, d := range unrelated {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// An unmarked .hg in a numeric directory is still not ours.
	if err := os.MkdirAll(filepath.Join(root, "998", ".hg"), 0700); err != nil {
		t.Fatal(err)
	}
	unrelated = append(unrelated, filepath.Join(root, "998"))

	for i, stage := range []string{"allocated", "initialized", "moved"} {
		name := fmt.Sprintf("crash%d", i)
		crashAt(t, stage)
		runUntilCrash(t, stage, func() {
			CreateHgRepository(ctx, r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: name})
		})

		half := &Repository{OwnerId: owner.Id, LowerName: name}
		if has, err := x.Get(half); err != nil || !has || half.State != RepositoryProvisioning {
			t.Fatalf("%s: row %+v, %v, %v", stage, half, has, err)
		}
		assertHidden(t, owner, half.Id, name)
		staged := filepath.Join(root, stagingDirName, fmt.Sprintf("%d-%s", half.Id, half.StorageNonce))
		final := storagePath(t, root, half.Id)
		if want := stage == "initialized"; exists(staged) != want {
			t.Fatalf("%s: staged directory exists = %v", stage, !want)
		}
		if want := stage == "moved"; exists(final) != want {
			t.Fatalf("%s: storage directory exists = %v", stage, !want)
		}

		reload()
		for pass := 0; pass < 2; pass++ {
			if err := ReconcileHgRepositories(ctx, root); err != nil {
				t.Fatalf("%s: reconcile pass %d: %v", stage, pass, err)
			}
		}
		if has, err := x.Id(half.Id).Get(new(Repository)); err != nil || has {
			t.Fatalf("%s: half-created row survived reconciliation (%v)", stage, err)
		}
		if exists(staged) || exists(final) {
			t.Fatalf("%s: half-created storage survived reconciliation", stage)
		}
		for _, d := range unrelated {
			if !exists(d) {
				t.Fatalf("%s: reconciliation removed unrelated %s", stage, d)
			}
		}
		if _, err := GetRepositoryByName(owner.Id, "demo"); err != nil || !exists(storagePath(t, root, repo.Id)) {
			t.Fatalf("%s: reconciliation touched the ready repository: %v", stage, err)
		}
	}

	// A failure after init cleans up its own allocation right away, but never
	// a directory it does not own.
	var squatter string
	lifecycleTestHook = func(s string) {
		if s != "initialized" {
			return
		}
		half := &Repository{OwnerId: owner.Id, LowerName: "squatted"}
		x.Get(half)
		squatter = storagePath(t, root, half.Id)
		os.MkdirAll(filepath.Join(squatter, ".hg"), 0700)
	}
	_, err := CreateHgRepository(ctx, r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "squatted"})
	lifecycleTestHook = nil
	if err == nil {
		t.Fatal("create succeeded over an existing storage directory")
	}
	if has, _ := x.Get(&Repository{OwnerId: owner.Id, LowerName: "squatted"}); has {
		t.Fatal("failed create left its row")
	}
	if !exists(squatter) {
		t.Fatal("failed create removed a directory it did not own")
	}
	if names := dirNames(t, filepath.Join(root, stagingDirName)); len(names) != 1 {
		t.Fatalf("failed create left staging entries %q", names)
	}
	if u, _ := GetUserById(owner.Id); u.NumRepos != 1 {
		t.Fatalf("num_repos %d after failures, want 1", u.NumRepos)
	}
}

func TestRepositoryRenameKeepsStorageAndIssues(t *testing.T) {
	newTestDatabase(t)
	root := newLifecycleRoot(t)
	r := newFakeHgRunner(t)
	owner := newAccessTestUser(t, "alice", true)
	collaborator := newAccessTestUser(t, "bob", true)
	newOwner := newAccessTestUser(t, "carol", true)

	repo, err := CreateHgRepository(context.Background(), r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "demo", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	createTestHgRepo(t, r, root, owner, "taken")
	if err := AddAccess(&Access{UserId: collaborator.Id, RepoId: repo.Id, Mode: WRITABLE}); err != nil {
		t.Fatal(err)
	}
	issue := &Issue{RepoId: repo.Id, Index: 1, Name: "broken", PosterId: owner.Id}
	if err := NewIssue(issue); err != nil {
		t.Fatal(err)
	}
	beforeID, beforePath := repo.Id, storagePath(t, root, repo.Id)
	var beforeAccess []*Access
	if err := x.Asc("id").Find(&beforeAccess, &Access{RepoId: repo.Id}); err != nil {
		t.Fatal(err)
	}

	if err := ChangeRepositoryName(repo.Id, "taken"); err != ErrRepoAlreadyExist {
		t.Fatalf("rename onto existing name: %v", err)
	}
	if err := ChangeRepositoryName(repo.Id, "new"); err != ErrRepoNameIllegal {
		t.Fatalf("rename to reserved name: %v", err)
	}
	if err := ChangeRepositoryName(repo.Id, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetRepositoryByName(owner.Id, "demo"); err != ErrRepoNotExist {
		t.Fatalf("old name still resolves: %v", err)
	}
	var renamed *Repository
	renamed, err = GetRepositoryByName(owner.Id, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Renamed" {
		t.Fatalf("name %q, want Renamed", renamed.Name)
	}
	var afterAccess []*Access
	if err = x.Asc("id").Find(&afterAccess, &Access{RepoId: repo.Id}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(accessKeys(beforeAccess)) != fmt.Sprint(accessKeys(afterAccess)) {
		t.Fatalf("rename touched access rows: %v -> %v", accessKeys(beforeAccess), accessKeys(afterAccess))
	}

	renamed.Owner = owner
	if err = TransferOwnership(owner, newOwner.Name, renamed); err != nil {
		t.Fatal(err)
	}
	after, err := GetRepositoryByName(newOwner.Id, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	afterID, afterPath := after.Id, storagePath(t, root, after.Id)
	if issue, err = GetIssueById(issue.Id); err != nil {
		t.Fatal(err)
	}
	if beforePath != afterPath || beforeID != afterID || issue.RepoId != afterID {
		t.Fatal("rename/transfer moved storage or detached issues")
	}
	if !exists(filepath.Join(afterPath, ".hg", storageMarker)) {
		t.Fatal("storage marker missing after transfer")
	}
	if after.State != RepositoryReady || after.StorageNonce != repo.StorageNonce {
		t.Fatalf("transfer changed lifecycle fields: %q %q", after.State, after.StorageNonce)
	}
	for _, c := range []struct {
		name   string
		userID int64
		want   RepositoryAccess
	}{
		{"new owner", newOwner.Id, RepositoryAccess{true, true, true}},
		{"previous owner", owner.Id, RepositoryAccess{}},
		{"collaborator", collaborator.Id, RepositoryAccess{Read: true, Write: true}},
	} {
		if got, err := RepositoryAccessFor(c.userID, afterID); err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v, want %+v", c.name, got, err, c.want)
		}
	}

	// Renaming accounts moves nothing on disk either.
	if err = ChangeUserName(newOwner, "dave"); err != nil {
		t.Fatal(err)
	}
	if !exists(afterPath) || exists(filepath.Join(root, "carol")) || exists(filepath.Join(root, "dave")) {
		t.Fatal("account rename touched repository storage")
	}
}

func accessKeys(as []*Access) []string {
	keys := make([]string, len(as))
	for i, a := range as {
		keys[i] = fmt.Sprintf("%d:%d:%d:%d", a.Id, a.UserId, a.RepoId, a.Mode)
	}
	return keys
}

type fakeDrain struct {
	t     *testing.T
	root  string
	owner *User
	calls []int64
	err   error
}

func (d *fakeDrain) drain(_ context.Context, id int64) error {
	d.calls = append(d.calls, id)
	row := new(Repository)
	if has, err := x.Id(id).Get(row); err != nil || !has || row.State != RepositoryDeleting {
		d.t.Errorf("drain ran before tombstone: %+v, %v, %v", row, has, err)
	}
	assertHidden(d.t, d.owner, id, row.LowerName)
	if !exists(filepath.Join(storagePath(d.t, d.root, id), ".hg")) {
		d.t.Errorf("storage removed before drain")
	}
	return d.err
}

func TestRepositoryDeleteDrainsFirst(t *testing.T) {
	reload := newTestDatabase(t)
	root := newLifecycleRoot(t)
	r := newFakeHgRunner(t)
	ctx := context.Background()
	owner := newAccessTestUser(t, "alice", true)
	collaborator := newAccessTestUser(t, "bob", true)
	d := &fakeDrain{t: t, root: root, owner: owner, err: errors.New("sessions still active")}

	repo := createTestHgRepo(t, r, root, owner, "demo")
	keep := createTestHgRepo(t, r, root, owner, "keep")
	if err := AddAccess(&Access{UserId: collaborator.Id, RepoId: repo.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	if err := NewIssue(&Issue{RepoId: repo.Id, Index: 1, Name: "broken", PosterId: owner.Id}); err != nil {
		t.Fatal(err)
	}
	path := storagePath(t, root, repo.Id)

	if err := DeleteHgRepository(ctx, root, repo.Id, d.drain); err == nil {
		t.Fatal("deletion continued after drain failed")
	}
	if len(d.calls) != 1 || d.calls[0] != repo.Id {
		t.Fatalf("drain calls %v", d.calls)
	}
	if !exists(path) {
		t.Fatal("storage removed although drain failed")
	}
	assertHidden(t, owner, repo.Id, "demo")
	if _, err := CreateHgRepository(ctx, r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "demo"}); err != ErrRepoAlreadyExist {
		t.Fatalf("name of a deleting repository reused: %v", err)
	}

	d.err = nil
	if err := DeleteHgRepository(ctx, root, repo.Id, d.drain); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Fatal("storage survived deletion")
	}
	for _, bean := range []interface{}{&Repository{Id: repo.Id}, &Access{RepoId: repo.Id}, &Issue{RepoId: repo.Id}, &Watch{RepoId: repo.Id}} {
		if n, err := x.Count(bean); err != nil || n != 0 {
			t.Fatalf("%T rows left: %d, %v", bean, n, err)
		}
	}
	if u, _ := GetUserById(owner.Id); u.NumRepos != 1 {
		t.Fatalf("num_repos %d, want 1", u.NumRepos)
	}
	if err := DeleteHgRepository(ctx, root, repo.Id, d.drain); err != ErrRepoNotExist {
		t.Fatalf("second deletion: %v", err)
	}
	if _, err := GetRepositoryById(keep.Id); err != nil || !exists(storagePath(t, root, keep.Id)) {
		t.Fatalf("deletion touched another repository: %v", err)
	}

	// A crash after any stage leaves a hidden repository that a retry, or
	// startup reconciliation, finishes deleting.
	for i, stage := range []string{"tombstoned", "drained", "storage-removed"} {
		for _, viaReconcile := range []bool{false, true} {
			name := fmt.Sprintf("crash%d%v", i, viaReconcile)
			victim := createTestHgRepo(t, r, root, owner, name)
			crashAt(t, stage)
			runUntilCrash(t, stage, func() { DeleteHgRepository(ctx, root, victim.Id, d.drain) })
			assertHidden(t, owner, victim.Id, name)
			// A retry drains again, possibly after storage is already gone.
			noCheck := func(context.Context, int64) error { return nil }

			if viaReconcile {
				reload()
				err := ReconcileHgRepositories(ctx, root)
				if err != nil {
					t.Fatalf("%s: reconcile: %v", stage, err)
				}
			} else if err := DeleteHgRepository(ctx, root, victim.Id, noCheck); err != nil {
				t.Fatalf("%s: retry: %v", stage, err)
			}
			if has, _ := x.Id(victim.Id).Get(new(Repository)); has || exists(storagePath(t, root, victim.Id)) {
				t.Fatalf("%s (reconcile %v): deletion not finished", stage, viaReconcile)
			}
		}
	}

	// A crash while removing moved storage leaves only the staging copy,
	// possibly without its marker; the retry still removes it.
	moved := createTestHgRepo(t, r, root, owner, "moved")
	staged := filepath.Join(root, stagingDirName, fmt.Sprintf("%d-%s", moved.Id, moved.StorageNonce))
	if err := os.Rename(storagePath(t, root, moved.Id), staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(staged, ".hg", storageMarker)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteHgRepository(ctx, root, moved.Id, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if exists(staged) {
		t.Fatal("retried deletion left the staging copy")
	}

	if u, _ := GetUserById(owner.Id); u.NumRepos != 1 {
		t.Fatalf("num_repos %d after retried deletions, want 1", u.NumRepos)
	}
}

// linkDir makes link point at target with a symbolic link or, on Windows, a
// directory junction. It reports false when the platform refuses.
func linkDir(t *testing.T, kind, target, link string) bool {
	t.Helper()
	var err error
	if kind == "symlink" {
		err = os.Symlink(target, link)
	} else {
		var out []byte
		out, err = exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			err = fmt.Errorf("%v: %s", err, out)
		}
	}
	if err != nil {
		t.Logf("cannot create %s here, sub-case not run: %v", kind, err)
		return false
	}
	return true
}

func TestRepositoryPathContainment(t *testing.T) {
	newTestDatabase(t)
	root := newLifecycleRoot(t)
	r := newFakeHgRunner(t)
	ctx := context.Background()
	owner := newAccessTestUser(t, "alice", true)

	for _, id := range []int64{0, -1} {
		if _, err := RepositoryStoragePath(root, id); err == nil {
			t.Errorf("ID %d accepted", id)
		}
	}
	if p, err := RepositoryStoragePath(root, 7); err != nil || p != filepath.Join(root, "7") {
		t.Fatalf("path %q, %v", p, err)
	}
	if _, err := RepositoryStoragePath("relative/root", 7); err == nil {
		t.Error("relative root accepted")
	}
	nonASCII := filepath.Join(root, "dépôt")
	if err := ValidateRepositoryRoot(nonASCII); (err == nil) == (runtime.GOOS == "windows") {
		t.Errorf("non-ASCII root on %s: %v", runtime.GOOS, err)
	}
	if _, err := CreateHgRepository(ctx, r, "relative", CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "x"}); err == nil {
		t.Error("create accepted a relative root")
	}

	kinds := []string{"symlink"}
	if runtime.GOOS == "windows" {
		kinds = append(kinds, "junction")
	}
	for _, kind := range kinds {
		for _, level := range []string{"storage", ".hg"} {
			name := strings.ReplaceAll(kind+"-"+level, ".", "")
			repo := createTestHgRepo(t, r, root, owner, name)
			path := storagePath(t, root, repo.Id)

			// Move the real, correctly marked storage outside the root and
			// leave a link in its place.
			outside := filepath.Join(t.TempDir(), "outside")
			var link, target string
			if level == "storage" {
				link, target = path, outside
			} else {
				link, target = filepath.Join(path, ".hg"), filepath.Join(outside, ".hg")
			}
			if err := os.Rename(path, outside); err != nil {
				t.Fatal(err)
			}
			if level == ".hg" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if !linkDir(t, kind, target, link) {
				continue
			}
			if err := DeleteHgRepository(ctx, root, repo.Id, func(context.Context, int64) error { return nil }); err == nil {
				t.Errorf("%s at %s level: deletion followed it", kind, level)
			}
			if !exists(filepath.Join(outside, ".hg", storageMarker)) {
				t.Fatalf("%s at %s level: deletion removed files outside the root", kind, level)
			}
		}

		// A linked staging area must not lead reconciliation outside either.
		half := &Repository{OwnerId: owner.Id, Name: "half-" + kind, LowerName: "half-" + kind,
			State: RepositoryProvisioning, StorageNonce: "0123456789abcdef0123456789abcdef"}
		if _, err := x.Insert(half); err != nil {
			t.Fatal(err)
		}
		staging := filepath.Join(root, stagingDirName)
		if err := os.RemoveAll(staging); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		victim := filepath.Join(outside, fmt.Sprintf("%d-%s", half.Id, half.StorageNonce))
		if err := os.MkdirAll(filepath.Join(victim, ".hg"), 0700); err != nil {
			t.Fatal(err)
		}
		if !linkDir(t, kind, outside, staging) {
			continue
		}
		ReconcileHgRepositories(ctx, root)
		if !exists(victim) {
			t.Fatalf("%s staging area: reconciliation removed files outside the root", kind)
		}
		os.Remove(staging)
		x.Id(half.Id).Delete(new(Repository))
	}
}

func TestFreshInstallRequired(t *testing.T) {
	newTestDatabase(t)
	root := filepath.Join(t.TempDir(), "repos")
	if err := CheckFreshInstall(root); err != nil {
		t.Fatalf("empty database and missing root: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "alice", "demo.git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckFreshInstall(root); !errors.Is(err, ErrNotFreshInstall) {
		t.Fatalf("Git-era repository root: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "alice")); err != nil {
		t.Fatal(err)
	}
	if err := CheckFreshInstall(root); err != nil {
		t.Fatalf("empty root: %v", err)
	}
	newAccessTestUser(t, "alice", true)
	if err := CheckFreshInstall(root); !errors.Is(err, ErrNotFreshInstall) {
		t.Fatalf("database with data: %v", err)
	}
}

func TestRepositoryDeleteConcurrentTransfer(t *testing.T) {
	newTestDatabase(t)
	root := newLifecycleRoot(t)
	r := newFakeHgRunner(t)
	ctx := context.Background()
	alice := newAccessTestUser(t, "alice", true)
	carol := newAccessTestUser(t, "carol", true)
	org, err := CreateOrganization(&User{Name: "acme", Email: "acme@example.com", Type: ORGANIZATION, IsActive: true}, carol)
	if err != nil {
		t.Fatal(err)
	}
	numRepos := func(u *User) int {
		got, err := GetUserById(u.Id)
		if err != nil {
			t.Fatal(err)
		}
		return got.NumRepos
	}

	// A transfer loaded before the tombstone must not commit after it.
	repo := createTestHgRepo(t, r, root, alice, "demo")
	stale, err := GetRepositoryById(repo.Id)
	if err != nil {
		t.Fatal(err)
	}
	stale.Owner = alice
	if err = DeleteHgRepository(ctx, root, repo.Id, func(context.Context, int64) error { return errors.New("busy") }); err == nil {
		t.Fatal("drain failure ignored")
	}
	if err = TransferOwnership(alice, carol.Name, stale); err != ErrRepoNotExist {
		t.Fatalf("transfer of a deleting repository: %v", err)
	}
	if numRepos(alice) != 1 || numRepos(carol) != 0 {
		t.Fatalf("num_repos alice %d carol %d after refused transfer", numRepos(alice), numRepos(carol))
	}
	if err = DeleteHgRepository(ctx, root, repo.Id, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if numRepos(alice) != 0 {
		t.Fatalf("num_repos alice %d after delete, want 0", numRepos(alice))
	}

	// A transfer committing between the deletion's load and its tombstone
	// is what the deletion then cleans up.
	repo = createTestHgRepo(t, r, root, alice, "moving")
	lifecycleTestHook = func(stage string) {
		if stage != "loaded" {
			return
		}
		lifecycleTestHook = nil
		cur, err := GetRepositoryById(repo.Id)
		if err != nil {
			t.Fatal(err)
		}
		cur.Owner = alice
		if err = TransferOwnership(alice, org.Name, cur); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { lifecycleTestHook = nil })
	if err = DeleteHgRepository(ctx, root, repo.Id, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if numRepos(alice) != 0 || numRepos(org) != 0 {
		t.Fatalf("num_repos alice %d acme %d, want 0 and 0", numRepos(alice), numRepos(org))
	}
	team, err := org.GetOwnerTeam()
	if err != nil {
		t.Fatal(err)
	}
	if team.NumRepos != 0 || strings.Contains(team.RepoIds, fmt.Sprintf("$%d|", repo.Id)) {
		t.Fatalf("owner team still lists the deleted repository: %d %q", team.NumRepos, team.RepoIds)
	}
}
