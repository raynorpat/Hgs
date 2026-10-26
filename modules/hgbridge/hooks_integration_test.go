// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package hgbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

func TestRealHookRejectsTransaction(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	gogs := buildGogs(t)
	t.Run("current", func(t *testing.T) { checkRealHooks(t, exe, gogs) })
	if old := os.Getenv("GOGS_TEST_HG_OLD_SERVER"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_SERVER (Mercurial 5.0.x) not set")
	} else {
		t.Run("old server", func(t *testing.T) { checkRealHooks(t, old, gogs) })
	}
}

// TestHookCommandSurvivesTonative feeds accepted hook commands through
// Mercurial's own hooks.tonative rewrite (procutil.shelltonative, which is
// shelltocmdexe on Windows and the identity elsewhere) and requires them to
// come back unchanged, so enabling that option cannot alter what runs.
func TestHookCommandSurvivesTonative(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}

	goos, paths := runtime.GOOS, windowsAccepted
	if goos != "windows" {
		paths = []string{`/opt/Gogs é ü/it's "$HOME" ~/x/gogs`}
	}
	var accepted []string
	for _, p := range paths {
		q, err := quoteFor(goos, p)
		if err != nil {
			t.Fatal(err)
		}
		accepted = append(accepted, q+" "+HookCommand+" check pretxnopen")
	}
	installed, err := HookConfig(filepath.Join(t.TempDir(), "gogs bin é ü PROGRA~1", "gogs.exe"), true)
	if err != nil {
		t.Fatal(err)
	}
	accepted = append(accepted, values(installed)...)
	// The reviewer's injection, which the rewrite must visibly alter on
	// Windows; this shows the probe really reaches shelltocmdexe.
	injection := `"C:\x'&echo PWN&'\gogs.exe" ` + HookCommand + " check pretxnopen"

	var hexes []string
	for _, c := range append(accepted, injection) {
		hexes = append(hexes, "'"+hex.EncodeToString([]byte(c))+"'")
	}
	code := "print(*[__import__('binascii').hexlify(__import__('mercurial.utils.procutil', fromlist=['_'])" +
		".shelltonative(__import__('binascii').unhexlify(h), {b'HG_NODE': b'abc'})).decode()" +
		" for h in [" + strings.Join(hexes, ", ") + "]], sep=' ')"
	var out bytes.Buffer
	if err := r.Run(context.Background(), "", []string{"debugshell", "-c", code}, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(out.String())
	if len(got) != len(hexes) {
		t.Fatalf("shelltonative probe returned %d results for %d commands: %q", len(got), len(hexes), out.String())
	}
	for i, c := range accepted {
		if b, _ := hex.DecodeString(got[i]); string(b) != c {
			t.Errorf("Mercurial rewrites accepted hook command\n%s\ninto\n%s", c, b)
		}
	}
	b, _ := hex.DecodeString(got[len(got)-1])
	if rewritten := string(b) != injection; rewritten != (goos == "windows") {
		t.Errorf("shelltonative(%q) = %q; probe does not exercise Mercurial's rewrite", injection, b)
	}
}

// buildGogs installs the application binary where quoting matters: spaces,
// non-ASCII letters (ANSI-representable on Windows) and, for sh, a single
// quote, which quoteFor rejects for cmd.exe.
func buildGogs(t *testing.T) string {
	name, dir := "gogs", "gogs bin é ü it's"
	if runtime.GOOS == "windows" {
		name, dir = "gogs.exe", "gogs bin é ü PROGRA~1"
	}
	gogs := filepath.Join(t.TempDir(), dir, name)
	if err := hg.CheckPath(gogs); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", gogs, "github.com/gogits/gogs")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gogs: %v\n%s", err, out)
	}
	return gogs
}

type hgHarness struct {
	t   *testing.T
	r   *hg.Runner
	ctx context.Context
}

func configArgs(cfg map[string]string) []string {
	var keys []string
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		args = append(args, "--config", k+"="+cfg[k])
	}
	return args
}

// hg runs one command and returns stdout, or an error carrying stdout and stderr.
func (h *hgHarness) hg(repo string, cfg, env map[string]string, args ...string) (string, error) {
	c, err := h.r.Start(h.ctx, repo, append(configArgs(cfg), args...), env)
	if err != nil {
		h.t.Fatal(err)
	}
	var stderr bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&stderr, c.Stderr); close(done) }()
	out, _ := io.ReadAll(c.Stdout)
	<-done
	err = c.Stop(h.ctx)
	if err != nil {
		return string(out), fmt.Errorf("hg %v: %v: %s%s", args, err, out, stderr.Bytes())
	}
	return string(out), nil
}

func (h *hgHarness) must(repo string, cfg, env map[string]string, args ...string) string {
	h.t.Helper()
	out, err := h.hg(repo, cfg, env, args...)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// snapshot captures repository contents and metadata: every revision with
// its phase and bookmarks, the heads, and the bookmark table.
func (h *hgHarness) snapshot(repo string) string {
	h.t.Helper()
	return h.must(repo, nil, nil, "log", "--hidden", "-T", "{rev}:{node} {phase} {bookmarks} {files}\n") +
		h.must(repo, nil, nil, "heads", "-T", "{node}\n") +
		h.must(repo, nil, nil, "bookmarks", "-T", "{bookmark}={node}\n")
}

// denied runs a command that must fail and leave repo exactly as it was.
func (h *hgHarness) denied(name, repo string, cfg, env map[string]string, args ...string) {
	h.t.Helper()
	before := h.snapshot(repo)
	if _, err := h.hg(repo, cfg, env, args...); err == nil {
		h.t.Errorf("%s: hg %v succeeded", name, args)
	} else if !hookRejected.MatchString(err.Error()) {
		h.t.Errorf("%s: not rejected by the Gogs hook: %v", name, err)
	}
	if after := h.snapshot(repo); after != before {
		h.t.Errorf("%s: repository changed\nbefore:\n%s\nafter:\n%s", name, before, after)
	}
	h.must(repo, nil, nil, "verify", "-q")
}

var boundPort = regexp.MustCompile(`bound to [^)]*:(\d+)\)`)

// hookRejected matches the Gogs hook's message or Mercurial's report of one of
// the installed hooks failing; on some paths Mercurial drops hook output.
var hookRejected = regexp.MustCompile(`gogs: write rejected|(pretxnopen|prepushkey|pretxnclose|pretxnclose-bookmark|pretxnclose-phase) hook exited with status 1`)

// serve starts an HTTP push target for repo with the given hooks and session
// environment, as the HTTPS transport's per-request workers will.
func (h *hgHarness) serve(repo string, cfg, env map[string]string) (string, *syncBuffer) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(h.ctx)
	args := append(configArgs(cfg), "--config", "web.allow-push=*", "--config", "web.push_ssl=false",
		"serve", "--address", "127.0.0.1", "--port", "0")
	c, err := h.r.Start(ctx, repo, args, env)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		cancel()
		c.Wait()
		c.Stdout.Close()
		c.Stderr.Close()
	})
	log := &syncBuffer{}
	go io.Copy(log, c.Stderr)
	port := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(io.TeeReader(c.Stdout, log))
		for sc.Scan() {
			if m := boundPort.FindStringSubmatch(sc.Text()); m != nil {
				port <- m[1]
				break
			}
		}
		io.Copy(log, c.Stdout)
	}()
	select {
	case p := <-port:
		return "http://127.0.0.1:" + p + "/", log
	case <-time.After(hg.StartupTimeout):
		h.t.Fatal("hg serve did not report its port")
		return "", nil
	}
}

func checkRealHooks(t *testing.T, exe, gogs string) {
	root := filepath.Join(t.TempDir(), "hooks é")
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	h := &hgHarness{t: t, r: r, ctx: ctx}

	// The authorizer allows the first allowCalls checks of a session; -1
	// allows all. Allowing exactly one lets pretxnopen pass so that the
	// transaction-close hooks are what must reject.
	var allowCalls, calls atomic.Int64
	authorize := func(context.Context, Grant) error {
		if n := allowCalls.Load(); n >= 0 && calls.Add(1) > n {
			return errors.New("write access withdrawn")
		}
		return nil
	}
	policy := func(n int64) { allowCalls.Store(n); calls.Store(0) }
	ctl, err := NewController(authorize, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close(ctx)

	reader, err := HookConfig(gogs, false)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := HookConfig(gogs, true)
	if err != nil {
		t.Fatal(err)
	}
	_, env, revoke, err := ctl.Issue(Grant{Principal: Principal{UserID: 1, KeyID: 1}, RepoID: 9, CanWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	_, otherRepo, revokeOther, err := ctl.Issue(Grant{Principal: Principal{UserID: 1, KeyID: 1}, RepoID: 10, CanWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	defer revokeOther()
	otherRepo[EnvRepoID] = "9"

	dest := filepath.Join(root, "dest repo")
	h.must("", nil, nil, "init", dest)
	write := func(repo, name string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(dest, "seed")
	h.must(dest, nil, nil, "commit", "-A", "-m", "seed", "-u", "seed")

	// Local transactions: commit, bookmark and phase changes.
	write(dest, "local")
	commit := []string{"commit", "-A", "-m", "local", "-u", "tester"}
	policy(-1)
	h.denied("reader commit", dest, reader, env, commit...)
	h.denied("reader bookmark", dest, reader, env, "bookmark", "-r", "0", "readerbook")
	h.denied("reader phase", dest, reader, env, "phase", "--public", "-r", "0")
	h.denied("writer without capability", dest, writer, nil, commit...)
	h.denied("writer with another repository's capability", dest, writer, otherRepo, commit...)
	policy(0)
	h.denied("revoked writer", dest, writer, env, commit...)
	policy(1)
	h.denied("writer revoked before commit", dest, writer, env, commit...)
	policy(1)
	h.denied("writer revoked before bookmark move", dest, writer, env, "bookmark", "-r", "0", "latebook")
	policy(1)
	h.denied("writer revoked before phase move", dest, writer, env, "phase", "--public", "-r", "0")

	policy(-1)
	before := h.snapshot(dest)
	h.must(dest, writer, env, commit...)
	h.must(dest, writer, env, "bookmark", "-r", ".", "main")
	h.must(dest, writer, env, "phase", "--public", "-r", ".")
	if h.snapshot(dest) == before {
		t.Fatal("authorized writer changed nothing")
	}

	// Pushes into an HTTP worker for dest from a clone.
	src := filepath.Join(root, "src repo")
	h.must("", nil, nil, "clone", dest, src)
	write(src, "pushed")
	h.must(src, nil, nil, "commit", "-A", "-m", "pushed", "-u", "tester")
	h.must(src, nil, nil, "bookmark", "-r", ".", "feature")
	push := func(url string, extra ...string) []string {
		// The managed config sends every HTTP request to a dead proxy; clients
		// in these tests must reach the local server directly.
		return append([]string{"push", "--config", "http_proxy.host=", "-B", "feature", url}, extra...)
	}
	bundle1 := []string{"--config", "devel.legacy.exchange=bundle1"}

	readerURL, readerLog := h.serve(dest, reader, nil)
	h.deniedPush("reader push", readerLog, dest, src, push(readerURL))
	h.deniedPush("reader bundle1 push", readerLog, dest, src, push(readerURL, bundle1...))

	writerURL, writerLog := h.serve(dest, writer, env)
	policy(0)
	h.deniedPush("revoked writer push", writerLog, dest, src, push(writerURL))
	policy(1)
	h.deniedPush("writer revoked before push commit", writerLog, dest, src, push(writerURL))
	policy(1)
	h.deniedPush("writer revoked before bundle1 push commit", writerLog, dest, src, push(writerURL, bundle1...))

	policy(-1)
	h.must(src, nil, nil, push(writerURL)...)
	tip := h.must(src, nil, nil, "log", "-r", "feature", "-T", "{node}")
	if got := h.must(dest, nil, nil, "log", "-r", "feature", "-T", "{node}"); got != tip {
		t.Fatalf("authorized push: dest feature = %q, want %q", got, tip)
	}

	// Bookmark-only push: no changegroup, only bookmark updates.
	h.must(src, nil, nil, "bookmark", "-r", "0", "-f", "feature")
	policy(1)
	h.deniedPush("writer revoked before bookmark-only push", writerLog, dest, src, push(writerURL))
	policy(1)
	h.deniedPush("writer revoked before bundle1 pushkey", writerLog, dest, src, push(writerURL, bundle1...))

	revoke()
	policy(-1)
	h.deniedPush("revoked session push", writerLog, dest, src, push(writerURL))
	ctl.Close(ctx)
	write(dest, "after close")
	h.denied("writer with controller gone", dest, writer, env, commit...)
}

// deniedPush pushes from src into the server whose output is log, and asserts
// the Gogs hook in the server rejected it and dest is unchanged..
func (h *hgHarness) deniedPush(name string, log *syncBuffer, dest, src string, args []string) {
	h.t.Helper()
	before := h.snapshot(dest)
	rejected := len(hookRejected.FindAllString(log.String(), -1))
	if _, err := h.hg(src, nil, nil, args...); err == nil {
		h.t.Errorf("%s: hg %v succeeded", name, args)
	} else if !hookRejected.MatchString(err.Error()) && len(hookRejected.FindAllString(log.String(), -1)) == rejected {
		h.t.Errorf("%s: not rejected by the Gogs hook: %v\nserver:\n%s", name, err, log)
	}
	if after := h.snapshot(dest); after != before {
		h.t.Errorf("%s: repository changed\nbefore:\n%s\nafter:\n%s", name, before, after)
	}
	h.must(dest, nil, nil, "verify", "-q")
}

// syncBuffer collects a server's output while tests read it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
