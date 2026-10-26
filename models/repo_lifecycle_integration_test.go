// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package models

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/hg"
)

func TestRepositoryProvisionRealMercurial(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	newTestDatabase(t)
	// Drop every PATH entry holding git; Mercurial may still need the rest
	// (on Windows, its Python DLL).
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := exec.LookPath(filepath.Join(dir, "git")); err != nil {
			keep = append(keep, dir)
		}
	}
	t.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
	if p, err := exec.LookPath("git"); err == nil {
		t.Fatalf("git still reachable at %s", p)
	}

	root := filepath.Join(t.TempDir(), "repository root with spaces")
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	owner := newAccessTestUser(t, "alice", true)

	repo, err := CreateHgRepository(ctx, r, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "demo", Description: "no readme"})
	if err != nil {
		t.Fatal(err)
	}
	path := storagePath(t, root, repo.Id)
	if names := dirNames(t, path); len(names) != 1 || names[0] != ".hg" {
		t.Fatalf("storage holds %q, want only .hg (no working files)", names)
	}
	if names := dirNames(t, filepath.Join(root, stagingDirName)); len(names) != 0 {
		t.Fatalf("staging left %q", names)
	}
	if b, err := os.ReadFile(filepath.Join(path, ".hg", storageMarker)); err != nil || !bytes.Equal(b, storageMarkerContent(repo)) {
		t.Fatalf("marker %q, %v", b, err)
	}
	// The repository hgrc is read after the managed configuration, so it
	// must never be able to override or unset hooks.
	if b, err := os.ReadFile(filepath.Join(path, ".hg", "hgrc")); err == nil {
		for _, bad := range []string{"[hooks]", "%unset", "%include"} {
			if bytes.Contains(b, []byte(bad)) {
				t.Fatalf("repository hgrc contains %s:\n%s", bad, b)
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err = r.Run(ctx, path, []string{"log", "-T", "json"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(out.String()), ""); got != "[]" {
		t.Fatalf("new repository has history: %s", got)
	}

	if err = DeleteHgRepository(ctx, root, repo.Id, func(context.Context, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Fatal("storage survived deletion")
	}
}
