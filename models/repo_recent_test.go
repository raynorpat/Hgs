package models

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRecentsLimitsAndRenames(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "owner", true)
	now := time.Now()
	var last *Repository
	for i := 0; i < 25; i++ {
		last = newAccessTestRepo(t, owner, fmt.Sprintf("repo%02d", i), false)
		if err := RecordRepositoryVisit(context.Background(), owner.Id, last.Id, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := x.Where("actor_id=?", owner.Id).Count(new(RepositoryVisit)); err != nil || n != 20 {
		t.Fatalf("retained %d %v", n, err)
	}
	x.Id(last.Id).Cols("name", "lower_name").Update(&Repository{Name: "renamed", LowerName: "renamed"})
	if err := RecordRepositoryVisit(context.Background(), owner.Id, last.Id, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := RecentRepositories(context.Background(), owner.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 || rows[0].Name != "renamed" {
		t.Fatalf("recent names/limit: %+v", rows)
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		if seen[r.Id] {
			t.Fatal("duplicate")
		}
		seen[r.Id] = true
	}
	if n, _ := x.Where("actor_id=?", owner.Id).Count(new(RepositoryVisit)); n != 20 {
		t.Fatal("duplicate retained")
	}
}

func TestRecentsRevocationAndAccountIsolation(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "owner", true)
	reader := newAccessTestUser(t, "reader", true)
	repo := newAccessTestRepo(t, owner, "private", true)
	AddAccess(&Access{UserId: reader.Id, RepoId: repo.Id, Mode: READABLE})
	for _, id := range []int64{owner.Id, reader.Id} {
		if err := RecordRepositoryVisit(context.Background(), id, repo.Id, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	x.Where("user_id=?", reader.Id).Delete(new(Access))
	rows, err := RecentRepositories(context.Background(), reader.Id)
	if err != nil || len(rows) != 0 {
		t.Fatal("revoked read", err)
	}
	p, _ := ListAccessibleRepositories(context.Background(), reader.Id, CatalogQuery{})
	if p.Total != 0 {
		t.Fatal("revoked count")
	}
	ClearRepositoryVisits(context.Background(), reader.Id)
	rows, err = RecentRepositories(context.Background(), owner.Id)
	if err != nil || len(rows) != 1 {
		t.Fatal("account isolation", err)
	}
	x.Id(repo.Id).Delete(new(Repository))
	rows, err = RecentRepositories(context.Background(), owner.Id)
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted recent", err)
	}
}

func TestRecentsLifecycleAndDisabledAccounts(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "owner", true)
	repo := newAccessTestRepo(t, owner, "private", true)
	if err := RecordRepositoryVisit(context.Background(), owner.Id, repo.Id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Id(repo.Id).Cols("state").Update(&Repository{State: RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	rows, err := RecentRepositories(context.Background(), owner.Id)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleting recent: %d %v", len(rows), err)
	}
	if _, err := x.Id(repo.Id).Cols("state").Update(&Repository{State: RepositoryReady}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Id(owner.Id).Cols("is_active").Update(&User{IsActive: false}); err != nil {
		t.Fatal(err)
	}
	rows, err = RecentRepositories(context.Background(), owner.Id)
	if err != nil || len(rows) != 0 {
		t.Fatalf("disabled recent: %d %v", len(rows), err)
	}
	page, err := ListAccessibleRepositories(context.Background(), owner.Id, CatalogQuery{})
	if err != nil || page.Total != 0 {
		t.Fatal("disabled catalog", page.Total, err)
	}
}
