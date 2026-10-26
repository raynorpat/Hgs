//go:build integration

package hg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiffSelectsMergeParent(t *testing.T) {
	r, root := newIntegrationRunner(t)
	repo := filepath.Join(root, "merge")
	hg := func(args ...string) string { return historyHG(t, r, repo, args...) }
	historyHG(t, r, "", "init", repo)
	write := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) NodeID {
		hg("commit", "-q", "-A", "-u", "t", "-d", "0 0", "-m", msg)
		return NodeID(hg("log", "-r", ".", "-T", "{node}"))
	}
	write("base", "root\n")
	base := commit("base")
	write("left", "left\n")
	left := commit("left")
	hg("update", "-q", "-r", string(base))
	write("right", "right\n")
	right := commit("right")
	hg("merge", "-q", "-r", string(left))
	merge := commit("merge")
	ctx := context.Background()
	cs, err := r.Changeset(ctx, repo, merge)
	if err != nil || len(cs.Parents) != 2 || cs.Parents[0] != right || cs.Parents[1] != left {
		t.Fatalf("merge parents %+v, %v", cs, err)
	}
	d0, err := r.Diff(ctx, repo, merge, 0)
	if err != nil {
		t.Fatal(err)
	}
	d1, err := r.Diff(ctx, repo, merge, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(d0.Text, []byte("+left")) || bytes.Contains(d0.Text, []byte("+right")) || !bytes.Contains(d1.Text, []byte("+right")) || bytes.Equal(d0.Text, d1.Text) {
		t.Fatalf("parent diffs: %q / %q", d0.Text, d1.Text)
	}
	d, err := r.Diff(ctx, repo, base, 0)
	if err != nil || !bytes.Contains(d.Text, []byte("+root")) {
		t.Fatalf("root diff: %q, %v", d.Text, err)
	}
	for _, index := range []int{-1, 1, 2} {
		if _, err := r.Diff(ctx, repo, base, index); err == nil {
			t.Errorf("root accepted parent %d", index)
		}
	}
	if _, err := r.Diff(ctx, repo, left, 1); err == nil {
		t.Error("nonmerge accepted parent 1")
	}
	write("binary", "a\x00b")
	binary := commit("binary")
	d, err = r.Diff(ctx, repo, binary, 0)
	if err != nil || !d.Binary || d.Truncated || len(d.Text) == 0 {
		t.Fatalf("binary diff %+v, %v", d, err)
	}
	write("huge", string(bytes.Repeat([]byte("x"), MaxDiffBytes+100)))
	big := commit("huge line")
	d, err = r.Diff(ctx, repo, big, 0)
	if err != nil || len(d.Text) > MaxDiffBytes || !d.Truncated {
		t.Fatalf("huge diff bytes=%d truncated=%v, %v", len(d.Text), d.Truncated, err)
	}
}
