// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

type DiffResult struct {
	Text              []byte
	Truncated, Binary bool
}

// Diff compares node with its ordered parent (0 or 1). A root's parent 0
// is the null revision. Mercurial's extended patch preserves copy/mode data;
// binary content is summarized instead of base85-encoded into a patch.
func (r *Runner) Diff(ctx context.Context, path string, node NodeID, parentIndex int) (DiffResult, error) {
	if parentIndex < 0 || parentIndex > 1 {
		return DiffResult{}, fmt.Errorf("hg: invalid diff parent %d", parentIndex)
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	cs, err := r.Changeset(ctx, path, node)
	if err != nil {
		return DiffResult{}, err
	}
	parent := strings.Repeat("0", 40)
	if len(cs.Parents) > 0 {
		if parentIndex >= len(cs.Parents) {
			return DiffResult{}, fmt.Errorf("hg: changeset has no parent %d", parentIndex)
		}
		parent = string(cs.Parents[parentIndex])
	} else if parentIndex != 0 {
		return DiffResult{}, fmt.Errorf("hg: root changeset has no parent %d", parentIndex)
	}
	w := newDiffWriter()
	err = r.run(ctx, path, []string{"diff", "--git", "--nodates", "--no-binary", "-r", parent, "-r", string(cs.Node)}, nil, w, -1)
	if err != nil && !errors.Is(err, ErrOutputLimit) {
		return DiffResult{}, err
	}
	return w.result(), nil
}

type diffWriter struct {
	text      []byte
	lines     int
	truncated bool
}

func newDiffWriter() *diffWriter { return &diffWriter{} }

func (w *diffWriter) Write(p []byte) (int, error) {
	n := 0
	for n < len(p) && len(w.text)+n < MaxDiffBytes && w.lines < MaxDiffLines {
		if p[n] == '\n' {
			w.lines++
		}
		n++
	}
	w.text = append(w.text, p[:n]...)
	if n < len(p) {
		w.truncated = true
		return n, ErrOutputLimit
	}
	return n, nil
}

func (w *diffWriter) result() DiffResult {
	d := DiffResult{Text: w.text, Truncated: w.truncated}
	for _, line := range bytes.Split(w.text, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("Binary file ")) && bytes.HasSuffix(line, []byte(" has changed")) {
			d.Binary = true
			break
		}
	}
	return d
}
