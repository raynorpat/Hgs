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

func TestReadFileNeverChecksOut(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "")
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
	}
	if after := len(fakeCalls(t, r)); after != before {
		t.Errorf("rejected input ran %d Mercurial commands", after-before)
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

func TestListDirectoryFlat(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "")
	writeFakeFixture(t, r, "fake-manifest", "644   a.txt\n755 * bin\n644 @ link\n644   my file.txt\n644   only/nested/c.txt\n644   only/d.txt\n")
	root, err := r.ListDirectory(ctx, repo, newerDefaultNode, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root, []TreeEntry{
		{Name: "a.txt"},
		{Name: "bin", Executable: true},
		{Name: "link", Symlink: true},
		{Name: "my file.txt"},
		{Name: "only", Directory: true},
	}) {
		t.Fatalf("root %+v", root)
	}
	sub, err := r.ListDirectory(ctx, repo, newerDefaultNode, "only")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sub, []TreeEntry{{Name: "d.txt"}, {Name: "nested", Directory: true}}) {
		t.Fatalf("only %+v", sub)
	}
	sawFiles := false
	for _, call := range fakeCalls(t, r) {
		if call[2] == "debugdata" {
			t.Fatalf("flat listing used %v", call)
		}
		if call[2] == "files" {
			t.Fatalf("flat listing used the per-file flags template: %v", call)
		}
		if call[2] == "manifest" && strings.Contains(strings.Join(call, " "), " -v") {
			sawFiles = true
		}
	}
	if !sawFiles {
		t.Fatal("flat listing did not ask manifest -v for flags and paths")
	}

	writeFakeFixture(t, r, "fake-manifest", "644   a.txt")
	if _, err := r.ListDirectory(ctx, repo, newerDefaultNode, ""); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("unterminated list: %v", err)
	}
	writeFakeFixture(t, r, "fake-manifest", "")
	if entries, err := r.ListDirectory(ctx, repo, newerDefaultNode, ""); err != nil || len(entries) != 0 {
		t.Fatalf("empty list: %v %+v", err, entries)
	}
	writeFakeFixture(t, r, "fake-manifest", "644   a.txt\n")
	if entries, err := r.ListDirectory(ctx, repo, newerDefaultNode, "only"); err != nil || len(entries) != 0 {
		t.Fatalf("no match: %v %+v", err, entries)
	}
}

func TestListDirectoryTree(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "")
	if err := os.MkdirAll(filepath.Join(repo, ".hg", "store"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hg", "store", "requires"), []byte("share-safe\ntreemanifest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifestNode := strings.Repeat("d", 40)
	child := strings.Repeat("b", 40)
	fileNode := strings.Repeat("c", 40)
	writeFakeFixture(t, r, "fake-manifest-node", manifestNode+"\n")
	writeFakeFixture(t, r, "fake-debugdata", "a.txt\x00"+fileNode+"\nonly\x00"+child+"t\n")
	writeFakeFixture(t, r, "fake-debugdata-only", "d.txt\x00"+fileNode+"x\n")
	root, err := r.ListDirectory(ctx, repo, newerDefaultNode, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root, []TreeEntry{{Name: "a.txt"}, {Name: "only", Directory: true}}) {
		t.Fatalf("root %+v", root)
	}
	sub, err := r.ListDirectory(ctx, repo, newerDefaultNode, "only")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sub, []TreeEntry{{Name: "d.txt", Executable: true}}) {
		t.Fatalf("only %+v", sub)
	}
	if _, err := r.ListDirectory(ctx, repo, newerDefaultNode, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing directory: %v", err)
	}
	for _, call := range fakeCalls(t, r) {
		if call[2] == "files" || call[2] == "manifest" {
			t.Fatalf("tree listing used %v", call)
		}
	}
	sawData := false
	for _, call := range fakeCalls(t, r) {
		if call[2] == "debugdata" {
			sawData = true
		}
	}
	if !sawData {
		t.Fatal("tree listing did not read manifest text")
	}

	writeFakeFixture(t, r, "fake-debugdata", "a.txt\x00"+fileNode)
	if _, err := r.ListDirectory(ctx, repo, newerDefaultNode, ""); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("unterminated manifest text: %v", err)
	}
}

func TestListPathFlatReadsManifestOnce(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "")
	writeFakeFixture(t, r, "fake-manifest", "644   a.txt\n755 * only/run.sh\n644   only/nested/c.txt\n")
	entries, file, err := r.ListPath(ctx, repo, newerDefaultNode, "only")
	if err != nil || file != nil || !reflect.DeepEqual(entries, []TreeEntry{{Name: "nested", Directory: true}, {Name: "run.sh", Executable: true}}) {
		t.Fatalf("directory: %+v %+v %v", entries, file, err)
	}
	entries, file, err = r.ListPath(ctx, repo, newerDefaultNode, "only/run.sh")
	if err != nil || len(entries) != 0 || file == nil || *file != (TreeEntry{Name: "run.sh", Executable: true}) {
		t.Fatalf("file: %+v %+v %v", entries, file, err)
	}
	for _, missing := range []string{"nope", "only/nope", "a.txt/x"} {
		if _, _, err = r.ListPath(ctx, repo, newerDefaultNode, missing); !errors.Is(err, ErrNotFound) {
			t.Errorf("ListPath(%q): %v", missing, err)
		}
	}
	if _, _, err = r.ListPath(ctx, repo, newerDefaultNode, "../a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("escaping path: %v", err)
	}
	calls := fakeCalls(t, r)
	if len(calls) != 5 {
		t.Errorf("five accepted lookups ran %d Mercurial commands, want one manifest each: %v", len(calls), calls)
	}
	for _, call := range calls {
		if call[2] != "manifest" {
			t.Errorf("flat lookup ran %v", call)
		}
	}
}

func TestListPathTree(t *testing.T) {
	ctx := context.Background()
	r, repo := newBrowseRunner(t, "")
	if err := os.MkdirAll(filepath.Join(repo, ".hg", "store"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hg", "store", "requires"), []byte("share-safe\ntreemanifest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	child, fileNode := strings.Repeat("b", 40), strings.Repeat("c", 40)
	writeFakeFixture(t, r, "fake-manifest-node", strings.Repeat("d", 40)+"\n")
	writeFakeFixture(t, r, "fake-debugdata", "a.txt\x00"+fileNode+"\nonly\x00"+child+"t\n")
	writeFakeFixture(t, r, "fake-debugdata-only", "d.txt\x00"+fileNode+"x\n")
	entries, file, err := r.ListPath(ctx, repo, newerDefaultNode, "only")
	if err != nil || file != nil || !reflect.DeepEqual(entries, []TreeEntry{{Name: "d.txt", Executable: true}}) {
		t.Fatalf("directory: %+v %+v %v", entries, file, err)
	}
	if _, file, err = r.ListPath(ctx, repo, newerDefaultNode, "a.txt"); err != nil || file == nil || file.Name != "a.txt" {
		t.Fatalf("file: %+v %v", file, err)
	}
	if _, _, err = r.ListPath(ctx, repo, newerDefaultNode, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
