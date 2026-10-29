package hosting

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type smallSendListener struct{ net.Listener }

func (l smallSendListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		if tcp, ok := c.(*net.TCPConn); ok {
			tcp.SetWriteBuffer(4096)
		}
	}
	return c, err
}

type observedResponse struct {
	http.ResponseWriter
	active, completed *atomic.Int64
}

func (w observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w observedResponse) Write(p []byte) (int, error) {
	w.active.Add(1)
	defer w.active.Add(-1)
	defer w.completed.Add(1)
	return w.ResponseWriter.Write(p)
}
func (w observedResponse) FlushError() error {
	w.active.Add(1)
	defer w.active.Add(-1)
	defer w.completed.Add(1)
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func blockedHTTPResponse(t *testing.T, s *Service) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	var active, completed atomic.Int64
	released := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		s.HTTP().ServeHTTP(observedResponse{w, &active, &completed}, r)
	}))
	server.Listener = smallSendListener{server.Listener}
	server.StartTLS()
	t.Cleanup(server.Close)
	addr := strings.TrimPrefix(server.URL, "https://")
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetReadBuffer(1024)
	conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
	t.Cleanup(func() { conn.Close() })
	if err = conn.Handshake(); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", server.URL+"/alice/project?cmd=getbundle", nil)
	request.SetBasicAuth("alice", "password")
	if err = request.Write(conn); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("response start: %v %v", response, err)
	}
	// Leave this real client connected without reading the large body. Require
	// an observed Write/Flush to remain in flight without progress before cancel.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		before := completed.Load()
		if active.Load() > 0 {
			time.Sleep(150 * time.Millisecond)
			if active.Load() > 0 && completed.Load() == before {
				return server, released
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("response did not reach downstream backpressure")
	return nil, nil
}
func TestHostingDrainInterruptsBlockedHTTPResponse(t *testing.T) {
	s, _, repo := fixture(t)
	_, released := blockedHTTPResponse(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.DrainRepository(ctx, repo.Id); err != nil {
		t.Fatalf("stalled response prevented drain/release: %v", err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler retained stalled response")
	}
}

func TestHostingTimeoutInterruptsBlockedHTTPResponse(t *testing.T) {
	for _, limit := range []string{"idle", "session"} {
		t.Run(limit, func(t *testing.T) {
			s, _, _ := fixtureConfig(t, func(c *Config) {
				c.Mercurial.MaxProcesses = 1
				c.Mercurial.IdleTimeout = time.Minute
				c.Mercurial.SessionTimeout = time.Minute
				if limit == "idle" {
					c.Mercurial.IdleTimeout = 2 * time.Second
				} else {
					c.Mercurial.SessionTimeout = 2 * time.Second
				}
			})
			_, released := blockedHTTPResponse(t, s)
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				t.Fatal("worker timeout did not release stalled downstream response")
			}
			// This must obtain the only process slot without drain or shutdown.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			child, err := s.Runner().Start(ctx, "", []string{"version"}, nil)
			if err != nil {
				t.Fatalf("timeout retained worker admission: %v", err)
			}
			// Admission is the assertion: a separate version command's runtime
			// is not evidence that the previous response retained the sole slot.
			cancel()
			child.Stdin.Close()
			child.Stdout.Close()
			child.Stderr.Close()
			joined := make(chan error, 1)
			go func() { joined <- child.Wait() }()
			select {
			case err := <-joined:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("admitted probe did not cancel cleanly: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("admitted capacity probe did not join")
			}
		})
	}
}
func TestHostingShutdownInterruptsBlockedHTTPResponse(t *testing.T) {
	s, _, _ := fixture(t)
	_, released := blockedHTTPResponse(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired shutdown not reported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung on downstream write")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not release response")
	}
}
func TestHostingHealthySequentialResponsesKeepConnection(t *testing.T) {
	s, _, _ := fixture(t)
	server := httptest.NewTLSServer(s.HTTP())
	defer server.Close()
	client := server.Client()
	for i := 0; i < 3; i++ {
		reused := false
		request, _ := http.NewRequest("GET", server.URL+"/alice/project?cmd=capabilities", nil)
		request.SetBasicAuth("alice", "password")
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(data) != "lookup\n" {
			t.Fatalf("healthy response %d: %d %q %v", i, response.StatusCode, data, err)
		}
		if i > 0 && !reused {
			t.Fatal("normal worker cleanup poisoned the keepalive connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
