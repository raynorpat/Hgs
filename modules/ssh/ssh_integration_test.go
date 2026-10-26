// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

// TestSSHRecorder is the ssh command of TestNativeClientCommandShape: run
// with "-- <record file> <ssh arguments>", it records the arguments and
// fails the connection. In a normal test run it has no arguments and does
// nothing.
func TestSSHRecorder(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 {
		return
	}
	b, _ := json.Marshal(args[1:])
	os.WriteFile(args[0], b, 0600)
	os.Exit(1)
}

func hgPath(t *testing.T) string {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	return exe
}

type hgClient struct {
	t   *testing.T
	r   *hg.Runner
	ctx context.Context
}

func newHgClient(t *testing.T, exe, dir string, maxProcesses int) *hgClient {
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(dir, "hgrc"), MaxProcesses: maxProcesses})
	if err != nil {
		t.Fatal(err)
	}
	return &hgClient{t: t, r: r, ctx: context.Background()}
}

// hg runs one command and returns stdout, or an error carrying stdout and stderr.
func (h *hgClient) hg(ctx context.Context, repo string, args ...string) (string, error) {
	c, err := h.r.Start(ctx, repo, args, nil)
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&stderr, c.Stderr); close(done) }()
	out, _ := io.ReadAll(c.Stdout)
	<-done
	if err = c.Stop(ctx); err != nil {
		return string(out), fmt.Errorf("hg %v: %v: %s%s", args, err, out, stderr.Bytes())
	}
	return string(out), nil
}

func (h *hgClient) must(repo string, args ...string) string {
	h.t.Helper()
	out, err := h.hg(h.ctx, repo, args...)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *hgClient) snapshot(repo string) string {
	h.t.Helper()
	return h.must(repo, "log", "--hidden", "-T", "{rev}:{node} {phase} {bookmarks} {files}\n") +
		h.must(repo, "bookmarks", "-T", "{bookmark}={node}\n")
}

// sshCommand returns ui.ssh for OpenSSH using only the given key and host
// key file, never the operator's configuration or agent.
func sshCommand(t *testing.T, key, knownHosts string) []string {
	exe, err := exec.LookPath("ssh")
	var env []string
	if runtime.GOOS == "windows" {
		// Windows OpenSSH, not an MSYS build that happens to be on PATH. It
		// exits silently without ProgramData, which the runner strips.
		exe = filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe")
		_, err = os.Stat(exe)
		env = []string{"--config", "sshenv.PROGRAMDATA=" + os.Getenv("ProgramData")}
	}
	if err != nil {
		t.Fatalf("OpenSSH client for integration tests: %v", err)
	}
	return append(env, "--config", fmt.Sprintf(`ui.ssh="%s" -F none -i "%s" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes `+
		`-o StrictHostKeyChecking=yes -o UserKnownHostsFile="%s"`,
		filepath.ToSlash(exe), filepath.ToSlash(key), filepath.ToSlash(knownHosts)))
}

// TestNativeClientCommandShape records the remote command native clients
// ask ssh to run and checks ParseCommand accepts exactly that.
func TestNativeClientCommandShape(t *testing.T) {
	check := func(t *testing.T, exe string, quotesRemotecmd bool) {
		dir := t.TempDir()
		client := newHgClient(t, exe, filepath.Join(dir, "client"), 0)
		self, _ := os.Executable()
		record := filepath.Join(dir, "record.json")
		recorder := fmt.Sprintf(`"%s" -test.run=TestSSHRecorder -- "%s"`, self, record)
		for _, tc := range []struct {
			remotecmd, want string
		}{
			{"", "hg -R owner/repo serve --stdio"},
			{`C:\Program Files\Mercurial\hg.exe`, `'C:\Program Files\Mercurial\hg.exe' -R owner/repo serve --stdio`},
		} {
			// 2.0.x sends ui.remotecmd unquoted.
			if tc.remotecmd != "" && !quotesRemotecmd {
				continue
			}
			os.Remove(record)
			args := []string{"clone", "--config", "ui.ssh=" + recorder}
			if tc.remotecmd != "" {
				args = append(args, "--config", "ui.remotecmd="+tc.remotecmd)
			}
			if _, err := client.hg(client.ctx, "", append(args, "ssh://hg@127.0.0.1:2222/owner/repo", filepath.Join(dir, "dest"))...); err == nil {
				t.Fatal("clone through the recorder succeeded")
			}
			b, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("recorder did not run: %v", err)
			}
			var argv []string
			json.Unmarshal(b, &argv)
			// OpenSSH sends the words after the destination joined by spaces.
			for i, a := range argv {
				if a == "hg@127.0.0.1" {
					argv = argv[i+1:]
					break
				}
			}
			got := strings.Join(argv, " ")
			t.Logf("%s on %s, remotecmd %q: ssh argv %s; remote command %q", exe, runtime.GOOS, tc.remotecmd, b, got)
			if got != tc.want {
				t.Errorf("remote command %q, want %q", got, tc.want)
			}
			if _, _, err := ParseCommand(got); err != nil {
				t.Errorf("ParseCommand(%q): %v", got, err)
			}
		}
	}
	t.Run("current client", func(t *testing.T) { check(t, hgPath(t), true) })
	if old := os.Getenv("GOGS_TEST_HG_OLD_CLIENT"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_CLIENT (Mercurial 2.0.x client) not set")
	} else {
		t.Run("old client", func(t *testing.T) { check(t, old, false) })
	}
}

func TestSSHNativeClients(t *testing.T) {
	exe := hgPath(t)
	gogs := buildGogs(t)
	t.Run("current server, current client", func(t *testing.T) { checkSSH(t, exe, exe, gogs) })
	if old := os.Getenv("GOGS_TEST_HG_OLD_CLIENT"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_CLIENT (Mercurial 2.0.x client) not set")
	} else {
		t.Run("current server, old client", func(t *testing.T) { checkSSH(t, exe, old, gogs) })
	}
	if old := os.Getenv("GOGS_TEST_HG_OLD_SERVER"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_SERVER (Mercurial 5.0.x) not set")
	} else {
		t.Run("old server, current client", func(t *testing.T) { checkSSH(t, old, exe, gogs) })
	}
}

func buildGogs(t *testing.T) string {
	name := "gogs"
	if runtime.GOOS == "windows" {
		name = "gogs.exe"
	}
	gogs := filepath.Join(t.TempDir(), "gogs bin", name)
	cmd := exec.Command("go", "build", "-o", gogs, "github.com/gogits/gogs")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gogs: %v\n%s", err, out)
	}
	return gogs
}

// writeKey saves an OpenSSH private key for the client and returns its signer.
func writeKey(t *testing.T, path string) ssh.Signer {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows OpenSSH ignores keys whose ACL grants anyone but the owner.
		u, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*"+u.Uid+":F").CombinedOutput(); err != nil {
			t.Fatalf("restrict key ACL: %v\n%s", err, out)
		}
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

var hookRejected = regexp.MustCompile(`gogs: write rejected|(pretxnopen|prepushkey|pretxnclose|pretxnclose-bookmark|pretxnclose-phase) hook exited with status 1`)

func checkSSH(t *testing.T, serverExe, clientExe, gogs string) {
	root := filepath.Join(t.TempDir(), "ssh é")
	keys := t.TempDir()
	server := newHgClient(t, serverExe, filepath.Join(root, "server"), 0)
	if err := server.r.Check(server.ctx); err != nil {
		t.Fatal(err)
	}
	client := newHgClient(t, clientExe, filepath.Join(root, "client"), 0)

	// The authorizer can hold a hook callback until the hook goes away.
	var mu sync.Mutex
	var block bool
	blocked := make(chan context.Context, 1)
	ctl, err := hgbridge.NewController(func(ctx context.Context, g hgbridge.Grant) error {
		if g.Principal.UserID != 1 || !g.CanWrite {
			t.Errorf("hook callback for %+v; only the writer has a capability", g)
		}
		mu.Lock()
		hold := block
		mu.Unlock()
		if hold {
			blocked <- ctx
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close(context.Background())
	bridge, err := hgbridge.NewStdioBridge(server.r, ctl, gogs, filepath.Join(root, "server"))
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	dest := filepath.Join(root, "repos", "9")
	server.must("", "init", dest)
	if err := os.WriteFile(filepath.Join(dest, "seed"), []byte("seed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server.must(dest, "commit", "-A", "-m", "seed", "-u", "seed")

	writerKey, readerKey := filepath.Join(keys, "writer"), filepath.Join(keys, "reader")
	principals := map[string]hgbridge.Principal{
		string(writeKey(t, writerKey).PublicKey().Marshal()): {UserID: 1, KeyID: 1},
		string(writeKey(t, readerKey).PublicKey().Marshal()): {UserID: 2, KeyID: 2},
	}
	var resolved []int64
	host := newSigner(t)
	s := NewServer(host, func(_ context.Context, k ssh.PublicKey) (hgbridge.Principal, error) {
		p, ok := principals[string(k.Marshal())]
		if !ok {
			return p, errors.New("unknown key")
		}
		return p, nil
	}, func(_ context.Context, p hgbridge.Principal, owner, repo string) (hgbridge.Target, hgbridge.Grant, error) {
		if owner != "owner" || repo != "repo" {
			return hgbridge.Target{}, hgbridge.Grant{}, errors.New("no such repository")
		}
		mu.Lock()
		resolved = append(resolved, p.UserID)
		mu.Unlock()
		return hgbridge.Target{RepoID: 9, Path: dest}, hgbridge.Grant{Principal: p, RepoID: 9, CanWrite: p.UserID == 1}, nil
	}, bridge)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ln)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()
	knownHosts := filepath.Join(keys, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(ln.Addr().String())}, host.PublicKey())
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	url := "ssh://hg@" + ln.Addr().String() + "/owner/repo"
	writer := sshCommand(t, writerKey, knownHosts)
	reader := sshCommand(t, readerKey, knownHosts)
	as := func(who []string, args ...string) []string { return append(append([]string(nil), who...), args...) }

	commit := func(repo, name string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		client.must(repo, "commit", "-A", "-m", name, "-u", "tester")
		return client.must(repo, "log", "-r", "tip", "-T", "{node}")
	}
	destTip := func() string { return server.must(dest, "log", "-r", "tip", "-T", "{node}") }

	writerClone := filepath.Join(root, "writer clone")
	client.must("", as(writer, "clone", url, writerClone)...)
	pushed := commit(writerClone, "writer change")
	client.must(writerClone, as(writer, "push", url)...)
	if got := destTip(); got != pushed {
		t.Fatalf("writer push: repository tip %s, want %s", got, pushed)
	}

	readerClone := filepath.Join(root, "reader clone")
	client.must("", as(reader, "clone", url, readerClone)...)
	if got := client.must(readerClone, "log", "-r", "tip", "-T", "{node}"); got != pushed {
		t.Fatalf("reader clone tip %s, want %s", got, pushed)
	}
	before := server.snapshot(dest)
	commit(readerClone, "reader change")
	client.must(readerClone, "bookmark", "-r", pushed, "reader-mark")
	for _, push := range [][]string{{"push", url}, {"push", "-r", pushed, "-B", "reader-mark", url}} {
		if out, err := client.hg(client.ctx, readerClone, as(reader, push...)...); err == nil {
			t.Errorf("reader %v succeeded: %s", push, out)
		} else if !hookRejected.MatchString(err.Error()) {
			t.Errorf("reader %v not rejected by the Gogs hook: %v", push, err)
		}
		if server.snapshot(dest) != before {
			t.Fatalf("reader %v changed the repository", push)
		}
	}

	// Two users at once, each in their own worker.
	pushed = commit(writerClone, "second change")
	client.must(writerClone, as(writer, "push", url)...)
	mu.Lock()
	resolved = nil
	mu.Unlock()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, who := range [][]string{writer, reader} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = client.hg(client.ctx, []string{writerClone, readerClone}[i], as(who, "pull", url)...)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent pull %d: %v", i, err)
		}
	}
	mu.Lock()
	if len(resolved) != 2 || resolved[0]+resolved[1] != 3 {
		t.Errorf("concurrent sessions resolved as users %v, want 1 and 2", resolved)
	}
	mu.Unlock()
	if got := client.must(readerClone, "log", "-r", pushed, "-T", "{node}"); got != pushed {
		t.Errorf("reader pull did not fetch %s", pushed)
	}
	server.must(dest, "verify", "-q")

	// A writer whose push is held inside a hook callback disconnects: the
	// hook and the worker go away and nothing changes.
	mu.Lock()
	block = true
	mu.Unlock()
	commit(writerClone, "cancelled change")
	before = server.snapshot(dest)
	ctx, cancel := context.WithCancel(context.Background())
	pushDone := make(chan error, 1)
	go func() {
		_, err := client.hg(ctx, writerClone, as(writer, "push", url)...)
		pushDone <- err
	}()
	var hook context.Context
	select {
	case hook = <-blocked:
	case err := <-pushDone:
		t.Fatalf("push ended before its hook called back: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("push did not reach its hook")
	}
	cancelled := time.Now()
	cancel()
	<-pushDone
	select {
	case <-hook.Done():
		if !errors.Is(hook.Err(), context.Canceled) {
			t.Errorf("hook callback ended by %v, not by the hook going away", hook.Err())
		}
	case <-time.After(hgbridge.CallbackTimeout):
		t.Error("hook still connected after the client went away")
	}
	t.Logf("hook gone %v after the client", time.Since(cancelled))
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		s.mu.Lock()
		n := len(s.conns)
		s.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancelled session's worker was not reaped")
		}
	}
	if server.snapshot(dest) != before {
		t.Error("cancelled push changed the repository")
	}
	// The killed worker leaves its repository lock behind for Mercurial to
	// break. Mercurial 7.2.4 on Windows crashes reading a lock under a
	// non-ASCII path, so verify is not run here.
	if _, err := os.Lstat(filepath.Join(dest, ".hg", "store", "lock")); err == nil {
		t.Log("cancelled push left a stale store lock, as expected after a kill")
	}
}
