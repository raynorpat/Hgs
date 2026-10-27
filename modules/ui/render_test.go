package ui

import (
	"html/template"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth/apiv1"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	v1 "github.com/gogits/gogs/routers/api/v1"
	"github.com/macaron-contrib/i18n"
	"github.com/macaron-contrib/session"
)

func shellFixture(t *testing.T, sub string, kind ContextKind, user *models.User, access, writer, admin bool, title string, configure ...func(*middleware.Context)) (Shell, string) {
	t.Helper()
	setting.AppSubUrl = sub
	m := macaron.New()
	m.SetURLPrefix(sub)
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
	var shell Shell
	m.Get("/", func(c *macaron.Context) {
		ctx := &middleware.Context{Context: c, User: user, IsSigned: user != nil}
		ctx.Repo.HasAccess, ctx.Repo.IsOwner, ctx.Repo.IsTrueOwner = access, writer, admin
		ctx.Repo.Repository = &models.Repository{Name: "laboratory"}
		ctx.Repo.Owner = &models.User{Name: "alice"}
		ctx.Repo.RepoLink = sub + "/alice/laboratory"
		c.Data["Title"] = title
		c.Data["SignedUser"] = user
		for _, f := range configure {
			f(ctx)
		}
		shell = BuildShell(ctx, kind, "source")
		if want, ok := c.Data["UILanguageExpected"].(string); ok && c.Data["UILanguageLink"] != want {
			t.Errorf("language destination = %v, want %s", c.Data["UILanguageLink"], want)
		}
		if _, ok := c.Data["Shell"]; !ok {
			t.Fatal("missing Shell view data")
		}
		start, err := c.HTMLString("shared/start", c.Data)
		if err != nil {
			t.Fatal(err)
		}
		end, err := c.HTMLString("shared/end", c.Data)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte(start + end))
	})
	r := httptest.NewRecorder()
	m.ServeHTTP(r, httptest.NewRequest("GET", sub+"/?q=hg&page=2&token=private", nil))
	if r.Code != 200 {
		t.Fatalf("render status %d: %s", r.Code, r.Body.String())
	}
	return shell, r.Body.String()
}

func TestSharedShellPermissionsAndSubURL(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	for _, sub := range []string{"", "/code"} {
		for _, tc := range []struct {
			name                  string
			kind                  ContextKind
			user                  *models.User
			access, writer, admin bool
			settings              bool
		}{
			{"signed", Workspace, &models.User{Name: "alice"}, false, false, false, false},
			{"anonymous", Anonymous, nil, false, false, false, false},
			{"admin", Admin, &models.User{Name: "alice", IsAdmin: true}, false, false, false, false},
			{"reader", Repository, &models.User{Name: "alice"}, true, false, false, false},
			{"writer", Repository, &models.User{Name: "alice"}, true, true, false, false},
			{"owner", Repository, &models.User{Name: "alice"}, true, true, true, true},
			{"global admin reader", Repository, &models.User{Name: "alice", IsAdmin: true}, true, false, false, false},
			{"unauthorized admin", Admin, &models.User{Name: "alice"}, false, false, false, false},
		} {
			t.Run(sub+tc.name, func(t *testing.T) {
				s, html := shellFixture(t, sub, tc.kind, tc.user, tc.access, tc.writer, tc.admin, "Laboratory")
				if s.SubURL != sub {
					t.Fatalf("sub URL: %q", s.SubURL)
				}
				if strings.Contains(html, `href="`+sub+`/alice/laboratory/settings"`) != tc.settings {
					t.Fatal("repository settings do not match admin permission")
				}
				if strings.Contains(html, `href="`+sub+`/admin"`) != (tc.user != nil && tc.user.IsAdmin && tc.kind != Anonymous) {
					t.Fatal("administration permission mismatch")
				}
				if tc.kind == Anonymous {
					if strings.Contains(html, "hg-rail") || strings.Contains(html, "data-drawer") {
						t.Fatal("reduced shell has app navigation")
					}
					return
				}
				if !strings.Contains(html, `data-drawer="hg-context"`) || !strings.Contains(html, `aria-controls="hg-context"`) {
					t.Fatal("missing narrow navigation opener")
				}
				if strings.Contains(html, "/pulls") || strings.Contains(html, "/help") || strings.Contains(html, "/logout") {
					t.Fatal("unimplemented or unsafe action exposed")
				}
				if !strings.Contains(html, `href="`+sub+`/css/app.css"`) {
					t.Fatal("assets ignore sub URL")
				}
				if tc.kind == Repository {
					keys := []string{}
					for _, n := range s.Navigation {
						keys = append(keys, n.Key)
					}
					expected := "workspace,source,changesets,branches,issues"
					if tc.settings {
						expected += ",settings"
					}
					if strings.Join(keys, ",") != expected {
						t.Fatalf("repo order %v", keys)
					}
				}
			})
		}
	}
}

func TestSharedShellEscapesLabels(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	_, html := shellFixture(t, "/code", Workspace, &models.User{Name: `<img src=x onerror=alert(1)>`}, false, false, false, `<script>alert(1)</script>`)
	if strings.Contains(html, "<script>alert") || strings.Contains(html, "<img src=x") {
		t.Fatal("shell permits injected HTML")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("escaped page title missing")
	}
}

func TestSharedComponentsEscapeAndAssociate(t *testing.T) {
	m := macaron.New()
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Get("/", func(c *macaron.Context) {
		field, err := c.HTMLString("shared/form_field", Field{ID: "name", Name: "name", Label: `<label>`, Value: `\" onfocus=alert(1)`, Hint: "Choose a name", Error: "Name required", Required: true})
		if err != nil {
			t.Fatal(err)
		}
		password, err := c.HTMLString("shared/form_field", Field{ID: "password", Name: "password", Type: "password", Label: "Password", Value: "secret-never-retain"})
		if err != nil {
			t.Fatal(err)
		}
		pages, err := c.HTMLString("shared/pagination", struct {
			Label string
			Pages []PageLink
		}{"Pagination", []PageLink{{Label: "Previous", Disabled: true}, {Label: "1", Current: true}, {Label: "2", URL: "/code/explore?q=laboratory&page=2"}}})
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte(field + password + pages))
	})
	r := httptest.NewRecorder()
	m.ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
	html := r.Body.String()
	for _, want := range []string{`for="name"`, `aria-describedby="name-hint name-error"`, `aria-invalid="true"`, `&lt;label&gt;`, `aria-current="page"`, `aria-disabled="true"`, `href="/code/explore?q=laboratory&amp;page=2"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in %s", want, html)
		}
	}
	if strings.Contains(html, "secret-never-retain") || strings.Contains(html, `<label>`) {
		t.Fatal("secret or unsafe component value rendered")
	}
}

func TestSharedLanguageURL(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	setting.AppSubUrl = "/code"
	languageURL := base.TemplateFuncs["UILanguageURL"].(func(string, string) string)
	if got := languageURL("/code/explore?q=hg&page=2&lang=old", "en-US"); got != "/code/explore?lang=en-US&page=2&q=hg" {
		t.Fatal(got)
	}
	for _, bad := range []string{"https://other.invalid/", "//other.invalid/path", "javascript:alert(1)"} {
		if got := languageURL(bad, "en-US"); got != "/code/?lang=en-US" {
			t.Fatalf("unsafe locale destination: %s", got)
		}
	}
}

func TestSharedShellComposedCapabilities(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	s, html := shellFixture(t, "/code", Workspace, &models.User{Name: "alice"}, false, false, false, "Workspace", func(ctx *middleware.Context) {
		ctx.Data[SearchURL] = "/code/fixture/search"
		ctx.Data[RecentsURL] = "/code/fixture/recents"
		ctx.Data[PullRequestsURL] = "/code/fixture/pulls"
		ctx.Data[HelpURL] = "/code/fixture/help"
		ctx.Data[SignOutURL] = "/code/fixture/signout"
		ctx.Data["CsrfTokenHtml"] = template.HTML(`<input type="hidden" name="_csrf" value="fixture-token">`)
	})
	keys := []string{}
	for _, n := range s.Navigation {
		keys = append(keys, n.Key)
	}
	if strings.Join(keys, ",") != "overview,repositories,organizations,pull_requests,issues" {
		t.Fatalf("workspace order: %v", keys)
	}
	previous := -1
	for _, want := range []string{`data-popout-target="hg-search"`, `href="/code/explore"`, `data-popout-target="hg-recents"`, `data-popout-target="hg-create"`} {
		i := strings.Index(html, want)
		if i <= previous {
			t.Fatalf("utility order missing %s", want)
		}
		previous = i
	}
	if !strings.Contains(html, `<form method="post" action="/code/fixture/signout">`) || !strings.Contains(html, `name="_csrf" value="fixture-token"`) {
		t.Fatal("signout composition must be CSRF POST")
	}
}

func TestSharedMarkdownPreviewSanitizesMarkup(t *testing.T) {
	m := macaron.New()
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Post("/api/v1/markdown", func(c *macaron.Context) {
		v1.Markdown(&middleware.Context{Context: c}, apiv1.MarkdownForm{Mode: c.Query("mode"), Text: c.Query("text"), Context: c.Query("context")})
	})
	payload := `**Safe preview**<script>alert(1)</script><img src=x onerror=alert(1)>[unsafe](javascript:alert(1))`
	body := url.Values{"mode": {"gfm"}, "text": {payload}, "context": {"alice/laboratory"}}.Encode()
	req := httptest.NewRequest("POST", "/api/v1/markdown", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r := httptest.NewRecorder()
	m.ServeHTTP(r, req)
	html := r.Body.String()
	if r.Code != 200 || !strings.Contains(html, "<strong>Safe preview</strong>") {
		t.Fatalf("preview status/content %d: %s", r.Code, html)
	}
	for _, bad := range []string{"<script", "onerror", `href="javascript:`} {
		if strings.Contains(html, bad) {
			t.Fatalf("unsafe preview %s", html)
		}
	}
}

func TestSharedShellAvatarSources(t *testing.T) {
	oldSub, oldDisabled, oldCache, oldSource := setting.AppSubUrl, setting.DisableGravatar, setting.Service.EnableCacheAvatar, setting.GravatarSource
	defer func() {
		setting.AppSubUrl = oldSub
		setting.DisableGravatar = oldDisabled
		setting.Service.EnableCacheAvatar = oldCache
		setting.GravatarSource = oldSource
	}()
	for _, tc := range []struct {
		name                    string
		custom, disabled, cache bool
		want                    string
	}{
		{"uploaded", true, true, true, "/code/avatars/42"},
		{"disabled", false, true, false, "/code/img/avatar_default.jpg"},
		{"cache", false, false, true, "/code/avatar/opaque-avatar-hash"},
		{"configured source", false, false, false, "https://avatar.invalid/opaque-avatar-hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setting.DisableGravatar = tc.disabled
			setting.Service.EnableCacheAvatar = tc.cache
			setting.GravatarSource = "https://avatar.invalid/"
			s, html := shellFixture(t, "/code", Workspace, &models.User{Id: 42, Name: "alice", Email: "private@example.invalid", Avatar: "opaque-avatar-hash", UseCustomAvatar: tc.custom}, false, false, false, "Workspace")
			if s.AvatarURL != tc.want {
				t.Fatalf("avatar URL %q, want %q", s.AvatarURL, tc.want)
			}
			if strings.Contains(html, "private@example.invalid") {
				t.Fatal("raw account email exposed")
			}
		})
	}
}

func TestSharedShellLanguageContext(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	for _, sub := range []string{"", "/code"} {
		shellFixture(t, sub, Anonymous, nil, false, false, false, "Sign in", func(ctx *middleware.Context) { ctx.Data["UILanguageExpected"] = sub + "/?page=2&q=hg" })
	}
}

func TestSharedShellAlerts(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	_, html := shellFixture(t, "/code", Anonymous, nil, false, false, false, "Sign in", func(ctx *middleware.Context) {
		ctx.Data["Flash"] = &session.Flash{ErrorMsg: `<b>Invalid input</b>`, SuccessMsg: "Saved", InfoMsg: "Check your email"}
	})
	for _, want := range []string{`role="alert"><strong>Error:</strong>`, `role="status"><strong>Success:</strong>`, `role="status"><strong>Information:</strong>`, `&lt;b&gt;Invalid input&lt;/b&gt;`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing alert semantics: %s", want)
		}
	}
}

func TestSharedShellConfiguredInstanceIdentity(t *testing.T) {
	oldName, oldSub := setting.AppName, setting.AppSubUrl
	defer func() { setting.AppName = oldName; setting.AppSubUrl = oldSub }()
	for _, name := range []string{"Research laboratory", `Research <img src=x onerror="alert(1)"> & development`} {
		setting.AppName = name
		escaped := template.HTMLEscapeString(name)
		for _, kind := range []ContextKind{Workspace, Anonymous} {
			for _, sub := range []string{"", "/code"} {
				t.Run(string(kind)+sub+name, func(t *testing.T) {
					_, html := shellFixture(t, sub, kind, &models.User{Name: "alice"}, false, false, false, "Overview")
					if !strings.Contains(html, "<title>Overview · "+escaped+" · Hgs</title>") {
						t.Error("browser title lost configured instance or product identity")
					}
					footer := html[strings.Index(html, "<footer"):]
					if !strings.Contains(footer, escaped) || !strings.Contains(footer, "Hgs") || !strings.Contains(footer, "© 2015 GoGits") {
						t.Error("footer lost configured instance, product identity or legal attribution")
					}
					if kind == Anonymous {
						header := html[strings.Index(html, "<header"):strings.Index(html, "</header>")]
						if !strings.Contains(header, escaped) || !strings.Contains(header, "Hgs") {
							t.Error("reduced header lost configured instance or product identity")
						}
					}
					if strings.Contains(html, "<img src=x") {
						t.Error("configured instance name injected markup")
					}
				})
			}
		}
	}
}

func TestSharedShellExploreLanguageContext(t *testing.T) {
	old := setting.AppSubUrl
	defer func() { setting.AppSubUrl = old }()
	for _, sub := range []string{"", "/code"} {
		shellFixture(t, sub, Workspace, &models.User{Name: "alice"}, false, false, false, "Explore", func(ctx *middleware.Context) {
			ctx.Req.URL.Path = "/explore"
			ctx.Req.URL.RawQuery = "q=hg&visibility=private&scope=mine&owner_id=2&page_size=10&page=2&token=secret"
			ctx.Data["UILanguageExpected"] = sub + "/explore?owner_id=2&page=2&page_size=10&q=hg&scope=mine&visibility=private"
		})
	}
}
