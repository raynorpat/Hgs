package middleware

import (
	"compress/gzip"
	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/statuspage"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusPageGzipBeforeContext(t *testing.T) {
	statuspage.Configure("../..", "/code")
	m := macaron.New()
	m.Use(macaron.Gziper())
	m.Use(StatusRecovery())
	m.Use(func() { panic("private SELECT /secret") })
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/code/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	statuspage.Recovery(m).ServeHTTP(w, r)
	if w.Code != 500 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip HTTP %d %v", w.Code, w.Header())
	}
	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Something went wrong") || strings.Contains(string(body), "SELECT") {
		t.Fatalf("unsafe gzip response: %s", body)
	}
}

func TestStatusPageGzipHEAD(t *testing.T) {
	statuspage.Configure("../..", "/code")
	m := macaron.New()
	m.Use(macaron.Gziper())
	m.Use(StatusRecovery())
	m.Use(func() { panic("early") })
	server := httptest.NewServer(statuspage.Recovery(m))
	defer server.Close()
	req, _ := http.NewRequest("HEAD", server.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || len(b) != 0 || resp.StatusCode != 500 || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("HEAD %d %q %v", resp.StatusCode, b, err)
	}
}
func TestStatusPageContextLateFailure(t *testing.T) {
	m := macaron.New()
	m.Use(StatusRecovery())
	m.Get("/", func(c *macaron.Context) {
		c.Resp.Write([]byte("partial"))
		ctx := Context{Context: c}
		ctx.Handle(500, "late", nil)
	})
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Fatal("late failure did not abort")
		}
	}()
	statuspage.Recovery(m).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestStatusPageFlushedBeforeContext(t *testing.T) {
	m := macaron.New()
	m.Use(StatusRecovery())
	m.Use(func(c *macaron.Context) { c.Resp.Flush(); panic("late") })
	w := httptest.NewRecorder()
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("flushed response must abort")
		}
		if w.Body.Len() != 0 {
			t.Error("appended status page to flushed response")
		}
	}()
	statuspage.Recovery(m).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
}
