// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nodes of the fixture buildBrowseFixture creates, as recorded in testdata
// from Mercurial 7.2.4.
const (
	baseNode          NodeID = "5a5d8f1ece2eb0c248d510d928a9476c6511c3f9"
	olderDefaultNode  NodeID = "33f3e68acbdf5c29779b7281bc14e10e659d116f"
	newerDefaultNode  NodeID = "f63f99903c6f738b1dc0cc74e4402d4da6d33c1e"
	defaultClosedNode NodeID = "329f08208d0d5f55a192ff82a700c89485bbcaa0"
	featureOpenNode   NodeID = "bc47a5e907b9ea2a6916ab8e90df526778350748"
	featureClosedNode NodeID = "ede4042fbc9bd98babbaa4c4a5363b2aa438abe8"
	closedTipNode     NodeID = "53cf592b090ae916cd724b249ca85882e2a66a1c"
	laterBranchNode   NodeID = "e58cebec02e1f7f7821b7eb7a534db2edc39e6f5"
)

const hostileName = "x) or all() or (y"

func parseGoldenReferences(t *testing.T, name string) []Reference {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := parseReferences(b)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

// newBrowseRunner returns a fake-Mercurial runner whose log output is the
// named testdata file, and a repository directory holding only .hg.
func newBrowseRunner(t *testing.T, logFile string) (*Runner, string) {
	t.Helper()
	r := newTestRunner(t, Config{})
	if logFile != "" {
		b, err := os.ReadFile(filepath.Join("testdata", logFile))
		if err != nil {
			t.Fatal(err)
		}
		writeFakeFixture(t, r, "fake-log", string(b))
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".hg"), 0700); err != nil {
		t.Fatal(err)
	}
	return r, repo
}

func writeFakeFixture(t *testing.T, r *Runner, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(r.cfg.ConfigPath), name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// fakeCalls returns the argument lists of the browse commands the fake ran.
func fakeCalls(t *testing.T, r *Runner) [][]string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(r.cfg.ConfigPath), "fake-calls"))
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line != "" {
			calls = append(calls, strings.Split(line, "\x00"))
		}
	}
	return calls
}

func TestLandingSelectsOpenDefaultHead(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "browse-refs.jsonl")
	selected, err := r.Landing(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	// defaultClosedNode and the secret root have higher revisions.
	if selected != newerDefaultNode {
		t.Errorf("landing %s; want the newer open default head %s", selected, newerDefaultNode)
	}
	if calls := fakeCalls(t, r); len(calls) != 1 {
		t.Errorf("landing ran %d Mercurial commands; want 1", len(calls))
	}

	r, repo = newBrowseRunner(t, "browse-refs-closed.jsonl")
	if n, err := r.Landing(ctx, repo); err != nil || n != closedTipNode {
		t.Errorf("all-closed landing %s, %v; want tip %s", n, err, closedTipNode)
	}

	r, repo = newBrowseRunner(t, "")
	writeFakeFixture(t, r, "fake-log", "")
	if _, err := r.Landing(ctx, repo); !errors.Is(err, ErrEmptyRepository) {
		t.Errorf("empty landing: %v", err)
	}
	if refs, err := r.References(ctx, repo); err != nil || refs == nil || len(refs) != 0 {
		t.Errorf("empty references %v, %v; want an empty list", refs, err)
	}
}

func TestReferencesRemainDistinct(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "browse-refs.jsonl")
	refs, err := r.References(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		kind RefKind
		name string
		node NodeID
	}
	got := map[key]bool{}
	for _, ref := range refs {
		if _, err := ParseNodeID(string(ref.Node)); err != nil || ref.Revision < 0 {
			t.Errorf("reference %+v lacks a full node or revision", ref)
		}
		got[key{ref.Kind, ref.Name, ref.Node}] = ref.Closed
	}
	want := map[key]bool{
		{RefBranch, "later", laterBranchNode}:       false,
		{RefBranch, "default", olderDefaultNode}:    false,
		{RefBranch, "default", newerDefaultNode}:    false,
		{RefBranch, "default", defaultClosedNode}:   true,
		{RefBranch, "feature/x", featureOpenNode}:   false,
		{RefBranch, "feature/x", featureClosedNode}: true,
		{RefTag, "same", olderDefaultNode}:          false,
		{RefBookmark, "same", newerDefaultNode}:     false,
		{RefBookmark, hostileName, featureOpenNode}: false,
		{RefBookmark, "é", baseNode}:                false,
	}
	if len(got) != len(want) {
		t.Errorf("references %v; want %v (no tip, no secret changesets)", got, want)
	}
	for k, closed := range want {
		if c, ok := got[k]; !ok || c != closed {
			t.Errorf("reference %v: present %v closed %v; want closed %v", k, ok, c, closed)
		}
	}

	tagNode, err := r.Resolve(ctx, repo, RefSelection{RefTag, "same"})
	if err != nil {
		t.Fatal(err)
	}
	bookmarkNode, err := r.Resolve(ctx, repo, RefSelection{RefBookmark, "same"})
	if err != nil {
		t.Fatal(err)
	}
	if tagNode != olderDefaultNode || bookmarkNode != newerDefaultNode {
		t.Errorf("tag same = %s, bookmark same = %s", tagNode, bookmarkNode)
	}
	// An open head wins over a closed head of the same branch with a higher revision.
	if n, err := r.Resolve(ctx, repo, RefSelection{RefBranch, "feature/x"}); err != nil || n != featureOpenNode {
		t.Errorf("feature/x resolved to %s, %v; want %s", n, err, featureOpenNode)
	}
	for _, sel := range []RefSelection{{RefBranch, "same"}, {RefTag, "tip"}, {RefBookmark, "secretbm"}, {RefBranch, ""}, {"revset", "all()"}} {
		if n, err := r.Resolve(ctx, repo, sel); err == nil {
			t.Errorf("Resolve(%v) = %s; want an error", sel, n)
		}
	}
}

func TestReferencesRejectInvalidEncoding(t *testing.T) {
	b := []byte(`{"rev":1,"node":"33f3e68acbdf5c29779b7281bc14e10e659d116f","branch":"`)
	b = append(b, 0xff)
	b = append(b, []byte(`","head":"1"}`)...)
	if refs, err := parseReferences(b); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("invalid reference encoding gave %v, %v; want ErrUnsupportedEncoding", refs, err)
	}
}

func TestLiteralReferenceCannotInjectRevset(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "browse-refs.jsonl")
	if n, err := r.Resolve(ctx, repo, RefSelection{RefBookmark, hostileName}); err != nil || n != featureOpenNode {
		t.Errorf("hostile bookmark resolved to %s, %v", n, err)
	}
	for _, name := range []string{"all()", "x", "y", hostileName + " "} {
		if n, err := r.Resolve(ctx, repo, RefSelection{RefBookmark, name}); !errors.Is(err, ErrNotFound) {
			t.Errorf("bookmark %q resolved to %s, %v; want ErrNotFound", name, n, err)
		}
	}
	// Node selections are validated before Mercurial sees them.
	for _, name := range []string{hostileName, "tip", "0", string(newerDefaultNode)[:12], strings.Repeat("0", 40), strings.Repeat("f", 40), string(newerDefaultNode) + ")"} {
		if n, err := r.Resolve(ctx, repo, RefSelection{RefNode, name}); !errors.Is(err, ErrNotFound) {
			t.Errorf("node %q resolved to %s, %v; want ErrNotFound", name, n, err)
		}
	}
	for _, call := range fakeCalls(t, r) {
		for _, arg := range call {
			if strings.Contains(arg, "all()") || strings.Contains(arg, "same") {
				t.Errorf("a reference name reached Mercurial: %q", call)
			}
		}
	}

	writeFakeFixture(t, r, "fake-visible", string(newerDefaultNode))
	if n, err := r.Resolve(ctx, repo, RefSelection{RefNode, strings.ToUpper(string(newerDefaultNode))}); err != nil || n != newerDefaultNode {
		t.Errorf("node selection resolved to %s, %v", n, err)
	}
	calls := fakeCalls(t, r)
	want := []string{"-R", repo, "log", "-r", "id(" + string(newerDefaultNode) + ") and not secret()", "-T", "{node}"}
	if last := calls[len(calls)-1]; strings.Join(last, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("node lookup ran %q; want %q", last, want)
	}
	// Unknown or secret nodes print nothing.
	writeFakeFixture(t, r, "fake-visible", "")
	if n, err := r.Resolve(ctx, repo, RefSelection{RefNode, string(newerDefaultNode)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("invisible node resolved to %s, %v", n, err)
	}
}
