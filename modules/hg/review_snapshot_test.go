// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReviewHeavyOperationDeadline(t *testing.T) {
	r := &Runner{cfg: Config{SessionTimeout: 90 * time.Second}}
	ctx, cancel := r.reviewOperationContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 80*time.Second || time.Until(deadline) > 90*time.Second {
		t.Fatalf("heavy operation incorrectly inherits browser limit: deadline in %v", time.Until(deadline))
	}
	parent, parentCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer parentCancel()
	child, childCancel := r.reviewOperationContext(parent)
	defer childCancel()
	select {
	case <-child.Done():
		if !errors.Is(child.Err(), context.DeadlineExceeded) {
			t.Fatalf("caller deadline: %v", child.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("shorter caller deadline not inherited")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	child, childCancel = r.reviewOperationContext(canceled)
	defer childCancel()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("caller cancellation not inherited: %v", child.Err())
	}
}
