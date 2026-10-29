package statuspage

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStatusPageCodesAndContent(t *testing.T) {
	p := New("../..", "/code")
	for _, code := range []int{404, 500, 503} {
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, "http://localhost/code/private-secret?token=secret", nil)
			w := httptest.NewRecorder()
			err := p.Render(w, r, Model{Code: code, RetryAfterSeconds: 300})
			if err != nil || w.Code != code || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %s: %d %v", code, method, w.Code, err)
			}
			if method == "HEAD" {
				if w.Body.Len() != 0 {
					t.Fatal("HEAD body")
				}
				continue
			}
			for _, want := range []string{"/code/img/hgs-", "/code/status/help", "<html lang=\"en-US\""} {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("missing %s", want)
				}
			}
			if code == 503 && w.Header().Get("Retry-After") != "300" {
				t.Fatal("missing retry")
			}
		}
	}
	r := httptest.NewRequest("GET", "http://localhost/code/no?lang=de-DE", nil)
	w := httptest.NewRecorder()
	p.Render(w, r, Model{Code: 500})
	if !strings.Contains(w.Body.String(), "Etwas ist schiefgelaufen") {
		t.Fatal(w.Body.String())
	}
}
func TestStatusPageNoSensitiveDetails(t *testing.T) {
	p := New("../..", "/code")
	w := httptest.NewRecorder()
	p.Render(w, httptest.NewRequest("POST", "http://localhost/code/private-repo?token=secret", nil), Model{Code: 503, Message: "<script>alert('x')</script>", HomeURL: "https://evil.invalid", RequestID: "/secret/path"})
	for _, bad := range []string{"<script>", "private-repo", "token=secret", "evil.invalid", "/secret/path", "hgs-demo"} {
		if strings.Contains(w.Body.String(), bad) {
			t.Errorf("leaked %s", bad)
		}
	}
}
func TestStatusPageFallback(t *testing.T) {
	for _, p := range []*Renderer{New(t.TempDir(), "/code"), New("../..", "/code")} {
		p.templates = template.Must(template.New("broken").Parse(`{{.Missing.Field}}`))
		w := httptest.NewRecorder()
		err := p.Render(w, httptest.NewRequest("GET", "/", nil), Model{Code: 500})
		if err == nil || w.Code != 500 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || strings.Contains(w.Body.String(), "Missing") {
			t.Fatalf("fallback %d %v %s", w.Code, err, w.Body.String())
		}
	}
}

func TestStatusPageMissingImageAndPreloadedResources(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"templates/status", "public/css", "public/img", "conf/locale"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"templates/status/layout.tmpl", "templates/status/404.tmpl", "templates/status/500.tmpl", "templates/status/503.tmpl", "public/css/app.css", "public/css/status.css", "public/img/hgs-beaker.svg", "public/img/hgs-error.png"} {
		b, e := os.ReadFile(filepath.Join("../..", name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	preloaded := New(root, "/code")
	if err := os.WriteFile(filepath.Join(root, "public/img/hgs-error.png"), []byte("broken PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	for i, p := range []*Renderer{preloaded, New(root, "/code")} {
		w := httptest.NewRecorder()
		err := p.Render(w, httptest.NewRequest("GET", "/", nil), Model{Code: 500})
		if w.Code != 500 {
			t.Fatal(w.Code)
		}
		if i == 0 && err != nil {
			t.Fatal("preloaded image changed")
		}
		if i == 1 && (err == nil || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain")) {
			t.Fatal("corrupt image not safely handled")
		}
	}
}
func TestStatusPageLocalizedUTF8(t *testing.T) {
	p := New("../..", "/code")
	for _, code := range []int{404, 500, 503} {
		for _, lang := range []string{"en-US", "de-DE"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/code/", nil)
			r.AddCookie(&http.Cookie{Name: "lang", Value: lang})
			p.Render(w, r, Model{Code: code})
			if !utf8.Valid(w.Body.Bytes()) || strings.ContainsRune(w.Body.String(), '\uFFFD') || !strings.Contains(w.Body.String(), `lang="`+lang+`"`) {
				t.Fatalf("invalid locale %s/%d", lang, code)
			}
		}
	}
}

func TestStatusPageTransportFailure(t *testing.T) {
	p := New("../..", "/code")
	for _, path := range []string{"/code/api/v1/repos", "/code/hg/private/repo", "/api/v1/repos", "/hg/private/repo"} {
		w := httptest.NewRecorder()
		p.Render(w, httptest.NewRequest("GET", path, nil), Model{Code: 500})
		if w.Code != 500 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || strings.Contains(w.Body.String(), "<html") {
			t.Fatalf("transport failure %s %d %s", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestStatusPageLanguageNavigation(t *testing.T) {
	p := New("../..", "/code")
	for _, code := range []int{404, 500, 503} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/code/private-repo?token=secret&lang=de-DE", nil)
		p.Render(w, r, Model{Code: code})
		for _, want := range []string{`aria-label="Sprache"`, `href="?lang=en-US"`, `href="?lang=de-DE"`, `href="/code/?lang=de-DE"`, `href="/code/status/help?lang=de-DE"`} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("%d missing %s", code, want)
			}
		}
		if strings.Contains(w.Body.String(), "private-repo") || strings.Contains(w.Body.String(), "token=secret") {
			t.Fatal("language navigation reflected request details")
		}
	}
	w := httptest.NewRecorder()
	p.Help(w, httptest.NewRequest("GET", "/code/status/help?lang=de-DE", nil))
	if !strings.Contains(w.Body.String(), "Eine kleine Hilfe") || !strings.Contains(w.Body.String(), `href="/code/?lang=de-DE"`) {
		t.Fatal("help lost language")
	}
}
