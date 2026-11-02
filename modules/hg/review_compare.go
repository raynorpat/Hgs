// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// CompareReview reads the snapshot through the read service, so the snapshot
// must lie under the served root.
func (r *Runner) CompareReview(ctx context.Context, s ReviewSnapshot) (ReviewComparison, error) {
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	result := ReviewComparison{Base: s.Base, Source: s.Source, Target: s.Target}
	if _, err := loadReviewMetadata(s); err != nil {
		return result, err
	}
	bases, err := r.readReviewBases(ctx, s.Path, s.Source, s.Target)
	if err != nil {
		return result, err
	}
	if len(bases) != 1 || bases[0] != s.Base {
		return result, ErrAmbiguousHead
	}
	var out struct {
		Repo struct{ ChangesetsBetween []gqlChangeset }
	}
	err = r.readQuery(ctx, s.Path, "ChangesetsBetween", `query ChangesetsBetween($repo: String!, $source: NodeId!, $target: NodeId!) { repo(path: $repo) { changesetsBetween(source: $source, target: $target) { `+changesetSelection+` } } }`, map[string]interface{}{"source": string(s.Source), "target": string(s.Target)}, &out)
	if errors.Is(err, ErrOutputLimit) || len(out.Repo.ChangesetsBetween) > MaxReviewChangesets {
		return result, ErrComparisonTooLarge
	}
	if err != nil {
		return result, err
	}
	for _, g := range out.Repo.ChangesetsBetween {
		cs, err := g.changeset()
		if err != nil {
			return result, err
		}
		result.Changesets = append(result.Changesets, cs)
	}
	if len(result.Changesets) == 0 {
		return result, ErrNoChanges
	}
	rel, err := r.reads.relative(s.Path)
	if err != nil {
		return result, err
	}
	w := newDiffWriter()
	truncated, err := r.reads.Stream(ctx, "/diff", url.Values{"repo": {rel}, "from": {string(s.Base)}, "to": {string(s.Source)}, "max": {strconv.Itoa(MaxDiffBytes)}}, w)
	if err != nil && !errors.Is(err, ErrOutputLimit) {
		return result, err
	}
	w.truncated = w.truncated || truncated
	result.Diff = w.result()
	if result.Diff.Truncated {
		return result, ErrComparisonTooLarge
	}
	return result, nil
}
