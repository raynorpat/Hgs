// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"github.com/go-xorm/xorm"
	"io"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type queryMeasure struct{ queries, rows, maxRows int }
type measuredConnector struct {
	measure *queryMeasure
	dsn     string
}

func TestContributionQuerySnapshotAndTombstone(t *testing.T) {
	newTestDatabase(t)
	now := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	if _, err := x.Insert(&User{Id: 1, Name: "reader", LowerName: "reader", IsActive: true}, &Repository{Id: 10, OwnerId: 1, Name: "snapshot", State: RepositoryReady}, &ContributionIndexState{RepositoryID: 10, Generation: 1, NextGeneration: 1, LastSuccess: now}); err != nil {
		t.Fatal(err)
	}
	i := &Issue{RepoId: 10, Index: 1, PosterId: 1, Name: "Before"}
	if _, err := x.Insert(i); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Exec("UPDATE issue SET created = ? WHERE id = ?", now.Add(-time.Hour).Format("2006-01-02 15:04:05"), i.Id); err != nil {
		t.Fatal(err)
	}
	if err := IndexContributionObject(context.Background(), "issue", i.Id); err != nil {
		t.Fatal(err)
	}
	q := ContributionQuery{Year: 2024, Limit: 25, Cutoff: now}
	read := func(gate func(context.Context, int64, int64) (bool, error)) ContributionResult {
		t.Helper()
		r, e := ReadContributionQuery(context.Background(), 0, 1, false, q, gate)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	allow := func(context.Context, int64, int64) (bool, error) { return true, nil }
	before := read(allow)
	if len(before.Groups) != 1 {
		var e ContributionEvent
		_, _ = x.Get(&e)
		var canonical Issue
		_, _ = x.Id(i.Id).Get(&canonical)
		t.Fatalf("snapshot setup: result=%+v event=%+v issue=%+v", before, e, canonical)
	}
	snapshot, closeSnapshot, err := contributionReadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeSnapshot()
	access, err := repositoryAccessFor(snapshot, 0, 10, false)
	if err != nil || !access.Read {
		t.Fatalf("same-session policy: %+v %v", access, err)
	}
	var original Issue
	if _, err = snapshot.Id(i.Id).Get(&original); err != nil {
		t.Fatal(err)
	}
	if err = contributionTransaction(context.Background(), func(s *xorm.Session) error {
		if _, e := s.Id(i.Id).Cols("name").Update(&Issue{Name: "After"}); e != nil {
			return e
		}
		return stampContributionObject(s, "issue", i.Id)
	}); err != nil {
		t.Fatal(err)
	}
	var stillBefore []Issue
	if err = snapshot.Where("id = ?", i.Id).Find(&stillBefore); err != nil {
		t.Fatal(err)
	}
	if len(stillBefore) != 1 || original.Name != "Before" || stillBefore[0].Name != "Before" {
		t.Fatalf("Get/Find escaped snapshot: %+v %+v", original, stillBefore)
	}
	closeSnapshot()
	after := read(allow)
	if after.Groups[0].Links[0].Label != "After" || after.Revision == before.Revision {
		t.Fatal("committed source change not visible on fresh snapshot")
	}
	if err := contributionTransaction(context.Background(), func(s *xorm.Session) error {
		if err := stampContributionObject(s, "issue", i.Id); err != nil {
			return err
		}
		_, err := s.Id(i.Id).Delete(new(Issue))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deleted := read(allow)
	if len(deleted.Groups) != 0 || deleted.Revision == after.Revision {
		t.Fatal("canonical deletion lost its revision tombstone")
	}
}

func TestContributionCountsIndexedUTC(t *testing.T) {
	newTestDatabase(t)
	if _, err := x.Insert(&User{Id: 1, Name: "team", Type: ORGANIZATION}, &Repository{Id: 10, OwnerId: 1, Name: "utc", State: RepositoryReady}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gen, err := BeginContributionGeneration(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, "2024-03-01T00:30:00+02:00")
	if err != nil {
		t.Fatal(err)
	}
	e := ContributionEvent{Kind: "changeset", Node: fmt.Sprintf("%040x", 1), SourceKey: fmt.Sprintf("changeset:%040x", 1), OccurredAt: at}
	if err = StageContributionEvents(ctx, 10, gen, []ContributionEvent{e}); err != nil {
		t.Fatal(err)
	}
	if err = PublishContributionGeneration(ctx, 10, gen, "", e.Node, 0, at); err != nil {
		t.Fatal(err)
	}
	var stored ContributionEvent
	if _, err = x.Where("source_key = ?", e.SourceKey).Get(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.UTCDate != "2024-02-29" || stored.UTCMonth != "2024-02" || stored.OccurredUnix != 1709245800 {
		t.Fatalf("UTC projection: date=%s month=%s unix=%d", stored.UTCDate, stored.UTCMonth, stored.OccurredUnix)
	}
}

func TestContributionQuerySingleConnectionGate(t *testing.T) {
	newTestDatabase(t)
	if _, err := x.Insert(&User{Id: 1, Name: "reader", LowerName: "reader", IsActive: true}, &Repository{Id: 10, OwnerId: 1, Name: "one-connection", State: RepositoryReady}); err != nil {
		t.Fatal(err)
	}
	x.DB().SetMaxOpenConns(1)
	defer x.DB().SetMaxOpenConns(0)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := ReadContributionQuery(ctx, 0, 1, false, ContributionQuery{Year: 2024, Limit: 25, Cutoff: time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)}, func(_ context.Context, viewer, repo int64) (bool, error) {
		a, e := RepositoryAccessFor(viewer, repo)
		return a.Read, e
	})
	if err != nil {
		t.Fatalf("database-backed gate deadlocked the one-connection pool: %v", err)
	}
}

type measuredDriver struct{ measure *queryMeasure }

func (d measuredDriver) Open(dsn string) (driver.Conn, error) {
	v, e := (&sqlite.Driver{}).Open(dsn)
	if e != nil {
		return nil, e
	}
	return measuredConn{v, d.measure}, nil
}
func (c measuredConnector) Driver() driver.Driver { return measuredDriver{c.measure} }
func (c measuredConnector) Connect(context.Context) (driver.Conn, error) {
	v, e := c.Driver().Open(c.dsn)
	if e != nil {
		return nil, e
	}
	return v, nil
}

type measuredConn struct {
	driver.Conn
	measure *queryMeasure
}

func (c measuredConn) Prepare(q string) (driver.Stmt, error) {
	s, e := c.Conn.Prepare(q)
	if e != nil {
		return nil, e
	}
	return measuredStmt{s, c.measure}, nil
}

type measuredStmt struct {
	driver.Stmt
	measure *queryMeasure
}

func (s measuredStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.measure.queries++
	r, e := s.Stmt.Query(args)
	if e != nil {
		return nil, e
	}
	return &measuredRows{Rows: r, measure: s.measure}, nil
}

type measuredRows struct {
	driver.Rows
	measure *queryMeasure
	n       int
}

func (r *measuredRows) Next(v []driver.Value) error {
	e := r.Rows.Next(v)
	if e == nil {
		r.n++
		r.measure.rows++
	}
	if e == io.EOF && r.n > r.measure.maxRows {
		r.measure.maxRows = r.n
	}
	return e
}

func TestContributionQueryBounded(t *testing.T) {
	newTestDatabase(t)
	now := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	if _, err := x.Insert(&User{Id: 1, Name: "team", LowerName: "team", Type: ORGANIZATION}, &Repository{Id: 10, OwnerId: 1, Name: "large", State: RepositoryReady}, &ContributionIndexState{RepositoryID: 10, Generation: 1, NextGeneration: 1, LastSuccess: now}); err != nil {
		t.Fatal(err)
	}
	const total = 5000
	s := x.NewSession()
	defer s.Close()
	if err := s.Begin(); err != nil {
		t.Fatal(err)
	}
	for start := 1; start <= total; start += 100 {
		events := []ContributionEvent{}
		members := []ContributionRepository{}
		classes := []ContributionClass{}
		for n := start; n < start+100; n++ {
			e := ContributionEvent{ID: int64(n), Kind: "changeset", Node: fmt.Sprintf("%040x", n), SourceKey: fmt.Sprintf("changeset:%040x", n), AuthorEmail: fmt.Sprintf("author%d@example.invalid", n), OccurredAt: now.Add(-time.Hour)}
			e.OccurredUnix, e.UTCDate, e.UTCMonth = e.OccurredAt.Unix(), "2024-03-01", "2024-03"
			events = append(events, e)
			members = append(members, ContributionRepository{EventID: e.ID, RepositoryID: 10, Generation: 1})
			c := contributionClass(10, 0, e)
			c.Revision = int64(n)
			classes = append(classes, c)
		}
		if _, err := s.Insert(&events, &members, &classes); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	measure := &queryMeasure{}
	old := x.DB().DB
	instrumented := sql.OpenDB(measuredConnector{measure, sqliteDSN(DbCfg.Path)})
	x.DB().DB = instrumented
	defer func() { x.DB().DB = old; instrumented.Close() }()
	result, err := ReadContributionQuery(context.Background(), 0, 1, true, ContributionQuery{Year: 2024, Limit: 1, Cutoff: now}, func(context.Context, int64, int64) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Groups) != 1 || result.Groups[0].N != total || len(result.Groups[0].Links) != 25 || len(result.Days) != 1 || result.Days[0].N != total {
		t.Fatalf("independent large total/bound: %+v", result)
	}
	if measure.queries > 40 || measure.rows > 200 || measure.maxRows > 100 {
		t.Fatalf("unbounded query materialization: queries=%d rows=%d maxBatch=%d", measure.queries, measure.rows, measure.maxRows)
	}
	t.Logf("5000 distinct authors: queries=%d rows=%d maxBatch=%d groups=1 links=25 total=5000", measure.queries, measure.rows, measure.maxRows)
}
