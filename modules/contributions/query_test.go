// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
)

type queryFixture struct {
	db       *xorm.Engine
	s        *Service
	now      time.Time
	sequence int
}

func newQueryFixture(t *testing.T) *queryFixture {
	t.Helper()
	oldDB, oldLog, oldSub, oldSecret, oldSignin := models.DbCfg, setting.LogRootPath, setting.AppSubUrl, setting.SecretKey, setting.Service.RequireSignInView
	root := t.TempDir()
	models.DbCfg.Type, models.DbCfg.Path = "sqlite3", filepath.Join(root, "query.db")
	setting.LogRootPath, setting.AppSubUrl, setting.SecretKey = root, "/code", "owned-contribution-query-fixture"
	setting.Service.RequireSignInView = false
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		models.CloseEngine()
		models.DbCfg, setting.LogRootPath, setting.AppSubUrl, setting.SecretKey, setting.Service.RequireSignInView = oldDB, oldLog, oldSub, oldSecret, oldSignin
	})
	f := &queryFixture{db: db, now: time.Date(2024, 3, 2, 12, 0, 0, 0, time.UTC)}
	f.s = NewService(nil, func(context.Context, int64) (string, error) { t.Fatal("Read invoked native path"); return "", nil }, func(ctx context.Context, viewer, repo int64) (bool, error) {
		a, e := models.RepositoryAccessFor(viewer, repo)
		return a.Read, e
	}, func() time.Time { return f.now })
	for _, u := range []models.User{{Id: 1, Name: "alice", LowerName: "alice", Email: "alice@example.invalid", IsActive: true}, {Id: 2, Name: "bob", LowerName: "bob", Email: "bob@example.invalid", IsActive: true}, {Id: 3, Name: "team", LowerName: "team", Type: models.ORGANIZATION, IsActive: true}} {
		if _, err := db.Insert(&u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Insert(&models.EmailVerification{UserID: 1, NormalizedEmail: "alice@example.invalid", VerifiedAt: f.now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.repo(t, 10, 1, false)
	return f
}

func (f *queryFixture) repo(t *testing.T, id, owner int64, private bool) {
	t.Helper()
	r := models.Repository{Id: id, OwnerId: owner, CreatorID: 1, Name: fmt.Sprintf("repo%d", id), LowerName: fmt.Sprintf("repo%d", id), State: models.RepositoryReady, IsPrivate: private}
	if _, err := f.db.Insert(&r, &models.ContributionIndexState{RepositoryID: id, Generation: 1, NextGeneration: 1, LastSuccess: f.now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
}

func (f *queryFixture) event(t *testing.T, repo int64, kind, at string) models.ContributionEvent {
	t.Helper()
	f.sequence++
	date, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	e := models.ContributionEvent{Kind: kind, OccurredAt: date.UTC(), ActorID: 1, SourceKey: fmt.Sprintf("fixture:%d", f.sequence), ObjectID: int64(f.sequence)}
	e.OccurredUnix, e.UTCDate, e.UTCMonth = date.Unix(), date.UTC().Format("2006-01-02"), date.UTC().Format("2006-01")
	switch kind {
	case "changeset":
		e.Node = fmt.Sprintf("%040x", f.sequence)
		e.SourceKey = "changeset:" + e.Node
		e.ActorID = 0
		e.AuthorEmail = "alice@example.invalid"
	case "issue":
		e.SourceKey = fmt.Sprintf("issue:%d", e.ObjectID)
		i := models.Issue{Id: e.ObjectID, RepoId: repo, Index: int64(f.sequence), PosterId: 1, Name: fmt.Sprintf("Issue %d", f.sequence), Created: date.UTC()}
		if _, err = f.db.Insert(&i); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE issue SET created = ? WHERE id = ?", date.UTC().Format("2006-01-02 15:04:05"), i.Id); err != nil {
			t.Fatal(err)
		}
	case "pull", "review":
		e.SourceKey = fmt.Sprintf("pull:%d", e.ObjectID)
		p := models.PullRequest{ID: e.ObjectID, TargetRepoID: repo, SourceRepoID: repo, Number: int64(f.sequence), AuthorID: 1, Title: "Pull title", State: models.PullOpen, Created: date.UTC()}
		if _, err = f.db.Insert(&p); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE pull_request SET created = ? WHERE id = ?", date.UTC().Format("2006-01-02 15:04:05"), p.ID); err != nil {
			t.Fatal(err)
		}
		if kind == "review" {
			e.SourceKey = fmt.Sprintf("review:1:%d", e.ObjectID)
			if _, err = f.db.Insert(&models.ContributionFirstReview{ReviewerID: 1, PullID: p.ID, OccurredAt: date.UTC()}); err != nil {
				t.Fatal(err)
			}
		}
	case "repository":
		e.ObjectID = repo
		e.SourceKey = fmt.Sprintf("repository:%d", repo)
		if _, err = f.db.Exec("UPDATE repository SET created = ? WHERE id = ?", date.UTC().Format("2006-01-02 15:04:05"), repo); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.db.Insert(&e); err != nil {
		t.Fatal(err)
	}
	generation := int64(0)
	if kind == "changeset" {
		generation = 1
	}
	if _, err = f.db.Insert(&models.ContributionRepository{EventID: e.ID, RepositoryID: repo, Generation: generation}); err != nil {
		t.Fatal(err)
	}
	source := int64(0)
	if kind == "pull" || kind == "review" {
		source = repo
	}
	if _, err = f.db.Insert(&models.ContributionClass{Key: fmt.Sprintf("%064x", f.sequence), RepositoryID: repo, SourceID: source, ActorID: e.ActorID, AuthorEmail: e.AuthorEmail, Kind: kind, Revision: int64(f.sequence)}); err != nil {
		t.Fatal(err)
	}
	if f.sequence == 1 {
		if _, err = f.db.Insert(&models.ContributionClock{ID: 1, Revision: 1}); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err = f.db.Id(1).Cols("revision").Update(&models.ContributionClock{Revision: int64(f.sequence)}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (f *queryFixture) read(t *testing.T, viewer int64, scope Scope, q Query) Page {
	t.Helper()
	p, err := f.s.Read(context.Background(), viewer, scope, q)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestContributionCountsAndUTC(t *testing.T) {
	f := newQueryFixture(t)
	f.event(t, 10, "changeset", "2024-03-01T00:30:00+02:00") // leap day UTC
	f.event(t, 10, "changeset", "2024-02-29T23:30:00-02:00") // March UTC
	f.event(t, 10, "issue", "2024-02-29T12:00:00Z")
	f.event(t, 10, "pull", "2024-03-01T12:00:00Z")
	f.event(t, 10, "review", "2024-03-01T13:00:00Z")
	f.event(t, 10, "repository", "2024-02-01T00:00:00Z")
	f.event(t, 10, "changeset", "2024-03-03T00:00:00Z") // future
	f.event(t, 10, "changeset", "0001-01-01T00:00:00Z") // invalid
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.Totals != (Counts{Changesets: 2, Issues: 1, Pulls: 1, Reviews: 1}) || len(p.Days) != 62 || p.State != "ready" {
		t.Fatalf("UTC totals/state: %+v days=%d state=%s", p.Totals, len(p.Days), p.State)
	}
	if p.Days[59] != (DayCount{Date: "2024-02-29", Counts: Counts{Changesets: 1, Issues: 1}}) || p.Days[60].Counts != (Counts{Changesets: 1, Pulls: 1, Reviews: 1}) {
		t.Fatalf("midnight dates: %+v %+v", p.Days[59], p.Days[60])
	}
	day := f.read(t, 0, Scope{OwnerID: 1}, Query{Day: "2024-02-29", Kind: "changeset"})
	if day.Totals != (Counts{Changesets: 1}) {
		t.Fatalf("same day/category filters: %+v", day)
	}
	prior := f.read(t, 0, Scope{OwnerID: 1}, Query{Year: 2020})
	if len(prior.Days) != 366 || prior.Totals != (Counts{}) {
		t.Fatalf("past leap year: %d %+v", len(prior.Days), prior.Totals)
	}
	f.event(t, 10, "changeset", "1968-02-29T12:00:00Z")
	historical := f.read(t, 0, Scope{OwnerID: 1}, Query{Year: 1968})
	if len(historical.Days) != 366 || historical.Totals.Changesets != 1 {
		t.Fatalf("valid pre-epoch timestamp excluded: %+v", historical.Totals)
	}
	f.event(t, 10, "changeset", "0001-01-01T00:00:01Z") // valid instant in the same year as zero
	firstYear := f.read(t, 0, Scope{OwnerID: 1}, Query{Year: 1})
	if len(firstYear.Days) != 365 || firstYear.Totals != (Counts{Changesets: 1}) || firstYear.Days[0].Counts != (Counts{Changesets: 1}) || len(firstYear.Groups) != 1 || firstYear.Groups[0].Count != 1 || len(firstYear.Years) == 0 || firstYear.Years[len(firstYear.Years)-1] != 1 {
		t.Fatalf("year 1 must exclude zero but retain the next second: totals=%+v days=%d first=%+v groups=%+v yearCount=%d", firstYear.Totals, len(firstYear.Days), firstYear.Days[0], firstYear.Groups, len(firstYear.Years))
	}
	for _, q := range []Query{{Year: -1}, {Year: 2025}, {Day: "2024-02-30"}, {Day: "2023-01-01"}, {Kind: "comment"}, {Limit: -1}, {Limit: 51}} {
		if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, q); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("invalid query %+v: %v", q, err)
		}
	}
}

func TestContributionPrivateVisibility(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, true)
	f.event(t, 11, "changeset", "2001-01-01T00:00:00Z")
	pull := f.event(t, 10, "pull", "2002-01-01T00:00:00Z")
	if _, err := f.db.Id(pull.ObjectID).Cols("source_repo_id").Update(&models.PullRequest{SourceRepoID: 11}); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if len(p.Years) != 1 || p.Years[0] != 2024 || p.Totals != (Counts{}) || len(p.Groups) != 0 {
		t.Fatalf("private years/counts leaked: %+v", p)
	}
	setting.Service.RequireSignInView = true
	f.event(t, 10, "issue", "2024-03-01T00:00:00Z")
	p = f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.Totals != (Counts{}) {
		t.Fatal("anonymous sign-in policy bypass")
	}
}

func TestContributionForkDedup(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 9, 2, true)
	f.repo(t, 12, 3, false)
	e := f.event(t, 9, "changeset", "2024-02-29T12:00:00Z")
	for _, id := range []int64{10, 12} {
		if _, err := f.db.Insert(&models.ContributionRepository{EventID: e.ID, RepositoryID: id, Generation: 1}); err != nil {
			t.Fatal(err)
		}
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.Totals.Changesets != 1 || len(p.Groups) != 1 || p.Groups[0].Links[0].RepositoryID != 10 {
		t.Fatalf("authorized representative: %+v", p)
	}
	org := f.read(t, 0, Scope{OwnerID: 3, Organization: true}, Query{})
	if org.Totals.Changesets != 1 || org.Groups[0].Links[0].RepositoryID != 12 {
		t.Fatalf("org target scope: %+v", org)
	}
	if _, err := f.db.Where("user_id = ?", 1).Delete(new(models.EmailVerification)); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, 0, Scope{OwnerID: 1}, Query{}); got.Totals.Changesets != 0 {
		t.Fatal("stale stored attribution")
	}
	if got := f.read(t, 0, Scope{OwnerID: 3, Organization: true}, Query{}); got.Totals.Changesets != 1 {
		t.Fatal("unattributed org author excluded")
	}
}

func TestContributionLifecycle(t *testing.T) {
	f := newQueryFixture(t)
	e := f.event(t, 10, "issue", "2024-03-01T00:00:00Z")
	if _, err := f.db.Id(e.ObjectID).Cols("name", "is_closed").Update(&models.Issue{Name: "<changed>", IsClosed: true}); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if len(p.Groups) != 1 || !strings.Contains(p.Groups[0].Links[0].URL, "/code/alice/repo10/issues/") {
		t.Fatalf("canonical route: %+v", p.Groups)
	}
	if _, err := f.db.Id(e.ObjectID).Delete(new(models.Issue)); err != nil {
		t.Fatal(err)
	}
	if p = f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Issues != 0 {
		t.Fatal("deleted source still counted")
	}
	if _, err := f.db.Id(10).Cols("updating").Update(&models.ContributionIndexState{Updating: true}); err != nil {
		t.Fatal(err)
	}
	if p = f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.State != "updating" {
		t.Fatalf("lag claimed ready: %s", p.State)
	}
}

func TestContributionLifecyclePendingCanonical(t *testing.T) {
	f := newQueryFixture(t)
	i := &models.Issue{RepoId: 10, Index: 1, PosterId: 1, Name: "Not indexed yet"}
	if _, err := f.db.Insert(i); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Id(i.Id).Cols("created").Update(&models.Issue{Created: f.now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := models.EnqueueContribution(context.Background(), "issue", i.Id); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.State != "updating" || p.Totals.Issues != 0 {
		t.Fatalf("pending canonical source claimed complete: %s %+v", p.State, p.Totals)
	}
}

func TestContributionLifecycleWholeGroupStatus(t *testing.T) {
	f := newQueryFixture(t)
	first := f.event(t, 10, "issue", "2024-02-01T00:00:00Z")
	for n := 0; n < 29; n++ {
		f.event(t, 10, "issue", "2024-02-01T00:00:00Z")
	}
	if _, err := f.db.Id(first.ObjectID).Cols("is_closed").Update(&models.Issue{IsClosed: true}); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if len(p.Groups) != 1 || p.Groups[0].Count != 30 || len(p.Groups[0].Links) != 25 || p.Groups[0].Status != "mixed" {
		t.Fatalf("whole-group status inferred from only representative links: %+v", p.Groups)
	}
}

func TestContributionLifecycleUTCRepair(t *testing.T) {
	f := newQueryFixture(t)
	e := f.event(t, 10, "issue", "2024-02-01T00:00:00Z")
	if _, err := f.db.Exec("UPDATE contribution_event SET utc_date = NULL, utc_month = NULL, occurred_unix = NULL WHERE id = ?", e.ID); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.State != "updating" || p.Totals.Issues != 0 {
		t.Fatalf("unprojected index claimed complete: %s %+v", p.State, p.Totals)
	}
	if err := models.IndexContributionObject(context.Background(), "issue", e.ObjectID); err != nil {
		t.Fatal(err)
	}
	p = f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.State != "ready" || p.Totals.Issues != 1 {
		t.Fatalf("normal reconciliation did not repair UTC projection: %s %+v", p.State, p.Totals)
	}
}

func TestContributionLifecycleColdForeignRepository(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, false)
	if _, err := f.db.Id(11).Delete(new(models.ContributionIndexState)); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{})
	if p.State != "updating" || p.Totals != (Counts{}) {
		t.Fatalf("unknown attribution in cold readable history claimed complete: %s %+v", p.State, p.Totals)
	}
	if _, err := f.db.Where("user_id = ?", 1).Delete(new(models.EmailVerification)); err != nil {
		t.Fatal(err)
	}
	if p = f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.State != "ready" {
		t.Fatalf("unverified foreign attribution was claimed pending: %s", p.State)
	}
	if _, err := f.db.Insert(&models.EmailVerification{UserID: 1, NormalizedEmail: "alice@example.invalid", VerifiedAt: f.now}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Insert(&models.ContributionIndexState{RepositoryID: 11, Generation: 1, NextGeneration: 1, Unavailable: true}); err != nil {
		t.Fatal(err)
	}
	if p = f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.State != "unavailable" {
		t.Fatalf("failed readable history claimed complete: %s", p.State)
	}
	if _, err := f.db.Id(11).Cols("is_private").Update(&models.Repository{IsPrivate: true}); err != nil {
		t.Fatal(err)
	}
	if p = f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.State != "ready" {
		t.Fatalf("hidden foreign history leaked state: %s", p.State)
	}
}

func TestContributionPagination(t *testing.T) {
	f := newQueryFixture(t)
	for _, at := range []string{"2024-01-01T00:00:00Z", "2024-02-01T00:00:00Z", "2024-03-01T00:00:00Z"} {
		f.event(t, 10, "changeset", at)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	if len(p.Groups) != 1 || p.NextCursor == "" {
		t.Fatalf("first bounded page: %+v", p)
	}
	q := Query{Limit: 1, Cursor: p.NextCursor}
	next := f.read(t, 0, Scope{OwnerID: 1}, q)
	repeat := f.read(t, 0, Scope{OwnerID: 1}, q)
	if len(next.Groups) != 1 || next.Groups[0].Key == p.Groups[0].Key || next.Groups[0].Key != repeat.Groups[0].Key {
		t.Fatalf("unstable page: %+v %+v", next.Groups, repeat.Groups)
	}
	if _, err := f.s.Read(context.Background(), 2, Scope{OwnerID: 1}, q); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("cross-viewer cursor: %v", err)
	}
	q.Cursor = p.NextCursor[:len(p.NextCursor)/2] + "forged"
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, q); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("forged cursor: %v", err)
	}
	f.event(t, 10, "changeset", "2024-03-02T00:00:00Z")
	q.Cursor = p.NextCursor
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, q); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("new-event stale cursor: %v", err)
	}
}
