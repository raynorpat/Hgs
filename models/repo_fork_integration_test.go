//go:build integration

package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/modules/hg"
)

func TestForkNativeProvisioningRecovery(t *testing.T) {
	for _, stage := range []string{"allocated", "fork-copied", "fork-initialized", "fork-moved"} {
		t.Run(stage, func(t *testing.T) {
			reload := newTestDatabase(t)
			owner := newAccessTestUser(t, "native-owner", true)
			root := newLifecycleRoot(t)
			r, err := hg.NewRunner(hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(t.TempDir(), "hgrc")})
			if err != nil {
				t.Fatal(err)
			}
			source, err := CreateHgRepository(context.Background(), r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "source"})
			if err != nil {
				t.Fatal(err)
			}
			op, repo, err := BeginForkProvisioning(owner.Id, source.Id, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "interrupted"})
			if err != nil {
				t.Fatal(err)
			}
			if stage != "allocated" {
				crashAt(t, stage)
				runUntilCrash(t, stage, func() {
					if err := PrepareForkStorage(context.Background(), r, root, op); err != nil {
						t.Fatal(err)
					}
				})
			}
			if _, err = GetRepositoryById(repo.Id); err != ErrRepoNotExist {
				t.Fatalf("partial fork ready: %v", err)
			}
			reload()
			if err = RollbackForkProvisioning(root, op); err != nil {
				t.Fatal(err)
			}
			if err = ReconcileHgRepositories(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{storagePath(t, root, repo.Id), stagingPath(root, repo)} {
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("owned path retained %s: %v", path, err)
				}
			}
			if _, err = GetRepositoryById(source.Id); err != nil {
				t.Fatal("source lost", err)
			}
		})
	}
}

func TestForkNativeUnknownStorageAndFinalRevocation(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "unknown-owner", true)
	actor := newAccessTestUser(t, "unknown-actor", true)
	root := newLifecycleRoot(t)
	r, err := hg.NewRunner(hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(t.TempDir(), "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	source, err := CreateHgRepository(context.Background(), r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "source", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	x.Insert(&Access{UserId: actor.Id, RepoId: source.Id, Mode: READABLE})
	op, repo, err := BeginForkProvisioning(actor.Id, source.Id, CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "revoked"})
	if err != nil {
		t.Fatal(err)
	}
	if err = PrepareForkStorage(context.Background(), r, root, op); err != nil {
		t.Fatal(err)
	}
	x.Where("user_id = ? AND repo_id = ?", actor.Id, source.Id).Delete(new(Access))
	if _, err = PublishFork(context.Background(), root, op); err == nil {
		t.Fatal("source revocation passed final ready gate")
	}
	final := storagePath(t, root, repo.Id)
	marker := filepath.Join(final, ".hg", storageMarker)
	if err = os.WriteFile(marker, []byte("different allocation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = RollbackForkProvisioning(root, op); err == nil {
		t.Fatal("unowned path cleanup accepted")
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "different allocation" {
		t.Fatalf("unrelated storage changed %s %v", b, err)
	}
	pending, err := PendingForkProvisioning()
	if err != nil || len(pending) != 1 {
		t.Fatalf("unknown ownership fence lost: %v %v", pending, err)
	}
	if _, err = GetRepositoryById(repo.Id); err != ErrRepoNotExist {
		t.Fatal("unknown fork ready", err)
	}
}
