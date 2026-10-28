package middleware

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	core "github.com/Unknwon/i18n"
	"github.com/Unknwon/macaron"
	"github.com/macaron-contrib/i18n"
)

func TestLocalizationLayersEnglishFallback(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(runtime.GC)
	custom := filepath.Join(dir, "custom")
	if err := os.Mkdir(custom, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"locale_en-US.ini":        "[ui]\nmissing = English fallback\nkeep = English original\nsame = English default\nformat = %s has %d issues\n",
		"locale_qf-AA.ini":        "[ui]\nkeep = First translation\nsame = same\nformat = %s owns %d translated issues\n",
		"locale_qf-BB.ini":        "[ui]\nkeep = Second translation\n",
		"custom/locale_qf-AA.ini": "[ui]\nkeep = Custom override\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := macaron.New()
	m.SetURLPrefix("/code")
	m.Use(Localization(i18n.Options{Directory: dir, CustomDirectory: custom, Langs: []string{"qf-AA", "qf-BB"}, Names: []string{"First", "Second"}, SubURL: "/code", Parameter: "locale"}))
	m.Get("/", func(ctx *macaron.Context) {
		json.NewEncoder(ctx.Resp).Encode(map[string]string{"missing": ctx.Tr("ui.missing"), "keep": ctx.Tr("ui.keep"), "same": ctx.Tr("ui.same"), "format": ctx.Tr("ui.format", "Pat", 3), "lang": ctx.Locale.Language()})
	})
	request := httptest.NewRequest("GET", "/code/?locale=qf-AA", nil)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, request)
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err, w.Body.String())
	}
	for key, want := range map[string]string{"missing": "English fallback", "keep": "Custom override", "same": "same", "format": "Pat owns 3 translated issues", "lang": "qf-AA"} {
		if got[key] != want {
			t.Errorf("%s=%q want %q", key, got[key], want)
		}
	}
	if core.IndexLang("qf-BB") != core.IndexLang("qf-AA")+1 || core.GetDescriptionByLang("qf-AA") != "First" {
		t.Fatal("configured locale order/description lost")
	}
	second := httptest.NewRecorder()
	m.ServeHTTP(second, httptest.NewRequest("GET", "/code/?locale=qf-BB", nil))
	if err := json.Unmarshal(second.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["keep"] != "Second translation" || got["format"] != "Pat has 3 issues" {
		t.Fatal("locale/fallback formatting", got)
	}
	cookieRequest := httptest.NewRequest("GET", "/code/", nil)
	for _, cookie := range second.Result().Cookies() {
		if cookie.Name == "lang" {
			if cookie.Path != "/code" {
				t.Fatal("locale cookie path changed")
			}
			cookieRequest.AddCookie(cookie)
		}
	}
	cookieResponse := httptest.NewRecorder()
	m.ServeHTTP(cookieResponse, cookieRequest)
	if err := json.Unmarshal(cookieResponse.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["lang"] != "qf-BB" {
		t.Fatal("language cookie behavior changed")
	}
	// Repeated application construction retains the already layered registrations.
	Localization(i18n.Options{Directory: dir, CustomDirectory: custom, Langs: []string{"qf-AA", "qf-BB"}, Names: []string{"First", "Second"}})
	if core.Tr("qf-AA", "ui.missing") != "English fallback" {
		t.Fatal("repeat composition lost fallback")
	}
}

func TestLocalizationRedirectSubURLRestoresRequestURI(t *testing.T) {
	for _, tc := range []struct{ name, prefix, file string }{
		{"root", "", "source%20file.txt"}, {"subcode", "/code", "source%20file.txt"},
		{"rootRaw", "", "source%2Ffile.txt"}, {"subcodeRaw", "/code", "source%2Ffile.txt"},
	} {
		prefix := tc.prefix
		t.Run(tc.name, func(t *testing.T) {
			m := macaron.New()
			m.SetURLPrefix(prefix)
			m.Use(macaron.Renderer(macaron.RenderOptions{Directory: t.TempDir()}))
			var after, downstream string
			m.Use(func(ctx *macaron.Context) { ctx.Next(); after = ctx.Req.RequestURI })
			m.Use(Localization(i18n.Options{Directory: "../../conf/locale", CustomDirectory: t.TempDir(), Langs: []string{"en-US", "de-DE"}, Names: []string{"English", "Deutsch"}, SubURL: prefix, Redirect: true}))
			m.Use(func(ctx *macaron.Context) { downstream = ctx.Req.RequestURI })
			m.Get("/owner/repo/src/:node/*", func(ctx *macaron.Context) { ctx.Resp.Write([]byte(ctx.Locale.Language())) })
			path := prefix + "/owner/repo/src/abc/" + tc.file
			original := path + "?lang=de-DE&q=a%2Fb"
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest("GET", original, nil))
			if w.Code != 302 || w.Header().Get("Location") != path {
				t.Fatalf("redirect = %d %s, want %s", w.Code, w.Header().Get("Location"), path)
			}
			if after != original {
				t.Errorf("redirect changed original RequestURI: %q", after)
			}
			followURI := path + "?q=a%2Fb"
			request := httptest.NewRequest("GET", followURI, nil)
			found := false
			for _, cookie := range w.Result().Cookies() {
				if cookie.Name == "lang" {
					want := prefix
					if want == "" {
						want = "/"
					}
					if cookie.Path != want || cookie.Value != "de-DE" {
						t.Errorf("language cookie: %+v", cookie)
					}
					request.AddCookie(cookie)
					found = true
				}
			}
			if !found {
				t.Fatal("missing language cookie")
			}
			result := httptest.NewRecorder()
			m.ServeHTTP(result, request)
			if result.Code != 200 || result.Body.String() != "de-DE" || downstream != followURI || after != followURI {
				t.Fatalf("follow = %d %q downstream=%q after=%q", result.Code, result.Body.String(), downstream, after)
			}
		})
	}
}
