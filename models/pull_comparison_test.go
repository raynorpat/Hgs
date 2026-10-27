package models

import (
	"context"
	"errors"
	"github.com/gogits/gogs/modules/hg"
	"strings"
	"testing"
)

// Reservation must revoke the old current readiness before native work; an
// out-of-order refresh must not overwrite a newer reserved version.
func TestPullComparisonReservationAndPublication(t *testing.T) {
	_, author, _, _, input := pullFixture(t, false)
	ctx := context.Background()
	p, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ReservePullComparison(ctx, author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReservePullComparison(ctx, author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Created.IsZero() {
		t.Fatal("reservation lost creation time")
	}
	if first.Revision != 1 || second.Revision != 2 || first.ArtifactID == second.ArtifactID || len(first.ArtifactID) != 64 || first.OwnershipNonce == "" {
		t.Fatalf("bad reservations %+v %+v", first, second)
	}
	p, err = GetPullRecordByID(ctx, author.Id, p.ID)
	if err != nil || p.CurrentComparisonID != second.ID {
		t.Fatalf("pending was not current %+v %v", p, err)
	}
	finish := func(c PullComparison) PullComparison {
		c.SourceNode = hg.NodeID(strings.Repeat("1", 40))
		c.TargetNode = hg.NodeID(strings.Repeat("2", 40))
		c.BaseNode = hg.NodeID(strings.Repeat("3", 40))
		c.SnapshotComplete = true
		c.State = "ready"
		return c
	}
	if err := CompletePullComparison(ctx, author.Id, finish(first)); !errors.Is(err, ErrPullConflict) {
		t.Fatalf("stale refresh won %v", err)
	}
	bad := finish(second)
	bad.ArtifactID = first.ArtifactID
	if err := CompletePullComparison(ctx, author.Id, bad); err == nil {
		t.Fatal("ownership spoof accepted")
	}
	if err := CompletePullComparison(ctx, author.Id, finish(second)); err != nil {
		t.Fatal(err)
	}
	bad = finish(second)
	bad.SourceNode = bad.TargetNode
	if err := CompletePullComparison(ctx, author.Id, bad); err == nil {
		t.Fatal("published pins were mutable")
	}
	if _, err := GetPullComparison(ctx, author.Id, p.ID+1, second.ID); err == nil {
		t.Fatal("comparison cross-pull leak")
	}
}

func TestPullComparisonPublicationRechecksAuthority(t *testing.T) {
	_, author, target, _, input := pullFixture(t, true)
	ctx := context.Background()
	p, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReservePullComparison(ctx, author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.SourceNode = hg.NodeID(strings.Repeat("1", 40))
	c.TargetNode = hg.NodeID(strings.Repeat("2", 40))
	c.BaseNode = hg.NodeID(strings.Repeat("3", 40))
	c.SnapshotComplete = true
	c.State = "ready"
	if _, err := x.Where("user_id = ? AND repo_id = ?", author.Id, target.Id).Delete(new(Access)); err != nil {
		t.Fatal(err)
	}
	if err := CompletePullComparison(ctx, author.Id, c); err == nil {
		t.Fatal("revoked reader published")
	}
	if _, err := GetPullComparison(ctx, author.Id, p.ID, c.ID); err == nil {
		t.Fatal("revoked reader read")
	}
}
