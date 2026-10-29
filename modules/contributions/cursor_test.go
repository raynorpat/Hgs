// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
)

func TestContributionPaginationCompleteGroups(t *testing.T) {
	f := newQueryFixture(t)
	for n := 0; n < 80; n++ {
		f.event(t, 10, "changeset", "2024-02-01T00:00:00Z")
	}
	f.event(t, 10, "issue", "2024-02-01T00:00:00Z")
	f.event(t, 10, "changeset", "2024-01-01T00:00:00Z")
	q := Query{Limit: 1}
	total := 0
	groups := map[string]bool{}
	for {
		p := f.read(t, 0, Scope{OwnerID: 1}, q)
		if p.Totals != (Counts{Changesets: 81, Issues: 1}) {
			t.Fatalf("page totals: %+v", p.Totals)
		}
		for _, g := range p.Groups {
			if groups[g.Key] {
				t.Fatal("repeated group")
			}
			groups[g.Key] = true
			total += g.Count
			if g.Count == 80 && len(g.Links) != 25 {
				t.Fatalf("bounded representative links: %d", len(g.Links))
			}
		}
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
	}
	if total != 82 || len(groups) != 3 {
		t.Fatalf("complete groups did not reconcile: %d/%d", total, len(groups))
	}
}

func TestContributionPaginationHiddenWork(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, true)
	f.event(t, 10, "changeset", "2024-01-01T00:00:00Z")
	f.event(t, 10, "changeset", "2024-02-01T00:00:00Z")
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	q := Query{Limit: 1, Cursor: p.NextCursor}
	f.event(t, 11, "changeset", "2024-02-01T00:00:00Z")
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, q); err != nil {
		t.Fatalf("hidden repository activity invalidated public cursor: %v", err)
	}
	e := f.event(t, 10, "pull", "2024-02-01T00:00:00Z")
	if _, err := f.db.Id(e.ObjectID).Cols("source_repo_id").Update(&models.PullRequest{SourceRepoID: 11}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Where("kind = ?", "pull").Cols("source_id").Update(&models.ContributionClass{SourceID: 11}); err != nil {
		t.Fatal(err)
	}
	if err := models.EnqueueContribution(context.Background(), "pull", e.ObjectID); err != nil {
		t.Fatal(err)
	}
	// Replace synthetic publication with the actual canonical index operation.
	if _, err := f.db.Where("event_id = ?", e.ID).Delete(new(models.ContributionRepository)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Id(e.ID).Delete(new(models.ContributionEvent)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Where("kind = ?", "pull").Delete(new(models.ContributionClass)); err != nil {
		t.Fatal(err)
	}
	if err := models.IndexContributionObject(context.Background(), "pull", e.ObjectID); err != nil {
		t.Fatal(err)
	}
	gen, err := models.BeginContributionGeneration(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	private := models.ContributionEvent{Kind: "changeset", Node: strings.Repeat("f", 40), SourceKey: "changeset:" + strings.Repeat("f", 40), AuthorEmail: "alice@example.invalid", OccurredAt: f.now.Add(-time.Hour)}
	if err = models.StageContributionEvents(context.Background(), 11, gen, []models.ContributionEvent{private}); err != nil {
		t.Fatal(err)
	}
	if err = models.PublishContributionGeneration(context.Background(), 11, gen, "", private.Node, 1, f.now); err != nil {
		t.Fatal(err)
	}
	next, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, q)
	if err != nil || next.State != "ready" {
		t.Fatalf("private source job leaked via cursor/state: %s %v", next.State, err)
	}
}

func TestContributionPaginationEqualCardinalityPermissionSwap(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, true)
	f.repo(t, 12, 2, true)
	// Before: events 1,4,5. After: 2,3,5. Count, max ID and sum ID are identical.
	for _, repo := range []int64{11, 12, 12, 11} {
		f.event(t, repo, "changeset", "2024-01-01T00:00:00Z")
	}
	f.event(t, 10, "changeset", "2024-02-01T00:00:00Z")
	if err := models.AddAccess(&models.Access{UserId: 1, RepoId: 11, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 1, Scope{OwnerID: 1}, Query{Limit: 1})
	if p.Totals.Changesets != 3 {
		t.Fatalf("before swap: %+v", p.Totals)
	}
	if err := models.DeleteAccess(&models.Access{UserId: 1, RepoId: 11}); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: 1, RepoId: 12, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	if after := f.read(t, 1, Scope{OwnerID: 1}, Query{}); after.Totals.Changesets != 3 {
		t.Fatalf("after swap: %+v", after.Totals)
	}
	if _, err := f.s.Read(context.Background(), 1, Scope{OwnerID: 1}, Query{Limit: 1, Cursor: p.NextCursor}); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("equal-cardinality permission swap retained cursor: %v", err)
	}
}

func TestContributionPaginationPreflightRoster(t *testing.T) {
	for _, hidden := range []bool{true, false} {
		t.Run(map[bool]string{true: "hidden", false: "readable"}[hidden], func(t *testing.T) {
			f := newQueryFixture(t)
			f.event(t, 10, "changeset", "2024-01-01T00:00:00Z")
			f.event(t, 10, "changeset", "2024-02-01T00:00:00Z")
			p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
			original := f.s.read
			inserted := false
			f.s.read = func(ctx context.Context, viewer, repo int64) (bool, error) {
				if !inserted {
					inserted = true
					owner := int64(1)
					if hidden {
						owner = 2
					}
					f.repo(t, 11, owner, hidden)
				}
				return original(ctx, viewer, repo)
			}
			_, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, Query{Limit: 1, Cursor: p.NextCursor})
			if hidden && err != nil {
				t.Fatalf("hidden roster change signaled by cursor: %v", err)
			}
			if !hidden && !errors.Is(err, ErrStaleCursor) {
				t.Fatalf("new relevant repository lacked a fresh gate: %v", err)
			}
		})
	}
}

func TestContributionPaginationForeignStateOnly(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, false)
	f.event(t, 10, "changeset", "2024-01-01T00:00:00Z")
	f.event(t, 10, "changeset", "2024-02-01T00:00:00Z")
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	q := Query{Limit: 1, Cursor: p.NextCursor}
	if err := models.EnqueueContribution(context.Background(), "repository", 11); err != nil {
		t.Fatal(err)
	}
	p = f.read(t, 0, Scope{OwnerID: 1}, q)
	if p.State != "updating" || p.Totals.Changesets != 2 {
		t.Fatalf("foreign state affected counts or completeness: %s %+v", p.State, p.Totals)
	}
	if err := models.FailContributionGeneration(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	p = f.read(t, 0, Scope{OwnerID: 1}, q)
	if p.State != "unavailable" {
		t.Fatalf("foreign failed state omitted: %s", p.State)
	}
}

func TestContributionPaginationPreflightPrivateSource(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, true)
	f.event(t, 10, "changeset", "2024-01-01T00:00:00Z")
	f.event(t, 10, "changeset", "2024-02-01T00:00:00Z")
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	original := f.s.read
	inserted := false
	f.s.read = func(ctx context.Context, viewer, repo int64) (bool, error) {
		if !inserted {
			inserted = true
			f.repo(t, 12, 2, false)
			pull := &models.PullRequest{TargetRepoID: 12, SourceRepoID: 11, Number: 1, AuthorID: 1, Title: "Hidden source", State: models.PullOpen}
			if _, err := f.db.Insert(pull); err != nil {
				return false, err
			}
			if err := models.IndexContributionObject(ctx, "pull", pull.ID); err != nil {
				return false, err
			}
		}
		return original(ctx, viewer, repo)
	}
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, Query{Limit: 1, Cursor: p.NextCursor}); err != nil {
		t.Fatalf("private source revealed by new public target roster: %v", err)
	}
}

func TestContributionPaginationReviewRepairAndRebuild(t *testing.T) {
	f := newQueryFixture(t)
	e := f.event(t, 10, "review", "2024-02-01T00:00:00Z")
	f.event(t, 10, "changeset", "2024-03-01T00:00:00Z")
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	var first models.ContributionFirstReview
	if _, err := f.db.Where("pull_id = ?", e.ObjectID).Get(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Id(first.ID).Cols("occurred_at").Update(&models.ContributionFirstReview{OccurredAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if err := models.IndexContributionObject(context.Background(), "review", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, Query{Limit: 1, Cursor: p.NextCursor}); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("same-event date repair retained cursor: %v", err)
	}
	p = f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	if _, err := f.db.Id(10).Cols("generation", "next_generation").Update(&models.ContributionIndexState{Generation: 2, NextGeneration: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, Query{Limit: 1, Cursor: p.NextCursor}); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("rebuild retained cursor: %v", err)
	}
}

func TestContributionPaginationProofSwap(t *testing.T) {
	f := newQueryFixture(t)
	// Before: 1,4,5; after: 2,3,5. Count/max/sum and the highest class epoch
	// remain identical, so only the effective proof dependency detects the swap.
	for n, email := range []string{"alice@example.invalid", "second@example.invalid", "second@example.invalid", "alice@example.invalid", "common@example.invalid"} {
		at := "2024-01-01T00:00:00Z"
		if n == 4 {
			at = "2024-02-01T00:00:00Z"
		}
		e := f.event(t, 10, "changeset", at)
		if _, err := f.db.Id(e.ID).Cols("author_email").Update(&models.ContributionEvent{AuthorEmail: email}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Where("revision = ?", n+1).Cols("author_email").Update(&models.ContributionClass{AuthorEmail: email}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Insert(&models.EmailAddress{Uid: 1, Email: "second@example.invalid"}, &models.EmailAddress{Uid: 1, Email: "common@example.invalid"}, &models.EmailVerification{UserID: 1, NormalizedEmail: "common@example.invalid", VerifiedAt: f.now}); err != nil {
		t.Fatal(err)
	}
	p := f.read(t, 0, Scope{OwnerID: 1}, Query{Limit: 1})
	if p.Totals.Changesets != 3 {
		t.Fatalf("before proof swap: %+v", p.Totals)
	}
	if _, err := f.db.Where("user_id = ? AND normalized_email = ?", 1, "alice@example.invalid").Cols("normalized_email").Update(&models.EmailVerification{NormalizedEmail: "second@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if after := f.read(t, 0, Scope{OwnerID: 1}, Query{}); after.Totals.Changesets != 3 {
		t.Fatalf("after proof swap: %+v", after.Totals)
	}
	if _, err := f.s.Read(context.Background(), 0, Scope{OwnerID: 1}, Query{Limit: 1, Cursor: p.NextCursor}); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("effective proof swap retained cursor: %v", err)
	}
	if strings.Contains(p.NextCursor, "alice") || strings.Contains(p.NextCursor, "example") {
		t.Fatal("opaque cursor exposes proof material")
	}
}
