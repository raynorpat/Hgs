//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
	"golang.org/x/crypto/ssh"
	"gopkg.in/ini.v1"
)

const mozillaURL = "https://hg-edge.mozilla.org/mozilla-central/"
const mozillaSourceUnverified = "TIME-BOUND-UNVERIFIED"

var mozillaStaticFiles = []string{"app.ini", "frontend.pem", "tls-key.pem", "known_hosts", "hostkey", "siteadmin-key", "owner-key", "writer-key", "reader-key", "stranger-key"}

func mozillaCheckStaticFiles(root string, hashes map[string]string) error {
	if len(hashes) != len(mozillaStaticFiles) {
		return errors.New("incomplete static identity witness")
	}
	for _, name := range mozillaStaticFiles {
		b, err := os.ReadFile(filepath.Join(root, name))
		digest := sha256.Sum256(b)
		if err != nil || !strings.EqualFold(hex.EncodeToString(digest[:]), hashes[name]) {
			return fmt.Errorf("static fixture identity mismatch: %s", name)
		}
	}
	return nil
}

func TestMozillaStaticIdentity(t *testing.T) {
	root := t.TempDir()
	hashes := map[string]string{}
	for _, name := range mozillaStaticFiles {
		body := []byte("identity for " + name)
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		hashes[name] = hex.EncodeToString(digest[:])
	}
	if err := mozillaCheckStaticFiles(root, hashes); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hostkey"), []byte("different identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if mozillaCheckStaticFiles(root, hashes) == nil {
		t.Fatal("changed host identity accepted")
	}
	delete(hashes, "hostkey")
	if mozillaCheckStaticFiles(root, hashes) == nil {
		t.Fatal("missing host identity accepted")
	}
}

type mozillaCandidate struct {
	State, Error, Fixture, ArmedReceipt              string
	KillAttempted, FixtureExists, HostJournalPresent bool
	RemainingOwned                                   []json.RawMessage
	PushReceipt, VerifyPhase                         mozillaSample
}

func mozillaValidateCandidate(root string, c mozillaCandidate) error {
	if c.State != "INTERRUPTED_RECOVERY_CANDIDATE" || c.Error != "" || !c.KillAttempted || !c.FixtureExists || c.HostJournalPresent || len(c.RemainingOwned) != 0 || c.PushReceipt.Name != "initial-push-https" || c.PushReceipt.Kind != "native-hg" || c.PushReceipt.Error != "" || c.PushReceipt.ExitCode != 0 || c.VerifyPhase.Name != "https-verify" || c.VerifyPhase.Kind != "phase-start" {
		return errors.New("candidate is not the authorized successful-seed preservation boundary")
	}
	rel, err := filepath.Rel(root, c.Fixture)
	if !filepath.IsAbs(c.Fixture) || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("retained fixture must be a dedicated descendant of the owned root")
	}
	return nil
}

func mozillaCodeNode(branches string) (string, error) {
	var node string
	for _, line := range strings.Split(branches, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] != "default" {
			continue
		}
		closed, err := strconv.ParseBool(fields[2])
		if err != nil || closed || node != "" {
			return "", errors.New("open default branch is absent or ambiguous")
		}
		if _, err := hg.ParseNodeID(fields[1]); err != nil {
			return "", err
		}
		node = fields[1]
	}
	if node == "" {
		return "", errors.New("open default code branch missing")
	}
	return node, nil
}

func mozillaHistoryLink(body []byte, node, path string) (string, error) {
	for _, match := range regexp.MustCompile(`href="([^"]+)"`).FindAllSubmatch(body, -1) {
		u, err := url.Parse(html.UnescapeString(string(match[1])))
		if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/owner/public/commits" || u.Fragment != "" {
			continue
		}
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(values) != 1 || len(values["cursor"]) != 1 {
			continue
		}
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(values.Get("cursor"))
		if err != nil {
			continue
		}
		var cursor struct {
			V      int    `json:"v"`
			Node   string `json:"node"`
			Rev    int64  `json:"rev"`
			Before int64  `json:"before"`
			Path   string `json:"path"`
		}
		if json.Unmarshal(decoded, &cursor) != nil || cursor.V != 1 || cursor.Node != node || cursor.Rev < 0 || cursor.Before != -1 {
			continue
		}
		p, err := base64.RawURLEncoding.Strict().DecodeString(cursor.Path)
		if err == nil && string(p) == path {
			return u.RequestURI(), nil
		}
	}
	return "", errors.New("source page did not supply a history cursor for the exact code node and file path")
}

func TestMozillaRetainedCandidateGuard(t *testing.T) {
	root := t.TempDir()
	valid := mozillaCandidate{State: "INTERRUPTED_RECOVERY_CANDIDATE", Fixture: filepath.Join(root, "fixture"), KillAttempted: true, FixtureExists: true, PushReceipt: mozillaSample{Name: "initial-push-https", Kind: "native-hg"}, VerifyPhase: mozillaSample{Name: "https-verify", Kind: "phase-start"}}
	if err := mozillaValidateCandidate(root, valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*mozillaCandidate)
	}{
		{"unproven-state", func(c *mozillaCandidate) { c.State = "ARMED_NO_STOP" }},
		{"failed-push", func(c *mozillaCandidate) { c.PushReceipt.Error = "context deadline exceeded" }},
		{"bad-native-exit", func(c *mozillaCandidate) { c.PushReceipt.ExitCode = 1 }},
		{"phase-only", func(c *mozillaCandidate) { c.PushReceipt.Kind = "phase-start" }},
		{"no-verify-boundary", func(c *mozillaCandidate) { c.VerifyPhase.Name = "initial-push-ssh" }},
		{"remaining-owner", func(c *mozillaCandidate) { c.RemainingOwned = []json.RawMessage{json.RawMessage(`{}`)} }},
		{"transaction", func(c *mozillaCandidate) { c.HostJournalPresent = true }},
		{"outside-root", func(c *mozillaCandidate) { c.Fixture = root + "-other/fixture" }},
		{"root-itself", func(c *mozillaCandidate) { c.Fixture = root }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.edit(&c)
			if mozillaValidateCandidate(root, c) == nil {
				t.Fatal("unsafe recovery candidate accepted")
			}
		})
	}
}

func TestMozillaCodeNodeAndHistorySelection(t *testing.T) {
	tip := strings.Repeat("a", 40)
	code := strings.Repeat("b", 40)
	branches := "tags-unified\t" + tip + "\tFalse\ndefault\t" + code + "\tFalse\n"
	if got, err := mozillaCodeNode(branches); err != nil || got != code {
		t.Fatalf("code node=%q err=%v; want open default, not global tip", got, err)
	}
	if _, err := mozillaCodeNode("default\t" + code + "\tTrue"); err == nil {
		t.Error("closed default accepted")
	}
	path := "dom/sample file.txt"
	cursor, _ := json.Marshal(struct {
		V      int    `json:"v"`
		Node   string `json:"node"`
		Rev    int64  `json:"rev"`
		Before int64  `json:"before"`
		Path   string `json:"path"`
	}{1, code, 123, -1, base64.RawURLEncoding.EncodeToString([]byte(path))})
	want := "/owner/public/commits?cursor=" + base64.RawURLEncoding.EncodeToString(cursor)
	body := []byte(`<a href="/owner/public/commits">all history</a><a href="` + html.EscapeString(want) + `">file history</a>`)
	if got, err := mozillaHistoryLink(body, code, path); err != nil || got != want {
		t.Fatalf("pinned UI history=%q err=%v", got, err)
	}
	if _, err := mozillaHistoryLink(body, tip, path); err == nil {
		t.Error("wrong snapshot accepted")
	}
	if _, err := mozillaHistoryLink(body, code, "other.txt"); err == nil {
		t.Error("wrong file path accepted")
	}
}

func TestMozillaRetainedFixtureRebind(t *testing.T) {
	if os.Getenv("GOGS_TEST_MOZILLA_RECOVERY_PROBE") != "1" {
		t.Skip("explicit tiny extracted-package recovery probe")
	}
	a := newConfiguredApplication(t, versions{name: "retained probe", server: hgPath(t), client: hgPath(t)}, func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "MAX_PROCESSES = 2", "MAX_PROCESSES = 8"), "SESSION_TIMEOUT = 2\n", "SESSION_TIMEOUT = 30\n")
	}, 4*time.Hour)
	a.stop()
	if !a.lastCleanStop {
		t.Fatal("probe setup did not stop cleanly")
	}
	models.CloseEngine()
	root := filepath.Dir(a.root)
	raw := filepath.Join(root, "raw")
	if err := os.Mkdir(raw, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value interface{}) {
		b, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(raw, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	digest := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(b)
		return hex.EncodeToString(hash[:])
	}
	write("preserve-first-push-armed.json", map[string]string{"fixture": a.root, "appExecutable": a.binary, "appSHA256": digest(a.binary), "configSHA256": digest(a.config)})
	hashes := map[string]string{}
	for _, name := range mozillaStaticFiles {
		hashes[name] = digest(filepath.Join(a.root, name))
	}
	write("fixture-static-before-preservation.json", map[string]interface{}{"fixture": a.root, "files": hashes})
	// Synthetic boundary evidence belongs only to this tiny test fixture. No
	// real dataset receipt is modified or treated as a successful verification.
	write("preserved-fixture-candidate.json", mozillaCandidate{State: "INTERRUPTED_RECOVERY_CANDIDATE", Fixture: a.root, ArmedReceipt: filepath.Join(raw, "preserve-first-push-armed.json"), KillAttempted: true, FixtureExists: true, PushReceipt: mozillaSample{Name: "initial-push-https", Kind: "native-hg"}, VerifyPhase: mozillaSample{Name: "https-verify", Kind: "phase-start"}})
	m := &mozillaRun{t: t, root: root, raw: raw}
	rebound := m.retainedApplication()
	if rebound.root != a.root || rebound.repos["public"].Id != a.repos["public"].Id || rebound.keyIDs["writer"] != a.keyIDs["writer"] {
		t.Fatal("rebind replaced fixture identity")
	}
	s, _ := m.request(rebound, "rebound-login", "/user/login", 1)
	if s.Error != "" {
		t.Fatal(s.Error)
	}
	if _, err := os.OpenFile(filepath.Join(a.root, "task16-adoption-lease.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); !os.IsExist(err) {
		t.Fatalf("replay lease missing: %v", err)
	}
}

func mozillaValidateSourceTimeout(s mozillaSample, stdout, stderr []byte, nativeExit int) error {
	// Accept only this observed two-hour, empty-output attempt. The old recorder's
	// ExitCode field was defective; the independent native exit witness is used.
	if s.Name != "source-verify" || s.Kind != "native-hg" || s.Error != context.DeadlineExceeded.Error() || s.Seconds < 7200 || s.Seconds > 7260 || s.Bytes != 0 || s.HashOnly || nativeExit != 1 || len(stdout) != 0 || len(stderr) != 0 {
		return errors.New("source failure is not the approved time-bound-only attempt")
	}
	return nil
}

func TestMozillaTimedOutSourceGate(t *testing.T) {
	valid := mozillaSample{Name: "source-verify", Kind: "native-hg", Error: context.DeadlineExceeded.Error(), Seconds: 7200.1837995}
	if err := mozillaValidateSourceTimeout(valid, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		edit        func(*mozillaSample)
		out, errout []byte
		exit        int
	}{
		{name: "integrity-error", edit: func(s *mozillaSample) { s.Error = "integrity check failed" }, exit: 1},
		{name: "early-error", edit: func(s *mozillaSample) { s.Seconds = 10 }, exit: 1},
		{name: "stdout-evidence", out: []byte("missing revlog"), exit: 1},
		{name: "stderr-evidence", errout: []byte("corrupt manifest"), exit: 1},
		{name: "wrong-native-exit", exit: 0},
		{name: "discarded-output", edit: func(s *mozillaSample) { s.HashOnly = true }, exit: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			if tc.edit != nil {
				tc.edit(&s)
			}
			if err := mozillaValidateSourceTimeout(s, tc.out, tc.errout, tc.exit); err == nil {
				t.Fatal("non-approved source failure was accepted")
			}
		})
	}
}

// A frozen source is verified once per process, then its complete identity is
// rechecked by every phase. Identical failed ingestions are not silently retried.
var mozillaVerifiedSources sync.Map
var mozillaFailedSeeds sync.Map

// Standalone named tests use fresh fixtures. Explicit retained mode instead
// adopts the first preserved seed once and runs ordered phases without copies.
// An external resource supervisor is required; see the benchmark document.
type mozillaRun struct {
	t                 *testing.T
	root, raw, source string
	r                 *hg.Runner
	next              atomic.Uint64
	mu                sync.Mutex
	deadline          time.Duration
	identity          mozillaIdentity
	jar               http.CookieJar
	largePath         string
	largeBytes        int64
	followPath        string
	browseNode        string
}

type mozillaIdentity struct {
	URL, AcquiredUTC, FrozenUTC, Tip, NodesSHA256                  string
	SourceDirectory, VerificationState                             string
	SourceClassification, UpstreamReference, IntegrityVerification string
	Changesets, StoreBytes                                         int64
	Heads, Branches, Tags, Bookmarks, Requirements                 string
}

type mozillaSample struct {
	Name, StartedUTC, Kind, Error, Stdout, Stderr, SHA256 string
	Repository, Executable                                string `json:",omitempty"`
	SourceDirectory                                       string `json:",omitempty"`
	Args                                                  string `json:",omitempty"`
	Seconds, FirstByteSeconds                             float64
	Bytes                                                 int64
	Status, Concurrency, ExitCode                         int
	HashOnly                                              bool
}

type mozillaSink struct {
	w     io.Writer
	h     hash.Hash
	n     int64
	first time.Time
}

func (w *mozillaSink) Write(p []byte) (int, error) {
	if w.first.IsZero() {
		w.first = time.Now()
	}
	n, e := w.w.Write(p)
	w.h.Write(p[:n])
	w.n += int64(n)
	return n, e
}

func newMozillaRun(t *testing.T) *mozillaRun {
	t.Helper()
	if os.Getenv("GOGS_TEST_MOZILLA") != "1" {
		t.Skip("full mozilla-central history is opt-in: GOGS_TEST_MOZILLA=1")
	}
	root := os.Getenv("GOGS_TEST_MOZILLA_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("GOGS_TEST_MOZILLA_ROOT must be an absolute dedicated external data directory")
	}
	checkout, _ := filepath.Abs("../..")
	if rel, e := filepath.Rel(checkout, root); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatal("Mozilla data must remain outside the source checkout")
	}
	if rel, e := filepath.Rel(root, os.TempDir()); e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatal("the platform temp directory (TEMP/TMP on Windows, TMPDIR on Unix) must be inside the dedicated data root")
	}
	for _, name := range []string{"GOGS_TEST_HG", "GOGS_TEST_BINARY", "GOGS_TEST_ASSETS"} {
		if os.Getenv(name) == "" {
			t.Fatalf("heavyweight suite requires %s", name)
		}
	}
	if os.Getenv("GOGS_TEST_PACKAGE_DIRECT") != "1" {
		t.Fatal("heavyweight suite requires actual extracted package: GOGS_TEST_PACKAGE_DIRECT=1")
	}
	owner, err := os.ReadFile(filepath.Join(root, "OWNER"))
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(owner)), "gogs-task16-") {
		t.Fatal("dedicated data directory requires an explicit gogs-task16- ownership sentinel")
	}
	if _, err = os.Stat(filepath.Join(root, "raw", "preflight.json")); err != nil {
		t.Fatal("record resource preflight in root/raw/preflight.json before heavyweight execution")
	}
	m := &mozillaRun{t: t, root: root, source: filepath.Join(root, "source"), deadline: 2 * time.Hour}
	if b, e := os.ReadFile(filepath.Join(root, "source-acquisition.json")); e == nil {
		var receipt mozillaSample
		if json.Unmarshal(b, &receipt) != nil || receipt.Error != "" || receipt.ExitCode != 0 || receipt.Name != "upstream-acquisition" || receipt.StartedUTC == "" || !strings.Contains(receipt.Args, mozillaURL) {
			t.Fatal("invalid successful source acquisition receipt")
		}
		if receipt.SourceDirectory != "" {
			if !regexp.MustCompile(`^source(?:-[a-z0-9]+)*$`).MatchString(receipt.SourceDirectory) {
				t.Fatal("source receipt directory must be a dedicated direct child of the data root")
			}
			m.source = filepath.Join(root, receipt.SourceDirectory)
		}
	} else if !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if value := os.Getenv("GOGS_TEST_MOZILLA_TIMEOUT"); value != "" {
		m.deadline, err = time.ParseDuration(value)
		if err != nil || m.deadline <= 0 || m.deadline > 4*time.Hour {
			t.Fatal("GOGS_TEST_MOZILLA_TIMEOUT must be finite, positive and at most 4h")
		}
	}
	m.raw, err = os.MkdirTemp(filepath.Join(root, "raw"), t.Name()+"-")
	if err != nil {
		t.Fatal(err)
	}
	m.r, err = hg.NewRunner(hg.Config{Executable: hgPath(t), ConfigPath: filepath.Join(m.raw, "native", "hgrc"), MaxProcesses: 8, SessionTimeout: m.deadline, IdleTimeout: m.deadline})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("raw evidence=%s; client operation ceiling=%s; server workers=8 session=30m idle=120s queue=5s", m.raw, m.deadline)
	return m
}

func (m *mozillaRun) record(s mozillaSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, e := os.OpenFile(filepath.Join(m.raw, "samples.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		m.t.Error(e)
		return
	}
	defer f.Close()
	if e = json.NewEncoder(f).Encode(s); e != nil {
		m.t.Error(e)
	}
}

// command retains and hashes native output by default. Hash-only consumers opt
// out of retention explicitly; command arguments never decide whether a later
// consumer can read stdout. The application keeps its production output limits.
// Credentials are redacted from receipts.
type mozillaCommandOptions struct {
	Concurrency int
	HashOnly    bool
}

func (m *mozillaRun) command(name, repo string, args ...string) mozillaSample {
	return m.commandWithConcurrency(name, repo, 1, args...)
}

func (m *mozillaRun) commandWithConcurrency(name, repo string, concurrency int, args ...string) mozillaSample {
	return m.commandWithOptions(name, repo, mozillaCommandOptions{Concurrency: concurrency}, args...)
}

func (m *mozillaRun) commandWithOptions(name, repo string, options mozillaCommandOptions, args ...string) mozillaSample {
	concurrency := options.Concurrency
	start := time.Now()
	id := m.next.Add(1)
	if strings.Contains(name, "verify") || strings.HasPrefix(name, "initial-push") || strings.HasPrefix(name, "full-clone") {
		// ui.status explicitly flushes timestamped messages even under HGPLAIN.
		// Restrict this to human-readable operations, never inventory/hash output.
		args = append([]string{"--config", "ui.timestamp-output=true", "--config", "ui.message-output=stderr"}, args...)
	}
	args = append([]string{"--config", "http.timeout=120", "--config", "http_proxy.host="}, args...)
	s := mozillaSample{Name: name, Kind: "native-hg", Concurrency: concurrency, StartedUTC: start.UTC().Format(time.RFC3339Nano), Args: redact(args), Repository: repo, Executable: hgPath(m.t), Stdout: filepath.Join(m.raw, fmt.Sprintf("%04d.stdout", id)), Stderr: filepath.Join(m.raw, fmt.Sprintf("%04d.stderr", id))}
	m.record(mozillaSample{Name: name, Kind: "phase-start", Concurrency: concurrency, StartedUTC: s.StartedUTC, Args: s.Args, Stdout: s.Stdout, Stderr: s.Stderr})
	out, e := os.Create(s.Stdout)
	if e != nil {
		s.Error = e.Error()
		s.ExitCode = -1
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			s.ExitCode = exit.ExitCode()
		}
		m.record(s)
		return s
	}
	defer out.Close()
	errout, e := os.Create(s.Stderr)
	if e != nil {
		s.Error = e.Error()
		s.ExitCode = -1
		m.record(s)
		return s
	}
	defer errout.Close()
	ctx, cancel := context.WithTimeout(context.Background(), m.deadline)
	defer cancel()
	child, e := m.r.Start(ctx, repo, args, nil)
	var output io.Writer = out
	if options.HashOnly {
		output = io.Discard
		s.HashOnly = true
	}
	w := &mozillaSink{w: output, h: sha256.New()}
	if e == nil {
		child.Stdin.Close()
		var stderrErr error
		done := make(chan struct{})
		go func() { _, stderrErr = io.Copy(errout, child.Stderr); close(done) }()
		_, copyErr := io.Copy(w, child.Stdout)
		<-done
		e = child.Stop(ctx)
		if e == nil {
			e = copyErr
		}
		if e == nil {
			e = stderrErr
		}
	}
	s.Seconds = time.Since(start).Seconds()
	s.Bytes = w.n
	s.SHA256 = hex.EncodeToString(w.h.Sum(nil))
	if !w.first.IsZero() {
		s.FirstByteSeconds = w.first.Sub(start).Seconds()
	}
	if e != nil {
		s.Error = e.Error()
		s.ExitCode = -1
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			s.ExitCode = exit.ExitCode()
		}
	}
	m.record(s)
	return s
}

func (m *mozillaRun) must(name, repo string, args ...string) mozillaSample {
	m.t.Helper()
	return m.mustWithOptions(name, repo, mozillaCommandOptions{Concurrency: 1}, args...)
}

func (m *mozillaRun) mustHashOnly(name, repo string, args ...string) mozillaSample {
	m.t.Helper()
	return m.mustWithOptions(name, repo, mozillaCommandOptions{Concurrency: 1, HashOnly: true}, args...)
}

func (m *mozillaRun) mustWithOptions(name, repo string, options mozillaCommandOptions, args ...string) mozillaSample {
	m.t.Helper()
	s := m.commandWithOptions(name, repo, options, args...)
	if s.Error != "" {
		m.t.Fatalf("%s: %s (stdout %s; stderr %s)", name, s.Error, s.Stdout, s.Stderr)
	}
	return s
}

func TestMozillaManifestSelectionRetainsInventory(t *testing.T) {
	testMozillaManifestSelection(t, false)
}

func TestMozillaManifestSelectionPreservesNativePaths(t *testing.T) {
	testMozillaManifestSelection(t, true)
}

func testMozillaManifestSelection(t *testing.T, nonASCII bool) {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		files, _ := filepath.Glob(filepath.Join(root, "*.stderr"))
		for _, path := range files {
			data, _ := os.ReadFile(path)
			if len(data) != 0 {
				t.Logf("native diagnostic %s: %q", filepath.Base(path), data)
			}
		}
	})
	r, err := hg.NewRunner(hg.Config{Executable: hgPath(t), ConfigPath: filepath.Join(root, "native", "hgrc"), SessionTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	m := &mozillaRun{t: t, root: root, raw: root, source: repo, r: r, deadline: 30 * time.Second}
	m.must("init", "", "init", repo)
	extra, media, copied := "extra.txt", "clip.webm", "copied.txt"
	if nonASCII {
		extra, media, copied = "café.txt", "vidéo.webm", "copié.txt"
	}
	for name, body := range map[string][]byte{"readme.txt": []byte("representative text"), "image.png": {0, 1, 2}, media: bytes.Repeat([]byte{3}, 64), extra: []byte("additional path")} {
		if err := os.WriteFile(filepath.Join(repo, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m.must("add", repo, "add")
	m.must("commit", repo, "commit", "-u", "test", "-m", "representative files")
	m.must("copy", repo, "copy", "readme.txt", copied)
	m.must("commit-copy", repo, "commit", "-u", "test", "-m", "copy")
	m.browseNode = m.text("node", repo, "log", "-r", "tip", "-T", "{node}")
	t.Run("hash-only-baseline-and-comparison", func(t *testing.T) {
		retained := m.must("retained-manifest-control", repo, "manifest", "-r", m.browseNode)
		baseline := m.commandWithOptions("baseline-manifest", repo, mozillaCommandOptions{Concurrency: 1, HashOnly: true}, "manifest", "-r", m.browseNode)
		comparison := m.mustHashOnly("comparison-manifest", repo, "manifest", "-r", m.browseNode)
		if retained.Bytes == 0 || retained.HashOnly {
			t.Fatal("retained control is empty or discarded")
		}
		for _, sample := range []mozillaSample{baseline, comparison} {
			discarded, err := os.ReadFile(sample.Stdout)
			if err != nil || len(discarded) != 0 || !sample.HashOnly || sample.Error != "" || sample.Bytes != retained.Bytes || sample.SHA256 != retained.SHA256 {
				t.Errorf("hash-only receipt differs from retained output: %+v read=%v retainedBytes=%d", sample, err, retained.Bytes)
			}
		}
	})
	// Zero HTTP samples exercises native command-to-selection and content checks
	// without starting a hosting fixture or accessing the full dataset.
	m.files(nil, repo, 0)
	data, err := os.ReadFile(filepath.Join(root, "file-selection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var selection struct {
		ManifestCount      int
		Selected           map[string]string
		FollowCopyEvidence bool
	}
	if err := json.Unmarshal(data, &selection); err != nil {
		t.Fatal(err)
	}
	if selection.ManifestCount != 5 {
		t.Fatalf("manifest inventory lost: count=%d want 5", selection.ManifestCount)
	}
	kinds := []string{"text", "binary", "large"}
	if nonASCII {
		kinds = append(kinds, "non-ascii")
	}
	for _, kind := range kinds {
		if selection.Selected[kind] == "" {
			t.Errorf("missing representative %s", kind)
		}
	}
	mediaBytes, err := hg.LocalBytes(media)
	if err != nil {
		t.Fatal(err)
	}
	copiedBytes, err := hg.LocalBytes(copied)
	if err != nil {
		t.Fatal(err)
	}
	if m.largePath != string(mediaBytes) || m.largeBytes != 64 || m.followPath != string(copiedBytes) || !selection.FollowCopyEvidence {
		t.Fatalf("selection did not reach content/history checks: large=%q bytes=%d follow=%q copy=%v", m.largePath, m.largeBytes, m.followPath, selection.FollowCopyEvidence)
	}
	follow := m.must("baseline-file-history", repo, "log", "--follow", "-r", m.browseNode, "-l", "30", "-T", "{node}\n", "--", m.nativePathPattern(m.followPath))
	if follow.Bytes != 82 {
		t.Fatalf("copy history lost: got %d bytes, want two node lines", follow.Bytes)
	}
	receipts, err := os.ReadFile(filepath.Join(root, "samples.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range bytes.Split(receipts, []byte("\n")) {
		var sample mozillaSample
		if len(line) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &sample); err != nil {
			t.Fatal(err)
		}
		if sample.Name == "code-manifest-paths" && sample.Kind == "native-hg" {
			found = true
			inventory, err := os.ReadFile(sample.Stdout)
			if err != nil || len(inventory) == 0 || sample.HashOnly || int64(len(inventory)) != sample.Bytes {
				t.Fatalf("selection manifest was not retained: bytes=%d hashOnly=%v read=%v", len(inventory), sample.HashOnly, err)
			}
		}
	}
	if !found {
		t.Fatal("missing selection manifest receipt")
	}
}

func TestMozillaCommandRecordsNativeExitCode(t *testing.T) {
	root := t.TempDir()
	r, err := hg.NewRunner(hg.Config{Executable: hgPath(t), ConfigPath: filepath.Join(root, "native", "hgrc"), SessionTimeout: time.Minute, IdleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	m := &mozillaRun{t: t, root: root, raw: root, r: r, deadline: time.Minute}
	left, right := filepath.Join(root, "left"), filepath.Join(root, "right")
	m.must("init-left", "", "init", left)
	m.must("init-right", "", "init", right)
	verified := m.must("verify-status", left, "verify")
	status, err := os.ReadFile(verified.Stderr)
	if err != nil || !bytes.Contains(status, []byte("checking changesets")) {
		t.Fatalf("long operation status must use the readable stderr stream: %q (%v)", status, err)
	}
	s := m.command("no-changes", left, "incoming", "--quiet", right)
	if s.ExitCode != 1 || s.Error == "" {
		t.Fatalf("native no-change exit must remain observable: exit=%d error=%q", s.ExitCode, s.Error)
	}

	// A failed launch has no native exit status and must not look successful.
	s = m.commandWithConcurrency("missing-repository", filepath.Join(root, "missing"), 5, "log")
	if s.ExitCode == 0 || s.Error == "" {
		t.Fatalf("failed command recorded success: exit=%d error=%q", s.ExitCode, s.Error)
	}
	if s.Concurrency != 5 {
		t.Fatalf("requested workload cohort was lost: concurrency=%d", s.Concurrency)
	}
}
func (m *mozillaRun) text(name, repo string, args ...string) string {
	s := m.must(name, repo, args...)
	if s.Bytes > 8<<20 {
		m.t.Fatalf("%s is too large for a metadata read", name)
	}
	b, e := os.ReadFile(s.Stdout)
	if e != nil {
		m.t.Fatal(e)
	}
	return strings.TrimSpace(string(b))
}

func mozillaTrackedTags(value string) string {
	lines := strings.Split(value, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "tip\t") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
func mozillaTreeBytes(ctx context.Context, root string) (int64, error) {
	var n int64
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			i, e := d.Info()
			if e != nil {
				return e
			}
			n += i.Size()
		}
		return nil
	})
	return n, e
}

func TestMozillaSizeScanHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mozillaTreeBytes(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled size scan returned %v", err)
	}
}

// Hash every stable store byte without buffering a revlog or hashing volatile
// caches/locks. Read-only ACL attempts must preserve this complete store digest.
func (m *mozillaRun) storeDigest(name, store string) string {
	start := time.Now()
	sum := sha256.New()
	var n int64
	err := filepath.WalkDir(filepath.Join(store, ".hg", "store"), func(path string, d fs.DirEntry, e error) error {
		if time.Since(start) >= m.deadline {
			return context.DeadlineExceeded
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "lock" {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unexpected store file type: %s", path)
		}
		rel, e := filepath.Rel(store, path)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		fmt.Fprintf(sum, "%s\x00%d\x00", filepath.ToSlash(rel), info.Size())
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		size, e := io.Copy(sum, f)
		f.Close()
		n += size
		return e
	})
	s := mozillaSample{Name: name, Kind: "store-hash", StartedUTC: start.UTC().Format(time.RFC3339Nano), Seconds: time.Since(start).Seconds(), Bytes: n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	if err != nil {
		s.Error = err.Error()
	}
	m.record(s)
	if err != nil {
		m.t.Fatal(err)
	}
	return s.SHA256
}

// subsetSource selects a prepared converted subset (latest changesets only, new
// node IDs) for UI smoke runs. It is never evidence of full-scale acceptance.
func subsetSource() bool { return os.Getenv("GOGS_TEST_MOZILLA_SUBSET") == "1" }

func (m *mozillaRun) freeze() {
	var resume *mozillaIdentity
	conditional := false
	if _, err := os.Stat(filepath.Join(m.source, ".hg")); os.IsNotExist(err) {
		s := m.must("upstream-acquisition", "", "--config", "ui.clonebundles=true", "clone", "--stream", "--noupdate", mozillaURL, m.source)
		s.SourceDirectory = filepath.Base(m.source)
		b, _ := json.Marshal(s)
		if err = os.WriteFile(filepath.Join(m.root, "source-acquisition.json"), b, 0600); err != nil {
			m.t.Fatal(err)
		}
	} else if err != nil {
		m.t.Fatal(err)
	}
	if _, e := os.Stat(filepath.Join(m.root, "frozen-source.json")); os.IsNotExist(e) {
		var acquired bool
		if b, e := os.ReadFile(filepath.Join(m.root, "raw", "acquire.result.json")); e == nil {
			var result struct {
				ExitCode        *int
				Failure         *string
				EndUTC, Command string
			}
			if json.Unmarshal(b, &result) == nil && result.ExitCode != nil && *result.ExitCode == 0 && result.Failure == nil && result.EndUTC != "" && strings.Contains(result.Command, "clone --noupdate "+mozillaURL) {
				acquired = true
			}
		}
		if b, e := os.ReadFile(filepath.Join(m.root, "source-acquisition.json")); e == nil {
			var result mozillaSample
			if json.Unmarshal(b, &result) == nil && result.Error == "" && result.Name == "upstream-acquisition" && result.StartedUTC != "" && strings.Contains(result.Args, mozillaURL) {
				acquired = true
			}
		}
		if !acquired && !subsetSource() {
			m.t.Fatal("existing source lacks a successful full clone receipt; partial stores cannot be frozen as a complete dataset")
		}
	}
	config, e := os.ReadFile(filepath.Join(m.source, ".hg", "hgrc"))
	if !subsetSource() && (e != nil || !strings.Contains(string(config), "default = "+mozillaURL)) {
		m.t.Fatal("source provenance mismatch: native clone must retain the exact approved default URL")
	}
	// Conditional reuse is an explicit measurement-order exception, never a pass.
	if prior, ok := mozillaVerifiedSources.Load(m.source); ok {
		if prior.(string) == mozillaSourceUnverified && os.Getenv("GOGS_TEST_MOZILLA_ALLOW_TIMED_OUT_SOURCE") == "1" {
			conditional = true
		} else if prior.(string) != "" {
			m.t.Fatalf("source verification previously failed in this process: %s", prior)
		} else {
			m.record(mozillaSample{Name: "source-verify-reuse", Kind: "verified-source-reuse", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano)})
		}
	} else {
		if os.Getenv("GOGS_TEST_MOZILLA_ALLOW_TIMED_OUT_SOURCE") == "1" {
			resume = m.resumeTimedOutSource()
			conditional = true
		} else {
			s := m.command("source-verify", m.source, "verify")
			mozillaVerifiedSources.Store(m.source, s.Error)
			if s.Error != "" {
				m.t.Fatalf("source verification failed: %s; %s", s.Error, s.Stderr)
			}
		}
	}
	nodes := m.must("source-visible-nodes", m.source, "log", "-r", "sort(all(), rev)", "-T", "{node}\n")
	id := mozillaIdentity{URL: mozillaURL, SourceDirectory: filepath.Base(m.source), FrozenUTC: time.Now().UTC().Format(time.RFC3339Nano), Tip: m.text("source-tip", m.source, "log", "-r", "tip", "-T", "{node}"), NodesSHA256: nodes.SHA256, Changesets: nodes.Bytes / 41}
	classifyMozillaIdentity(&id, subsetSource(), conditional)
	if b, e := os.ReadFile(filepath.Join(m.root, "raw", "acquire.result.json")); e == nil && filepath.Base(m.source) == "source" {
		var receipt struct{ EndUTC string }
		if json.Unmarshal(b, &receipt) == nil {
			id.AcquiredUTC = receipt.EndUTC
		}
	}
	if id.AcquiredUTC == "" {
		if b, e := os.ReadFile(filepath.Join(m.root, "source-acquisition.json")); e == nil {
			var receipt mozillaSample
			if json.Unmarshal(b, &receipt) == nil {
				start, e := time.Parse(time.RFC3339Nano, receipt.StartedUTC)
				if e == nil {
					id.AcquiredUTC = start.Add(time.Duration(receipt.Seconds * float64(time.Second))).UTC().Format(time.RFC3339Nano)
				}
			}
		}
	}
	if nodes.Bytes%41 != 0 || id.Changesets == 0 {
		m.t.Fatal("invalid complete node inventory")
	}
	id.Heads = m.text("source-heads", m.source, "log", "-r", "heads(all())", "-T", "{node}\n")
	id.Branches = m.text("source-branches", m.source, "branches", "--closed", "-T", "{branch}\t{node}\t{closed}\n")
	id.Tags = mozillaTrackedTags(m.text("source-tags", m.source, "tags", "-T", "{tag}\t{node}\n"))
	id.Bookmarks = m.text("source-bookmarks", m.source, "bookmarks", "-T", "{bookmark}\t{node}\n")
	m.must("source-nonpublic-phases", m.source, "log", "-r", "not public()", "-T", "{node}\t{phase}\n")
	var err error
	sizeStarted := time.Now()
	m.record(mozillaSample{Name: "source-apparent-size", Kind: "phase-start", StartedUTC: sizeStarted.UTC().Format(time.RFC3339Nano)})
	sizeContext, cancelSize := context.WithTimeout(context.Background(), m.deadline)
	id.StoreBytes, err = mozillaTreeBytes(sizeContext, filepath.Join(m.source, ".hg"))
	cancelSize()
	sizeSample := mozillaSample{Name: "source-apparent-size", Kind: "store-size", StartedUTC: sizeStarted.UTC().Format(time.RFC3339Nano), Seconds: time.Since(sizeStarted).Seconds(), Bytes: id.StoreBytes}
	if err != nil {
		sizeSample.Error = err.Error()
	}
	m.record(sizeSample)
	if err != nil {
		m.t.Fatal(err)
	}
	for _, p := range []string{".hg/requires", ".hg/store/requires"} {
		b, e := os.ReadFile(filepath.Join(m.source, p))
		if e != nil && !os.IsNotExist(e) {
			m.t.Fatal(e)
		}
		id.Requirements += p + ":\n" + string(b)
	}
	path := filepath.Join(m.root, "frozen-source.json")
	if resume != nil {
		if resume.URL != id.URL || resume.SourceDirectory != id.SourceDirectory || resume.Tip != id.Tip || resume.NodesSHA256 != id.NodesSHA256 || resume.Changesets != id.Changesets || resume.Heads != id.Heads || resume.Branches != id.Branches || resume.Tags != id.Tags || resume.Bookmarks != id.Bookmarks || resume.Requirements != id.Requirements {
			m.t.Fatal("explicit conditional-source checkpoint identity mismatch")
		}
		mozillaVerifiedSources.Store(m.source, mozillaSourceUnverified)
	}
	if b, e := os.ReadFile(path); e == nil {
		var old mozillaIdentity
		if json.Unmarshal(b, &old) != nil || old.URL != id.URL || old.SourceDirectory != id.SourceDirectory || old.Tip != id.Tip || old.NodesSHA256 != id.NodesSHA256 || old.Changesets != id.Changesets || old.Heads != id.Heads || old.Branches != id.Branches || old.Tags != id.Tags || old.Bookmarks != id.Bookmarks || old.Requirements != id.Requirements {
			m.t.Fatal("frozen source identity mismatch; refusing moving or substituted dataset")
		}
		id.AcquiredUTC = old.AcquiredUTC
		id.FrozenUTC = old.FrozenUTC
	} else if !os.IsNotExist(e) {
		m.t.Fatal(e)
	}
	b, _ := json.MarshalIndent(id, "", "  ")
	if err = os.WriteFile(path, b, 0600); err != nil {
		m.t.Fatal(err)
	}
	m.identity = id
	m.browseNode, err = mozillaCodeNode(id.Branches)
	if err != nil {
		m.t.Fatal(err)
	}
	m.record(mozillaSample{Name: "representative-code-node", Kind: "dataset-selection", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Args: "open default branch " + m.browseNode + "; global dataset tip remains " + id.Tip})
	m.t.Logf("frozen tip=%s changesets=%d metadata bytes=%d source integrity=%s", id.Tip, id.Changesets, id.StoreBytes, id.VerificationState)
	if conditional {
		message := "source integrity FAILED/UNVERIFIED after the recorded two-hour deadline; all hosted observations are conditional on undetected source damage"
		m.record(mozillaSample{Name: "source-integrity-gate", Kind: "correctness-gate", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Error: message})
		m.t.Error(message) // Continue measurements, but every named gate must fail.
	}
}

// The documented exception preserves a failed correctness gate. It admits only
// the exact observed timeout plus independent exit and immutable identity proof.
// Ordinary runs still require a successful native verification before hosting.
func (m *mozillaRun) resumeTimedOutSource() *mozillaIdentity {
	var checkpoint struct {
		SourcePath, HgSHA256, HgVersion, AcquisitionSHA256, ReceiptSHA256 string
		VerificationState, NativeExitSHA256, ProcessWitnessSHA256         string
		Identity                                                          mozillaIdentity
	}
	b, err := os.ReadFile(filepath.Join(m.root, "source-verification-checkpoint.json"))
	if err != nil || json.Unmarshal(b, &checkpoint) != nil || checkpoint.VerificationState != mozillaSourceUnverified || checkpoint.Identity.VerificationState != mozillaSourceUnverified {
		m.t.Fatal("explicit timed-out-source continuation requires its complete unverified checkpoint")
	}
	var stopped struct {
		VerifySucceeded, SourceJournalPresent bool
		SourceDirectory                       string
		VerifyReceipt                         mozillaSample
	}
	receipt, err := os.ReadFile(filepath.Join(m.root, "raw", "post-verify-harness-stop.json"))
	if err != nil || json.Unmarshal(receipt, &stopped) != nil || stopped.VerifySucceeded || stopped.SourceJournalPresent || stopped.SourceDirectory != filepath.Base(m.source) || filepath.Clean(stopped.VerifyReceipt.Repository) != filepath.Clean(m.source) {
		m.t.Fatal("checkpoint does not describe the failed verification of this source")
	}
	var nativeExit struct {
		ExitCode                                  int
		Source, Executable, StartedUTC, ExitedUTC string
	}
	b, err = os.ReadFile(filepath.Join(m.root, "raw", "source-verify-process-exit.json"))
	if err != nil || json.Unmarshal(b, &nativeExit) != nil || filepath.Clean(nativeExit.Source) != filepath.Clean(m.source) || filepath.Clean(nativeExit.Executable) != filepath.Clean(hgPath(m.t)) {
		m.t.Fatal("independent native exit/source witness mismatch")
	}
	out, outErr := os.ReadFile(stopped.VerifyReceipt.Stdout)
	errout, errErr := os.ReadFile(stopped.VerifyReceipt.Stderr)
	if outErr != nil || errErr != nil || mozillaValidateSourceTimeout(stopped.VerifyReceipt, out, errout, nativeExit.ExitCode) != nil {
		m.t.Fatal("source output/failure is not the approved time-bound-only attempt")
	}
	started, startErr := time.Parse(time.RFC3339Nano, nativeExit.StartedUTC)
	exited, exitErr := time.Parse(time.RFC3339Nano, nativeExit.ExitedUTC)
	commandStart, commandErr := time.Parse(time.RFC3339Nano, stopped.VerifyReceipt.StartedUTC)
	if startErr != nil || exitErr != nil || commandErr != nil || started.Sub(commandStart) < 0 || started.Sub(commandStart) > 2*time.Second || exited.Sub(started) < 2*time.Hour-time.Second || exited.Sub(started) > 2*time.Hour+time.Minute {
		m.t.Fatal("native timing witness does not corroborate the two-hour source deadline")
	}
	for _, item := range []struct{ path, want string }{
		{hgPath(m.t), checkpoint.HgSHA256},
		{filepath.Join(m.root, "source-acquisition.json"), checkpoint.AcquisitionSHA256},
		{filepath.Join(m.root, "raw", "post-verify-harness-stop.json"), checkpoint.ReceiptSHA256},
		{filepath.Join(m.root, "raw", "source-verify-process-exit.json"), checkpoint.NativeExitSHA256},
		{filepath.Join(m.root, "raw", "source-verify-process-witness.json"), checkpoint.ProcessWitnessSHA256},
	} {
		b, e := os.ReadFile(item.path)
		digest := sha256.Sum256(b)
		if e != nil || !strings.EqualFold(hex.EncodeToString(digest[:]), item.want) {
			m.t.Fatal("restart checkpoint content mismatch: " + item.path)
		}
	}
	if filepath.Clean(checkpoint.SourcePath) != filepath.Clean(m.source) || m.text("resume-native-version", "", "version", "--quiet") != checkpoint.HgVersion {
		m.t.Fatal("restart checkpoint source/native version mismatch")
	}
	for _, name := range []string{".hg/store/journal", ".hg/store/lock", ".hg/wlock"} {
		if _, err := os.Lstat(filepath.Join(m.source, name)); !os.IsNotExist(err) {
			m.t.Fatal("restart source has an active or ambiguous transaction/lock")
		}
	}
	return &checkpoint.Identity
}

func (m *mozillaRun) application() *application {
	return m.applicationAt("")
}

func (m *mozillaRun) applicationAt(root string) *application {
	a := newConfiguredApplicationAt(m.t, versions{name: "Mozilla native", server: hgPath(m.t), client: hgPath(m.t)}, func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "MAX_PROCESSES = 2", "MAX_PROCESSES = 8"), "SESSION_TIMEOUT = 2\n", "SESSION_TIMEOUT = 30\n")
	}, 4*time.Hour, root)
	config, e := os.ReadFile(a.config)
	if e != nil {
		m.t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(m.raw, "application.ini"), config, 0600); e != nil {
		m.t.Fatal(e)
	}
	m.t.Cleanup(func() {
		a.stop()
		b, e := os.ReadFile(filepath.Join(a.root, "server.log"))
		if e == nil {
			os.WriteFile(filepath.Join(m.raw, "server.log"), b, 0600)
		}
	})
	return a
}

// This is a one-shot adoption of the explicitly preserved first HTTPS fixture,
// not a general checkpoint/retry mechanism. The lease is deliberately retained
// after any failure so a later invocation cannot replay writes automatically.
func (m *mozillaRun) retainedApplication() *application {
	readJSON := func(path string, dst interface{}) {
		b, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(b, dst) != nil {
			m.t.Fatalf("invalid retained evidence %s: %v", path, err)
		}
	}
	var candidate mozillaCandidate
	readJSON(filepath.Join(m.root, "raw", "preserved-fixture-candidate.json"), &candidate)
	if err := mozillaValidateCandidate(m.root, candidate); err != nil {
		m.t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(candidate.Fixture)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(candidate.Fixture)) {
		m.t.Fatal("retained fixture is absent or redirects through a filesystem link")
	}
	var armed struct {
		Fixture, AppExecutable, AppSHA256, ConfigSHA256 string
	}
	armedPath := filepath.Join(m.root, "raw", "preserve-first-push-armed.json")
	if filepath.Clean(candidate.ArmedReceipt) != filepath.Clean(armedPath) {
		m.t.Fatal("candidate does not reference the pinned preservation receipt")
	}
	readJSON(armedPath, &armed)
	var static struct {
		Fixture string
		Files   map[string]string
	}
	readJSON(filepath.Join(m.root, "raw", "fixture-static-before-preservation.json"), &static)
	if filepath.Clean(static.Fixture) != filepath.Clean(candidate.Fixture) {
		m.t.Fatal("static witness belongs to another fixture")
	}
	if err := mozillaCheckStaticFiles(candidate.Fixture, static.Files); err != nil {
		m.t.Fatal(err)
	}
	if filepath.Clean(armed.Fixture) != filepath.Clean(candidate.Fixture) || !strings.EqualFold(filepath.Clean(armed.AppExecutable), filepath.Clean(gogsExe)) {
		m.t.Fatal("retained fixture/package identity mismatch")
	}
	for _, item := range []struct{ path, want string }{{gogsExe, armed.AppSHA256}, {filepath.Join(candidate.Fixture, "app.ini"), armed.ConfigSHA256}} {
		b, e := os.ReadFile(item.path)
		digest := sha256.Sum256(b)
		if e != nil || !strings.EqualFold(hex.EncodeToString(digest[:]), item.want) {
			m.t.Fatal("retained content identity mismatch: " + item.path)
		}
	}
	// Bounded process census precedes opening SQLite or starting the application.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Get-CimInstance Win32_Process | Select-Object ProcessId,CommandLine | ConvertTo-Json -Compress")
	} else {
		command = exec.CommandContext(ctx, "ps", "-eo", "pid=,args=")
	}
	processes, err := command.Output()
	if err != nil || bytes.Contains(bytes.ToLower(processes), []byte(strings.ToLower(filepath.ToSlash(candidate.Fixture)))) || bytes.Contains(bytes.ToLower(processes), []byte(strings.ToLower(strings.ReplaceAll(filepath.ToSlash(candidate.Fixture), "/", `\\`)))) {
		m.t.Fatalf("retained fixture process ownership is not cold: %v", err)
	}
	for _, id := range []string{"1", "2"} {
		for _, name := range []string{".hg/store/journal", ".hg/store/lock", ".hg/wlock"} {
			if _, e := os.Lstat(filepath.Join(candidate.Fixture, "repos", id, name)); !os.IsNotExist(e) {
				m.t.Fatal("retained repository has an ambiguous transaction/lock: " + id + "/" + name)
			}
		}
	}
	lease, err := os.OpenFile(filepath.Join(candidate.Fixture, "task16-adoption-lease.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		m.t.Fatal("retained fixture already adopted or recovery requires explicit review: ", err)
	}
	json.NewEncoder(lease).Encode(map[string]interface{}{"pid": os.Getpid(), "utc": time.Now().UTC(), "raw": m.raw, "sourceNodesSHA256": m.identity.NodesSHA256, "browseNode": m.browseNode})
	lease.Close()
	a := &application{t: m.t, root: candidate.Fixture, binary: gogsExe, direct: true, users: map[string]*models.User{}, keys: map[string]string{}, keyIDs: map[string]int64{}, repos: map[string]*models.Repository{}}
	a.config, a.repoRoot = filepath.Join(a.root, "app.ini"), filepath.Join(a.root, "repos")
	a.known, a.cacerts = filepath.Join(a.root, "known_hosts"), filepath.Join(a.root, "frontend.pem")
	configBytes, err := os.ReadFile(a.config)
	if err != nil {
		m.t.Fatal(err)
	}
	cfg, err := ini.Load(configBytes)
	if err != nil {
		m.t.Fatal(err)
	}
	a.address = net.JoinHostPort(cfg.Section("server").Key("HTTP_ADDR").String(), cfg.Section("server").Key("HTTP_PORT").String())
	a.sshAddress = cfg.Section("server").Key("SSH_ADDRESS").String()
	if cfg.Section("mercurial").Key("MAX_PROCESSES").String() != "8" || cfg.Section("mercurial").Key("SESSION_TIMEOUT").String() != "30" || filepath.Clean(cfg.Section("repository").Key("ROOT").String()) != filepath.Clean(a.repoRoot) || filepath.Clean(cfg.Section("database").Key("PATH").String()) != filepath.Join(a.root, "application.db") {
		m.t.Fatal("retained fixture storage or shipping limits differ")
	}
	for _, address := range []string{a.address, a.sshAddress} {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			m.t.Fatal("retained address is occupied: ", e)
		}
		listener.Close()
	}
	// The interrupted process was not a clean DB close. Let the same SQLite
	// implementation recover its WAL in place, then require an integrity check.
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(a.root, "application.db"))+"?mode=rw&_pragma=busy_timeout(2000)")
	if err != nil {
		m.t.Fatal(err)
	}
	dbctx, dbcancel := context.WithTimeout(context.Background(), 30*time.Second)
	var integrity string
	err = db.QueryRowContext(dbctx, "PRAGMA integrity_check").Scan(&integrity)
	dbcancel()
	db.Close()
	if err != nil || integrity != "ok" {
		m.t.Fatalf("retained SQLite integrity failed: %q %v", integrity, err)
	}
	oldDB, oldLog, oldRoot := models.DbCfg, setting.LogRootPath, setting.RepoRootPath
	models.DbCfg.Type, models.DbCfg.Path = "sqlite3", filepath.ToSlash(filepath.Join(a.root, "application.db"))
	setting.LogRootPath, setting.RepoRootPath = filepath.Join(a.root, "model-log"), a.repoRoot
	m.t.Cleanup(func() {
		a.stop()
		models.CloseEngine()
		models.DbCfg, setting.LogRootPath, setting.RepoRootPath = oldDB, oldLog, oldRoot
		state := map[string]interface{}{"utc": time.Now().UTC(), "fixture": a.root, "raw": m.raw, "cleanApplicationStop": a.lastCleanStop, "databaseClosed": true, "testFailed": m.t.Failed(), "sourceIntegrity": m.identity.VerificationState, "note": "retained in place; no automatic replay authorized"}
		b, _ := json.MarshalIndent(state, "", "  ")
		os.WriteFile(filepath.Join(a.root, "task16-continuation-closed.json"), b, 0600)
	})
	if err = models.NewEngine(); err != nil {
		m.t.Fatal(err)
	}
	for i, role := range []string{"siteadmin", "owner", "writer", "reader", "stranger"} {
		u, e := models.GetUserByName(role)
		if e != nil || u.Id != int64(i+1) || !u.IsActive {
			m.t.Fatalf("retained account %s mismatch: %v", role, e)
		}
		a.users[role], a.keys[role] = u, filepath.Join(a.root, role+"-key")
		key, e := os.ReadFile(a.keys[role])
		if e != nil {
			m.t.Fatal(e)
		}
		signer, e := ssh.ParsePrivateKey(key)
		if e != nil {
			m.t.Fatal(e)
		}
		keys, e := models.ListPublicKeys(u.Id)
		if e != nil || len(keys) != 1 || keys[0].Id != int64(i+1) || strings.TrimSpace(keys[0].Content) != strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) {
			m.t.Fatalf("retained key membership %s mismatch: %v", role, e)
		}
		a.keyIDs[role] = keys[0].Id
	}
	for i, name := range []string{"public", "private"} {
		repo, e := models.GetRepositoryByName(a.users["owner"].Id, name)
		if e != nil || repo.Id != int64(i+1) || repo.IsPrivate != (name == "private") {
			m.t.Fatalf("retained repository %s mismatch: %v", name, e)
		}
		a.repos[name] = repo
		for _, role := range []string{"owner", "writer", "reader", "stranger"} {
			access, e := models.RepositoryAccessFor(a.users[role].Id, repo.Id)
			if e != nil || access.Read != (name == "public" || role != "stranger") || access.Write != (role == "owner" || role == "writer") || access.Admin != (role == "owner") {
				m.t.Fatalf("retained %s/%s ACL mismatch: %+v %v", name, role, access, e)
			}
		}
	}
	host, err := os.ReadFile(filepath.Join(a.root, "hostkey"))
	if err != nil {
		m.t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(host)
	if err != nil {
		m.t.Fatal(err)
	}
	known, err := os.ReadFile(a.known)
	_, port, _ := net.SplitHostPort(a.sshAddress)
	if err != nil || strings.TrimSpace(string(known)) != "[127.0.0.1]:"+port+" "+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) {
		m.t.Fatal("retained SSH host identity mismatch")
	}
	helper, _ := os.Executable()
	a.server, err = hg.NewRunner(hg.Config{Executable: helper, ConfigPath: filepath.Join(a.root, "supervisor", "hgrc"), SessionTimeout: 4 * time.Hour, IdleTimeout: 4 * time.Hour})
	if err != nil {
		m.t.Fatal(err)
	}
	a.client = newHgCmd(m.t, hgPath(m.t), filepath.Join(a.root, "client"), 4)
	a.inspect = newHgCmd(m.t, hgPath(m.t), filepath.Join(a.root, "inspect"), 4)
	a.start(m.t)
	m.record(mozillaSample{Name: "retained-fixture-adopted", Kind: "recovery-check", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Repository: a.root, Args: "same package/config/accounts/keys/ACL/host key; SQLite integrity_check=ok; interrupted fixture retained in place"})
	return a
}

func (m *mozillaRun) retainedOutcome(name string) {
	b, err := os.ReadFile(filepath.Join(m.root, "raw", "retained-phase-results.json"))
	var results map[string]bool
	if err != nil || json.Unmarshal(b, &results) != nil || !results[name] {
		m.t.Errorf("retained %s phase failed or remains unverified; no duplicate seed is attempted", name)
	}
}

// The fresh entry has its own one-shot lease and evidence. It never adopts a
// historical interruption or consumes retained-phase-results.json. A failed or
// interrupted attempt requires explicit review; re-running cannot replay writes.
func skipStandaloneMozillaOrdered(t *testing.T) {
	if os.Getenv("GOGS_TEST_MOZILLA_ORDERED") == "1" {
		t.Skip("fresh ordered mode runs all gates in TestMozillaCentralOrdered; no duplicate fixtures")
	}
}

func TestMozillaCentralOrdered(t *testing.T) {
	if os.Getenv("GOGS_TEST_MOZILLA_ORDERED") != "1" {
		t.Skip("explicit fresh ordered run: GOGS_TEST_MOZILLA_ORDERED=1")
	}
	if os.Getenv("GOGS_TEST_MOZILLA") != "1" || os.Getenv("GOGS_TEST_MOZILLA_RETAINED") != "" || os.Getenv("GOGS_TEST_MOZILLA_ALLOW_TIMED_OUT_SOURCE") != "" {
		t.Fatal("ordered mode requires GOGS_TEST_MOZILLA=1 and forbids retained/conditional-source modes")
	}
	m := newMozillaRun(t)
	m.freshOrdered(m.freeze)
}

func (m *mozillaRun) freshOrdered(verifySource func()) {
	lease, err := os.OpenFile(filepath.Join(m.root, "ordered-run-lease.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		m.t.Fatal("ordered run already attempted or root unavailable; explicit review required, no replay: ", err)
	}
	err = json.NewEncoder(lease).Encode(map[string]interface{}{"utc": time.Now().UTC(), "raw": m.raw, "note": "one fresh attempt; no automatic replay or adoption"})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		m.t.Fatalf("write ordered lease: %v %v", err, closeErr)
	}
	results := map[string]bool{}
	var a *application
	// Registered before constructing the fixture: its own cleanup stops the app
	// and closes the global model engine first (testing cleanup runs in LIFO order).
	m.t.Cleanup(func() {
		if a == nil || !a.lastCleanStop || !a.databaseClosed {
			m.t.Error("ordered fixture is not proven cold; lease retained, no phase results published")
			return
		}
		if left := waitNoProcesses(a.config, a.repoRoot); len(left) != 0 {
			m.t.Errorf("ordered fixture still owns processes: %v; no cold checkpoint published", left)
			return
		}
		state := map[string]interface{}{"utc": time.Now().UTC(), "fixture": a.root, "raw": m.raw, "cleanApplicationStop": a.lastCleanStop, "databaseClosed": a.databaseClosed, "testFailed": m.t.Failed(), "sourceIntegrity": m.identity.VerificationState, "sourceIdentity": m.identity, "results": results, "note": "retained in place; explicit review required before any further writes"}
		b, e := json.MarshalIndent(state, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(m.root, "ordered-cold-checkpoint.json"), b, 0600)
		}
		if e != nil {
			m.t.Error(e)
		}
	})
	phase := func(name string, fn func()) bool {
		ok := m.t.Run(name, func(t *testing.T) {
			previous := m.t
			m.t = t
			defer func() { m.t = previous }()
			fn()
		})
		results[name] = ok
		return ok
	}
	if !phase("source", func() {
		verifySource()
		if m.identity.VerificationState != "VERIFIED" && m.identity.VerificationState != "TRANSFORMED_SUBSET_LOCAL_INTEGRITY_VERIFIED" {
			m.t.Fatal("fresh ordered source gate requires successful native verification")
		}
	}) {
		return
	}
	a = m.applicationAt(filepath.Join(m.root, "ordered-fixture"))
	hashes := map[string]string{}
	for _, name := range mozillaStaticFiles {
		b, e := os.ReadFile(filepath.Join(a.root, name))
		if e != nil {
			m.t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	userIDs, repoIDs := map[string]int64{}, map[string]int64{}
	for name, user := range a.users {
		userIDs[name] = user.Id
	}
	for name, repo := range a.repos {
		repoIDs[name] = repo.Id
	}
	b, err := json.MarshalIndent(map[string]interface{}{"fixture": a.root, "files": hashes, "userIDs": userIDs, "keyIDs": a.keyIDs, "repositoryIDs": repoIDs}, "", "  ")
	if err != nil {
		m.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(m.raw, "ordered-fixture-identity.json"), b, 0600); err != nil {
		m.t.Fatal(err)
	}
	remotes, stores := make([]string, 2), make([]string, 2)
	options := make([][]string, 2)
	transports, repos := []string{transportHTTP, transportSSH}, []string{"public", "private"}
	for i, transport := range transports {
		if !phase(transport+"-ingestion", func() {
			var ok bool
			remotes[i], options[i], stores[i], ok = m.seed(a, transport, repos[i])
			if !ok {
				return
			}
			m.compareIdentity(stores[i], remotes[i], options[i], transport)
			m.compareRequirements(stores[i], transport)
		}) {
			return
		}
	}
	// These local reads do not need the service's finite lifetime running down.
	a.stop()
	if !a.lastCleanStop {
		m.t.Fatal("ordered application did not stop before native verification")
	}
	for i, transport := range transports {
		if !phase(transport+"-verification", func() { m.must(transport+"-verify", stores[i], "verify") }) {
			return
		}
	}
	a.start(m.t)
	// All pristine content, UI, native baselines and read capacity precede the
	// disposable workflow roots that change the default branch and bookmarks.
	if !phase("pristine-reads", func() { m.performance(a, stores[0]) }) {
		return
	}
	for i, transport := range transports {
		if !phase(transport+"-workflows", func() { m.workflows(a, transport, repos[i], remotes[i], options[i], stores[i]) }) {
			return
		}
	}
	if !phase("issues", func() { m.issues(a) }) {
		return
	}
	if err := mozillaCheckStaticFiles(a.root, hashes); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mozillaRun) compareRequirements(store, label string) {
	var requirements string
	for _, path := range []string{".hg/requires", ".hg/store/requires"} {
		b, err := os.ReadFile(filepath.Join(store, path))
		if err != nil && !os.IsNotExist(err) {
			m.t.Fatal(err)
		}
		requirements += path + ":\n" + string(b)
	}
	s := mozillaSample{Name: label + "-requirements", Kind: "requirements-check", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Repository: store, Args: requirements}
	if requirements != m.identity.Requirements {
		s.Error = "hosted requirements differ from the frozen source"
		m.t.Error(label + ": " + s.Error)
	}
	m.record(s)
}

func (m *mozillaRun) retainedPhases() {
	a := m.retainedApplication()
	results := map[string]bool{}
	phase := func(name string, fn func()) bool {
		ok := m.t.Run(name, func(t *testing.T) { previous := m.t; m.t = t; defer func() { m.t = previous }(); fn() })
		results[name] = ok
		b, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(filepath.Join(m.root, "raw", "retained-phase-results.json"), b, 0600); err != nil {
			m.t.Fatal(err)
		}
		return ok
	}
	remote, opts := a.remote(m.t, transportHTTP, "writer", "public")
	store := filepath.Join(a.repoRoot, "1")
	if !phase("retained-identity", func() { m.compareIdentity(store, remote, opts, "retained-https") }) {
		return
	}
	// Read pristine code before test-only extra roots alter default selection.
	phase("performance", func() { m.performance(a, store) })
	var sshRemote, sshStore string
	var sshOpts []string
	sshOK := phase("ssh-ingestion", func() {
		var ok bool
		sshRemote, sshOpts, sshStore, ok = m.seed(a, transportSSH, "private")
		if !ok {
			return
		}
		m.compareIdentity(sshStore, sshRemote, sshOpts, "retained-ssh")
	})
	// Native local verification needs no running service. Stop cleanly instead
	// of spending the application's finite lifetime on independent native reads.
	a.stop()
	if !a.lastCleanStop {
		m.t.Fatal("retained application did not stop before native verification")
	}
	httpsVerified := phase("https-verification", func() { m.must("https-verify", store, "verify") })
	sshVerified := false
	if sshOK {
		sshVerified = phase("ssh-verification", func() { m.must("ssh-verify", sshStore, "verify") })
	}
	a.start(m.t)
	workflowOK := httpsVerified && phase("https-workflows", func() { m.workflows(a, transportHTTP, "public", remote, opts, store) })
	if sshVerified {
		workflowOK = phase("ssh-workflows", func() { m.workflows(a, transportSSH, "private", sshRemote, sshOpts, sshStore) }) && workflowOK
	} else {
		workflowOK = false
	}
	issueOK := phase("issues", func() { m.issues(a) })
	results["correctness"] = workflowOK && issueOK && sshOK && httpsVerified && sshVerified && m.identity.VerificationState != mozillaSourceUnverified
	b, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(m.root, "raw", "retained-phase-results.json"), b, 0600); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mozillaRun) seed(a *application, transport, repo string) (string, []string, string, bool) {
	remote, opts := a.remote(m.t, transport, "writer", repo)
	store := filepath.Join(a.repoRoot, fmt.Sprint(a.repos[repo].Id))
	seedKey := m.source + "|" + m.identity.NodesSHA256 + "|" + a.binary + "|" + transport
	if previous, ok := mozillaFailedSeeds.Load(seedKey); ok {
		m.record(mozillaSample{Name: "initial-push-" + transport, Kind: "blocked-prerequisite", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Error: previous.(string)})
		m.t.Errorf("%s ingestion already failed under unchanged limits; dependent phase remains unverified: %s", transport, previous)
		return remote, opts, store, false
	}
	if m.text("empty-"+transport, store, "log", "-T", "{node}") != "" {
		m.t.Fatal("seed target is not empty")
	}
	args := append(append([]string{}, opts...), "push", "--force", remote)
	for _, line := range strings.Split(m.identity.Bookmarks, "\n") {
		if line != "" {
			name, _, _ := strings.Cut(line, "\t")
			args = append(args, "--bookmark", name)
		}
	}
	s := m.command("initial-push-"+transport, m.source, args...)
	if s.Error != "" {
		mozillaFailedSeeds.Store(seedKey, s.Error+"; "+s.Stderr)
		m.t.Errorf("full %s ingestion failed: %s; evidence %s", transport, s.Error, s.Stderr)
		return remote, opts, store, false
	}
	m.t.Logf("full %s ingestion %.3fs", transport, s.Seconds)
	return remote, opts, store, true
}

func (m *mozillaRun) compare(store, remote string, opts []string, label string) {
	m.must(label+"-verify", store, "verify")
	m.compareIdentity(store, remote, opts, label)
}

func (m *mozillaRun) compareIdentity(store, remote string, opts []string, label string) {
	nodes := m.must(label+"-nodes", store, "log", "-r", "sort(all(), rev)", "-T", "{node}\n")
	// Revision order can differ between exchanges. Compare sorted full node sets.
	want := m.mustHashOnly(label+"-source-set", m.source, "log", "-r", "sort(all(), node)", "-T", "{node}\n")
	got := m.mustHashOnly(label+"-host-set", store, "log", "-r", "sort(all(), node)", "-T", "{node}\n")
	if nodes.Bytes != m.identity.Changesets*41 || want.SHA256 != got.SHA256 {
		m.t.Error(label + ": complete visible changeset sets differ")
	}
	for _, q := range []struct {
		name, want string
		args       []string
	}{{"heads", m.identity.Heads, []string{"log", "-r", "heads(all())", "-T", "{node}\n"}}, {"branches", m.identity.Branches, []string{"branches", "--closed", "-T", "{branch}\t{node}\t{closed}\n"}}, {"tags", m.identity.Tags, []string{"tags", "-T", "{tag}\t{node}\n"}}, {"bookmarks", m.identity.Bookmarks, []string{"bookmarks", "-T", "{bookmark}\t{node}\n"}}} {
		got := m.text(label+"-"+q.name, store, q.args...)
		if q.name == "tags" {
			got = mozillaTrackedTags(got)
		}
		a, b := strings.Split(got, "\n"), strings.Split(q.want, "\n")
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, "\n") != strings.Join(b, "\n") {
			m.t.Error(label + ": " + q.name + " differ")
		}
	}
	for _, verb := range []string{"incoming", "outgoing"} {
		s := m.command(label+"-"+verb, m.source, append(append([]string{}, opts...), verb, "--quiet", remote)...)
		b, _ := os.ReadFile(s.Stdout)
		e, _ := os.ReadFile(s.Stderr)
		if s.ExitCode != 1 || len(bytes.TrimSpace(b)) != 0 || len(bytes.TrimSpace(e)) != 0 {
			m.t.Errorf("%s %s expected native no-changes exit1/empty output, got error=%q; inspect %s", label, verb, s.Error, s.Stderr)
		}
	}
	for _, rev := range []string{"0", fmt.Sprint(m.identity.Changesets / 2), m.identity.Tip, m.browseNode} {
		node := m.text(label+"-pin", m.source, "log", "-r", rev, "-T", "{node}")
		want := m.mustHashOnly(label+"-source-manifest", m.source, "manifest", "--debug", "-r", node)
		got := m.mustHashOnly(label+"-host-manifest", store, "manifest", "--debug", "-r", node)
		if want.SHA256 != got.SHA256 {
			m.t.Error(label + ": pinned manifest differs at " + node)
		}
	}
	// Publishing phase changes are recorded separately, never equated with nodes.
	m.must(label+"-phase-counts", store, "log", "-r", "not public()", "-T", "{node}\t{phase}\n")
}

func TestMozillaCentralIngestion(t *testing.T) {
	skipStandaloneMozillaOrdered(t)
	m := newMozillaRun(t)
	m.freeze()
	if os.Getenv("GOGS_TEST_MOZILLA_RETAINED") == "1" {
		m.retainedPhases()
		return
	}
	a := m.application()
	for i, transport := range []string{transportHTTP, transportSSH} {
		repo := []string{"public", "private"}[i]
		remote, opts, store, ok := m.seed(a, transport, repo)
		if ok {
			m.compare(store, remote, opts, transport)
		}
	}
}

func (m *mozillaRun) request(a *application, name, path string, concurrency int) (mozillaSample, []byte) {
	start := time.Now()
	s := mozillaSample{Name: name, Kind: "https-ui", StartedUTC: start.UTC().Format(time.RFC3339Nano), Concurrency: concurrency, Args: "GET https://" + a.address + path}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var first time.Time
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { first = time.Now() }}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", "https://"+a.address+path, nil)
	var body []byte
	if e == nil {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, Timeout: 45 * time.Second, Jar: m.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer client.CloseIdleConnections()
		var resp *http.Response
		resp, e = client.Do(req)
		if e == nil {
			s.Status = resp.StatusCode
			if strings.Contains(path, "/raw/") {
				s.Kind = "https-raw"
				s.HashOnly = true
				w := &mozillaSink{w: io.Discard, h: sha256.New()}
				_, e = io.Copy(w, resp.Body)
				s.Bytes = w.n
				s.SHA256 = hex.EncodeToString(w.h.Sum(nil))
			} else {
				s.Stdout = filepath.Join(m.raw, fmt.Sprintf("%04d.http", m.next.Add(1)))
				f, openErr := os.Create(s.Stdout)
				if openErr != nil {
					e = openErr
				} else {
					w := &mozillaSink{w: f, h: sha256.New()}
					_, e = io.Copy(w, resp.Body)
					f.Close()
					s.Bytes = w.n
					s.SHA256 = hex.EncodeToString(w.h.Sum(nil))
					if s.Bytes <= 2<<20 {
						body, _ = os.ReadFile(s.Stdout)
					}
				}
			}
			resp.Body.Close()
			// A branch selection pins the node by design: it answers 302, not a page.
			if resp.StatusCode != 200 && !(resp.StatusCode == 302 && strings.Contains(path, "ref_kind=") && strings.Contains(resp.Header.Get("Location"), "/owner/public/")) && e == nil {
				e = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		}
	}
	s.Seconds = time.Since(start).Seconds()
	if !first.IsZero() {
		s.FirstByteSeconds = first.Sub(start).Seconds()
	}
	if e != nil {
		s.Error = e.Error()
	}
	m.record(s)
	return s, body
}

func (m *mozillaRun) workflows(a *application, transport, repo, remote string, opts []string, store string) {
	clone := filepath.Join(a.root, "verify-"+transport)
	m.must("full-clone-"+transport, "", append(append([]string{}, opts...), "clone", "--noupdate", remote, clone)...)
	m.compare(clone, remote, opts, "clone-"+transport)
	// Only the reader holds another full store. The writer makes a disposable
	// extra root, so testing incremental changes never checks out Firefox.
	writer := filepath.Join(a.root, "writer-"+transport)
	m.must("workflow-init", "", "init", writer)
	if e := os.WriteFile(filepath.Join(writer, "task16-workflow.txt"), []byte("increment 0\n"), 0600); e != nil {
		m.t.Fatal(e)
	}
	m.must("workflow-add", writer, "add", "task16-workflow.txt")
	m.must("workflow-commit", writer, "commit", "-u", "task16", "-m", "task16 disposable extra head")
	m.must("workflow-bookmark", writer, "bookmark", "task16-disposable")
	before := m.mustHashOnly("acl-before", store, "log", "-r", "sort(all(), node)", "-T", "{node}\n")
	beforeStore := m.storeDigest("acl-store-before", store)
	// Revoke before opening a new session. Already-open writer transactions are
	// deliberately not asserted to roll back under the current authorization policy.
	if e := models.DeleteAccess(&models.Access{UserId: a.users["writer"].Id, RepoId: a.repos[repo].Id}); e != nil {
		m.t.Fatal(e)
	}
	revoked := m.command("revoked-writer-push-"+transport, writer, append(append([]string{}, opts...), "push", "--force", "--bookmark", "task16-disposable", remote)...)
	if revoked.Error == "" {
		m.t.Error("revoked writer opened a new write session")
	}
	if m.storeDigest("revoked-writer-store-after", store) != beforeStore {
		m.t.Error("revoked writer changed stable store bytes")
	}
	if e := models.AddAccess(&models.Access{UserId: a.users["writer"].Id, RepoId: a.repos[repo].Id, Mode: models.WRITABLE}); e != nil {
		m.t.Fatal(e)
	}
	roles := []string{"reader", "stranger"}
	if transport == transportHTTP {
		roles = append(roles, "anonymous")
	}
	for _, role := range roles {
		denied, dopts := a.remote(m.t, transport, role, repo)
		s := m.command("denied-push-"+transport+"-"+role, writer, append(append([]string{}, dopts...), "push", "--force", "--bookmark", "task16-disposable", denied)...)
		if s.Error == "" {
			m.t.Error("unauthorized full-history push succeeded")
		}
		after := m.mustHashOnly("acl-after", store, "log", "-r", "sort(all(), node)", "-T", "{node}\n")
		if after.SHA256 != before.SHA256 {
			m.t.Error("denied push changed changesets")
		}
		if m.storeDigest("acl-store-after-"+role, store) != beforeStore {
			m.t.Error("denied push changed stable repository store bytes")
		}
		if m.text("acl-bookmarks", store, "bookmarks", "-T", "{bookmark}\t{node}\n") != m.identity.Bookmarks {
			m.t.Error("denied push changed bookmarks")
		}
	}
	if repo == "private" {
		denied, dopts := a.remote(m.t, transport, "stranger", repo)
		s := m.command("denied-private-read-"+transport, clone, append(append([]string{}, dopts...), "incoming", denied)...)
		b, _ := os.ReadFile(s.Stderr)
		if s.Error == "" || len(b) == 0 {
			m.t.Error("private read lacks explicit denial")
		}
	}
	reader, readopts := a.remote(m.t, transport, "reader", repo)
	var node string
	// Five isolated samples precede the separate requested reader/writer sweep.
	// Concurrency labels describe the cohort; timestamps establish actual overlap.
	readerLoads := []int{0, 0, 0, 0, 0}
	if repo == "public" {
		readerLoads = append(readerLoads, 1, 4, 8, 12)
	}
	for i, n := range readerLoads {
		cohort := "isolated"
		if n > 0 {
			cohort = "mixed"
		}
		if i > 0 {
			if e := os.WriteFile(filepath.Join(writer, "task16-workflow.txt"), []byte(fmt.Sprintf("increment %d\n", i)), 0600); e != nil {
				m.t.Fatal(e)
			}
			m.must("workflow-commit", writer, "commit", "-u", "task16", "-m", fmt.Sprintf("increment %d", i))
		}
		node = m.text("workflow-node", writer, "log", "-r", "tip", "-T", "{node}")
		var cursor []byte
		if n > 0 {
			_, page := m.request(a, "pagination-before", "/owner/public/commits", 1)
			match := regexp.MustCompile(`href="([^"]*\?cursor=[^"]+)"`).FindSubmatch(page)
			if len(match) > 1 {
				cursor = match[1]
			} else {
				m.t.Error("history page supplied no pagination cursor")
			}
		}
		done := make(chan mozillaSample, 1)
		go func() {
			done <- m.commandWithConcurrency("incremental-push-"+cohort+"-"+transport, writer, n+1, append(append([]string{}, opts...), "push", "--force", "--bookmark", "task16-disposable", remote)...)
		}()
		var cursorSample mozillaSample
		if n > 0 {
			var wg sync.WaitGroup
			for j := 0; j < n; j++ {
				wg.Add(1)
				go func(j int) {
					defer wg.Done()
					if j == 0 && len(cursor) > 0 {
						u, e := url.Parse(html.UnescapeString(string(cursor)))
						if e != nil {
							m.t.Error(e)
							return
						}
						var body []byte
						cursorSample, body = m.request(a, "pagination-during-push", u.RequestURI(), n+1)
						if cursorSample.Error != "" || bytes.Contains(body, []byte(node)) {
							m.t.Error("frozen history cursor failed during concurrent push")
						}
						return
					}
					m.request(a, "mixed-readers-writer", "/owner/public/commits", n+1)
				}(j)
			}
			wg.Wait()
		}
		push := <-done
		if push.Error != "" {
			m.t.Fatalf("incremental push failed: %s %s", push.Error, push.Stderr)
		}
		if n > 0 {
			pushStart, _ := time.Parse(time.RFC3339Nano, push.StartedUTC)
			cursorStart, _ := time.Parse(time.RFC3339Nano, cursorSample.StartedUTC)
			overlap := cursorSample.StartedUTC != "" && cursorStart.Before(pushStart.Add(time.Duration(push.Seconds*float64(time.Second)))) && pushStart.Before(cursorStart.Add(time.Duration(cursorSample.Seconds*float64(time.Second))))
			evidence := mozillaSample{Name: "pagination-push-overlap", Kind: "overlap-check", Concurrency: n + 1, StartedUTC: push.StartedUTC}
			if !overlap {
				evidence.Error = "unverified: requested cursor and push intervals did not overlap"
				m.t.Log(evidence.Error)
			}
			m.record(evidence)
		}
		m.must("incremental-pull-after-"+cohort+"-"+transport, clone, append(append([]string{}, readopts...), "pull", reader)...)
		if m.text("incremental-pull-node", clone, "log", "-r", node, "-T", "{node}") != node {
			m.t.Error("reader missed incremental node")
		}
		m.must("noop-pull-after-"+cohort+"-"+transport, clone, append(append([]string{}, readopts...), "pull", reader)...)
	}
	a.stop()
	if !a.lastCleanStop {
		m.t.Error("application did not stop cleanly")
	}
	a.start(m.t)
	if m.text("restart-node", store, "log", "-r", node, "-T", "{node}") != node {
		m.t.Error("accepted node lost across restart")
	}
	if !strings.Contains(m.text("restart-bookmark", store, "bookmarks", "-T", "{bookmark}\t{node}\n"), "task16-disposable\t"+node) {
		m.t.Error("bookmark lost across restart")
	}
	m.must("pull-after-restart-"+transport, clone, append(append([]string{}, readopts...), "pull", reader)...)
	m.must("workflow-final-verify", store, "verify")
	original := m.mustHashOnly("workflow-original-node-set", m.source, "log", "-r", "sort(all(), node)", "-T", "{node}\n")
	retained := m.mustHashOnly("workflow-retained-node-set", store, "log", "-r", "sort(all() - ancestors("+node+"), node)", "-T", "{node}\n")
	if original.SHA256 != retained.SHA256 || original.Bytes != retained.Bytes {
		m.t.Error("original full-history node set changed after workflow/restart")
	}
	// Remove only this function's complete disposable reader after all processes
	// have finished, bounding simultaneous full stores to source+two hosts+reader.
	if e := os.RemoveAll(clone); e != nil {
		m.t.Fatal(e)
	}
}
func TestMozillaCentralCorrectness(t *testing.T) {
	skipStandaloneMozillaOrdered(t)
	m := newMozillaRun(t)
	m.freeze()
	if os.Getenv("GOGS_TEST_MOZILLA_RETAINED") == "1" {
		m.retainedOutcome("correctness")
		return
	}
	a := m.application()
	for i, transport := range []string{transportHTTP, transportSSH} {
		repo := []string{"public", "private"}[i]
		remote, opts, store, ok := m.seed(a, transport, repo)
		if !ok {
			continue
		}
		m.compare(store, remote, opts, transport)
		if repo == "public" {
			m.files(a, store, 1)
		}
		m.workflows(a, transport, repo, remote, opts, store)
	}
	m.issues(a)
}

// nativePathPattern keeps manifest path bytes out of Windows argv recoding.
// Like the production file/history readers, native Hg reads literal path:
// patterns from a NUL-delimited file. Keep the pattern as raw evidence.
func (m *mozillaRun) nativePathPattern(paths ...string) string {
	m.t.Helper()
	f, err := os.CreateTemp(m.raw, "native-path-*.list")
	if err != nil {
		m.t.Fatal(err)
	}
	for _, r := range f.Name() {
		if r > 127 {
			f.Close()
			m.t.Fatal("native pattern filename must be ASCII")
		}
	}
	for _, path := range paths {
		if path == "" || strings.ContainsAny(path, "\x00\n\r") {
			f.Close()
			m.t.Fatal("invalid manifest path")
		}
		if _, err := f.Write([]byte("path:" + path + "\x00")); err != nil {
			f.Close()
			m.t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		m.t.Fatal(err)
	}
	return "listfile0:" + f.Name()
}

// files scans the immutable manifest once. Size probes are limited to 64
// deterministic media candidates; "large" means largest among those candidates,
// never the global largest file. This avoids reopening every historical filelog.
func (m *mozillaRun) files(a *application, store string, samples int) {
	copies := m.must("recent-file-copy-candidates", m.source, "log", "-r", "last(ancestors("+m.browseNode+"), 1000)", "-T", "{file_copies % '{name}\\n'}")
	copyData, e := os.ReadFile(copies.Stdout)
	if e != nil {
		m.t.Fatal(e)
	}
	candidates := map[string]bool{}
	for _, path := range strings.Split(string(copyData), "\n") {
		if path != "" {
			candidates[path] = true
		}
	}
	inventory := m.mustWithOptions("code-manifest-paths", m.source, mozillaCommandOptions{Concurrency: 1, HashOnly: false}, "manifest", "-r", m.browseNode)
	f, e := os.Open(inventory.Stdout)
	if e != nil {
		m.t.Fatal(e)
	}
	defer f.Close()
	selected := map[string]string{}
	sizePaths := []string{}
	var count int64
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		path := scanner.Text()
		count++
		if m.followPath == "" && candidates[path] {
			m.followPath = path
		}
		if selected["text"] == "" && strings.HasSuffix(path, ".txt") {
			selected["text"] = path
		}
		if selected["binary"] == "" && strings.HasSuffix(path, ".png") {
			selected["binary"] = path
		}
		if selected["non-ascii"] == "" {
			for _, r := range path {
				if r > 127 {
					selected["non-ascii"] = path
					break
				}
			}
		}
		if len(sizePaths) < 64 {
			switch strings.ToLower(filepath.Ext(path)) {
			case ".webm", ".mp4", ".ogg", ".wav", ".zip", ".wasm":
				sizePaths = append(sizePaths, path)
			}
		}
	}
	if e = scanner.Err(); e != nil {
		m.t.Fatal(e)
	}
	if len(sizePaths) == 0 && selected["binary"] != "" {
		sizePaths = append(sizePaths, selected["binary"])
	}
	var largest int64
	if len(sizePaths) > 0 {
		args := []string{"files", "-r", m.browseNode, "-T", "{size}\t{path}\n", "--", m.nativePathPattern(sizePaths...)}
		sizes := m.command("representative-size-inventory-max64", m.source, args...)
		if sizes.Error == "" {
			data, err := os.ReadFile(sizes.Stdout)
			if err != nil {
				m.t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				value, path, ok := strings.Cut(line, "\t")
				n, err := strconv.ParseInt(value, 10, 64)
				if !ok || err != nil {
					m.t.Fatal("invalid completed size inventory")
				}
				if n > largest {
					largest, selected["large"] = n, path
				}
			}
		} else {
			m.t.Error("representative size inventory failed; large-file coverage unverified")
		}
	}
	if m.followPath == "" {
		m.followPath = selected["text"]
		m.t.Log("rename event unavailable among last1000 code ancestors; rename coverage unverified")
	}
	b, _ := json.MarshalIndent(map[string]interface{}{"browseNode": m.browseNode, "manifestCount": count, "sizeCandidateCount": len(sizePaths), "largestCandidateBytes": largest, "selected": selected, "followHistoryPath": m.followPath, "followCopyEvidence": candidates[m.followPath], "selection": "deterministic first64 media paths; no global-largest claim"}, "", "  ")
	if e = os.WriteFile(filepath.Join(m.raw, "file-selection.json"), b, 0600); e != nil {
		m.t.Fatal(e)
	}
	for _, kind := range []string{"text", "binary", "large", "non-ascii"} {
		path, ok := selected[kind]
		if !ok {
			m.t.Logf("representative unavailable: %s", kind)
			continue
		}
		if kind == "large" {
			m.largePath = path
		}
		pattern := m.nativePathPattern(path)
		want := m.mustHashOnly("source-file-"+kind, m.source, "cat", "-r", m.browseNode, "--", pattern)
		if kind == "large" {
			m.largeBytes = want.Bytes
		}
		got := m.mustHashOnly("host-file-"+kind, store, "cat", "-r", m.browseNode, "--", pattern)
		if want.SHA256 != got.SHA256 || want.Bytes != got.Bytes {
			m.t.Errorf("%s native file identity differs", kind)
		}
		for i := 0; i < samples; i++ {
			s, _ := m.request(a, "raw-"+kind, "/owner/public/raw/"+m.browseNode+"/"+strings.ReplaceAll(url.PathEscape(path), "%2F", "/"), 1)
			if s.Error != "" || s.SHA256 != want.SHA256 || s.Bytes != want.Bytes {
				m.t.Errorf("%s raw identity sample%d: error=%s bytes=%d want%d", kind, i, s.Error, s.Bytes, want.Bytes)
			}
		}
	}
}

func (m *mozillaRun) issues(a *application) {
	var err error
	m.jar, err = cookiejar.New(nil)
	if err != nil {
		m.t.Fatal(err)
	}
	s, body := m.request(a, "issue-login-form", "/user/login", 1)
	match := regexp.MustCompile(`<meta name="_csrf" content="([^"]+)"`).FindSubmatch(body)
	if s.Error != "" || len(match) != 2 {
		m.t.Fatal("issue login CSRF form missing")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, Timeout: 30 * time.Second, Jar: m.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("POST", "https://"+a.address+"/user/login", strings.NewReader(url.Values{"uname": {"owner"}, "password": {"password"}}.Encode()))
	if err != nil {
		m.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Csrf-Token", string(match[1]))
	resp, err := client.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 302 {
		m.t.Fatalf("issue login HTTP %d", resp.StatusCode)
	}
	issue := &models.Issue{RepoId: a.repos["public"].Id, Index: 1, Name: "Mozilla benchmark issue", PosterId: a.users["owner"].Id, Content: "retained full-history issue"}
	if e := models.NewIssue(issue); e != nil {
		m.t.Fatal(e)
	}
	data := []byte("Mozilla benchmark attachment\x00\xff\n")
	path := filepath.Join(a.root, "attachment.bin")
	if e := os.WriteFile(path, data, 0600); e != nil {
		m.t.Fatal(e)
	}
	attachment, e := models.CreateAttachment(issue.Id, 0, "attachment.bin", path)
	if e != nil {
		m.t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		s, body := m.request(a, "issue-read", "/owner/public/issues/1", 1)
		if s.Error != "" || !bytes.Contains(body, []byte(issue.Content)) {
			m.t.Error("full-history issue read failed")
		}
		s, body = m.request(a, "attachment-read", fmt.Sprintf("/owner/public/issues/1/attachment/%d", attachment.Id), 1)
		if s.Error != "" || !bytes.Equal(body, data) {
			m.t.Error("issue attachment identity failed")
		}
		if i == 0 {
			a.stop()
			a.start(m.t)
		}
	}
}

func TestMozillaCentralPerformance(t *testing.T) {
	skipStandaloneMozillaOrdered(t)
	m := newMozillaRun(t)
	m.freeze()
	if os.Getenv("GOGS_TEST_MOZILLA_RETAINED") == "1" {
		m.retainedOutcome("performance")
		return
	}
	a := m.application()
	remote, opts, store, ok := m.seed(a, transportHTTP, "public")
	if !ok {
		return
	}
	m.performance(a, store)
	m.workflows(a, transportHTTP, "public", remote, opts, store)
}

// Run separately after hosting workers have exited. This bounded source-only
// cohort supplies native baselines even when full hosted ingestion is blocked.
// It does not turn the failed source-integrity gate into a pass.
func TestMozillaCentralNativeBaseline(t *testing.T) {
	skipStandaloneMozillaOrdered(t)
	m := newMozillaRun(t)
	m.deadline = 30 * time.Second
	m.freeze()
	m.record(mozillaSample{Name: "native-baseline-cohort", Kind: "measurement-scope", StartedUTC: time.Now().UTC().Format(time.RFC3339Nano), Args: "source-only; warm shared workstation; exactly five samples per query;30s operation bound; no hosted/UI measurements"})
	for i := 0; i < 5; i++ {
		for _, query := range []struct {
			name, repo string
			args       []string
			hashOnly   bool
		}{
			{"source-baseline-startup", "", []string{"version", "-T", "json"}, false},
			{"source-baseline-code-manifest", m.source, []string{"manifest", "-r", m.browseNode}, true},
			{"source-baseline-global-history", m.source, []string{"log", "-l", "30", "-T", "json"}, false},
		} {
			result := m.commandWithOptions(query.name, query.repo, mozillaCommandOptions{Concurrency: 1, HashOnly: query.hashOnly}, query.args...)
			if result.Error != "" {
				t.Errorf("%s sample%d failed: %s", query.name, i, result.Error)
			}
		}
	}
	m.freeze() // Exact full node/ref/requirements identity, never another verify.
}

func (m *mozillaRun) performance(a *application, store string) {
	node := m.browseNode
	m.files(a, store, 5)
	m.cancelRead(a)
	merge := m.text("select-merge", m.source, "log", "-r", "last(merge() and ancestors("+node+"))", "-T", "{node}")
	_, filePage := m.request(a, "file-history-link-source", "/owner/public/src/"+node+"/"+strings.ReplaceAll(url.PathEscape(m.followPath), "%2F", "/"), 1)
	historyLink, err := mozillaHistoryLink(filePage, node, m.followPath)
	if err != nil {
		m.t.Fatal(err)
	}
	pages := []struct{ name, path string }{{"home", "/owner/public"}, {"ref-selection", "/owner/public?ref_kind=branch&ref_name=default"}, {"directory", "/owner/public/src/" + node + "/dom"}, {"repository-history", "/owner/public/commits"}, {"file-history", historyLink}}
	if merge == "" {
		m.t.Log("no merge among code ancestors; merge-diff coverage unverified")
	} else {
		pages = append(pages, struct{ name, path string }{"merge-diff-parent0", "/owner/public/commit/" + merge + "?parent=0"}, struct{ name, path string }{"merge-diff-parent1", "/owner/public/commit/" + merge + "?parent=1"})
	}
	for _, p := range pages {
		for i := 0; i < 5; i++ {
			s, body := m.request(a, p.name, p.path, 1)
			if s.Error != "" {
				m.t.Errorf("%s sample%d: %s", p.name, i, s.Error)
				continue
			}
			switch p.name {
			case "home", "directory":
				if !bytes.Contains(body, []byte(`href="/owner/public/commit/`+node+`"`)) {
					m.t.Errorf("%s sample%d did not render the selected code node", p.name, i)
				}
			case "file-history":
				if !bytes.Contains(body, []byte(html.EscapeString(m.followPath))) {
					m.t.Errorf("file-history sample%d did not render the selected path", i)
				}
			case "merge-diff-parent0", "merge-diff-parent1":
				parent := strings.TrimPrefix(p.name, "merge-diff-parent")
				selected := regexp.MustCompile(regexp.QuoteMeta(`href="/owner/public/commit/`+merge+`?parent=`+parent+`"`) + `>[^<]*</a> ✓`)
				if !selected.Match(body) {
					m.t.Errorf("%s sample%d did not select the requested merge parent", p.name, i)
				}
			}
		}
	}
	// Native costs use the same frozen dataset and streaming output, separately
	// from Go/database/proxy/rendering. They are not subtracted as causal proof.
	followPattern := m.nativePathPattern(m.followPath)
	for i := 0; i < 5; i++ {
		m.must("baseline-process-startup", "", "version", "-T", "json")
		m.mustHashOnly("baseline-manifest", store, "manifest", "-r", node)
		m.must("baseline-history", store, "log", "-l", "30", "-T", "json")
		m.must("baseline-file-history", store, "log", "--follow", "-r", node, "-l", "30", "--", followPattern)
	}
	for _, n := range []int{1, 4, 8, 12} {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); m.request(a, "concurrent-history", "/owner/public/commits", n) }()
		}
		wg.Wait()
	}
}

func (m *mozillaRun) cancelRead(a *application) {
	if m.largePath == "" || m.largeBytes < 1<<20 {
		m.t.Error("active-stream cancellation unverified: representative candidate is absent or below1MiB")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	path := "/owner/public/raw/" + m.browseNode + "/" + strings.ReplaceAll(url.PathEscape(m.largePath), "%2F", "/")
	req, e := http.NewRequestWithContext(ctx, "GET", "https://"+a.address+path, nil)
	if e != nil {
		m.t.Fatal(e)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	start := time.Now()
	s := mozillaSample{Name: "cancel-large-read", Kind: "https-cancellation", StartedUTC: start.UTC().Format(time.RFC3339Nano)}
	resp, e := client.Do(req)
	if e == nil {
		s.Status = resp.StatusCode
		if resp.StatusCode != 200 {
			e = fmt.Errorf("HTTP %d before cancellation", resp.StatusCode)
		} else {
			var one [1]byte
			_, e = io.ReadFull(resp.Body, one[:])
			s.Bytes = 1
			cancel()
			n, readErr := io.Copy(io.Discard, resp.Body)
			s.Bytes += n
			if e == nil {
				e = readErr
			}
		}
		resp.Body.Close()
	}
	s.Seconds = time.Since(start).Seconds()
	if e != nil {
		s.Error = e.Error()
	}
	m.record(s)
	if s.Status != 200 {
		m.t.Error("large-file cancellation could not reach an active stream")
	} else if s.Bytes >= m.largeBytes || !errors.Is(e, context.Canceled) {
		m.t.Error("active-stream cancellation unverified: response completed or no cancellation error was observed")
	}
	// A fresh read checks admissions after cancellation. Final application.stop
	// verifies no owned process survives; in-process capability counts are not
	// exposed by the production binary and remain a named observation gap.
	after, _ := m.request(a, "post-cancellation-history", "/owner/public/commits", 1)
	if after.Error != "" {
		m.t.Error("read admission failed after cancellation")
	}
}

func classifyMozillaIdentity(id *mozillaIdentity, subset, conditional bool) {
	id.VerificationState = "VERIFIED"
	id.SourceClassification = "upstream-clone"
	id.IntegrityVerification = "local-hg-verify"
	if subset {
		id.URL = ""
		id.UpstreamReference = mozillaURL
		id.SourceClassification = "transformed-subset"
		id.VerificationState = "TRANSFORMED_SUBSET_LOCAL_INTEGRITY_VERIFIED"
	}
	if conditional {
		id.VerificationState = mozillaSourceUnverified
		id.IntegrityVerification = "unverified"
	}
}
