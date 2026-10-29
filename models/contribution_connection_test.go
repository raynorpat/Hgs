// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContributionConnectionDeadlineClamp(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	deadline := time.Now().Add(80 * time.Millisecond)
	conn := &contributionConn{Conn: a, deadline: deadline}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := conn.Read(make([]byte, 1))
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("driver reset removed absolute deadline: %v", err)
	}
}

func TestContributionConnectionBudget(t *testing.T) {
	old := DbCfg
	t.Cleanup(func() { DbCfg = old })
	for _, backend := range []string{"mysql", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var accepted atomic.Int32
			var children sync.WaitGroup
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					accepted.Add(1)
					children.Add(1)
					go func() {
						defer children.Done()
						defer conn.Close()
						conn.SetDeadline(time.Now().Add(2 * time.Second))
						io.Copy(io.Discard, conn)
					}()
				}
			}()
			defer func() { listener.Close(); <-finished; children.Wait() }()
			DbCfg.Type, DbCfg.Host, DbCfg.Name, DbCfg.User, DbCfg.Passwd, DbCfg.SSLMode = backend, listener.Addr().String(), "owned_fixture", "owned_fixture", "", "disable"
			short, long := 200*time.Millisecond, 600*time.Millisecond
			results := make(chan time.Duration, 2)
			for _, budget := range []time.Duration{short, long} {
				go func(budget time.Duration) {
					ctx, cancel := context.WithTimeout(context.Background(), budget)
					defer cancel()
					started := time.Now()
					db, owned, err := contributionDatabase(ctx)
					if err == nil {
						err = db.PingContext(ctx)
						if owned {
							db.Close()
						}
					}
					if err == nil {
						results <- 0
					} else {
						results <- time.Since(started)
					}
				}(budget)
			}
			first, second := <-results, <-results
			if first < short/2 || first > 450*time.Millisecond || second < long/2 || second > 1500*time.Millisecond || accepted.Load() != 2 {
				t.Fatalf("independent deadlines: %s %s, accepted=%d", first, second, accepted.Load())
			}
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			if _, _, err := contributionDatabase(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired budget accepted: %v", err)
			}
			if accepted.Load() != 2 {
				t.Fatal("expired budget redialed")
			}
			// database/sql may reconnect with a fresh context. The connector must
			// retain its original expired budget and avoid opening another socket.
			connector := contributionConnector{backend: backend, deadline: time.Now().Add(-time.Second)}
			for retry := 0; retry < 3; retry++ {
				if _, err := connector.Connect(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("retry renewed budget: %v", err)
				}
			}
			if accepted.Load() != 2 {
				t.Fatal("retry redialed")
			}
		})
	}
}
