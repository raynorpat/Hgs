package org

import (
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/setting"
	"github.com/macaron-contrib/i18n"
)

func TestOrganizationPeopleLinks(t *testing.T) {
	old := setting.AppSubUrl
	setting.AppSubUrl = "/code"
	defer func() { setting.AppSubUrl = old }()
	m := macaron.New()
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
	m.Use(i18n.I18n(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US"}, Names: []string{"English"}}))
	m.Get("/", func(c *macaron.Context) {
		c.Data["ProfilePeople"] = []*models.User{{Id: 1, Name: "alice", FullName: "Alice"}, {Id: 2, Name: "bob", FullName: "Bob"}}
		body, err := c.HTMLString("shared/profile_people", c.Data)
		if err != nil {
			t.Error(err)
			c.Error(500)
			return
		}
		c.Write([]byte(body))
	})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	for _, name := range []string{"alice", "bob"} {
		if !strings.Contains(w.Body.String(), `href="/code/`+name+`"`) {
			t.Errorf("missing canonical People anchor for %s", name)
		}
	}
	if strings.Contains(w.Body.String(), "/members/action/") {
		t.Error("public People has mutation controls")
	}
}
