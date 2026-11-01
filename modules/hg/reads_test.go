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
