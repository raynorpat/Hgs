//go:build integration

package hosting

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

func TestReviewPublishSameRepositoryEndpoints(t *testing.T) {
	for _, kind := range []string{"heads", "bookmarks"} {
		t.Run(kind, func(t *testing.T) {
			exe := os.Getenv("GOGS_TEST_HG")
			if exe == "" {
				t.Fatal("GOGS_TEST_HG required")
			}
			s, owner, repo := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe; c.Mercurial.MaxProcesses = 1 })
			s.hookExecutable = nativeHookBinary(t)
			target, _ := models.RepositoryStoragePath(s.RepositoryRoot(), repo.Id)
			local := filepath.Join(t.TempDir(), "local")
			nativeRun(t, s.runner, "", "init", local)
			commit := func(name string) hg.NodeID {
				t.Helper()
				if err := os.WriteFile(filepath.Join(local, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
				nativeRun(t, s.runner, local, "add", name)
				nativeRun(t, s.runner, local, "commit", "-u", "alice", "-m", name)
				n, err := hg.ParseNodeID(nativeRun(t, s.runner, local, "log", "-r", ".", "-T", "{node}"))
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			base := commit("base")
			src := commit("source")
			nativeRun(t, s.runner, local, "update", "--clean", "-r", string(base))
			dst := commit("destination")
			nativeRun(t, s.runner, target, "pull", local)
			nativeRun(t, s.runner, target, "bookmark", "--inactive", "-r", string(src), "feature")
			nativeRun(t, s.runner, target, "bookmark", "--inactive", "-r", string(dst), "main")
			nativeRun(t, s.runner, target, "bookmark", "--inactive", "-r", string(base), "untouched")
			sourceSelection := hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBranch, Name: "default"}, Head: src}
			targetSelection := hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBranch, Name: "default"}, Head: dst}
			if kind == "bookmarks" {
				sourceSelection = hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "feature"}}
				targetSelection = hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "main"}}
			}
			sourceEndpoint, err := s.runner.ResolveReview(context.Background(), target, sourceSelection)
			if err != nil {
				t.Fatal(err)
			}
			targetEndpoint, err := s.runner.ResolveReview(context.Background(), target, targetSelection)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.runner.SnapshotReview(context.Background(), sourceEndpoint, targetEndpoint, filepath.Join(t.TempDir(), "snapshot"))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := s.runner.PrepareReviewMerge(context.Background(), snapshot, filepath.Join(t.TempDir(), "prepared"), "alice", "merge selected heads")
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Mode != "two_parent" {
				t.Fatalf("wrong mode %s", prepared.Mode)
			}
			e := MutationExpectation{RepoID: repo.Id, Selection: targetSelection, Node: dst, OperationID: "same-repository-operation", Source: MutationEndpoint{RepoID: repo.Id, Selection: sourceSelection, Node: src}, Prepared: prepared}
			if err = publish(t, s, owner.Id, e, target); err != nil {
				t.Fatal(err)
			}
			if got := nativeRun(t, s.runner, target, "heads", "-T", "{node}"); got != string(prepared.Result) {
				t.Fatalf("wrong surviving head: %s", got)
			}
			if got := nativeRun(t, s.runner, target, "log", "-r", string(prepared.Result), "-T", "{p1node}\n{p2node}\n"); got != string(dst)+"\n"+string(src)+"\n" {
				t.Fatalf("wrong merge parents: %s", got)
			}
			for name, want := range map[string]hg.NodeID{"feature": src, "untouched": base, "main": dst} {
				if name == "main" && kind == "bookmarks" {
					want = prepared.Result
				}
				current, err := s.runner.ResolveReview(context.Background(), target, hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: name}})
				if err != nil || current.Node != want {
					t.Fatalf("bookmark %s moved: %+v %v", name, current, err)
				}
			}
			before := nativeRun(t, s.runner, target, "log", "-T", "{node}\n")
			if err = publish(t, s, owner.Id, e, target); err != nil {
				t.Fatal(err)
			}
			if after := nativeRun(t, s.runner, target, "log", "-T", "{node}\n"); after != before || len(strings.Fields(after)) != 4 {
				t.Fatal("retry duplicated merge")
			}
			nativeRun(t, s.runner, target, "verify")
		})
	}
}
