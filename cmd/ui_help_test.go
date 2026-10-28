package cmd

import (
	"html"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/setting"
)

func TestSharedHelpAndFooter(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			a := newViewAppPrefix(t, "", prefix)
			setting.Langs, setting.Names = []string{"en-US", "de-DE"}, []string{"English", "Deutsch"}
			a.handler = newWebHandler(a.holder)
			for _, who := range []string{"", "bob", "alice"} {
				w := a.request(t, "GET", prefix+"/help?q=mercurial&page=2&token=secret&password=secret", "", who)
				body := w.Body.String()
				if w.Code != 200 || !strings.Contains(body, `data-local-help`) {
					t.Fatalf("help user=%q status=%d body=%s", who, w.Code, body)
				}
				if strings.Contains(body, `class="hg-rail"`) != (who != "") {
					t.Error("Help shell does not follow account state")
				}
				if !strings.Contains(body, `href="`+prefix+`/help"`) || strings.Contains(body, "secret") {
					t.Error("missing Help navigation or unsafe query copied")
				}
				match := regexp.MustCompile(`<a lang="de-DE" href="([^"]+)"`).FindStringSubmatch(body)
				if len(match) != 2 {
					t.Fatal("German footer link missing")
				}
				link := html.UnescapeString(match[1])
				u, err := url.Parse(link)
				if err != nil || u.Path != prefix+"/help" || u.Query().Get("q") != "mercurial" || u.Query().Get("page") != "2" || u.Query().Get("token") != "" {
					t.Fatalf("unsafe/lost locale context: %s", link)
				}
				selected := a.request(t, "GET", link, "", who)
				if selected.Code != 200 || !strings.Contains(selected.Body.String(), `lang="de-DE"`) || !strings.Contains(selected.Body.String(), ">Hilfe</h1>") || !strings.Contains(selected.Body.String(), "page=2&amp;q=mercurial") {
					t.Fatalf("locale continuation: %d %s", selected.Code, selected.Header().Get("Location"))
				}
				var cookieFound bool
				for _, c := range selected.Result().Cookies() {
					if c.Name == "lang" && c.Value == "de-DE" && (c.Path == prefix || (prefix == "" && c.Path == "/")) {
						cookieFound = true
						r := httptest.NewRequest("GET", "http://host"+prefix+"/help", nil)
						r.AddCookie(c)
						cw := httptest.NewRecorder()
						a.handler.ServeHTTP(cw, r)
						if cw.Code != 200 || !strings.Contains(cw.Body.String(), ">Hilfe</h1>") {
							t.Error("locale cookie selection lost")
						}
					}
				}
				if !cookieFound {
					t.Error("selected language cookie missing or wrong scope")
				}
			}
			setting.Service.RequireSignInView = true
			a.handler = newWebHandler(a.holder)
			if w := a.request(t, "GET", prefix+"/help", "", ""); w.Code != 302 || w.Header().Get("Location") != prefix+"/user/login" {
				t.Fatalf("Help policy: %d %s", w.Code, w.Header().Get("Location"))
			}
			if w := a.request(t, "GET", prefix+"/help", "", "bob"); w.Code != 200 {
				t.Fatalf("signed Help policy: %d", w.Code)
			}
			setting.InstallLock = false
			if w := a.request(t, "GET", prefix+"/help", "", ""); w.Code != 302 || w.Header().Get("Location") != prefix+"/install" {
				t.Fatalf("Help install guard: %d %s", w.Code, w.Header().Get("Location"))
			}
			w := a.request(t, "GET", prefix+"/user/login", "", "")
			if strings.Contains(w.Body.String(), `href="`+prefix+`/help"`) {
				t.Error("preinstall Help advertised")
			}
		})
	}
}
