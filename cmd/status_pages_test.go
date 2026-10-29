package cmd

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/maintenance"
	"github.com/gogits/gogs/modules/setting"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaintenanceHostingAdmission(t *testing.T) {
	old := setting.Maintenance
	t.Cleanup(func() { setting.Maintenance = old })
	setting.Maintenance = maintenance.Config{Enabled: true, RetryAfterSeconds: 300}
	holder := &hostingHolder{}
	if err := holder.Initialize(); !errors.Is(err, maintenance.ErrMaintenance) {
		t.Fatalf("holder: %v", err)
	}
	if holder.Service() != nil || holder.Contributions() != nil || holder.sshListener != nil || holder.contributionDone != nil {
		t.Fatal("maintenance initialized service")
	}
	if s, err := hosting.New(context.Background(), hosting.Config{Maintenance: true}); s != nil || !errors.Is(err, maintenance.ErrMaintenance) {
		t.Fatalf("hosting %v %v", s, err)
	}
}
func TestStatusPageProductionRoutes(t *testing.T) {
	a := newViewApp(t)
	a.repo.IsPrivate = true
	if err := models.UpdateRepository(a.repo); err != nil {
		t.Fatal(err)
	}
	denied := a.request(t, "GET", "/gogs/alice/project", "", "")
	missing := a.request(t, "GET", "/gogs/alice/never-existed", "", "")
	if denied.Code != 404 || missing.Code != 404 || denied.Body.String() != missing.Body.String() {
		t.Fatal("private and missing repositories are distinguishable")
	}
	for _, p := range []string{"/gogs/nonexistent/missing", "/gogs/alice/project-does-not-exist"} {
		w := a.request(t, "GET", p, "", "")
		if w.Code != 404 || !strings.Contains(w.Body.String(), "We couldn&#39;t find that page") {
			t.Fatalf("404 %d %s", w.Code, w.Body.String())
		}
	}
	if err := models.CloseEngine(); err != nil {
		t.Fatal(err)
	}
	w := a.request(t, "GET", "/gogs/explore", "", "")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "Something went wrong") || strings.Contains(w.Body.String(), "database is closed") {
		t.Fatalf("500 %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.handler.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/gogs/explore", nil))
	if w.Code != 404 || w.Body.Len() != 0 {
		t.Fatalf("unregistered HEAD route %d body=%d", w.Code, w.Body.Len())
	}
}
