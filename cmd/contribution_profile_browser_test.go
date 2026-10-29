package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/contributions"
	"github.com/gogits/gogs/modules/setting"
)

type contributionProviderTransport struct {
	sink  *url.URL
	local *http.Transport
}

func (t contributionProviderTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "gravatar.com" || !strings.HasPrefix(r.URL.Path, "/avatar/") {
		return nil, fmt.Errorf("fixture refuses external request: %s", r.URL.Host)
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.sink.Scheme
	u.Host = t.sink.Host
	copy.URL = &u
	copy.Host = t.sink.Host
	return t.local.RoundTrip(copy)
}

// Only canonical fixture seeding. Requests use newWebHandler and ordinary login.
func seedContributionProfileBrowser(t *testing.T, a *viewApp, writer *models.User, pullID, comparisonID int64, mode string) {
	oldKey := setting.SecretKey
	setting.SecretKey = "task23-disposable-browser-key"
	t.Cleanup(func() { setting.SecretKey = oldKey })
	t.Helper()
	if mode != "ready" && mode != "cold" && mode != "unavailable" {
		t.Fatal("UI_CONTRIBUTION_FIXTURE must be ready|cold|unavailable")
	}
	// Stop and join the owned worker before deterministic seeding. Use its existing
	// service for actual native indexing below; no request gets a seeding callback.
	a.holder.contributionCancel()
	<-a.holder.contributionDone
	index := a.holder.contributionIndex
	ctx := context.Background()
	seedProfileReadmeBrowser(t, a)
	seedAvatarBrowser(t, a, writer, "enabled")
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("PROFILE_PROVIDER_FAILURE %s", r.URL.Path)
		http.Error(w, "owned provider failure", 503)
	}))
	sinkURL, err := url.Parse(sink.URL)
	if err != nil {
		t.Fatal(err)
	}
	oldTransport := http.DefaultTransport
	transport := &http.Transport{}
	http.DefaultTransport = contributionProviderTransport{sinkURL, transport}
	t.Cleanup(func() { http.DefaultTransport = oldTransport; transport.CloseIdleConnections(); sink.Close() })
	failed := &models.User{Name: "providerfail", Email: "provider-failure@example.invalid", Passwd: "password", IsActive: true}
	if err = models.CreateUser(failed); err != nil {
		t.Fatal(err)
	}
	org, err := models.GetUserByName("laboratory")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*models.User{a.bob, writer, failed} {
		if err = org.AddMember(u.Id); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []*models.User{a.bob, failed} {
		if err = models.ChangeOrgUserStatus(org.Id, u.Id, true); err != nil {
			t.Fatal(err)
		}
	}
	code, err := models.CreateEmailVerification(ctx, a.alice.Id, a.alice.Email, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = models.ConsumeEmailVerification(ctx, code, "", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	host := a.holder.Service()
	orgRepo, err := models.CreateHgRepository(ctx, host.Runner(), host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: org.Id, Name: "calendar"})
	if err != nil {
		t.Fatal(err)
	}
	readme, err := models.GetRepositoryByName(a.alice.Id, "alice")
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(a.root, "profile-work-alice")
	if err = os.WriteFile(filepath.Join(work, "contribution.txt"), []byte("Native verified author fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if err := host.Runner().Run(ctx, work, args, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "contribution.txt")
	run("commit", "-u", "Alice <"+a.alice.Email+">", "-d", "2024-02-29 12:00:00 +0000", "-m", "Leap day verified contribution")
	dest, err := models.RepositoryStoragePath(host.RepositoryRoot(), readme.Id)
	if err != nil {
		t.Fatal(err)
	}
	run("push", dest)

	unavailableUser := &models.User{Name: "unreadable", Email: "unreadable@example.invalid", Passwd: "password", IsActive: true}
	if err = models.CreateUser(unavailableUser); err != nil {
		t.Fatal(err)
	}
	binaryRepo, err := models.CreateHgRepository(ctx, host.Runner(), host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: unavailableUser.Id, Name: "unreadable"})
	if err != nil {
		t.Fatal(err)
	}
	binaryWork := filepath.Join(a.root, "binary-readme-work")
	binaryRun := func(args ...string) {
		t.Helper()
		if err := host.Runner().Run(ctx, binaryWork, args, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = host.Runner().Run(ctx, "", []string{"init", binaryWork}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(binaryWork, "README.md"), []byte{0, 1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	binaryRun("add", "README.md")
	binaryRun("commit", "-u", "Fixture", "-m", "Binary README fixture")
	binaryDest, err := models.RepositoryStoragePath(host.RepositoryRoot(), binaryRepo.Id)
	if err != nil {
		t.Fatal(err)
	}
	binaryRun("push", binaryDest)
	db := draftFixtureDB(t)
	// 36 monthly repository groups, with one 30-event group: 65 issue contributions.
	for _, repo := range []*models.Repository{a.repo, readme, orgRepo} {
		for month := 1; month <= 12; month++ {
			count := 1
			if repo.Id == a.repo.Id && month == 2 {
				count = 30
			}
			for n := 0; n < count; n++ {
				day := time.Date(2024, time.Month(month), 15, 12, 0, 0, 0, time.UTC)
				if month == 2 {
					day = time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
				}
				i := &models.Issue{RepoId: repo.Id, PosterId: a.alice.Id, Name: fmt.Sprintf("Calendar %s %02d / %02d <escaped>", repo.Name, month, n), Content: "Canonical fixture issue for contribution navigation."}
				if err = models.NewIssue(i); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("UPDATE issue SET created = ? WHERE id = ?", day.Format("2006-01-02 15:04:05"), i.Id); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err = models.SubmitPullReview(ctx, writer.Id, pullID, comparisonID, models.ReviewApproved); err != nil {
		t.Fatal(err)
	}
	// Drain committed canonical work explicitly, using the same index methods and
	// durable completion API as the owned worker. No generated events/DTOs.
	if err = index.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for after := int64(0); ; {
		jobs, e := models.PendingContributionJobs(ctx, after, 100)
		if e != nil {
			t.Fatal(e)
		}
		for _, job := range jobs {
			after = job.ID
			if job.Kind == "repository" {
				e = index.IndexRepository(ctx, job.ObjectID)
			} else {
				e = index.IndexObject(ctx, job.Kind, job.ObjectID)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = models.CompleteContributionJob(ctx, job.ID, job.Requested); e != nil {
				t.Fatal(e)
			}
		}
		if len(jobs) < 100 {
			break
		}
	}
	if mode == "cold" {
		if _, err = models.BeginContributionGeneration(ctx, orgRepo.Id); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "unavailable" {
		if err = models.FailContributionGeneration(ctx, orgRepo.Id); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []contributions.Scope{{OwnerID: a.alice.Id}, {OwnerID: org.Id, Organization: true}, {OwnerID: writer.Id}} {
		p, e := index.Read(ctx, 0, scope, contributions.Query{Year: 2024})
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("CONTRIBUTION_FIXTURE scope=%+v year=2024 totals=%+v state=%s groups-first=%d has-more=%t", scope, p.Totals, p.State, len(p.Groups), p.NextCursor != "")
	}
	t.Logf("CONTRIBUTION_FIXTURE mode=%s normal login alice|bob|writer/password; user /code/alice?year=2024; org /code/org/laboratory?year=2024; People public=bob,providerfail; private=alice,writer; missing README bob; unavailable binary README /code/unreadable; provider failure /code/providerfail; private README organization (org members); leap-day user changesets=1 issues=32 total33; year2024 user changesets=1 issues=65 total66, org issues=12; 2026 PR alice/review writer; owner membership control /code/org/laboratory/members; worker stopped/joined, canonical mutations remain query-visible; stop=existing /fixture/stop, automatic20min", mode)
}
