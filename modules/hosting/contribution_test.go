package hosting

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestContributionMaintenance(t *testing.T) {
	s, _, repo := fixture(t)
	entered := make(chan struct{})
	released := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.WithMaintenanceRead(context.Background(), repo.Id, func(ctx context.Context, path string) error {
			if path == "" {
				return errors.New("empty admitted path")
			}
			close(entered)
			<-ctx.Done()
			<-released
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("admission stalled")
	}
	drained := make(chan error, 1)
	go func() { drained <- s.DrainRepository(context.Background(), repo.Id) }()
	select {
	case err := <-drained:
		t.Fatalf("drain released running callback: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(released)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("callback cancellation: %v", err)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if err := s.WithMaintenanceRead(context.Background(), repo.Id, func(context.Context, string) error { t.Fatal("drained store admitted"); return nil }); err == nil {
		t.Fatal("admitted drained repository")
	}
}
