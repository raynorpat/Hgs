package cmd

import (
	"context"
	"testing"
	"time"
)

func TestContributionNotifierFailureDoesNotVeto(t *testing.T) {
	if err := hgHook(context.Background(), []string{"notify", "txnclose"}, nil); err != nil {
		t.Fatalf("advisory failure vetoed committed transaction: %v", err)
	}
	if err := hgHook(context.Background(), []string{"check", "pretxnopen"}, nil); err == nil {
		t.Fatal("missing authority accepted by writer gate")
	}
}

func TestContributionHolderJoinsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	release := make(chan struct{})
	stopped := make(chan error, 1)
	h := &hostingHolder{contributionCancel: cancel, contributionDone: done}
	go func() { <-ctx.Done(); <-release; close(done) }()
	go func() { stopped <- h.Shutdown(context.Background()) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("worker not cancelled")
	}
	select {
	case <-stopped:
		t.Fatal("shutdown returned before worker joined")
	default:
	}
	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join worker")
	}
}
