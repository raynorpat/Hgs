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

// changesetSelection is the service's fields for a Changeset.
const changesetSelection = `id revision branch user comments time { time } parents { id }`

type gqlChangeset struct {
	ID, Branch, User, Comments string
	Revision                   int64
	Time                       struct{ Time int64 }
	Parents                    []struct{ ID string }
}

func (g gqlChangeset) changeset() (Changeset, error) {
	node, err := ParseNodeID(g.ID)
	if err != nil || g.Revision < 0 {
		return Changeset{}, errors.New("hg: invalid changeset metadata")
	}
	cs := Changeset{Node: node, Revision: g.Revision, User: g.User, Message: g.Comments, Branch: g.Branch, Date: time.Unix(g.Time.Time, 0).UTC(), Parents: []NodeID{}}
	for _, p := range g.Parents {
		parent, err := ParseNodeID(p.ID)
		if err != nil {
			return Changeset{}, errors.New("hg: invalid changeset parent")
		}
		cs.Parents = append(cs.Parents, parent)
	}
	return cs, nil
}

// readChangeset reads one changeset from the service; hidden and secret
// changesets are ErrNotFound.
func (r *Runner) readChangeset(ctx context.Context, path string, node NodeID) (Changeset, error) {
	var out struct {
		Repo struct{ Changeset gqlChangeset }
	}
	err := r.readQuery(ctx, path, "Changeset", `query Changeset($repo: String!, $id: NodeId!) { repo(path: $repo) { changeset(id: $id) { `+changesetSelection+` } } }`, map[string]interface{}{"id": string(node)}, &out)
	if err != nil {
		return Changeset{}, err
	}
	cs, err := out.Repo.Changeset.changeset()
	if err != nil {
		return Changeset{}, err
	}
	if cs.Node != node {
		return Changeset{}, fmt.Errorf("%w: answered for changeset %s", ErrReadService, cs.Node)
	}
	return cs, nil
}

// Changeset reads Mercurial's author, date and ordered parents without
// inventing committer metadata. Hidden and secret changesets are not served.
func (r *Runner) Changeset(ctx context.Context, path string, node NodeID) (Changeset, error) {
	node, err := ParseNodeID(string(node))
	if err != nil {
		return Changeset{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	return r.readChangeset(ctx, path, node)
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

// History reads one page of history from the service, which pins the visible
// tip's local revision as the snapshot and follows recorded copies and renames
// for a file Path. Validation that needs no repository runs before the query.
func (r *Runner) History(ctx context.Context, path string, q HistoryQuery) (HistoryPage, error) {
	page := HistoryPage{}
	if q.BeforeRevision < -1 || q.Limit < 0 {
		return page, ErrInvalidCursor
	}
	vars := map[string]interface{}{"before": q.BeforeRevision, "limit": q.Limit}
	if q.Snapshot == "" {
		if q.BeforeRevision != -1 {
			return page, ErrInvalidCursor
		}
	} else {
		node, err := ParseNodeID(string(q.Snapshot))
		if err != nil || q.SnapshotRevision < 0 || q.BeforeRevision > q.SnapshotRevision {
			return page, ErrInvalidCursor
		}
		vars["snapshot"], vars["snapshotRevision"] = string(node), q.SnapshotRevision
	}
	if q.Path != "" {
		if err := checkFile(q.Path); err != nil {
			return page, err
		}
		if !utf8.ValidString(q.Path) {
			return page, ErrUnsupportedEncoding
		}
		vars["path"] = q.Path
	}
	var out struct {
		Repo struct {
			History struct {
				Snapshot         string
				SnapshotRevision int64
				Entries          []gqlChangeset
				NextBefore       int64
				HasMore          bool
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	err := r.readQuery(ctx, path, "History", `query History($repo: String!, $snapshot: NodeId, $snapshotRevision: Int, $before: Int!, $path: GQLPath, $limit: Int!) { repo(path: $repo) { history(snapshot: $snapshot, snapshotRevision: $snapshotRevision, before: $before, path: $path, limit: $limit) { snapshot snapshotRevision nextBefore hasMore entries { `+changesetSelection+` } } } }`, vars, &out)
	if err != nil {
		return page, err
	}
	h := out.Repo.History
	if page.Snapshot, err = ParseNodeID(h.Snapshot); err != nil || h.SnapshotRevision < 0 {
		return HistoryPage{}, fmt.Errorf("%w: invalid history snapshot", ErrReadService)
	}
	page.SnapshotRevision, page.NextBefore, page.HasMore = h.SnapshotRevision, h.NextBefore, h.HasMore
	page.Entries = []Changeset{}
	for _, g := range h.Entries {
		cs, err := g.changeset()
		if err != nil {
			return HistoryPage{}, err
		}
		page.Entries = append(page.Entries, cs)
	}
	return page, nil
}
