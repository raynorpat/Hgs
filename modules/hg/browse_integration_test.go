// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package hg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// specialFiles is applied with import --bypass, which writes the changeset
// straight to the store: Windows could not check out a symlink or both "A"
// and "a", but the repository must still hold them.
const specialFiles = "# HG changeset patch\n# User t\n# Date 0 0\nspecial files\n\n" +
	"diff --git a/a b/a\nnew file mode 100644\n--- /dev/null\n+++ b/a\n@@ -0,0 +1,1 @@\n+lower\n" +
	"diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1,1 @@\n+d/x\n\\ No newline at end of file\n" +
	"diff --git a/run.sh b/run.sh\nnew file mode 100755\n--- /dev/null\n+++ b/run.sh\n@@ -0,0 +1,1 @@\n+#!/bin/sh\n" +
	"diff --git a/[ab] b/[ab]\nnew file mode 100644\n--- /dev/null\n+++ b/[ab]\n@@ -0,0 +1,1 @@\n+glob\n" +
	"diff --git a/caf\xe9 b/caf\xe9\nnew file mode 100644\n--- /dev/null\n+++ b/caf\xe9\n@@ -0,0 +1,1 @@\n+latin-1 name\n"

// buildBrowseFixture returns a checkout-free storage repository and its
// nodes by role; see the golden files in testdata for the resulting graph.
func buildBrowseFixture(t *testing.T, r *Runner, root string) (string, map[string]NodeID) {
	t.Helper()
	ctx := context.Background()
	work := filepath.Join(root, "work")
	hg := func(repo string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := r.Run(ctx, repo, args, nil, &out); err != nil {
			t.Fatalf("hg %q: %v", args, err)
		}
		return out.String()
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(work, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	nodes := map[string]NodeID{}
	commit := func(role string, args ...string) {
		t.Helper()
		hg(work, append([]string{"commit", "-q", "-u", "t", "-d", "0 0", "-m", role}, args...)...)
		nodes[role] = NodeID(hg(work, "log", "-r", ".", "-T", "{node}"))
	}

	hg("", "init", work)
	write("A", "top\n")
	write("d/x", "sub\n")
	hg(work, "add", "-q")
	commit("base")
	write("d/x", "older default head\n")
	commit("older")

	hg(work, "update", "-q", "-r", string(nodes["base"]))
	hg(work, "branch", "-q", "feature/x")
	write("d/x", "feature\n")
	commit("feature")
	write("d/x", "feature open\n")
	commit("featureOpen")
	hg(work, "update", "-q", "-r", string(nodes["feature"]))
	write("d/x", "feature closing\n")
	commit("featureToClose")
	commit("featureClosed", "--close-branch")

	hg(work, "update", "-q", "-r", string(nodes["base"]))
	hg(work, "tag", "-f", "-u", "t", "-d", "0 0", "-r", string(nodes["older"]), "same")
	nodes["tagCommit"] = NodeID(hg(work, "log", "-r", ".", "-T", "{node}"))
	patch := filepath.Join(root, "special.diff")
	if err := os.WriteFile(patch, []byte(specialFiles), 0600); err != nil {
		t.Fatal(err)
	}
	hg(work, "import", "-q", "--bypass", patch)
	nodes["newer"] = NodeID(hg(work, "log", "-r", "tip", "-T", "{node}"))

	hg(work, "update", "-q", "-r", string(nodes["base"]))
	write("A", "closing\n")
	commit("toClose")
	commit("defaultClosed", "--close-branch")
	// An open head newer than every default head, on another branch.
	hg(work, "update", "-q", "-r", string(nodes["base"]))
	hg(work, "branch", "-q", "later")
	commit("later")

	hg(work, "bookmark", "-r", string(nodes["featureOpen"]), "x) or all() or (y")
	hg(work, "bookmark", "-r", string(nodes["newer"]), "same")
	hg(work, "bookmark", "-r", string(nodes["base"]), "é")

	// A new open default root with the highest revision: only the secret
	// phase keeps it from being the landing head.
	hg(work, "update", "-q", "-r", "null")
	write("secret.txt", "secret\n")
	hg(work, "add", "-q", "secret.txt")
	commit("secret")
	hg(work, "bookmark", "-r", string(nodes["secret"]), "secretbm")

	storage := filepath.Join(root, "storage")
	hg("", "clone", "-q", "-U", work, storage)
	hg(storage, "phase", "-q", "--secret", "--force", "-r", string(nodes["secret"]))
	return storage, nodes
}

func newIntegrationRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	root := t.TempDir()
	r, err := NewRunner(Config{Executable: exe, ConfigPath: filepath.Join(root, "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	return r, root
}

func TestBrowseRealMercurial(t *testing.T) {
	r, root := newIntegrationRunner(t)
	ctx := context.Background()
	storage, nodes := buildBrowseFixture(t, r, root)

	refs, err := r.References(ctx, storage)
	if err != nil {
		t.Fatal(err)
	}
	// The recorded outputs the unit tests use must match the live ones.
	if golden := parseGoldenReferences(t, "browse-refs.jsonl"); !reflect.DeepEqual(refs, golden) {
		t.Errorf("live references differ from testdata:\n%v\n%v", refs, golden)
	}
	selected, err := r.Landing(ctx, storage)
	if err != nil {
		t.Fatal(err)
	}
	tagNode, err := r.Resolve(ctx, storage, RefSelection{RefTag, "same"})
	if err != nil {
		t.Fatal(err)
	}
	bookmarkNode, err := r.Resolve(ctx, storage, RefSelection{RefBookmark, "same"})
	if err != nil {
		t.Fatal(err)
	}
	if hostile, err := r.Resolve(ctx, storage, RefSelection{RefBookmark, "x) or all() or (y"}); err != nil || hostile != nodes["featureOpen"] {
		t.Errorf("hostile bookmark resolved to %q, %v", hostile, err)
	}
	if branch, err := r.Resolve(ctx, storage, RefSelection{RefBranch, "feature/x"}); err != nil || branch != nodes["featureOpen"] {
		t.Errorf("feature/x resolved to %q, %v; want its open head", branch, err)
	}
	for _, sel := range []RefSelection{{RefNode, string(nodes["secret"])}, {RefBookmark, "secretbm"}, {RefBookmark, "all()"}} {
		if n, err := r.Resolve(ctx, storage, sel); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%v) = %q, %v; want ErrNotFound", sel, n, err)
		}
	}
	if n, err := r.Resolve(ctx, storage, RefSelection{RefNode, strings.ToUpper(string(nodes["older"]))}); err != nil || n != nodes["older"] {
		t.Errorf("node selection resolved to %q, %v", n, err)
	}
	if _, err := r.Manifest(ctx, storage, nodes["secret"]); !errors.Is(err, ErrNotFound) {
		t.Errorf("secret manifest: %v; want ErrNotFound", err)
	}
	if err := r.ReadFile(ctx, storage, nodes["secret"], "secret.txt", io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("secret file: %v; want ErrNotFound", err)
	}

	entries, err := r.Manifest(ctx, storage, selected)
	if err != nil {
		t.Fatal(err)
	}
	if golden := parseGoldenManifest(t, "browse-manifest.txt"); !reflect.DeepEqual(entries, golden) {
		t.Errorf("live manifest differs from testdata:\n%#v\n%#v", entries, golden)
	}
	for file, want := range map[string]string{
		"A": "top\n", "a": "lower\n", "link": "d/x", "run.sh": "#!/bin/sh\n", "[ab]": "glob\n",
		"caf\xe9": "latin-1 name\n", "d/x": "sub\n", ".hgtags": string(nodes["older"]) + " same\n",
	} {
		var got bytes.Buffer
		if err := r.ReadFile(ctx, storage, selected, file, &got); err != nil || got.String() != want {
			t.Errorf("ReadFile(%q) = %q, %v; want %q", file, got.String(), err, want)
		}
	}
	for _, file := range []string{"d", "b", "nope", "D/x", ".hg/hgrc", "../work/A", "secret.txt"} {
		if err := r.ReadFile(ctx, storage, selected, file, io.Discard); !errors.Is(err, ErrNotFound) {
			t.Errorf("ReadFile(%q): %v; want ErrNotFound", file, err)
		}
	}

	ls, err := os.ReadDir(storage)
	checkoutCreated := err != nil || len(ls) != 1 || ls[0].Name() != ".hg"
	if selected != nodes["newer"] || tagNode == bookmarkNode || checkoutCreated {
		t.Fatal("reference selection or checkout-free browsing is incorrect")
	}
	if tagNode != nodes["older"] || bookmarkNode != nodes["newer"] {
		t.Errorf("tag same = %s, bookmark same = %s", tagNode, bookmarkNode)
	}

	// All heads closed: the tip. No changesets: an explicit empty result.
	closed := filepath.Join(root, "closed")
	for _, args := range [][]string{
		{"init", closed},
		{"-R", closed, "commit", "-q", "-A", "-u", "t", "-d", "0 0", "-m", "c0", "--config", "ui.allowemptycommit=1"},
		{"-R", closed, "commit", "-q", "--close-branch", "-u", "t", "-d", "0 0", "-m", "c1"},
		{"-R", closed, "update", "-q", "-r", "0"},
		{"-R", closed, "branch", "-q", "b"},
		{"-R", closed, "commit", "-q", "-u", "t", "-d", "0 0", "-m", "c2"},
		{"-R", closed, "commit", "-q", "--close-branch", "-u", "t", "-d", "0 0", "-m", "c3"},
	} {
		if err := r.Run(ctx, "", args, nil, nil); err != nil {
			t.Fatalf("hg %q: %v", args, err)
		}
	}
	if refs, err := r.References(ctx, closed); err != nil || !reflect.DeepEqual(refs, parseGoldenReferences(t, "browse-refs-closed.jsonl")) {
		t.Errorf("all-closed references %v, %v differ from testdata", refs, err)
	}
	var tip bytes.Buffer
	r.Run(ctx, closed, []string{"log", "-r", "tip", "-T", "{node}"}, nil, &tip)
	if n, err := r.Landing(ctx, closed); err != nil || string(n) != tip.String() {
		t.Errorf("all-closed landing %q, %v; want tip %s", n, err, tip.String())
	}

	empty := filepath.Join(root, "empty")
	if err := r.Run(ctx, "", []string{"init", empty}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if refs, err := r.References(ctx, empty); err != nil || refs == nil || len(refs) != 0 {
		t.Errorf("empty references %v, %v", refs, err)
	}
	if _, err := r.Landing(ctx, empty); !errors.Is(err, ErrEmptyRepository) {
		t.Errorf("empty landing: %v", err)
	}
}

func TestBrowseHiddenChangeset(t *testing.T) {
	r, root := newIntegrationRunner(t)
	ctx := context.Background()
	repo := filepath.Join(root, "hidden")
	var node bytes.Buffer
	for _, args := range [][]string{
		{"init", repo},
		{"-R", repo, "commit", "-q", "-u", "t", "-d", "0 0", "-m", "hidden", "--config", "ui.allowemptycommit=1"},
		{"-R", repo, "log", "-r", ".", "-T", "{node}"},
	} {
		var out io.Writer
		if args[0] == "-R" && args[2] == "log" {
			out = &node
		}
		if err := r.Run(ctx, "", args, nil, out); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"update", "-q", "-r", "null"},
		{"--config", "experimental.evolution=createmarkers", "debugobsolete", node.String()},
	} {
		if err := r.Run(ctx, repo, args, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if refs, err := r.References(ctx, repo); err != nil || len(refs) != 0 {
		t.Errorf("hidden references: %v, %v", refs, err)
	}
	if n, err := r.Resolve(ctx, repo, RefSelection{RefNode, node.String()}); !errors.Is(err, ErrNotFound) {
		t.Errorf("hidden node resolved to %s, %v", n, err)
	}
	if _, err := r.Manifest(ctx, repo, NodeID(node.String())); !errors.Is(err, ErrNotFound) {
		t.Errorf("hidden manifest: %v", err)
	}
	if err := r.ReadFile(ctx, repo, NodeID(node.String()), "missing", io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("hidden file: %v", err)
	}
}

func TestReferencesPromoteServedBranchHead(t *testing.T) {
	r, root := newIntegrationRunner(t)
	ctx := context.Background()
	repo := filepath.Join(root, "secret-child")
	hg := func(path string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := r.Run(ctx, path, args, nil, &out); err != nil {
			t.Fatalf("hg %q: %v", args, err)
		}
		return out.String()
	}
	commit := func(message string) NodeID {
		t.Helper()
		hg(repo, "commit", "-q", "-u", "t", "-d", "0 0", "-m", message, "--config", "ui.allowemptycommit=1")
		return NodeID(hg(repo, "log", "-r", ".", "-T", "{node}"))
	}
	hg("", "init", repo)
	visible := commit("visible")
	secret := commit("secret child")
	hg(repo, "phase", "-q", "--secret", "--force", "-r", string(secret))
	check := func(want []Reference) {
		t.Helper()
		if refs, err := r.References(ctx, repo); err != nil || !reflect.DeepEqual(refs, want) {
			t.Errorf("served branch references = %v, %v; want %v", refs, err, want)
		}
		if n, err := r.Landing(ctx, repo); err != nil || n != visible {
			t.Errorf("landing = %s, %v; want served default head %s", n, err, visible)
		}
		if n, err := r.Resolve(ctx, repo, RefSelection{RefBranch, "default"}); err != nil || n != visible {
			t.Errorf("default = %s, %v; want %s", n, err, visible)
		}
	}
	defaultRef := Reference{Kind: RefBranch, Name: "default", Node: visible, Revision: 0}
	check([]Reference{defaultRef})

	// A descendant on a different branch must not suppress the default
	// named-branch head, even though it is no longer a DAG head.
	hg(repo, "update", "-q", "-r", string(visible))
	hg(repo, "branch", "-q", "side")
	side := commit("side child")
	check([]Reference{{Kind: RefBranch, Name: "side", Node: side, Revision: 2}, defaultRef})
}

func TestReferencesFollowBranchAncestry(t *testing.T) {
	r, root := newIntegrationRunner(t)
	ctx := context.Background()
	repo := filepath.Join(root, "return-to-branch")
	hg := func(path string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := r.Run(ctx, path, args, nil, &out); err != nil {
			t.Fatalf("hg %q: %v", args, err)
		}
		return out.String()
	}
	commit := func(message string, args ...string) NodeID {
		t.Helper()
		hg(repo, append([]string{"commit", "-q", "-u", "t", "-d", "0 0", "-m", message, "--config", "ui.allowemptycommit=1"}, args...)...)
		return NodeID(hg(repo, "log", "-r", ".", "-T", "{node}"))
	}
	hg("", "init", repo)
	commit("default ancestor")
	hg(repo, "branch", "-q", "side")
	side := commit("side head")
	hg(repo, "branch", "-q", "--force", "default")
	closed := commit("closed default return", "--close-branch")

	// The original default changeset has a same-branch descendant through
	// side. It must not reappear as an open default head and win landing.
	want := []Reference{
		{Kind: RefBranch, Name: "default", Node: closed, Revision: 2, Closed: true},
		{Kind: RefBranch, Name: "side", Node: side, Revision: 1},
	}
	if refs, err := r.References(ctx, repo); err != nil || !reflect.DeepEqual(refs, want) {
		t.Errorf("returning branch references = %v, %v; want %v", refs, err, want)
	}
	if n, err := r.Landing(ctx, repo); err != nil || n != side {
		t.Errorf("landing = %s, %v; want open side head %s", n, err, side)
	}
	if n, err := r.Resolve(ctx, repo, RefSelection{RefBranch, "default"}); err != nil || n != closed {
		t.Errorf("default = %s, %v; want closed default head %s", n, err, closed)
	}
}
