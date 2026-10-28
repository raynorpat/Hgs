package routers_test

import (
	"bytes"
	"crypto/sha256"
	"html/template"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers"
	"github.com/macaron-contrib/i18n"
)

// Presentation seam only. cmd/landing_test.go separately exercises registered
// production middleware, real public/private data, and dashboard routing.
func TestLandingIdentityAndAuth(t *testing.T) {
	oldName, oldSub, oldService, oldOAuth := setting.AppName, setting.AppSubUrl, setting.Service, setting.OauthService
	t.Cleanup(func() {
		setting.AppName, setting.AppSubUrl, setting.Service, setting.OauthService = oldName, oldSub, oldService, oldOAuth
	})
	setting.AppSubUrl = "/code"
	setting.Service.RequireSignInView = true // no catalog query needed for presentation
	setting.OauthService = &setting.Oauther{GitHub: true, Google: true, Weibo: true, Tencent: true}
	for _, name := range []string{"Hgs", "The operator's laboratory"} {
		for _, disabled := range []bool{false, true} {
			setting.AppName, setting.Service.DisableRegistration = name, disabled
			m := macaron.New()
			m.SetURLPrefix("/code")
			m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
			m.Use(middleware.Localization(i18n.Options{Directory: "../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
			m.Get("/", func(c *macaron.Context) {
				c.Data["PageStartTime"] = time.Now()
				c.Data["TmplLoadTimes"] = func() string { return "0" }
				c.Data["ShowRegistrationButton"] = !disabled
				routers.Home(&middleware.Context{Context: c})
			})
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest("GET", "/code/", nil))
			body := w.Body.String()
			for _, want := range []string{"A painless, self-hosted Mercurial service.", "Hgs", `/code/img/hgs-mascot.png`, "Renee French", "CC BY 4.0", `/code/user/login`, `/code/user/login/oauth2/github`, `/code/user/login/oauth2/google`, `/code/user/login/oauth2/weibo`, `/code/user/login/oauth2/qq`} {
				if w.Code != 200 || !strings.Contains(body, want) {
					t.Errorf("landing %q disabled=%v missing %q (HTTP %d)", name, disabled, want, w.Code)
				}
			}
			if strings.Contains(body, `/code/user/sign_up`) == disabled {
				t.Error("registration policy ignored")
			}
			if name != "Hgs" && !strings.Contains(body, "The operator&#39;s laboratory") {
				t.Error("configured instance name lost")
			}
			if strings.Contains(body, `href="/code/explore`) {
				t.Error("restricted landing exposes Explore")
			}
		}
	}
}

func TestLandingAssetPackaging(t *testing.T) {
	source, err := os.ReadFile("../docs/prototypes/landing-page/gopher-lab.png")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := os.ReadFile("../public/img/hgs-mascot.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, asset) {
		t.Fatal("approved mascot bytes changed")
	}
	t.Logf("approved mascot SHA256=%x", sha256.Sum256(asset))
	credit, err := os.ReadFile("../public/img/hgs-mascot.LICENSE.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Renee French", "https://go.dev/blog/gopher", "https://creativecommons.org/licenses/by/4.0/", "AI-generated", "2026-10-26", "lab coat", "beaker", "pose"} {
		if !strings.Contains(string(credit), s) {
			t.Errorf("missing provenance %q", s)
		}
	}
	for _, s := range []string{"hgs-beaker.svg", "hgs-beaker-inverse.svg", "favicon.png", "favicon-16.png", "favicon-32.png", "favicon.ico"} {
		if _, err := os.Stat(filepath.Join("../public/img", s)); err != nil {
			t.Error(err)
		}
	}
}

func TestHgsIdentityAssets(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{{"favicon-16.png", 16}, {"favicon-32.png", 32}, {"favicon.png", 180}} {
		data, err := os.ReadFile(filepath.Join("../public/img", tc.name))
		if err != nil {
			t.Fatal(err)
		}
		im, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if im.Bounds().Dx() != tc.size || im.Bounds().Dy() != tc.size {
			t.Errorf("%s dimensions=%v", tc.name, im.Bounds())
		}
		_, _, _, alpha := im.At(0, 0).RGBA()
		if alpha != 0 {
			t.Errorf("%s opaque background", tc.name)
		}
	}
	data, err := os.ReadFile("../public/img/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 38 || !bytes.Equal(data[:6], []byte{0, 0, 1, 0, 2, 0}) || data[6] != 16 || data[7] != 16 || data[22] != 32 || data[23] != 32 {
		t.Fatal("favicon ICO lacks 16/32 frames")
	}
	for _, name := range []string{"gogs-lg.png", "favicon.bak.png"} {
		if _, err := os.Stat(filepath.Join("../public/img", name)); !os.IsNotExist(err) {
			t.Errorf("retired product image remains: %s", name)
		}
	}
}
