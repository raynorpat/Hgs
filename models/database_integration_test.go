//go:build integration

package models

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
)

// Network backends require an explicitly selected, empty disposable database on
// loopback. Never inherit production configuration or silently skip a requested
// backend. CI provisions each backend in a separate service/job.
func TestDatabaseAcceptance(t *testing.T) {
	backend := os.Getenv("GOGS_TEST_DB_BACKEND")
	if backend == "" {
		backend = "sqlite3"
		t.Log("MySQL/PostgreSQL unavailable locally unless GOGS_TEST_DB_BACKEND is explicitly configured")
	}
	root := t.TempDir()
	oldDB, oldLog, oldRoot := DbCfg, setting.LogRootPath, setting.RepoRootPath
	t.Cleanup(func() { CloseEngine(); DbCfg, setting.LogRootPath, setting.RepoRootPath = oldDB, oldLog, oldRoot })
	DbCfg.Type = backend
	DbCfg.Path = filepath.ToSlash(filepath.Join(root, "database.db"))
	setting.LogRootPath = root
	setting.RepoRootPath = filepath.Join(root, "repos")
	if backend != "sqlite3" {
		if backend != "mysql" && backend != "postgres" {
			t.Fatalf("unsupported test database %q", backend)
		}
		DbCfg.Host, DbCfg.Name, DbCfg.User, DbCfg.Passwd = os.Getenv("GOGS_TEST_DB_HOST"), os.Getenv("GOGS_TEST_DB_NAME"), os.Getenv("GOGS_TEST_DB_USER"), os.Getenv("GOGS_TEST_DB_PASSWORD")
		DbCfg.SSLMode = "disable"
		host, _, err := net.SplitHostPort(DbCfg.Host)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() || !strings.HasPrefix(DbCfg.Name, "gogs_test_") || DbCfg.User == "" {
			t.Fatal("backend requires explicit loopback host:port, gogs_test_ database and test user")
		}
		e, err := getEngine()
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		metas, err := e.DBMetas()
		if err != nil {
			t.Fatal(err)
		}
		if len(metas) != 0 {
			t.Fatal("refusing a nonempty backend database; provision a new isolated service database")
		}
	}
	if err := NewEngine(); err != nil {
		t.Fatal(err)
	}
	owner := newAccessTestUser(t, "alice", true)
	next := newAccessTestUser(t, "bob", true)
	if _, err := UserSignIn(owner.Name, "password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if got, err := UserSignIn(owner.Name, "alice-password"); err != nil || got.Id != owner.Id {
		t.Fatalf("account sign-in: %+v %v", got, err)
	}
	hgExe := os.Getenv("GOGS_TEST_HG")
	if hgExe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	r, err := hg.NewRunner(hg.Config{Executable: hgExe, ConfigPath: filepath.Join(root, "hg", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := CreateHgRepository(context.Background(), r, setting.RepoRootPath, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "project", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	issue := &Issue{RepoId: repo.Id, Index: 1, Name: "retained issue", PosterId: owner.Id, Content: "content"}
	if err = NewIssue(issue); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateComment(owner.Id, repo.Id, issue.Id, COMMENT_TYPE_COMMENT, "discussion", nil); err != nil {
		t.Fatal(err)
	}
	if err = NewLabel(&Label{RepoId: repo.Id, Name: "bug", Color: "#ff0000"}); err != nil {
		t.Fatal(err)
	}
	if err = ChangeRepositoryName(repo.Id, "renamed"); err != nil {
		t.Fatal(err)
	}
	repo, err = GetRepositoryById(repo.Id)
	if err != nil {
		t.Fatal(err)
	}
	repo.Owner = owner
	if err = TransferOwnership(owner, next.Name, repo); err != nil {
		t.Fatal(err)
	}
	if err = CloseEngine(); err != nil {
		t.Fatal(err)
	}
	if err = NewEngine(); err != nil {
		t.Fatal(err)
	}
	repo, err = GetRepositoryByName(next.Id, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := GetIssueByIndex(repo.Id, 1); err != nil || got.Id != issue.Id {
		t.Fatalf("issue after restart: %+v %v", got, err)
	}
	if comments, err := GetIssueComments(issue.Id); err != nil || len(comments) != 1 || comments[0].Content != "discussion" {
		t.Fatalf("comments: %+v %v", comments, err)
	}
	if labels, err := GetLabels(repo.Id); err != nil || len(labels) != 1 {
		t.Fatalf("labels: %+v %v", labels, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- DeleteHgRepository(ctx, setting.RepoRootPath, repo.Id, func(context.Context, int64) error {
			// A drain may need authorization/database reads. The parent deletion
			// must not hold a transaction that deadlocks those reads.
			_, err := RepositoryAccessFor(next.Id, repo.Id)
			if err != ErrRepoNotExist {
				return fmt.Errorf("tombstone authorization: %v", err)
			}
			return nil
		})
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("deletion deadlocked its permission callback")
	}
	if _, err = GetRepositoryById(repo.Id); err != ErrRepoNotExist {
		t.Fatalf("repository survived deletion: %v", err)
	}
	if n, err := x.Where("repo_id=?", repo.Id).Count(new(Issue)); err != nil || n != 0 {
		t.Fatalf("orphan issue rows: %d %v", n, err)
	}
	t.Logf("%s schema/create/rename/transfer/issues/reopen/delete passed", backend)
}
