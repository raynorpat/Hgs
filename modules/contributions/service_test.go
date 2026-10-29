// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContributionReconcileWhileIndexBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	jobs, ticks := make(chan time.Time), make(chan time.Time, 1)
	indexEntered, reconciled := make(chan struct{}), make(chan struct{})
	releaseIndex, releaseReconcile, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var indexOnce sync.Once
	s := new(Service)
	go func() {
		defer close(done)
		s.run(ctx, jobs, ticks, func(ctx context.Context) error {
			if calls.Add(1) > 1 {
				close(reconciled)
				<-ctx.Done()
				<-releaseReconcile
			}
			return ctx.Err()
		}, func(ctx context.Context) error {
			indexOnce.Do(func() { close(indexEntered) })
			<-ctx.Done()
			<-releaseIndex
			return ctx.Err()
		})
	}()
	defer func() {
		cancel()
		select {
		case <-releaseIndex:
		default:
			close(releaseIndex)
		}
		select {
		case <-releaseReconcile:
		default:
			close(releaseReconcile)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("owned workers did not join")
		}
	}()
	select {
	case <-indexEntered:
	case <-time.After(time.Second):
		t.Fatal("index did not start")
	}
	ticks <- time.Now()
	select {
	case <-reconciled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("reconciliation delayed by blocked repository index")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("returned with workers blocked")
	default:
	}
	close(releaseIndex)
	select {
	case <-done:
		t.Fatal("returned before reconciliation joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseReconcile)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not join both workers")
	}
}

func TestContributionReconcileStartupCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	new(Service).run(ctx, nil, nil, func(context.Context) error { cancel(); return ctx.Err() }, func(context.Context) error { t.Fatal("worker launched after startup cancellation"); return nil })
}
