package models

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/gogits/gogs/modules/setting"
)

func TestUserSignInRejectsInactive(t *testing.T) {
	newTestDatabase(t)
	newAccessTestUser(t, "erin", true)
	u := newAccessTestUser(t, "alice", true)
	got, err := UserSignIn("alice", "alice-password")
	if err != nil || got == nil || got.Id != u.Id {
		t.Fatalf("active sign-in: %v %+v", err, got)
	}
	u.IsActive = false
	if err := UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	got, err = UserSignIn("alice", "alice-password")
	if err != ErrUserNotExist || got != nil {
		t.Fatalf("inactive sign-in: %v %+v", err, got)
	}
}

func TestNewIssueNumbersAreUnique(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "alice", true)
	repo := newAccessTestRepo(t, owner, "issues", false)
	for i := 0; i < 2; i++ {
		issue := &Issue{RepoId: repo.Id, Index: 7, Name: "sequential", PosterId: owner.Id}
		if err := NewIssue(issue); err != nil {
			t.Fatal(err)
		}
		if issue.Index != int64(i+1) {
			t.Fatalf("issue index %d, want %d", issue.Index, i+1)
		}
	}
	if _, err := GetIssueByIndex(repo.Id, 7); err != ErrIssueNotExist {
		t.Fatalf("caller index was kept: %v", err)
	}

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- NewIssue(&Issue{RepoId: repo.Id, Index: 7, Name: "concurrent", PosterId: owner.Id})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for index := int64(1); index <= 2+workers; index++ {
		issue, err := GetIssueByIndex(repo.Id, index)
		if err != nil {
			t.Fatalf("issue %d: %v", index, err)
		}
		if issue.Index != index {
			t.Fatalf("stored index %d, want %d", issue.Index, index)
		}
	}
}

func TestTransferOwnershipUpdatesOwnerOnly(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "alice", true)
	next := newAccessTestUser(t, "bob", true)
	repo := newAccessTestRepo(t, owner, "proj", false)
	repo.Owner = owner
	if _, err := x.Exec("UPDATE repository SET num_issues = 7 WHERE id = ?", repo.Id); err != nil {
		t.Fatal(err)
	}
	repo.NumIssues = 1
	repo.Description = "stale"
	if err := TransferOwnership(owner, next.Name, repo); err != nil {
		t.Fatal(err)
	}
	var got Repository
	has, err := x.Id(repo.Id).Get(&got)
	if err != nil || !has {
		t.Fatal(err, has)
	}
	if got.NumIssues != 7 || got.Description != "" || got.IsPrivate || got.OwnerId != next.Id {
		t.Fatalf("owner %d num %d private %v desc %q", got.OwnerId, got.NumIssues, got.IsPrivate, got.Description)
	}
}

func TestSQLiteDSNWaitsAndUsesWAL(t *testing.T) {
	dsn := sqliteDSN("data/gogs.db")
	want := "file:data/gogs.db?cache=shared&mode=rwc&_pragma=busy_timeout(2000)&_pragma=journal_mode(WAL)"
	if dsn != want {
		t.Fatal(dsn)
	}
}

func TestDatabaseLogShowsErrorsOnly(t *testing.T) {
	oldX, oldDb, oldRoot, oldHas := x, DbCfg, setting.LogRootPath, HasEngine
	dir := t.TempDir()
	DbCfg.Type = "sqlite3"
	DbCfg.Path = filepath.ToSlash(filepath.Join(dir, "gogs.db"))
	setting.LogRootPath = dir
	t.Cleanup(func() {
		if x != nil {
			x.Close()
		}
		if xormLog != nil {
			xormLog.Close()
			xormLog = nil
		}
		x, DbCfg, setting.LogRootPath, HasEngine = oldX, oldDb, oldRoot, oldHas
	})
	e, err := getEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := setEngine(e); err != nil {
		t.Fatal(err)
	}
	if x.ShowSQL || x.ShowInfo || x.ShowDebug || x.ShowWarn || !x.ShowErr {
		t.Fatalf("SQL=%v info=%v debug=%v warn=%v err=%v", x.ShowSQL, x.ShowInfo, x.ShowDebug, x.ShowWarn, x.ShowErr)
	}
}
