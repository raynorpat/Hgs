package cmd

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type viewSmallSendListener struct{ net.Listener }

func (l viewSmallSendListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		if tcp, ok := c.(*net.TCPConn); ok {
			tcp.SetWriteBuffer(4096)
		}
	}
	return c, err
}

type viewObservedWriter struct {
	http.ResponseWriter
	active, completed *atomic.Int64
}

func (w viewObservedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w viewObservedWriter) Write(p []byte) (int, error) {
	w.active.Add(1)
	defer w.active.Add(-1)
	defer w.completed.Add(1)
	return w.ResponseWriter.Write(p)
}
func TestRawFileStoppedReaderDrains(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	a.fixture(t, "manifest", "644   large\n")
	a.fixture(t, "large", "1")
	var active, completed atomic.Int64
	released := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		a.handler.ServeHTTP(viewObservedWriter{w, &active, &completed}, r)
	}))
	server.Listener = viewSmallSendListener{server.Listener}
	server.StartTLS()
	defer server.Close()
	addr, _ := net.ResolveTCPAddr("tcp", strings.TrimPrefix(server.URL, "https://"))
	raw, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetReadBuffer(1024)
	conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
	defer conn.Close()
	if err = conn.Handshake(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", server.URL+"/gogs/alice/project/raw/"+viewNode+"/large", nil)
	if err = req.Write(conn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("raw response start: %v %v", resp, err)
	}
	blocked := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		before := completed.Load()
		if active.Load() > 0 {
			time.Sleep(150 * time.Millisecond)
			if active.Load() > 0 && completed.Load() == before {
				blocked = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("raw response did not reach socket backpressure")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = a.holder.Service().DrainRepository(ctx, a.repo.Id); err != nil {
		t.Fatalf("stalled raw retained repository read: %v", err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("raw handler did not finish")
	}
}

type viewBlockedHTML struct {
	header         http.Header
	ready, unblock chan struct{}
	once           sync.Once
}

func (w *viewBlockedHTML) Header() http.Header { return w.header }
func (w *viewBlockedHTML) WriteHeader(int)     {}
func (w *viewBlockedHTML) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	<-w.unblock
	return len(p), nil
}
func TestMercurialViewReleasesBeforeBlockedErrorPage(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	a.fixture(t, "changeset", "invalid changeset output\n")
	w := &viewBlockedHTML{header: http.Header{}, ready: make(chan struct{}), unblock: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.handler.ServeHTTP(w, httptest.NewRequest("GET", "http://host/gogs/alice/project/src/"+viewNode, nil))
	}()
	defer func() { close(w.unblock); <-done }()
	select {
	case <-w.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("error page did not reach writer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := a.holder.Service().DrainRepository(ctx, a.repo.Id); err != nil {
		t.Fatalf("completed Hg command retained read during HTML transfer: %v", err)
	}
}
