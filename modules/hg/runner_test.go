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
	if len(args) > 2 && args[0] == "-R" {
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
	if args[0] == "argv" {
		for _, a := range os.Args[1:] {
			fmt.Println(a)
		}
		return 0
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
		if len(args) == 1 {
			io.Copy(os.Stdout, os.Stdin)
			break
		}
		// Browse reads: echo the pattern files, so tests see what was passed.
		recordFakeCall()
		var patterns []byte
		for _, a := range args {
			if f, ok := strings.CutPrefix(a, "listfile0:"); ok {
				b, _ := os.ReadFile(f)
				patterns = append(patterns, b...)
			}
		}
		switch {
		case bytes.HasSuffix(patterns, []byte("\x00path:missing\x00")):
			return 1
		case bytes.HasSuffix(patterns, []byte("\x00path:big\x00")):
			os.Stdout.Write(bytes.Repeat([]byte("x"), MaxCommandOutput+1))
		default:
			os.Stdout.Write(patterns)
		}
	case "manifest":
		recordFakeCall()
		m := fixture("fake-manifest", "\x00")
		if m == "\x00" {
			fmt.Fprint(os.Stderr, "abort: empty revision set")
			return 255
		}
		fmt.Print(m)
	case "branches":
		recordFakeCall()
		fmt.Print(fixture("fake-branches", ""))
	case "flood":
		// The grandchild, sharing both pipes, starts flooding first; its marker
		// file proves it ran before the child floods too.
		marker := filepath.Join(filepath.Dir(os.Getenv("HGRCPATH")), "grandchild-started")
		gc := exec.Command(os.Args[0], "flood-grandchild", args[1])
		gc.Stdout, gc.Stderr = os.Stdout, os.Stderr
		if err := gc.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, err := os.Stat(marker); err != nil; _, err = os.Stat(marker) {
			time.Sleep(10 * time.Millisecond)
		}
		flood(args[1])
	case "flood-grandchild":
		if err := os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("HGRCPATH")), "grandchild-started"), nil, 0600); err != nil {
			return 1
		}
		flood(args[1])
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
		if len(args) == 5 && args[4] == "{node}" {
			recordFakeCall()
			fmt.Print(fixture("fake-visible", ""))
			break
		}
		if fixture("fake-log", "\x00") != "\x00" {
			recordFakeCall()
		}
		fmt.Print(fixture("fake-log", `[{"node": "0000000000000000000000000000000000000000", "rev": -1}]`))
	default:
		fmt.Fprintf(os.Stderr, "fake hg: unknown command %q\n", args[0])
		return 255
	}
	return 0
}

// recordFakeCall appends the fake's arguments to fake-calls for fakeCalls.
func recordFakeCall() {
	f, err := os.OpenFile(filepath.Join(filepath.Dir(os.Getenv("HGRCPATH")), "fake-calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		fmt.Fprintln(f, strings.Join(os.Args[1:], "\x00"))
		f.Close()
	}
}

// flood writes to stdout and/or stderr ("out", "err", "both") until killed.
func flood(which string) {
	junk := bytes.Repeat([]byte("x"), 4096)
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if which == "both" || (which == "out") == (f == os.Stdout) {
			go func() {
				for {
					f.Write(junk)
				}
			}()
		}
	}
	time.Sleep(time.Minute)
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
		{Executable: exe, ConfigPath: filepath.Join(dir, "hgrc"), IdleTimeout: -1},
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

func TestRunnerSessionConfig(t *testing.T) {
	r := newTestRunner(t, Config{})
	rc := filepath.Join(t.TempDir(), "hooks.rc")
	c, err := r.StartWithConfig(context.Background(), "", []string{"env"}, nil, rc)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(c.Stdout)
	c.Stop(context.Background())
	if want := "HGRCPATH=" + r.cfg.ConfigPath + string(os.PathListSeparator) + rc; !strings.Contains(string(out), want+"\n") {
		t.Errorf("environment lacks %q:\n%s", want, out)
	}
	for _, bad := range []string{"hooks.rc", rc + string(os.PathListSeparator) + rc} {
		if _, err := r.StartWithConfig(context.Background(), "", []string{"env"}, nil, bad); err == nil {
			t.Errorf("session config %q was accepted", bad)
		}
	}

	// dispatch.py serves stdio only for exactly "-R <repo> serve --stdio".
	repo := t.TempDir()
	var stdout bytes.Buffer
	if err := r.Run(context.Background(), repo, []string{"argv"}, nil, &stdout); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "-R\n"+repo+"\nargv\n"; got != want {
		t.Errorf("argv %q, want %q", got, want)
	}
}

func TestLocalBytes(t *testing.T) {
	got, err := LocalBytes("gogs bin é")
	if err != nil {
		t.Fatal(err)
	}
	switch enc := localEncoding(); enc {
	case "utf-8":
		if string(got) != "gogs bin é" {
			t.Errorf("LocalBytes = %q, want UTF-8", got)
		}
	case "cp1252":
		if string(got) != "gogs bin \xe9" {
			t.Errorf("LocalBytes = %q, want Windows-1252", got)
		}
		if _, err = LocalBytes("雪"); err == nil {
			t.Error("LocalBytes accepted a character outside the code page")
		}
	default:
		t.Logf("encoding %s: only checked for errors", enc)
	}
}

// TestManagedConfigBlocksOutboundFetches: every HTTP(S) request Mercurial
// makes as a server, as for a pushed remote-changegroup part, must go to an
// unconnectable proxy, loopback included.
func TestManagedConfigBlocksOutboundFetches(t *testing.T) {
	section := ""
	got := map[string]string{}
	for _, line := range strings.Split(managedConfig, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
		} else if k, v, ok := strings.Cut(line, "="); ok && section == "http_proxy" {
			got[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if got["host"] != "127.0.0.1:0" || got["always"] != "true" {
		t.Errorf("managed [http_proxy] = %v, want host 127.0.0.1:0 and always true", got)
	}
}
