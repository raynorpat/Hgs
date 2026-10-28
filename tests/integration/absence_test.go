//go:build integration

package integration

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A missing shared dependency, duplicate link, or root-relative sub-URL link
// breaks a real rendered page; framework route aliases are not production proof.
func sharedCutoverAssets(t *testing.T, body, prefix string) {
	t.Helper()
	for _, asset := range []string{"/css/app.css", "/js/app/shell.js", "/js/app/forms.js", "/js/app/markdown.js"} {
		if strings.Count(body, `"`+prefix+asset+`"`) != 1 {
			t.Errorf("asset %s not linked exactly once", asset)
		}
	}
	for _, match := range regexp.MustCompile(`(?:href|src)="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		asset := strings.Split(match[1], "#")[0]
		if !strings.HasPrefix(asset, prefix+"/css/") && !strings.HasPrefix(asset, prefix+"/js/") && !strings.HasPrefix(asset, prefix+"/img/ui-icons.svg") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../../public", strings.TrimPrefix(asset, prefix+"/")))
		if err != nil || len(data) == 0 {
			t.Errorf("linked local asset %s: %v", asset, err)
		}
	}
	for _, obsolete := range []string{"/ng/js/", "/ng/css/", "/js/app.js", "/css/bootstrap", "ZeroClipboard"} {
		if strings.Contains(body, obsolete) {
			t.Errorf("shared page loads obsolete %s", obsolete)
		}
	}
}

func TestSharedUICutoverAndRouteMatrix(t *testing.T) {
	t.Run("accounts-admin-auth", func(t *testing.T) {
		f := accountApp(t, true)
		for _, path := range []string{"/admin", "/admin/users", "/admin/users/new", fmt.Sprintf("/admin/users/%d", f.member.Id), "/admin/orgs", "/admin/repos", "/admin/auths", "/admin/auths/new", "/admin/config", "/admin/notices", "/admin/monitor", "/profile/member", "/org/create", "/user/settings", "/user/settings/password", "/user/settings/email", "/user/settings/ssh", "/user/settings/social", "/user/settings/applications", "/user/settings/delete"} {
			w := f.request(t, "GET", path, "administrator", nil, false)
			sharedAccountPage(t, w, false)
			sharedCutoverAssets(t, w.Body.String(), "/code")
			if strings.HasPrefix(path, "/admin") && f.request(t, "GET", path, "member", nil, false).Code != 403 {
				t.Errorf("nonadmin admitted %s", path)
			}
		}
		for _, path := range []string{"/user/login", "/user/sign_up", "/user/forget_password"} {
			w := f.request(t, "GET", path, "", nil, false)
			sharedAccountPage(t, w, true)
			sharedCutoverAssets(t, w.Body.String(), "/code")
		}
	})
	t.Run("issues-labels-milestones", func(t *testing.T) {
		f := issueFixture(t)
		root := "/fixtureowner/empty-lab"
		for _, path := range []string{"/issues", root + "/issues", root + "/issues/new", root + "/issues/milestones", root + "/issues/milestones/new"} {
			w := f.get(t, path, "owner")
			requireUI(t, w, "")
			sharedCutoverAssets(t, w.Body.String(), f.prefix)
		}
	})
	t.Run("install-early-status", func(t *testing.T) {
		f := accountApp(t, false)
		w := f.request(t, "GET", "/install", "", nil, false)
		sharedAccountPage(t, w, true)
		sharedCutoverAssets(t, w.Body.String(), "/code")
		for _, code := range []int{401, 403, 404, 500} {
			w = f.request(t, "GET", fmt.Sprintf("/early/%d", code), "", nil, false)
			if w.Code != code || !strings.Contains(w.Body.String(), `/code/css/app.css`) || strings.Contains(w.Body.String(), `class="hg-rail"`) {
				t.Errorf("early status %d: %d", code, w.Code)
			}
		}
	})
}

func TestSharedUISubURLAndLocales(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		name := "root"
		if prefix != "" {
			name = "subcode"
		}
		t.Run(name, func(t *testing.T) {
			f := newUIFixture(t, prefix)
			for _, lang := range []string{"en-US", "de-DE"} {
				w := f.get(t, "/repo/create?lang="+lang, "owner")
				requireUI(t, w, "")
				sharedCutoverAssets(t, w.Body.String(), prefix)
				if !strings.Contains(w.Body.String(), `lang="`+lang+`"`) || !strings.Contains(w.Body.String(), `action="`+prefix+`/repo/create"`) || strings.Contains(w.Body.String(), ">ui.") {
					t.Errorf("subURL/locale %q %s", prefix, lang)
				}
				asset := httptest.NewRequest("GET", prefix+"/js/app/shell.js", nil)
				response := httptest.NewRecorder()
				f.m.ServeHTTP(response, asset)
				if response.Code != 200 || response.Body.Len() == 0 {
					t.Errorf("served asset %q: %d", prefix, response.Code)
				}
			}
		})
	}
}
