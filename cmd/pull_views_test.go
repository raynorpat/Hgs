package cmd

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPullProductionRoutes(t *testing.T) {
	for _, prefix := range []string{"", "/gogs"} {
		name := "root"
		if prefix != "" {
			name = "suburl"
		}
		t.Run(name, func(t *testing.T) {
			a := newViewAppPrefix(t, "", prefix)
			b := &viewBrowser{a: a, cookies: map[string]*http.Cookie{}}
			b.do(t, "GET", prefix+"/user/login", nil)
			if w := b.do(t, "POST", prefix+"/user/login", url.Values{"uname": {"alice"}, "password": {"password"}}); w.Code != 302 {
				t.Fatal("login", w.Code)
			}
			for _, path := range []string{"/pulls", "/alice/project/pulls", "/alice/project/pulls/new"} {
				w := b.do(t, "GET", prefix+path, nil)
				if w.Code != 200 {
					t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
				}
				if strings.HasSuffix(path, "/new") && !strings.Contains(w.Body.String(), `name="source_repository"`) {
					t.Fatal("exact repository source field missing")
				}
				if !strings.Contains(w.Body.String(), `href="`+prefix+`/alice/project/pulls`) && path != "/pulls" {
					t.Fatal("missing contextual PR navigation")
				}
			}
			for _, path := range []string{"/alice/project/pulls/1", "/alice/project/pulls/1/operations/fake-operation", "/alice/project/pulls/1/comparisons/1/files/README.md?side=source"} {
				if w := b.do(t, "GET", prefix+path, nil); w.Code != 404 {
					t.Fatalf("literal precedence %s: %d", path, w.Code)
				}
			}
			token := b.token
			b.token = ""
			if w := b.do(t, "POST", prefix+"/alice/project/pulls/new", url.Values{}); w.Code != 400 {
				t.Fatal("missing CSRF", w.Code)
			}
			b.token = token
			if w := b.do(t, "POST", prefix+"/alice/project/pulls/new", url.Values{"actor_id": {"999"}}); w.Code != 422 {
				t.Fatal("forged actor", w.Code)
			}
		})
	}
}
