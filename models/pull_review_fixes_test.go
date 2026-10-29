package models

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
)

func TestPullDestinationKinds(t *testing.T) {
	_, author, _, _, p := pullFixture(t, false)
	for _, kind := range []hg.RefKind{hg.RefTag, hg.RefNode} {
		selection, _ := json.Marshal(hg.ReviewSelection{Ref: hg.RefSelection{Kind: kind, Name: strings.Repeat("a", 40)}})
		p.TargetSelection = string(selection)
		if _, err := CreatePullRecord(context.Background(), author.Id, p); !errors.Is(err, ErrPullInvalid) {
			t.Errorf("destination %s: %v", kind, err)
		}
	}
	p.TargetSelection = `{"Ref":{"Kind":"branch","Name":"default"}}`
	for _, kind := range []hg.RefKind{hg.RefTag, hg.RefNode} {
		raw, _ := json.Marshal(hg.ReviewSelection{Ref: hg.RefSelection{Kind: kind, Name: strings.Repeat("a", 40)}})
		p.SourceSelection = string(raw)
		if _, err := CreatePullRecord(context.Background(), author.Id, p); err != nil {
			t.Errorf("source %s rejected: %v", kind, err)
		}
	}
}

func TestPullAuditBoundAndPermission(t *testing.T) {
	_, author, _, _, input := pullFixture(t, true)
	p, err := CreatePullRecord(context.Background(), author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 105; n++ {
		if err := pullEvent(x, p.ID, author.Id, 0, 0, "metadata-edited", ""); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ListPullEvents(context.Background(), author.Id, p.ID)
	if err != nil || len(rows) != 100 || rows[0].ID != 6 || rows[99].ID != 105 {
		t.Fatalf("audit bound/order %d %v", len(rows), err)
	}
	if rows, err := ListPullEvents(context.Background(), 0, p.ID); err == nil || len(rows) != 0 {
		t.Fatal("private audit disclosed")
	}
}

func TestPullStateAndMetadataAudit(t *testing.T) {
	_, author, _, _, input := pullFixture(t, false)
	p, err := CreatePullRecord(context.Background(), author.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ListPullEvents(context.Background(), author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []PullState{PullDeclined, PullDeclined, PullOpen} {
		if err := SetPullRecordState(context.Background(), author.Id, p.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 2; n++ {
		if err := EditPullRecord(context.Background(), author.Id, p.ID, "Changed", "Body"); err != nil {
			t.Fatal(err)
		}
	}
	after, err := ListPullEvents(context.Background(), author.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+3 {
		t.Fatalf("events before=%d after=%d, want three real mutations", len(before), len(after))
	}
	for _, event := range after[len(before):] {
		if event.ActorID != author.Id || event.Created.IsZero() {
			t.Fatalf("missing audit identity: %+v", event)
		}
	}
	if _, err := x.Exec("CREATE TRIGGER reject_pull_audit BEFORE INSERT ON pull_event BEGIN SELECT RAISE(ABORT, 'fixture audit failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := SetPullRecordState(context.Background(), author.Id, p.ID, PullDeclined); err == nil {
		t.Fatal("state committed without audit")
	}
	if err := EditPullRecord(context.Background(), author.Id, p.ID, "Lost edit", "Lost body"); err == nil {
		t.Fatal("metadata committed without audit")
	}
	stored, err := GetPullRecordByID(context.Background(), author.Id, p.ID)
	if err != nil || stored.State != PullOpen || stored.Title != "Changed" {
		t.Fatalf("mutation escaped rollback %+v %v", stored, err)
	}
}

func TestPullCommentLiteralCode(t *testing.T) {
	for _, body := range []string{"```html\n<script>alert(1)</script>\n```", "Use `javascript:` as a malicious example", "`<a onclick=evil()>`"} {
		if !validPullComment(body) {
			t.Errorf("literal review rejected: %s", body)
		}
	}
	for _, body := range []string{"<script>alert(1)</script>", "<img src=x onerror=alert(1)>", "[click](javascript:alert(1))"} {
		rendered := string(base.RenderMarkdown([]byte(body), "/owner/repo"))
		if strings.Contains(rendered, "<script") || strings.Contains(rendered, "onerror=") || strings.Contains(rendered, `href="javascript:`) {
			t.Fatalf("unsafe rendered review: %s", rendered)
		}
	}
}

func TestPullSourceOperationalError(t *testing.T) {
	_, author, target, _, _ := pullFixture(t, false)
	if _, err := x.Exec("DROP TABLE fork_relation"); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePullSource(context.Background(), author.Id, target.Id, "pull-author/source")
	if err == nil || errors.Is(err, ErrPullNotExist) || errors.Is(err, ErrPullInvalid) {
		t.Fatalf("operational lookup failure disguised: %v", err)
	}
}

func TestPullImmutableDestinationMutationAdmission(t *testing.T) {
	for _, kind := range []hg.RefKind{hg.RefTag, hg.RefNode} {
		t.Run(string(kind), func(t *testing.T) {
			owner, _, _, p, c := reviewFixture(t, false)
			raw, _ := json.Marshal(hg.ReviewSelection{Ref: hg.RefSelection{Kind: kind, Name: strings.Repeat("a", 40)}})
			if _, err := x.Id(p.ID).Cols("target_selection").Update(&PullRequest{TargetSelection: string(raw)}); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := BeginMergeOperation(ctx, owner.Id, MergeAdmission{ID: "unsupported-operation", IdempotencyKey: "unsupported-target", PullID: p.ID, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode}); !errors.Is(err, ErrPullInvalid) {
				t.Errorf("immutable merge admitted: %v", err)
			}
			if _, err := ReservePullComparison(ctx, owner.Id, p.ID); !errors.Is(err, ErrPullInvalid) {
				t.Errorf("immutable refresh admitted: %v", err)
			}
		})
	}
}
