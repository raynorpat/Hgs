// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startFakeTree starts a fake hg that has launched a grandchild sharing its stdout.
func startFakeTree(t *testing.T, ctx context.Context, r *Runner, args ...string) (*Child, *bufio.Reader) {
	t.Helper()
	c, err := r.Start(ctx, "", append([]string{"tree"}, args...), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := bufio.NewReader(c.Stdout)
	if line, err := out.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("tree did not start: %q, %v", line, err)
	}
	return c, out
}

// waitGone reports whether Wait returned and the shared stdout reached EOF,
// which happens only after the child and grandchild have both exited.
func waitGone(c *Child, out io.Reader) (waitErr error, childAlive, grandchildAlive bool) {
	waited := make(chan error, 1)
	go func() { waited <- c.Wait() }()
	select {
	case waitErr = <-waited:
	case <-time.After(10 * time.Second):
		return nil, true, true
	}
	eof := make(chan struct{})
	go func() { io.Copy(io.Discard, out); close(eof) }()
	select {
	case <-eof:
	case <-time.After(10 * time.Second):
		grandchildAlive = true
	}
	return waitErr, false, grandchildAlive
}

func TestCancelReapsDescendants(t *testing.T) {
	r := newTestRunner(t, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, out := startFakeTree(t, ctx, r)
	defer c.Stop(context.Background())

	err, childAlive, grandchildAlive := waitGone(c, out)
	if !errors.Is(err, context.DeadlineExceeded) || childAlive || grandchildAlive {
		t.Fatalf("cancellation leaked process or lost deadline: %v", err)
	}
}

func TestExitReapsDescendants(t *testing.T) {
	r := newTestRunner(t, Config{})
	c, out := startFakeTree(t, context.Background(), r, "exit")
	defer c.Stop(context.Background())

	err, childAlive, grandchildAlive := waitGone(c, out)
	if err != nil || childAlive || grandchildAlive {
		t.Fatalf("exit leaked process: %v", err)
	}
}

func TestStopEndsSession(t *testing.T) {
	r := newTestRunner(t, Config{})

	// Closing stdin lets a well-behaved child exit on its own.
	c, err := r.Start(context.Background(), "", []string{"cat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("graceful stop: %v", err)
	}

	c, out := startFakeTree(t, context.Background(), r)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.Stop(ctx); !errors.Is(err, ErrStopped) {
		t.Fatalf("forced stop: %v", err)
	}
	if _, childAlive, grandchildAlive := waitGone(c, out); childAlive || grandchildAlive {
		t.Fatal("forced stop leaked process")
	}
}

func TestIdleAndSessionTimeouts(t *testing.T) {
	r := newTestRunner(t, Config{})
	r.idleTimeout = 300 * time.Millisecond
	c, out := startFakeTree(t, context.Background(), r)
	if err, childAlive, grandchildAlive := waitGone(c, out); !errors.Is(err, ErrIdleTimeout) || childAlive || grandchildAlive {
		t.Fatalf("idle child: %v", err)
	}

	r = newTestRunner(t, Config{SessionTimeout: 500 * time.Millisecond})
	r.idleTimeout = 300 * time.Millisecond
	c, err := r.Start(context.Background(), "", []string{"cat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Steady activity defeats the idle watchdog but not the session ceiling.
	go func() {
		for {
			if _, err := c.Stdin.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	go io.Copy(io.Discard, c.Stdout)
	if err := c.Wait(); !errors.Is(err, ErrSessionTimeout) {
		t.Fatalf("active child: %v", err)
	}
}

func TestProcessLimitQueueTimeout(t *testing.T) {
	r := newTestRunner(t, Config{MaxProcesses: 1})
	r.queueTimeout = 200 * time.Millisecond
	first, err := r.Start(context.Background(), "", []string{"sleep"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if _, err := r.Start(context.Background(), "", []string{"sleep"}, nil); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("full runner did not time out queueing: %v", err)
	}
	if d := time.Since(start); d < r.queueTimeout || d > 5*time.Second {
		t.Fatalf("queue wait = %v, want about %v", d, r.queueTimeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Start(ctx, "", []string{"sleep"}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued start ignored its context: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stopCancel()
	first.Stop(stopCtx)
	next, err := r.Start(context.Background(), "", []string{"cat"}, nil)
	if err != nil {
		t.Fatalf("slot not released after stop: %v", err)
	}
	next.Stop(context.Background())
}

func TestCancelReapsBlockedWriters(t *testing.T) {
	// Start: the caller never reads, so child and grandchild block on full pipes.
	r := newTestRunner(t, Config{MaxProcesses: 1})
	r.queueTimeout = 2 * time.Second
	marker := filepath.Join(filepath.Dir(r.cfg.ConfigPath), "grandchild-started")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := r.Start(ctx, "", []string{"flood", "both"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err, childAlive, grandchildAlive := waitGone(c, c.Stdout)
	if !errors.Is(err, context.DeadlineExceeded) || childAlive || grandchildAlive {
		t.Fatalf("cancellation of blocked writers leaked process or lost deadline: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("grandchild never ran: %v", err)
	}
	c.Stop(context.Background())

	// Run: stderr is drained internally while stdout stays under its bound.
	os.Remove(marker)
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ran := make(chan error, 1)
	go func() { ran <- r.Run(ctx, "", []string{"flood", "err"}, nil, nil) }()
	select {
	case err = <-ran:
	case <-time.After(15 * time.Second):
		t.Fatal("Run hung after its deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run of blocked writers: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("grandchild never ran: %v", err)
	}

	// The single slot is free again.
	next, err := r.Start(context.Background(), "", []string{"cat"}, nil)
	if err != nil {
		t.Fatalf("slot not released: %v", err)
	}
	next.Stop(context.Background())
}
