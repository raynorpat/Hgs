// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

var (
	ErrStaleRepository = errors.New("hosting: review endpoint changed")
	ErrMutationBusy    = errors.New("hosting: repository mutation busy")
)

type MutationEndpoint struct {
	RepoID    int64
	Selection hg.ReviewSelection
	Node      hg.NodeID
}

// Source authority is read; destination authority is write. Prepared is the
// immutable private result persisted before any history becomes public.
type MutationExpectation struct {
	RepoID      int64
	Selection   hg.ReviewSelection
	Node        hg.NodeID
	OperationID string
	Source      MutationEndpoint
	Prepared    hg.PreparedMerge
}

var operationIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`)

func (s *Service) mutationRequest(e MutationExpectation) hg.PublishReviewRequest {
	p, _ := models.RepositoryStoragePath(s.RepositoryRoot(), e.RepoID)
	return hg.PublishReviewRequest{Prepared: e.Prepared, TargetPath: p, Expected: hg.ReviewEndpoint{Path: p, Selection: e.Selection, Node: e.Node}, OperationID: e.OperationID}
}
func (s *Service) mutationAuthority(actor int64, e MutationExpectation) error {
	if err := models.ValidateRepositoryCredential(actor, 0, 0); err != nil {
		return hgbridge.ErrDenied
	}
	for _, endpoint := range []MutationEndpoint{e.Source, {RepoID: e.RepoID, Selection: e.Selection, Node: e.Node}} {
		a, err := models.RepositoryAccessFor(actor, endpoint.RepoID)
		if err != nil || !a.Read || (endpoint.RepoID == e.RepoID && !a.Write) {
			return hgbridge.ErrDenied
		}
	}
	return nil
}
func (s *Service) mutationCheck(ctx context.Context, actor int64, e MutationExpectation, allowOwnChanges bool) error {
	if err := s.mutationAuthority(actor, e); err != nil {
		return err
	}
	q := s.mutationRequest(e)
	imported, _, err := s.inspector.ReviewPublicationState(ctx, q)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStaleRepository, err)
	}
	// Once our own result replaced a same-store named-branch head, it is no
	// longer possible to resolve the original source head. The durable whole-
	// store fence proves that the only intervening change was this operation.
	if allowOwnChanges && imported && e.Source.RepoID == e.RepoID && e.Source.Selection.Ref.Kind == hg.RefBranch && e.Source.Selection.Ref.Name == e.Prepared.DestinationBranch {
		return nil
	}
	path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), e.Source.RepoID)
	src, err := s.inspector.InspectReviewEndpoint(ctx, path, e.Source.Selection)
	if err != nil || src.Node != e.Source.Node {
		return ErrStaleRepository
	}
	return nil
}

// WithRepositoryMutation reserves both stores or neither. It refuses active
// sessions rather than killing an already-open native transaction. A callback
// error keeps the durable fence, even if the native command actually committed.
// Reusing the same identity is the only recovery entry point; no new grant is
// persisted or inherited. Startup restores reservations before native admission.
func (s *Service) WithRepositoryMutation(ctx context.Context, actor int64, e MutationExpectation, apply func(context.Context, map[string]string) error) error {
	if actor <= 0 || apply == nil || !operationIDPattern.MatchString(e.OperationID) || e.RepoID <= 0 || e.Source.RepoID <= 0 || e.Prepared.Source != e.Source.Node || e.Prepared.Target != e.Node {
		return hgbridge.ErrDenied
	}
	payload, err := json.Marshal(e)
	if err != nil || len(payload) > 16384 {
		return hgbridge.ErrDenied
	}
	if err := s.mutationAuthority(actor, e); err != nil {
		_ = s.reconcileRevokedOperation(e, actor, string(payload))
		return err
	}
	ids := map[int64]bool{e.Source.RepoID: true, e.RepoID: true}
	r := s.sessions
	r.mu.Lock()
	if r.closed || r.activeOperations[e.OperationID] {
		r.mu.Unlock()
		return ErrMutationBusy
	}
	for id := range ids {
		if r.blocked[id] || (r.reservations[id] != "" && r.reservations[id] != e.OperationID) {
			r.mu.Unlock()
			return ErrMutationBusy
		}
	}
	for session := range r.sessions {
		if ids[session.grant.RepoID] {
			r.mu.Unlock()
			return ErrMutationBusy
		}
	}
	complete, err := models.BeginReviewPublication(models.ReviewPublication{ID: e.OperationID, ActorID: actor, Payload: string(payload)}, []int64{e.Source.RepoID, e.RepoID})
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if complete {
		r.mu.Unlock()
		return nil
	}
	for id := range ids {
		r.reservations[id] = e.OperationID
	}
	r.activeOperations[e.OperationID] = true
	limit := s.cfg.Mercurial.SessionTimeout
	if limit <= 0 {
		limit = hg.DefaultSessionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	publication := &trackedSession{grant: hgbridge.Grant{RepoID: e.RepoID}, cancel: cancel, done: make(chan struct{})}
	r.sessions[publication] = struct{}{}
	r.mu.Unlock()
	op, statusErr := models.ReviewPublicationStatus(e.OperationID)
	started := statusErr != nil || op.Started
	completed := false
	terminalInvalid := false
	defer func() {
		cancel()
		if started && !completed && (terminalInvalid || s.mutationAuthority(actor, e) != nil) {
			inspectionCtx, stop := context.WithTimeout(context.Background(), hg.BrowserCommandTimeout)
			_ = s.finishRevokedPublication(inspectionCtx, e, op)
			stop()
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if !started {
			if abortErr := models.AbortReviewPublication(e.OperationID); abortErr == nil {
				for id := range ids {
					delete(r.reservations, id)
				}
			}
		}
		delete(r.activeOperations, e.OperationID)
		delete(r.sessions, publication)
		close(publication.done)
	}()
	if statusErr != nil {
		return statusErr
	}
	for id := range ids {
		path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), id)
		if err := hg.CheckReviewPublicationQuiescent(path); err != nil {
			if !started || id != e.RepoID {
				return err
			}
			if err := s.mutationAuthority(actor, e); err != nil {
				return err
			}
			if err := s.runner.RecoverReviewPublication(ctx, path); err != nil {
				return err
			}
		}
	}
	if err = s.inspector.ValidateReviewPublication(ctx, s.mutationRequest(e)); err != nil {
		terminalInvalid = errors.Is(err, hg.ErrUnsafeCheckout) || errors.Is(err, hg.ErrNotFound) || os.IsNotExist(err)
		return err
	}
	if err = s.mutationCheck(ctx, actor, e, started); err != nil {
		return err
	}
	// All callbacks inspect with a separate bounded runner, never the writer's
	// process semaphore. Otherwise MaxProcesses=1 deadlocks at pretxnopen.
	env, revoke, err := s.controller.IssueOperation(e.OperationID, hgbridge.Grant{Principal: hgbridge.Principal{UserID: actor}, RepoID: e.RepoID, CanWrite: true}, func(checkCtx context.Context, _ hgbridge.Grant) error {
		return s.mutationCheck(checkCtx, actor, e, true)
	})
	if err != nil {
		return err
	}
	defer revoke()
	hooks, err := hgbridge.HookConfig(s.hookExecutable, true)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(hooks))
	for k := range hooks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var config strings.Builder
	config.WriteString("[hooks]\n")
	for _, key := range keys {
		fmt.Fprintf(&config, "%s = %s\n", strings.TrimPrefix(key, "hooks."), hooks[key])
	}
	f, err := os.CreateTemp(filepath.Dir(s.cfg.Mercurial.ConfigPath), "publication-*.rc")
	if err != nil {
		return err
	}
	rc := f.Name()
	defer os.Remove(rc)
	encoded, err := hg.LocalBytes(config.String())
	if err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	env["GOGS_HG_PUBLICATION_CONFIG"] = rc
	env["GOGS_HG_OPERATION_ID"] = e.OperationID
	if err = models.StartReviewPublication(e.OperationID); err != nil {
		return err
	}
	started = true
	if err = apply(ctx, env); err != nil {
		return err
	}
	if err = s.inspector.ValidateReviewPublication(ctx, s.mutationRequest(e)); err != nil {
		terminalInvalid = errors.Is(err, hg.ErrUnsafeCheckout) || errors.Is(err, hg.ErrNotFound) || os.IsNotExist(err)
		return err
	}
	if err = s.mutationCheck(ctx, actor, e, started); err != nil {
		return err
	}
	_, complete, err = s.inspector.ReviewPublicationState(ctx, s.mutationRequest(e))
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("hosting: publication incomplete")
	}
	// Publish the durable success record before allowing any competing session.
	r.mu.Lock()
	defer r.mu.Unlock()
	if err = models.CompleteReviewPublication(e.OperationID); err != nil {
		return err
	}
	completed = true
	for id := range ids {
		delete(r.reservations, id)
	}
	return nil
}

// finishRevokedPublication only observes an operation that cannot continue.
// It never runs recover or mutates either repository on the revoked actor's
// behalf. The caller owns the operation's active registry entry or is startup.
func (s *Service) finishRevokedPublication(ctx context.Context, e MutationExpectation, op models.ReviewPublication) error {
	current, readErr := models.ReviewPublicationStatus(op.ID)
	if readErr != nil {
		return readErr
	}
	if current.Complete || current.Failed || current.Aborted {
		return nil
	}
	op = current
	complete, repair := false, false
	if op.Started {
		q := s.mutationRequest(e)
		if err := hg.CheckReviewPublicationQuiescent(q.TargetPath); err != nil {
			repair = true
		} else {
			_, observed, err := s.inspector.ReviewPublicationState(ctx, q)
			complete = err == nil && observed
			repair = err != nil
		}
	}
	r := s.sessions
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	if !op.Started {
		err = models.AbortReviewPublication(op.ID)
	} else {
		err = models.ConcludeRevokedReviewPublication(op.ID, e.RepoID, complete, repair)
	}
	if err != nil {
		return err
	}
	for id, owner := range r.reservations {
		if owner == op.ID && (!repair || id != e.RepoID) {
			delete(r.reservations, id)
		}
	}
	return nil
}
func (s *Service) reconcileRevokedOperation(e MutationExpectation, actor int64, payload string) error {
	r := s.sessions
	r.mu.Lock()
	if r.closed || r.activeOperations[e.OperationID] {
		r.mu.Unlock()
		return ErrMutationBusy
	}
	op, err := models.ReviewPublicationStatus(e.OperationID)
	if err != nil || op.ActorID != actor || op.Payload != payload || op.Complete || op.Aborted || op.Failed {
		r.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hg.BrowserCommandTimeout)
	entry := &trackedSession{grant: hgbridge.Grant{RepoID: e.RepoID}, cancel: cancel, done: make(chan struct{})}
	r.activeOperations[e.OperationID] = true
	r.sessions[entry] = struct{}{}
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.activeOperations, e.OperationID)
		delete(r.sessions, entry)
		close(entry.done)
		r.mu.Unlock()
	}()
	return s.finishRevokedPublication(ctx, e, op)
}
func (s *Service) reconcileRevokedPublicationsAtStartup(ctx context.Context) error {
	ids := map[string]bool{}
	for _, id := range s.sessions.reservations {
		ids[id] = true
	}
	for id := range ids {
		op, err := models.ReviewPublicationStatus(id)
		if err != nil {
			return err
		}
		if op.Complete || op.Aborted || op.Failed {
			continue
		}
		var e MutationExpectation
		// Corrupt/unrecognized persisted identity stays fenced. Never guess which
		// endpoint was read-only or release a store on untrusted/corrupt metadata.
		if len(op.Payload) > 16384 || json.Unmarshal([]byte(op.Payload), &e) != nil || e.OperationID != id || e.RepoID <= 0 || e.Source.RepoID <= 0 || e.Prepared.Source != e.Source.Node || e.Prepared.Target != e.Node || s.sessions.reservations[e.RepoID] != id {
			continue
		}
		_, artifactErr := os.Lstat(e.Prepared.Path)
		if s.mutationAuthority(op.ActorID, e) != nil || (op.Started && os.IsNotExist(artifactErr)) {
			if err = s.finishRevokedPublication(ctx, e, op); err != nil {
				return err
			}
		}
	}
	return nil
}
