package hosting

import (
	"context"
	"errors"
	"fmt"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/log"
)

// ForkRepository is the hosting-owned source fence. It refuses any managed
// session rather than cancelling an already authorized native transaction.
func (s *Service) ForkRepository(ctx context.Context, actor, sourceID int64, opts models.CreateHgRepositoryOptions) (*models.Repository, error) {
	return s.forkRepository(ctx, actor, sourceID, opts, false)
}

// StartForkRepository returns the invisible allocation while its bounded worker
// remains owned by the hosting registry. Every later decision uses live policy.
func (s *Service) StartForkRepository(ctx context.Context, actor, sourceID int64, opts models.CreateHgRepositoryOptions) (*models.Repository, error) {
	return s.forkRepository(ctx, actor, sourceID, opts, true)
}
func (s *Service) forkRepository(ctx context.Context, actor, sourceID int64, opts models.CreateHgRepositoryOptions, background bool) (*models.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r := s.sessions
	r.mu.Lock()
	busy := func(reason string) (*models.Repository, error) {
		r.mu.Unlock()
		log.Warn("Fork admission denied repository=%d reason=%s", sourceID, reason)
		return nil, fmt.Errorf("%w: %s", ErrMutationBusy, reason)
	}
	operationLimit := s.cfg.Mercurial.MaxProcesses
	if operationLimit <= 0 {
		operationLimit = hg.DefaultMaxProcesses
	}
	if len(r.activeOperations) >= operationLimit {
		return busy("operation-capacity")
	}
	if r.closed || r.blocked[sourceID] || r.reservations[sourceID] != "" {
		return busy("lifecycle-or-reservation")
	}
	for entry := range r.sessions {
		if entry.grant.RepoID == sourceID {
			if entry.maintenance {
				return busy("maintenance-read")
			}
			return busy("managed-session")
		}
	}
	op, allocation, err := models.BeginForkProvisioning(actor, sourceID, opts)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.reservations[sourceID] = op.ID
	r.activeOperations[op.ID] = true
	limit := s.cfg.Mercurial.SessionTimeout
	if limit <= 0 {
		limit = hg.DefaultSessionTimeout
	}
	if background {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	entry := &trackedSession{grant: hgbridge.Grant{RepoID: sourceID}, cancel: cancel, done: make(chan struct{}), valid: func() bool {
		if models.ValidateRepositoryCredential(actor, 0, 0) != nil || models.ForkOwnerAllowed(actor, opts.OwnerID) != nil {
			return false
		}
		a, err := models.RepositoryAccessFor(actor, sourceID)
		return err == nil && a.Read
	}}
	r.sessions[entry] = struct{}{}
	r.mu.Unlock()
	perform := func() (*models.Repository, error) {
		completed := false
		released := false
		defer func() {
			cancel()
			if !completed {
				cleanupErr := models.RollbackForkProvisioning(s.RepositoryRoot(), op)
				released = cleanupErr == nil
				if cleanupErr != nil {
					_ = models.FailForkProvisioning(op)
					log.Error(4, "Fork operation %s cleanup retained: %v", op.ID, cleanupErr)
				}
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			if completed || released {
				if r.reservations[sourceID] == op.ID {
					delete(r.reservations, sourceID)
				}
			}
			delete(r.activeOperations, op.ID)
			delete(r.sessions, entry)
			close(entry.done)
		}()
		if forkTestHook != nil {
			forkTestHook("allocated", op)
		}
		if err = models.PrepareForkStorage(ctx, s.runner, s.RepositoryRoot(), op); err != nil {
			return nil, err
		}
		if forkTestHook != nil {
			forkTestHook("prepared", op)
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		repo, err := models.PublishFork(ctx, s.RepositoryRoot(), op)
		if err != nil {
			return nil, err
		}
		completed = true
		return repo, nil
	}
	if background {
		go func() {
			if _, err := perform(); err != nil {
				log.Error(4, "Fork operation %s failed: %v", op.ID, err)
			}
		}()
		return allocation, nil
	}
	return perform()
}

// Test-only stage observation permits deterministic revocation/fault races.
var forkTestHook func(string, models.ForkProvisioning)

// Restore typed claims before admission. Unknown or colliding owners fail
// startup closed. Rollback touches only the identified destination allocation.
func (s *Service) reconcileForksAtStartup() error {
	pending, err := models.PendingForkProvisioning()
	if err != nil {
		return err
	}
	for _, op := range pending {
		if op.SourceID <= 0 || op.RepositoryID <= 0 || s.sessions.reservations[op.SourceID] != "" {
			return errors.New("hosting: invalid or colliding fork reservation")
		}
		s.sessions.reservations[op.SourceID] = op.ID
	}
	for _, op := range pending {
		if err = models.RollbackForkProvisioning(s.RepositoryRoot(), op); err != nil {
			return err
		}
		if s.sessions.reservations[op.SourceID] != op.ID {
			return errors.New("hosting: fork reservation owner changed")
		}
		delete(s.sessions.reservations, op.SourceID)
	}
	return nil
}
