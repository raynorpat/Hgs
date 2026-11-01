package hg

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func newTestReadClient(t *testing.T, h http.HandlerFunc) *ReadClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewReadClient(filepath.Join(t.TempDir(), "root"))
	c.SetEndpoint(strings.TrimPrefix(srv.URL, "http://"), "tok")
	return c
}

func gqlError(code string) string {
	return `{"data":null,"errors":[{"message":"m","extensions":{"code":"` + code + `"}}]}`
}

func TestQueryDecodesData(t *testing.T) {
	c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"a":{"b":7}}}`))
	})
	var out struct{ A struct{ B int } }
	if err := c.Query(context.Background(), "Op", "query Op { a { b } }", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.A.B != 7 {
		t.Fatalf("got %+v", out)
	}
}

func TestQuerySendsBearerAndOperationName(t *testing.T) {
	var auth, path, body string
	c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		var b bytes.Buffer
		b.ReadFrom(r.Body)
		body = b.String()
		w.Write([]byte(`{"data":{}}`))
	})
	if err := c.Query(context.Background(), "Op", "query Op { a }", map[string]interface{}{"x": 1}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" || path != "/gql" {
		t.Fatalf("auth %q path %q", auth, path)
	}
	for _, want := range []string{`"operationName":"Op"`, `"query":"query Op { a }"`, `"variables":{"x":1}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s lacks %s", body, want)
		}
	}
}

func TestErrorCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"not found", 200, gqlError("NOT_FOUND"), ErrNotFound},
		{"empty", 200, gqlError("EMPTY_REPOSITORY"), ErrEmptyRepository},
		{"cursor", 200, gqlError("INVALID_CURSOR"), ErrInvalidCursor},
		{"internal", 200, gqlError("INTERNAL"), ErrReadService},
		{"unknown", 200, gqlError("WHAT"), ErrReadService},
		{"500", 500, `oops`, ErrReadService},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			})
			err := c.Query(context.Background(), "Op", "query Op { a }", nil, &struct{}{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
	t.Run("refused", func(t *testing.T) {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		addr := l.Addr().String()
		l.Close()
		c := NewReadClient(t.TempDir())
		c.SetEndpoint(addr, "")
		if err := c.Query(context.Background(), "Op", "query Op { a }", nil, &struct{}{}); !errors.Is(err, ErrReadService) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("context", func(t *testing.T) {
		c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) {})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.Query(ctx, "Op", "query Op { a }", nil, &struct{}{}); !errors.Is(err, context.Canceled) || errors.Is(err, ErrReadService) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestStreamErrorBody(t *testing.T) {
	c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(gqlError("NOT_FOUND")))
	})
	var buf bytes.Buffer
	_, err := c.Stream(context.Background(), "/raw", url.Values{"repo": {"r"}}, &buf)
	if !errors.Is(err, ErrNotFound) || buf.Len() != 0 {
		t.Fatalf("err %v, wrote %q", err, buf.String())
	}
}

func TestStreamTruncated(t *testing.T) {
	c := newTestReadClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("repo") != "r" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("bad request %s", r.URL)
		}
		w.Header().Set("X-Truncated", "true")
		w.Write([]byte("abc"))
	})
	var buf bytes.Buffer
	trunc, err := c.Stream(context.Background(), "/diff", url.Values{"repo": {"r"}}, &buf)
	if err != nil || !trunc || buf.String() != "abc" {
		t.Fatalf("trunc %v err %v body %q", trunc, err, buf.String())
	}
}

func TestRelative(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	c := NewReadClient(root)
	got, err := c.relative(filepath.Join(root, "a", "b"))
	if err != nil || got != "a/b" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, p := range []string{filepath.Join(root, "..", "x"), filepath.Dir(root)} {
		if _, err := c.relative(p); err == nil {
			t.Fatalf("%s: want error", p)
		}
	}
}

func TestCloseRefusedConnectionIsReleased(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	root := t.TempDir()
	c := NewReadClient(root)
	c.SetEndpoint(addr, "")
	if err := c.Close(context.Background(), filepath.Join(root, "r")); err != nil {
		t.Fatal(err)
	}
}
