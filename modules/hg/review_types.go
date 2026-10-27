// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import "errors"

var (
	ErrAmbiguousHead      = errors.New("hg: ambiguous review head")
	ErrNoChanges          = errors.New("hg: no review changes")
	ErrUnrelatedHistory   = errors.New("hg: unrelated review history")
	ErrMergeConflict      = errors.New("hg: review merge conflict; resolve locally")
	ErrUnsafeCheckout     = errors.New("hg: unsafe review checkout; resolve locally")
	ErrComparisonTooLarge = errors.New("hg: review comparison too large")
)

type ReviewSelection struct {
	Ref  RefSelection
	Head NodeID
}

// ReviewEndpoint paths must be internal ID-resolved storage paths, never request values.
type ReviewEndpoint struct {
	Path      string
	Selection ReviewSelection
	Node      NodeID
}

// ReviewSnapshot names an application-owned private artifact, never a published store.
type ReviewSnapshot struct {
	Path                 string
	Source, Target, Base NodeID
}
type ReviewComparison struct {
	Base, Source, Target NodeID
	Changesets           []Changeset
	Diff                 DiffResult
}
type PreparedMerge struct {
	Path                        string
	Source, Target, Result      NodeID
	DestinationBranch, Bookmark string
	Mode                        string
}
