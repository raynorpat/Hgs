// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parseGoldenManifest(t *testing.T, name string) []ManifestEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestReadFileNeverChecksOut(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "")
	b, err := os.ReadFile(filepath.Join("testdata", "browse-manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeFakeFixture(t, r, "fake-manifest", string(b))

	entries, err := r.Manifest(ctx, repo, newerDefaultNode)
	if err != nil {
		t.Fatal(err)
	}
	want := []ManifestEntry{
		{Path: ".hgtags"}, {Path: "A"}, {Path: "[ab]"}, {Path: "a"}, {Path: "caf\xe9"}, {Path: "d/x"},
		{Path: "link", Symlink: true}, {Path: "run.sh", Executable: true},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("manifest %#v; want %#v", entries, want)
	}

	// The fake cat prints the literal pattern files it was given.
	for file, patterns := range map[string]string{
		"A":       "rootfilesin:\x00path:A\x00",
		"[ab]":    "rootfilesin:\x00path:[ab]\x00",
		"caf\xe9": "rootfilesin:\x00path:caf\xe9\x00",
		"d/x":     "rootfilesin:d\x00path:d/x\x00",
		"d/e/f g": "rootfilesin:d/e\x00path:d/e/f g\x00",
	} {
		var out bytes.Buffer
		if err := r.ReadFile(ctx, repo, newerDefaultNode, file, &out); err != nil || out.String() != patterns {
			t.Errorf("ReadFile(%q) passed %q, %v; want %q", file, out.String(), err, patterns)
		}
	}

	// Raw reads stream past the rendered-output limit.
	var big bytes.Buffer
	if err := r.ReadFile(ctx, repo, newerDefaultNode, "big", &big); err != nil || big.Len() != MaxCommandOutput+1 {
		t.Errorf("streamed %d bytes, %v; want %d", big.Len(), err, MaxCommandOutput+1)
	}
	if err := r.ReadFile(ctx, repo, newerDefaultNode, "missing", io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}

	before := len(fakeCalls(t, r))
	for file, wantErr := range map[string]error{
		"": ErrNotFound, "/A": ErrNotFound, "A/": ErrNotFound, "d//x": ErrNotFound, "./A": ErrNotFound,
		"../A": ErrNotFound, "d/../A": ErrNotFound, ".hg/hgrc": ErrNotFound, "d/.HG/store": ErrNotFound,
		".hg": ErrNotFound, "A\x00": ErrNotFound, `d\x`: ErrUnsupportedEncoding, `..\A`: ErrUnsupportedEncoding,
	} {
		if err := r.ReadFile(ctx, repo, newerDefaultNode, file, io.Discard); !errors.Is(err, wantErr) {
			t.Errorf("ReadFile(%q): %v; want %v", file, err, wantErr)
		}
	}
	for _, node := range []NodeID{"", "tip", NodeID(strings.Repeat("0", 40)), newerDefaultNode + " or all()"} {
		if err := r.ReadFile(ctx, repo, node, "A", io.Discard); !errors.Is(err, ErrNotFound) {
			t.Errorf("ReadFile at %q: %v", node, err)
		}
		if _, err := r.Manifest(ctx, repo, node); !errors.Is(err, ErrNotFound) {
			t.Errorf("Manifest at %q: %v", node, err)
		}
	}
	if after := len(fakeCalls(t, r)); after != before {
		t.Errorf("rejected input ran %d Mercurial commands", after-before)
	}

	// A changeset Mercurial does not serve (unknown or secret) aborts the
	// command; the follow-up lookup turns that into ErrNotFound.
	os.Remove(filepath.Join(filepath.Dir(r.cfg.ConfigPath), "fake-manifest"))
	if _, err := r.Manifest(ctx, repo, newerDefaultNode); !errors.Is(err, ErrNotFound) {
		t.Errorf("manifest of an unserved node: %v", err)
	}

	for _, call := range fakeCalls(t, r) {
		if cmd := call[2]; cmd != "log" && cmd != "manifest" && cmd != "cat" {
			t.Errorf("browsing ran %q", call)
		}
		if call[2] != "log" && call[4] != "id("+string(newerDefaultNode)+") and not secret()" {
			t.Errorf("browsing used revision %q", call[4])
		}
	}
	ls, err := os.ReadDir(repo)
	if checkoutCreated := err != nil || len(ls) != 1 || ls[0].Name() != ".hg"; checkoutCreated {
		t.Errorf("repository directory now holds %v, %v", ls, err)
	}
}
