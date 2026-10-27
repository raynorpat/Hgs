//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

// This probe exercises the real native/package path on generated local data.
// It never supplies an upstream acquisition receipt or accesses Mozilla data.
func TestMozillaOrderedFreshProbe(t *testing.T) {
	base := os.Getenv("GOGS_TEST_MOZILLA_ORDERED_PROBE_ROOT")
	if base == "" {
		t.Skip("explicit tiny ordered package probe")
	}
	if !filepath.IsAbs(base) || os.Getenv("GOGS_TEST_PACKAGE_DIRECT") != "1" {
		t.Fatal("probe requires an absolute owned root and direct extracted package")
	}
	for _, mode := range []string{"success", "read-failure"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp(base, mode+"-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("retained probe root: " + root)
			run := func() ([]byte, error) {
				exe, _ := os.Executable()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMozillaOrderedProbeChild$", "-test.v", "-test.timeout=7m")
				cmd.Env = append(os.Environ(), "GOGS_TEST_MOZILLA_ORDERED_CHILD="+mode, "GOGS_TEST_MOZILLA_ORDERED_CHILD_ROOT="+root)
				return cmd.CombinedOutput()
			}
			out, err := run()
			if e := os.WriteFile(filepath.Join(root, "first-run.log"), out, 0600); e != nil {
				t.Fatal(e)
			}
			if (err == nil) != (mode == "success") {
				t.Fatalf("unexpected child outcome: %v\n%s", err, out)
			}
			var closed struct {
				CleanApplicationStop, DatabaseClosed, TestFailed bool
				Results                                          map[string]bool
				Fixture                                          string
			}
			data, err := os.ReadFile(filepath.Join(root, "ordered-cold-checkpoint.json"))
			if err != nil || json.Unmarshal(data, &closed) != nil {
				t.Fatalf("missing cold checkpoint after ordered run: %v\n%s", err, out)
			}
			if !closed.CleanApplicationStop || !closed.DatabaseClosed || closed.TestFailed != (mode != "success") {
				t.Fatalf("unclean or misleading checkpoint: %s", data)
			}
			for _, phase := range []string{"source", "https-ingestion", "ssh-ingestion", "https-verification", "ssh-verification"} {
				if !closed.Results[phase] {
					t.Fatalf("missing successful %s: %s", phase, data)
				}
			}
			if mode == "success" && (!closed.Results["pristine-reads"] || !closed.Results["https-workflows"] || !closed.Results["ssh-workflows"] || !closed.Results["issues"]) {
				t.Fatalf("ordered gates missing: %s", data)
			}
			if mode == "read-failure" {
				if closed.Results["pristine-reads"] || closed.Results["https-workflows"] || !bytes.Contains(out, []byte("active-stream cancellation unverified")) {
					t.Fatalf("failure did not stop before writes: %s\n%s", data, out)
				}
			}
			for _, name := range append(append([]string{}, mozillaStaticFiles...), "application.db", "repos/1/.hg/requires", "repos/2/.hg/requires") {
				if _, err := os.Stat(filepath.Join(closed.Fixture, name)); err != nil {
					t.Fatalf("fixture was not retained: %s: %v", name, err)
				}
			}
			var identity struct {
				Fixture                        string
				Files                          map[string]string
				UserIDs, KeyIDs, RepositoryIDs map[string]int64
			}
			witness, err := os.ReadFile(filepath.Join(root, "raw", "ordered-fixture-identity.json"))
			if err != nil || json.Unmarshal(witness, &identity) != nil || identity.Fixture != closed.Fixture {
				t.Fatalf("fixture identity witness missing or substituted: %v", err)
			}
			if err := mozillaCheckStaticFiles(closed.Fixture, identity.Files); err != nil {
				t.Fatal(err)
			}
			for i, role := range []string{"siteadmin", "owner", "writer", "reader", "stranger"} {
				if identity.UserIDs[role] != int64(i+1) || identity.KeyIDs[role] != int64(i+1) {
					t.Fatalf("account/key IDs changed for %s", role)
				}
			}
			if identity.RepositoryIDs["public"] != 1 || identity.RepositoryIDs["private"] != 2 {
				t.Fatal("numeric repository identities changed")
			}
			counts := map[string]int{}
			lastPristine, firstWorkflow, index := 0, 0, 0
			samples, err := os.ReadFile(filepath.Join(root, "raw", "samples.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(bytes.NewReader(samples))
			for scanner.Scan() {
				index++
				var s mozillaSample
				if json.Unmarshal(scanner.Bytes(), &s) != nil {
					t.Fatal("invalid sample")
				}
				if s.Kind == "native-hg" {
					counts[s.Name]++
				}
				if strings.HasPrefix(s.Name, "baseline-") || s.Name == "concurrent-history" || strings.HasPrefix(s.Name, "source-file-") {
					lastPristine = index
				}
				if s.Name == "full-clone-https" && firstWorkflow == 0 {
					firstWorkflow = index
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"source-verify", "initial-push-https", "initial-push-ssh"} {
				if counts[name] != 1 {
					t.Fatalf("%s ran %d times", name, counts[name])
				}
			}
			if mode == "success" {
				if firstWorkflow <= lastPristine || counts["full-clone-https"] != 1 || counts["full-clone-ssh"] != 1 {
					t.Fatal("pristine reads did not precede exactly one workflow per transport")
				}
				for _, name := range []string{"baseline-process-startup", "baseline-manifest", "baseline-history", "baseline-file-history"} {
					if counts[name] != 5 {
						t.Fatalf("%s samples=%d, want 5", name, counts[name])
					}
				}
			} else if firstWorkflow != 0 || counts["full-clone-ssh"] != 0 {
				t.Fatal("later read failure was followed by workflow writes")
			}
			replay, replayErr := run()
			os.WriteFile(filepath.Join(root, "blocked-replay.log"), replay, 0600)
			if replayErr == nil || !bytes.Contains(replay, []byte("ordered run already attempted")) {
				t.Fatalf("repeat invocation not blocked: %v\n%s", replayErr, replay)
			}
			after, _ := os.ReadFile(filepath.Join(root, "raw", "samples.jsonl"))
			checkpointAfter, _ := os.ReadFile(filepath.Join(root, "ordered-cold-checkpoint.json"))
			if !bytes.Equal(samples, after) || !bytes.Equal(data, checkpointAfter) {
				t.Fatal("blocked invocation changed evidence or replayed work")
			}
		})
	}
}

func TestMozillaOrderedProbeChild(t *testing.T) {
	mode := os.Getenv("GOGS_TEST_MOZILLA_ORDERED_CHILD")
	if mode == "" {
		t.Skip("subprocess only")
	}
	root := os.Getenv("GOGS_TEST_MOZILLA_ORDERED_CHILD_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("missing probe root")
	}
	raw := filepath.Join(root, "raw")
	if err := os.MkdirAll(raw, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := hg.NewRunner(hg.Config{Executable: hgPath(t), ConfigPath: filepath.Join(raw, "native", "hgrc"), MaxProcesses: 8, SessionTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	m := &mozillaRun{t: t, root: root, raw: raw, source: filepath.Join(root, "tiny-local-source"), r: r, deadline: 30 * time.Second}
	m.freshOrdered(func() { prepareMozillaOrderedProbe(m, mode) })
}

func prepareMozillaOrderedProbe(m *mozillaRun, mode string) {
	m.must("tiny-init", "", "init", m.source)
	if err := os.Mkdir(filepath.Join(m.source, "dom"), 0700); err != nil {
		m.t.Fatal(err)
	}
	media := make([]byte, 4<<20)
	if _, err := rand.Read(media); err != nil {
		m.t.Fatal(err)
	}
	if mode == "read-failure" {
		media = media[:64]
	}
	for name, body := range map[string][]byte{"dom/readme.txt": []byte("text\n"), "image.png": {0, 1, 2}, "clip.webm": media, "café.txt": []byte("native path bytes\n")} {
		if err := os.WriteFile(filepath.Join(m.source, name), body, 0600); err != nil {
			m.t.Fatal(err)
		}
	}
	m.must("tiny-add", m.source, "add")
	m.must("tiny-commit", m.source, "commit", "-u", "probe", "-m", "initial")
	m.must("tiny-copy", m.source, "copy", "dom/readme.txt", "dom/copied.txt")
	m.must("tiny-commit", m.source, "commit", "-u", "probe", "-m", "copy")
	for i := 0; i < 31; i++ {
		if err := os.WriteFile(filepath.Join(m.source, "dom/readme.txt"), []byte(fmt.Sprintf("text %d\n", i)), 0600); err != nil {
			m.t.Fatal(err)
		}
		m.must("tiny-commit", m.source, "commit", "-u", "probe", "-m", "history")
	}
	m.must("tiny-update", m.source, "update", "-r", "0")
	if err := os.WriteFile(filepath.Join(m.source, "side.txt"), []byte("side\n"), 0600); err != nil {
		m.t.Fatal(err)
	}
	m.must("tiny-add-side", m.source, "add", "side.txt")
	m.must("tiny-commit", m.source, "commit", "-u", "probe", "-m", "side")
	m.must("tiny-merge", m.source, "merge", "-r", "32", "--tool", ":merge")
	m.must("tiny-commit", m.source, "commit", "-u", "probe", "-m", "merge")
	m.must("source-verify", m.source, "verify")
	nodes := m.must("tiny-nodes", m.source, "log", "-r", "sort(all(), rev)", "-T", "{node}\n")
	m.identity = mozillaIdentity{URL: "generated-local-probe", VerificationState: "VERIFIED", Changesets: nodes.Bytes / 41, NodesSHA256: nodes.SHA256}
	m.identity.Tip = m.text("tiny-tip", m.source, "log", "-r", "tip", "-T", "{node}")
	m.identity.Heads = m.text("tiny-heads", m.source, "log", "-r", "heads(all())", "-T", "{node}\n")
	m.identity.Branches = m.text("tiny-branches", m.source, "branches", "--closed", "-T", "{branch}\t{node}\t{closed}\n")
	m.identity.Tags = mozillaTrackedTags(m.text("tiny-tags", m.source, "tags", "-T", "{tag}\t{node}\n"))
	m.identity.Bookmarks = m.text("tiny-bookmarks", m.source, "bookmarks", "-T", "{bookmark}\t{node}\n")
	for _, p := range []string{".hg/requires", ".hg/store/requires"} {
		b, err := os.ReadFile(filepath.Join(m.source, p))
		if err != nil && !os.IsNotExist(err) {
			m.t.Fatal(err)
		}
		m.identity.Requirements += p + ":\n" + string(b)
	}
	m.browseNode = m.identity.Tip
	if !strings.Contains(m.identity.Branches, "default\t") {
		m.t.Fatal("missing code branch")
	}
}
