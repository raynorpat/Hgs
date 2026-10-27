// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"errors"
)

func (r *Runner) CompareReview(ctx context.Context, s ReviewSnapshot) (ReviewComparison, error) {
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	result := ReviewComparison{Base: s.Base, Source: s.Source, Target: s.Target}
	if _, err := loadReviewMetadata(s); err != nil {
		return result, err
	}
	if err := r.reviewCapability(ctx); err != nil {
		return result, err
	}
	bases, err := r.reviewBases(ctx, s.Path, s.Source, s.Target)
	if err != nil {
		return result, err
	}
	if len(bases) != 1 || bases[0] != s.Base {
		return result, ErrAmbiguousHead
	}
	var out bytes.Buffer
	revset := "sort(ancestors(id(" + string(s.Source) + ")) - ancestors(id(" + string(s.Target) + ")), rev) and not secret()"
	if err = r.reviewRun(ctx, s.Path, []string{"log", "-r", revset, "-T", changesetTemplate}, &out); err != nil {
		return result, reviewComparisonError(err)
	}
	result.Changesets, err = parseChangesets(out.Bytes())
	if err != nil {
		return result, err
	}
	if len(result.Changesets) == 0 {
		return result, ErrNoChanges
	}
	w := newDiffWriter()
	err = r.reviewRun(ctx, s.Path, []string{"diff", "--git", "--nodates", "--no-binary", "-r", string(s.Base), "-r", string(s.Source)}, w)
	if err != nil && !errors.Is(err, ErrOutputLimit) {
		return result, err
	}
	result.Diff = w.result()
	if result.Diff.Truncated || errors.Is(err, ErrOutputLimit) {
		result.Diff.Truncated = true
		return result, ErrComparisonTooLarge
	}
	return result, nil
}
