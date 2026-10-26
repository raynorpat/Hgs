// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"errors"
	"strings"
)

var (
	ErrEmptyRepository = errors.New("hg: repository has no changesets")
	ErrNotFound        = errors.New("hg: no such reference, changeset or file")
	// ErrUnsupportedEncoding reports a name Mercurial's command line cannot
	// take or give back unchanged on every platform.
	ErrUnsupportedEncoding = errors.New("hg: name cannot be passed to Mercurial losslessly")
)

// NodeID is a full 40-hex changeset ID.
type NodeID string

// ParseNodeID accepts a full changeset ID in either case. The null and
// working-directory pseudo IDs are not changesets and yield ErrNotFound.
func ParseNodeID(s string) (NodeID, error) {
	s = strings.ToLower(s)
	if len(s) != 40 || strings.Trim(s, "0123456789abcdef") != "" ||
		s == strings.Repeat("0", 40) || s == strings.Repeat("f", 40) {
		return "", ErrNotFound
	}
	return NodeID(s), nil
}

type RefKind string

const (
	RefBranch   RefKind = "branch"
	RefBookmark RefKind = "bookmark"
	RefTag      RefKind = "tag"
	RefNode     RefKind = "node"
)

// RefSelection is a typed, literal reference name; it is never a revset.
type RefSelection struct {
	Kind RefKind
	Name string
}

// Reference is one branch head, bookmark or tag. A named branch with several
// heads has one Reference per head.
type Reference struct {
	Kind     RefKind
	Name     string
	Node     NodeID
	Revision int64
	// Closed marks a branch head that closes its branch.
	Closed bool
}

// ManifestEntry is a file in a changeset. Path holds the repository's raw
// path bytes, which need not be UTF-8.
type ManifestEntry struct {
	Path       string
	Executable bool
	Symlink    bool
}
