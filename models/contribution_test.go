package models

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/go-xorm/xorm"
	"strings"
	"testing"
	"time"
)

type competingContributionInsert struct {
	db       *sql.DB
	competed bool
}

func (c *competingContributionInsert) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if strings.HasPrefix(query, "INSERT") && !c.competed {
		c.competed = true
		if _, err := c.db.ExecContext(ctx, query, args...); err != nil {
			return nil, err
		}
	}
	return c.db.ExecContext(ctx, query, args...)
}
func TestContributionEnqueueCompetingInsert(t *testing.T) {
	newTestDatabase(t)
	competitor := &competingContributionInsert{db: x.DB().DB}
	if err := enqueueContributionSQL(context.Background(), competitor, "repository", 999); err != nil {
		t.Fatal(err)
	}
	var job ContributionJob
	if ok, err := x.Where("object_id = ?", 999).Get(&job); err != nil || !ok || job.Requested != 2 || !competitor.competed {
		t.Fatalf("competing durable requests lost: %+v %v", job, err)
	}
}

func TestContributionSourceDeletionInterleaving(t *testing.T) {
	for _, kind := range []string{"pull", "review"} {
		t.Run(kind, func(t *testing.T) {
			_, author, reviewer, p, c := reviewFixture(t, false)
			ctx := context.Background()
			if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
				t.Fatal(err)
			}
			if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); err != nil {
				t.Fatal(err)
			}
			id := p.ID
			if kind == "review" {
				var first ContributionFirstReview
				if ok, err := x.Where("pull_id = ?", p.ID).Get(&first); err != nil || !ok {
					t.Fatal(err)
				}
				id = first.ID
			}
			if err := IndexContributionObject(ctx, kind, id); err != nil {
				t.Fatal(err)
			}
			// Capture the canonical discovery used by the indexer, then finish real
			// source deletion before the delayed membership publication resumes.
			stale, ok, err := readContributionObject(contributionEngine(), kind, id)
			if err != nil || !ok {
				t.Fatal(err)
			}
			if err := DeleteHgRepository(ctx, t.TempDir(), p.SourceRepoID, func(context.Context, int64) error { return nil }, func(context.Context, int64) error { return nil }); err != nil {
				t.Fatal(err)
			}
			_ = publishContributionObject(ctx, stale)
			var memberships []ContributionRepository
			if err := x.Find(&memberships); err != nil || len(memberships) != 0 {
				t.Fatalf("stale %s discovery restored deleted membership: %+v %v", kind, memberships, err)
			}
		})
	}
}

func TestContributionMergedSourceRetention(t *testing.T) {
	_, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); err != nil {
		t.Fatal(err)
	}
	var first ContributionFirstReview
	if ok, err := x.Where("pull_id = ?", p.ID).Get(&first); err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := x.Id(p.ID).Cols("state").Update(&PullRequest{State: PullMerged}); err != nil {
		t.Fatal(err)
	}
	op := &MergeOperation{ID: "contribution-retained-merge", PullID: p.ID, ComparisonID: c.ID, State: MergeFinalized, ResultNode: c.SourceNode}
	if _, err := x.Insert(op, &ReviewPublication{ID: op.ID, Started: true, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteHgRepository(ctx, t.TempDir(), p.SourceRepoID, func(context.Context, int64) error { return nil }, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]int64{"pull": p.ID, "review": first.ID} {
		if err := IndexContributionObject(ctx, kind, id); err != nil {
			t.Fatalf("retained %s: %v", kind, err)
		}
	}
	var memberships []ContributionRepository
	if err := x.Find(&memberships); err != nil || len(memberships) != 2 {
		t.Fatalf("retained merged history: %+v %v", memberships, err)
	}
}

func TestContributionEnqueueRejectedInsert(t *testing.T) {
	newTestDatabase(t)
	if _, err := x.Exec("CREATE TRIGGER reject_contribution_job BEFORE INSERT ON contribution_job BEGIN SELECT RAISE(ABORT, 'private rejection'); END"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueContribution(context.Background(), "repository", 999); !errors.Is(err, ErrContributionStorage) {
		t.Fatalf("failed insert without durable row returned %v", err)
	}
	var jobs []ContributionJob
	if err := x.Find(&jobs); err != nil || len(jobs) != 0 {
		t.Fatalf("unexpected jobs: %+v %v", jobs, err)
	}
}

func TestContributionIdempotentIndex(t *testing.T) {
	reload := newTestDatabase(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := EnqueueContribution(ctx, "repository", 7); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := PendingContributionJobs(ctx, 0, 100)
	if err != nil || len(jobs) != 1 || jobs[0].Requested != 3 {
		t.Fatalf("coalescing: %+v %v", jobs, err)
	}
	claimed := jobs[0]
	if err := EnqueueContribution(ctx, "repository", 7); err != nil {
		t.Fatal(err)
	}
	if err := CompleteContributionJob(ctx, claimed.ID, claimed.Requested); err != nil {
		t.Fatal(err)
	}
	reload()
	jobs, err = PendingContributionJobs(ctx, 0, 100)
	if err != nil || len(jobs) != 1 || jobs[0].Requested != 4 || jobs[0].Completed != 3 {
		t.Fatalf("racing publication lost: %+v %v", jobs, err)
	}
}

func TestContributionPublicationRollback(t *testing.T) {
	newTestDatabase(t)
	ctx := context.Background()
	r := &Repository{OwnerId: 1, Name: "rollback", LowerName: "rollback", State: RepositoryReady, StorageNonce: "identity"}
	if _, err := x.Insert(r); err != nil {
		t.Fatal(err)
	}
	stage := func(node string) int64 {
		t.Helper()
		g, err := BeginContributionGeneration(ctx, r.Id)
		if err != nil {
			t.Fatal(err)
		}
		if err = StageContributionEvents(ctx, r.Id, g, []ContributionEvent{{Kind: "changeset", Node: node, SourceKey: "changeset:" + node, OccurredAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	first := stage(strings.Repeat("1", 40))
	if err := PublishContributionGeneration(ctx, r.Id, first, r.StorageNonce, strings.Repeat("1", 40), 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	next := stage(strings.Repeat("2", 40))
	if _, err := x.Exec("CREATE TRIGGER fail_contribution_publish BEFORE INSERT ON contribution_repository BEGIN SELECT RAISE(ABORT, 'owned failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := PublishContributionGeneration(ctx, r.Id, next, r.StorageNonce, strings.Repeat("2", 40), 1, time.Now()); err == nil {
		t.Fatal("injected publish failure ignored")
	}
	var rows []ContributionRepository
	if err := x.Find(&rows); err != nil || len(rows) != 1 || rows[0].Generation != first {
		t.Fatalf("failed replacement exposed partial membership: %+v %v", rows, err)
	}
	state, err := ContributionIndexStatus(ctx, r.Id)
	if err != nil || state.Generation != first || !state.Updating {
		t.Fatalf("failed replacement published watermark: %+v %v", state, err)
	}
}

func TestContributionPrivateSQL(t *testing.T) {
	newTestDatabase(t)
	u := &User{Name: "private-proof", LowerName: "private-proof", Email: "PRIVATE-PROOF@example.invalid", IsActive: true}
	r := &Repository{OwnerId: 1, Name: "private", LowerName: "private", State: RepositoryReady}
	if _, err := x.Insert(u, r); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	x.Logger = xorm.NewSimpleLogger(&logged)
	x.ShowSQL = true
	x.ShowDebug = true
	x.ShowErr = true
	ctx := context.Background()
	code, err := CreateEmailVerification(ctx, u.Id, u.Email, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ConsumeEmailVerification(ctx, code, "", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifiedContributionAuthor(ctx, u.Email); err != nil {
		t.Fatal(err)
	}
	g, err := BeginContributionGeneration(ctx, r.Id)
	if err != nil {
		t.Fatal(err)
	}
	e := ContributionEvent{Kind: "changeset", Node: strings.Repeat("a", 40), SourceKey: "changeset:" + strings.Repeat("a", 40), AuthorEmail: "PRIVATE-AUTHOR@example.invalid", OccurredAt: time.Now()}
	if err = StageContributionEvents(ctx, r.Id, g, []ContributionEvent{e}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(logged.String()), "private-proof@example.invalid") || strings.Contains(logged.String(), e.AuthorEmail) || strings.Contains(logged.String(), code) {
		t.Fatal("private lookup material escaped SQL suppression")
	}
}

func TestContributionRebuild(t *testing.T) {
	reload := newTestDatabase(t)
	ctx := context.Background()
	r := &Repository{OwnerId: 1, Name: "index", LowerName: "index", State: RepositoryReady, StorageNonce: "owned"}
	if _, err := x.Insert(r); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	e := ContributionEvent{Kind: "changeset", SourceKey: "changeset:1111111111111111111111111111111111111111", Node: "1111111111111111111111111111111111111111", OccurredAt: now}
	g, err := BeginContributionGeneration(ctx, r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = StageContributionEvents(ctx, r.Id, g, []ContributionEvent{e}); err != nil {
		t.Fatal(err)
	}
	var visible []ContributionRepository
	if err = x.Find(&visible); err != nil || len(visible) != 0 {
		t.Fatalf("partial generation visible: %+v %v", visible, err)
	}
	if err = PublishContributionGeneration(ctx, r.Id, g, r.StorageNonce, e.Node, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err = BeginContributionGeneration(ctx, r.Id)
	if err != nil {
		t.Fatal(err)
	}
	reload()
	if err = x.Find(&visible); err != nil || len(visible) != 1 {
		t.Fatalf("unfinished replacement lost published membership: %+v %v", visible, err)
	}
	g, err = BeginContributionGeneration(ctx, r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = StageContributionEvents(ctx, r.Id, g, []ContributionEvent{e, e}); err != nil {
		t.Fatal(err)
	}
	if err = PublishContributionGeneration(ctx, r.Id, g, r.StorageNonce, e.Node, 0, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var events []ContributionEvent
	if err = x.Find(&events); err != nil || len(events) != 1 || !events[0].OccurredAt.Equal(now) {
		t.Fatalf("duplicate/date changed: %+v %v", events, err)
	}
}

func TestContributionCanonicalJobs(t *testing.T) {
	newTestDatabase(t)
	r := &Repository{OwnerId: 1, Name: "issues", LowerName: "issues", State: RepositoryReady}
	if _, err := x.Insert(r); err != nil {
		t.Fatal(err)
	}
	issue := &Issue{RepoId: r.Id, PosterId: 1, Name: "canonical"}
	if err := NewIssue(issue); err != nil {
		t.Fatal(err)
	}
	jobs, err := PendingContributionJobs(context.Background(), 0, 100)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != "issue" || jobs[0].ObjectID != issue.Id {
		t.Fatalf("committed issue lacks atomic job: %+v %v", jobs, err)
	}
}

func TestContributionFirstReview(t *testing.T) {
	_, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); err != nil {
		t.Fatal(err)
	}
	var rows []ContributionFirstReview
	if err := x.Find(&rows); err != nil || len(rows) != 1 {
		t.Fatalf("first formal review not independently retained: %+v %v", rows, err)
	}
	first := rows[0]
	if first.OccurredAt.Year() < 2020 {
		t.Fatalf("first review lost canonical timestamp: %+v", first)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewChangesRequested); err != nil {
		t.Fatal(err)
	}
	rows = nil
	if err := x.Find(&rows); err != nil || len(rows) != 1 || rows[0] != first {
		t.Fatalf("review edit duplicated/redated event: %+v %v", rows, err)
	}
	if err := IndexContributionObject(ctx, "review", first.ID); err != nil {
		t.Fatal(err)
	}
	// Recovery may discover an older immutable audit record after a new review
	// was indexed. Reconciliation must restore the original occurrence date.
	earliest := first.OccurredAt.Add(-24 * time.Hour)
	if err := firstContributionReview(x, reviewer.Id, p.ID, earliest); err != nil {
		t.Fatal(err)
	}
	if err := IndexContributionObject(ctx, "review", first.ID); err != nil {
		t.Fatal(err)
	}
	var event ContributionEvent
	if ok, err := x.Where("kind = ?", "review").Get(&event); err != nil || !ok || !event.OccurredAt.Equal(earliest) {
		t.Fatalf("backfill retained later review date: %+v %v", event, err)
	}
}

func TestContributionRepositoryDeletion(t *testing.T) {
	newTestDatabase(t)
	u := &User{Name: "delete-owner", LowerName: "delete-owner", Email: "delete@example.invalid", IsActive: true}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	r := &Repository{OwnerId: u.Id, Name: "deleted", LowerName: "deleted", State: RepositoryDeleting}
	fork := &Repository{OwnerId: u.Id, Name: "retained", LowerName: "retained", State: RepositoryReady}
	if _, err := x.Insert(r, fork); err != nil {
		t.Fatal(err)
	}
	e := &ContributionEvent{Kind: "changeset", SourceKey: "changeset:" + strings.Repeat("b", 40), Node: strings.Repeat("b", 40)}
	if _, err := x.Insert(e); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&ContributionRepository{EventID: e.ID, RepositoryID: r.Id, Generation: 1}, &ContributionRepository{EventID: e.ID, RepositoryID: fork.Id, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueContribution(context.Background(), "repository", r.Id); err != nil {
		t.Fatal(err)
	}
	if err := deleteRepositoryRows(r); err != nil {
		t.Fatal(err)
	}
	var members []ContributionRepository
	if err := x.Find(&members); err != nil || len(members) != 1 || members[0].RepositoryID != fork.Id {
		t.Fatalf("deleted/shared membership: %+v %v", members, err)
	}
	jobs, err := PendingContributionJobs(context.Background(), 0, 100)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("deleted job retained: %+v %v", jobs, err)
	}
}
