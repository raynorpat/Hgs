package maintenance

import (
	"github.com/gogits/gogs/modules/statuspage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaintenanceAdmission(t *testing.T) {
	cfg := Config{Enabled: true, RetryAfterSeconds: 300, Message: "<b>planned</b>"}
	h, err := New(cfg, "/code", statuspage.New("../..", "/code"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/code/", "/code/api/v1/repos", "/code/hg/owner/private", "/code/user/login", "/code/css/../img/hgs-maintenance.png", "/code/%69mg/hgs-maintenance.png", "/code/img/hgs-maintenance.png/", "/img/hgs-maintenance.png"} {
		for _, method := range []string{"GET", "HEAD", "POST", "OPTIONS"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 503 || w.Header().Get("Retry-After") != "300" {
				t.Errorf("%s %s = %d", method, path, w.Code)
			}
			if strings.Contains(path, "/api/") || strings.Contains(path, "/hg/") {
				if strings.Contains(w.Body.String(), "<html") {
					t.Fatal("HTML transport")
				}
			}
		}
	}
	for _, path := range []string{"/code/css/status.css", "/code/css/app.css", "/code/img/hgs-beaker.svg", "/code/img/hgs-maintenance.png", "/code/status/help"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Errorf("allow %s %d", path, w.Code)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 503 {
			t.Fatal("method bypass")
		}
	}
	_ = http.MethodGet
}
func TestMaintenanceAssetsAndRestart(t *testing.T) {
	cfg := Config{Enabled: true, RetryAfterSeconds: 60}
	h, err := New(cfg, "", statuspage.New("../..", ""))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	cfg.RetryAfterSeconds = 900
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") != "60" {
		t.Fatal("configuration was mutable")
	}
	for _, c := range []Config{{RetryAfterSeconds: 59}, {RetryAfterSeconds: 86401}, {RetryAfterSeconds: 300, Message: strings.Repeat("ü", 301)}, {RetryAfterSeconds: 300, Message: "bad\x00message"}} {
		if c.Validate() == nil {
			t.Fatal("accepted invalid config")
		}
	}
	if (Config{RetryAfterSeconds: 300, Message: strings.Repeat("ü", 300)}).Validate() != nil {
		t.Fatal("Unicode counted as bytes")
	}
}
