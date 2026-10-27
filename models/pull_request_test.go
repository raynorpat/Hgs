package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
)

func pullFixture(t *testing.T, private bool) (*User, *User, *Repository, *Repository, PullRequest) {
	t.Helper()
	newTestDatabase(t)
	owner := newAccessTestUser(t, "pull-owner", true)
	author := newAccessTestUser(t, "pull-author", true)
	target := newAccessTestRepo(t, owner, "target", private)
	source := newAccessTestRepo(t, author, "source", private)
	if _, err := x.Insert(&ForkRelation{RepositoryID: source.Id, SourceID: target.Id, FamilyID: target.Id}, &Access{UserId: author.Id, RepoId: target.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	src, _ := json.Marshal(hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefNode, Name: strings.Repeat("1", 40)}})
	dst, _ := json.Marshal(hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefNode, Name: strings.Repeat("2", 40)}})
	return owner, author, target, source, PullRequest{SourceRepoID: source.Id, TargetRepoID: target.Id, Title: "A proposal", Body: "description", SourceSelection: string(src), TargetSelection: string(dst)}
}

func TestPullCreatePermissionsAndFamily(t *testing.T) {
	owner, author, target, source, input := pullFixture(t, true)
	ctx := context.Background()
	for _, id := range []int64{0, owner.Id} {
		if _, err := CreatePullRecord(ctx, id, input); err == nil {
			t.Fatalf("unauthorized actor %d created", id)
		}
	}
	p, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil || p.State != PullOpen || p.CurrentComparisonID != 0 || p.AuthorID != author.Id || p.Number != 1 {
		t.Fatalf("pending creation: %+v %v", p, err)
	}
	cases := []struct {
		name   string
		change func(*PullRequest)
	}{
		{"title length", func(p *PullRequest) { p.Title = strings.Repeat("a", 256) }},
		{"empty title", func(p *PullRequest) { p.Title = " \n" }},
		{"body length", func(p *PullRequest) { p.Body = strings.Repeat("a", 65537) }},
		{"invalid selection", func(p *PullRequest) { p.SourceSelection = `{"Ref":{"Kind":"revset","Name":"all()"}}` }},
		{"destination edit", func(p *PullRequest) { p.ID = 1 }},
		{"premerged", func(p *PullRequest) { p.State = PullMerged }},
		{"comparison spoof", func(p *PullRequest) { p.CurrentComparisonID = 42 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := input
			tc.change(&q)
			if _, err := CreatePullRecord(ctx, author.Id, q); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	q := input
	q.Title = strings.Repeat("界", 255)
	q.Body = strings.Repeat("a", 65536)
	if _, err := CreatePullRecord(ctx, author.Id, q); err != nil {
		t.Fatalf("valid text boundary: %v", err)
	}
	other := newAccessTestRepo(t, author, "unrelated", true)
	q = input
	q.SourceRepoID = other.Id
	if _, err := CreatePullRecord(ctx, author.Id, q); err == nil {
		t.Fatal("unrelated family accepted")
	}
	q = input
	q.TargetRepoID = source.Id
	if _, err := CreatePullRecord(ctx, author.Id, q); err != nil {
		t.Fatalf("same repository: %v", err)
	}
	if _, err := x.Id(source.Id).Cols("is_private").Update(&Repository{IsPrivate: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePullRecord(ctx, author.Id, input); err == nil {
		t.Fatal("privacy mismatch accepted")
	}
	x.Id(source.Id).Cols("is_private").Update(&Repository{IsPrivate: true})
	x.Where("user_id = ? AND repo_id = ?", author.Id, target.Id).Delete(new(Access))
	if _, err := CreatePullRecord(ctx, author.Id, input); err == nil {
		t.Fatal("missing ancestor/target read accepted")
	}
}

func TestPullNumberAllocation(t *testing.T) {
	_, author, target, source, input := pullFixture(t, false)
	if _, err := x.Insert(&Issue{RepoId: target.Id, Index: 1, Name: "Canonical issue"}); err != nil {
		t.Fatal(err)
	}
	const n = 12
	results := make(chan PullRequest, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 60; attempt++ {
				p, err := CreatePullRecord(context.Background(), author.Id, input)
				if errors.Is(err, ErrPullConflict) {
					time.Sleep(time.Millisecond * 5)
					continue
				}
				if err != nil {
					failures <- err
					return
				}
				results <- p
				return
			}
			failures <- fmt.Errorf("conflict retry exhausted")
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := map[int64]bool{}
	for p := range results {
		if seen[p.Number] {
			t.Fatalf("duplicate number %d", p.Number)
		}
		seen[p.Number] = true
	}
	if len(seen) != n {
		t.Fatalf("created %d of %d", len(seen), n)
	}
	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("sequence missing %d", i)
		}
	}
	input.TargetRepoID = source.Id
	p, err := CreatePullRecord(context.Background(), author.Id, input)
	if err != nil || p.Number != 1 {
		t.Fatalf("target-local numbering: %+v %v", p, err)
	}
	var issue Issue
	ok, err := x.Where("repo_id = ? AND `index` = ?", target.Id, 1).Get(&issue)
	if err != nil || !ok || issue.Name != "Canonical issue" || issue.IsPull {
		t.Fatalf("issue changed: %+v %v", issue, err)
	}
	// The unique key remains a final guard, independent of application allocation.
	p.ID = 0
	p.Number = 1
	if _, err := x.Insert(&p); err == nil {
		t.Fatal("duplicate target/number inserted")
	}
}

func TestPullVisibilityAndTransitions(t *testing.T) {
	owner, author, target, source, input := pullFixture(t, true)
	ctx := context.Background()
	p, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	stranger := newAccessTestUser(t, "pull-stranger", true)
	for _, actor := range []int64{0, stranger.Id, owner.Id} {
		if got, err := GetPullRecord(ctx, actor, target.Id, p.Number); err == nil || got.Title != "" {
			t.Fatalf("private title leak %+v %v", got, err)
		}
		page, _ := ListPullRecords(ctx, actor, PullQuery{RepoID: target.Id})
		if page.Total != 0 || len(page.Requests) != 0 {
			t.Fatalf("private count leak %+v", page)
		}
	}
	if err := EditPullRecord(ctx, author.Id, p.ID, "Edited", "body"); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(ctx, author.Id, p.ID, PullDeclined); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(ctx, author.Id, p.ID, PullOpen); err != nil {
		t.Fatal(err)
	}
	x.Insert(&Access{UserId: owner.Id, RepoId: source.Id, Mode: READABLE})
	if err := EditPullRecord(ctx, owner.Id, p.ID, "Owner edit", ""); err == nil {
		t.Fatal("target writer edited author content")
	}
	if err := SetPullRecordState(ctx, owner.Id, p.ID, PullDeclined); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(ctx, owner.Id, p.ID, PullOpen); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(ctx, owner.Id, p.ID, PullMerged); err == nil {
		t.Fatal("generic API merged")
	}
	x.Id(p.ID).Cols("state").Update(&PullRequest{State: PullMerged})
	if err := SetPullRecordState(ctx, author.Id, p.ID, PullOpen); err == nil {
		t.Fatal("merged reopened")
	}
	x.Id(p.ID).Cols("state").Update(&PullRequest{State: PullOpen})
	page, err := ListPullRecords(ctx, author.Id, PullQuery{RepoID: target.Id, Filter: "created", PageSize: 1000})
	if err != nil || page.Total != 1 || page.Page != 1 || page.PageSize != 100 {
		t.Fatalf("page %+v %v", page, err)
	}
	x.Where("user_id = ? AND repo_id = ?", author.Id, target.Id).Delete(new(Access))
	page, _ = ListPullRecords(ctx, author.Id, PullQuery{})
	if page.Total != 0 {
		t.Fatal("revoked ancestry count")
	}
	if err := EditPullRecord(ctx, author.Id, p.ID, "Revoked", ""); err == nil {
		t.Fatal("revoked author edited")
	}
	if err := SetPullRecordState(ctx, author.Id, p.ID, PullDeclined); err == nil {
		t.Fatal("revoked author changed state")
	}
}

func TestPullVisibilityPublicAndPaging(t *testing.T) {
	_, author, target, _, input := pullFixture(t, false)
	old := setting.Service.RequireSignInView
	setting.Service.RequireSignInView = false
	t.Cleanup(func() { setting.Service.RequireSignInView = old })
	for i := 0; i < 3; i++ {
		if _, err := CreatePullRecord(context.Background(), author.Id, input); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListPullRecords(context.Background(), 0, PullQuery{RepoID: target.Id, Page: 2, PageSize: 2})
	if err != nil || page.Total != 3 || len(page.Requests) != 1 {
		t.Fatalf("public page %+v %v", page, err)
	}
	if _, err := GetPullRecord(context.Background(), 0, target.Id, 1); err != nil {
		t.Fatal(err)
	}
	setting.Service.RequireSignInView = true
	page, _ = ListPullRecords(context.Background(), 0, PullQuery{})
	if page.Total != 0 {
		t.Fatal("signin policy leaked counts")
	}
	for _, q := range []PullQuery{{State: "bogus"}, {Filter: "assigned"}, {RepoID: -1}} {
		if _, err := ListPullRecords(context.Background(), author.Id, q); err == nil {
			t.Fatal("unsupported query accepted")
		}
	}
}

func TestPullVisibilityAuthorizationBeforePagination(t *testing.T) {
	_, author, target, source, input := pullFixture(t, true)
	ctx := context.Background()
	visible, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	second := newAccessTestRepo(t, author, "hidden-source", true)
	if _, err := x.Insert(&ForkRelation{RepositoryID: second.Id, SourceID: target.Id, FamilyID: target.Id}); err != nil {
		t.Fatal(err)
	}
	input.SourceRepoID = second.Id
	if _, err := CreatePullRecord(ctx, author.Id, input); err != nil {
		t.Fatal(err)
	}
	reader := newAccessTestUser(t, "partial-reader", true)
	for _, id := range []int64{target.Id, source.Id} {
		if _, err := x.Insert(&Access{UserId: reader.Id, RepoId: id, Mode: READABLE}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListPullRecords(ctx, reader.Id, PullQuery{RepoID: target.Id, PageSize: 1})
	if err != nil || page.Total != 1 || len(page.Requests) != 1 || page.Requests[0].ID != visible.ID {
		t.Fatalf("hidden newer source affected page/count: %+v %v", page, err)
	}
	page, err = ListPullRecords(ctx, reader.Id, PullQuery{RepoID: target.Id, Page: 2, PageSize: 1})
	if err != nil || page.Total != 1 || len(page.Requests) != 0 {
		t.Fatalf("hidden source affected second page: %+v %v", page, err)
	}
}

func TestPullCreateCurrentAuthorityAtPersistence(t *testing.T) {
	_, author, target, _, input := pullFixture(t, true)
	if err := ValidatePullCreation(author.Id, input); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Where("user_id = ? AND repo_id = ?", author.Id, target.Id).Delete(new(Access)); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePullRecord(context.Background(), author.Id, input); err == nil {
		t.Fatal("preflight became a reusable grant")
	}
	if _, err := x.Insert(&Access{UserId: author.Id, RepoId: target.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Id(author.Id).Cols("is_active").Update(&User{IsActive: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePullRecord(context.Background(), author.Id, input); err == nil {
		t.Fatal("disabled author created")
	}
}

func TestPullVisibilityReopenRequiresSourceWrite(t *testing.T) {
	owner, author, _, source, input := pullFixture(t, false)
	p, err := CreatePullRecord(context.Background(), author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Id(source.Id).Cols("owner_id").Update(&Repository{OwnerId: owner.Id}); err != nil {
		t.Fatal(err)
	}
	// Public reads remain, but authorship alone cannot reopen without source write.
	if err := SetPullRecordState(context.Background(), author.Id, p.ID, PullDeclined); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(context.Background(), author.Id, p.ID, PullOpen); err == nil {
		t.Fatal("author reopened after source write revocation")
	}
	if err := SetPullRecordState(context.Background(), owner.Id, p.ID, PullOpen); err != nil {
		t.Fatalf("target writer cannot reopen: %v", err)
	}
}

// Explicit disposable network fixture mode. A requested unavailable backend is a
// failure, never a fallback to SQLite or a silently skipped runtime claim.
func TestPullNumberNetworkFixture(t *testing.T) {
	backend := os.Getenv("GOGS_TEST_DB_BACKEND")
	if backend == "" {
		t.Skip("MySQL/PostgreSQL runtime unverified: no explicit disposable fixture configured")
	}
	if backend != "mysql" && backend != "postgres" {
		t.Fatalf("unsupported network fixture %q", backend)
	}
	oldX, oldDB, oldHas := x, DbCfg, HasEngine
	t.Cleanup(func() {
		if x != oldX {
			x.Close()
		}
		x, DbCfg, HasEngine = oldX, oldDB, oldHas
	})
	DbCfg.Type = backend
	DbCfg.Host = os.Getenv("GOGS_TEST_DB_HOST")
	DbCfg.Name = os.Getenv("GOGS_TEST_DB_NAME")
	DbCfg.User = os.Getenv("GOGS_TEST_DB_USER")
	DbCfg.Passwd = os.Getenv("GOGS_TEST_DB_PASSWORD")
	DbCfg.SSLMode = "disable"
	host, _, err := net.SplitHostPort(DbCfg.Host)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || !strings.HasPrefix(DbCfg.Name, "gogs_test_") || DbCfg.User == "" {
		t.Fatal("requires explicit loopback host:port, gogs_test_ database, and test user")
	}
	e, err := getEngine()
	if err != nil {
		t.Fatal(err)
	}
	metas, err := e.DBMetas()
	if err != nil {
		e.Close()
		t.Fatal(err)
	}
	if len(metas) != 0 {
		e.Close()
		t.Fatal("refusing nonempty network database")
	}
	if err := e.StoreEngine("InnoDB").Sync2(tables...); err != nil {
		e.Close()
		t.Fatal(err)
	}
	x = e
	HasEngine = true
	author := newAccessTestUser(t, "network-author", true)
	repo := newAccessTestRepo(t, author, "network", false)
	input := PullRequest{SourceRepoID: repo.Id, TargetRepoID: repo.Id, Title: "Network sequence", SourceSelection: `{"Ref":{"Kind":"node","Name":"1111111111111111111111111111111111111111"}}`, TargetSelection: `{"Ref":{"Kind":"node","Name":"2222222222222222222222222222222222222222"}}`}
	const n = 12
	results := make(chan PullRequest, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 60; attempt++ {
				p, err := CreatePullRecord(context.Background(), author.Id, input)
				if errors.Is(err, ErrPullConflict) {
					time.Sleep(5 * time.Millisecond)
					continue
				}
				if err != nil {
					failures <- err
					return
				}
				results <- p
				return
			}
			failures <- errors.New("retry exhausted")
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := map[int64]bool{}
	for p := range results {
		if seen[p.Number] {
			t.Fatalf("duplicate %d", p.Number)
		}
		seen[p.Number] = true
	}
	if len(seen) != n {
		t.Fatalf("created %d/%d", len(seen), n)
	}
	page, err := ListPullRecords(context.Background(), author.Id, PullQuery{RepoID: repo.Id})
	if err != nil || page.Total != n {
		t.Fatalf("network page %+v %v", page, err)
	}
	if err := EditPullRecord(context.Background(), author.Id, page.Requests[0].ID, "Updated", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(context.Background(), author.Id, page.Requests[0].ID, PullDeclined); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s unique concurrent target numbering, authorized list, edit and transition passed", backend)
}

func TestPullBodyCapacityAcrossDialects(t *testing.T) {
	for _, tc := range []struct {
		backend, dsn string
		capacity     map[string]int
	}{
		{"mysql", "unused:unused@tcp(127.0.0.1:1)/gogs_test_unused", map[string]int{"TEXT": 65535, "MEDIUMTEXT": 16777215, "LONGTEXT": 2147483647}},
		{"postgres", "host=127.0.0.1 port=1 user=unused dbname=gogs_test_unused sslmode=disable", map[string]int{"TEXT": 1073741824}},
		{"sqlite3", ":memory:", map[string]int{"TEXT": 1000000000}},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			engine, err := xorm.NewEngine(tc.backend, tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			column := engine.TableInfo(new(PullRequest)).GetColumn("body")
			kind := strings.ToUpper(engine.SqlType(column))
			if tc.capacity[kind] < 65536 {
				t.Fatalf("body column %s cannot hold the accepted 65536-byte body", kind)
			}
		})
	}
}
