package ui

import (
	"html/template"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/macaron-contrib/i18n"
)

func TestProfileRepositoriesUseValidatedOwner(t *testing.T) {
	oldDB, oldLog, oldSub := models.DbCfg, setting.LogRootPath, setting.AppSubUrl
	t.Cleanup(func() {
		models.DbCfg, setting.LogRootPath, setting.AppSubUrl = oldDB, oldLog, oldSub
	})
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.Join(t.TempDir(), "unavailable-owner.db")
	setting.LogRootPath, setting.AppSubUrl = t.TempDir(), "/code"
	// No user table: a redundant owner lookup genuinely fails after the caller
	// has already resolved the owner and selected its readable repositories.
	if err := models.SetEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := models.CloseEngine(); err != nil {
			t.Error(err)
		}
	})
	owner := &models.User{Id: 7, Name: "alice"}
	repo := &models.Repository{Id: 9, OwnerId: owner.Id, Name: "laboratory"}
	if _, err := repo.RepoLink(); err == nil {
		t.Fatal("fixture did not fail the redundant owner lookup")
	}
	m := macaron.New()
	m.Get("/", func(c *macaron.Context) {
		status, err := PrepareProfile(&middleware.Context{Context: c}, owner, nil, []*models.Repository{repo})
		if err != nil {
			t.Fatal(err)
		}
		view := c.Data["Profile"].(ProfileView)
		if status != 200 || len(view.Repositories) != 1 {
			t.Fatalf("readable repository lost after owner lookup failure: status=%d rows=%d", status, len(view.Repositories))
		}
		if view.Repositories[0].URL != "/code/alice/laboratory" || view.Repositories[0].Repository != repo {
			t.Fatalf("canonical repository changed: %+v", view.Repositories[0])
		}
		if repo.Owner != nil {
			t.Error("composition mutated repository owner cache")
		}
		other := *repo
		other.OwnerId++
		delete(c.Data, "Profile")
		status, err = PrepareProfile(&middleware.Context{Context: c}, owner, nil, []*models.Repository{&other})
		if err == nil || status != 500 || c.Data["Profile"] != nil {
			t.Fatal("unexpected owner mismatch became a successful profile")
		}
	})
	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/?tab=repositories", nil))
}

func TestProfileRepositorySummaryRendering(t *testing.T) {
	oldSub := setting.AppSubUrl
	setting.AppSubUrl = "/code"
	t.Cleanup(func() { setting.AppSubUrl = oldSub })
	for _, layout := range []string{"shared/profile_repositories", "user/profile", "org/home"} {
		for _, variant := range []string{"guest", "watching", "starred", "organization-owner", "empty", "activity"} {
			t.Run(layout+"/"+variant, func(t *testing.T) {
				m := macaron.New()
				m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
				m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
				m.Get("/", func(c *macaron.Context) {
					owner := &models.User{Id: 7, Name: "alice"}
					if variant == "organization-owner" {
						owner.Type = models.ORGANIZATION
					}
					view := ProfileView{Owner: owner, URL: "/code/alice", Tab: "repositories", Repositories: []ProfileRepository{{Repository: &models.Repository{Name: "laboratory", Description: "<script>unsafe</script>", IsPrivate: true, NumWatches: 2, NumStars: 3}, URL: "/code/alice/laboratory", Watching: variant == "watching", Starring: variant == "starred"}}}
					if variant == "empty" {
						view.Repositories = nil
					}
					if variant == "activity" {
						view.Tab = "activity"
					}
					c.Data["Profile"], c.Data["UILanguageLink"] = view, view.URL
					c.Data["IsSigned"] = variant != "guest"
					c.Data["IsOrganizationOwner"] = variant == "organization-owner"
					c.Data["CsrfTokenHtml"] = template.HTML(`<input name="_csrf" value="token">`)
					body, err := c.HTMLString(layout, c.Data)
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
				if w.Code != 200 {
					t.Fatalf("render HTTP %d", w.Code)
				}
				if variant == "activity" {
					if strings.Contains(body, "hg-profile-repositories") {
						t.Fatal("activity duplicated repository section")
					}
					return
				}
				if strings.Count(body, "hg-profile-repositories") != 1 {
					t.Fatal("repository section missing or repeated")
				}
				if strings.Contains(body, `/repo/create?org=7`) != (variant == "organization-owner") {
					t.Error("organization create permission mismatch")
				}
				if variant == "empty" {
					if !strings.Contains(body, "No accessible repositories") || strings.Contains(body, "/action/") {
						t.Error("empty state/control mismatch")
					}
					return
				}
				for _, want := range []string{`href="/code/alice/laboratory"`, ">Private</span>", "&lt;script&gt;unsafe&lt;/script&gt;"} {
					if !strings.Contains(body, want) {
						t.Errorf("missing %s", want)
					}
				}
				if variant == "guest" {
					if strings.Contains(body, "/action/") || !strings.Contains(body, "Watch &middot; 2") || !strings.Contains(body, "Star &middot; 3") {
						t.Error("guest counters/mutation controls mismatch")
					}
					return
				}
				for _, action := range []string{"watch", "star"} {
					active := variant == "watching" && action == "watch" || variant == "starred" && action == "star"
					verb, pressed, label, count := action, "false", "Watch", "2"
					if action == "star" {
						label, count = "Star", "3"
					}
					if active {
						verb, pressed, label = "un"+action, "true", "Un"+action
					}
					want := `<form method="post" action="/code/alice/laboratory/action/` + verb + `"><input name="_csrf" value="token"><button class="hg-button" aria-pressed="` + pressed + `">` + label + ` &middot; ` + count + `</button></form>`
					if !strings.Contains(body, want) {
						t.Errorf("independent %s form/state/count/CSRF missing", action)
					}
				}
			})
		}
	}
}
