package ui

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth/apiv1"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	v1 "github.com/gogits/gogs/routers/api/v1"
	"github.com/macaron-contrib/i18n"
)

// TestSharedShellBrowserFixture is an opt-in disposable, database-free QA server.
// UI_FIXTURE_ADDR=127.0.0.1:43127 go test ./modules/ui -run TestSharedShellBrowserFixture -v -timeout 30m
// Every fixture route and item is visibly identified as synthetic, never production.
func TestSharedShellBrowserFixture(t *testing.T) {
	addr := os.Getenv("UI_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("set UI_FIXTURE_ADDR for isolated browser QA")
	}
	setting.AppSubUrl = "/code"
	setting.DisableGravatar = true
	m := macaron.New()
	m.SetURLPrefix("/code")
	m.Use(macaron.Static("../../public", macaron.StaticOptions{}))
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
	m.Get("/", func(c *macaron.Context) {
		user := &models.User{Name: "alexandria-research-and-development-laboratory", UseCustomAvatar: true, Id: 42, IsAdmin: true}
		ctx := &middleware.Context{Context: c, User: user, IsSigned: true}
		kind := Workspace
		if c.Query("kind") == "anonymous" {
			kind = Anonymous
			ctx.User = nil
			ctx.IsSigned = false
		}
		if c.Query("kind") == "repository" {
			kind = Repository
			ctx.Repo.HasAccess = true
			ctx.Repo.Owner = user
			ctx.Repo.Repository = &models.Repository{Name: "laboratory-with-a-very-long-repository-name"}
			ctx.Repo.RepoLink = "/code/alexandria/laboratory"
			ctx.Repo.IsTrueOwner = true
		}
		if c.Query("kind") == "admin" {
			kind = Admin
		}
		c.Data["Title"] = "Shared application shell · isolated fixture"
		c.Data["SignedUser"] = user
		c.Data[SearchURL] = "/code/fixture/search"
		c.Data[RecentsURL] = "/code/fixture/recents"
		c.Data["UIRecentRepositories"] = []NavItem{{Label: "alexandria / laboratory-with-a-long-repository-name", URL: "/code/?kind=repository"}}
		c.Data["Link"] = "/code/?kind=" + string(kind)
		s := BuildShell(ctx, kind, "repositories")
		switch c.Query("avatar") {
		case "real":
			s.AvatarURL = "/code/img/avatar_default.jpg"
		case "fallback":
			s.AvatarURL = ""
		default:
			s.AvatarURL = "/code/fixture/broken-avatar"
		}
		c.Data["Shell"] = s
		start, err := c.HTMLString("shared/start", c.Data)
		if err != nil {
			t.Error(err)
			c.Error(500)
			return
		}
		end, err := c.HTMLString("shared/end", c.Data)
		if err != nil {
			t.Error(err)
			c.Error(500)
			return
		}
		field, err := c.HTMLString("shared/form_field", Field{ID: "fixture-name", Name: "name", Label: "Repository name (fixture)", Value: "laboratory", Hint: "Synthetic data; changes are never saved.", Required: true})
		if err != nil {
			t.Error(err)
			c.Error(500)
			return
		}
		if c.Query("text") == "200" {
			// Text-only enlargement stress fixture, not a claim of browser chrome zoom.
			start += `<style>.hg-app{font-size:28px;line-height:42px}.hg-app h1{font-size:56px;line-height:72px}@media(max-width:800px){.hg-app h1{font-size:48px;line-height:64px}}.hg-app h2{font-size:40px;line-height:56px}.hg-app .hg-context-caption,.hg-app .hg-meta,.hg-app .hg-footer{font-size:24px;line-height:36px}.hg-app .hg-table{font-size:26px;line-height:40px}</style>`
		}
		c.Resp.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Write([]byte(start + `<h1>Shared application shell</h1><div class="hg-alert hg-alert-info" role="status">Isolated UI fixture · synthetic data · no production actions</div><p>A long repository title wraps without widening the page: alexandria-research-and-development / laboratory-with-a-very-long-repository-name.</p><section id="repositories"><h2>Repositories</h2><div class="hg-scroll"><table class="hg-table"><thead><tr><th>Repository</th><th>Updated</th></tr></thead><tbody><tr><td><a href="/code/?kind=repository">alexandria / laboratory-with-a-very-long-repository-name</a></td><td>Today</td></tr><tr><td>mercurial-tools</td><td>Yesterday</td></tr></tbody></table></div></section><section id="organizations"><h2>Organizations</h2><p>No organizations in this isolated fixture.</p></section><h2>Form components</h2><form>` + field + `<button class="hg-button hg-button-primary" type="button">Fixture action</button></form><section data-markdown-editor data-markdown-url="/code/fixture/markdown" data-preview-error="Preview could not load. Your text is preserved."><h2>Markdown preview</h2><div class="hg-tabs"><button type="button" class="hg-button" data-markdown-write aria-pressed="true">Write</button><button type="button" class="hg-button" data-markdown-preview aria-pressed="false">Preview</button></div><label for="fixture-markdown">Description (fixture)</label><textarea id="fixture-markdown">**Safe preview** &lt;script&gt;alert(1)&lt;/script&gt;</textarea><div data-markdown-output hidden></div><p role="status" data-markdown-status></p></section>` + end))
	})
	m.Get("/fixture/search", func(c *macaron.Context) {
		c.Write([]byte("Isolated search fixture: no matching repositories. Query: " + template.HTMLEscapeString(c.Query("q"))))
	})
	m.Get("/fixture/recents", func(c *macaron.Context) { c.Write([]byte("Isolated recents fixture. No production history.")) })
	m.Post("/fixture/markdown", func(c *macaron.Context) {
		ctx := &middleware.Context{Context: c}
		v1.Markdown(ctx, apiv1.MarkdownForm{Text: c.Query("text"), Mode: c.Query("mode"), Context: c.Query("context")})
	})
	server := &http.Server{Addr: addr, Handler: m, ReadHeaderTimeout: 5 * time.Second}
	t.Logf("isolated shell fixture at http://%s/code/ (%s)", addr, filepath.Base(t.TempDir()))
	if err := server.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}
