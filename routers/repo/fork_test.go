package repo

import (
	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/auth"
	"github.com/macaron-contrib/binding"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestForkFormIgnoresClientPathsAndSource(t *testing.T) {
	// The shared binding contract has no storage/source URL field. The actual
	// handler obtains source identity from authorized route metadata (HTTP test).
	m := macaron.New()
	var got auth.CreateRepoForm
	m.Post("/fork", binding.Bind(auth.CreateRepoForm{}), func(f auth.CreateRepoForm) { got = f })
	values := url.Values{"uid": {"12"}, "repo_name": {"copy"}, "desc": {"description"}, "source_id": {"999"}, "clone_url": {"https://attacker.invalid"}, "path": {"../escape"}}
	r := httptest.NewRequest(http.MethodPost, "/fork", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	if w.Code != 200 || got.Uid != 12 || got.RepoName != "copy" || got.Description != "description" {
		t.Fatalf("bound fork input: %d %+v", w.Code, got)
	}
}
