package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
)

func TestLandingPublicExplorePolicy(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			a := newViewAppPrefix(t, "", prefix)
			s := a.holder.Service()
			secret, err := models.CreateHgRepository(context.Background(), s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: a.alice.Id, Name: "private-landing-secret", Private: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = models.AddAccess(&models.Access{UserId: a.bob.Id, RepoId: secret.Id, Mode: models.READABLE}); err != nil {
				t.Fatal(err)
			}
			for _, blocked := range []bool{false, true} {
				setting.Service.RequireSignInView = blocked
				a.handler = newWebHandler(a.holder) // capture the real registration gate for each setting
				w := a.request(t, "GET", prefix+"/", "", "")
				body := w.Body.String()
				if w.Code != 200 || !strings.Contains(body, "A painless, self-hosted Mercurial service.") {
					t.Fatalf("landing blocked=%v: %d %s", blocked, w.Code, body)
				}
				if strings.Contains(body, `href="`+prefix+`/explore?visibility=public"`) == blocked {
					t.Errorf("landing Explore policy blocked=%v", blocked)
				}
				if strings.Contains(body, secret.Name) {
					t.Fatal("private landing disclosure")
				}
				if blocked && (strings.Contains(body, "data-landing-catalog") || strings.Contains(body, "data-landing-clone")) {
					t.Fatal("restricted preview/clone rendered")
				}
				for _, route := range []string{"/explore", "/explore/search"} {
					for _, query := range []string{"?visibility=public", "?visibility=all&page_size=1", "?visibility=private&page_size=1&page=2", "?q=secret&visibility=all"} {
						w = a.request(t, "GET", prefix+route+query, "", "")
						want := 200
						if blocked {
							want = 404
						}
						if w.Code != want || strings.Contains(w.Body.String(), secret.Name) || (blocked && strings.Contains(w.Body.String(), a.repo.Name)) {
							t.Fatalf("anonymous %s blocked=%v: %d %s", route+query, blocked, w.Code, w.Body)
						}
						if !blocked && !strings.Contains(query, "q=") && !strings.Contains(w.Body.String(), ">1 repository</p>") {
							t.Fatalf("anonymous public-only count %s: %s", route+query, w.Body)
						}
					}
				}
				w = a.request(t, "GET", prefix+"/explore?visibility=private", "", "bob")
				if w.Code != 200 || !strings.Contains(w.Body.String(), secret.Name) {
					t.Fatal("authorized private catalog lost", w.Code, w.Body)
				}
				w = a.request(t, "GET", prefix+"/", "", "alice")
				if w.Code != 200 || strings.Contains(w.Body.String(), "hgs-mascot.png") || !strings.Contains(w.Body.String(), `class="hg-workspace`) {
					t.Fatal("dashboard routing lost", w.Code, w.Body)
				}
			}
			setting.Service.RequireSignInView = false
			a.repo.IsPrivate = true
			if err := models.UpdateRepository(a.repo); err != nil {
				t.Fatal(err)
			}
			a.handler = newWebHandler(a.holder)
			w := a.request(t, "GET", prefix+"/", "", "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "No matching repositories.") || strings.Contains(w.Body.String(), "data-landing-clone") || strings.Contains(w.Body.String(), secret.Name) {
				t.Fatal("public-empty landing", w.Code, w.Body)
			}
		})
	}
}
