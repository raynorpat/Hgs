package routers_test

import (
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers"
	"github.com/macaron-contrib/i18n"
)

// Presentation evidence only; cmd tests cover production registration and guards.
func TestSharedHelpAndFooter(t *testing.T) {
	oldSub, oldName, oldInstall := setting.AppSubUrl, setting.AppName, setting.InstallLock
	oldSSH, oldAddress, oldSecret := setting.DisableSSH, setting.SSHAddress, setting.SecretKey
	t.Cleanup(func() {
		setting.AppSubUrl, setting.AppName, setting.InstallLock = oldSub, oldName, oldInstall
		setting.DisableSSH, setting.SSHAddress, setting.SecretKey = oldSSH, oldAddress, oldSecret
	})
	setting.AppName, setting.InstallLock, setting.SecretKey = "Operator's laboratory", true, "never-render-this-secret"
	for _, prefix := range []string{"", "/code"} {
		setting.AppSubUrl = prefix
		for _, signed := range []bool{false, true} {
			for _, ssh := range []struct {
				disabled bool
				address  string
				visible  bool
			}{{true, "private-bind-address:2222", false}, {false, "", false}, {false, "private-bind-address:2222", true}} {
				setting.DisableSSH, setting.SSHAddress = ssh.disabled, ssh.address
				m := macaron.New()
				m.SetURLPrefix(prefix)
				m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
				m.Use(middleware.Localization(i18n.Options{Directory: "../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
				m.Get("/help", func(c *macaron.Context) {
					c.Data["PageStartTime"] = time.Now()
					c.Data["TmplLoadTimes"] = func() string { return "0" }
					ctx := &middleware.Context{Context: c, IsSigned: signed}
					if signed {
						ctx.User = &models.User{Name: "reader"}
						c.Data["SignedUser"] = ctx.User
					}
					routers.Help(ctx)
				})
				w := httptest.NewRecorder()
				m.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/help", nil))
				body := w.Body.String()
				if w.Code != 200 {
					t.Fatalf("help render status=%d %s", w.Code, body)
				}
				for _, want := range []string{`data-local-help`, `href="` + prefix + `/help"`, "Operator&#39;s laboratory", "© 2015 GoGits", `href="https://github.com/gogits/gogs"`, "Source and license", "hg pull", "hg update", "hg commit", "hg push", "Watch controls", `https://www.mercurial-scm.org/guide`, `<summary>Language</summary>`} {
					if !strings.Contains(body, want) {
						t.Errorf("Help/footer missing %q", want)
					}
				}
				if strings.Contains(body, "data-help-ssh") != ssh.visible {
					t.Error("SSH guidance ignores effective hosting configuration")
				}
				for _, forbidden := range []string{"never-render-this-secret", "private-bind-address", "git clone", "git push", "/hooks", "/releases"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("Help exposed %q", forbidden)
					}
				}
			}
		}
	}
}
