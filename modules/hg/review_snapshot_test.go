// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// Metadata validation must be cheap enough for a raw file read and refuse
// mismatched pins or imported repository configuration before native cat.
func TestValidateReviewSnapshotPinsAndConfiguration(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".hg"), 0700); err != nil {
		t.Fatal(err)
	}
	s := ReviewSnapshot{Path: repo, Source: NodeID(strings.Repeat("1", 40)), Target: NodeID(strings.Repeat("2", 40)), Base: NodeID(strings.Repeat("3", 40))}
	if err := writeReviewMetadata(repo, reviewMetadata{Version: 1, Source: s.Source, Target: s.Target, Base: s.Base}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".hg", "hgrc")
	if err := os.WriteFile(config, []byte("# Private Gogs review artifact.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReviewSnapshot(s); err != nil {
		t.Fatal(err)
	}
	changed := s
	changed.Source = changed.Target
	if err := ValidateReviewSnapshot(changed); err == nil {
		t.Fatal("mismatched pins accepted")
	}
	if err := os.WriteFile(config, []byte("[extensions]\nunsafe = /outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReviewSnapshot(s); err == nil {
		t.Fatal("non-application configuration accepted")
	}
}
