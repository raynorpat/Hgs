package cmd

import (
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
)

// These requests use the same handler registration as server startup. Adding
// any removed route, including an authenticated org hook route, fails this gate.
func TestGitAndDeferredRoutesAbsent(t *testing.T) {
	a := newViewApp(t)
	if _, err := models.CreateOrganization(&models.User{Name: "acme", Email: "acme@example.test"}, a.alice); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/alice/project.git/info/refs?service=git-upload-pack", "/alice/project.git/git-upload-pack", "/alice/project.git/git-receive-pack",
		"/alice/project/info/refs?service=git-receive-pack", "/alice/project/git-upload-pack", "/alice/project/git-receive-pack",
		"/alice/project/archive/main.zip", "/repo/fork/1", "/repo/migrate", "/repo/mirror",
		"/alice/project/releases", "/alice/project/releases/new", "/alice/project/releases/edit/v1",
		"/alice/project/settings/hooks", "/alice/project/settings/hooks/new", "/alice/project/settings/hooks/gogs/new", "/alice/project/settings/hooks/slack/new",
		"/alice/project/settings/hooks/1", "/alice/project/settings/hooks/gogs/1", "/alice/project/settings/hooks/slack/1", "/alice/project/settings/hooks/1/delete",
		"/alice/project/settings/hooks/git", "/alice/project/settings/hooks/git/update",
		"/org/acme/settings/hooks", "/org/acme/settings/hooks/new", "/org/acme/settings/hooks/gogs/new", "/org/acme/settings/hooks/slack/new",
		"/org/acme/settings/hooks/1", "/org/acme/settings/hooks/gogs/1", "/org/acme/settings/hooks/slack/1",
		"/api/v1/repos/alice/project/hooks", "/api/v1/repos/alice/project/hooks/1", "/api/v1/repos/migrate", "/api/v1/user/repos", "/api/v1/orgs/acme/repos",
	}
	for _, user := range []string{"", "alice"} {
		for _, method := range []string{"GET", "POST"} {
			for _, path := range paths {
				// Listing repositories remains supported; creating through the old
				// Git API does not.
				if method == "GET" && path == "/api/v1/user/repos" {
					continue
				}
				w := a.request(t, method, "/gogs"+path, "", user)
				if w.Code != 404 && w.Code != 405 {
					t.Errorf("%s %s user=%q: removed endpoint returned %d", method, path, user, w.Code)
				}
			}
		}
	}
	w := a.request(t, "GET", "/gogs/org/acme/settings", "", "alice")
	if w.Code != 200 || strings.Contains(w.Body.String(), "/settings/hooks") {
		t.Fatalf("organization settings retain hook navigation: %d %s", w.Code, w.Body)
	}
}

func TestDashboardRetainedFeedLinksAndNavigation(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{{"root", ""}, {"subpath", "/gogs"}} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := tc.prefix
			a := newViewAppPrefix(t, "", prefix)
			w := a.request(t, "GET", prefix+"/", "", "alice")
			if w.Code != 200 {
				t.Fatalf("dashboard: %d %s", w.Code, w.Body)
			}
			body := w.Body.String()
			if strings.Contains(body, "/gogs//gogs/") || strings.Contains(body, "dashboard-my-mirror") || strings.Contains(body, "/repo/migrate") {
				t.Fatalf("dashboard retains broken feed URL or deferred navigation: %s", body)
			}
			if !strings.Contains(body, `created repository <a href="`+prefix+`/alice/project"`) {
				t.Fatal("missing absolute create activity link", body)
			}
		})
	}
}

func TestRepositorySearchUsesMercurialMetadata(t *testing.T) {
	a := newViewApp(t)
	w := a.request(t, "GET", "/gogs/api/v1/repos/search?q=project", "", "")
	if w.Code != 200 {
		t.Fatalf("search: %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"clone_url": "http://example.test/gogs/hg/alice/project"`) || strings.Contains(body, `"fork"`) || strings.Contains(body, ".git") {
		t.Fatalf("repository search contains false Git metadata: %s", body)
	}
}

func TestForkRouteAndHeaderAvailable(t *testing.T) {
	a := newViewApp(t)
	for _, path := range []string{"/gogs/alice/project", "/gogs/alice/project/fork"} {
		w := a.request(t, "GET", path, "", "alice")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "/gogs/alice/project/fork") {
			t.Fatalf("fork route/header %s: %d %s", path, w.Code, w.Body)
		}
	}
}
