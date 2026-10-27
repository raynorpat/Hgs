package user_test

import (
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/routers/user"
)

func TestSharedUIWorkspaceSkipsUnavailableRepositories(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		mount := "root"
		if prefix != "" {
			mount = "subURL"
		}
		for _, kind := range []string{"deleting-feed", "missing-feed", "deleting-collaboration"} {
			t.Run(mount+"/"+kind, func(t *testing.T) {
				f := newDiscoveryFixture(t, prefix)
				f.m.Get("/", middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true}), user.Dashboard)
				// Keep an independent authorized issue event to prove filtering doesn't blank the page.
				keep := &models.Action{UserId: f.alice.Id, ActUserId: f.alice.Id, ActUserName: f.alice.Name, OpType: models.CREATE_ISSUE, RepoId: f.repo.Id, RepoUserName: f.alice.Name, RepoName: f.repo.Name, Content: "1|authorized activity remains"}
				if _, err := f.db.Insert(keep); err != nil {
					t.Fatal(err)
				}
				unavailable := &models.Repository{OwnerId: f.alice.Id, Name: "unavailable-lab", LowerName: "unavailable-lab", IsPrivate: true, State: models.RepositoryDeleting}
				if kind == "deleting-collaboration" {
					unavailable.OwnerId = f.bob.Id
				}
				if _, err := f.db.Insert(unavailable); err != nil {
					t.Fatal(err)
				}
				if kind == "deleting-collaboration" {
					if err := models.AddAccess(&models.Access{UserId: f.alice.Id, RepoId: unavailable.Id, Mode: models.READABLE}); err != nil {
						t.Fatal(err)
					}
				} else {
					unavailableEvent := &models.Action{UserId: f.alice.Id, ActUserId: f.alice.Id, ActUserName: f.alice.Name, OpType: models.CREATE_ISSUE, RepoId: unavailable.Id, RepoUserName: f.alice.Name, RepoName: unavailable.Name, Content: "2|unavailable activity must disappear"}
					if _, err := f.db.Insert(unavailableEvent); err != nil {
						t.Fatal(err)
					}
					if kind == "missing-feed" {
						if _, err := f.db.Id(unavailable.Id).Delete(new(models.Repository)); err != nil {
							t.Fatal(err)
						}
					}
				}
				response := f.request("GET", "/", f.alice.Name, "", nil)
				body := response.Body.String()
				if response.Code != 200 {
					t.Fatalf("workspace status=%d, want 200 with unavailable row omitted", response.Code)
				}
				for _, want := range []string{`data-hg-shell`, `id="repositories"`, prefix + "/fixturealice/alice-secret", prefix + "/fixturealice/public-lab", "authorized activity remains"} {
					if !strings.Contains(body, want) {
						t.Errorf("missing unrelated authorized workspace content %q", want)
					}
				}
				for _, hidden := range []string{"unavailable-lab", "unavailable activity must disappear"} {
					if strings.Contains(body, hidden) {
						t.Errorf("rendered unavailable repository data %q", hidden)
					}
				}
			})
		}
	}
}
