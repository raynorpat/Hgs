// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package hgbridge

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

// TestStdioSessionConfigRunsHooks runs transactions under the session config
// files NewStdioBridge writes, with the hook executable under a non-ASCII
// path: Mercurial must start the hook itself, so readers are refused by the
// deny hook's own message and writers pass the check hook.
func TestStdioSessionConfigRunsHooks(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	gogs := buildGogs(t)
	root := t.TempDir()
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := &hgHarness{t: t, r: r, ctx: ctx}
	ctl, err := NewController(func(context.Context, Grant) error { return nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close(ctx)
	b, err := NewStdioBridge(r, ctl, gogs, filepath.Join(root, "managed"))
	if err != nil {
		t.Fatal(err)
	}
	_, env, revoke, err := ctl.Issue(Grant{Principal: Principal{UserID: 1, KeyID: 1}, RepoID: 9, CanWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()

	repo := filepath.Join(root, "repo")
	h.must("", nil, nil, "init", repo)
	bookmark := func(rc string, env map[string]string, name string) (string, error) {
		c, err := r.StartWithConfig(ctx, repo, []string{"bookmark", name}, env, rc)
		if err != nil {
			t.Fatal(err)
		}
		msg, _ := io.ReadAll(c.Stderr)
		err = c.Stop(ctx)
		return string(msg), err
	}

	msg, err := bookmark(b.rc[false], nil, "reader")
	if err == nil {
		t.Fatal("reader transaction succeeded")
	}
	if !strings.Contains(msg, "gogs: write rejected: this session has read-only access") {
		t.Fatalf("reader transaction not refused by the deny hook: %s", msg)
	}
	if msg, err = bookmark(b.rc[true], env, "writer"); err != nil {
		t.Fatalf("writer transaction refused: %v: %s", err, msg)
	}
}
