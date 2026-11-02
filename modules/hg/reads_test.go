// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// startReads serves the repositories under root to r through a real
// hgqlserve, stopped when the test ends. root must be canonical.
func startReads(t *testing.T, r *Runner, root string) {
	t.Helper()
	client := NewReadClient(root)
	s, err := StartReadServer(context.Background(), hgqlserveBinary(t), root, filepath.Join(t.TempDir(), "tok"), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(context.Background()) })
	r.SetReads(client)
}

// newReadRunner returns a real Mercurial runner whose reads go to a real
// hgqlserve over a fresh root. Repositories made under r.reads.root are served.
func newReadRunner(t *testing.T) *Runner {
	t.Helper()
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Skip("GOGS_TEST_HG is not set")
	}
	hgqlserveBinary(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	startReads(t, r, root)
	return r
}

// readHg runs Mercurial in dir and returns its output.
func readHg(t *testing.T, r *Runner, dir string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := r.Run(context.Background(), dir, args, nil, &out); err != nil {
		t.Fatalf("hg %q: %v", args, err)
	}
	return out.String()
}

// readRepo initializes a repository under the served root.
func readRepo(t *testing.T, r *Runner, name string) string {
	t.Helper()
	repo := filepath.Join(r.reads.root, name)
	readHg(t, r, "", "init", repo)
	return repo
}

// readCommit makes an empty commit and returns its node.
func readCommit(t *testing.T, r *Runner, repo, msg string, args ...string) NodeID {
	t.Helper()
	readHg(t, r, repo, append([]string{"commit", "-q", "--config", "ui.allowemptycommit=1", "-u", "t <t@example.test>", "-d", "1700000000 -7200", "-m", msg}, args...)...)
	return NodeID(readHg(t, r, repo, "log", "-r", ".", "-T", "{node}"))
}

// hgReferences is what References read before the service: the old commands.
func hgReferences(t *testing.T, r *Runner, repo string) []Reference {
	t.Helper()
	out := readHg(t, r, repo, "branches", "--closed", "-T", branchHeadsTemplate) +
		readHg(t, r, repo, "log", "-r", referencesRevset, "-T", referencesTemplate)
	refs, err := parseReferences([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func TestReferencesMatchHg(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "refs")
	base := readCommit(t, r, repo, "base")
	readHg(t, r, repo, "bookmark", "-r", string(base), "bm")
	readHg(t, r, repo, "tag", "-u", "t", "-d", "0 0", "-r", string(base), "v1")
	readHg(t, r, repo, "branch", "-q", "feat")
	readCommit(t, r, repo, "feat")
	readCommit(t, r, repo, "feat closed", "--close-branch")
	readHg(t, r, repo, "update", "-q", "-r", string(base))
	secret := readCommit(t, r, repo, "secret")
	readHg(t, r, repo, "bookmark", "-r", string(secret), "secretbm")
	readHg(t, r, repo, "phase", "-q", "--secret", "--force", "-r", string(secret))
	readHg(t, r, repo, "update", "-q", "-r", string(base))
	pruned := readCommit(t, r, repo, "pruned")
	readHg(t, r, repo, "update", "-q", "-r", string(base))
	readHg(t, r, repo, "--config", "experimental.evolution=createmarkers", "debugobsolete", string(pruned))

	want := hgReferences(t, r, repo)
	got, err := r.References(context.Background(), repo)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("References = %+v, %v\nhg says     %+v", got, err, want)
	}
	for _, ref := range got {
		if ref.Node == secret || ref.Node == pruned {
			t.Errorf("served %+v", ref)
		}
	}
}

func TestChangesetMatchesHg(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "changesets")
	base := readCommit(t, r, repo, "base")
	left := readCommit(t, r, repo, "left\n\nbody é")
	readHg(t, r, repo, "update", "-q", "-r", string(base))
	readHg(t, r, repo, "branch", "-q", "other")
	right := readCommit(t, r, repo, "right")
	readHg(t, r, repo, "update", "-q", "-r", string(left))
	readHg(t, r, repo, "merge", "-q", "-r", string(right))
	merge := readCommit(t, r, repo, "merge")
	for _, node := range []NodeID{base, left, right, merge} {
		old, err := parseChangesets([]byte(readHg(t, r, repo, "log", "-r", nodeRevset(node), "-T", changesetTemplate)))
		if err != nil || len(old) != 1 {
			t.Fatal(old, err)
		}
		got, err := r.Changeset(context.Background(), repo, node)
		if err != nil || !reflect.DeepEqual(got, old[0]) {
			t.Errorf("Changeset(%s) = %+v, %v\nhg says %+v", node, got, err, old[0])
		}
	}
	if got, _ := r.Changeset(context.Background(), repo, merge); len(got.Parents) != 2 || got.Parents[0] != left || got.Parents[1] != right {
		t.Errorf("merge parents %v; want [%s %s]", got.Parents, left, right)
	}
}

func TestSecretNodeNotFound(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "secret")
	readCommit(t, r, repo, "public")
	secret := readCommit(t, r, repo, "secret")
	readHg(t, r, repo, "phase", "-q", "--secret", "--force", "-r", string(secret))
	ctx := context.Background()
	if _, err := r.Changeset(ctx, repo, secret); !errors.Is(err, ErrNotFound) {
		t.Errorf("secret Changeset: %v", err)
	}
	if _, err := r.Changeset(ctx, repo, NodeID(strings.Repeat("a", 40))); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown Changeset: %v", err)
	}
	if n, err := r.Resolve(ctx, repo, RefSelection{RefNode, string(secret)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("secret Resolve = %s, %v", n, err)
	}
}

func TestNonUTF8RefUnsupported(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "raw")
	node := readCommit(t, r, repo, "base")
	// Command lines cannot carry raw bytes on every platform; write the file.
	if err := os.WriteFile(filepath.Join(repo, ".hg", "bookmarks"), []byte(string(node)+" \xff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refs, err := r.References(context.Background(), repo); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("References = %v, %v; want ErrUnsupportedEncoding", refs, err)
	}
}

func TestPushVisibleOnNextRead(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "push")
	first := readCommit(t, r, repo, "first")
	ctx := context.Background()
	refs, err := r.References(ctx, repo)
	if err != nil || len(refs) != 1 || refs[0].Node != first {
		t.Fatalf("before: %+v, %v", refs, err)
	}
	second := readCommit(t, r, repo, "second")
	refs, err = r.References(ctx, repo)
	if err != nil || len(refs) != 1 || refs[0].Node != second {
		t.Fatalf("after: %+v, %v; want head %s", refs, err, second)
	}
}

func TestReadsNeedService(t *testing.T) {
	r := newTestRunner(t, Config{})
	repo := t.TempDir()
	ctx := context.Background()
	if _, err := r.References(ctx, repo); !errors.Is(err, ErrReadService) {
		t.Errorf("References: %v", err)
	}
	if _, err := r.Changeset(ctx, repo, NodeID(strings.Repeat("a", 40))); !errors.Is(err, ErrReadService) {
		t.Errorf("Changeset: %v", err)
	}
}

// fakeReads is a stand-in read service answering canned JSON by operation name.
type fakeReads struct {
	mu     sync.Mutex
	bodies []string
}

// useFakeReads points r's reads at a fake service. root must contain the
// repositories the test reads.
func useFakeReads(t *testing.T, r *Runner, root string, handlers map[string]string) *fakeReads {
	t.Helper()
	f := &fakeReads{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var q struct{ OperationName string }
		json.Unmarshal(body, &q)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		resp, ok := handlers[q.OperationName]
		if !ok {
			resp = fakeError("INTERNAL")
		}
		w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c := NewReadClient(root)
	c.SetEndpoint(strings.TrimPrefix(srv.URL, "http://"), "")
	r.SetReads(c)
	return f
}

func (f *fakeReads) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

func fakeError(code string) string {
	return `{"errors":[{"message":"fake","extensions":{"code":"` + code + `"}}]}`
}

// fakeRefsData is the service's answer to the Refs operation for refs.
func fakeRefsData(t *testing.T, refs []Reference) string {
	t.Helper()
	type gql struct {
		Kind, Name, NameBase64, Node string
		Revision                     int64
		Closed                       bool
	}
	out := []gql{}
	for _, ref := range refs {
		out = append(out, gql{strings.ToUpper(string(ref.Kind)), ref.Name, base64.StdEncoding.EncodeToString([]byte(ref.Name)), string(ref.Node), ref.Revision, ref.Closed})
	}
	b, err := json.Marshal(map[string]interface{}{"data": map[string]interface{}{"repo": map[string]interface{}{"refs": out}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeChangesetData is the service's answer to the Changeset operation for a
// root changeset.
func fakeChangesetData(node NodeID) string {
	return `{"data":{"repo":{"changeset":{"id":"` + string(node) + `","revision":0,"branch":"default","user":"t","comments":"root","time":{"time":0,"tz":0},"parents":[]}}}}`
}

func fakeServedData(node NodeID) string {
	return `{"data":{"repo":{"changeset":{"id":"` + string(node) + `"}}}}`
}

// historyFakeRunner is a Runner reading from a fake service that answers the
// History operation with data, and the repository it reads.
func historyFakeRunner(t *testing.T, data string) (*Runner, string, *fakeReads) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, Config{})
	f := useFakeReads(t, r, root, map[string]string{"History": data})
	return r, filepath.Join(root, "repo"), f
}

const fakeHistoryEmpty = `{"data":{"repo":{"history":{"snapshot":"` + "0123456789012345678901234567890123456789" + `","snapshotRevision":0,"nextBefore":0,"hasMore":false,"entries":[]}}}}`

func TestHistoryCursorErrors(t *testing.T) {
	r, repo, f := historyFakeRunner(t, fakeHistoryEmpty)
	node := NodeID(strings.Repeat("a", 40))
	for name, q := range map[string]HistoryQuery{
		"before below -1":             {BeforeRevision: -2},
		"negative limit":              {BeforeRevision: -1, Limit: -1},
		"before without snapshot":     {BeforeRevision: 3},
		"malformed snapshot":          {Snapshot: "tip", SnapshotRevision: 5, BeforeRevision: -1},
		"negative snapshot revision":  {Snapshot: node, SnapshotRevision: -1, BeforeRevision: -1},
		"before beyond snapshot":      {Snapshot: node, SnapshotRevision: 5, BeforeRevision: 6},
		"unusable snapshot, no limit": {Snapshot: "", SnapshotRevision: 0, BeforeRevision: 0},
	} {
		if _, err := r.History(context.Background(), repo, q); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: %v; want ErrInvalidCursor", name, err)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests for invalid queries", n)
	}
	// A cursor only the service can tell is stale also maps.
	r, repo, _ = historyFakeRunner(t, fakeError("INVALID_CURSOR"))
	if _, err := r.History(context.Background(), repo, HistoryQuery{Snapshot: node, SnapshotRevision: 5, BeforeRevision: 2}); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("service INVALID_CURSOR: %v", err)
	}
}

func TestHistoryEmptyPathOmitted(t *testing.T) {
	r, repo, f := historyFakeRunner(t, fakeHistoryEmpty)
	if _, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1}); err != nil {
		t.Fatal(err)
	}
	var body struct{ Variables map[string]interface{} }
	reqs := f.requests()
	if len(reqs) != 1 || json.Unmarshal([]byte(reqs[0]), &body) != nil {
		t.Fatalf("requests %q", reqs)
	}
	for _, k := range []string{"path", "snapshot", "snapshotRevision"} {
		if v, ok := body.Variables[k]; ok {
			t.Errorf("variable %s = %v sent", k, v)
		}
	}
}

func TestHistoryNonUTF8PathUnsupported(t *testing.T) {
	r, repo, f := historyFakeRunner(t, fakeHistoryEmpty)
	if _, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1, Path: "caf\xe9"}); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("History = %v; want ErrUnsupportedEncoding", err)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
}

func TestHistoryEmpty(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "empty")
	if _, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1}); !errors.Is(err, ErrEmptyRepository) {
		t.Fatalf("History = %v; want ErrEmptyRepository", err)
	}
}

// historyNodes walks every page of q and returns the nodes in order.
func historyNodes(t *testing.T, r *Runner, repo string, q HistoryQuery) []NodeID {
	t.Helper()
	got := []NodeID{}
	for {
		page, err := r.History(context.Background(), repo, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, cs := range page.Entries {
			got = append(got, cs.Node)
		}
		if !page.HasMore {
			return got
		}
		q.Snapshot, q.SnapshotRevision, q.BeforeRevision = page.Snapshot, page.SnapshotRevision, page.NextBefore
	}
}

func hgNodes(t *testing.T, r *Runner, repo string, args ...string) []NodeID {
	t.Helper()
	nodes := []NodeID{}
	for _, n := range strings.Fields(readHg(t, r, repo, append([]string{"log", "-T", "{node}\n"}, args...)...)) {
		nodes = append(nodes, NodeID(n))
	}
	return nodes
}

func TestHistoryPagesMatchHg(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "pages")
	readHg(t, r, repo, "debugbuilddag", "+250")
	readHg(t, r, repo, "phase", "-q", "--secret", "--force", "-r", "tip")
	want := hgNodes(t, r, repo, "-r", "sort((0:249) and not secret(), -rev)")
	if len(want) != 249 {
		t.Fatalf("hg lists %d changesets", len(want))
	}
	got := historyNodes(t, r, repo, HistoryQuery{BeforeRevision: -1, Limit: 30})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("History lists %d changesets, hg %d; first difference differs", len(got), len(want))
	}
}

func TestFileHistoryFollowsRenames(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "follow")
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("old", "one\n")
	readHg(t, r, repo, "add", "old")
	readCommit(t, r, repo, "created")
	readHg(t, r, repo, "rename", "old", "new")
	readCommit(t, r, repo, "renamed")
	write("other", "x\n")
	readHg(t, r, repo, "add", "other")
	readCommit(t, r, repo, "unrelated")
	write("new", "two\n")
	readCommit(t, r, repo, "edited")
	want := hgNodes(t, r, repo, "--follow", "-r", "max(not secret())", "--", "path:new")
	if len(want) != 3 {
		t.Fatalf("hg follows %d changesets", len(want))
	}
	for _, limit := range []int{1, 2, 30} {
		if got := historyNodes(t, r, repo, HistoryQuery{BeforeRevision: -1, Path: "new", Limit: limit}); !reflect.DeepEqual(got, want) {
			t.Errorf("limit %d: %v; want %v", limit, got, want)
		}
	}
	if _, err := r.History(context.Background(), repo, HistoryQuery{BeforeRevision: -1, Path: "old"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("path absent at the tip: %v", err)
	}
}

func TestHistoryDeletedFileWithAnchor(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "deleted")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readHg(t, r, repo, "add", "f")
	added := readCommit(t, r, repo, "added")
	readHg(t, r, repo, "remove", "f")
	readCommit(t, r, repo, "removed")
	ctx := context.Background()
	if _, err := r.History(ctx, repo, HistoryQuery{BeforeRevision: -1, Path: "f"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted at the tip: %v; want ErrNotFound", err)
	}
	page, err := r.History(ctx, repo, HistoryQuery{Snapshot: added, SnapshotRevision: 0, BeforeRevision: -1, Path: "f"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Node != added {
		t.Fatalf("anchored at %s: %+v, %v", added, page, err)
	}
}

// treePatch is applied with import --bypass: Windows cannot check out a
// symlink or a latin-1 name, but the repository can hold them.
const treePatch = "# HG changeset patch\n# User t\n# Date 0 0\ntree\n\n" +
	"diff --git a/run.sh b/run.sh\nnew file mode 100755\n--- /dev/null\n+++ b/run.sh\n@@ -0,0 +1,1 @@\n+#!/bin/sh\n" +
	"diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1,1 @@\n+d/x\n\\ No newline at end of file\n" +
	"diff --git a/d/x b/d/x\nnew file mode 100644\n--- /dev/null\n+++ b/d/x\n@@ -0,0 +1,1 @@\n+sub\n" +
	"diff --git a/d/e/f b/d/e/f\nnew file mode 100644\n--- /dev/null\n+++ b/d/e/f\n@@ -0,0 +1,1 @@\n+deep\n" +
	"diff --git a/caf\xe9 b/caf\xe9\nnew file mode 100644\n--- /dev/null\n+++ b/caf\xe9\n@@ -0,0 +1,1 @@\n+latin-1 name\n"

// treeRepo makes a repository holding treePatch and returns it with its node.
func treeRepo(t *testing.T, r *Runner, name string, initArgs ...string) (string, NodeID) {
	t.Helper()
	repo := filepath.Join(r.reads.root, name)
	readHg(t, r, "", append([]string{"init"}, append(initArgs, repo)...)...)
	return repo, importPatch(t, r, repo, treePatch)
}

// importPatch commits patch to repo without a checkout and returns its node.
func importPatch(t *testing.T, r *Runner, repo, patch string) NodeID {
	t.Helper()
	file := filepath.Join(t.TempDir(), "p.diff")
	if err := os.WriteFile(file, []byte(patch), 0600); err != nil {
		t.Fatal(err)
	}
	readHg(t, r, repo, "import", "-q", "--bypass", file)
	return NodeID(readHg(t, r, repo, "log", "-r", "tip", "-T", "{node}"))
}

// manifestChildren is the immediate children of dir read from "hg manifest -v".
func manifestChildren(t *testing.T, r *Runner, repo string, node NodeID, dir string) []TreeEntry {
	t.Helper()
	prefix := dir
	if dir != "" {
		prefix += "/"
	}
	byName := map[string]TreeEntry{}
	for _, line := range strings.Split(readHg(t, r, repo, "manifest", "-v", "-r", string(node)), "\n") {
		if len(line) < 7 || !strings.HasPrefix(line[6:], prefix) {
			continue
		}
		name, _, isDir := strings.Cut(line[6+len(prefix):], "/")
		e := TreeEntry{Name: name, Directory: isDir}
		if !isDir {
			e.Executable, e.Symlink = line[:6] == "755 * ", line[:6] == "644 @ "
		}
		byName[name] = e
	}
	want := []TreeEntry{}
	for _, e := range byName {
		want = append(want, e)
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
	return want
}

func checkListings(t *testing.T, r *Runner, repo string, node NodeID) {
	t.Helper()
	for _, dir := range []string{"", "d", "d/e"} {
		got, err := r.ListDirectory(context.Background(), repo, node, dir)
		if want := manifestChildren(t, r, repo, node, dir); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ListDirectory(%q) = %+v, %v\nmanifest says %+v", dir, got, err, want)
		}
	}
}

func TestListDirectoryMatchesHg(t *testing.T) {
	r := newReadRunner(t)
	repo, node := treeRepo(t, r, "flat")
	checkListings(t, r, repo, node)
	if _, err := r.ListDirectory(context.Background(), repo, node, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing directory: %v", err)
	}
}

func TestListDirectoryTreeManifest(t *testing.T) {
	r := newReadRunner(t)
	repo, node := treeRepo(t, r, "tree", "--config", "experimental.treemanifest=1")
	if !strings.Contains(readHg(t, r, repo, "debugrequires"), "treemanifest") {
		t.Skip("this Mercurial did not make a tree manifest")
	}
	checkListings(t, r, repo, node)
}

func TestListPathFile(t *testing.T) {
	r := newReadRunner(t)
	repo, node := treeRepo(t, r, "file")
	ctx := context.Background()
	for p, want := range map[string]TreeEntry{
		"run.sh": {Name: "run.sh", Executable: true},
		"link":   {Name: "link", Symlink: true},
		"d/x":    {Name: "x"},
	} {
		entries, file, err := r.ListPath(ctx, repo, node, p)
		if err != nil || len(entries) != 0 || file == nil || *file != want {
			t.Errorf("ListPath(%q) = %+v, %+v, %v; want file %+v", p, entries, file, err, want)
		}
	}
	entries, file, err := r.ListPath(ctx, repo, node, "d")
	if err != nil || file != nil || len(entries) != 2 {
		t.Errorf("ListPath(d) = %+v, %+v, %v", entries, file, err)
	}
	if entries, file, err = r.ListPath(ctx, repo, node, ""); err != nil || file != nil || len(entries) != 4 {
		t.Errorf("ListPath(root) = %+v, %+v, %v", entries, file, err)
	}
}

func TestListPathUnknownNotFound(t *testing.T) {
	r := newReadRunner(t)
	repo, node := treeRepo(t, r, "unknown")
	for _, p := range []string{"nope", "d/nope", "run.sh/x", "../a", "D/x"} {
		if _, _, err := r.ListPath(context.Background(), repo, node, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("ListPath(%q): %v", p, err)
		}
	}
}

func TestTreeNonUTF8NameExact(t *testing.T) {
	r := newReadRunner(t)
	repo, node := treeRepo(t, r, "latin1")
	entries, err := r.ListDirectory(context.Background(), repo, node, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		found = found || e.Name == "caf\xe9"
	}
	if !found {
		t.Errorf("no entry named %q in %+v", "caf\xe9", entries)
	}
	// The service takes directory names as strings.
	if _, err := r.ListDirectory(context.Background(), repo, node, "d/caf\xe9"); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Errorf("non-UTF-8 directory: %v", err)
	}
	if _, _, err := r.ListPath(context.Background(), repo, node, "caf\xe9"); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Errorf("non-UTF-8 path: %v", err)
	}
}

func TestReadFileBinaryMatchesCat(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "binary")
	content := make([]byte, 0, 3<<20)
	for len(content) < 3<<20 {
		for b := 0; b < 256; b++ {
			content = append(content, byte(b))
		}
	}
	content = append(content, "\r\n\x00trailing"...)
	if err := os.WriteFile(filepath.Join(repo, "blob.bin"), content, 0600); err != nil {
		t.Fatal(err)
	}
	node := readCommit(t, r, repo, "binary", "-A")
	var got bytes.Buffer
	if err := r.ReadFile(context.Background(), repo, node, "blob.bin", &got); err != nil || !bytes.Equal(got.Bytes(), content) {
		t.Fatalf("ReadFile returned %d bytes, %v; want %d", got.Len(), err, len(content))
	}
	var cat bytes.Buffer
	if err := r.run(context.Background(), repo, []string{"cat", "-r", string(node), "blob.bin"}, nil, &cat, -1); err != nil || !bytes.Equal(cat.Bytes(), content) {
		t.Errorf("hg cat returned %d bytes, %v; want %d", cat.Len(), err, len(content))
	}
}

func TestReadFilePathsNeedingEscape(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "escape")
	names := []string{"a b.txt", "100%.txt", "a&b+c.txt", "naïve.txt"}
	for _, name := range names[:3] {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("content of "+name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	readCommit(t, r, repo, "names", "-A")
	// A patch stores the UTF-8 name whatever the platform's file encoding.
	node := importPatch(t, r, repo, "# HG changeset patch\n# User t\n# Date 0 0\nutf8\n\ndiff --git a/naïve.txt b/naïve.txt\nnew file mode 100644\n--- /dev/null\n+++ b/naïve.txt\n@@ -0,0 +1,1 @@\n+content of naïve.txt\n")
	for _, name := range names {
		var got bytes.Buffer
		if err := r.ReadFile(context.Background(), repo, node, name, &got); err != nil || got.String() != "content of "+name+"\n" {
			t.Errorf("ReadFile(%q) = %q, %v", name, got.String(), err)
		}
	}
}

func TestReadFileNonUTF8Name(t *testing.T) {
	r := newReadRunner(t)
	repo, node := treeRepo(t, r, "rawname")
	var got bytes.Buffer
	if err := r.ReadFile(context.Background(), repo, node, "caf\xe9", &got); err != nil || got.String() != "latin-1 name\n" {
		t.Errorf("ReadFile = %q, %v", got.String(), err)
	}
}

func TestReadFileSecretNotFound(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "secretfile")
	if err := os.WriteFile(filepath.Join(repo, "s.txt"), []byte("s"), 0600); err != nil {
		t.Fatal(err)
	}
	node := readCommit(t, r, repo, "secret", "-A")
	readHg(t, r, repo, "phase", "-q", "--secret", "--force", "-r", string(node))
	ctx := context.Background()
	if err := r.ReadFile(ctx, repo, node, "s.txt", io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReadFile: %v", err)
	}
	if _, err := r.ListDirectory(ctx, repo, node, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("ListDirectory: %v", err)
	}
}

func TestReadFileServiceDiesMidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("only a part"))
	}))
	t.Cleanup(srv.Close)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewReadClient(root)
	c.SetEndpoint(strings.TrimPrefix(srv.URL, "http://"), "")
	r := newTestRunner(t, Config{})
	r.SetReads(c)
	err = r.ReadFile(context.Background(), filepath.Join(root, "repo"), NodeID(strings.Repeat("a", 40)), "f", io.Discard)
	if !errors.Is(err, ErrReadService) {
		t.Fatalf("ReadFile = %v; want ErrReadService", err)
	}
}

func TestStreamWriterErrorNotWrapped(t *testing.T) {
	c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("abc")) })
	boom := errors.New("boom")
	_, err := c.Stream(context.Background(), "/raw", nil, writerFunc(func([]byte) (int, error) { return 0, boom }))
	if !errors.Is(err, boom) || errors.Is(err, ErrReadService) {
		t.Fatalf("Stream = %v; want the writer's error alone", err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A push rejected by a hook rolls its transaction back, which renames and
// truncates revlogs; a repository read through the service must allow that.
func TestFailedPushRollsBackAfterRead(t *testing.T) {
	r := newReadRunner(t)
	repo := readRepo(t, r, "target")
	readCommit(t, r, repo, "first")
	ctx := context.Background()
	if _, err := r.References(ctx, repo); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(r.reads.root, "clone")
	readHg(t, r, "", "clone", repo, clone)
	readCommit(t, r, clone, "second")

	hgrc := filepath.Join(repo, ".hg", "hgrc")
	if err := os.WriteFile(hgrc, []byte("[hooks]\npretxnchangegroup.reject = exit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, clone, []string{"push"}, nil, io.Discard); err == nil {
		t.Fatal("push was not rejected")
	}
	readHg(t, r, repo, "verify")
	if err := os.Remove(hgrc); err != nil {
		t.Fatal(err)
	}
	readHg(t, r, clone, "push")
	if refs, err := r.References(ctx, repo); err != nil || len(refs) != 1 {
		t.Fatalf("after push: %+v, %v", refs, err)
	}
}
