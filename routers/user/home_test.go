package user

import (
	"context"
	"errors"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/contributions"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/profile"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
	"github.com/macaron-contrib/i18n"
)

// A misplaced introduction or missing ordinary tab link breaks profile navigation.
func TestProfileApprovedLayout(t *testing.T) {
	old := setting.AppSubUrl
	setting.AppSubUrl = "/code"
	defer func() { setting.AppSubUrl = old }()
	m := macaron.New()
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
	m.Get("/", func(c *macaron.Context) {
		owner := &models.User{Id: 1, Name: "alice", FullName: "Alice <script>"}
		c.Data["Owner"] = owner
		c.Data["UILanguageLink"] = "/code/alice"
		c.Data["Profile"] = ui.ProfileView{Owner: owner, URL: "/code/alice", Tab: "repositories", Repositories: []ui.ProfileRepository{{Repository: &models.Repository{Name: "private-lab", IsPrivate: true}, URL: "/code/alice/private-lab"}}}
		c.Data["ProfileReadme"] = profile.Content{State: "ready", HTML: template.HTML("<p>Safe introduction</p>")}
		c.Data["TabName"] = "repositories"
		body, err := c.HTMLString("user/profile", c.Data)
		if err != nil {
			t.Error(err)
			c.Error(500)
			return
		}
		c.Write([]byte(body))
	})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()
	if !utf8.ValidString(body) || strings.ContainsRune(body, utf8.RuneError) {
		t.Error("profile response contains invalid or replacement Unicode in visible labels")
	}
	identity, readme, tabs := strings.Index(body, "data-profile-identity"), strings.Index(body, "data-profile-readme"), strings.Index(body, "data-profile-tabs")
	if w.Code != 200 || identity < 0 || readme < identity || tabs < readme {
		t.Fatalf("identity, README, tabs must appear in order: %d %d %d HTTP %d", identity, readme, tabs, w.Code)
	}
	for _, want := range []string{`href="/code/alice?tab=overview"`, `href="/code/alice?tab=activity"`, `Alice &lt;script&gt;`, `>Private</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, `href="/code/user/settings"`) {
		t.Error("visitor has owner control")
	}
}

type profileReaderFixture struct {
	page contributions.Page
	err  error
	t    *testing.T
}

func (f profileReaderFixture) Contributions() contributions.Reader { return f }
func (f profileReaderFixture) Read(_ context.Context, viewer int64, scope contributions.Scope, q contributions.Query) (contributions.Page, error) {
	if viewer != 0 || scope.OwnerID != 7 || scope.Organization || q.Year != 2024 || q.Day != "2024-02-29" || q.Kind != "review" {
		f.t.Errorf("wrong query scope/filter: viewer=%d scope=%+v query=%+v", viewer, scope, q)
	}
	return f.page, f.err
}

func TestProfileContributionStates(t *testing.T) {
	old := setting.AppSubUrl
	setting.AppSubUrl = "/code"
	defer func() { setting.AppSubUrl = old }()
	for _, tc := range []struct {
		state  string
		err    error
		status int
		want   string
	}{
		{"ready", nil, 200, "No accessible contributions"},
		{"updating", nil, 200, "Contributions are updating"},
		{"unavailable", errors.New("private backend detail"), 200, "Contributions are temporarily unavailable"},
		{"", contributions.ErrStaleCursor, 409, "Restart activity"},
	} {
		t.Run(tc.state+tc.want, func(t *testing.T) {
			m := macaron.New()
			m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
			m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
			m.Get("/", func(c *macaron.Context) {
				ctx := &middleware.Context{Context: c}
				c.Data["UILanguageLink"] = "/code/alice"
				c.Data["ProfileReadme"] = profile.Content{State: "absent"}
				status := ui.PrepareProfile(ctx, &models.User{Id: 7, Name: "alice"}, profileReaderFixture{page: contributions.Page{State: tc.state, Years: []int{2024}}, err: tc.err, t: t}, nil)
				body, err := c.HTMLString("user/profile", c.Data)
				if err != nil {
					t.Fatal(err)
				}
				c.Resp.WriteHeader(status)
				c.Write([]byte(body))
			})
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest("GET", "/?tab=activity&year=2024&day=2024-02-29&kind=review&cursor=old-token", nil))
			body := w.Body.String()
			if w.Code != tc.status || !strings.Contains(body, tc.want) {
				t.Errorf("state HTTP %d body missing %s", w.Code, tc.want)
			}
			if strings.Contains(body, "private backend detail") {
				t.Error("backend error disclosed")
			}
			if tc.status == 409 && !strings.Contains(body, `href="/code/alice?day=2024-02-29&amp;kind=review&amp;tab=activity&amp;year=2024"`) {
				t.Error("restart lost filters or retained cursor")
			}
		})
	}
}

func TestProfileGermanLabels(t *testing.T) {
	m := macaron.New()
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"de-DE"}, Names: []string{"Deutsch"}}))
	m.Get("/", func(c *macaron.Context) {
		c.Data["Profile"] = ui.ProfileView{Page: contributions.Page{State: "ready"}, Filters: []ui.NavItem{{Key: "changeset", URL: "/?kind=changeset"}, {Key: "review", URL: "/?kind=review"}}, Groups: []ui.ProfileGroup{{ActivityGroup: contributions.ActivityGroup{Kind: "review", Title: "alice/project", Count: 1}, Month: "2024-02"}}}
		body, err := c.HTMLString("shared/contribution_activity", c.Data)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte(body))
	})
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Accept-Language", "de-DE")
	m.ServeHTTP(w, request)
	body := w.Body.String()
	if !utf8.ValidString(body) || strings.ContainsRune(body, utf8.RuneError) {
		t.Fatal("German profile labels are not valid UTF8")
	}
	for _, want := range []string{"\u00c4nderungss\u00e4tze", "\u00dcberpr\u00fcfungen", "&middot;"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing localized rendered label %q", want)
		}
	}
}
