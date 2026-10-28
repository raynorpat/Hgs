package hg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHistoryRejectsInvalidCursor(t *testing.T) {
	r, repo := newBrowseRunner(t, "")
	for _, q := range []HistoryQuery{
		{BeforeRevision: -2}, {BeforeRevision: 0},
		{Snapshot: "tip", BeforeRevision: -1},
		{Snapshot: baseNode, SnapshotRevision: -1, BeforeRevision: 0},
		{Snapshot: baseNode, SnapshotRevision: 0, BeforeRevision: 1},
		{BeforeRevision: -1, Limit: -1},
	} {
		if _, err := r.History(context.Background(), repo, q); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("History(%+v): %v; want ErrInvalidCursor", q, err)
		}
	}
	if calls := fakeCalls(t, r); len(calls) != 0 {
		t.Fatalf("invalid cursor invoked Mercurial: %v", calls)
	}
}

func TestHistoryStartsAtExplicitSnapshot(t *testing.T) {
	r, repo := newBrowseRunner(t, "")
	writeFakeFixture(t, r, "fake-log", `{"node":"`+string(baseNode)+`","rev":0,"p1node":"`+strings.Repeat("0", 40)+`","p2node":"`+strings.Repeat("0", 40)+`","author":"t","desc":"root","branch":"default","date":[0,0]}`+"\n")
	page, err := r.History(context.Background(), repo, HistoryQuery{Snapshot: baseNode, SnapshotRevision: 0, BeforeRevision: -1})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Node != baseNode {
		t.Fatalf("explicit snapshot %+v, %v", page, err)
	}
}

func TestHistoryParsesChangesetData(t *testing.T) {
	data := `{"node":"` + string(olderDefaultNode) + `","rev":1,"p1node":"` + string(baseNode) + `","p2node":"` + strings.Repeat("0", 40) + `","author":"User <u@example.test>","desc":"subject\n\nbody","branch":"feature/x","date":[123.5,-3600]}` + "\n"
	cs, err := parseChangesets([]byte(data))
	if err != nil || len(cs) != 1 {
		t.Fatalf("parse: %v, %v", cs, err)
	}
	got := cs[0]
	if got.User != "User <u@example.test>" || got.Message != "subject\n\nbody" || got.Branch != "feature/x" || got.Revision != 1 ||
		len(got.Parents) != 1 || got.Parents[0] != baseNode || !got.Date.Equal(time.Unix(123, 500000000)) {
		t.Fatalf("lost changeset data: %+v", got)
	}
	for _, b := range []string{"garbage", `{}`, strings.Replace(data, `"rev":1`, `"rev":-1`, 1), strings.Replace(data, `123.5,-3600`, `123.5`, 1)} {
		if _, err := parseChangesets([]byte(b)); err == nil {
			t.Errorf("accepted malformed data %q", b)
		}
	}
}

func TestHistoryBoundsPageSize(t *testing.T) {
	for _, tc := range []struct {
		limit int
		want  string
	}{{0, "31"}, {1, "2"}, {100, "101"}, {101, "101"}, {1000000, "101"}} {
		r, repo := newBrowseRunner(t, "")
		writeFakeFixture(t, r, "fake-log", `{"node":"`+string(baseNode)+`","rev":0,"p1node":"`+strings.Repeat("0", 40)+`","p2node":"`+strings.Repeat("0", 40)+`","author":"t","desc":"root","branch":"default","date":[0,0]}`+"\n")
		page, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1, SnapshotRevision: -123, Limit: tc.limit})
		if err != nil || len(page.Entries) != 1 || page.Snapshot != baseNode || page.SnapshotRevision != 0 {
			t.Fatalf("page %+v, %v", page, err)
		}
		calls := fakeCalls(t, r)
		call := calls[len(calls)-1]
		found := false
		for i, arg := range call {
			if arg == "-l" && i+1 < len(call) && call[i+1] == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("limit %d ran %v; want -l %s", tc.limit, call, tc.want)
		}
	}
}

func TestHistoryAndDiffRejectInvalidNodesAndPaths(t *testing.T) {
	r, repo := newBrowseRunner(t, "")
	for _, node := range []NodeID{"", "tip", "all()", NodeID(strings.Repeat("0", 40)), baseNode + " or all()"} {
		if _, err := r.Changeset(context.Background(), repo, node); !errors.Is(err, ErrNotFound) {
			t.Errorf("Changeset(%q): %v", node, err)
		}
		if _, err := diffNode(r, context.Background(), repo, node, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("Diff(%q): %v", node, err)
		}
	}
	for _, path := range []string{"../a", "/a", ".hg/store", "a\x00", "a//b", `a\b`} {
		if _, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1, Path: path}); err == nil {
			t.Errorf("History accepted path %q", path)
		}
	}
	for _, index := range []int{-1, 2, 100} {
		if _, err := diffNode(r, context.Background(), repo, baseNode, index); err == nil {
			t.Errorf("Diff accepted parent %d", index)
		}
	}
	if calls := fakeCalls(t, r); len(calls) != 0 {
		t.Errorf("invalid arguments invoked Mercurial: %v", calls)
	}
}
