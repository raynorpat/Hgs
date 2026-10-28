//go:build integration

package integration

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/middleware"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type pullSmallListener struct{ net.Listener }

func (l pullSmallListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e == nil {
		if tcp, ok := c.(*net.TCPConn); ok {
			tcp.SetWriteBuffer(4096)
		}
	}
	return c, e
}

type pullObservedWriter struct {
	http.ResponseWriter
	active, completed *atomic.Int64
}

func (w pullObservedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w pullObservedWriter) Write(p []byte) (int, error) {
	w.active.Add(1)
	defer w.active.Add(-1)
	defer w.completed.Add(1)
	return w.ResponseWriter.Write(p)
}

func TestPullHTTPBlockedDownloadRevocation(t *testing.T) {
	f := seededPullHTTP(t)
	p := f.create(t)
	c, e := f.service.RefreshComparison(context.Background(), f.owner.Id, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, endpoint := range []struct {
		name string
		id   int64
	}{{"source", f.source.Id}, {"target", f.public.Id}} {
		t.Run(endpoint.name, func(t *testing.T) {
			var active, completed atomic.Int64
			released := make(chan struct{})
			parentLive := make(chan bool, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(released)
				observed := pullObservedWriter{w, &active, &completed}
				r = middleware.WithBrowserResponseController(r, observed)
				f.m.ServeHTTP(observed, r)
				parentLive <- r.Context().Err() == nil
				if middleware.BrowserResponseAborted(r) {
					panic(http.ErrAbortHandler)
				}
			}))
			server.Listener = pullSmallListener{server.Listener}
			server.StartTLS()
			defer server.Close()
			addr, _ := net.ResolveTCPAddr("tcp", strings.TrimPrefix(server.URL, "https://"))
			raw, e := net.DialTCP("tcp", nil, addr)
			if e != nil {
				t.Fatal(e)
			}
			raw.SetReadBuffer(1024)
			conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
			defer conn.Close()
			if e = conn.Handshake(); e != nil {
				t.Fatal(e)
			}
			request, _ := http.NewRequest("GET", fmt.Sprintf("%s%s%s/1/comparisons/%d/files/large.txt?side=source", server.URL, f.prefix, f.route, c.ID), nil)
			request.Header.Set("X-Fixture-User", "reader")
			if e = request.Write(conn); e != nil {
				t.Fatal(e)
			}
			response, e := http.ReadResponse(bufio.NewReader(conn), request)
			if e != nil || response.StatusCode != 200 {
				t.Fatal("download start", response, e)
			}
			blocked := false
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				before := completed.Load()
				if active.Load() > 0 {
					time.Sleep(150 * time.Millisecond)
					if active.Load() > 0 && completed.Load() == before {
						blocked = true
						break
					}
				}
			}
			if !blocked {
				t.Fatal("download never reached actual socket backpressure")
			}
			if _, e = f.db.Id(endpoint.id).Cols("is_private").Update(&models.Repository{IsPrivate: true}); e != nil {
				t.Fatal(e)
			}
			defer f.db.Id(endpoint.id).Cols("is_private").Update(&models.Repository{IsPrivate: false})
			select {
			case <-released:
			case <-time.After(8 * time.Second):
				t.Fatal("child revocation did not unblock HTTP Write")
			}
			// The client's request is never canceled or disconnected to trigger this.
			// Its server context can become canceled only once the abort unwinds net/http.
			if live := <-parentLive; !live {
				t.Log("net/http canceled parent after failed socket write; client stayed connected")
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, e = io.ReadAll(response.Body); e == nil {
				t.Fatal("partial response incorrectly completed")
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e = f.host.DrainRepository(ctx, f.source.Id); e != nil {
		t.Fatal("source lease retained", e)
	}
	if e = f.host.DrainRepository(ctx, f.public.Id); e != nil {
		t.Fatal("target lease retained", e)
	}
}
