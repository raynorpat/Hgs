//go:build integration

package integration

import (
	"context"
	"fmt"
	"html"
	htmltemplate "html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/repo"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/binding"
	"github.com/macaron-contrib/cache"
	"github.com/macaron-contrib/csrf"
	"github.com/macaron-contrib/i18n"
	"github.com/macaron-contrib/session"
)

type uiProvider struct{ s *hosting.Service }

func (p uiProvider) Service() *hosting.Service { return p.s }

type uiFixture struct {
	m                         *macaron.Macaron
	prefix, node, merge, root string
	db                        *xorm.Engine
	owner, reader             *models.User
	public, private, empty    *models.Repository
}

func newUIFixture(t *testing.T, prefix string) *uiFixture {
	t.Helper()
	root := t.TempDir()
	oldDB, oldHas := models.DbCfg, models.HasEngine
	oldGravatar := setting.DisableGravatar
	oldLog, oldRoot, oldSub, oldURL, oldInstall, oldService := setting.LogRootPath, setting.RepoRootPath, setting.AppSubUrl, setting.AppUrl, setting.InstallLock, setting.Service
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg, models.HasEngine = oldDB, oldHas
		setting.DisableGravatar = oldGravatar
		setting.LogRootPath, setting.RepoRootPath, setting.AppSubUrl, setting.AppUrl, setting.InstallLock, setting.Service = oldLog, oldRoot, oldSub, oldURL, oldInstall, oldService
	})
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(root, "ui.db"))
	setting.LogRootPath = root
	setting.RepoRootPath = filepath.Join(root, "repos")
	setting.AppSubUrl = prefix
	setting.AppUrl = "http://localhost" + prefix + "/"
	setting.InstallLock = true
	setting.Service.RequireSignInView = false
	setting.Service.EnableNotifyMail = false
	setting.DisableGravatar = true
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	models.HasEngine = true
	f := &uiFixture{root: root, prefix: prefix, owner: &models.User{Name: "fixtureowner", Email: "owner@example.invalid", Passwd: "fixture-password", IsActive: true}, reader: &models.User{Name: "fixturereader", Email: "reader@example.invalid", Passwd: "fixture-password", IsActive: true}}
	for _, u := range []*models.User{f.owner, f.reader} {
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
	service, err := hosting.New(context.Background(), hosting.Config{Mercurial: hg.Config{Executable: hgPath(t), ConfigPath: filepath.Join(root, "managed", "hgrc")}, RepositoryRoot: setting.RepoRootPath, HostKeyPath: filepath.Join(root, "host-key"), PublicURL: setting.AppUrl, AppSubURL: prefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		service.Shutdown(ctx)
	})
	for i, name := range []string{"repository-with-a-long-name-for-the-shared-navigation", "private-lab", "empty-lab"} {
		r, err := models.CreateHgRepository(context.Background(), service.Runner(), service.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: f.owner.Id, Name: name, Private: i == 1})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.public = r
		}
		if i == 1 {
			f.private = r
		}
		if i == 2 {
			f.empty = r
		}
	}
	if err := models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.private.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	h := newHgCmd(t, hgPath(t), filepath.Join(root, "fixture-hg"), 2)
	local := filepath.Join(root, "working")
	h.must(t, "", "init", local)
	files := map[string][]byte{"README.md": []byte("# Lab\n<script>alert(1)</script>\n"), "source & space.go": []byte("package main\n// <script>alert(2)</script>\nfunc main() {}\n"), "binary.dat": {0, 1, 2, 3}, "large.txt": []byte(strings.Repeat("x", hg.MaxRenderedFileBytes+1)), "empty/README.md": {}}
	for name, b := range files {
		path := filepath.Join(local, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h.must(t, local, "add")
	h.must(t, local, "commit", "-u", "Fixture <owner@example.invalid>", "-m", "first <script> changeset")
	first := strings.TrimSpace(h.stdout(t, local, "log", "-r", ".", "-T", "{node}"))
	h.must(t, local, "bookmark", "mark / lab")
	h.must(t, local, "tag", "-u", "Fixture", "v1")
	h.must(t, local, "branch", "named / lab")
	h.must(t, local, "commit", "-u", "Fixture", "-m", "named branch")
	f.node = strings.TrimSpace(h.stdout(t, local, "log", "-r", ".", "-T", "{node}"))
	h.must(t, local, "update", "-r", first)
	os.WriteFile(filepath.Join(local, "other.txt"), []byte("other"), 0600)
	h.must(t, local, "add", "other.txt")
	h.must(t, local, "commit", "-u", "Fixture", "-m", "other head")
	h.must(t, local, "merge", "-r", f.node)
	h.must(t, local, "commit", "-u", "Fixture", "-m", "merge fixture")
	f.merge = strings.TrimSpace(h.stdout(t, local, "log", "-r", ".", "-T", "{node}"))
	h.must(t, local, "update", "-r", f.node)
	if err := os.WriteFile(filepath.Join(local, "second-head.txt"), []byte("head two"), 0600); err != nil {
		t.Fatal(err)
	}
	h.must(t, local, "add", "second-head.txt")
	h.must(t, local, "commit", "-u", "Fixture", "-m", "second named head")
	h.must(t, local, "update", "-r", f.node)
	if err := os.WriteFile(filepath.Join(local, "third-head.txt"), []byte("head three"), 0600); err != nil {
		t.Fatal(err)
	}
	h.must(t, local, "add", "third-head.txt")
	h.must(t, local, "commit", "-u", "Fixture", "-m", "third named head")
	for _, r := range []*models.Repository{f.public, f.private} {
		dest, err := models.RepositoryStoragePath(service.RepositoryRoot(), r.Id)
		if err != nil {
			t.Fatal(err)
		}
		h.must(t, local, "push", "--force", "--rev", "all()", "--bookmark", "mark / lab", dest)
	}
	m := macaron.New()
	m.SetURLPrefix(prefix)
	m.MapTo(uiProvider{service}, (*hosting.Provider)(nil))
	m.Use(macaron.Static("../../public"))
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []htmltemplate.FuncMap{base.TemplateFuncs}}))
	m.Use(middleware.Localization(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US", "de-DE"}, Names: []string{"English", "Deutsch"}}))
	m.Use(cache.Cacher())
	m.Use(session.Sessioner(session.Options{Provider: "memory", CookieName: "ui_fixture", CookiePath: prefix}))
	m.Use(csrf.Csrfer(csrf.Options{Secret: "disposable-ui-fixture", CookiePath: prefix, SetCookie: true}))
	m.Use(middleware.Contexter())
	m.Use(func(ctx *middleware.Context) {
		ctx.PrepareHTML = user.PrepareDiscovery
		// Test-only identity selection; production authority still comes from RepoAssignment.
		if who := ctx.Req.Header.Get("X-Fixture-User"); who != "" {
			ctx.User = f.owner
			if who == "reader" {
				ctx.User = f.reader
			}
			ctx.IsSigned = true
			ctx.Data["SignedUser"] = ctx.User
			ctx.Data["IsSigned"] = true
		}
	})
	guard := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	m.Get("/", guard, user.Dashboard)
	m.Get("/repo/create", guard, repo.Create)
	m.Post("/repo/create", guard, binding.BindIgnErr(auth.CreateRepoForm{}), repo.CreatePost)
	m.Get("/fixture/account/:name", func(ctx *middleware.Context) {
		id := f.owner.Id
		if ctx.Params(":name") == "reader" {
			id = f.reader.Id
		}
		if ctx.Params(":name") == "anonymous" {
			id = 0
		}
		ctx.Session.Set("uid", id)
		ctx.Redirect(prefix + "/")
	})
	m.Group("/:username/:reponame", func() {
		m.Get("/settings", guard, middleware.RequireTrueOwner(), repo.Settings)
		m.Post("/settings", guard, middleware.RequireTrueOwner(), binding.BindIgnErr(auth.RepoSettingForm{}), repo.SettingsPost)
		m.Route("/settings/collaboration", "GET,POST", guard, middleware.RequireTrueOwner(), repo.SettingsCollaboration)
		m.Post("/action/:action", guard, repo.Action)
		m.Group("", func() {
			m.Get("/commits", repo.Commits)
			m.Get("/commits/*", repo.Commits)
			m.Get("/branches", repo.Branches)
			m.Get("/commit/:node", repo.PrepareRepositoryUI("changesets"), middleware.RepoRef("commit"), repo.Diff)
			m.Get("/src/:node", repo.PrepareRepositoryUI("source"), middleware.RepoRef("src"), repo.Home)
			m.Get("/src/:node/*", repo.PrepareRepositoryUI("source"), middleware.RepoRef("src"), repo.Home)
			m.Get("", repo.PrepareRepositoryUI("source"), middleware.RepoRef("src"), repo.Home)
		}, middleware.RepoBrowse())
	}, middleware.RepoAssignment(true))
	f.m = m
	return f
}
func (f *uiFixture) get(t *testing.T, path, who string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", f.prefix+path, nil)
	r.Header.Set("Accept", "text/html")
	if who != "" {
		r.Header.Set("X-Fixture-User", who)
	}
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, middleware.WithBrowserResponseController(r, w))
	return w
}
func requireUI(t *testing.T, w *httptest.ResponseRecorder, active string) {
	t.Helper()
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	for _, want := range []string{`data-hg-shell`, `class="hg-main"`, `aria-current="page">`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing shared UI %q", want)
		}
	}
	if active != "" && !regexp.MustCompile(`<a class="hg-nav-item" href="`+regexp.QuoteMeta(active)+`"[^>]*aria-current="page"`).MatchString(body) {
		t.Errorf("missing navigation %s", active)
	}
	for _, old := range []string{"ng/css", "ng/js", "ZeroClipboard", "/pulls", "git clone", "git init"} {
		if strings.Contains(body, old) {
			t.Errorf("obsolete/unavailable UI %q", old)
		}
	}
}
func TestSharedUIRepositoryRoutes(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		name := "root"
		if prefix != "" {
			name = "subcode"
		}
		t.Run(name, func(t *testing.T) {
			f := newUIFixture(t, prefix)
			root := "/fixtureowner/" + f.public.Name
			for _, p := range []string{root, root + "/commits", root + "/branches", root + "/commit/" + f.merge, root + "/src/" + f.node + "/source%20&%20space.go", root + "/src/" + f.node + "/binary.dat", root + "/src/" + f.node + "/large.txt", root + "/src/" + f.node + "/empty", "/fixtureowner/empty-lab", root + "/settings", root + "/settings/collaboration", "/repo/create"} {
				t.Run(p, func(t *testing.T) {
					active := prefix + root
					switch {
					case strings.Contains(p, "/commits") || strings.Contains(p, "/commit/"):
						active += "/commits"
					case strings.HasSuffix(p, "/branches"):
						active += "/branches"
					case strings.Contains(p, "/settings"):
						active += "/settings"
					case p == "/repo/create":
						active = prefix + "/#repositories"
					case p == "/fixtureowner/empty-lab":
						active = prefix + p
					}
					requireUI(t, f.get(t, p, "owner"), active)
				})
			}
			body := f.get(t, root, "owner").Body.String()
			if !strings.Contains(body, `data-revision-kind="bookmark"`) || !strings.Contains(body, `ref_kind=bookmark`) || !strings.Contains(body, `data-node="`+f.node+`"`) {
				t.Error("typed revision selector missing")
			}
			if strings.Contains(body, "<script>alert(1)</script>") {
				t.Error("unsafe README")
			}
			if !strings.Contains(body, prefix+root+"/src/") {
				t.Error("immutable source links missing")
			}
			source := f.get(t, root+"/src/"+f.node+"/source%20&%20space.go", "owner").Body.String()
			if !strings.Contains(source, "&lt;script&gt;alert(2)&lt;/script&gt;") {
				t.Error("source is not escaped")
			}
			for _, kind := range []string{"bookmark", "tag", "branch"} {
				name := map[string]string{"bookmark": "mark / lab", "tag": "v1", "branch": "named / lab"}[kind]
				w := f.get(t, root+"?ref_kind="+kind+"&ref_name="+url.QueryEscape(name), "owner")
				if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), prefix+root+"/src/") {
					t.Errorf("typed %s selection %d %s", kind, w.Code, w.Header().Get("Location"))
				}
			}
			private := "/fixtureowner/private-lab"
			if w := f.get(t, private, ""); w.Code != 404 {
				t.Errorf("private anonymous status=%d", w.Code)
			}
			w := f.get(t, private, "reader")
			requireUI(t, w, "")
			if strings.Contains(w.Body.String(), `href="`+prefix+private+`/settings"`) {
				t.Error("reader exposed settings")
			}
			if w = f.get(t, private+"/settings", "reader"); w.Code != 404 {
				t.Errorf("reader settings status=%d", w.Code)
			}
			recents, err := models.RecentRepositories(context.Background(), f.owner.Id)
			if err != nil || len(recents) == 0 {
				t.Errorf("foreground migrated visits not recorded: %v %v", recents, err)
			}
		})
	}
}
func TestSharedUIWorkspace(t *testing.T) {
	f := newUIFixture(t, "/code")
	w := f.get(t, "/", "owner")
	requireUI(t, w, "/code/")
	for _, want := range []string{`id="repositories"`, `id="organizations"`, `href="/code/fixtureowner/` + f.public.Name + `"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("workspace missing %s", want)
		}
	}
	if strings.Contains(w.Body.String(), "news-content") {
		t.Error("fabricated activity")
	}
	// Unsupported history and stale privacy flags cannot create dashboard rows.
	for _, act := range []*models.Action{{UserId: f.reader.Id, OpType: models.STAR_REPO, ActUserId: f.owner.Id, ActUserName: f.owner.Name, RepoId: f.public.Id, RepoUserName: f.owner.Name, RepoName: f.public.Name}, {UserId: f.reader.Id, OpType: models.CREATE_REPO, ActUserId: f.owner.Id, ActUserName: f.owner.Name, RepoId: f.private.Id, RepoUserName: f.owner.Name, RepoName: f.private.Name, IsPrivate: false}} {
		if _, err := f.db.Insert(act); err != nil {
			t.Fatal(err)
		}
	}
	if err := models.DeleteAccess(&models.Access{UserId: f.reader.Id, RepoId: f.private.Id}); err != nil {
		t.Fatal(err)
	}
	reader := f.get(t, "/", "reader")
	requireUI(t, reader, "/code/")
	if strings.Contains(reader.Body.String(), "private-lab") || strings.Contains(reader.Body.String(), `class="hg-feed"`) || !strings.Contains(reader.Body.String(), "No activity yet.") {
		t.Error("workspace fabricated or disclosed inaccessible activity")
	}
}
func TestSharedUIBrowserFixture(t *testing.T) {
	addr := os.Getenv("UI_REPOSITORY_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("opt-in disposable browser fixture")
	}
	f := newUIFixture(t, "/code")
	t.Logf("synthetic source: /code/fixtureowner/%s node=%s merge=%s", f.public.Name, f.node, f.merge)
	server := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.m.ServeHTTP(w, middleware.WithBrowserResponseController(r, w))
	})}
	t.Cleanup(func() { server.Close() })
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

func (f *uiFixture) post(t *testing.T, path, who string, values url.Values, validCSRF bool) *httptest.ResponseRecorder {
	t.Helper()
	page := f.get(t, "/repo/create", who)
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if validCSRF {
		if len(token) != 2 {
			t.Fatal("missing actual CSRF token")
		}
		values.Set("_csrf", html.UnescapeString(token[1]))
	}
	r := httptest.NewRequest("POST", f.prefix+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Fixture-User", who)
	for _, cookie := range page.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, middleware.WithBrowserResponseController(r, w))
	return w
}
func TestSharedUIRepositoryMutations(t *testing.T) {
	f := newUIFixture(t, "/code")
	root := "/fixtureowner/" + f.public.Name
	if models.IsStaring(f.owner.Id, f.public.Id) {
		t.Fatal("unexpected initial star")
	}
	if w := f.get(t, root+"/action/star", "owner"); w.Code == 200 || models.IsStaring(f.owner.Id, f.public.Id) {
		t.Fatal("GET action mutated")
	}
	if w := f.post(t, root+"/action/star", "owner", url.Values{}, false); w.Code != 400 || models.IsStaring(f.owner.Id, f.public.Id) {
		t.Fatalf("CSRF action status=%d", w.Code)
	}
	for _, action := range []string{"star", "unstar", "unwatch", "watch"} {
		w := f.post(t, root+"/action/"+action, "owner", url.Values{}, true)
		if w.Code != 302 {
			t.Fatalf("%s status=%d %s", action, w.Code, w.Body.String())
		}
		if action == "star" && !models.IsStaring(f.owner.Id, f.public.Id) {
			t.Error("star failed")
		}
		if action == "unstar" && models.IsStaring(f.owner.Id, f.public.Id) {
			t.Error("unstar failed")
		}
		if action == "watch" && !models.IsWatching(f.owner.Id, f.public.Id) {
			t.Error("watch failed")
		}
		if action == "unwatch" && models.IsWatching(f.owner.Id, f.public.Id) {
			t.Error("unwatch failed")
		}
	}
	collab := root + "/settings/collaboration"
	w := f.post(t, collab, "owner", url.Values{"collaborator": {"fixturereader"}}, true)
	if w.Code != 302 {
		t.Fatalf("add collaborator %d %s", w.Code, w.Body.String())
	}
	if has, _ := models.HasAccess(f.reader.Id, f.public.Id, models.WRITABLE); !has {
		t.Error("collaborator not added")
	}
	f.get(t, collab+"?remove=fixturereader", "owner")
	if has, _ := models.HasAccess(f.reader.Id, f.public.Id, models.WRITABLE); !has {
		t.Error("GET removed collaborator")
	}
	if w = f.post(t, collab, "owner", url.Values{"remove": {"fixturereader"}}, false); w.Code != 400 {
		t.Errorf("CSRF remove status=%d", w.Code)
	}
	if w = f.post(t, collab, "owner", url.Values{"remove": {"fixturereader"}}, true); w.Code != 302 {
		t.Errorf("remove status=%d", w.Code)
	}
	if has, _ := models.HasAccess(f.reader.Id, f.public.Id, models.WRITABLE); has {
		t.Error("collaborator not removed")
	}
	// Binder validation must retain safe submitted values and never passwords.
	values := url.Values{"action": {"update"}, "repo_name": {"invalid name"}, "desc": {"safe <retained>"}, "site": {"https://example.invalid"}, "private": {"on"}}
	w = f.post(t, root+"/settings", "owner", values, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `value="invalid name"`) || !strings.Contains(w.Body.String(), "safe &lt;retained&gt;") {
		t.Errorf("settings retention failed: %d %s", w.Code, w.Body.String())
	}
	w = f.post(t, root+"/settings", "owner", url.Values{"action": {"update"}, "repo_name": {f.public.Name}, "desc": {"updated description"}}, true)
	if w.Code != 302 {
		t.Errorf("settings update %d %s", w.Code, w.Body.String())
	}
	current, err := models.GetRepositoryById(f.public.Id)
	if err != nil || current.Description != "updated description" {
		t.Errorf("settings were not updated %v", err)
	}
	for _, action := range []string{"transfer", "delete"} {
		w = f.post(t, root+"/settings", "owner", url.Values{"action": {action}, "repo_name": {f.public.Name}, "new_owner_name": {"fixturereader"}, "password": {"wrong-secret"}}, true)
		if w.Code != 200 || strings.Contains(w.Body.String(), "wrong-secret") {
			t.Errorf("%s failure unsafe/status=%d", action, w.Code)
		}
	}
	current, err = models.GetRepositoryById(f.public.Id)
	if err != nil || current.OwnerId != f.owner.Id {
		t.Errorf("failed destructive action changed repository %v", err)
	}
	// Successful actual create, rename, transfer and delete use only disposable storage.
	w = f.post(t, "/repo/create", "owner", url.Values{"uid": {fmt.Sprint(f.owner.Id)}, "repo_name": {"disposable-workflow"}, "desc": {"created through form"}}, true)
	if w.Code != 302 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	created, err := models.GetRepositoryByName(f.owner.Id, "disposable-workflow")
	if err != nil {
		t.Fatal(err)
	}
	w = f.post(t, "/fixtureowner/disposable-workflow/settings", "owner", url.Values{"action": {"update"}, "repo_name": {"renamed-workflow"}, "desc": {"renamed through form"}}, true)
	if w.Code != 302 || w.Header().Get("Location") != "/code/fixtureowner/renamed-workflow/settings" {
		t.Fatalf("rename %d %s", w.Code, w.Header().Get("Location"))
	}
	renamed, err := models.GetRepositoryByName(f.owner.Id, "renamed-workflow")
	if err != nil || renamed.Id != created.Id {
		t.Fatalf("rename lost repository identity: %v", err)
	}
	w = f.post(t, "/fixtureowner/renamed-workflow/settings", "owner", url.Values{"action": {"transfer"}, "repo_name": {"renamed-workflow"}, "new_owner_name": {"fixturereader"}, "password": {"fixture-password"}}, true)
	if w.Code != 302 {
		t.Fatalf("transfer %d %s", w.Code, w.Body.String())
	}
	transferred, err := models.GetRepositoryByName(f.reader.Id, "renamed-workflow")
	if err != nil || transferred.Id != created.Id {
		t.Fatalf("transfer lost identity %v", err)
	}
	w = f.post(t, "/fixturereader/renamed-workflow/settings", "reader", url.Values{"action": {"delete"}, "repo_name": {"renamed-workflow"}, "password": {"fixture-password"}}, true)
	if w.Code != 302 {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	if _, err = models.GetRepositoryById(created.Id); err != models.ErrRepoNotExist {
		t.Errorf("deleted repository remains: %v", err)
	}

}
