//go:build integration

package models

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
)

// This helper exists only in the test executable. The parent kills the process
// while the package-private lifecycle callback is blocked; no deferred cleanup
// or simulated panic runs in the child.
func TestLifecycleCrashChild(t *testing.T) {
	stage := os.Getenv("GOGS_HG_CRASH_STAGE")
	if stage == "" {
		return
	}
	root := os.Getenv("GOGS_HG_CRASH_ROOT")
	DbCfg.Type, DbCfg.Path = "sqlite3", filepath.ToSlash(filepath.Join(root, "db"))
	setting.LogRootPath = root
	if err := NewEngine(); err != nil {
		t.Fatal(err)
	}
	r, err := hg.NewRunner(hg.Config{Executable: os.Getenv("GOGS_HG_CRASH_EXE"), ConfigPath: filepath.Join(root, "native", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	owner := newAccessTestUser(t, "alice", true)
	barrier := func(s string) {
		if s == stage {
			if err := os.WriteFile(filepath.Join(root, "reached"), []byte(s), 0600); err != nil {
				t.Fatal(err)
			}
			for {
				time.Sleep(time.Hour)
			}
		}
	}
	if !strings.HasPrefix(stage, "delete-") {
		lifecycleTestHook = barrier
	}
	repo, err := CreateHgRepository(context.Background(), r, filepath.Join(root, "repos"), CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "project"})
	if err != nil {
		t.Fatal(err)
	}
	barrier("ready")
	if strings.HasPrefix(stage, "delete-") {
		lifecycleTestHook = func(s string) { barrier("delete-" + s) }
		if err = DeleteHgRepository(context.Background(), filepath.Join(root, "repos"), repo.Id, func(context.Context, int64) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("requested boundary not reached")
}

func TestLifecycleCrashRecovery(t *testing.T) {
	hgExe := os.Getenv("GOGS_TEST_HG")
	if hgExe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	for _, stage := range []string{"allocated", "initialized", "moved", "ready", "delete-loaded", "delete-tombstoned", "delete-drained", "delete-storage-removed"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			exe, _ := os.Executable()
			r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "supervisor", "hgrc")})
			if err != nil {
				t.Fatal(err)
			}
			c, err := r.Start(context.Background(), "", []string{"-test.run=^TestLifecycleCrashChild$", "-test.timeout=60s"}, map[string]string{"GOGS_HG_CRASH_STAGE": stage, "GOGS_HG_CRASH_ROOT": root, "GOGS_HG_CRASH_EXE": hgExe})
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); io.Copy(&output, c.Stdout) }()
			go func() { defer wg.Done(); io.Copy(io.Discard, c.Stderr) }()
			kill := func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); c.Stop(ctx); wg.Wait() }
			defer kill()
			deadline := time.Now().Add(20 * time.Second)
			for !exists(filepath.Join(root, "reached")) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if !exists(filepath.Join(root, "reached")) {
				kill()
				t.Fatalf("boundary not reached: %s", output.String())
			}
			kill()
			oldDB, oldLog := DbCfg, setting.LogRootPath
			DbCfg.Type, DbCfg.Path = "sqlite3", filepath.ToSlash(filepath.Join(root, "db"))
			setting.LogRootPath = root
			t.Cleanup(func() { CloseEngine(); DbCfg, setting.LogRootPath = oldDB, oldLog })
			if err = NewEngine(); err != nil {
				t.Fatal(err)
			}
			var row Repository
			if has, err := x.Get(&row); err != nil || !has {
				t.Fatalf("durable boundary row: %v %v", has, err)
			}
			ready := stage == "ready" || stage == "delete-loaded"
			if !ready {
				if _, err = GetRepositoryById(row.Id); err != ErrRepoNotExist {
					t.Fatalf("partial lifecycle visible: %v", err)
				}
			}
			for pass := 0; pass < 2; pass++ {
				if err = ReconcileHgRepositories(context.Background(), filepath.Join(root, "repos")); err != nil {
					t.Fatal(err)
				}
			}
			has, err := x.Id(row.Id).Get(new(Repository))
			if err != nil || has != ready {
				t.Fatalf("reconciled row present=%t want=%t err=%v", has, ready, err)
			}
			if got := exists(filepath.Join(root, "repos", fmt.Sprint(row.Id))); got != ready {
				t.Fatalf("reconciled storage present=%t want=%t", got, ready)
			}
			if ready {
				native, err := hg.NewRunner(hg.Config{Executable: hgExe, ConfigPath: filepath.Join(root, "verify", "hgrc")})
				if err != nil {
					t.Fatal(err)
				}
				if err = native.Run(context.Background(), filepath.Join(root, "repos", fmt.Sprint(row.Id)), []string{"verify"}, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
