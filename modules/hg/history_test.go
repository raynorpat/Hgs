package hg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

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
