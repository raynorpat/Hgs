package hgbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type registrarFunc func(Grant, context.CancelFunc) (func(), error)

func (f registrarFunc) Register(g Grant, c context.CancelFunc) (func(), error) { return f(g, c) }
func TestHTTPLifecycleCancelsReadyReadersAndWriters(t *testing.T) {
	for _, grant := range []Grant{testReader, testWriter} {
		started := make(chan struct{})
		b, _, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })
		var cancel context.CancelFunc
		released := make(chan struct{})
		b.Registrar = registrarFunc(func(g Grant, c context.CancelFunc) (func(), error) {
			if g != grant {
				t.Errorf("wrong grant: %+v", g)
			}
			cancel = c
			return func() { close(released) }, nil
		})
		done := make(chan error, 1)
		go func() {
			done <- b.Serve(context.Background(), httptest.NewRecorder(), httptest.NewRequest("GET", "http://host/?cmd=capabilities", nil), testTarget, grant)
		}()
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("serve ended before backend: %v", err)
		case <-time.After(time.Second):
			t.Fatal("no backend")
		}
		cancel()
		select {
		case <-released:
		case <-time.After(time.Second):
			t.Fatal("targeted cancellation did not release worker")
		}
		<-done
		if starts, reaps := f.count(); starts != 1 || reaps != 1 {
			t.Fatalf("starts=%d reaps=%d", starts, reaps)
		}
	}
}

func TestHTTPLifecycleCancellationBeforeStart(t *testing.T) {
	b, _, workers := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled worker received request") })
	released := false
	b.Registrar = registrarFunc(func(g Grant, cancel context.CancelFunc) (func(), error) {
		cancel()
		return func() { released = true }, nil
	})
	err := b.Serve(context.Background(), httptest.NewRecorder(), httptest.NewRequest("GET", "http://host/?cmd=capabilities", nil), testTarget, testReader)
	if err == nil || !released {
		t.Fatalf("canceled admission: %v release=%v", err, released)
	}
	if started, _ := workers.count(); started != 0 {
		t.Fatalf("canceled admission started %d workers", started)
	}
}
