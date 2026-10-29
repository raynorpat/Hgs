//go:build integration

package integration

import (
	"github.com/gogits/gogs/modules/maintenance"
	"github.com/gogits/gogs/modules/statuspage"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusPageMaintenanceStandalone(t *testing.T) {
	p := statuspage.New("../..", "/code")
	h, err := maintenance.New(maintenance.Config{Enabled: true, RetryAfterSeconds: 300}, "/code", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en-US", "de-DE"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/code/?lang="+lang, nil))
		if w.Code != 503 || !strings.Contains(w.Body.String(), `lang="`+lang+`"`) {
			t.Fatalf("locale %s HTTP %d", lang, w.Code)
		}
	}
}
