// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package hg

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunnerRealMercurial(t *testing.T) {
	// Ambient configuration loading a missing extension would print a
	// diagnostic on every command if it leaked through.
	ambient := filepath.Join(t.TempDir(), "ambient.rc")
	os.WriteFile(ambient, []byte("[extensions]\nmissingext = "+filepath.Join(t.TempDir(), "nope.py")+"\n"), 0600)
	t.Setenv("HGRCPATH", ambient)
	t.Setenv("PYTHONPATH", t.TempDir())

	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	checkRealMercurial(t, exe)

	if old := os.Getenv("GOGS_TEST_HG_OLD_SERVER"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_SERVER (Mercurial 5.0.x) not set")
	} else {
		checkRealMercurial(t, old)
	}
	t.Log("unavailable: no real Mercurial 4.x executable; rejection is covered by TestRunnerCheckVersions")
}

func checkRealMercurial(t *testing.T, exe string) {
	// Windows Mercurial uses the ANSI code page for file names, so non-Latin
	// names are covered only off Windows; CheckPath rejects them there.
	name := "hg runner é ü"
	if runtime.GOOS != "windows" {
		name += " 漢字"
	}
	root := filepath.Join(t.TempDir(), name)
	r, err := NewRunner(Config{Executable: exe, ConfigPath: filepath.Join(root, "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.Check(ctx); err != nil {
		t.Fatalf("%s: %v", exe, err)
	}

	repo := filepath.Join(root, "repo é")
	if err := r.Run(ctx, "", []string{"init", repo}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(repo, ".hg", "requires")); err != nil || fi.IsDir() {
		t.Fatalf("repository not created at %q: %v", repo, err)
	}

	// An enclosing repository's hgrc must not reach repository-less commands.
	bogus := "[extensions]\nmissingrepoext = " + filepath.Join(root, "nope.py") + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".hg", "hgrc"), []byte(bogus), 0600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(repo, "data", "hgrc")
	if _, err := NewRunner(Config{Executable: exe, ConfigPath: inside}); err == nil {
		t.Error("NewRunner accepted a config directory inside a repository")
	}
	// Bypass that refusal to show HGRCSKIPREPO alone also keeps it out.
	os.MkdirAll(filepath.Dir(inside), 0700)
	os.WriteFile(inside, []byte(managedConfig), 0600)
	skipped := *r
	skipped.cfg.ConfigPath = inside
	c, err := skipped.Start(ctx, "", []string{"version", "-T", "json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, c.Stdout)
	if stderr, _ := io.ReadAll(c.Stderr); c.Wait() != nil || len(stderr) > 0 {
		t.Errorf("enclosing repository config loaded: %q", stderr)
	}
	c.Stop(ctx)
	// Repository commands do read their own hgrc; drop the bogus one.
	os.Remove(filepath.Join(repo, ".hg", "hgrc"))

	for _, probe := range []struct {
		repo string
		args []string
	}{
		{"", []string{"version", "-T", "json"}},
		{repo, []string{"log", "-r", "null", "-T", "json"}},
	} {
		c, err := r.Start(ctx, probe.repo, probe.args, nil)
		if err != nil {
			t.Fatal(err)
		}
		go io.Copy(io.Discard, c.Stdout)
		stderr, _ := io.ReadAll(c.Stderr)
		if err := c.Wait(); err != nil || len(stderr) > 0 {
			t.Errorf("hg %v: %v, stderr %q", probe.args, err, stderr)
		}
		c.Stop(ctx)
	}
}
