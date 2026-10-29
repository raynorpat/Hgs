package statuspage

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"

	"github.com/gogits/gogs/modules/log"
)

type responseState struct {
	http.ResponseWriter
	committed bool
}

type responseStateKey struct{}

// Committed includes flushes/hijacks which the legacy framework does not track.
func Committed(r *http.Request) bool {
	state, _ := r.Context().Value(responseStateKey{}).(*responseState)
	return state != nil && state.committed
}

func (w *responseState) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseState) WriteHeader(code int) {
	if code >= 200 || code == 101 {
		w.committed = true
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *responseState) Write(b []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(b)
}
func (w *responseState) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{w}, r)
}

type flushState struct{ *responseState }

func (w flushState) Flush() { w.committed = true; w.ResponseWriter.(http.Flusher).Flush() }

type hijackState struct{ *responseState }

func (w hijackState) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, e := w.ResponseWriter.(http.Hijacker).Hijack()
	if e == nil {
		w.committed = true
	}
	return c, b, e
}

// Keep only capabilities supplied by the underlying HTTP transport. Unwrap
// retains ResponseController deadlines and full-duplex support for raw streams.
func trackResponse(w http.ResponseWriter) (http.ResponseWriter, *responseState) {
	s := &responseState{ResponseWriter: w}
	_, f := w.(http.Flusher)
	_, h := w.(http.Hijacker)
	p, hasPush := w.(http.Pusher)
	switch {
	case f && h && hasPush:
		return struct {
			*responseState
			http.Flusher
			http.Hijacker
			http.Pusher
		}{s, flushState{s}, hijackState{s}, p}, s
	case f && h:
		return struct {
			*responseState
			http.Flusher
			http.Hijacker
		}{s, flushState{s}, hijackState{s}}, s
	case f && hasPush:
		return struct {
			*responseState
			http.Flusher
			http.Pusher
		}{s, flushState{s}, p}, s
	case h && hasPush:
		return struct {
			*responseState
			http.Hijacker
			http.Pusher
		}{s, hijackState{s}, p}, s
	case f:
		return struct {
			*responseState
			http.Flusher
		}{s, flushState{s}}, s
	case h:
		return struct {
			*responseState
			http.Hijacker
		}{s, hijackState{s}}, s
	case hasPush:
		return struct {
			*responseState
			http.Pusher
		}{s, p}, s
	}
	return s, s
}
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracked, state := trackResponse(w)
		r = r.WithContext(context.WithValue(r.Context(), responseStateKey{}, state))
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error(4, "HTTP handler failed: %v", v)
				if state.committed {
					panic(http.ErrAbortHandler)
				}
				w.Header().Del("Content-Encoding")
				Render(w, r, Model{Code: 500})
			}
		}()
		next.ServeHTTP(tracked, r)
	})
}
