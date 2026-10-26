// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package hgbridge decides, from inside Mercurial transactions, whether a
// session may write. Write-capable sessions carry a capability that the
// internal hook command presents to the parent's loopback control endpoint;
// read-only sessions carry none and get hooks that deny unconditionally.
package hgbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrDenied is returned for every refused write.
var ErrDenied = errors.New("hgbridge: write not authorized")

// Principal is the authenticated actor of a session. A zero UserID is
// anonymous; a zero KeyID or TokenID means that credential is not used.
type Principal struct {
	UserID, KeyID, TokenID int64
}

// Grant is what a session was authorized for when it started.
type Grant struct {
	Principal Principal
	RepoID    int64
	CanWrite  bool
}

// Authorize rechecks that the grant's credential is still valid and that the
// actor still has write access to the repository. Any error denies.
type Authorize func(context.Context, Grant) error

// Controller holds the capabilities of live write sessions and answers the
// hook callbacks for them. Authority exists only in this process's memory.
type Controller struct {
	authorize Authorize
	ttl       time.Duration
	now       func() time.Time

	ln  net.Listener
	srv *http.Server

	mu     sync.Mutex
	closed bool
	grants map[[sha256.Size]byte]session
}

type session struct {
	grant   Grant
	expires time.Time
	touch   func()
}

// NewController starts the loopback control endpoint. ttl bounds every
// capability's life and should equal the session ceiling of the workers it
// is issued to, which the process runner enforces by killing them.
func NewController(authorize Authorize, ttl time.Duration) (*Controller, error) {
	if authorize == nil || ttl <= 0 {
		return nil, errors.New("hgbridge: controller needs an authorizer and a positive lifetime")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("hgbridge: listen for hook callbacks: %v", err)
	}
	c := &Controller{
		authorize: authorize,
		ttl:       ttl,
		now:       time.Now,
		ln:        ln,
		grants:    make(map[[sha256.Size]byte]session),
	}
	c.srv = &http.Server{
		Handler:           c,
		ReadHeaderTimeout: CallbackTimeout,
		ReadTimeout:       CallbackTimeout,
		WriteTimeout:      CallbackTimeout,
	}
	go c.srv.Serve(ln)
	return c, nil
}

// Issue creates the capability of one write-capable session and returns it
// with the GOGS_HG_* environment the session's Mercurial process needs.
// Read-only and anonymous grants get no capability. revoke is idempotent.
//
// touch, if not nil, is called after every allowed check: while Mercurial
// runs transaction hooks its stdio can stay silent for minutes, and the
// callbacks are what show the worker is still making progress.
func (c *Controller) Issue(g Grant, touch func()) (capability string, env map[string]string, revoke func(), err error) {
	if !g.CanWrite || g.Principal.UserID <= 0 || g.RepoID <= 0 {
		return "", nil, nil, errors.New("hgbridge: only an authenticated writer of a repository gets a capability")
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, nil, err
	}
	capability = base64.RawURLEncoding.EncodeToString(raw)
	key := sha256.Sum256([]byte(capability))

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", nil, nil, errors.New("hgbridge: controller is closed")
	}
	now := c.now()
	for k, s := range c.grants {
		if !now.Before(s.expires) {
			delete(c.grants, k)
		}
	}
	c.grants[key] = session{grant: g, expires: now.Add(c.ttl), touch: touch}

	env = map[string]string{
		EnvCapability: capability,
		EnvControl:    c.ln.Addr().String(),
		EnvRepoID:     strconv.FormatInt(g.RepoID, 10),
	}
	revoke = func() {
		c.mu.Lock()
		delete(c.grants, key)
		c.mu.Unlock()
	}
	return capability, env, revoke, nil
}

// lookup finds a live session. Sessions are keyed by the capability's
// SHA-256, so map lookup timing reveals nothing usable about capabilities.
func (c *Controller) lookup(key [sha256.Size]byte) (session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.grants[key]
	if ok && !c.now().Before(s.expires) {
		delete(c.grants, key)
		return session{}, false
	}
	return s, ok && !c.closed
}

// Check allows hook to continue a transaction in repository repoID only if
// capability belongs to a live session for that repository and the
// authorizer confirms the session's current rights.
func (c *Controller) Check(ctx context.Context, capability string, repoID int64, hook string) error {
	if !isHook(hook) {
		return fmt.Errorf("%w: unknown hook %q", ErrDenied, hook)
	}
	key := sha256.Sum256([]byte(capability))
	s, ok := c.lookup(key)
	if !ok {
		return fmt.Errorf("%w: unknown or expired session", ErrDenied)
	}
	if s.grant.RepoID != repoID {
		return fmt.Errorf("%w: session is for another repository", ErrDenied)
	}
	if err := c.authorize(ctx, s.grant); err != nil {
		return fmt.Errorf("%w: %v", ErrDenied, err)
	}
	// The session may have been revoked, or the callback abandoned, while
	// the authorizer ran.
	if _, ok = c.lookup(key); !ok || ctx.Err() != nil {
		return fmt.Errorf("%w: session ended during the check", ErrDenied)
	}
	if s.touch != nil {
		s.touch()
	}
	return nil
}

// Close stops the control endpoint and forgets every capability, so all
// later hook callbacks deny.
func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.grants = make(map[[sha256.Size]byte]session)
	c.mu.Unlock()
	err := c.srv.Shutdown(ctx)
	// Serve may not have registered the listener with srv yet.
	c.ln.Close()
	return err
}
