// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package ssh

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

// TestMain doubles as a fake "hg serve --stdio": the runner invokes the test
// binary with Mercurial arguments instead of -test.* flags.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(fakeServe(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeServe records its start beside the managed configuration, reports its
// repository on stderr, then obeys stdin lines: "echo <text>", "whoami",
// "exit <code>" or "block".
func fakeServe(args []string) int {
	// Mercurial serves stdio only for exactly this argument vector.
	if len(args) != 4 || args[0] != "-R" || args[2] != "serve" || args[3] != "--stdio" {
		fmt.Fprintf(os.Stderr, "unexpected arguments %q", args)
		return 99
	}
	f, err := os.CreateTemp(filepath.Join(filepath.Dir(filepath.Dir(filepath.SplitList(os.Getenv("HGRCPATH"))[0])), "starts"), "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 98
	}
	f.Close()
	mode := "none"
	if paths := filepath.SplitList(os.Getenv("HGRCPATH")); len(paths) == 2 {
		rc, _ := os.ReadFile(paths[1])
		for _, line := range strings.Split(string(rc), "\n") {
			if strings.HasPrefix(line, "pretxnopen = ") {
				words := strings.Fields(line)
				mode = words[len(words)-2]
			}
		}
	}
	fmt.Fprintf(os.Stderr, "repo=%s\n", filepath.Base(args[1]))
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		cmd, arg, _ := strings.Cut(sc.Text(), " ")
		switch cmd {
		case "echo":
			fmt.Println(arg)
		case "whoami":
			fmt.Printf("hooks=%s capability=%s\n", mode, os.Getenv(hgbridge.EnvCapability))
		case "exit":
			var code int
			fmt.Sscan(arg, &code)
			return code
		case "block":
			fmt.Println("blocked")
			time.Sleep(time.Hour)
		}
	}
	return 0
}

type testServer struct {
	t      *testing.T
	s      *Server
	ctl    *hgbridge.Controller
	addr   string
	dir    string
	served chan error

	writer, reader, other ssh.Signer

	mu       sync.Mutex
	resolved []hgbridge.Principal
	checked  []hgbridge.Grant
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// newTestServer serves owner/repo (ID 9) to three keys: user 1 and user 3
// may write, user 2 may only read. A nonzero execTimeout replaces the
// server's before it starts serving.
func newTestServer(t *testing.T, execTimeout time.Duration) *testServer {
	t.Helper()
	ts := &testServer{t: t, dir: t.TempDir(), writer: newSigner(t), reader: newSigner(t), other: newSigner(t)}
	exe, _ := os.Executable()
	runner, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(ts.dir, "config", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"starts", "9"} {
		if err := os.Mkdir(filepath.Join(ts.dir, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ts.ctl, err = hgbridge.NewController(func(_ context.Context, g hgbridge.Grant) error {
		ts.mu.Lock()
		ts.checked = append(ts.checked, g)
		ts.mu.Unlock()
		return nil
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.ctl.Close(context.Background()) })
	bridge, err := hgbridge.NewStdioBridge(runner, ts.ctl, exe, filepath.Join(ts.dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Close)

	keys := map[string]hgbridge.Principal{
		string(ts.writer.PublicKey().Marshal()): {UserID: 1, KeyID: 11},
		string(ts.reader.PublicKey().Marshal()): {UserID: 2, KeyID: 12},
		string(ts.other.PublicKey().Marshal()):  {UserID: 3, KeyID: 13},
	}
	resolveKey := func(_ context.Context, k ssh.PublicKey) (hgbridge.Principal, error) {
		p, ok := keys[string(k.Marshal())]
		if !ok {
			return p, errors.New("unknown key")
		}
		return p, nil
	}
	resolveTarget := func(_ context.Context, p hgbridge.Principal, owner, repo string) (hgbridge.Target, hgbridge.Grant, error) {
		ts.mu.Lock()
		ts.resolved = append(ts.resolved, p)
		ts.mu.Unlock()
		if owner != "owner" || repo != "repo" {
			return hgbridge.Target{}, hgbridge.Grant{}, errors.New("no such repository")
		}
		return hgbridge.Target{RepoID: 9, Path: filepath.Join(ts.dir, "9")},
			hgbridge.Grant{Principal: p, RepoID: 9, CanWrite: p.UserID != 2}, nil
	}
	ts.s = NewServer(newSigner(t), resolveKey, resolveTarget, bridge)
	if execTimeout > 0 {
		ts.s.execTimeout = execTimeout
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ts.addr = ln.Addr().String()
	ts.served = make(chan error, 1)
	go func() { ts.served <- ts.s.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		ts.s.Shutdown(ctx)
	})
	return ts
}

func (ts *testServer) dial(user string, key ssh.Signer) (*ssh.Client, error) {
	return ssh.Dial("tcp", ts.addr, &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

func (ts *testServer) session(key ssh.Signer) (*ssh.Client, *ssh.Session) {
	ts.t.Helper()
	c, err := ts.dial("hg", key)
	if err != nil {
		ts.t.Fatal(err)
	}
	s, err := c.NewSession()
	if err != nil {
		ts.t.Fatal(err)
	}
	return c, s
}

func (ts *testServer) starts() int {
	ts.t.Helper()
	entries, err := os.ReadDir(filepath.Join(ts.dir, "starts"))
	if err != nil {
		ts.t.Fatal(err)
	}
	return len(entries)
}

func (ts *testServer) liveConns() int {
	ts.s.mu.Lock()
	defer ts.s.mu.Unlock()
	return len(ts.s.conns)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

const serveCommand = "hg -R owner/repo serve --stdio"

func TestSSHRejectsArbitraryExecution(t *testing.T) {
	ts := newTestServer(t, 0)
	for _, cmd := range append(rejectedCommands, "hg -R owner/missing serve --stdio") {
		c, s := ts.session(ts.writer)
		var stdout, stderr bytes.Buffer
		s.Stdout, s.Stderr = &stdout, &stderr
		err := s.Run(cmd)
		var exit *ssh.ExitError
		if !errors.As(err, &exit) || exit.ExitStatus() != 1 {
			t.Errorf("%q: %v, want exit status 1", cmd, err)
		}
		if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "gogs: ") {
			t.Errorf("%q: stdout %q, stderr %q", cmd, stdout.String(), stderr.String())
		}
		c.Close()
	}
	if n := ts.starts(); n != 0 {
		t.Fatalf("rejected commands started %d workers", n)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.resolved) != 1 {
		t.Errorf("only the valid command should be resolved, got %d", len(ts.resolved))
	}
}

func TestSSHRejectsShellPTYForwardingEnv(t *testing.T) {
	ts := newTestServer(t, 0)
	if _, err := ts.dial("git", ts.writer); err == nil {
		t.Error("username other than hg accepted")
	}
	if _, err := ts.dial("hg", newSigner(t)); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := ssh.Dial("tcp", ts.addr, &ssh.ClientConfig{
		User:            "hg",
		Auth:            []ssh.AuthMethod{ssh.Password("secret"), ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}); err == nil {
		t.Error("authentication without a key accepted")
	}

	c, s := ts.session(ts.writer)
	defer c.Close()
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
		t.Error("PTY accepted")
	}
	if err := s.Setenv("HGRCPATH", "/tmp/evil"); err == nil {
		t.Error("environment accepted")
	}
	if err := s.RequestSubsystem("sftp"); err == nil {
		t.Error("subsystem accepted")
	}
	if err := s.Shell(); err == nil {
		t.Error("shell accepted")
	}
	if ok, _, err := c.SendRequest("tcpip-forward", true, ssh.Marshal(struct {
		Addr string
		Port uint32
	}{"127.0.0.1", 0})); ok || err != nil {
		t.Errorf("remote forwarding: ok %v, %v", ok, err)
	}
	if conn, err := c.Dial("tcp", ts.addr); err == nil {
		conn.Close()
		t.Error("local forwarding accepted")
	}
	if _, _, err := c.OpenChannel("x11", nil); err == nil {
		t.Error("x11 channel accepted")
	}
	if _, err := c.NewSession(); err == nil {
		t.Error("second session on one connection accepted")
	}
	// The rejected requests leave the session usable for its one command.
	var stdout bytes.Buffer
	s.Stdin, s.Stdout = strings.NewReader("echo ok\n"), &stdout
	if err := s.Run(serveCommand); err != nil || stdout.String() != "ok\n" {
		t.Errorf("serve after rejected requests: %v, %q", err, stdout.String())
	}
	if n := ts.starts(); n != 1 {
		t.Errorf("%d workers started, want 1", n)
	}

	// A connection that never asks for a command is closed.
	ts = newTestServer(t, 100*time.Millisecond)
	c, err := ts.dial("hg", ts.writer)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { c.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Error("connection without a command stayed open")
	}
}

func TestSSHReturnsChildStatus(t *testing.T) {
	ts := newTestServer(t, 0)
	for _, tc := range []struct {
		input  string
		status int
		stdout string
	}{
		{"echo first\n", 0, "first\n"},
		{"echo partial\nexit 3\n", 3, "partial\n"},
	} {
		c, s := ts.session(ts.reader)
		var stdout, stderr bytes.Buffer
		s.Stdin, s.Stdout, s.Stderr = strings.NewReader(tc.input), &stdout, &stderr
		err := s.Run(serveCommand)
		var exit *ssh.ExitError
		if tc.status == 0 && err != nil || tc.status != 0 && (!errors.As(err, &exit) || exit.ExitStatus() != tc.status) {
			t.Errorf("%q: %v, want exit status %d", tc.input, err, tc.status)
		}
		if stdout.String() != tc.stdout || stderr.String() != "repo=9\n" {
			t.Errorf("%q: stdout %q, stderr %q", tc.input, stdout.String(), stderr.String())
		}
		c.Close()
	}
}

func TestSSHSeparatesConcurrentUsers(t *testing.T) {
	ts := newTestServer(t, 0)
	type user struct {
		key   ssh.Signer
		id    int64
		write bool
	}
	users := []user{{ts.writer, 1, true}, {ts.reader, 2, false}, {ts.other, 3, true}}
	replies := make([]string, len(users))
	var stdins []io.WriteCloser
	var waits []func() error
	for i, u := range users {
		c, s := ts.session(u.key)
		defer c.Close()
		in, _ := s.StdinPipe()
		out, _ := s.StdoutPipe()
		if err := s.Start(serveCommand); err != nil {
			t.Fatal(err)
		}
		io.WriteString(in, "whoami\n")
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil {
			t.Fatalf("user %d: %v", u.id, err)
		}
		replies[i] = strings.TrimSpace(line)
		stdins, waits = append(stdins, in), append(waits, s.Wait)
	}

	// All three workers are running at once.
	caps := map[string]int64{}
	for i, u := range users {
		mode, capability, _ := strings.Cut(strings.TrimPrefix(replies[i], "hooks="), " capability=")
		if want := map[bool]string{true: "check", false: "deny"}[u.write]; mode != want || (capability != "") != u.write {
			t.Errorf("user %d: %q", u.id, replies[i])
		}
		if capability == "" {
			continue
		}
		if err := ts.ctl.Check(context.Background(), capability, 9, "pretxnopen"); err != nil {
			t.Errorf("user %d: capability rejected: %v", u.id, err)
		}
		caps[capability] = u.id
	}
	if len(caps) != 2 {
		t.Errorf("writers share a capability: %v", replies)
	}
	ts.mu.Lock()
	for i, g := range ts.checked {
		if want := []int64{1, 3}[i]; g.Principal.UserID != want {
			t.Errorf("capability %d checked as user %d, want %d", i, g.Principal.UserID, want)
		}
	}
	for i, p := range ts.resolved {
		if p.UserID != users[i].id || p.KeyID != users[i].id+10 {
			t.Errorf("connection %d resolved as %+v", i, p)
		}
	}
	ts.mu.Unlock()

	for i := range users {
		stdins[i].Close()
		if err := waits[i](); err != nil {
			t.Errorf("user %d: %v", users[i].id, err)
		}
	}
	for c := range caps {
		if err := ts.ctl.Check(context.Background(), c, 9, "pretxnopen"); err == nil {
			t.Error("capability outlived its session")
		}
	}
}

// blocked starts a worker that reports it is blocked and keeps stdin open.
func (ts *testServer) blocked() *ssh.Client {
	ts.t.Helper()
	c, s := ts.session(ts.writer)
	in, _ := s.StdinPipe()
	out, _ := s.StdoutPipe()
	if err := s.Start(serveCommand); err != nil {
		ts.t.Fatal(err)
	}
	io.WriteString(in, "block\n")
	if line, err := bufio.NewReader(out).ReadString('\n'); line != "blocked\n" {
		ts.t.Fatalf("worker did not block: %q, %v", line, err)
	}
	return c
}

func TestSSHCancelBlockedChannel(t *testing.T) {
	ts := newTestServer(t, 0)
	ts.blocked().Close()
	waitFor(t, "the cancelled session's worker to be reaped", func() bool { return ts.liveConns() == 0 })

	// Closing only the channel ends the worker too.
	c, s := ts.session(ts.writer)
	defer c.Close()
	in, _ := s.StdinPipe()
	out, _ := s.StdoutPipe()
	if err := s.Start(serveCommand); err != nil {
		t.Fatal(err)
	}
	io.WriteString(in, "block\n")
	bufio.NewReader(out).ReadString('\n')
	s.Close()
	waitFor(t, "the closed channel's worker to be reaped", func() bool { return ts.liveConns() == 0 })
}

func TestSSHShutdown(t *testing.T) {
	ts := newTestServer(t, 0)
	defer ts.blocked().Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := ts.s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown with a blocked session: %v", err)
	}
	if n := ts.liveConns(); n != 0 {
		t.Errorf("%d connections survive Shutdown", n)
	}
	if err := <-ts.served; !errors.Is(err, ErrServerClosed) {
		t.Errorf("Serve: %v", err)
	}
	if _, err := ts.dial("hg", ts.writer); err == nil {
		t.Error("connection accepted after Shutdown")
	}
}
