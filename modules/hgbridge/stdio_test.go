// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

// TestMain doubles as a fake "hg serve --stdio": the runner invokes the test
// binary with Mercurial arguments instead of -test.* flags.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(fakeStdioServe(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeStdioServe reports how it was configured on stderr, then obeys stdin
// lines: "echo <text>", "exit <code>", "block", "silent <duration>" (no
// output for that long) or "callbacks <n> <interval>" (n hook callbacks that
// far apart, as a long transaction makes, with no output in between); the
// last two then print "done" and exit.
func fakeStdioServe(args []string) int {
	// Mercurial serves stdio only for exactly this argument vector.
	if len(args) != 4 || args[0] != "-R" || args[2] != "serve" || args[3] != "--stdio" {
		fmt.Fprintf(os.Stderr, "unexpected arguments %q", args)
		return 99
	}
	mode := sessionHookMode()
	fmt.Fprintf(os.Stderr, "repo=%s hooks=%s repoid=%s capability=%t\n",
		filepath.Base(args[1]), mode, os.Getenv(EnvRepoID), os.Getenv(EnvCapability) != "")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		cmd, arg, _ := strings.Cut(sc.Text(), " ")
		switch cmd {
		case "echo":
			fmt.Println(arg)
		case "exit":
			var code int
			fmt.Sscan(arg, &code)
			return code
		case "block":
			fmt.Println("blocked")
			time.Sleep(time.Hour)
		case "silent":
			d, _ := time.ParseDuration(arg)
			time.Sleep(d)
			fmt.Println("done")
			return 0
		case "callbacks":
			count, every, _ := strings.Cut(arg, " ")
			n, _ := strconv.Atoi(count)
			interval, _ := time.ParseDuration(every)
			for i := 0; i < n; i++ {
				time.Sleep(interval)
				if err := fakeHookCallback(); err != nil {
					fmt.Println(err)
					return 5
				}
			}
			fmt.Println("done")
			return 0
		}
	}
	return 0
}

// fakeHookCallback makes the callback a check hook makes.
func fakeHookCallback() error {
	id, _ := strconv.ParseInt(os.Getenv(EnvRepoID), 10, 64)
	body, _ := json.Marshal(HookRequest{Hook: "pretxnclose-phase", RepoID: id})
	req, _ := http.NewRequest(http.MethodPost, "http://"+os.Getenv(EnvControl)+ControlPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+os.Getenv(EnvCapability))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("callback denied: %s", resp.Status)
	}
	return nil
}

// newTestStdioBridge runs workers whose runner kills them after idle without
// activity; zero means the production limit.
func newTestStdioBridge(t *testing.T, idle time.Duration) (*StdioBridge, *Controller, Target) {
	t.Helper()
	exe, _ := os.Executable()
	dir := t.TempDir()
	runner, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(dir, "config", "hgrc"), IdleTimeout: idle})
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := NewController(func(context.Context, Grant) error { return nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctl.Close(context.Background()) })
	b, err := NewStdioBridge(runner, ctl, exe, filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	repo := filepath.Join(dir, "9")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	return b, ctl, Target{RepoID: 9, Path: repo}
}

func TestStdioBridgeAppliesGrant(t *testing.T) {
	b, ctl, target := newTestStdioBridge(t, 0)
	for _, canWrite := range []bool{false, true} {
		exe, _ := os.Executable()
		want, _ := HookConfig(exe, canWrite)
		rc, err := os.ReadFile(b.rc[canWrite])
		lines := strings.Split(strings.TrimSuffix(string(rc), "\n"), "\n")
		if err != nil || lines[0] != "[hooks]" || len(lines) != len(want)+1 {
			t.Fatalf("session rc for writers=%t: %q, %v", canWrite, rc, err)
		}
		for _, l := range lines[1:] {
			name, cmd, _ := strings.Cut(l, " = ")
			if want["hooks."+name] != cmd {
				t.Errorf("session rc line %q is not a hook from HookConfig", l)
			}
		}
	}
	for _, tc := range []struct {
		name  string
		grant Grant
		want  string
	}{
		{"reader", testReader, "repo=9 hooks=deny repoid= capability=false\n"},
		{"writer", testWriter, "repo=9 hooks=check repoid=9 capability=true\n"},
	} {
		var out, stderr bytes.Buffer
		code, err := b.ServeStdio(context.Background(), target, tc.grant, strings.NewReader("echo hello\n"), &out, &stderr)
		if err != nil || code != 0 {
			t.Fatalf("%s: ServeStdio = %d, %v; stderr %q", tc.name, code, err, stderr.String())
		}
		if out.String() != "hello\n" || stderr.String() != tc.want {
			t.Errorf("%s: stdout %q, stderr %q; want hello and %q", tc.name, out.String(), stderr.String(), tc.want)
		}
	}
	ctl.mu.Lock()
	live := len(ctl.grants)
	ctl.mu.Unlock()
	if live != 0 {
		t.Errorf("%d capabilities outlive their sessions", live)
	}
}

func TestStdioBridgeReturnsExitStatus(t *testing.T) {
	b, _, target := newTestStdioBridge(t, 0)
	var out, stderr bytes.Buffer
	code, err := b.ServeStdio(context.Background(), target, testReader, strings.NewReader("echo partial\nexit 3\n"), &out, &stderr)
	if err != nil || code != 3 {
		t.Fatalf("ServeStdio = %d, %v; want exit status 3", code, err)
	}
	if out.String() != "partial\n" {
		t.Errorf("stdout %q", out.String())
	}
	if _, err := b.ServeStdio(context.Background(), Target{RepoID: 8, Path: target.Path}, testReader, strings.NewReader(""), &out, &stderr); err == nil {
		t.Error("ServeStdio accepted a grant for another repository")
	}
}

// blockedSession starts a worker that blocks after reporting so, with its
// stdin held open, and returns ServeStdio's result channel.
func blockedSession(t *testing.T, ctx context.Context, b *StdioBridge, target Target) (<-chan error, func()) {
	t.Helper()
	in, feed := io.Pipe()
	out, outW := io.Pipe()
	result := make(chan error, 1)
	go func() {
		_, err := b.ServeStdio(ctx, target, testWriter, in, outW, io.Discard)
		outW.Close()
		result <- err
	}()
	go io.WriteString(feed, "block\n")
	line, err := bufio.NewReader(out).ReadString('\n')
	if line != "blocked\n" {
		t.Fatalf("worker did not block: %q, %v", line, err)
	}
	go io.Copy(io.Discard, out)
	return result, func() { feed.Close() }
}

func TestStdioBridgeCancelKillsWorker(t *testing.T) {
	b, _, target := newTestStdioBridge(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	result, release := blockedSession(t, ctx, b, target)
	defer release()
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Error("cancelled session reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the session did not end the worker")
	}

	result, release = blockedSession(t, context.Background(), b, target)
	defer release()
	b.Close()
	select {
	case <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not end the worker")
	}
	if _, err := b.ServeStdio(context.Background(), target, testReader, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Error("closed bridge started a worker")
	}
}

// A writer's transaction can keep stdio silent for minutes while Mercurial
// runs one hook per new changeset; its callbacks keep the worker alive, and
// only real silence ends it.
func TestStdioBridgeHookCallbacksAreActivity(t *testing.T) {
	const idle = time.Second
	b, _, target := newTestStdioBridge(t, idle)
	session := func(command string) (string, int, error) {
		in, feed := io.Pipe()
		defer feed.Close()
		go io.WriteString(feed, command+"\n")
		var out bytes.Buffer
		code, err := b.ServeStdio(context.Background(), target, testWriter, in, &out, io.Discard)
		return out.String(), code, err
	}

	// 3 s of stdio silence, a callback every 250 ms.
	if out, code, err := session("callbacks 12 250ms"); err != nil || code != 0 || out != "done\n" {
		t.Fatalf("worker making callbacks: %q, exit %d, %v; want it to finish", out, code, err)
	}
	if out, _, err := session("silent 3s"); !errors.Is(err, hg.ErrIdleTimeout) {
		t.Fatalf("silent worker: %q, %v; want ErrIdleTimeout", out, err)
	}
}

// sessionHookMode reads the pretxnopen hook's mode from the session rc
// appended to HGRCPATH.
func sessionHookMode() string {
	paths := filepath.SplitList(os.Getenv("HGRCPATH"))
	if len(paths) != 2 {
		return "none"
	}
	rc, err := os.ReadFile(paths[1])
	if err != nil {
		return "unreadable"
	}
	for _, line := range strings.Split(string(rc), "\n") {
		if strings.HasPrefix(line, "pretxnopen = ") {
			words := strings.Fields(line)
			return words[len(words)-2]
		}
	}
	return "none"
}
