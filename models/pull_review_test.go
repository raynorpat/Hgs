package models

import (
	"context"
	"github.com/gogits/gogs/modules/hg"
	"strings"
	"testing"
)

func reviewFixture(t *testing.T, private bool) (*User, *User, *User, PullRequest, PullComparison) {
	owner, author, target, source, input := pullFixture(t, private)
	reviewer := newAccessTestUser(t, "reviewer", true)
	for _, a := range []*Access{{UserId: reviewer.Id, RepoId: target.Id, Mode: WRITABLE}, {UserId: reviewer.Id, RepoId: source.Id, Mode: READABLE}, {UserId: owner.Id, RepoId: source.Id, Mode: READABLE}} {
		if err := AddAccess(a); err != nil {
			t.Fatal(err)
		}
	}
	p, err := CreatePullRecord(context.Background(), author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReservePullComparison(context.Background(), author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.SourceNode = hg.NodeID(strings.Repeat("1", 40))
	c.TargetNode = hg.NodeID(strings.Repeat("2", 40))
	c.BaseNode = hg.NodeID(strings.Repeat("3", 40))
	c.State = "ready"
	c.SnapshotComplete = true
	if err := CompletePullComparison(context.Background(), author.Id, c); err != nil {
		t.Fatal(err)
	}
	return owner, author, reviewer, p, c
}
func reviewState(t *testing.T, pullID, userID int64) ReviewState {
	t.Helper()
	var r PullReview
	if ok, err := x.Where("pull_id = ? AND reviewer_id = ?", pullID, userID).Get(&r); err != nil || !ok {
		t.Fatalf("missing review: %v", err)
	}
	return r.State
}
func TestPullReviewAuthority(t *testing.T) {
	owner, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	reader := newAccessTestUser(t, "reader", true)
	for _, id := range []int64{author.Id, reader.Id, reviewer.Id} {
		if err := SubmitPullReview(ctx, id, p.ID, c.ID, ReviewApproved); err == nil {
			t.Fatalf("unassigned/self review %d", id)
		}
	}
	if err := AssignPullReviewers(ctx, reader.Id, p.ID, []int64{reader.Id}); err == nil {
		t.Fatal("reader assigned self")
	}
	for _, id := range []int64{author.Id, reader.Id} {
		if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{id}); err == nil {
			t.Fatal("ineligible assigned")
		}
	}
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); err != nil {
		t.Fatal(err)
	}
	page, err := ListPullRecords(ctx, reviewer.Id, PullQuery{Filter: "assigned"})
	if err != nil || page.Total != 1 {
		t.Fatalf("assigned page %+v %v", page, err)
	}
	if err := AssignPullReviewers(ctx, owner.Id, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewChangesRequested); err == nil {
		t.Fatal("unassigned review")
	}
}
func TestPullReviewBlocksCannotBeBypassed(t *testing.T) {
	owner, author, r, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{r.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, r.Id, p.ID, c.ID, ReviewChangesRequested); err != nil {
		t.Fatal(err)
	}
	if err := AssignPullReviewers(ctx, author.Id, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := reviewState(t, p.ID, r.Id); got != ReviewChangesRequested {
		t.Fatalf("unassignment cleared block: %s", got)
	}
	if err := DismissPullReview(ctx, author.Id, p.ID, r.Id, "ignore"); err == nil {
		t.Fatal("nonadmin dismissed")
	}
	if err := DismissPullReview(ctx, owner.Id, p.ID, r.Id, " "); err == nil {
		t.Fatal("reasonless dismissal")
	}
	if err := DismissPullReview(ctx, owner.Id, p.ID, r.Id, "superseded discussion"); err != nil {
		t.Fatal(err)
	}
	if got := reviewState(t, p.ID, r.Id); got != ReviewInactive {
		t.Fatal(got)
	}
	var events []PullEvent
	if err := x.Where("pull_id = ? AND kind = ?", p.ID, "review-dismissed").Find(&events); err != nil || len(events) != 1 || events[0].Reason != "superseded discussion" {
		t.Fatalf("audit %+v %v", events, err)
	}
}
func TestPullReviewRevisionAndAccessLoss(t *testing.T) {
	for _, kind := range []string{"target", "source", "ancestor", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			_, author, r, p, c := reviewFixture(t, true)
			ctx := context.Background()
			if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{r.Id}); err != nil {
				t.Fatal(err)
			}
			if err := SubmitPullReview(ctx, r.Id, p.ID, c.ID, ReviewChangesRequested); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "target":
				if err := AddAccess(&Access{UserId: r.Id, RepoId: p.TargetRepoID, Mode: READABLE}); err != nil {
					t.Fatal(err)
				}
				if err := AddAccess(&Access{UserId: r.Id, RepoId: p.TargetRepoID, Mode: WRITABLE}); err != nil {
					t.Fatal(err)
				}
			case "source":
				if err := DeleteAccess(&Access{UserId: r.Id, RepoId: p.SourceRepoID}); err != nil {
					t.Fatal(err)
				}
				if err := AddAccess(&Access{UserId: r.Id, RepoId: p.SourceRepoID, Mode: READABLE}); err != nil {
					t.Fatal(err)
				}
			case "ancestor":
				if err := DeleteAccess(&Access{UserId: r.Id, RepoId: p.TargetRepoID}); err != nil {
					t.Fatal(err)
				}
				if err := AddAccess(&Access{UserId: r.Id, RepoId: p.TargetRepoID, Mode: WRITABLE}); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				r.IsActive = false
				if err := UpdateUser(r); err != nil {
					t.Fatal(err)
				}
				r.IsActive = true
				if err := UpdateUser(r); err != nil {
					t.Fatal(err)
				}
			}
			if got := reviewState(t, p.ID, r.Id); got != ReviewInactive {
				t.Fatalf("regrant revived old block: %s", got)
			}
			if err := SubmitPullReview(ctx, r.Id, p.ID, c.ID, ReviewApproved); err != nil {
				t.Fatal(err)
			}
			next, err := ReservePullComparison(ctx, author.Id, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			next.SourceNode = hg.NodeID(strings.Repeat("4", 40))
			next.TargetNode = c.TargetNode
			next.BaseNode = c.BaseNode
			next.State = "ready"
			next.SnapshotComplete = true
			if err := CompletePullComparison(ctx, author.Id, next); err != nil {
				t.Fatal(err)
			}
			if got := reviewState(t, p.ID, r.Id); got != ReviewInactive {
				t.Fatalf("approval survived movement: %s", got)
			}
			if err := SubmitPullReview(ctx, r.Id, p.ID, c.ID, ReviewApproved); err == nil {
				t.Fatal("stale comparison approved")
			}
			if err := SubmitPullReview(ctx, r.Id, p.ID, next.ID, ReviewChangesRequested); err != nil {
				t.Fatal(err)
			}
			newer, err := ReservePullComparison(ctx, author.Id, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			newer.SourceNode = hg.NodeID(strings.Repeat("5", 40))
			newer.TargetNode = c.TargetNode
			newer.BaseNode = c.BaseNode
			newer.State = "ready"
			newer.SnapshotComplete = true
			if err := CompletePullComparison(ctx, author.Id, newer); err != nil {
				t.Fatal(err)
			}
			if got := reviewState(t, p.ID, r.Id); got != ReviewChangesRequested {
				t.Fatalf("movement cleared block: %s", got)
			}
		})
	}
}
func TestPullReviewAlternateGrants(t *testing.T) {
	owner, author, r, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{owner.Id, r.Id}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{owner.Id, r.Id} {
		if err := SubmitPullReview(ctx, id, p.ID, c.ID, ReviewChangesRequested); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []*Access{{UserId: owner.Id, RepoId: p.TargetRepoID}, {UserId: r.Id, RepoId: p.SourceRepoID}} {
		if err := DeleteAccess(a); err != nil {
			t.Fatal(err)
		}
	}
	other := newAccessTestUser(t, "other", true)
	if err := AddAccess(&Access{UserId: other.Id, RepoId: p.TargetRepoID, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{owner.Id, r.Id} {
		if got := reviewState(t, p.ID, id); got != ReviewChangesRequested {
			t.Fatalf("effective grant preserved, block lost: %s", got)
		}
	}
}

func TestPullCreateWithReviewersAndAssignedFilter(t *testing.T) {
	owner, author, target, source, input := pullFixture(t, false)
	ctx := context.Background()
	reviewer := newAccessTestUser(t, "reviewer", true)
	reader := newAccessTestUser(t, "reader", true)
	for _, a := range []*Access{{UserId: reviewer.Id, RepoId: target.Id, Mode: WRITABLE}, {UserId: reviewer.Id, RepoId: source.Id, Mode: READABLE}, {UserId: owner.Id, RepoId: source.Id, Mode: READABLE}} {
		if err := AddAccess(a); err != nil {
			t.Fatal(err)
		}
	}
	input.ReviewerIDs = []int64{reader.Id}
	if _, err := CreatePullRecord(ctx, author.Id, input); err == nil {
		t.Fatal("read-only invitee accepted as reviewer")
	}
	input.ReviewerIDs = []int64{reviewer.Id}
	p, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := ListPullRecords(ctx, reviewer.Id, PullQuery{Filter: "assigned"}); err != nil || page.Total != 1 || page.Requests[0].ID != p.ID {
		t.Fatalf("assigned page %+v %v", page, err)
	}
	if page, err := ListPullRecords(ctx, reader.Id, PullQuery{Filter: "assigned"}); err != nil || page.Total != 0 {
		t.Fatalf("unassigned page %+v %v", page, err)
	}
	if err := DeleteAccess(&Access{UserId: reviewer.Id, RepoId: target.Id}); err != nil {
		t.Fatal(err)
	}
	if page, err := ListPullRecords(ctx, reviewer.Id, PullQuery{Filter: "assigned"}); err != nil || page.Total != 0 {
		t.Fatalf("assignment survived authority loss %+v %v", page, err)
	}
}
