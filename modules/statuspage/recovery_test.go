package statuspage

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatusPageRecoveryBeforeContext(t *testing.T) {
	Configure("../..", "/code")
	w := httptest.NewRecorder()
	Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("SELECT secret FROM C:/private/repo") })).ServeHTTP(w, httptest.NewRequest("GET", "/code/?token=secret", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "SELECT") || !strings.Contains(w.Body.String(), "Something went wrong") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestStatusPageRecoveryCommitted(t *testing.T) {
	for _, mode := range []string{"write", "flush", "abort", "readfrom"} {
		t.Run(mode, func(t *testing.T) {
			w := httptest.NewRecorder()
			defer func() {
				if recover() != http.ErrAbortHandler {
					t.Error("must abort stream")
				}
				if strings.Contains(w.Body.String(), "Something went wrong") {
					t.Error("rewrote stream")
				}
			}()
			Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "write":
					w.Write([]byte("partial"))
				case "flush":
					http.NewResponseController(w).Flush()
				case "abort":
					panic(http.ErrAbortHandler)
				case "readfrom":
					w.(io.ReaderFrom).ReadFrom(strings.NewReader("partial"))
				}
				panic("late")
			})).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		})
	}
}

type basicResponse struct {
	header http.Header
	code   int
	body   strings.Builder
}

func (w *basicResponse) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *basicResponse) WriteHeader(code int) {
	if code >= 200 {
		w.code = code
	}
}
func (w *basicResponse) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.body.Write(b)
}
func TestStatusPageRecoveryInterimAndCapabilities(t *testing.T) {
	w := &basicResponse{}
	tracked, state := trackResponse(w)
	if _, ok := tracked.(http.Flusher); ok {
		t.Fatal("advertised unsupported Flusher")
	}
	if _, ok := tracked.(http.Hijacker); ok {
		t.Fatal("advertised unsupported Hijacker")
	}
	if _, ok := tracked.(http.Pusher); ok {
		t.Fatal("advertised unsupported Pusher")
	}
	tracked.WriteHeader(103)
	if state.committed {
		t.Fatal("interim header committed response")
	}
	if !errors.Is(http.NewResponseController(tracked).Flush(), http.ErrNotSupported) {
		t.Fatal("lost unsupported flush")
	}
	Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(103); panic("early") })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.code != 500 {
		t.Fatalf("interim response recovered as %d", w.code)
	}
}

type capableResponse struct {
	basicResponse
	conn     net.Conn
	deadline time.Time
}

func (w *capableResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
func (w *capableResponse) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func TestStatusPageRecoveryHijackAndController(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	base := &capableResponse{conn: a}
	tracked, state := trackResponse(base)
	deadline := time.Now().Add(time.Second)
	if err := http.NewResponseController(tracked).SetWriteDeadline(deadline); err != nil || base.deadline != deadline {
		t.Fatalf("controller unwrap %v", err)
	}
	conn, _, err := http.NewResponseController(tracked).Hijack()
	if err != nil || conn != a || !state.committed {
		t.Fatalf("hijack commitment %v %v", state.committed, err)
	}
}
