//go:build integration

package hg

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func historyHG(t *testing.T, r *Runner, repo string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := r.Run(context.Background(), repo, args, nil, &out); err != nil {
		t.Fatalf("hg %q: %v", args, err)
	}
	return out.String()
}

func TestHistoryDoesNotShiftOnPush(t *testing.T) {
	r, root := newIntegrationRunner(t)
	repo, nodes := buildBrowseFixture(t, r, root)
	ctx := context.Background()
	first, err := r.History(ctx, repo, HistoryQuery{BeforeRevision: -1, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []NodeID{nodes["later"], nodes["defaultClosed"], nodes["toClose"], nodes["newer"], nodes["tagCommit"], nodes["featureClosed"], nodes["featureToClose"], nodes["featureOpen"], nodes["feature"], nodes["older"], nodes["base"]}
	if first.Snapshot != nodes["later"] || !first.HasMore || first.SnapshotRevision != 10 {
		t.Fatalf("first page %+v", first)
	}
	// An independent client knows only this branch, so its push cannot also
	// publish the unrelated secret changeset retained in the fixture source.
	work := filepath.Join(root, "pusher")
	historyHG(t, r, "", "clone", "-q", "-r", string(nodes["older"]), repo, work)
	historyHG(t, r, work, "update", "-q", "-r", string(nodes["older"]))
	historyHG(t, r, work, "commit", "-q", "-u", "t", "-d", "1 0", "-m", "append", "--config", "ui.allowemptycommit=1")
	historyHG(t, r, work, "push", "-q", "--force", repo)
	combined := []NodeID{}
	page := first
	for {
		for _, cs := range page.Entries {
			combined = append(combined, cs.Node)
		}
		if !page.HasMore {
			break
		}
		page, err = r.History(ctx, repo, HistoryQuery{Snapshot: first.Snapshot, SnapshotRevision: first.SnapshotRevision, BeforeRevision: page.NextBefore, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		if page.Snapshot != first.Snapshot {
			t.Fatal("snapshot shifted")
		}
	}
	if !reflect.DeepEqual(combined, want) {
		t.Fatalf("concurrent push shifted history pagination: %v; want %v", combined, want)
	}
	for _, q := range []HistoryQuery{
		{Snapshot: first.Snapshot, SnapshotRevision: 9, BeforeRevision: first.NextBefore},
		{Snapshot: nodes["secret"], SnapshotRevision: 11, BeforeRevision: 1},
		{Snapshot: NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), SnapshotRevision: 11, BeforeRevision: 1},
	} {
		if _, err := r.History(ctx, repo, q); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("invalid snapshot %+v: %v", q, err)
		}
	}
	cs, err := r.Changeset(ctx, repo, nodes["base"])
	if err != nil || cs.Message != "base" || cs.User != "t" || cs.Branch != "default" || len(cs.Parents) != 0 || cs.Revision != 0 {
		t.Fatalf("root %+v, %v", cs, err)
	}
	for _, n := range []NodeID{nodes["secret"], "tip"} {
		if _, err := r.Changeset(ctx, repo, n); !errors.Is(err, ErrNotFound) {
			t.Errorf("Changeset(%s): %v", n, err)
		}
		if _, err := r.Diff(ctx, repo, n, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("Diff(%s): %v", n, err)
		}
	}
	// The existing raw-byte file fixture must work without CLI re-encoding.
	for file, message := range map[string]string{"[ab]": "special files", "caf\xe9": "special files", "A": "base", "d/x": "older"} {
		q := HistoryQuery{BeforeRevision: -1, Path: file}
		if file == "[ab]" || file == "caf\xe9" {
			q.Snapshot = nodes["newer"]
			q.SnapshotRevision = 7
		}
		p, err := r.History(ctx, repo, q)
		if err != nil || len(p.Entries) == 0 || p.Entries[0].Message != message {
			t.Errorf("history %q: %+v, %v", file, p, err)
		}
	}
}

func TestFileHistoryFollowsRename(t *testing.T) {
	r, root := newIntegrationRunner(t)
	repo := filepath.Join(root, "rename")
	hg := func(args ...string) string { return historyHG(t, r, repo, args...) }
	historyHG(t, r, "", "init", repo)
	commit := func(message string) NodeID {
		hg("commit", "-q", "-A", "-u", "Author <a@test>", "-d", "123 -3600", "-m", message)
		return NodeID(hg("log", "-r", ".", "-T", "{node}"))
	}
	if err := os.WriteFile(filepath.Join(repo, "old"), []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := commit("created")
	if err := os.Mkdir(filepath.Join(repo, "d"), 0700); err != nil {
		t.Fatal(err)
	}
	hg("rename", "old", "d/[new]")
	b := commit("renamed")
	// A separate named head changes the same renamed path before the pinned
	// snapshot's local revision. It must not enter this file's ancestry.
	hg("update", "-q", "-r", string(a))
	hg("branch", "-q", "unrelated")
	if err := os.MkdirAll(filepath.Join(repo, "d"), 0700); err != nil {
		t.Fatal(err)
	}
	hg("rename", "old", "d/[new]")
	if err := os.WriteFile(filepath.Join(repo, "d", "[new]"), []byte("unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	commit("unrelated head")
	hg("update", "-q", "-r", string(b))
	if err := os.WriteFile(filepath.Join(repo, "d", "[new]"), []byte("two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := commit("edited")
	hg("update", "-q", "-r", "null")
	page, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1, Path: "d/[new]", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := []NodeID{}
	for {
		for _, cs := range page.Entries {
			got = append(got, cs.Node)
		}
		if !page.HasMore {
			break
		}
		page, err = r.History(context.Background(), repo, HistoryQuery{Snapshot: page.Snapshot, SnapshotRevision: page.SnapshotRevision, BeforeRevision: page.NextBefore, Path: "d/[new]", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, []NodeID{c, b, a}) {
		t.Fatalf("rename history %v", got)
	}
	for _, file := range []string{"old", "missing", "d/new", "d"} {
		if _, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1, Path: file}); !errors.Is(err, ErrNotFound) {
			t.Errorf("absent path %q: %v; want ErrNotFound", file, err)
		}
	}
}

func TestHistoryRejectsHiddenAndExpiredSnapshot(t *testing.T) {
	r, root := newIntegrationRunner(t)
	repo := filepath.Join(root, "hidden-history")
	historyHG(t, r, "", "init", repo)
	historyHG(t, r, repo, "commit", "-q", "-u", "t", "-d", "0 0", "-m", "hidden", "--config", "ui.allowemptycommit=1")
	first, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1})
	if err != nil {
		t.Fatal(err)
	}
	historyHG(t, r, repo, "update", "-q", "-r", "null")
	historyHG(t, r, repo, "--config", "experimental.evolution=createmarkers", "debugobsolete", string(first.Snapshot))
	ctx := context.Background()
	if _, err := r.Changeset(ctx, repo, first.Snapshot); !errors.Is(err, ErrNotFound) {
		t.Errorf("hidden changeset: %v", err)
	}
	if _, err := r.Diff(ctx, repo, first.Snapshot, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("hidden diff: %v", err)
	}
	if _, err := r.History(ctx, repo, HistoryQuery{Snapshot: first.Snapshot, SnapshotRevision: first.SnapshotRevision, BeforeRevision: 0}); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("expired cursor: %v", err)
	}
	if _, err := r.History(ctx, repo, HistoryQuery{BeforeRevision: -1}); !errors.Is(err, ErrEmptyRepository) {
		t.Errorf("all hidden history: %v", err)
	}
}
