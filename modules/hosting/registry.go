package hosting

import (
	"context"
	"errors"
	"sync"

	"github.com/gogits/gogs/modules/hgbridge"
)

type trackedSession struct {
	grant       hgbridge.Grant
	cancel      context.CancelFunc
	done        chan struct{}
	valid       func() bool
	maintenance bool
}
type registry struct {
	mu               sync.Mutex
	closed           bool
	blocked          map[int64]bool
	reservations     map[int64]string
	activeOperations map[string]bool
	sessions         map[*trackedSession]struct{}
}

func newRegistry() *registry {
	return &registry{blocked: map[int64]bool{}, reservations: map[int64]string{}, activeOperations: map[string]bool{}, sessions: map[*trackedSession]struct{}{}}
}
func (r *registry) Register(g hgbridge.Grant, cancel context.CancelFunc) (func(), error) {
	return r.register(g, cancel, nil)
}
func (r *registry) register(g hgbridge.Grant, cancel context.CancelFunc, valid func() bool) (func(), error) {
	return r.registerRead(g, cancel, valid, false)
}
func (r *registry) registerRead(g hgbridge.Grant, cancel context.CancelFunc, valid func() bool, maintenance bool) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.blocked[g.RepoID] || r.reservations[g.RepoID] != "" {
		return nil, errors.New("hosting: repository unavailable")
	}
	s := &trackedSession{grant: g, cancel: cancel, done: make(chan struct{}), valid: valid, maintenance: maintenance}
	r.sessions[s] = struct{}{}
	var once sync.Once
	return func() { once.Do(func() { r.mu.Lock(); delete(r.sessions, s); close(s.done); r.mu.Unlock() }) }, nil
}
func (r *registry) drain(ctx context.Context, id int64) error {
	r.mu.Lock()
	if r.reservations[id] != "" {
		r.mu.Unlock()
		return ErrMutationBusy
	}
	if id == 0 {
		r.closed = true
	} else {
		r.blocked[id] = true
	}
	var sessions []*trackedSession
	for s := range r.sessions {
		if id == 0 || s.grant.RepoID == id {
			sessions = append(sessions, s)
		}
	}
	r.mu.Unlock()
	for _, s := range sessions {
		s.cancel()
	}
	return waitSessions(ctx, sessions)
}
func waitSessions(ctx context.Context, sessions []*trackedSession) error {
	for _, s := range sessions {
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (r *registry) check() {
	r.mu.Lock()
	var sessions []*trackedSession
	for s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()
	for _, s := range sessions {
		if s.valid != nil && !s.valid() {
			s.cancel()
		}
	}
}
func (r *registry) stopAdmissions() []*trackedSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	var sessions []*trackedSession
	for s := range r.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}
