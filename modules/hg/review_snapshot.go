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

// CloneVisible copies the selected local store's exchange-visible history and
// supported references into an independent repository with no working checkout.
// The caller fences source mutations and owns the output's parent allocation.
// Repository configuration is suppressed on both sides of the local exchange.
func (r *Runner) CloneVisible(ctx context.Context, sourcePath, outputPath string) error {
	ctx, cancel := r.reviewOperationContext(ctx)
	defer cancel()
	if err := r.reviewCapability(ctx); err != nil {
		return err
	}
	if err := reviewPath(sourcePath); err != nil {
		return err
	}
	for _, part := range []string{filepath.Join(sourcePath, ".hg"), filepath.Join(sourcePath, ".hg", "store")} {
		if err := reviewPath(part); err != nil {
			return err
		}
	}

	if !filepath.IsAbs(outputPath) || reviewContains(sourcePath, outputPath) || reviewContains(outputPath, sourcePath) {
		return ErrUnsafeCheckout
	}
	if err := reviewPath(filepath.Dir(outputPath)); err != nil {
		return err
	}
	if err := CheckReviewPublicationQuiescent(sourcePath); err != nil {
		return err
	}
	// Exclusive allocation prevents cloning into or cleaning someone else's store.
	if err := os.Mkdir(outputPath, 0700); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(outputPath)
		}
	}()
	if err := r.reviewRun(ctx, "", []string{"clone", "--pull", "-U", "--", sourcePath, outputPath}, nil); err != nil {
		return err
	}
	// Exchange transfers committed tags but not local tags. Copy only parsed,
	// visible tag references, never raw localtags or arbitrary source metadata.
	sourceRefs, err := r.reviewReferences(ctx, sourcePath)
	if err != nil {
		return err
	}
	copiedRefs, err := r.reviewReferences(ctx, outputPath)
	if err != nil {
		return err
	}
	desired := map[string]NodeID{}
	copied := map[string]NodeID{}
	for _, ref := range sourceRefs {
		if ref.Kind == RefTag {
			desired[ref.Name] = ref.Node
		}
	}
	for _, ref := range copiedRefs {
		if ref.Kind == RefTag {
			copied[ref.Name] = ref.Node
		}
	}
	var tags bytes.Buffer
	if err = r.reviewRun(ctx, sourcePath, []string{"tags", "--verbose", "-T", "json"}, &tags); err != nil {
		return err
	}
	var local []struct{ Tag, Node, Type string }
	if err = json.Unmarshal(tags.Bytes(), &local); err != nil {
		return err
	}
	for _, tag := range local {
		if tag.Type != "local" {
			continue
		} // Never recreate untransferred global tag metadata.
		node, err := ParseNodeID(tag.Node)
		if err != nil || desired[tag.Tag] != node {
			continue
		} // Secret/hidden local targets stay out.
		if copied[tag.Tag] == node {
			continue
		}
		if err = CheckPath(tag.Tag); err != nil {
			return err
		}
		if _, err = r.reviewChangeset(ctx, outputPath, node); err != nil {
			return err
		}
		if err = r.reviewRun(ctx, outputPath, []string{"tag", "--local", "--force", "-r", string(node), "--", tag.Tag}, nil); err != nil {
			return err
		}
	}
	tombstones, err := localTagTombstones(sourcePath, copied)
	if err != nil {
		return err
	}
	for name := range tombstones {
		if _, exists := desired[name]; exists {
			continue
		}
		if err = CheckPath(name); err != nil {
			return err
		}
		// Native removal requires a local entry, even when masking a global tag.
		if err = r.reviewRun(ctx, outputPath, []string{"tag", "--local", "--force", "-r", string(copied[name]), "--", name}, nil); err != nil {
			return err
		}
		if err = r.reviewRun(ctx, outputPath, []string{"tag", "--local", "--remove", "--", name}, nil); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(outputPath, ".hg", "hgrc"), []byte("# Managed Hgs repository.\n"), 0600); err != nil {
		return err
	}
	if err := r.reviewRun(ctx, outputPath, []string{"verify"}, nil); err != nil {
		return err
	}
	entries, err := os.ReadDir(outputPath)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != ".hg" {
		return ErrUnsafeCheckout
	}
	for _, name := range []string{"sharedpath", "hgrc-not-shared"} {
		if _, err := os.Lstat(filepath.Join(outputPath, ".hg", name)); !os.IsNotExist(err) {
			return ErrUnsafeCheckout
		}
	}
	keep = true
	return nil
}

// localTagTombstones recognizes only validated null-node records for already
// copied visible tag names. Native tags output supplies live local provenance;
// this bounded metadata read handles deletions omitted from that output. It does
// not copy sidecars or decode arbitrary names, and matches the runner's local
// encoding. Mercurial's last valid record wins.
func localTagTombstones(source string, candidates map[string]NodeID) (map[string]bool, error) {
	result := map[string]bool{}
	if err := reviewPath(filepath.Join(source, ".hg")); err != nil {
		return nil, err
	}
	path := filepath.Join(source, ".hg", "localtags")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return nil, ErrUnsafeCheckout
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1024*1024 {
		return nil, ErrOutputLimit
	}
	names := map[string]string{}
	for name := range candidates {
		encoded, err := LocalBytes(name)
		if err != nil {
			return nil, err
		}
		names[string(encoded)] = name
	}
	null := strings.Repeat("0", 40)
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		parts := bytes.SplitN(line, []byte{' '}, 2)
		if len(parts) != 2 {
			continue
		}
		name, ok := names[string(bytes.TrimSpace(parts[1]))]
		if !ok {
			continue
		}
		node := string(parts[0])
		if node != null {
			if _, err := ParseNodeID(node); err != nil {
				continue
			}
		}
		result[name] = node == null
	}
	for name, deleted := range result {
		if !deleted {
			delete(result, name)
		}
	}
	return result, nil
}

// ValidateReviewSnapshot validates the immutable marker and pins without a
// native log/diff command. It conveys no live repository access or merge grant.
// The caller owns the allocation and must prevent concurrent removal/mutation.
func ValidateReviewSnapshot(s ReviewSnapshot) error {
	_, err := loadReviewMetadata(s)
	if err != nil {
		return err
	}
	if err := reviewPath(filepath.Join(s.Path, ".hg")); err != nil {
		return err
	}
	// Raw cat uses repository-aware execution, so only the application-written
	// private configuration is accepted (no copied hooks or extension config).
	for _, name := range []string{"hgrc", "hgrc-not-shared"} {
		path := filepath.Join(s.Path, ".hg", name)
		fi, err := os.Lstat(path)
		if name == "hgrc-not-shared" && os.IsNotExist(err) {
			continue
		}
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > 1024 {
			return ErrUnsafeCheckout
		}
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "# Private Gogs review artifact.\n" {
			return ErrUnsafeCheckout
		}
	}
	return nil
}
