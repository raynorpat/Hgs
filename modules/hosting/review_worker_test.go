package hosting

import (
	"context"
	"errors"
	"github.com/gogits/gogs/modules/hg"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewWorkerBoundAndShutdown(t *testing.T) {
	s := &Service{sessions: newRegistry(), cfg: Config{Mercurial: hg.Config{MaxProcesses: 1}}}
	started, joined := make(chan struct{}), make(chan struct{})
	_, err := s.StartReviewOperation(context.Background(), func() (string, error) { return "first-operation", nil }, func() bool { return true }, func(ctx context.Context) { close(started); <-ctx.Done(); close(joined) })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	allocated := false
	_, err = s.StartReviewOperation(context.Background(), func() (string, error) { allocated = true; return "second-operation", nil }, nil, func(context.Context) {})
	if !errors.Is(err, ErrMutationBusy) || allocated {
		t.Fatal("capacity was not checked before allocation", err, allocated)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.sessions.drain(ctx, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("shutdown returned before worker joined")
	}
}

func TestReviewWorkerRequestEndAndRevocation(t *testing.T) {
	s := &Service{sessions: newRegistry(), cfg: Config{Mercurial: hg.Config{MaxProcesses: 1}}}
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	var allowed atomic.Bool
	allowed.Store(true)
	started, joined := make(chan struct{}), make(chan struct{})
	_, err := s.StartReviewOperation(request, func() (string, error) { return "owned-operation", nil }, allowed.Load, func(ctx context.Context) { close(started); <-ctx.Done(); close(joined) })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	select {
	case <-joined:
		t.Fatal("request end killed owned worker")
	case <-time.After(20 * time.Millisecond):
	}
	allowed.Store(false)
	s.sessions.check()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("revocation did not stop worker")
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if e := s.sessions.drain(ctx, 0); e != nil {
		t.Fatal(e)
	}
	allocated := false
	_, e := s.StartReviewOperation(context.Background(), func() (string, error) { allocated = true; return "late", nil }, nil, func(context.Context) {})
	if e == nil || allocated {
		t.Fatal("closed registry admitted work")
	}
}
