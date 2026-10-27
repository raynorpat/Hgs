package user_test

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
	"github.com/gogits/gogs/routers"
	"github.com/gogits/gogs/routers/repo"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/cache"
	"github.com/macaron-contrib/csrf"
	"github.com/macaron-contrib/i18n"
	"github.com/macaron-contrib/session"
)

type discoveryFixture struct {
	m          *macaron.Macaron
	alice, bob *models.User
	repo       *models.Repository
	db         *xorm.Engine
	prefix     string
}

func newDiscoveryFixture(t *testing.T, prefix string) *discoveryFixture {
	t.Helper()
	root := t.TempDir()
	oldDB, oldHas := models.DbCfg, models.HasEngine
	oldLog, oldRoot := setting.LogRootPath, setting.RepoRootPath
	oldSub, oldInstall, oldService := setting.AppSubUrl, setting.InstallLock, setting.Service
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg, models.HasEngine = oldDB, oldHas
		setting.LogRootPath, setting.RepoRootPath = oldLog, oldRoot
		setting.AppSubUrl, setting.InstallLock, setting.Service = oldSub, oldInstall, oldService
	})
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(root, "discovery.db"))
	setting.LogRootPath = root
	setting.RepoRootPath = filepath.Join(root, "repos")
	setting.AppSubUrl = prefix
	setting.InstallLock = true
	setting.Service.RequireSignInView = false
	setting.Service.EnableReverseProxyAuth = false
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	models.HasEngine = true
	f := &discoveryFixture{prefix: prefix, alice: &models.User{Name: "fixturealice", Email: "alice@example.invalid", Passwd: "fixture-password", IsActive: true}, bob: &models.User{Name: "fixturebob", Email: "bob@example.invalid", Passwd: "fixture-password", IsActive: true}}
	for _, u := range []*models.User{f.alice, f.bob} {
		if err := models.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.db, err = xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.repo = &models.Repository{OwnerId: f.alice.Id, Name: "alice-secret", LowerName: "alice-secret", IsPrivate: true, State: models.RepositoryReady}
	for _, r := range []*models.Repository{f.repo, {OwnerId: f.bob.Id, Name: "bob-secret", LowerName: "bob-secret", IsPrivate: true, State: models.RepositoryReady}, {OwnerId: f.alice.Id, Name: "public-lab", LowerName: "public-lab", State: models.RepositoryReady}} {
		if _, err = f.db.Insert(r); err != nil {
			t.Fatal(err)
		}
	}
	m := macaron.New()
	m.SetURLPrefix(prefix)
	m.Use(macaron.Static("../../public"))
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Use(middleware.Localization(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
	m.Use(cache.Cacher())
	m.Use(session.Sessioner(session.Options{Provider: "memory", CookieName: "discovery_fixture", CookiePath: prefix}))
	m.Use(csrf.Csrfer(csrf.Options{Secret: "disposable-discovery-fixture", CookiePath: prefix, SetCookie: true}))
	m.Use(middleware.Contexter())
	m.Use(func(ctx *middleware.Context) { ctx.PrepareHTML = user.PrepareDiscovery })
	guard := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	public := middleware.Toggle(&middleware.ToggleOptions{})
	m.Get("/explore", public, routers.Explore)
	m.Get("/explore/search", public, routers.ExploreSearch)
	m.Get("/user/recent", guard, user.Recent)
	m.Get("/repo/create", guard, repo.Create)
	m.Post("/user/recent/clear", guard, user.ClearRecent)
	m.Get("/fixturealice/alice-secret", guard, func(ctx *middleware.Context) {
		access, err := models.RepositoryAccessFor(ctx.User.Id, f.repo.Id)
		if err != nil || !access.Read {
			ctx.Error(404)
			return
		}
		ctx.Repo.Repository = f.repo
		ctx.Repo.Owner = f.alice
		ctx.Repo.HasAccess = true
		ctx.Repo.RepoLink = prefix + "/fixturealice/alice-secret"
		ctx.Data["Title"] = "Synthetic repository fixture"
		ctx.Data["CatalogQuery"] = models.NormalizeCatalogQuery(models.CatalogQuery{})
		ui.BuildShell(ctx, ui.Repository, "source")
		ctx.HTML(200, "explore/repos")
	})
	m.Get("/fixture/account/:name", func(ctx *middleware.Context) {
		var id int64
		if ctx.Params(":name") == "alice" {
			id = f.alice.Id
		}
		if ctx.Params(":name") == "bob" {
			id = f.bob.Id
		}
		ctx.Session.Set("uid", id)
		ctx.Redirect(prefix + "/explore")
	})
	m.Get("/fixture/page/:kind", func(ctx *middleware.Context) {
		ctx.Data["SignedUser"] = ctx.User
		ctx.Data["Title"] = "Isolated discovery fixture"
		ctx.Repo.Repository = f.repo
		ctx.Repo.HasAccess = true
		if ctx.Params(":kind") == "redirect" {
			ctx.Redirect(prefix + "/explore")
			return
		}
		if ctx.Params(":kind") == "asset" {
			ctx.Write([]byte("asset"))
			return
		}
		ui.BuildShell(ctx, ui.Workspace, "")
		if ctx.Params(":kind") == "error" {
			ctx.HTML(500, "explore/repos")
			return
		}
		if ctx.Params(":kind") == "broken" {
			ctx.HTML(200, "missing-template")
			return
		}
		ctx.HTML(200, "explore/repos")
	})
	f.m = m
	return f
}
func (f *discoveryFixture) request(method, path, actor, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, f.prefix+path, strings.NewReader(body))
	if actor != "" {
		req.SetBasicAuth(actor, "fixture-password")
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, req)
	return w
}
func TestCatalogRoutesPolicyAndInput(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		name := "root"
		if prefix != "" {
			name = "subURL"
		}
		t.Run(name, func(t *testing.T) {
			f := newDiscoveryFixture(t, prefix)
			for _, path := range []string{"/explore", "/explore/search?visibility=private"} {
				w := f.request("GET", path, "", "", nil)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "public-lab") || strings.Contains(w.Body.String(), "alice-secret") {
					t.Fatalf("anonymous %s: %d %s", path, w.Code, w.Body)
				}
			}
			w := f.request("GET", "/explore/search?q=secret", f.bob.Name, "", nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "bob-secret") || strings.Contains(w.Body.String(), "alice-secret") {
				t.Fatal("account search", w.Code, w.Body)
			}
			for _, query := range []string{"page=no", "page=-1", "visibility=oops", "scope=oops", "page_size=x", "q=" + url.QueryEscape(strings.Repeat("界", 201))} {
				w = f.request("GET", "/explore/search?"+query, f.alice.Name, "", nil)
				if w.Code != 400 || !strings.Contains(w.Body.String(), "role=\"alert\"") {
					t.Fatalf("invalid %s: %d %s", query, w.Code, w.Body)
				}
			}
			setting.Service.RequireSignInView = true
			for _, path := range []string{"/explore", "/explore/search"} {
				w = f.request("GET", path, "", "", nil)
				if w.Code != 404 || strings.Contains(w.Body.String(), "public-lab") {
					t.Fatalf("required sign-in: %d %s", w.Code, w.Body)
				}
			}
			w = f.request("GET", "/explore/search", f.alice.Name, "", nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "alice-secret") {
				t.Fatal("signed policy", w.Code, w.Body)
			}
		})
	}
}
func TestRecentsRoutesAndCSRF(t *testing.T) {
	f := newDiscoveryFixture(t, "/code")
	models.RecordRepositoryVisit(context.Background(), f.alice.Id, f.repo.Id, time.Now())
	w := f.request("GET", "/user/recent", f.bob.Name, "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "alice-secret") {
		t.Fatal("cross-account history", w.Code, w.Body)
	}
	w = f.request("POST", "/user/recent/clear", f.alice.Name, "", nil)
	if w.Code != 400 {
		t.Fatal("CSRF accepted", w.Code)
	}
	rows, _ := models.RecentRepositories(context.Background(), f.alice.Id)
	if len(rows) != 1 {
		t.Fatal("CSRF cleared")
	}
	token := csrf.GenerateToken("disposable-discovery-fixture", "0", "POST")
	w = f.request("POST", "/user/recent/clear", f.alice.Name, "_csrf="+url.QueryEscape(token), nil)
	if w.Code != 303 || w.Header().Get("Location") != "/code/user/recent" {
		t.Fatal("clear redirect", w.Code, w.Header())
	}
	rows, _ = models.RecentRepositories(context.Background(), f.alice.Id)
	if len(rows) != 0 {
		t.Fatal("did not clear")
	}
	w = f.request("GET", "/user/recent", "", "", nil)
	if w.Code != 302 {
		t.Fatal("unsigned recent", w.Code)
	}
}
func TestRecentsForegroundSuccessfulRenderOnly(t *testing.T) {
	f := newDiscoveryFixture(t, "/code")
	for _, tc := range []struct {
		kind    string
		headers map[string]string
		want    bool
	}{{"redirect", nil, false}, {"asset", nil, false}, {"error", map[string]string{"Accept": "text/html"}, false}, {"broken", map[string]string{"Accept": "text/html"}, false}, {"ok", nil, false}, {"ok", map[string]string{"Accept": "text/html", "X-Requested-With": "XMLHttpRequest"}, false}, {"ok", map[string]string{"Accept": "text/html", "Sec-Fetch-Mode": "cors"}, false}, {"ok", map[string]string{"Accept": "text/html", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, true}} {
		models.ClearRepositoryVisits(context.Background(), f.alice.Id)
		f.request("GET", "/fixture/page/"+tc.kind, f.alice.Name, "", tc.headers)
		rows, err := models.RecentRepositories(context.Background(), f.alice.Id)
		if err != nil || (len(rows) > 0) != tc.want {
			t.Fatalf("%s %+v: %d %v", tc.kind, tc.headers, len(rows), err)
		}
	}
}
func TestDiscoveryBrowserFixture(t *testing.T) {
	addr := os.Getenv("DISCOVERY_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("opt-in isolated discovery browser fixture")
	}
	if addr != "127.0.0.1:43128" {
		t.Fatal("fixture must bind its reserved loopback address")
	}
	f := newDiscoveryFixture(t, "/code")
	t.Log("Synthetic discovery data; account switches /code/fixture/account/alice and /bob")
	server := &http.Server{Addr: addr, Handler: f.m, ReadHeaderTimeout: 5 * time.Second}
	t.Fatal(server.ListenAndServe())
}

func TestRecentsReducedCompositionNeedsNoDatabase(t *testing.T) {
	m := macaron.New()
	m.Get("/", func(c *macaron.Context) {
		ctx := &middleware.Context{Context: c, IsSigned: true, User: &models.User{Id: 123, Name: "synthetic"}}
		ui.BuildShell(ctx, ui.Anonymous, "")
		if err := user.PrepareDiscovery(ctx); err != nil {
			t.Fatal(err)
		}
		if _, ok := ctx.Data[ui.RecentsURL]; ok {
			t.Fatal("reduced page got Recents")
		}
		c.Resp.Write([]byte("ok"))
	})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
