package contributions

import (
	"context"
	"testing"
)

func TestContributionVisibleHistory(t *testing.T) {
	called := false
	s := NewService(nil, func(context.Context, int64) (string, error) { called = true; return "unleased", nil }, nil, nil)
	if err := s.IndexRepository(context.Background(), 1); err == nil || called {
		t.Fatalf("index escaped maintenance admission: called=%v err=%v", called, err)
	}
}
