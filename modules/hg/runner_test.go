// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a fake Mercurial executable: the runner invokes the test
// binary with hg-style arguments instead of -test.* flags.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(fakeHg(os.Args[1:]))
	}
	os.Exit(m.Run())
}

const fakeHooksHelp = `hooks
-----
    "prepushkey"
    "pretxnopen"
    "pretxnclose"
    "pretxnclose-bookmark"
    "pretxnclose-phase"
`

func fakeHg(args []string) int {
	if len(args) > 2 && args[0] == "--repository" {
		args = args[2:]
	}
	// Tests steer the probes through files beside the managed configuration.
	fixture := func(name, def string) string {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("HGRCPATH")), name))
		if err != nil {
			return def
		}
		return string(b)
	}
	switch args[0] {
	case "env":
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
	case "spew":
		var errN, outN int
		fmt.Sscan(args[1], &errN)
		fmt.Sscan(args[2], &outN)
		os.Stderr.Write(bytes.Repeat([]byte("e"), errN))
		os.Stdout.Write(bytes.Repeat([]byte("o"), outN))
	case "fail":
		fmt.Fprint(os.Stderr, "abort: boom")
		return 3
	case "cat":
		io.Copy(os.Stdout, os.Stdin)
	case "sleep":
		time.Sleep(time.Minute)
	case "tree":
		// The grandchild shares stdout, so stdout reaches EOF only once it dies.
		gc := exec.Command(os.Args[0], "sleep")
		gc.Stdout = os.Stdout
		if err := gc.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("ready")
		if len(args) > 1 && args[1] == "exit" {
			return 0
		}
		time.Sleep(time.Minute)
	case "version":
		v := fixture("fake-version", `[{"extensions": [], "ver": "7.2.4"}]`)
		if v == "EXIT" {
			fmt.Fprintln(os.Stderr, "hg version: option -T not recognized")
			return 255
		}
		fmt.Print(v)
	case "help":
		fmt.Print(fixture("fake-help", fakeHooksHelp))
	case "init":
		return exitCode(os.MkdirAll(filepath.Join(args[1], ".hg"), 0700))
	case "log":
		fmt.Print(`[{"node": "0000000000000000000000000000000000000000", "rev": -1}]`)
	default:
		fmt.Fprintf(os.Stderr, "fake hg: unknown command %q\n", args[0])
		return 255
	}
	return 0
}

func exitCode(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func newTestRunner(t *testing.T, cfg Config) *Runner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Executable = exe
	cfg.ConfigPath = filepath.Join(t.TempDir(), "managed", "hgrc")
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readEnv(t *testing.T, r *Runner, repoPath string, env map[string]string) map[string]string {
	t.Helper()
	c, err := r.Start(context.Background(), repoPath, []string{"env"}, env)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	sc := bufio.NewScanner(c.Stdout)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && k != "" {
			vars[strings.ToUpper(k)] = v
		}
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	return vars
}

func TestRunnerIgnoresAmbientConfig(t *testing.T) {
	ambient := []string{
		"HGRCPATH", "HGPLAIN", "HGPLAINEXCEPT", "HGRCSKIPREPO", "HGUSER", "HGENCODING",
		"HGMODULEPOLICY", "HGEDITOR", "CHGHG", "PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP",
		"PYTHONUSERBASE", "PYTHONNOUSERSITE", "GOGS_HG_AMBIENT",
	}
	for _, k := range ambient {
		t.Setenv(k, "injected")
	}
	r := newTestRunner(t, Config{})

	vars := readEnv(t, r, "", map[string]string{"GOGS_HG_ACTOR": "42"})
	want := map[string]string{
		"HGRCPATH":         r.cfg.ConfigPath,
		"HGPLAIN":          "1",
		"HGENCODING":       localEncoding(),
		"HGRCSKIPREPO":     "1",
		"PYTHONNOUSERSITE": "1",
		"GOGS_HG_ACTOR":    "42",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
	for _, k := range ambient {
		if _, ok := want[k]; !ok && vars[k] != "" {
			t.Errorf("ambient %s leaked into Mercurial environment: %q", k, vars[k])
		}
	}
	if vars["PATH"] == "" {
		t.Error("PATH was not preserved")
	}
	if runtime.GOOS == "windows" && vars["SYSTEMROOT"] == "" {
		t.Error("SystemRoot was not preserved")
	}

	// A repository's own hgrc carries its managed hooks; neither the runner nor
	// the ambient environment may skip it.
	if v, ok := readEnv(t, r, t.TempDir(), nil)["HGRCSKIPREPO"]; ok {
		t.Errorf("HGRCSKIPREPO = %q for a repository command", v)
	}

	for _, k := range []string{"HGRCPATH", "PYTHONPATH", "FOO", "GOGS_HG_X=Y"} {
		if _, err := r.Start(context.Background(), "", []string{"env"}, map[string]string{k: "x"}); err == nil {
			t.Errorf("caller environment %q was accepted", k)
		}
	}

	rc, err := os.ReadFile(r.cfg.ConfigPath)
	if err != nil || !strings.Contains(string(rc), "interactive = false") {
		t.Errorf("managed configuration not written: %q, %v", rc, err)
	}
}

func TestRunnerBoundsOutput(t *testing.T) {
	r := newTestRunner(t, Config{})

	// Stderr far beyond a pipe buffer must be drained, not deadlock the child.
	var out bytes.Buffer
	if err := r.Run(context.Background(), "", []string{"spew", "1048576", "10"}, nil, &out); err != nil {
		t.Fatalf("drained run: %v", err)
	}
	if out.String() != "oooooooooo" {
		t.Fatalf("output = %q", out.String())
	}

	out.Reset()
	start := time.Now()
	err := r.Run(context.Background(), "", []string{"spew", "1048576", fmt.Sprint(3 * MaxCommandOutput)}, nil, &out)
	if !errors.Is(err, ErrOutputLimit) || out.Len() != MaxCommandOutput {
		t.Fatalf("unbounded output: %v, %d bytes", err, out.Len())
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("bounded run took %v", time.Since(start))
	}

	err = r.Run(context.Background(), "", []string{"fail"}, nil, &out)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || !strings.Contains(err.Error(), "abort: boom") {
		t.Fatalf("failure lost exit status or stderr: %v", err)
	}

	out.Reset()
	if err := r.Run(context.Background(), "", []string{"cat"}, strings.NewReader("hello"), &out); err != nil || out.String() != "hello" {
		t.Fatalf("input not delivered: %q, %v", out.String(), err)
	}
}

func TestRunnerCheckVersions(t *testing.T) {
	cases := []struct {
		version, help string
		ok            bool
	}{
		{`[{"ver": "7.2.4"}]`, "", true},
		{`[{"ver": "5.0.2"}]`, "", true},
		{`[{"ver": "5.0"}]`, "", true},
		{`[{"ver": "6.9.1+12-abcdef"}]`, "", true},
		{`[{"ver": "4.9"}]`, "", false},
		{`[{"ver": "4.9.9"}]`, "", false},
		{`[{"ver": "x.y"}]`, "", false},
		{`garbage`, "", false},
		{`[]`, "", false},
		{"EXIT", "", false},
		{`[{"ver": "7.2.4"}]`, `"pretxnopen" "pretxnclose" "prepushkey"`, false},
	}
	for _, c := range cases {
		r := newTestRunner(t, Config{})
		dir := filepath.Dir(r.cfg.ConfigPath)
		os.WriteFile(filepath.Join(dir, "fake-version"), []byte(c.version), 0600)
		if c.help != "" {
			os.WriteFile(filepath.Join(dir, "fake-help"), []byte(c.help), 0600)
		}
		err := r.Check(context.Background())
		if (err == nil) != c.ok {
			t.Errorf("Check(%s, %q) = %v, want ok=%v", c.version, c.help, err, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "Mercurial") {
			t.Errorf("diagnostic does not name Mercurial: %v", err)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for v, tested := range map[string]bool{"5.0.2": true, "7.2.4": true, "5.0": false, "6.9.1": false, "7.2.4+5-abc": false} {
		got, err := parseVersion(v)
		if err != nil || got != tested {
			t.Errorf("parseVersion(%q) = %v, %v; want tested=%v", v, got, err, tested)
		}
	}
}

func TestNewRunnerValidates(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	enclosing := t.TempDir()
	if err := os.Mkdir(filepath.Join(enclosing, ".hg"), 0700); err != nil {
		t.Fatal(err)
	}
	bad := []Config{
		{Executable: exe, ConfigPath: filepath.Join(enclosing, "data", "hg", "hgrc")},
		{Executable: "hg", ConfigPath: filepath.Join(dir, "hgrc")},
		{Executable: filepath.Join(dir, "missing-hg"), ConfigPath: filepath.Join(dir, "hgrc")},
		{Executable: exe, ConfigPath: "hgrc"},
		{Executable: exe, ConfigPath: filepath.Join(dir, "a"+string(os.PathListSeparator)+"b")},
		{Executable: exe, ConfigPath: filepath.Join(dir, "hgrc"), MaxProcesses: -1},
	}
	for _, cfg := range bad {
		if _, err := NewRunner(cfg); err == nil {
			t.Errorf("NewRunner(%+v) accepted invalid configuration", cfg)
		}
	}
	r, err := NewRunner(Config{Executable: exe, ConfigPath: filepath.Join(dir, "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	if cap(r.slots) != DefaultMaxProcesses || r.cfg.SessionTimeout != DefaultSessionTimeout ||
		r.queueTimeout != QueueTimeout || r.idleTimeout != TransferIdleTimeout {
		t.Errorf("defaults not applied: %+v", r)
	}
	if _, err := r.Start(context.Background(), "relative", []string{"env"}, nil); err == nil {
		t.Error("relative repository path accepted")
	}
	// No ANSI code page holds an emoji; this assumes the host's is not UTF-8.
	if _, err := r.Start(context.Background(), filepath.Join(dir, "😀"), []string{"env"}, nil); (err == nil) == (runtime.GOOS == "windows") {
		t.Errorf("non-ANSI repository path on %s: %v", runtime.GOOS, err)
	}
}
