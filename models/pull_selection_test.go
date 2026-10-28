package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/gogits/gogs/modules/hg"
	"strings"
	"testing"
)

func TestPullSourceExactNameBeyondCatalog(t *testing.T) {
	owner, author, target, source, _ := pullFixture(t, true)
	ctx := context.Background()
	for i := 0; i < 105; i++ {
		newAccessTestRepo(t, author, fmt.Sprintf("newer-%d", i), false)
	}
	first, err := ListAccessibleRepositories(ctx, author.Id, CatalogQuery{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range first.Repositories {
		if r.Id == source.Id {
			t.Fatal("fixture source must be beyond first100")
		}
	}
	name := author.Name + "/" + source.Name
	id, err := ResolvePullSource(ctx, author.Id, target.Id, name)
	if err != nil || id != source.Id {
		t.Fatalf("exact source: %d %v", id, err)
	}
	if _, err := ResolvePullSource(ctx, owner.Id, target.Id, name); err == nil {
		t.Fatal("nonwriter resolved source")
	}
	if _, err := ResolvePullSource(ctx, author.Id, target.Id, author.Name+"/newer-0"); err == nil {
		t.Fatal("unrelated source resolved")
	}
	// Keep destination readable while revoking a private intermediate ancestor.
	parent := newAccessTestRepo(t, owner, "intermediate", true)
	x.Insert(&ForkRelation{RepositoryID: parent.Id, SourceID: target.Id, FamilyID: target.Id})
	x.Id(source.Id).Cols("source_id").Update(&ForkRelation{SourceID: parent.Id})
	if _, err := ResolvePullSource(ctx, author.Id, target.Id, name); err == nil {
		t.Fatal("private ancestor bypass")
	}
}

func TestPullMergedComparisonFrozen(t *testing.T) {
	_, author, _, _, input := pullFixture(t, false)
	ctx := context.Background()
	p, err := CreatePullRecord(ctx, author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReservePullComparison(ctx, author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.Id(p.ID).Cols("state").Update(&PullRequest{State: PullMerged}); err != nil {
		t.Fatal(err)
	}
	if _, err = ReservePullComparison(ctx, author.Id, p.ID); !errors.Is(err, ErrPullState) {
		t.Fatalf("merged refresh: %v", err)
	}
	after, err := GetPullRecordByID(ctx, author.Id, p.ID)
	if err != nil || after.CurrentComparisonID != c.ID {
		t.Fatalf("merged comparison replaced: %+v %v", after, err)
	}
	count, err := x.Where("pull_id = ?", p.ID).Count(new(PullComparison))
	if err != nil || count != 1 {
		t.Fatalf("merged refresh allocated: %d %v", count, err)
	}
}

func TestPullSelectorCASAndAuthority(t *testing.T) {
	owner, author, _, _, in := pullFixture(t, false)
	ctx := context.Background()
	p, err := CreatePullRecord(ctx, author.Id, in)
	if err != nil {
		t.Fatal(err)
	}
	src, dst, _ := PullSelections(p)
	changed := src
	changed.Ref.Name = strings.Repeat("3", 40)
	if err := ReselectPullRecord(ctx, owner.Id, p, changed, dst); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal("target writer changed source", err)
	}
	if err := ReselectPullRecord(ctx, author.Id, p, changed, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := ReservePullComparison(ctx, author.Id, p.ID, p); !errors.Is(err, ErrPullConflict) {
		t.Fatal("old selector refresh reserved", err)
	}
	if err := ReselectPullRecord(ctx, author.Id, p, src, dst); !errors.Is(err, ErrPullConflict) {
		t.Fatal("old selector form saved", err)
	}
	fresh, _ := GetPullRecordByID(ctx, author.Id, p.ID)
	wrong := dst
	wrong.Ref = hg.RefSelection{Kind: hg.RefBookmark, Name: "elsewhere"}
	if err := ReselectPullRecord(ctx, author.Id, fresh, changed, wrong); !errors.Is(err, ErrPullInvalid) {
		t.Fatal("destination identity changed", err)
	}
}

func TestPullNeedsReviewTracksPins(t *testing.T) {
	_, author, reviewer, p, c := reviewFixture(t, false)
	ctx := context.Background()
	if e := AssignPullReviewers(ctx, author.Id, p.ID, []int64{reviewer.Id}); e != nil {
		t.Fatal(e)
	}
	check := func(want int) {
		t.Helper()
		page, e := ListPullRecords(ctx, reviewer.Id, PullQuery{Filter: "needs_review"})
		if e != nil || page.Total != want {
			t.Fatalf("needs-review = %d, want %d: %v", page.Total, want, e)
		}
	}
	check(1)
	if e := SubmitPullReview(ctx, reviewer.Id, p.ID, c.ID, ReviewApproved); e != nil {
		t.Fatal(e)
	}
	check(0)
	next, e := ReservePullComparison(ctx, author.Id, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	next.SourceNode, next.TargetNode, next.BaseNode, next.State, next.SnapshotComplete = c.SourceNode, c.TargetNode, c.BaseNode, "ready", true
	if e := CompletePullComparison(ctx, author.Id, next); e != nil {
		t.Fatal(e)
	}
	check(0)
	if e := SubmitPullReview(ctx, reviewer.Id, p.ID, next.ID, ReviewChangesRequested); e != nil {
		t.Fatal(e)
	}
	check(0)
	changed, e := ReservePullComparison(ctx, author.Id, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	changed.SourceNode, changed.TargetNode, changed.BaseNode, changed.State, changed.SnapshotComplete = hg.NodeID(strings.Repeat("4", 40)), c.TargetNode, c.BaseNode, "ready", true
	if e := CompletePullComparison(ctx, author.Id, changed); e != nil {
		t.Fatal(e)
	}
	check(1)
	if e := SetPullRecordState(ctx, author.Id, p.ID, PullDeclined); e != nil {
		t.Fatal(e)
	}
	check(0)
}
