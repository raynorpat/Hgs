package cmd

import (
	"github.com/gogits/gogs/models"
	"html"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestSharedUIRegisteredRepositoryActions(t *testing.T) {
	a := newViewApp(t)
	path := "/gogs/alice/project"
	w := a.request(t, "GET", path+"/action/star", "", "alice")
	if models.IsStaring(a.alice.Id, a.repo.Id) || w.Code == 200 {
		t.Fatalf("GET mutation remains registered: %d", w.Code)
	}
	w = a.request(t, "POST", path+"/action/star", "", "alice")
	if w.Code != 400 || models.IsStaring(a.alice.Id, a.repo.Id) {
		t.Fatalf("unprotected star %d", w.Code)
	}
	page := a.request(t, "GET", path+"/settings", "", "alice")
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(token) != 2 {
		t.Fatal("settings missing CSRF")
	}
	for _, action := range []string{"star", "unstar", "unwatch", "watch"} {
		values := url.Values{"_csrf": {html.UnescapeString(token[1])}}
		r := httptest.NewRequest("POST", "http://host"+path+"/action/"+action, strings.NewReader(values.Encode()))
		r.SetBasicAuth("alice", "password")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range page.Result().Cookies() {
			r.AddCookie(c)
		}
		w = httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		if w.Code != 302 {
			t.Fatalf("%s status=%d body=%s", action, w.Code, w.Body.String())
		}
	}
	if models.IsStaring(a.alice.Id, a.repo.Id) || !models.IsWatching(a.alice.Id, a.repo.Id) {
		t.Fatal("independent star/watch state lost")
	}
	w = a.request(t, "GET", path+"/issues", "", "alice")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `action="`+path+`/action/unwatch"`) {
		t.Fatalf("retained canonical Issues Watch unusable: %d", w.Code)
	}
}

func TestRepositoryWatchStarUI(t *testing.T) {
	a := newViewApp(t)
	a.repo.IsPrivate = true
	if err := models.UpdateRepository(a.repo); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: a.bob.Id, RepoId: a.repo.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	path := "/gogs/alice/project"
	page := a.request(t, "GET", path+"/issues", "", "bob")
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if page.Code != 200 || len(token) != 2 {
		t.Fatal("reader repository or CSRF unavailable")
	}
	for _, step := range []struct {
		action         string
		watch, star    bool
		watches, stars int
	}{
		{"watch", true, false, 2, 0}, {"watch", true, false, 2, 0},
		{"star", true, true, 2, 1}, {"star", true, true, 2, 1},
		{"unwatch", false, true, 1, 1}, {"unstar", false, false, 1, 0},
	} {
		values := url.Values{"_csrf": {html.UnescapeString(token[1])}}
		r := httptest.NewRequest("POST", "http://host"+path+"/action/"+step.action, strings.NewReader(values.Encode()))
		r.SetBasicAuth("bob", "password")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range page.Result().Cookies() {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		persisted, err := models.GetRepositoryById(a.repo.Id)
		if w.Code != 302 || err != nil || models.IsWatching(a.bob.Id, a.repo.Id) != step.watch || models.IsStaring(a.bob.Id, a.repo.Id) != step.star || persisted.NumWatches != step.watches || persisted.NumStars != step.stars {
			t.Fatalf("authenticated reader %s: status=%d repo=%+v err=%v", step.action, w.Code, persisted, err)
		}
	}
}
