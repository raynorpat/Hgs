//go:build integration

package integration

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/middleware"
	v1 "github.com/gogits/gogs/routers/api/v1"
	"github.com/gogits/gogs/routers/repo"
	"github.com/macaron-contrib/binding"
)

func TestForkSharedFormAndDiscovery(t *testing.T) {
	f := newUIFixture(t, "/code")
	guard := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	f.m.Group("/:username/:reponame", func() {
		f.m.Get("/fork", guard, repo.Fork)
		f.m.Post("/fork", guard, binding.BindIgnErr(auth.CreateRepoForm{}), repo.ForkPost)
	}, middleware.RepoAssignment(true))
	f.m.Get("/repo/forks/:id", guard, repo.ForkProgress)
	f.m.Get("/repo/forks/:id/status", guard, repo.ForkStatus)
	f.m.Get("/api/v1/user/repos", guard, v1.ListMyRepos)
	f.m.Get("/api/v1/repos/search", v1.SearchRepos)
	root := "/fixtureowner/private-lab"
	if err := models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.private.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	page := f.get(t, root+"/fork", "reader")
	if page.Code != 200 {
		t.Fatalf("form: %d %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"data-hg-shell", `action="/code` + root + `/fork"`, `name="_csrf"`, `name="repo_name"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("missing form %s", want)
		}
	}
	invalid := url.Values{"uid": {fmt.Sprint(f.reader.Id)}, "repo_name": {"bad/name"}, "desc": {"keep description"}}
	w := f.post(t, root+"/fork", "reader", invalid, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "keep description") || !strings.Contains(w.Body.String(), "bad/name") {
		t.Fatalf("invalid retry: %d %s", w.Code, w.Body.String())
	}
	valid := url.Values{"uid": {fmt.Sprint(f.reader.Id)}, "repo_name": {"private-copy"}, "desc": {"fork description"}}
	if w = f.post(t, root+"/fork", "reader", valid, false); w.Code != 400 {
		t.Fatalf("CSRF: %d", w.Code)
	}
	w = f.post(t, root+"/fork", "reader", valid, true)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "/code/repo/forks/") {
		t.Fatalf("fork progress redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	progress := strings.TrimPrefix(w.Header().Get("Location"), "/code")
	if page = f.get(t, progress, "reader"); page.Code != 200 || !strings.Contains(page.Body.String(), "data-fork-progress") {
		t.Fatalf("progress page: %d %s", page.Code, page.Body.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		w = f.get(t, progress+"/status", "reader")
		if w.Code == 200 && strings.Contains(w.Body.String(), "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fork did not finish: %d %s", w.Code, w.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	fork, err := models.GetRepositoryByName(f.reader.Id, "private-copy")
	if err != nil {
		t.Fatal(err)
	}
	if !fork.IsPrivate {
		t.Fatal("fork visibility not inherited")
	}
	status := fmt.Sprintf("/repo/forks/%d/status", fork.Id)
	if w = f.get(t, status, "reader"); w.Code != 200 || !strings.Contains(w.Body.String(), "ready") {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	if w = f.get(t, "/api/v1/user/repos", "reader"); w.Code != 200 || !strings.Contains(w.Body.String(), `"full_name":"fixturereader/private-copy"`) || !strings.Contains(w.Body.String(), `"admin":true`) {
		t.Fatalf("API DTO/permissions: %d %s", w.Code, w.Body.String())
	}
	if err = models.DeleteAccess(&models.Access{UserId: f.reader.Id, RepoId: f.private.Id}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api/v1/user/repos", "/api/v1/repos/search?q=private-copy&limit=1"} {
		w = f.get(t, route, "reader")
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-copy") {
			t.Errorf("ancestor API leak %s: %d %s", route, w.Code, w.Body.String())
		}
	}
	if w = f.get(t, status, "reader"); w.Code != 404 {
		t.Fatalf("revoked status: %d", w.Code)
	}
	if w = f.get(t, root+"/fork", "reader"); w.Code != 404 {
		t.Fatalf("revoked source form: %d", w.Code)
	}
}

func TestForkLifecycleMessages(t *testing.T) {
	f := newUIFixture(t, "/code")
	if _, err := f.db.Insert(&models.ForkRelation{RepositoryID: f.empty.Id, SourceID: f.private.Id, FamilyID: f.private.Id}); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteAccess(&models.Access{UserId: f.reader.Id, RepoId: f.private.Id}); err != nil {
		t.Fatal(err)
	}

	root := "/fixtureowner/private-lab"
	for _, action := range []string{"update", "transfer", "delete"} {
		values := url.Values{"repo_name": {"private-lab"}, "password": {"fixture-password"}, "new_owner_name": {"fixturereader"}}
		w := f.post(t, root+"/settings?action="+action, "owner", values, true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "dependent forks") {
			t.Fatalf("parent %s denial: %d %s", action, w.Code, w.Body.String())
		}
	}
	values := url.Values{"repo_name": {"empty-lab"}, "password": {"fixture-password"}, "new_owner_name": {"fixturereader"}}
	w := f.post(t, "/fixtureowner/empty-lab/settings?action=transfer", "owner", values, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private ancestors") {
		t.Fatalf("unsafe transfer denial: %d %s", w.Code, w.Body.String())
	}
}
