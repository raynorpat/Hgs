//go:build integration

package hg

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

type reviewFixture struct {
	t          *testing.T
	r          *Runner
	root, work string
	ctx        context.Context
	nodes      map[string]NodeID
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	root := t.TempDir()
	r, err := NewRunner(Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(root, "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	f := &reviewFixture{t: t, r: r, root: root, work: filepath.Join(root, "work"), ctx: context.Background(), nodes: map[string]NodeID{}}
	f.hg("", "init", f.work)
	f.write("a", "base\n")
	f.hg(f.work, "add", "-q")
	f.commit("base")
	return f
}
func (f *reviewFixture) hg(repo string, args ...string) string {
	f.t.Helper()
	var out bytes.Buffer
	if err := f.r.Run(f.ctx, repo, args, nil, &out); err != nil {
		f.t.Fatalf("hg %v: %v", args, err)
	}
	return out.String()
}
func (f *reviewFixture) write(path, content string) {
	f.t.Helper()
	p := filepath.Join(f.work, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *reviewFixture) commit(name string) {
	f.t.Helper()
	f.hg(f.work, "commit", "-q", "-u", "fixture", "-d", "0 0", "-m", name)
	f.nodes[name] = NodeID(f.hg(f.work, "log", "-r", ".", "-T", "{node}"))
}
func (f *reviewFixture) update(name string) {
	f.t.Helper()
	f.hg(f.work, "update", "-q", "-C", "-r", string(f.nodes[name]))
}
func (f *reviewFixture) endpoint(path, name string) ReviewEndpoint {
	f.t.Helper()
	e, err := f.r.ResolveReview(f.ctx, path, ReviewSelection{Ref: RefSelection{Kind: RefNode, Name: string(f.nodes[name])}})
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}
func (f *reviewFixture) snapshot(source, target string) ReviewSnapshot {
	f.t.Helper()
	s, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, source), f.endpoint(f.work, target), filepath.Join(f.root, "snapshot"))
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func reviewStore(t *testing.T, path string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(filepath.Join(path, ".hg", "store"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(path, p)
			m[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReviewSnapshotDoesNotChangeTarget(t *testing.T) {
	f := newReviewFixture(t)
	target := filepath.Join(f.root, "target")
	f.hg("", "clone", "-q", "-U", "--pull", f.work, target)
	f.write("a", "source\n")
	f.commit("source")
	f.hg(f.work, "bookmark", "-r", string(f.nodes["source"]), "selected")
	f.write("a", "unselected visible\n")
	f.commit("visibleExcluded")
	f.write("a", "excluded\n")
	f.commit("excluded")
	f.hg(f.work, "phase", "--force", "--secret", "-r", string(f.nodes["excluded"]))
	before := reviewStore(t, target)
	s, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "source"), f.endpoint(target, "base"), filepath.Join(f.root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, reviewStore(t, target)) {
		t.Fatal("target store changed during review")
	}
	if got := f.hg(s.Path, "log", "-r", "all()", "-T", "{node}\n"); strings.Contains(got, string(f.nodes["excluded"])) || strings.Contains(got, string(f.nodes["visibleExcluded"])) || !strings.Contains(got, string(f.nodes["source"])) {
		t.Fatalf("unexpected snapshot history: %s", got)
	}
	if got := f.hg(s.Path, "bookmarks", "-T", "{bookmark}"); got != "" {
		t.Fatalf("unselected bookmark copied: %s", got)
	}
	if _, err := os.Stat(filepath.Join(s.Path, "a")); !os.IsNotExist(err) {
		t.Fatalf("snapshot has checkout: %v", err)
	}
}
func TestReviewSelectionRejectsAmbiguousHead(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "one\n")
	f.commit("one")
	f.update("base")
	f.write("a", "two\n")
	f.commit("two")
	sel := ReviewSelection{Ref: RefSelection{Kind: RefBranch, Name: "default"}}
	if _, err := f.r.ResolveReview(f.ctx, f.work, sel); !errors.Is(err, ErrAmbiguousHead) {
		t.Fatalf("ambiguous: %v", err)
	}
	for _, name := range []string{"one", "two"} {
		sel.Head = f.nodes[name]
		got, err := f.r.ResolveReview(f.ctx, f.work, sel)
		if err != nil || got.Node != f.nodes[name] {
			t.Fatalf("explicit %s: %+v %v", name, got, err)
		}
	}
	sel.Head = f.nodes["base"]
	if _, err := f.r.ResolveReview(f.ctx, f.work, sel); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonhead: %v", err)
	}
	if _, err := f.r.Resolve(f.ctx, f.work, sel.Ref); err != nil {
		t.Fatalf("ordinary browsing changed: %v", err)
	}
	empty := filepath.Join(f.root, "empty")
	f.hg("", "init", empty)
	if _, err := f.r.ResolveReview(f.ctx, empty, ReviewSelection{Ref: RefSelection{Kind: RefBranch, Name: "default"}}); !errors.Is(err, ErrEmptyRepository) {
		t.Fatalf("empty: %v", err)
	}
}
func TestReviewComparisonPinsNodes(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "source\n")
	f.commit("source")
	s := f.snapshot("source", "base")
	first, err := f.r.CompareReview(f.ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	f.write("a", "later\n")
	f.commit("later")
	second, err := f.r.CompareReview(f.ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.Base != f.nodes["base"] || len(first.Changesets) != 1 || first.Changesets[0].Node != f.nodes["source"] || !bytes.Contains(first.Diff.Text, []byte("+source")) {
		t.Fatalf("unpinned comparison: %+v %+v", first, second)
	}
}
func TestReviewMergeCases(t *testing.T) {
	for _, mode := range []string{"same_branch", "cross_branch", "divergent", "already_integrated", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			f := newReviewFixture(t)
			if mode == "cross_branch" {
				f.hg(f.work, "branch", "feature")
			}
			f.write("a", "source\n")
			f.commit("source")
			target := "base"
			if mode == "already_integrated" {
				target = "source"
			} else if mode == "divergent" || mode == "conflict" {
				f.update("base")
				if mode == "conflict" {
					f.write("a", "target\n")
				} else {
					f.write("b", "target\n")
					f.hg(f.work, "add", "b")
				}
				f.commit("target")
				target = "target"
			}
			source := "source"
			if mode == "already_integrated" {
				source = "base"
			}
			s := f.snapshot(source, target)
			before := reviewStore(t, f.work)
			p, err := f.r.PrepareReviewMerge(f.ctx, s, filepath.Join(f.root, "prepared"), "Actor <actor@example.test>", "Merge PR #1")
			if mode == "conflict" {
				if !errors.Is(err, ErrMergeConflict) {
					t.Fatalf("conflict: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "two_parent"
			if mode == "same_branch" {
				want = "fast_forward"
			}
			if mode == "already_integrated" {
				want = "already_integrated"
			}
			if p.Mode != want || p.DestinationBranch != "default" {
				t.Fatalf("prepared: %+v", p)
			}
			cs, err := f.r.Changeset(f.ctx, p.Path, p.Result)
			if err != nil {
				t.Fatal(err)
			}
			if cs.Branch != "default" {
				t.Fatalf("result branch %s", cs.Branch)
			}
			if want == "two_parent" && !reflect.DeepEqual(cs.Parents, []NodeID{s.Target, s.Source}) {
				t.Fatalf("parents: %v", cs.Parents)
			}
			if want == "fast_forward" && p.Result != s.Source || want == "already_integrated" && p.Result != s.Target {
				t.Fatalf("result: %+v", p)
			}
			if !reflect.DeepEqual(before, reviewStore(t, f.work)) {
				t.Fatal("preparation changed target")
			}
		})
	}
}
func TestReviewCheckoutNeverExecutesContent(t *testing.T) {
	for _, kind := range []string{"config", "subrepo", "symlink_and_case_collision"} {
		t.Run(kind, func(t *testing.T) {
			f := newReviewFixture(t)
			f.divergentReviewTarget()
			if kind == "subrepo" {
				f.write(".hgsub", "nested = https://127.0.0.1:1/forbidden\n")
				f.hg(f.work, "add", ".hgsub")
				f.commit("source")
			} else if kind == "symlink_and_case_collision" {
				patch := filepath.Join(f.root, "unsafe.patch")
				if err := os.WriteFile(patch, []byte(strings.ReplaceAll(strings.ReplaceAll(specialFiles, "a/a b/a", "a/A b/A"), "+++ b/a", "+++ b/A")), 0600); err != nil {
					t.Fatal(err)
				}
				f.hg(f.work, "import", "--bypass", patch)
				f.nodes["source"] = NodeID(f.hg(f.work, "log", "-r", "tip", "-T", "{node}"))
			} else {
				f.write("a", "source\n")
				f.commit("source")
			}
			marker := filepath.Join(f.root, "EXECUTED")
			script := filepath.Join(f.root, "sentinel.cmd")
			if err := os.WriteFile(script, []byte("@echo executed > \""+marker+"\"\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "config" {
				rc := "[hooks]\npre-log = " + script + "\npre-update = " + script + "\n[ui]\nmerge = " + script + "\n[extensions]\nforbidden = " + filepath.Join(f.root, "forbidden.py") + "\n"
				if err := os.WriteFile(filepath.Join(f.work, ".hg", "hgrc"), []byte(rc), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := f.snapshot("source", "target")
			if s.Base == s.Source || s.Base == s.Target {
				t.Fatal("refusal fixture must require a two-parent merge")
			}
			_, err := f.r.PrepareReviewMerge(f.ctx, s, filepath.Join(f.root, "prepared"), "Actor", "Merge")
			if kind != "config" && !errors.Is(err, ErrUnsafeCheckout) {
				t.Fatalf("unsafe checkout: %v", err)
			}
			if kind == "config" && err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("repository executable invoked: %v", err)
			}
		})
	}
}

func TestReviewRejectsUnrelatedAndNoChanges(t *testing.T) {
	f := newReviewFixture(t)
	if _, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "base"), f.endpoint(f.work, "base"), filepath.Join(f.root, "same")); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("same endpoints: %v", err)
	}
	f.hg(f.work, "update", "-q", "-r", "null")
	f.write("unrelated", "other root\n")
	f.hg(f.work, "add", "unrelated")
	f.commit("unrelated")
	output := filepath.Join(f.root, "unrelated-snapshot")
	if _, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "unrelated"), f.endpoint(f.work, "base"), output); !errors.Is(err, ErrUnrelatedHistory) {
		t.Fatalf("unrelated: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed artifact retained: %v", err)
	}
}

func TestReviewRejectsMultipleMergeBases(t *testing.T) {
	f := newReviewFixture(t)
	f.write("left", "left\n")
	f.hg(f.work, "add", "left")
	f.commit("left")
	f.update("base")
	f.write("right", "right\n")
	f.hg(f.work, "add", "right")
	f.commit("right")
	f.hg(f.work, "merge", "--tool", ":merge", "-r", string(f.nodes["left"]))
	f.commit("rightMerge")
	f.update("left")
	f.hg(f.work, "merge", "--tool", ":merge", "-r", string(f.nodes["right"]))
	f.commit("leftMerge")
	_, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "leftMerge"), f.endpoint(f.work, "rightMerge"), filepath.Join(f.root, "snapshot"))
	if !errors.Is(err, ErrAmbiguousHead) {
		t.Fatalf("arbitrary common ancestor accepted: %v", err)
	}
}

func TestReviewComparisonBoundsAndBinary(t *testing.T) {
	for _, kind := range []string{"binary", "large"} {
		t.Run(kind, func(t *testing.T) {
			f := newReviewFixture(t)
			content := "\x00binary\x01"
			if kind == "large" {
				content = strings.Repeat("x", MaxDiffBytes+1024) + "\n"
			}
			f.write("a", content)
			f.commit("source")
			s := f.snapshot("source", "base")
			got, err := f.r.CompareReview(f.ctx, s)
			if kind == "binary" {
				if err != nil || !got.Diff.Binary {
					t.Fatalf("binary summary: %+v %v", got.Diff, err)
				}
			} else if !errors.Is(err, ErrComparisonTooLarge) || !got.Diff.Truncated || len(got.Diff.Text) > MaxDiffBytes {
				t.Fatalf("large diff: bytes=%d truncated=%v err=%v", len(got.Diff.Text), got.Diff.Truncated, err)
			}
		})
	}
}

func TestReviewPreparedBookmarkIdentity(t *testing.T) {
	f := newReviewFixture(t)
	f.hg(f.work, "bookmark", "-r", string(f.nodes["base"]), "destination")
	f.hg(f.work, "bookmark", "-r", string(f.nodes["base"]), "unrelated")
	f.write("a", "source\n")
	f.commit("source")
	target, err := f.r.ResolveReview(f.ctx, f.work, ReviewSelection{Ref: RefSelection{Kind: RefBookmark, Name: "destination"}})
	if err != nil {
		t.Fatal(err)
	}
	before := f.hg(f.work, "bookmarks", "-T", "{bookmark} {node}\n")
	s, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "source"), target, filepath.Join(f.root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.r.PrepareReviewMerge(f.ctx, s, filepath.Join(f.root, "prepared"), "Actor", "PR #1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Bookmark != "destination" || p.Mode != "fast_forward" {
		t.Fatalf("bookmark selection lost: %+v", p)
	}
	if after := f.hg(f.work, "bookmarks", "-T", "{bookmark} {node}\n"); after != before {
		t.Fatalf("public bookmarks moved: %q -> %q", before, after)
	}
}

func TestReviewDoesNotRunExternalMergeTool(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "source\n")
	f.commit("source")
	f.update("base")
	f.write("a", "target\n")
	f.commit("target")
	s := f.snapshot("source", "target")
	marker := filepath.Join(f.root, "EXECUTED")
	script := filepath.Join(f.root, "sentinel.cmd")
	if err := os.WriteFile(script, []byte("@echo executed > \""+marker+"\"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rc := "[ui]\nmerge = " + script + "\n[hooks]\npre-update = " + script + "\npre-merge = " + script + "\nprecommit = " + script + "\n[merge-tools]\nmalicious.executable = " + script + "\nmalicious.priority = 100000\n"
	if err := os.WriteFile(filepath.Join(s.Path, ".hg", "hgrc"), []byte(rc), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HGMERGE", script)
	if _, err := f.r.PrepareReviewMerge(f.ctx, s, filepath.Join(f.root, "prepared"), "Actor", "PR #1"); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected native conflict: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("external merge tool or hook invoked: %v", err)
	}
}

func TestReviewArtifactsCannotReusePublishedPaths(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "source\n")
	f.commit("source")
	s := f.snapshot("source", "base")
	before := reviewStore(t, f.work)
	for _, output := range []string{f.work, filepath.Join(f.work, "private-review"), filepath.Dir(f.work)} {
		if _, err := f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "source"), f.endpoint(f.work, "base"), output); err == nil {
			t.Fatalf("published/overlapping path accepted: %s", output)
		}
	}
	if _, err := f.r.PrepareReviewMerge(f.ctx, s, f.work, "Actor", "PR #1"); err == nil {
		t.Fatal("published output reused")
	}
	bad := s
	bad.Source = s.Target
	if _, err := f.r.CompareReview(f.ctx, bad); !errors.Is(err, ErrUnsafeCheckout) {
		t.Fatalf("tampered artifact pin accepted: %v", err)
	}
	if !reflect.DeepEqual(before, reviewStore(t, f.work)) {
		t.Fatal("published store changed on rejected artifact operation")
	}
}

func TestReviewCheckoutRejectsCaseCollisionAcrossEndpoints(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("requires a native case-insensitive host")
	}
	f := newReviewFixture(t)
	f.divergentReviewTarget()
	patch := "# HG changeset patch\n# User fixture\n# Date 0 0\ncase-only change\n\ndiff --git a/a b/a\ndeleted file mode 100644\n--- a/a\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-base\ndiff --git a/A b/A\nnew file mode 100644\n--- /dev/null\n+++ b/A\n@@ -0,0 +1,1 @@\n+source\n"
	patchPath := filepath.Join(f.root, "case.patch")
	if err := os.WriteFile(patchPath, []byte(patch), 0600); err != nil {
		t.Fatal(err)
	}
	f.hg(f.work, "import", "--bypass", patchPath)
	f.nodes["source"] = NodeID(f.hg(f.work, "log", "-r", "tip", "-T", "{node}"))
	s := f.snapshot("source", "target")
	if s.Base == s.Source || s.Base == s.Target {
		t.Fatal("refusal fixture must require a two-parent merge")
	}
	if _, err := f.r.PrepareReviewMerge(f.ctx, s, filepath.Join(f.root, "prepared"), "Actor", "Merge"); !errors.Is(err, ErrUnsafeCheckout) {
		t.Fatalf("cross-endpoint path collision accepted: %v", err)
	}
}

func TestReviewNoOutboundSubrepositoryRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	f := newReviewFixture(t)
	f.divergentReviewTarget()
	f.write(".hgsub", "nested = "+server.URL+"/repository\n")
	f.hg(f.work, "add", ".hgsub")
	f.commit("source")
	s := f.snapshot("source", "target")
	if s.Base == s.Source || s.Base == s.Target {
		t.Fatal("refusal fixture must require a two-parent merge")
	}
	if _, err := f.r.PrepareReviewMerge(f.ctx, s, filepath.Join(f.root, "prepared"), "Actor", "Merge"); !errors.Is(err, ErrUnsafeCheckout) {
		t.Fatalf("nested repository accepted: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("made %d outbound nested-repository requests", got)
	}
}

func TestReviewRejectsHiddenAndSecretEndpoints(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "secret\n")
	f.commit("secret")
	f.hg(f.work, "phase", "--force", "--secret", "-r", string(f.nodes["secret"]))
	if _, err := f.r.ResolveReview(f.ctx, f.work, ReviewSelection{Ref: RefSelection{Kind: RefNode, Name: string(f.nodes["secret"])}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("secret endpoint: %v", err)
	}
	f.update("base")
	f.write("hidden", "hidden\n")
	f.hg(f.work, "add", "hidden")
	f.commit("hidden")
	f.update("base")
	f.hg(f.work, "--config", "experimental.evolution=createmarkers", "debugobsolete", string(f.nodes["hidden"]))
	if _, err := f.r.ResolveReview(f.ctx, f.work, ReviewSelection{Ref: RefSelection{Kind: RefNode, Name: string(f.nodes["hidden"])}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden endpoint: %v", err)
	}
	f.write("a", "source\n")
	f.commit("source")
	s := f.snapshot("source", "base")
	got := f.hg(s.Path, "log", "--hidden", "-r", "all()", "-T", "{node}\n")
	if strings.Contains(got, string(f.nodes["hidden"])) || strings.Contains(got, string(f.nodes["secret"])) {
		t.Fatalf("nonserved history copied: %s", got)
	}
}

func TestReviewRejectsMovedOrDeletedSelection(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "source\n")
	f.commit("source")
	f.hg(f.work, "bookmark", "-r", string(f.nodes["source"]), "source-selection")
	source, err := f.r.ResolveReview(f.ctx, f.work, ReviewSelection{Ref: RefSelection{Kind: RefBookmark, Name: "source-selection"}})
	if err != nil {
		t.Fatal(err)
	}
	target := f.endpoint(f.work, "base")
	f.hg(f.work, "bookmark", "--force", "-r", string(f.nodes["base"]), "source-selection")
	if _, err = f.r.SnapshotReview(f.ctx, source, target, filepath.Join(f.root, "moved")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("moved source accepted: %v", err)
	}
	f.hg(f.work, "bookmark", "--delete", "source-selection")
	if _, err = f.r.SnapshotReview(f.ctx, source, target, filepath.Join(f.root, "deleted")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted source accepted: %v", err)
	}
	untouched := filepath.Join(f.root, "unrelated-output")
	if err = os.Mkdir(untouched, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(untouched, "keep")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.SnapshotReview(f.ctx, f.endpoint(f.work, "source"), target, untouched); err == nil {
		t.Fatal("existing output replaced")
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
		t.Fatalf("unrelated output damaged: %q %v", b, err)
	}
}

func TestReviewUnsupportedPathsRemainBrowsable(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("native Windows/macOS materialization policy")
	}
	f := newReviewFixture(t)
	f.divergentReviewTarget()
	name := "漢字"
	patch := "# HG changeset patch\n# User fixture\n# Date 0 0\nUnicode path\n\ndiff --git a/" + name + " b/" + name + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + name + "\n@@ -0,0 +1,1 @@\n+stored content\n"
	patchPath := filepath.Join(f.root, "unicode.patch")
	if err := os.WriteFile(patchPath, []byte(patch), 0600); err != nil {
		t.Fatal(err)
	}
	f.hg(f.work, "import", "--bypass", patchPath)
	f.nodes["source"] = NodeID(f.hg(f.work, "log", "-r", "tip", "-T", "{node}"))
	s := f.snapshot("source", "target")
	if s.Base == s.Source || s.Base == s.Target {
		t.Fatal("refusal fixture must require a two-parent merge")
	}
	entries, err := f.r.Manifest(f.ctx, s.Path, s.Source)
	if err != nil {
		t.Fatalf("manifest browsing failed: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.Path == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("stored name lost: %+v", entries)
	}
	output := filepath.Join(f.root, "prepared")
	if _, err := f.r.PrepareReviewMerge(f.ctx, s, output, "Actor", "Merge"); !errors.Is(err, ErrUnsafeCheckout) {
		t.Fatalf("unsupported materialization accepted: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("checkout began before preflight: %v", err)
	}
}

// Store-only operations must accept history which this host cannot check out.
// Reusing the exact node neither invokes update/merge nor accesses subrepos.
func TestReviewStoreOnlyModesDoNotMaterialize(t *testing.T) {
	for _, mode := range []string{"fast_forward", "already_integrated"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			f := newReviewFixture(t)
			patch := "# HG changeset patch\n# User fixture\n# Date 0 0\nstore-only unsafe checkout tree\n\n" +
				"diff --git a/a b/a\ndeleted file mode 100644\n--- a/a\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-base\n" +
				"diff --git a/A b/A\nnew file mode 100644\n--- /dev/null\n+++ b/A\n@@ -0,0 +1,1 @@\n+case rename\n" +
				"diff --git a/漢字 b/漢字\nnew file mode 100644\n--- /dev/null\n+++ b/漢字\n@@ -0,0 +1,1 @@\n+Unicode content\n" +
				"diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1,1 @@\n+../outside\n\\ No newline at end of file\n" +
				"diff --git a/.hgsub b/.hgsub\nnew file mode 100644\n--- /dev/null\n+++ b/.hgsub\n@@ -0,0 +1,1 @@\n+nested = " + server.URL + "/repository\n" +
				"diff --git a/.hgsubstate b/.hgsubstate\nnew file mode 100644\n--- /dev/null\n+++ b/.hgsubstate\n@@ -0,0 +1,1 @@\n+0000000000000000000000000000000000000000 nested\n"
			patchPath := filepath.Join(f.root, "store-only.patch")
			if err := os.WriteFile(patchPath, []byte(patch), 0600); err != nil {
				t.Fatal(err)
			}
			f.hg(f.work, "import", "--bypass", patchPath)
			f.nodes["unsupported"] = NodeID(f.hg(f.work, "log", "-r", "tip", "-T", "{node}"))
			sourcePath, targetPath := filepath.Join(f.root, "public-source"), filepath.Join(f.root, "public-target")
			for _, path := range []string{sourcePath, targetPath} {
				f.hg("", "clone", "--pull", "-U", "-r", string(f.nodes["unsupported"]), f.work, path)
			}
			sourceName, targetName := "unsupported", "base"
			if mode == "already_integrated" {
				sourceName, targetName = "base", "unsupported"
			}
			sourceBefore, targetBefore := reviewStore(t, sourcePath), reviewStore(t, targetPath)
			snapshot, err := f.r.SnapshotReview(f.ctx, f.endpoint(sourcePath, sourceName), f.endpoint(targetPath, targetName), filepath.Join(f.root, "snapshot"))
			if err != nil {
				t.Fatal(err)
			}
			entries, err := f.r.Manifest(f.ctx, snapshot.Path, f.nodes["unsupported"])
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, entry := range entries {
				found[entry.Path] = true
				if entry.Path == "link" && !entry.Symlink {
					t.Fatal("fixture lost symlink kind")
				}
			}
			for _, name := range []string{"A", "漢字", "link", ".hgsub", ".hgsubstate"} {
				if !found[name] {
					t.Fatalf("fixture missing %q", name)
				}
			}
			prepared, err := f.r.PrepareReviewMerge(f.ctx, snapshot, filepath.Join(f.root, "prepared"), "Actor", "Merge PR #1")
			if err != nil {
				t.Fatalf("store-only %s incorrectly requires a checkout: %v", mode, err)
			}
			if prepared.Mode != mode || prepared.Result != f.nodes["unsupported"] || prepared.Source != snapshot.Source || prepared.Target != snapshot.Target {
				t.Fatalf("wrong history-preserving result: %+v", prepared)
			}
			result, err := f.r.Changeset(f.ctx, prepared.Path, prepared.Result)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Parents, []NodeID{f.nodes["base"]}) || result.Branch != "default" {
				t.Fatalf("existing result rewritten: %+v", result)
			}
			files, err := os.ReadDir(prepared.Path)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0].Name() != ".hg" {
				t.Fatalf("prepared artifact has materialized files: %+v", files)
			}
			if parents := f.hg(prepared.Path, "parents", "-T", "{node}"); parents != "" {
				t.Fatalf("prepared artifact has working parent %s", parents)
			}
			if !reflect.DeepEqual(sourceBefore, reviewStore(t, sourcePath)) || !reflect.DeepEqual(targetBefore, reviewStore(t, targetPath)) {
				t.Fatal("store-only preparation changed public store bytes")
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("store-only preparation made %d subrepository requests", got)
			}
		})
	}
}

// Prepare a distinct destination head before adding unsupported source content,
// so refusal fixtures exercise actual two-parent materialization rather than FF.
func (f *reviewFixture) divergentReviewTarget() {
	f.t.Helper()
	f.write("target-only", "destination\n")
	f.hg(f.work, "add", "target-only")
	f.commit("target")
	f.update("base")
}
