// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package ssh serves Mercurial repositories over an embedded SSH server. The
// only thing a session can do is run a native client's
// "hg -R <owner>/<repository> serve --stdio", which the server rebuilds
// locally and runs through the same capability and hook model as HTTPS.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/log"
)

// ServiceUser is the only SSH username accepted; keys identify accounts.
const ServiceUser = "hg"

// ErrServerClosed is returned by Serve after Shutdown.
var ErrServerClosed = errors.New("ssh: server closed")

// ResolveKey maps an offered public key to its account. Any error, and any
// principal without a user, refuses the key.
type ResolveKey func(context.Context, ssh.PublicKey) (hgbridge.Principal, error)

// ResolveTarget authorizes principal to read owner/repository and returns
// the repository with the session's grant. Clients only ever see a generic
// denial; the resolver logs whatever failures deserve it.
type ResolveTarget func(ctx context.Context, principal hgbridge.Principal, owner, repository string) (hgbridge.Target, hgbridge.Grant, error)

// Server accepts SSH connections, each allowed one session running one
// Mercurial stdio worker.
type Server struct {
	config *ssh.ServerConfig
	repos  ResolveTarget
	bridge *hgbridge.StdioBridge
	// execTimeout bounds the time from handshake to the command; tests
	// shorten it.
	execTimeout time.Duration

	// base ends every session when Shutdown gives up waiting.
	base   context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	closed    bool
	listeners map[net.Listener]struct{}
	conns     map[net.Conn]struct{}
	active    sync.WaitGroup
}

// NewServer prepares a server presenting signer as its host key. Sessions
// run through bridge, which the caller closes after Shutdown.
func NewServer(signer ssh.Signer, keys ResolveKey, repos ResolveTarget, bridge *hgbridge.StdioBridge) *Server {
	s := &Server{
		repos:       repos,
		bridge:      bridge,
		execTimeout: hg.StartupTimeout,
		listeners:   make(map[net.Listener]struct{}),
		conns:       make(map[net.Conn]struct{}),
	}
	s.base, s.cancel = context.WithCancel(context.Background())
	// Only public keys are offered; with no other callback the library
	// refuses passwords, keyboard-interactive and "none".
	s.config = &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() != ServiceUser {
				return nil, errors.New("ssh: unknown user")
			}
			ctx, cancel := context.WithTimeout(s.base, hg.StartupTimeout)
			defer cancel()
			p, err := keys(ctx, key)
			if err != nil || p.UserID <= 0 {
				return nil, errors.New("ssh: key not accepted")
			}
			// The library attaches these permissions to the key that
			// actually authenticated, not merely to one that was offered.
			return &ssh.Permissions{Extensions: map[string]string{
				"gogs-user-id": strconv.FormatInt(p.UserID, 10),
				"gogs-key-id":  strconv.FormatInt(p.KeyID, 10),
			}}, nil
		},
	}
	s.config.AddHostKey(signer)
	return s
}

// Serve accepts connections on ln until Shutdown or an accept error.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return ErrServerClosed
	}
	s.listeners[ln] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.listeners, ln)
		s.mu.Unlock()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			if conn != nil {
				conn.Close()
			}
			return ErrServerClosed
		}
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.conns[conn] = struct{}{}
		s.active.Add(1)
		s.mu.Unlock()
		go s.serveConn(conn)
	}
}

// Shutdown stops accepting connections and waits for active sessions until
// ctx ends; then it kills their workers, closes their connections and
// returns ctx's error once they are gone.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for ln := range s.listeners {
		ln.Close()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() { s.active.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}
	s.cancel()
	s.mu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.mu.Unlock()
	<-done
	return ctx.Err()
}

func (s *Server) serveConn(conn net.Conn) {
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		s.active.Done()
	}()

	conn.SetDeadline(time.Now().Add(hg.StartupTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		return
	}
	defer sc.Close()
	conn.SetDeadline(time.Time{})
	// Global requests, port forwarding among them, are all refused.
	go ssh.DiscardRequests(reqs)

	var p hgbridge.Principal
	p.UserID, _ = strconv.ParseInt(sc.Permissions.Extensions["gogs-user-id"], 10, 64)
	p.KeyID, _ = strconv.ParseInt(sc.Permissions.Extensions["gogs-key-id"], 10, 64)

	ctx, cancel := context.WithCancel(s.base)
	defer cancel()
	go func() {
		sc.Wait()
		cancel()
	}()
	execTimer := time.AfterFunc(s.execTimeout, func() { sc.Close() })
	defer execTimer.Stop()

	sessionDone := make(chan struct{})
	session := false
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		if session {
			nc.Reject(ssh.Prohibited, "one session per connection")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			return
		}
		session = true
		go func() {
			defer close(sessionDone)
			defer sc.Close()
			s.serveSession(ctx, p, ch, chReqs, execTimer)
		}()
	}
	if session {
		<-sessionDone
	}
}

// serveSession refuses every request until an exec, runs it, and reports
// its exit status.
func (s *Server) serveSession(ctx context.Context, p hgbridge.Principal, ch ssh.Channel, reqs <-chan *ssh.Request, execTimer *time.Timer) {
	defer ch.Close()
	for req := range reqs {
		var exec struct{ Command string }
		if req.Type != "exec" || ssh.Unmarshal(req.Payload, &exec) != nil {
			req.Reply(false, nil)
			continue
		}
		execTimer.Stop()
		req.Reply(true, nil)

		// The client closing the channel ends the session.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			ssh.DiscardRequests(reqs)
			cancel()
		}()
		status := s.exec(ctx, p, ch, exec.Command)
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

func (s *Server) exec(ctx context.Context, p hgbridge.Principal, ch ssh.Channel, cmd string) uint32 {
	owner, repo, err := ParseCommand(cmd)
	if err != nil {
		fmt.Fprintln(ch.Stderr(), `gogs: only "hg -R <owner>/<repository> serve --stdio" is supported`)
		return 1
	}
	target, grant, err := s.repos(ctx, p, owner, repo)
	if err != nil {
		fmt.Fprintf(ch.Stderr(), "gogs: repository %s/%s not found or access denied\n", owner, repo)
		return 1
	}
	code, err := s.bridge.ServeStdio(ctx, target, grant, ch, ch, ch.Stderr())
	if err != nil {
		log.Warn("ssh: serve %s/%s for user %d: %v", owner, repo, p.UserID, err)
		fmt.Fprintln(ch.Stderr(), "gogs: repository session ended unexpectedly")
		return 255
	}
	return uint32(code)
}
