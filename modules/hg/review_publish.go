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
)

type PublishReviewRequest struct {
	Prepared    PreparedMerge
	TargetPath  string
	Expected    ReviewEndpoint
	OperationID string
}

// ReviewPublicationState inspects only local stores; presence alone is never
// completion for bookmark destinations. Callers must hold durable store fences.
func (r *Runner) ReviewPublicationState(ctx context.Context, q PublishReviewRequest) (imported, complete bool, err error) {
	if err := r.publicationCapability(ctx); err != nil {
		return false, false, err
	}
	p := q.Prepared
	if err := checkReviewJournal(q.TargetPath); err != nil {
		return false, false, err
	}
	if q.TargetPath != q.Expected.Path || p.Target != q.Expected.Node || p.Result == "" {
		return false, false, ErrNotFound
	}
	if _, err := ParseNodeID(string(p.Result)); err != nil {
		return false, false, err
	}
	cs, e := r.reviewChangeset(ctx, q.TargetPath, p.Result)
	// Only a successful, empty native result query proves absence. Inspection
	// failures must keep the target quarantined during terminal reconciliation.
	if e != nil && !errors.Is(e, ErrNotFound) {
		return false, false, e
	}
	if e == nil {
		if cs.Branch != p.DestinationBranch {
			return false, false, ErrNotFound
		}
		switch p.Mode {
		case "two_parent":
			if len(cs.Parents) != 2 || cs.Parents[0] != p.Target || cs.Parents[1] != p.Source {
				return false, false, ErrNotFound
			}
		case "fast_forward":
			if p.Result != p.Source {
				return false, false, ErrNotFound
			}
		case "already_integrated":
			if p.Result != p.Target {
				return false, false, ErrNotFound
			}
		default:
			return false, false, ErrNotFound
		}
		imported = true
	}
	selection := q.Expected.Selection
	if imported && selection.Ref.Kind == RefBranch && selection.Head != "" {
		selection.Head = p.Result
	}
	current, e := r.InspectReviewEndpoint(ctx, q.TargetPath, selection)
	if e != nil {
		return imported, false, e
	}
	if current.Node == p.Result && imported {
		return true, true, nil
	}
	if current.Node != q.Expected.Node {
		return imported, false, ErrNotFound
	}
	if selection.Ref.Kind != RefBookmark && imported {
		return imported, false, ErrNotFound
	}
	return imported, false, nil
}

// PublishReview consists of two native transactions under the hosting service's
// durable reservation: history-only unbundle, then only the selected bookmark.
// Any error is ambiguous and MUST leave that reservation in place.
func (r *Runner) PublishReview(ctx context.Context, q PublishReviewRequest, env map[string]string) error {
	ctx, cancel := r.reviewOperationContext(ctx)
	defer cancel()
	if q.OperationID == "" || env["GOGS_HG_OPERATION_ID"] != q.OperationID || env["GOGS_HG_PUBLICATION_CONFIG"] == "" {
		return errors.New("hg: publication requires an operation-bound hook environment")
	}
	if err := reviewPath(q.Prepared.Path); err != nil {
		return err
	}
	if err := reviewPath(q.TargetPath); err != nil {
		return err
	}
	if err := r.ValidateReviewPublication(ctx, q); err != nil {
		return err
	}
	p := q.Prepared
	if q.Expected.Selection.Ref.Kind != RefBookmark && q.Expected.Selection.Ref.Kind != RefBranch {
		return ErrNotFound
	}
	if q.Expected.Selection.Ref.Kind == RefBookmark && p.Bookmark != q.Expected.Selection.Ref.Name {
		return ErrNotFound
	}
	imported, complete, err := r.ReviewPublicationState(ctx, q)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}
	if !imported {
		// Bundle1 contains only a changegroup: no bookmarks, remote-changegroup
		// URLs, phase mutation parts, or repository configuration.
		dir, err := os.MkdirTemp(filepath.Dir(p.Path), "publish-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		bundle := filepath.Join(dir, "history.hg")
		if err = r.reviewRun(ctx, p.Path, []string{"bundle", "--type", "v1", "--base", string(p.Target), "--rev", string(p.Result), bundle}, nil); err != nil {
			return err
		}
		if err = r.publicationRun(ctx, q.TargetPath, []string{"unbundle", bundle}, env); err != nil {
			return err
		}
	}
	if p.Bookmark != "" {
		if err = CheckPath(p.Bookmark); err != nil {
			return err
		}
		if err = r.publicationRun(ctx, q.TargetPath, []string{"bookmark", "--inactive", "--force", "--rev", string(p.Result), "--", p.Bookmark}, env); err != nil {
			return err
		}
	}
	_, complete, err = r.ReviewPublicationState(ctx, q)
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("hg: publication incomplete")
	}
	return nil
}
func (r *Runner) publicationRun(ctx context.Context, path string, args []string, env map[string]string) error {
	// Use repository-less startup to suppress repository config. The one extra
	// config is created solely by hosting and installs operation-bound hooks.
	args = append([]string{"-R", path}, args...)
	c, err := r.StartWithConfig(ctx, "", args, env, env["GOGS_HG_PUBLICATION_CONFIG"])
	if err != nil {
		return err
	}
	defer c.Stdout.Close()
	defer c.Stderr.Close()
	c.Stdin.Close()
	stderr := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(c.Stderr, maxStderrBytes))
		io.Copy(io.Discard, c.Stderr)
		stderr <- b
	}()
	io.Copy(io.Discard, c.Stdout)
	msg := bytes.TrimSpace(<-stderr)
	if err = c.Wait(); err != nil {
		return fmt.Errorf("hg publication: %w: %s", err, msg)
	}
	return nil
}

// CheckReviewPublicationQuiescent never takes/removes a native lock. A pending
// operation stays fenced when a prior child may still own it or a transaction
// journal needs recovery. This is deliberately fail-closed after process crash.
func CheckReviewPublicationQuiescent(path string) error {
	if err := reviewPath(path); err != nil {
		return err
	}
	for _, name := range []string{filepath.Join(".hg", "store", "lock"), filepath.Join(".hg", "wlock")} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return errors.New("hg: publication store is not quiescent")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return checkReviewJournal(path)
}
func checkReviewJournal(path string) error {
	for _, name := range []string{filepath.Join(".hg", "store", "journal"), filepath.Join(".hg", "journal")} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return errors.New("hg: publication has an interrupted native transaction")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// RecoverReviewPublication is used only for an already-started durable operation
// with current target-write authority and both endpoint fences. Native Mercurial
// tests ownership and reaps stale locks; Go never deletes a lock or journal.
func (r *Runner) RecoverReviewPublication(ctx context.Context, path string) error {
	if err := r.publicationCapability(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserCommandTimeout)
	defer cancel()
	if err := reviewPath(path); err != nil {
		return err
	}
	// With no force options debuglocks acquires/releases both native locks,
	// rejects live/foreign owners and removes only locks Mercurial proves stale.
	if err := r.reviewRun(ctx, path, []string{"--config", "ui.timeout=0", "debuglocks"}, nil); err != nil {
		return err
	}
	if err := checkReviewJournal(path); err != nil {
		if err = r.reviewRun(ctx, path, []string{"--config", "ui.timeout=0", "recover", "--verify"}, nil); err != nil {
			return err
		}
	}
	return CheckReviewPublicationQuiescent(path)
}

// ValidateReviewPublication verifies the private immutable artifact before the
// caller marks a mutation started. Corrupt/internal mismatches cannot import
// history and then fail only at the post-publication check.
func (r *Runner) ValidateReviewPublication(ctx context.Context, q PublishReviewRequest) error {
	if err := r.publicationCapability(ctx); err != nil {
		return err
	}
	p := q.Prepared
	if q.Expected.Path != q.TargetPath || q.Expected.Node != p.Target {
		return ErrNotFound
	}
	if err := reviewPath(p.Path); err != nil {
		return err
	}
	marker := filepath.Join(filepath.Dir(p.Path), reviewMarker)
	fi, err := os.Lstat(marker)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 16384 {
		return ErrUnsafeCheckout
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	var m reviewMetadata
	if json.Unmarshal(data, &m) != nil {
		return ErrUnsafeCheckout
	}
	if _, err = loadReviewMetadata(ReviewSnapshot{Path: p.Path, Source: p.Source, Target: p.Target, Base: m.Base}); err != nil {
		return err
	}
	if m.TargetSelection != q.Expected.Selection {
		return ErrNotFound
	}
	if q.Expected.Selection.Ref.Kind == RefBookmark {
		if p.Bookmark != q.Expected.Selection.Ref.Name {
			return ErrNotFound
		}
	} else if q.Expected.Selection.Ref.Kind != RefBranch || p.Bookmark != "" {
		return ErrNotFound
	}
	target, err := r.reviewChangeset(ctx, p.Path, p.Target)
	if err != nil {
		return err
	}
	if target.Branch != p.DestinationBranch {
		return ErrNotFound
	}
	cs, err := r.reviewChangeset(ctx, p.Path, p.Result)
	if err != nil {
		return err
	}
	if cs.Branch != p.DestinationBranch {
		return ErrNotFound
	}
	switch p.Mode {
	case "two_parent":
		if len(cs.Parents) != 2 || cs.Parents[0] != p.Target || cs.Parents[1] != p.Source {
			return ErrNotFound
		}
	case "fast_forward":
		if p.Result != p.Source || m.Base != p.Target {
			return ErrNotFound
		}
	case "already_integrated":
		if p.Result != p.Target || m.Base != p.Source {
			return ErrNotFound
		}
	default:
		return ErrNotFound
	}
	return nil
}

// publicationCapability validates the configured engine's isolation once per
// runner lifetime. Validation runs before dispatch, never for the first time
// from a callback whose writer already occupies the normal process lane.
func (r *Runner) publicationCapability(ctx context.Context) error {
	if r.publicationIsolationVerified.Load() {
		return nil
	}
	if err := r.reviewCapability(ctx); err != nil {
		return err
	}
	r.publicationIsolationVerified.Store(true)
	return nil
}

// InspectReviewEndpoint uses the same isolated native templates as ResolveReview
// but needs only the requested reference family. No reference name enters a
// revset. Preflight primes the fixed-engine isolation probe before hook use.
func (r *Runner) InspectReviewEndpoint(ctx context.Context, path string, s ReviewSelection) (ReviewEndpoint, error) {
	result := ReviewEndpoint{Path: path, Selection: s}
	if err := r.publicationCapability(ctx); err != nil {
		return result, err
	}
	if s.Ref.Kind == RefNode {
		return r.resolveReview(ctx, path, s)
	}
	var out bytes.Buffer
	var args []string
	switch s.Ref.Kind {
	case RefBranch:
		args = []string{"branches", "--closed", "-T", branchHeadsTemplate}
	case RefBookmark, RefTag:
		args = []string{"log", "-r", referencesRevset, "-T", referencesTemplate}
	default:
		return result, ErrNotFound
	}
	if err := r.reviewRun(ctx, path, args, &out); err != nil {
		return result, err
	}
	refs, err := parseReferences(out.Bytes())
	if err != nil {
		return result, err
	}
	var matches []Reference
	for _, ref := range refs {
		if ref.Kind == s.Ref.Kind && ref.Name == s.Ref.Name {
			matches = append(matches, ref)
		}
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
	if len(matches) == 0 {
		return result, ErrNotFound
	}
	if len(matches) != 1 {
		return result, ErrAmbiguousHead
	}
	result.Node = matches[0].Node
	return result, nil
}
