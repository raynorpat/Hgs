//go:build integration

package hg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Every moved read must be served by hgqlserve: the runner's executable here
// cannot launch, so any read that starts hg fails.
func TestReadsStartNoProcess(t *testing.T) {
	f := newReviewFixture(t)
	f.write("dir/a", "source\n")
	f.hg(f.work, "add", "-q")
	f.commit("source")
	s := f.snapshot("source", "base")
	dud := filepath.Join(f.root, "dud")
	if err := os.WriteFile(dud, []byte("not a program"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(Config{Executable: dud, ConfigPath: filepath.Join(f.root, "dudcfg", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	r.SetReads(f.r.reads)
	node := f.nodes["source"]
	check := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	cs, err := r.Changeset(f.ctx, f.work, node)
	check("Changeset", err)
	_, err = r.History(f.ctx, f.work, HistoryQuery{Snapshot: node, SnapshotRevision: cs.Revision, Limit: 10})
	check("History", err)
	check("ReadFile", r.ReadFile(f.ctx, f.work, node, "dir/a", &bytes.Buffer{}))
	_, err = r.ListDirectory(f.ctx, f.work, node, "dir")
	check("ListDirectory", err)
	_, _, err = r.ListPath(f.ctx, f.work, node, "dir/a")
	check("ListPath", err)
	_, err = r.References(f.ctx, f.work)
	check("References", err)
	_, err = r.Landing(f.ctx, f.work)
	check("Landing", err)
	_, err = r.Resolve(f.ctx, f.work, RefSelection{Kind: RefBranch, Name: "default"})
	check("Resolve", err)
	_, err = r.Diff(f.ctx, f.work, cs, 0)
	check("Diff", err)
	_, err = r.CompareReview(f.ctx, s)
	check("CompareReview", err)
	_, err = r.ResolveReview(f.ctx, f.work, ReviewSelection{Ref: RefSelection{Kind: RefBranch, Name: "default"}})
	check("ResolveReview", err)
}
