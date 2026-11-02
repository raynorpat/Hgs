package hg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDiffBoundsBinaryAndLongLines(t *testing.T) {
	for _, tc := range []struct {
		name              string
		input             []byte
		truncated, binary bool
	}{
		{"empty", nil, false, false},
		{"normal", []byte("--- a/x\n+++ b/x\n+hello\n"), false, false},
		{"binary", []byte("diff -r abc x\nBinary file x has changed\n"), false, true},
		{"long line", bytes.Repeat([]byte("x"), MaxDiffBytes+1), true, false},
		{"many lines", bytes.Repeat([]byte("x\n"), MaxDiffLines+1), true, false},
		{"exact bytes", bytes.Repeat([]byte("x"), MaxDiffBytes), false, false},
		{"exact lines", bytes.Repeat([]byte("x\n"), MaxDiffLines), false, false},
		{"line limit then partial line", append(bytes.Repeat([]byte("x\n"), MaxDiffLines), []byte("tail")...), true, false},
		{"binary-like content", []byte("+Binary file x has changed\n"), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newDiffWriter()
			for pos := 0; pos < len(tc.input); {
				end := pos + 8191
				if end > len(tc.input) {
					end = len(tc.input)
				}
				_, err := w.Write(tc.input[pos:end])
				if errors.Is(err, ErrOutputLimit) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				pos = end
			}
			d := w.result()
			if len(d.Text) > MaxDiffBytes || bytes.Count(d.Text, []byte("\n")) > MaxDiffLines || d.Truncated != tc.truncated || d.Binary != tc.binary {
				t.Fatalf("diff bytes=%d lines=%d truncated=%v binary=%v", len(d.Text), bytes.Count(d.Text, []byte("\n")), d.Truncated, d.Binary)
			}
		})
	}
}

// fakeDiffReads points r's reads at a service answering /diff with status 200,
// the given header (if any) and body.
func fakeDiffReads(t *testing.T, r *Runner, root string, truncatedHeader bool, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/diff" || req.URL.Query().Get("parent") == "" || req.URL.Query().Get("max") != strconv.Itoa(MaxDiffBytes) {
			t.Errorf("request %s", req.URL)
		}
		if truncatedHeader {
			w.Header().Set("X-Truncated", "true")
		}
		for chunk := body; len(chunk) > 0; {
			n := min(len(chunk), 32768)
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			chunk = chunk[n:]
		}
	}))
	t.Cleanup(srv.Close)
	c := NewReadClient(root)
	c.SetEndpoint(strings.TrimPrefix(srv.URL, "http://"), "")
	r.SetReads(c)
}

func TestDiffServerTruncated(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, Config{})
	fakeDiffReads(t, r, root, true, []byte("diff --git a/x b/x\n"))
	d, err := r.Diff(context.Background(), filepath.Join(root, "repo"), Changeset{Node: newerDefaultNode}, 0)
	if err != nil || !d.Truncated || string(d.Text) != "diff --git a/x b/x\n" {
		t.Fatalf("Diff = %+v, %v", d, err)
	}
}

func TestDiffOverLimitTruncated(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, Config{})
	fakeDiffReads(t, r, root, false, bytes.Repeat([]byte("x"), MaxDiffBytes+1000))
	d, err := r.Diff(context.Background(), filepath.Join(root, "repo"), Changeset{Node: newerDefaultNode}, 0)
	if err != nil || !d.Truncated || len(d.Text) != MaxDiffBytes {
		t.Fatalf("Diff bytes=%d truncated=%v, %v", len(d.Text), d.Truncated, err)
	}
}

// compareFake is a review snapshot under a fake service answering
// ChangesetsBetween with n changesets and /diff with a one-line diff, and the
// /diff queries it received.
func compareFake(t *testing.T, n int) (*Runner, ReviewSnapshot, *[]url.Values) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := ReviewSnapshot{Path: filepath.Join(root, "cmp", "repo"), Base: NodeID(strings.Repeat("b", 40)), Source: NodeID(strings.Repeat("c", 40)), Target: NodeID(strings.Repeat("d", 40))}
	if err := os.MkdirAll(s.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeReviewMetadata(s.Path, reviewMetadata{Version: 1, Source: s.Source, Target: s.Target, Base: s.Base}); err != nil {
		t.Fatal(err)
	}
	cs := func(node NodeID) string {
		return `{"id":"` + string(node) + `","revision":0,"branch":"default","user":"t","comments":"c","time":{"time":0},"parents":[]}`
	}
	between := make([]string, n)
	for i := range between {
		between[i] = cs(s.Source)
	}
	var diffs []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/diff" {
			diffs = append(diffs, req.URL.Query())
			w.Write([]byte("diff --git a/x b/x\n"))
			return
		}
		body, _ := io.ReadAll(req.Body)
		switch {
		case bytes.Contains(body, []byte(`"operationName":"CommonHeads"`)):
			w.Write([]byte(`{"data":{"repo":{"commonHeads":[` + cs(s.Base) + `]}}}`))
		case bytes.Contains(body, []byte(`"operationName":"ChangesetsBetween"`)):
			w.Write([]byte(`{"data":{"repo":{"changesetsBetween":[` + strings.Join(between, ",") + `]}}}`))
		default:
			t.Errorf("request %s", body)
		}
	}))
	t.Cleanup(srv.Close)
	r := newTestRunner(t, Config{})
	c := NewReadClient(root)
	c.SetEndpoint(strings.TrimPrefix(srv.URL, "http://"), "")
	r.SetReads(c)
	return r, s, &diffs
}

func TestCompareReviewChangesetCap(t *testing.T) {
	r, s, diffs := compareFake(t, MaxReviewChangesets)
	got, err := r.CompareReview(context.Background(), s)
	if err != nil || len(got.Changesets) != MaxReviewChangesets {
		t.Fatalf("at the cap: %d changesets, %v", len(got.Changesets), err)
	}
	if len(*diffs) != 1 || (*diffs)[0].Get("max") != strconv.Itoa(MaxDiffBytes) {
		t.Fatalf("diff queries %v", *diffs)
	}
	r, s, diffs = compareFake(t, MaxReviewChangesets+1)
	if _, err := r.CompareReview(context.Background(), s); !errors.Is(err, ErrComparisonTooLarge) {
		t.Fatalf("over the cap: %v", err)
	}
	if len(*diffs) != 0 {
		t.Fatalf("over the cap still diffed: %v", *diffs)
	}
}

// diffHeaders returns the lines whose text must match hg's: file headers,
// copy and rename lines, and modes. Hunks may differ.
func diffHeaders(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, p := range []string{"diff --git ", "rename ", "copy ", "old mode ", "new mode ", "new file mode ", "deleted file mode ", "similarity index ", "Binary file ", "--- ", "+++ "} {
			if strings.HasPrefix(line, p) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

// diffFixture builds a repository with a root, a rename/copy/delete/binary
// change and, by import --bypass so Windows can hold them, mode changes and a
// symlink. It returns the nodes in order.
func diffFixture(t *testing.T, r *Runner, name string, binary bool) (string, []NodeID) {
	t.Helper()
	repo := readRepo(t, r, name)
	write := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) NodeID {
		readHg(t, r, repo, "commit", "-q", "-A", "-u", "t", "-d", "0 0", "-m", msg)
		return NodeID(readHg(t, r, repo, "log", "-r", ".", "-T", "{node}"))
	}
	body := strings.Repeat("line\n", 20)
	write("a.txt", body)
	write("b.txt", body+"b\n")
	write("gone.txt", "gone\n")
	if binary {
		write("bin", "a\x00b")
	}
	nodes := []NodeID{commit("root")}
	readHg(t, r, repo, "mv", "a.txt", "renamed.txt")
	readHg(t, r, repo, "cp", "b.txt", "copied.txt")
	readHg(t, r, repo, "rm", "gone.txt")
	if binary {
		write("bin", "a\x00c")
	}
	write("b.txt", body+"b changed\n")
	nodes = append(nodes, commit("rename copy delete"))
	patch := filepath.Join(t.TempDir(), "modes.diff")
	modes := "# HG changeset patch\n# User t\n# Date 0 0\nmodes\n\n" +
		"diff --git a/b.txt b/b.txt\nold mode 100644\nnew mode 100755\n" +
		"diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1,1 @@\n+b.txt\n\\ No newline at end of file\n" +
		"diff --git a/run.sh b/run.sh\nnew file mode 100755\n--- /dev/null\n+++ b/run.sh\n@@ -0,0 +1,1 @@\n+#!/bin/sh\n"
	if err := os.WriteFile(patch, []byte(modes), 0600); err != nil {
		t.Fatal(err)
	}
	readHg(t, r, repo, "import", "-q", "--bypass", patch)
	nodes = append(nodes, NodeID(readHg(t, r, repo, "log", "-r", "tip", "-T", "{node}")))
	return repo, nodes
}

func TestDiffHeadersMatchHg(t *testing.T) {
	r := newReadRunner(t)
	repo, nodes := diffFixture(t, r, "headers", true)
	for i, node := range nodes {
		parent := "null"
		if i > 0 {
			parent = string(nodes[i-1])
		}
		want := diffHeaders(readHg(t, r, repo, "diff", "--git", "--nodates", "--no-binary", "-r", parent, "-r", string(node)))
		d, err := diffNode(r, context.Background(), repo, node, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := diffHeaders(string(d.Text)); !reflect.DeepEqual(got, want) {
			t.Errorf("changeset %d headers\n got %q\nwant %q", i, got, want)
		}
		if d.Binary != (i < 2) || d.Truncated {
			t.Errorf("changeset %d: Binary=%v Truncated=%v", i, d.Binary, d.Truncated)
		}
	}
}

func TestDiffAppliesToSameTree(t *testing.T) {
	r := newReadRunner(t)
	repo, nodes := diffFixture(t, r, "applies", false)
	copyRepo := readRepo(t, r, "applied")
	for i, node := range nodes {
		d, err := diffNode(r, context.Background(), repo, node, 0)
		if err != nil {
			t.Fatal(err)
		}
		patch := filepath.Join(t.TempDir(), "p.diff")
		if err := os.WriteFile(patch, append([]byte("# HG changeset patch\n# User t\n# Date 0 0\nc\n\n"), d.Text...), 0600); err != nil {
			t.Fatal(err)
		}
		readHg(t, r, copyRepo, "import", "-q", "--bypass", patch)
		// Bypass imports read the working parent, which a commit does not move.
		readHg(t, r, copyRepo, "debugsetparents", readHg(t, r, copyRepo, "log", "-r", "tip", "-T", "{node}"))
		// --debug adds each file's node, which covers content and copy data.
		want := readHg(t, r, repo, "manifest", "--debug", "-r", string(node))
		if got := readHg(t, r, copyRepo, "manifest", "--debug", "-r", "tip"); got != want {
			t.Fatalf("changeset %d applies to\n%s\nwant\n%s", i, got, want)
		}
	}
}

func TestDiffRootAgainstNull(t *testing.T) {
	r := newReadRunner(t)
	repo, nodes := diffFixture(t, r, "root", false)
	d, err := diffNode(r, context.Background(), repo, nodes[0], 0)
	if err != nil || !bytes.Contains(d.Text, []byte("diff --git a/a.txt b/a.txt\nnew file mode 100644\n")) || !bytes.Contains(d.Text, []byte("+line\n")) {
		t.Fatalf("root diff %q, %v", d.Text, err)
	}
	if _, err := diffNode(r, context.Background(), repo, nodes[0], 1); err == nil {
		t.Error("root accepted parent 1")
	}
}
