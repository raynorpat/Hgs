// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// referencesRevset is fixed text; reference names never reach a revset.
// Secret changesets are not served, and hidden ones stay out because no
// browse command passes --hidden.
const referencesRevset = "sort((head() or bookmark() or tag()) and not secret(), -rev)"

// referencesTemplate prints one JSON object per changeset. Mercurial keeps
// branch, bookmark and tag names in UTF-8 and its json filter emits that
// form whatever HGENCODING is, so names arrive unchanged.
const referencesTemplate = `{dict(rev, node, branch, bookmarks, tags, head=ifcontains(rev, revset("head()"), "1"), closed=get(extras, "close"))|json}\n`

// References lists every branch head (closed ones included), bookmark and
// tag, newest revision first, with one Mercurial command.
func (r *Runner) References(ctx context.Context, path string) ([]Reference, error) {
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	var out bytes.Buffer
	if err := r.Run(ctx, path, []string{"log", "-r", referencesRevset, "-T", referencesTemplate}, nil, &out); err != nil {
		return nil, err
	}
	return parseReferences(out.Bytes())
}

func parseReferences(b []byte) ([]Reference, error) {
	// encoding/json replaces invalid UTF-8 with U+FFFD; never silently
	// turn a repository name into a different literal reference.
	if !utf8.Valid(b) {
		return nil, ErrUnsupportedEncoding
	}
	refs := []Reference{}
	d := json.NewDecoder(bytes.NewReader(b))
	for {
		var cs struct {
			Rev             int64
			Node            string
			Branch          string
			Bookmarks, Tags []string
			Head, Closed    string
		}
		if err := d.Decode(&cs); err == io.EOF {
			return refs, nil
		} else if err != nil {
			return nil, fmt.Errorf("hg: unrecognized reference output: %v", err)
		}
		node, err := ParseNodeID(cs.Node)
		if err != nil || cs.Rev < 0 {
			return nil, fmt.Errorf("hg: unrecognized changeset %q in reference output", cs.Node)
		}
		if cs.Head == "1" {
			refs = append(refs, Reference{Kind: RefBranch, Name: cs.Branch, Node: node, Revision: cs.Rev, Closed: cs.Closed == "1"})
		}
		for _, name := range cs.Bookmarks {
			refs = append(refs, Reference{Kind: RefBookmark, Name: name, Node: node, Revision: cs.Rev})
		}
		for _, name := range cs.Tags {
			// tip is Mercurial's moving pseudo-tag, not a tracked tag.
			if name != "tip" {
				refs = append(refs, Reference{Kind: RefTag, Name: name, Node: node, Revision: cs.Rev})
			}
		}
	}
}

// Landing selects the changeset a repository page opens at: the highest open
// head of the default branch, else the highest other open head, else, with
// every head closed, the tip.
func (r *Runner) Landing(ctx context.Context, path string) (NodeID, error) {
	refs, err := r.References(ctx, path)
	if err != nil {
		return "", err
	}
	ref, ok := best(refs, func(ref Reference) bool { return ref.Kind == RefBranch })
	if !ok {
		return "", ErrEmptyRepository
	}
	return ref.Node, nil
}

// best applies the landing preference to the matching references. The tip
// always is a head, so with every head closed the highest one is the tip.
func best(refs []Reference, match func(Reference) bool) (Reference, bool) {
	rank := func(ref Reference) int {
		switch {
		case ref.Closed:
			return 0
		case ref.Name == "default":
			return 2
		}
		return 1
	}
	var b Reference
	found := false
	for _, ref := range refs {
		if match(ref) && (!found || rank(ref) > rank(b) || rank(ref) == rank(b) && ref.Revision > b.Revision) {
			b, found = ref, true
		}
	}
	return b, found
}

// Resolve turns a typed selection into a served changeset. Names are matched
// literally against References; a branch selects its head as Landing would.
func (r *Runner) Resolve(ctx context.Context, path string, ref RefSelection) (NodeID, error) {
	switch ref.Kind {
	case RefNode:
		node, err := ParseNodeID(ref.Name)
		if err == nil {
			err = r.served(ctx, path, node)
		}
		if err != nil {
			return "", err
		}
		return node, nil
	case RefBranch, RefBookmark, RefTag:
	default:
		return "", fmt.Errorf("hg: unknown reference kind %q", ref.Kind)
	}
	refs, err := r.References(ctx, path)
	if err != nil {
		return "", err
	}
	found, ok := best(refs, func(c Reference) bool { return c.Kind == ref.Kind && c.Name == ref.Name })
	if !ok {
		return "", ErrNotFound
	}
	return found.Node, nil
}

// nodeRevset selects node unless it is secret. node is validated hex, so the
// revset holds no caller text.
func nodeRevset(node NodeID) string {
	return "id(" + string(node) + ") and not secret()"
}

// served returns ErrNotFound unless browsing exposes node.
func (r *Runner) served(ctx context.Context, path string, node NodeID) error {
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	var out bytes.Buffer
	if err := r.Run(ctx, path, []string{"log", "-r", nodeRevset(node), "-T", "{node}"}, nil, &out); err != nil {
		return err
	}
	if out.String() != string(node) {
		return ErrNotFound
	}
	return nil
}
