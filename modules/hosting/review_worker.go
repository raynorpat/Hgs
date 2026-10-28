package hosting

import (
	"context"
	"github.com/gogits/gogs/modules/hg"
)

// StartReviewOperation owns a bounded collaboration worker through shutdown.
// admit is DB-only; an empty ID means a terminal/already-running operation.
// The namespaced entry shares fork admission capacity. Nested publication has
// its own durable-ID entry (conservatively consuming an additional slot).
// RepoID zero is lifecycle tracking only, never a repository grant or fence.
func (s *Service) StartReviewOperation(request context.Context, admit func() (string, error), valid func() bool, perform func(context.Context)) (string, error) {
	if err := request.Err(); err != nil {
		return "", err
	}
	r := s.sessions
	r.mu.Lock()
	limit := s.cfg.Mercurial.MaxProcesses
	if limit <= 0 {
		limit = hg.DefaultMaxProcesses
	}
	if r.closed || len(r.activeOperations) >= limit {
		r.mu.Unlock()
		return "", ErrMutationBusy
	}
	id, err := admit()
	if err != nil || id == "" {
		r.mu.Unlock()
		return id, err
	}
	key := "worker:" + id
	if r.activeOperations[key] {
		r.mu.Unlock()
		return id, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.ReviewOperationTimeout())
	entry := &trackedSession{cancel: cancel, done: make(chan struct{}), valid: valid}
	r.activeOperations[key] = true
	r.sessions[entry] = struct{}{}
	r.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.activeOperations, key)
			delete(r.sessions, entry)
			close(entry.done)
			r.mu.Unlock()
		}()
		perform(ctx)
	}()
	return id, nil
}
