package auth

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Unknwon/macaron"
	"github.com/macaron-contrib/binding"
)

func TestSpecialRepositoryName(t *testing.T) {
	for _, form := range []interface{}{CreateRepoForm{}, RepoSettingForm{}} {
		m := macaron.New()
		m.Post("/", binding.BindIgnErr(form), func(errs binding.Errors) string {
			if len(errs) > 0 {
				return "invalid"
			}
			return "valid"
		})
		r := httptest.NewRequest("POST", "/", strings.NewReader("uid=1&repo_name=.github"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "valid" {
			t.Fatalf("%T .github: %d %s", form, w.Code, w.Body.String())
		}
	}
}
