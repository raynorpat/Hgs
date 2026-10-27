//go:build integration

package hg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkNeverCopiesSecrets(t *testing.T) {
	f := newReviewFixture(t)
	f.write("a", "second")
	f.commit("second")
	f.hg(f.work, "bookmark", "-r", string(f.nodes["base"]), "old")
	f.hg(f.work, "bookmark", "-r", string(f.nodes["second"]), "new")
	f.hg(f.work, "tag", "-u", "fixture", "-d", "0 0", "-r", string(f.nodes["base"]), "global-tag")
	f.hg(f.work, "tag", "--local", "-r", string(f.nodes["base"]), "local-tag")
	f.hg(f.work, "tag", "-u", "fixture", "-d", "0 0", "-r", string(f.nodes["base"]), "deleted-tag")
	f.hg(f.work, "tag", "--local", "--force", "-r", string(f.nodes["base"]), "deleted-tag")
	f.hg(f.work, "tag", "--local", "--remove", "deleted-tag")
	f.hg(f.work, "tag", "-u", "fixture", "-d", "0 0", "-r", string(f.nodes["base"]), "repointed-tag")
	f.hg(f.work, "tag", "--local", "--force", "-r", string(f.nodes["second"]), "repointed-tag")
	f.hg(f.work, "tag", "-u", "fixture", "-d", "0 0", "-r", string(f.nodes["base"]), "secret-only-global")
	f.hg(f.work, "phase", "--force", "--secret", "-r", "tip")
	f.write("a", "secret")
	f.commit("secret")
	f.hg(f.work, "phase", "--force", "--secret", "-r", string(f.nodes["secret"]))
	for _, name := range []string{"hgrc", "hgrc-not-shared"} {
		if err := os.WriteFile(filepath.Join(f.work, ".hg", name), []byte("[hooks]\npreoutgoing = exit 99\n[auth]\nsecret.password = private-password\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(f.root, "fork")
	if err := f.r.CloneVisible(f.ctx, f.work, out); err != nil {
		t.Fatal(err)
	}
	log := f.hg(out, "log", "-T", "{node}\n")
	if !strings.Contains(log, string(f.nodes["base"])) || !strings.Contains(log, string(f.nodes["second"])) || strings.Contains(log, string(f.nodes["secret"])) {
		t.Fatalf("visible history: %s", log)
	}
	if refs := f.hg(out, "bookmarks", "-T", "{bookmark}\n"); !strings.Contains(refs, "old") || !strings.Contains(refs, "new") {
		t.Fatalf("bookmarks: %s", refs)
	}
	localTags, err := os.ReadFile(filepath.Join(out, ".hg", "localtags"))
	if err != nil || strings.Contains(string(localTags), "global-tag") {
		t.Fatalf("global tag became local override: %s %v", localTags, err)
	}
	if tags := f.hg(out, "tags", "-T", "{tag}\n"); !strings.Contains(tags, "local-tag") {
		t.Fatalf("visible local tag omitted: %s", tags)
	}
	if tags := f.hg(out, "tags", "-T", "{tag}\n"); strings.Contains(tags, "secret-only-global") {
		t.Fatalf("secret global metadata re-exported: %s", tags)
	}
	if tags := f.hg(out, "tags", "-T", "{tag}\n"); strings.Contains(tags, "deleted-tag") {
		t.Fatalf("deleted local tag resurrected: %s", tags)
	}
	if node := f.hg(out, "log", "-r", "tag('repointed-tag')", "-T", "{node}"); node != string(f.nodes["second"]) {
		t.Fatalf("local repoint lost: %s", node)
	}
	for _, name := range []string{"sharedpath", "hgrc-not-shared"} {
		if _, err := os.Stat(filepath.Join(out, ".hg", name)); !os.IsNotExist(err) {
			t.Fatalf("copied %s: %v", name, err)
		}
	}
	config, err := os.ReadFile(filepath.Join(out, ".hg", "hgrc"))
	if err != nil || strings.Contains(string(config), "password") || strings.Contains(string(config), f.work) {
		t.Fatalf("config: %s %v", config, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".hg" {
		t.Fatalf("checkout: %v %v", entries, err)
	}
	second := filepath.Join(f.root, "fork-of-fork")
	if err := f.r.CloneVisible(f.ctx, out, second); err != nil {
		t.Fatal(err)
	}
	if f.hg(second, "log", "-T", "{node}\n") != log {
		t.Fatal("fork of fork changed history")
	}
	empty := filepath.Join(f.root, "empty")
	f.hg("", "init", empty)
	emptyFork := filepath.Join(f.root, "empty-fork")
	if err := f.r.CloneVisible(f.ctx, empty, emptyFork); err != nil {
		t.Fatal(err)
	}
	if f.hg(emptyFork, "log", "-T", "{node}") != "" {
		t.Fatal("empty fork gained history")
	}
	if err := f.r.CloneVisible(f.ctx, out, out); err == nil {
		t.Fatal("clone replaced source")
	}
}

func TestForkAllVisibleBranchesAndIndependentStorage(t *testing.T) {
	f := newReviewFixture(t)
	f.hg(f.work, "branch", "feature")
	f.write("a", "feature")
	f.commit("feature")
	f.hg(f.work, "commit", "--close-branch", "-u", "fixture", "-d", "0 0", "-m", "closed-feature")
	f.update("base")
	f.write("a", "head-one")
	f.commit("head-one")
	f.update("base")
	f.write("a", "head-two")
	f.commit("head-two")
	out := filepath.Join(f.root, "all-visible")
	if err := f.r.CloneVisible(f.ctx, f.work, out); err != nil {
		t.Fatal(err)
	}
	want := f.hg(f.work, "log", "-T", "{node} {branch}\n")
	if got := f.hg(out, "log", "-T", "{node} {branch}\n"); got != want {
		t.Fatalf("full visible history differs: %s vs %s", got, want)
	}
	if got := f.hg(out, "heads", "-T", "{node}\n"); got != f.hg(f.work, "heads", "-T", "{node}\n") {
		t.Fatal("multiple visible heads lost")
	}
	if !strings.Contains(f.hg(out, "branches", "--closed", "-T", "{branch} {closed}\n"), "feature True") {
		t.Fatal("closed branch lost")
	}
	before := reviewStore(t, f.work)
	// Removing a destination store file cannot mutate the source's file contents;
	// --pull also avoids hardlinks, proved by native file identity when present.
	for rel := range before {
		left, e1 := os.Stat(filepath.Join(f.work, rel))
		right, e2 := os.Stat(filepath.Join(out, rel))
		if e1 == nil && e2 == nil && os.SameFile(left, right) {
			t.Fatalf("shared mutable file %s", rel)
		}
	}
}
