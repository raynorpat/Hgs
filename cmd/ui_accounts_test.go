package cmd

import (
	"fmt"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"net/url"
	"strings"
	"testing"
)

func TestSharedUIRegisteredAccountMutationMethods(t *testing.T) {
	a := newViewApp(t)
	a.alice.IsAdmin = true
	if err := models.UpdateUser(a.alice); err != nil {
		t.Fatal(err)
	}
	if err := models.CreateRepositoryNotice("disposable notice"); err != nil {
		t.Fatal(err)
	}
	notices, err := models.GetNotices(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	org, err := models.CreateOrganization(&models.User{Name: "lab", Email: "lab@example.test", Type: models.ORGANIZATION, IsActive: true}, a.alice)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{fmt.Sprintf("/gogs/admin/notices/%d/delete", notices[0].Id), fmt.Sprintf("/gogs/org/lab/members/action/leave?uid=%d", a.alice.Id), fmt.Sprintf("/gogs/org/lab/teams/owners/action/leave?uid=%d", a.alice.Id), "/gogs/org/lab/teams/owners/action/repo/remove?repoid=1"}
	for _, path := range paths {
		w := a.request(t, "GET", path, "", "alice")
		if w.Code == 200 || w.Code == 302 {
			t.Errorf("GET registered %s: %d", path, w.Code)
		}
		w = a.request(t, "POST", path, "", "alice")
		if w.Code != 400 {
			t.Errorf("unprotected POST %s: %d", path, w.Code)
		}
	}
	if models.CountNotices() != 1 || !org.IsOwnedBy(a.alice.Id) {
		t.Fatal("denied mutation changed state")
	}
	browser := a.browser(t, "alice")
	w := browser.do(t, "GET", "/gogs/user/settings", nil)
	if !strings.Contains(w.Body.String(), `action="/gogs/user/logout"`) {
		t.Error("shared account menu missing POST logout")
	}
	w = browser.do(t, "GET", "/gogs/user/logout", nil)
	w = browser.do(t, "GET", "/gogs/user/settings", nil)
	if w.Code != 200 {
		t.Fatal("GET logout changed session")
	}
	w = browser.do(t, "POST", "/gogs/user/logout", nil)
	if w.Code != 302 {
		t.Fatal("logout status", w.Code)
	}
	w = browser.do(t, "GET", "/gogs/user/settings", nil)
	if w.Code != 302 {
		t.Fatal("POST logout left session active")
	}
	admin := a.browser(t, "alice")
	w = admin.do(t, "GET", "/gogs/admin/notices", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = admin.do(t, "POST", paths[0], nil)
	if w.Code != 302 || models.CountNotices() != 0 {
		t.Fatal("notice deletion failed", w.Code)
	}
	before := setting.AppUrl
	w = admin.do(t, "POST", "/gogs/admin/config", url.Values{"app_url": {"http://changed.invalid"}})
	if w.Code == 200 || setting.AppUrl != before {
		t.Error("configuration not read-only")
	}
}
