// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package integration

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

// TestSupervisionHookCallbackUnderSaturation runs the server with a single
// Mercurial process slot. A writer's hook callback is held while another
// user's request waits for that slot: hooks are not runner processes, so the
// push must complete instead of deadlocking, and the waiting request must
// end within the queue bound, served or refused.
func TestSupervisionHookCallbackUnderSaturation(t *testing.T) {
	v := matrix(t)[0]
	s := newStack(t, v, stackOptions{maxProcesses: 1})
	tp := newTemplate(s)
	for _, transport := range []string{transportHTTP, transportSSH} {
		t.Run(transport, func(t *testing.T) {
			c := newCase(t, s, tp, transport, "writer", "saturated-"+transport)
			c.commit("saturated change")
			readerURL, readerOpts := s.remote(t, transport, "reader", c.name)

			type result struct {
				err     error
				elapsed time.Duration
			}
			waiting := make(chan result, 1)
			var sawWorker atomic.Bool
			s.authz.reset(func(_ context.Context, call int) {
				if call != 1 {
					return
				}
				start := time.Now()
				go func() {
					dest := filepath.Join(s.root, "saturated-reader-"+transport)
					_, err := s.client.run(context.Background(), "", append(append([]string{}, readerOpts...), "clone", readerURL, dest)...)
					waiting <- result{err, time.Since(start)}
				}()
				// Hold the callback well inside its own timeout while the
				// reader queues for the slot this push's worker holds. The
				// process listing must see that worker, or the leftover checks
				// prove nothing.
				all, _ := processes()
				for _, p := range all {
					if strings.Contains(p, c.server) {
						sawWorker.Store(true)
					}
				}
				time.Sleep(time.Until(start.Add(2 * time.Second)))
			})
			defer s.authz.reset(nil)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			start := time.Now()
			out, err := s.client.run(ctx, c.client, append(append([]string{}, c.opts...), "push", c.url)...)
			if err != nil || !pushSucceeded(err, out) {
				t.Fatalf("push with a held hook callback under saturation failed after %v: %v\n%s", time.Since(start), err, out)
			}
			if s.authz.callCount() == 0 {
				t.Fatal("no hook callback was held")
			}
			if !sawWorker.Load() {
				t.Fatal("process listing did not show the running worker")
			}
			if after := snapshotRepository(t, s.server, c.server); !node(after, c.tip()) {
				t.Fatal("push did not land")
			}

			select {
			case r := <-waiting:
				t.Logf("waiting reader clone ended after %v: %v", r.elapsed, r.err)
			case <-time.After(hg.QueueTimeout + 2*hg.StartupTimeout):
				t.Fatal("reader request waiting for a slot did not end within the queue bound")
			}

			// Capacity is back once the push's worker is gone.
			dest := filepath.Join(s.root, "after-saturation-"+transport)
			s.client.must(t, "", append(append([]string{}, readerOpts...), "clone", readerURL, dest)...)
		})
	}
}
