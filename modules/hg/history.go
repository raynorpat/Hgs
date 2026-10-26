// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidCursor = errors.New("hg: invalid or expired history cursor")

type Changeset struct {
	Node                  NodeID
	Parents               []NodeID
	User, Message, Branch string
	Date                  time.Time
	Revision              int64
}

// HistoryQuery starts with BeforeRevision=-1; an empty Snapshot pins the
// visible tip. An explicit Snapshot must include its matching local revision.
// Later pages must return the Snapshot, SnapshotRevision and NextBefore tuple.
// Path is a literal repository file name; recorded copies/renames are followed.
type HistoryQuery struct {
	Snapshot         NodeID
	SnapshotRevision int64
	BeforeRevision   int64
	Path             string
	Limit            int
}

type HistoryPage struct {
	Snapshot         NodeID
	SnapshotRevision int64
	Entries          []Changeset
	NextBefore       int64
	HasMore          bool
}

const changesetTemplate = `{dict(node, rev, p1node, p2node, author, desc, branch, date)|json}\n`

// Changeset reads Mercurial's author, date and ordered parents without
// inventing committer metadata. Hidden and secret changesets are not served.
func (r *Runner) Changeset(ctx context.Context, path string, node NodeID) (Changeset, error) {
	node, err := ParseNodeID(string(node))
	if err != nil {
		return Changeset{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	entries, err := r.readChangesets(ctx, path, []string{"log", "-r", nodeRevset(node), "-T", changesetTemplate})
	if err != nil {
		return Changeset{}, err
	}
	if len(entries) != 1 || entries[0].Node != node {
		return Changeset{}, ErrNotFound
	}
	return entries[0], nil
}

func (r *Runner) readChangesets(ctx context.Context, path string, args []string) ([]Changeset, error) {
	var out bytes.Buffer
	if err := r.Run(ctx, path, args, nil, &out); err != nil {
		return nil, err
	}
	return parseChangesets(out.Bytes())
}

func parseChangesets(data []byte) ([]Changeset, error) {
	if !utf8.Valid(data) {
		return nil, ErrUnsupportedEncoding
	}
	entries := []Changeset{}
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var raw struct {
			Node, P1node, P2node, Author, Desc, Branch string
			Rev                                        *int64
			Date                                       []float64
		}
		if err := d.Decode(&raw); err == io.EOF {
			return entries, nil
		} else if err != nil {
			return nil, fmt.Errorf("hg: unrecognized changeset output: %v", err)
		}
		node, err := ParseNodeID(raw.Node)
		if err != nil || raw.Rev == nil || *raw.Rev < 0 || len(raw.Date) != 2 || math.IsNaN(raw.Date[0]) || math.IsInf(raw.Date[0], 0) || raw.Date[0] >= math.MaxInt64 || raw.Date[0] < math.MinInt64 {
			return nil, errors.New("hg: invalid changeset metadata")
		}
		sec, frac := math.Modf(raw.Date[0])
		cs := Changeset{Node: node, Revision: *raw.Rev, User: raw.Author, Message: raw.Desc, Branch: raw.Branch, Date: time.Unix(int64(sec), int64(frac*1e9)).UTC(), Parents: []NodeID{}}
		for _, p := range []string{raw.P1node, raw.P2node} {
			if p == strings.Repeat("0", 40) {
				continue
			}
			parent, err := ParseNodeID(p)
			if err != nil {
				return nil, errors.New("hg: invalid changeset parent")
			}
			cs.Parents = append(cs.Parents, parent)
		}
		entries = append(entries, cs)
	}
}

// History pins all served revisions through the visible tip's local revision,
// including named heads outside that tip's ancestry. The publishing store
// disables strip and obsolescence, so append-only revision bounds stay stable.
func (r *Runner) History(ctx context.Context, path string, q HistoryQuery) (HistoryPage, error) {
	page := HistoryPage{}
	if q.BeforeRevision < -1 || q.Limit < 0 {
		return page, ErrInvalidCursor
	}
	if q.Snapshot == "" {
		if q.BeforeRevision != -1 {
			return page, ErrInvalidCursor
		}
	} else {
		node, err := ParseNodeID(string(q.Snapshot))
		if err != nil || q.SnapshotRevision < 0 || q.BeforeRevision > q.SnapshotRevision {
			return page, ErrInvalidCursor
		}
		q.Snapshot = node
	}
	if q.Path != "" {
		if err := checkFile(q.Path); err != nil {
			return page, err
		}
	}
	if q.Limit == 0 {
		q.Limit = 30
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	if q.Snapshot == "" {
		entries, err := r.readChangesets(ctx, path, []string{"log", "-r", "max(not secret())", "-T", changesetTemplate})
		if err != nil {
			return page, err
		}
		if len(entries) == 0 {
			return page, ErrEmptyRepository
		}
		q.Snapshot, q.SnapshotRevision = entries[0].Node, entries[0].Revision
	} else {
		cs, err := r.Changeset(ctx, path, q.Snapshot)
		if errors.Is(err, ErrNotFound) || err == nil && cs.Revision != q.SnapshotRevision {
			return page, ErrInvalidCursor
		}
		if err != nil {
			return page, err
		}
	}
	page.Snapshot, page.SnapshotRevision = q.Snapshot, q.SnapshotRevision
	// File history requires the file at the selected snapshot; callers can
	// anchor an earlier changeset to browse a subsequently deleted file.
	var patternDir, pattern string
	if q.Path != "" {
		var err error
		patternDir, err = os.MkdirTemp(filepath.Dir(r.cfg.ConfigPath), "history-")
		if err != nil {
			return HistoryPage{}, err
		}
		defer os.RemoveAll(patternDir)
		pattern = filepath.Join(patternDir, "pattern")
		if err = os.WriteFile(pattern, []byte("path:"+q.Path+"\x00"), 0600); err != nil {
			return HistoryPage{}, err
		}
		parent := pathpkg.Dir(q.Path)
		if parent == "." {
			parent = ""
		}
		include := filepath.Join(patternDir, "include")
		if err = os.WriteFile(include, []byte("rootfilesin:"+parent+"\x00"), 0600); err != nil {
			return HistoryPage{}, err
		}
		var out bytes.Buffer
		// The template emits repository path bytes with '/', rather than
		// the platform-local presentation used by plain `hg files`.
		err = r.Run(ctx, path, []string{"files", "-r", nodeRevset(q.Snapshot), "-T", "{path}\\n", "-I", "listfile0:" + include, "--", "listfile0:" + pattern}, nil, &out)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return HistoryPage{}, ErrNotFound
		}
		if err != nil {
			return HistoryPage{}, err
		}
		if out.String() != q.Path+"\n" {
			return HistoryPage{}, ErrNotFound
		}
	}
	seed := "(0:" + strconv.FormatInt(q.SnapshotRevision, 10) + ") and not secret()"
	selection := seed
	if q.BeforeRevision >= 0 {
		if q.BeforeRevision == 0 {
			page.Entries = []Changeset{}
			return page, nil
		}
		selection += " and (0:" + strconv.FormatInt(q.BeforeRevision-1, 10) + ")"
	}
	args := []string{"log", "-r", "sort((" + selection + "), -rev)", "-l", strconv.Itoa(q.Limit + 1), "-T", changesetTemplate}
	if q.Path == "" {
		entries, err := r.readChangesets(ctx, path, args)
		if err != nil {
			return HistoryPage{}, err
		}
		page.Entries = entries
	} else {
		// Native --follow always starts at the pinned node's file ancestry,
		// retaining rename/copy history on later pages without including
		// unrelated heads. Only pagination bounds enter the template revset.
		template := `{ifcontains(rev, revset("` + selection + `"), '` + changesetTemplate + `')}`
		args = []string{"log", "--follow", "-r", nodeRevset(q.Snapshot), "-T", template, "--", "listfile0:" + pattern}
		w := &historyWriter{limit: q.Limit + 1, entries: []Changeset{}}
		err := r.run(ctx, path, args, nil, w, -1)
		if err != nil && !errors.Is(err, errHistoryPageFull) {
			return HistoryPage{}, err
		}
		if len(w.pending) != 0 {
			return HistoryPage{}, errors.New("hg: unterminated history output")
		}
		page.Entries = w.entries
	}
	page.HasMore = len(page.Entries) > q.Limit
	if page.HasMore {
		page.Entries = page.Entries[:q.Limit]
		page.NextBefore = page.Entries[len(page.Entries)-1].Revision
	}
	return page, nil
}

var errHistoryPageFull = errors.New("hg: history page collected")

// historyWriter keeps metadata bounded even if --follow traverses many
// revisions. Returning the sentinel stops the native process after one page.
type historyWriter struct {
	limit   int
	pending []byte
	entries []Changeset
	bytes   int
}

func (w *historyWriter) Write(p []byte) (int, error) {
	used := 0
	for len(p) > 0 {
		n := bytes.IndexByte(p, '\n')
		if n < 0 {
			n = len(p) - 1
		}
		chunk := p[:n+1]
		if w.bytes+len(chunk) > MaxCommandOutput {
			return used, ErrOutputLimit
		}
		w.bytes += len(chunk)
		w.pending = append(w.pending, chunk...)
		used += len(chunk)
		p = p[len(chunk):]
		if chunk[len(chunk)-1] != '\n' {
			continue
		}
		entries, err := parseChangesets(w.pending)
		if err != nil {
			return used, err
		}
		w.pending = w.pending[:0]
		w.entries = append(w.entries, entries...)
		if len(w.entries) >= w.limit {
			return used, errHistoryPageFull
		}
	}
	return used, nil
}
