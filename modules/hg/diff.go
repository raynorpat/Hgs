// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

type DiffResult struct {
	Text              []byte
	Truncated, Binary bool
}

// Diff compares a served changeset with its ordered parent (0 or 1). A root's
// parent 0 is the null revision. Mercurial's extended patch preserves
// copy/mode data; binary content is summarized instead of base85-encoded into
// a patch. The caller has already looked cs up with Changeset.
func (r *Runner) Diff(ctx context.Context, path string, cs Changeset, parentIndex int) (DiffResult, error) {
	if parentIndex < 0 || parentIndex > 1 {
		return DiffResult{}, fmt.Errorf("hg: invalid diff parent %d", parentIndex)
	}
	if parentIndex >= max(len(cs.Parents), 1) {
		return DiffResult{}, fmt.Errorf("hg: changeset has no parent %d", parentIndex)
	}
	if r.reads == nil {
		return DiffResult{}, fmt.Errorf("%w: no read client", ErrReadService)
	}
	rel, err := r.reads.relative(path)
	if err != nil {
		return DiffResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	w := newDiffWriter()
	truncated, err := r.reads.Stream(ctx, "/diff", url.Values{"repo": {rel}, "changeset": {string(cs.Node)}, "parent": {strconv.Itoa(parentIndex)}}, w)
	if err != nil && !errors.Is(err, ErrOutputLimit) {
		return DiffResult{}, err
	}
	w.truncated = w.truncated || truncated
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
