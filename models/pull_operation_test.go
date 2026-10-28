package models

import (
	"context"
	"errors"
	"testing"
)

func TestMergePartialBlocksFreshKeyAndComparison(t *testing.T) {
	owner, _, _, p, c := reviewFixture(t, false)
	ctx := context.Background()
	in := MergeAdmission{ID: "partial-operation", IdempotencyKey: "first-key", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode}
	op, err := BeginMergeOperation(ctx, owner.Id, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetMergePrepared(op.ID, c.SourceNode, "destination", "original artifact", "original expectation"); err != nil {
		t.Fatal(err)
	}
	if err := FailMerge(op.ID, "publication-failed"); err != nil {
		t.Fatal(err)
	}
	next, err := ReservePullComparison(ctx, owner.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	next.SourceNode, next.TargetNode, next.BaseNode, next.State, next.SnapshotComplete = c.SourceNode, c.TargetNode, c.BaseNode, "ready", true
	if err := CompletePullComparison(ctx, owner.Id, next); err != nil {
		t.Fatal(err)
	}
	in.ID, in.IdempotencyKey, in.ComparisonID = "different-operation", "different-key", next.ID
	if _, err := BeginMergeOperation(ctx, owner.Id, in); !errors.Is(err, ErrMergePartial) {
		t.Fatalf("same pins regenerated: %v", err)
	}
	stored, err := LoadMergeOperation(op.ID)
	if err != nil || stored.Prepared != "original artifact" || stored.Expectation != "original expectation" || stored.ResultNode != c.SourceNode {
		t.Fatalf("lost provenance: %+v %v", stored, err)
	}
}

func TestMergePublicationPolicyAndLookupFailure(t *testing.T) {
	owner, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	op, err := BeginMergeOperation(ctx, owner.Id, MergeAdmission{ID: "policy-operation", IdempotencyKey: "policy-key", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetMergePrepared(op.ID, c.SourceNode, "", "prepared", "expectation"); err != nil {
		t.Fatal(err)
	}
	check := func() error {
		return ValidateMergePublication(ctx, owner.Id, op.ID, p.SourceRepoID, p.TargetRepoID, c.SourceNode, c.TargetNode, c.SourceNode)
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	if err := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewChangesRequested); err != nil {
		t.Fatal(err)
	}
	if err := check(); !errors.Is(err, ErrMergeBlocked) {
		t.Fatalf("live review not checked: %v", err)
	}
	if err := ValidateMergePublication(ctx, owner.Id, "generic-operation", p.SourceRepoID, p.TargetRepoID, c.SourceNode, c.TargetNode, c.SourceNode); err != nil {
		t.Fatalf("generic hosting operation denied: %v", err)
	}
	if _, err := x.Exec("DROP TABLE merge_operation"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMergePublication(ctx, owner.Id, "generic-operation", p.SourceRepoID, p.TargetRepoID, c.SourceNode, c.TargetNode, c.SourceNode); err == nil {
		t.Fatal("lookup failure treated as absent PR operation")
	}
}
