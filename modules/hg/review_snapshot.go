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
	"os"
	"path/filepath"
	"strings"
)

// reviewRun deliberately uses the repository-less Runner entry point. This sets
// HGRCSKIPREPO before Mercurial opens either side of a local exchange, suppressing
// hgrc AND hgrc-not-shared (including hooks, extensions and merge tools). Normal
// Runner repository commands retain the hosting transaction authorization hooks.
func (r *Runner) reviewRun(ctx context.Context, path string, args []string, out io.Writer) error {
	if path != "" {
		if err := reviewPath(path); err != nil {
			return err
		}
		args = append([]string{"-R", path}, args...)
	}
	return r.Run(ctx, "", args, nil, out)
}

func reviewPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: path is not absolute", ErrUnsafeCheckout)
	}
	if err := CheckPath(path); err != nil {
		return err
	}
	// Refuse symbolic links, junctions and other non-directory ancestors before
	// passing a local path to Mercurial. Callers serialize owned artifact paths.
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 {
			return fmt.Errorf("%w: linked/non-directory path", ErrUnsafeCheckout)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// Probe behavior, not a version string. Older engines which ignore this
// environment variable must refuse review operations before opening input stores.
func (r *Runner) reviewCapability(ctx context.Context) error {
	dir, err := os.MkdirTemp(filepath.Dir(r.cfg.ConfigPath), "review-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	repo := filepath.Join(dir, "repo")
	if err = r.reviewRun(ctx, "", []string{"init", repo}, nil); err != nil {
		return err
	}
	const marker = "review-forbidden-config-marker"
	for _, name := range []string{"hgrc", "hgrc-not-shared"} {
		if err = os.WriteFile(filepath.Join(repo, ".hg", name), []byte("[gogs-review]\nmarker = "+marker+"\n"), 0600); err != nil {
			return err
		}
	}
	var out bytes.Buffer
	if err = r.reviewRun(ctx, repo, []string{"showconfig", "-T", "json"}, &out); err != nil {
		return fmt.Errorf("%w: configuration isolation unavailable: %v", ErrUnsafeCheckout, err)
	}
	if bytes.Contains(out.Bytes(), []byte(marker)) {
		return fmt.Errorf("%w: native engine reads repository configuration", ErrUnsafeCheckout)
	}
	return nil
}

func (r *Runner) reviewReferences(ctx context.Context, path string) ([]Reference, error) {
	var out bytes.Buffer
	if err := r.reviewRun(ctx, path, []string{"branches", "--closed", "-T", branchHeadsTemplate}, &out); err != nil {
		return nil, err
	}
	if err := r.reviewRun(ctx, path, []string{"log", "-r", referencesRevset, "-T", referencesTemplate}, &out); err != nil {
		return nil, err
	}
	return parseReferences(out.Bytes())
}

func (r *Runner) ResolveReview(ctx context.Context, path string, selection ReviewSelection) (ReviewEndpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	if err := r.reviewCapability(ctx); err != nil {
		return ReviewEndpoint{}, err
	}
	return r.resolveReview(ctx, path, selection)
}
func (r *Runner) resolveReview(ctx context.Context, path string, s ReviewSelection) (ReviewEndpoint, error) {
	result := ReviewEndpoint{Path: path, Selection: s}
	if s.Ref.Kind == RefNode {
		n, err := ParseNodeID(s.Ref.Name)
		if err != nil {
			return result, err
		}
		if s.Head != "" && s.Head != n {
			return result, ErrNotFound
		}
		if _, err = r.reviewChangeset(ctx, path, n); err != nil {
			return result, err
		}
		result.Node = n
		return result, nil
	}
	switch s.Ref.Kind {
	case RefBranch, RefBookmark, RefTag:
	default:
		return result, ErrNotFound
	}
	refs, err := r.reviewReferences(ctx, path)
	if err != nil {
		return result, err
	}
	if len(refs) == 0 {
		return result, ErrEmptyRepository
	}
	var matches []Reference
	for _, ref := range refs {
		if ref.Kind == s.Ref.Kind && ref.Name == s.Ref.Name {
			matches = append(matches, ref)
		}
	}
	if len(matches) == 0 {
		return result, ErrNotFound
	}
	if s.Head != "" {
		for _, ref := range matches {
			if ref.Node == s.Head {
				result.Node = ref.Node
				return result, nil
			}
		}
		return result, ErrNotFound
	}
	if len(matches) != 1 {
		return result, ErrAmbiguousHead
	}
	result.Node = matches[0].Node
	return result, nil
}

func (r *Runner) reviewChangeset(ctx context.Context, path string, node NodeID) (Changeset, error) {
	if _, err := ParseNodeID(string(node)); err != nil {
		return Changeset{}, err
	}
	var out bytes.Buffer
	if err := r.reviewRun(ctx, path, []string{"log", "-r", nodeRevset(node), "-T", changesetTemplate}, &out); err != nil {
		return Changeset{}, err
	}
	entries, err := parseChangesets(out.Bytes())
	if err != nil {
		return Changeset{}, err
	}
	if len(entries) == 0 {
		return Changeset{}, ErrNotFound
	}
	if len(entries) != 1 || entries[0].Node != node {
		return Changeset{}, errors.New("hg: unexpected review changeset result")
	}
	return entries[0], nil
}

type reviewMetadata struct {
	Version              int
	Source, Target, Base NodeID
	TargetSelection      ReviewSelection
}

const reviewMarker = "review-artifact.json"

func reviewContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Allocate exclusively. An existing path (including a live store) is never reused.
func newReviewArtifact(output string, inputs ...string) (string, error) {
	if !filepath.IsAbs(output) {
		return "", ErrUnsafeCheckout
	}
	output = filepath.Clean(output)
	if err := reviewPath(filepath.Dir(output)); err != nil {
		return "", err
	}
	for _, input := range inputs {
		if reviewContains(input, output) || reviewContains(output, input) {
			return "", ErrUnsafeCheckout
		}
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return "", err
	}
	return filepath.Join(output, "repo"), nil
}
func writeReviewMetadata(path string, m reviewMetadata) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(path), reviewMarker), data, 0600)
}
func loadReviewMetadata(s ReviewSnapshot) (reviewMetadata, error) {
	m := reviewMetadata{}
	if err := reviewPath(s.Path); err != nil {
		return m, err
	}
	if filepath.Base(s.Path) != "repo" {
		return m, ErrUnsafeCheckout
	}
	marker := filepath.Join(filepath.Dir(s.Path), reviewMarker)
	fi, err := os.Lstat(marker)
	if err != nil {
		return m, ErrUnsafeCheckout
	}
	if !fi.Mode().IsRegular() || fi.Size() > 16384 {
		return m, ErrUnsafeCheckout
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return m, err
	}
	if json.Unmarshal(data, &m) != nil || m.Version != 1 || m.Source != s.Source || m.Target != s.Target || m.Base != s.Base {
		return m, ErrUnsafeCheckout
	}
	for _, n := range []NodeID{s.Source, s.Target, s.Base} {
		if _, err := ParseNodeID(string(n)); err != nil {
			return m, err
		}
	}
	return m, nil
}
func (r *Runner) reviewClone(ctx context.Context, source string, node NodeID, path string) error {
	if err := reviewPath(source); err != nil {
		return err
	}
	if err := r.reviewRun(ctx, "", []string{"clone", "--pull", "-U", "-r", string(node), "--", source, path}, nil); err != nil {
		return err
	}
	// Never retain a generated default path pointing at another internal store.
	if err := os.WriteFile(filepath.Join(path, ".hg", "hgrc"), []byte("# Private Gogs review artifact.\n"), 0600); err != nil {
		return err
	}
	return nil
}
func (r *Runner) reviewBases(ctx context.Context, path string, source, target NodeID) ([]NodeID, error) {
	var out bytes.Buffer
	// heads of the common ancestor closure yields ALL maximal common ancestors,
	// unlike ancestor(a,b), whose tie-breaking would conceal criss-cross history.
	revset := "heads(ancestors(id(" + string(source) + ")) and ancestors(id(" + string(target) + "))) and not secret()"
	if err := r.reviewRun(ctx, path, []string{"log", "-r", revset, "-T", "{node}\n"}, &out); err != nil {
		return nil, err
	}
	var nodes []NodeID
	for _, line := range strings.Fields(out.String()) {
		n, err := ParseNodeID(line)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}
func (r *Runner) SnapshotReview(ctx context.Context, source, target ReviewEndpoint, output string) (ReviewSnapshot, error) {
	ctx, cancel := r.reviewOperationContext(ctx)
	defer cancel()
	result := ReviewSnapshot{}
	if err := r.reviewCapability(ctx); err != nil {
		return result, err
	}
	for _, endpoint := range []ReviewEndpoint{source, target} {
		current, err := r.resolveReview(ctx, endpoint.Path, endpoint.Selection)
		if err != nil {
			return result, err
		}
		if current.Node != endpoint.Node {
			return result, ErrNotFound
		}
	}
	if source.Node == target.Node {
		return result, ErrNoChanges
	}
	path, err := newReviewArtifact(output, source.Path, target.Path)
	if err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(filepath.Dir(path))
		}
	}()
	if err = r.reviewClone(ctx, target.Path, target.Node, path); err != nil {
		return result, err
	}
	// Force permits collecting unrelated endpoints only in the PRIVATE artifact;
	// the all-bases check below reports them as a typed failure and deletes it.
	if err = r.reviewRun(ctx, path, []string{"pull", "--force", "-r", string(source.Node), "--", source.Path}, nil); err != nil {
		return result, err
	}
	// Pull may transfer bookmark names. Private comparisons carry selections in
	// their own metadata; do not accidentally activate or republish unrelated refs.
	refs, err := r.reviewReferences(ctx, path)
	if err != nil {
		return result, err
	}
	for _, ref := range refs {
		if ref.Kind == RefBookmark {
			if err = CheckPath(ref.Name); err != nil {
				return result, err
			}
			if err = r.reviewRun(ctx, path, []string{"bookmark", "--delete", "--", ref.Name}, nil); err != nil {
				return result, err
			}
		}
	}
	bases, err := r.reviewBases(ctx, path, source.Node, target.Node)
	if err != nil {
		return result, err
	}
	if len(bases) == 0 {
		return result, ErrUnrelatedHistory
	}
	if len(bases) != 1 {
		return result, fmt.Errorf("%w: multiple common ancestors; update the source locally", ErrAmbiguousHead)
	}
	result = ReviewSnapshot{Path: path, Source: source.Node, Target: target.Node, Base: bases[0]}
	if err = writeReviewMetadata(path, reviewMetadata{Version: 1, Source: result.Source, Target: result.Target, Base: result.Base, TargetSelection: target.Selection}); err != nil {
		return ReviewSnapshot{}, err
	}
	keep = true
	return result, nil
}

// Make all review commands obey both process and payload bounds. ErrOutputLimit
// is mapped only at comparison boundaries, not confused with a merge conflict.
func reviewComparisonError(err error) error {
	if errors.Is(err, ErrOutputLimit) {
		return ErrComparisonTooLarge
	}
	return err
}

// reviewOperationContext is the overall bound for a private heavy operation.
func (r *Runner) reviewOperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	limit := r.cfg.SessionTimeout
	if limit <= 0 {
		limit = DefaultSessionTimeout
	}
	return context.WithTimeout(ctx, limit)
}
