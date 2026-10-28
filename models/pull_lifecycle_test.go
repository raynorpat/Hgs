package models

import (
	"context"
	"errors"
	"testing"
)

func TestCollaborationLifecycleMergeFence(t *testing.T) {
	for _, state := range []MergeState{MergeUnstarted, MergePrepared, MergePublished} {
		t.Run(string(state), func(t *testing.T) {
			owner, _, _, p, c := reviewFixture(t, false)
			op, err := BeginMergeOperation(context.Background(), owner.Id, MergeAdmission{ID: "lifecycle-operation", IdempotencyKey: "lifecycle-key", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.Id(op.ID).Cols("state").Update(&MergeOperation{State: state}); err != nil {
				t.Fatal(err)
			}
			// Native completion releases its reservations before PR finalization.
			if _, err := x.Insert(&ReviewPublication{ID: op.ID, Started: true, Complete: true}); err != nil {
				t.Fatal(err)
			}
			if err := tombstoneReviewSafe(p.SourceRepoID); !errors.Is(err, ErrReviewReserved) {
				t.Fatalf("recoverable %s admitted deletion: %v", state, err)
			}
			if _, err := GetRepositoryById(p.SourceRepoID); err != nil {
				t.Fatalf("busy deletion hid source: %v", err)
			}
		})
	}
}

func TestCollaborationPurgeAndRetryRetainsDenial(t *testing.T) {
	owner, author, reviewer, p, c := reviewFixture(t, true)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddPullComment(ctx, author.Id, p.ID, "private comment", nil); err != nil {
		t.Fatal(err)
	}
	op, err := BeginMergeOperation(ctx, owner.Id, MergeAdmission{ID: "failed-retained-operation", IdempotencyKey: "failed-retained-key", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetMergePrepared(op.ID, c.SourceNode, "private bookmark", "original prepared", "original expectation"); err != nil {
		t.Fatal(err)
	}
	if err := FailMerge(op.ID, "publication-failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&ReviewPublication{ID: op.ID, Started: true, Failed: true}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	drain := func(context.Context, int64) error { return nil }
	interrupted := errors.New("cleanup interrupted")
	if err := DeleteHgRepository(ctx, root, p.SourceRepoID, drain, func(context.Context, int64) error { return interrupted }); !errors.Is(err, interrupted) {
		t.Fatalf("cleanup failure: %v", err)
	}
	var retained PullRequest
	if _, err := x.Id(p.ID).Get(&retained); err != nil || retained.Title == "" || retained.SourceDeleted {
		t.Fatalf("owner rows lost before cleanup: %+v %v", retained, err)
	}
	if _, err := GetPullRecordByID(ctx, owner.Id, p.ID); err == nil {
		t.Fatal("deleting endpoint granted tombstone early")
	}
	if err := DeleteHgRepository(ctx, root, p.SourceRepoID, drain, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var stored PullRequest
	if _, err := x.Id(p.ID).Get(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.SourceDeleted || stored.State != PullDeclined || stored.Title != "" || stored.Body != "" || stored.SourceSelection != "" || stored.TargetSelection != "" || stored.AuthorID != 0 || stored.CurrentComparisonID != 0 || !stored.Created.Equal(retained.Created) || !stored.Updated.Equal(retained.Updated) {
		t.Fatalf("unmerged private fields not purged: %+v", stored)
	}
	for _, bean := range []interface{}{new(PullComment), new(PullReview), new(PullEvent), new(PullComparison)} {
		if n, err := x.Where("pull_id = ?", p.ID).Count(bean); err != nil || n != 0 {
			t.Fatalf("%T payload retained: %d %v", bean, n, err)
		}
	}
	if _, err := BeginMergeOperation(ctx, owner.Id, MergeAdmission{ID: "new-after-delete", IdempotencyKey: "new-after-delete", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode}); err == nil {
		t.Fatal("deleted source re-admitted merge")
	}
	if err := DeleteHgRepository(ctx, root, p.TargetRepoID, drain, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if rows, err := RepositoryPullDeletion(ctx, p.SourceRepoID); err != nil || len(rows) != 0 {
		t.Fatalf("completed cleanup not idempotent: %+v %v", rows, err)
	}
	storedOp, err := LoadMergeOperation(op.ID)
	if err != nil || storedOp.State != MergeFailed || storedOp.Prepared != "original prepared" || storedOp.Expectation != "original expectation" || storedOp.ResultNode != c.SourceNode {
		t.Fatalf("terminal provenance erased: %+v %v", storedOp, err)
	}
	if err := ValidateMergePublication(ctx, owner.Id, op.ID, p.SourceRepoID, p.TargetRepoID, c.SourceNode, c.TargetNode, c.SourceNode); err == nil {
		t.Fatal("denial binding became generic publication")
	}
}

func TestCollaborationMetadataAdmission(t *testing.T) {
	owner, _, _, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if _, err := BeginMergeOperation(ctx, owner.Id, MergeAdmission{ID: "busy-metadata", IdempotencyKey: "busy-metadata", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode}); err != nil {
		t.Fatal(err)
	}
	if err := ChangeRepositoryName(p.SourceRepoID, "rename-during-merge"); !errors.Is(err, ErrReviewReserved) {
		t.Fatalf("rename raced publication: %v", err)
	}
	if err := FailMerge("busy-metadata", "preparation-failed"); err != nil {
		t.Fatal(err)
	}
	if err := ChangeRepositoryName(p.SourceRepoID, "stable-id-renamed"); err != nil {
		t.Fatal(err)
	}
	source, err := GetRepositoryById(p.SourceRepoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.GetOwner(); err != nil {
		t.Fatal(err)
	}
	recipient := newAccessTestUser(t, "transfer-recipient", true)
	if err := TransferOwnership(source.Owner, recipient.Name, source); err != nil {
		t.Fatal(err)
	}
	current, err := GetPullRecordByID(ctx, owner.Id, p.ID)
	if err != nil || current.SourceRepoID != p.SourceRepoID || current.TargetRepoID != p.TargetRepoID {
		t.Fatalf("metadata changed immutable endpoints: %+v %v", current, err)
	}
}

func TestCollaborationLifecycleDeniesDatabaseUncertainty(t *testing.T) {
	_, _, _, p, _ := reviewFixture(t, false)
	if _, err := x.Exec("DROP TABLE merge_operation"); err != nil {
		t.Fatal(err)
	}
	if err := tombstoneReviewSafe(p.SourceRepoID); err == nil {
		t.Fatal("database uncertainty admitted deletion")
	}
	if _, err := GetRepositoryById(p.SourceRepoID); err != nil {
		t.Fatalf("failed query hid source: %v", err)
	}
}

func TestCollaborationPrivateTransferAndCorruptAncestry(t *testing.T) {
	_, _, _, p, _ := reviewFixture(t, true)
	source, err := GetRepositoryById(p.SourceRepoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.GetOwner(); err != nil {
		t.Fatal(err)
	}
	recipient := newAccessTestUser(t, "private-recipient", true)
	if err := TransferOwnership(source.Owner, recipient.Name, source); !errors.Is(err, ErrForkPolicy) {
		t.Fatalf("transfer widened private ancestor: %v", err)
	}
	if err := AddAccess(&Access{UserId: recipient.Id, RepoId: p.TargetRepoID, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	if err := TransferOwnership(source.Owner, recipient.Name, source); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&ForkRelation{RepositoryID: p.TargetRepoID, SourceID: p.SourceRepoID, FamilyID: p.TargetRepoID}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetPullRecordByID(context.Background(), recipient.Id, p.ID); !errors.Is(err, ErrPullNotExist) {
		t.Fatalf("cyclic ancestry read: %v", err)
	}
	if err := tombstoneReviewSafe(p.SourceRepoID); err == nil {
		t.Fatal("cyclic dependent admitted deletion")
	}
}

func TestCollaborationDeleteUserRetiresAtomically(t *testing.T) {
	_, author, reviewer, p, c := reviewFixture(t, true)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewChangesRequested); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Exec("CREATE TRIGGER refuse_retirement BEFORE INSERT ON pull_event WHEN NEW.kind = 'review-authority-lost' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser(reviewer); err == nil {
		t.Fatal("account deletion committed without review audit")
	}
	if _, err := GetUserById(reviewer.Id); err != nil {
		t.Fatalf("account did not roll back: %v", err)
	}
	if a, err := RepositoryAccessFor(reviewer.Id, p.TargetRepoID); err != nil || !a.Write {
		t.Fatalf("authority did not roll back: %+v %v", a, err)
	}
	if _, err := x.Exec("DROP TRIGGER refuse_retirement"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser(reviewer); err != nil {
		t.Fatal(err)
	}
	var r PullReview
	if _, err := x.Where("pull_id = ? AND reviewer_id = ?", p.ID, reviewer.Id).Get(&r); err != nil || r.State != ReviewInactive {
		t.Fatalf("review not retired: %+v %v", r, err)
	}
}

func TestCollaborationVisibilityComparisonBoundaries(t *testing.T) {
	_, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	pending, err := ReservePullComparison(ctx, author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := GetRepositoryById(p.SourceRepoID)
	if err != nil {
		t.Fatal(err)
	}
	source.IsPrivate = true
	if err := UpdateRepository(source); err != nil {
		t.Fatal(err)
	}
	pending.SourceNode, pending.TargetNode, pending.BaseNode, pending.State, pending.SnapshotComplete = c.SourceNode, c.TargetNode, c.BaseNode, "ready", true
	if err := CompletePullComparison(ctx, author.Id, pending); err == nil {
		t.Fatal("visibility race completed a ready comparison")
	}
	if _, err := ReservePullComparison(ctx, author.Id, p.ID); err == nil {
		t.Fatal("mismatched privacy reserved comparison")
	}
	// An existing ready pointer must not become review-ready merely because
	// the actor retains both reads after the visibility transition.
	if _, err := x.Id(p.ID).Cols("current_comparison_id").Update(&PullRequest{CurrentComparisonID: c.ID}); err != nil {
		t.Fatal(err)
	}
	page, err := ListPullRecords(ctx, reviewer.Id, PullQuery{Filter: "needs_review"})
	if err != nil || page.Total != 0 {
		t.Fatalf("mismatched privacy needs-review: %+v %v", page, err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); err == nil {
		t.Fatal("mismatched privacy review accepted")
	}
}

func TestCollaborationAcceptedProofAndTerminalDenial(t *testing.T) {
	owner, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); err != nil {
		t.Fatal(err)
	}
	op, err := BeginMergeOperation(ctx, owner.Id, MergeAdmission{ID: "retained-operation", IdempotencyKey: "retained-key", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetMergePrepared(op.ID, c.SourceNode, "", "original prepared", "original expectation"); err != nil {
		t.Fatal(err)
	}
	if err := SetMergePublished(op.ID); err != nil {
		t.Fatal(err)
	}
	if err := FinalizeMerge(op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Insert(&ReviewPublication{ID: op.ID, Started: true, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := tombstoneReviewSafe(p.SourceRepoID); err != nil {
		t.Fatalf("confirmed finalized fact blocked: %v", err)
	}
	rows, err := RepositoryPullDeletion(ctx, p.SourceRepoID)
	if err != nil || len(rows) != 1 || rows[0].AcceptedComparisonID != c.ID {
		t.Fatalf("accepted proof: %+v %v", rows, err)
	}
	if err := DeleteHgRepository(ctx, t.TempDir(), p.SourceRepoID, func(context.Context, int64) error { return nil }, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := AddAccess(&Access{UserId: reviewer.Id, RepoId: p.TargetRepoID, Mode: WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if state := reviewState(t, p.ID, reviewer.Id); state != ReviewApproved {
		t.Fatalf("retained accepted transcript changed after unrelated grant: %s", state)
	}
}
