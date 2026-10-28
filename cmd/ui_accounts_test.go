package cmd

import (
	"bytes"
	"fmt"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"image/png"
	"net/http/httptest"
	"net/url"
	"regexp"
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

func TestSharedUIAdminHeadingLocales(t *testing.T) {
	a := newViewApp(t)
	a.alice.IsAdmin = true
	if err := models.UpdateUser(a.alice); err != nil {
		t.Fatal(err)
	}
	setting.Langs = []string{"en-US", "de-DE"}
	setting.Names = []string{"English", "Deutsch"}
	a.handler = newWebHandler(a.holder)
	browser := a.browser(t, "alice")
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://host"+path, nil)
		req.RequestURI = req.URL.RequestURI()
		for _, cookie := range browser.cookies {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, req)
		for _, cookie := range w.Result().Cookies() {
			browser.cookies[cookie.Name] = cookie
		}
		return w
	}
	for _, tc := range []struct{ locale, path, title string }{{"en-US", "/admin/orgs", "Organizations"}, {"en-US", "/admin/config", "Configuration"}, {"de-DE", "/admin/orgs", "Organisationen"}, {"de-DE", "/admin/config", "Konfiguration"}} {
		w := get("/gogs" + tc.path + "?lang=" + tc.locale)
		if w.Code != 302 || w.Header().Get("Location") != "/gogs"+tc.path {
			t.Errorf("locale redirect: %d %s", w.Code, w.Header().Get("Location"))
		}
		w = get("/gogs" + tc.path)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>"+tc.title+"</h1>") {
			t.Errorf("%s %s heading incorrect, status=%d", tc.locale, tc.path, w.Code)
		}
	}
}

func TestSharedUICaptchaImageSubURL(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		name := "root"
		if prefix != "" {
			name = "subcode"
		}
		t.Run(name, func(t *testing.T) {
			a := newViewAppPrefix(t, "", prefix)
			setting.Service.DisableRegistration = false
			get := func(path string) *httptest.ResponseRecorder {
				req := httptest.NewRequest("GET", "http://host"+path, nil)
				req.RequestURI = req.URL.RequestURI()
				w := httptest.NewRecorder()
				a.handler.ServeHTTP(w, req)
				return w
			}
			page := get(prefix + "/user/sign_up")
			image := regexp.MustCompile(`<img[^>]*class="captcha-img"[^>]*src="([^"]+)"`).FindStringSubmatch(page.Body.String())
			if page.Code != 200 || len(image) != 2 || !strings.HasPrefix(image[1], prefix+"/captcha/") {
				t.Fatalf("signup captcha link: %d %v", page.Code, image)
			}
			for _, path := range []string{image[1], image[1] + "?reload=1"} {
				response := get(path)
				if response.Code != 200 {
					t.Fatalf("captcha %s = %d %s", path, response.Code, response.Header().Get("Content-Type"))
				}
				decoded, err := png.Decode(bytes.NewReader(response.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				if decoded.Bounds().Dx() <= 0 || decoded.Bounds().Dy() <= 0 {
					t.Fatal("empty captcha image")
				}
			}
		})
	}
}
