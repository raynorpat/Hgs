package cmd

import (
	"context"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/setting"
)

// Opt-in, disposable production-router fixture. Variants run in independent
// processes because the retained application settings are process-global.
func TestLandingBrowserFixture(t *testing.T) {
	if os.Getenv("UI_LANDING_FIXTURE") != "1" {
		t.Skip("opt-in owned Task16 browser fixture")
	}
	executable := os.Getenv("GOGS_TEST_HG")
	if executable == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	variant := os.Getenv("UI_LANDING_VARIANT")
	prefix := "/code"
	if variant == "root" {
		prefix = ""
	}
	oldName, oldOAuth, oldAvatar, oldGravatar := setting.AppName, setting.OauthService, setting.AvatarUploadPath, setting.DisableGravatar
	avatarRoot := t.TempDir()
	t.Cleanup(func() {
		setting.AppName, setting.OauthService, setting.AvatarUploadPath, setting.DisableGravatar = oldName, oldOAuth, oldAvatar, oldGravatar
	})
	setting.AppName = "Hgs"
	if variant == "custom" {
		setting.AppName = "The operator's laboratory"
	}
	setting.AvatarUploadPath = filepath.Join(avatarRoot, "avatars")
	setting.DisableGravatar = true
	a := newViewAppPrefix(t, executable, prefix)
	setting.Langs, setting.Names = []string{"en-US", "de-DE"}, []string{"English", "Deutsch"}
	setting.SessionConfig.CookieName = "landing_" + filepath.Base(a.root)
	setting.Service.DisableRegistration = variant == "closed"
	setting.Service.ShowRegistrationButton = variant != "closed"
	setting.Service.RequireSignInView = variant == "blocked"
	if variant == "installer" {
		setting.InstallLock = false
	}
	setting.OauthService = &setting.Oauther{GitHub: true, Google: true, Weibo: true, Tencent: true}
	if variant == "closed" {
		setting.OauthService = nil
	}
	h := a.holder.Service()
	if _, err := models.CreateHgRepository(context.Background(), h.Runner(), h.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: a.alice.Id, Name: "private-landing-secret", Private: true}); err != nil {
		t.Fatal(err)
	}
	if variant == "empty" {
		a.repo.IsPrivate = true
		if err := models.UpdateRepository(a.repo); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	setting.AppUrl = "http://" + listener.Addr().String() + prefix + "/"
	a.handler = newWebHandler(a.holder)
	// Read-only previews use the actual mail templates/data, without invoking any
	// delivery helper or starting an SMTP worker. This is not an authentication API.
	previews := macaron.New()
	previews.Use(macaron.Renderer(macaron.RenderOptions{Directory: filepath.Join(setting.StaticRootPath, "templates"), Funcs: []template.FuncMap{base.TemplateFuncs}}))
	previews.Get("/fixture/mail/:family", func(c *macaron.Context) {
		families := map[string]string{"active": "mail/auth/active", "secondary": "mail/auth/activate_email", "register": "mail/auth/register_success", "reset": "mail/auth/reset_passwd", "mention": "mail/notify/mention", "collaborator": "mail/notify/collaborator"}
		family, ok := families[c.Params(":family")]
		if !ok {
			c.Error(404)
			return
		}
		data := mailer.GetMailTmplData(&models.User{Name: "Alex <Lab>"})
		data["Code"], data["Email"], data["IssueLink"], data["RepoLink"], data["Subject"] = "synthetic-preview-token", "alex@example.invalid", "alice/project/issues/1", "alice/project", "Laboratory notification <sample>"
		body, err := c.HTMLString(family, data)
		if err != nil {
			t.Fatal(err)
		}
		if c.Query("images") == "blocked" {
			body = regexp.MustCompile(`<img[^>]*>`).ReplaceAllString(body, "")
		}
		c.Resp.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Write([]byte(body))
	})
	stop := make(chan struct{})
	var once sync.Once
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/fixture/stop" {
			w.Write([]byte("stopping owned Task16 fixture"))
			once.Do(func() { close(stop) })
			return
		}
		if strings.HasPrefix(r.URL.Path, "/fixture/mail/") {
			previews.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/fixture/identity" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Actual local identity assets</title></head><body style="font:16px Arial;color:#172B4D"><h1>Actual local identity assets</h1><p>Favicon 16 / 32; navigation 24 / 32; master and inverse on white and blue.</p>`))
			for _, bg := range []string{"white", "#0747A6"} {
				w.Write([]byte(`<div style="background:` + bg + `;padding:24px;display:flex;gap:24px;align-items:center">`))
				for _, asset := range []string{"favicon-16.png", "favicon-32.png", "hgs-beaker.svg", "hgs-beaker-inverse.svg"} {
					for _, size := range []string{"16", "24", "32"} {
						w.Write([]byte(`<img alt="` + asset + ` ` + size + `" src="` + prefix + `/img/` + asset + `" width="` + size + `" height="` + size + `">`))
					}
				}
				w.Write([]byte(`<img alt="Approved mascot" src="` + prefix + `/img/hgs-mascot.png" width="150"></div>`))
			}
			w.Write([]byte(`</body></html>`))
			return
		}
		a.handler.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("LANDING_BROWSER_URL=%s variant=%s; actual sign in %suser/login alice/password; signup %suser/sign_up; installer locked; mail previews http://%s/fixture/mail/{active,secondary,register,reset,mention,collaborator}?images=blocked; icons http://%s/fixture/identity; stop http://%s/fixture/stop; owned root=%s", setting.AppUrl, variant, setting.AppUrl, setting.AppUrl, listener.Addr(), listener.Addr(), listener.Addr(), a.root)
	select {
	case <-stop:
	case <-time.After(45 * time.Minute):
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
