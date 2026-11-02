// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestReadFileRejectsBadInput(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, Config{})
	f := useFakeReads(t, r, root, nil)
	repo := filepath.Join(root, "repo")
	for file, wantErr := range map[string]error{
		"": ErrNotFound, "/A": ErrNotFound, "A/": ErrNotFound, "d//x": ErrNotFound, "./A": ErrNotFound,
		"../A": ErrNotFound, "d/../A": ErrNotFound, ".hg/hgrc": ErrNotFound, "d/.HG/store": ErrNotFound,
		".hg": ErrNotFound, "A\x00": ErrNotFound, `d\x`: ErrUnsupportedEncoding, `..\A`: ErrUnsupportedEncoding,
	} {
		if err := r.ReadFile(context.Background(), repo, newerDefaultNode, file, io.Discard); !errors.Is(err, wantErr) {
			t.Errorf("ReadFile(%q): %v; want %v", file, err, wantErr)
		}
	}
	for _, node := range []NodeID{"", "tip", newerDefaultNode + " or all()"} {
		if err := r.ReadFile(context.Background(), repo, node, "A", io.Discard); !errors.Is(err, ErrNotFound) {
			t.Errorf("ReadFile at %q: %v", node, err)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("rejected input sent %d requests", n)
	}
}

// diffNode looks node up the way the commit page does before diffing it.
func diffNode(r *Runner, ctx context.Context, repo string, node NodeID, parentIndex int) (DiffResult, error) {
	cs, err := r.Changeset(ctx, repo, node)
	if err != nil {
		return DiffResult{}, err
	}
	return r.Diff(ctx, repo, cs, parentIndex)
}
