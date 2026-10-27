package collaboration

import (
	"context"
	"errors"
	"github.com/gogits/gogs/modules/hosting"
	"testing"
)

type testProvider struct{ reads int }

func (p *testProvider) Service() *hosting.Service { p.reads++; return nil }
func TestForkUsesCurrentProvider(t *testing.T) {
	p := &testProvider{}
	service := New(p)
	if p.reads != 0 {
		t.Fatal("eager pre-install hosting lookup")
	}
	for i := 0; i < 2; i++ {
		if _, err := service.Fork(context.Background(), 1, ForkOptions{SourceID: 2, OwnerID: 1, Name: "fork"}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unavailable host: %v", err)
		}
	}
	if p.reads != 2 {
		t.Fatal("current provider not queried per fork")
	}
}
