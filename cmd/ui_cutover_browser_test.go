package cmd

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
)

// Opt-in, owned real production router fixture. Test-only stop handling wraps
// the handler rather than registering any production fixture API.
func TestSharedUICutoverBrowserFixture(t *testing.T) {
	if os.Getenv("UI_CUTOVER_FIXTURE") != "1" {
		t.Skip("opt-in disposable Task15 browser fixture")
	}
	executable := os.Getenv("GOGS_TEST_HG")
	if executable == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	oldAvatar, oldGravatar := setting.AvatarUploadPath, setting.DisableGravatar
	setting.AvatarUploadPath = filepath.Join(t.TempDir(), "avatars")
	setting.DisableGravatar = true
	t.Cleanup(func() { setting.AvatarUploadPath = oldAvatar; setting.DisableGravatar = oldGravatar })
	a := newViewAppPrefix(t, executable, "/code")
	setting.Langs = []string{"en-US", "de-DE"}
	setting.Names = []string{"English", "Deutsch"}
	setting.SessionConfig.CookieName = "cutover_" + filepath.Base(filepath.Dir(a.root))
	if os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		setting.SessionConfig.Maxlifetime = 45
		setting.SessionConfig.Gclifetime = 2
	}
	writer := &models.User{Name: "writer", Email: "writer@example.invalid", Passwd: "password", IsActive: true}
	if err := models.CreateUser(writer); err != nil {
		t.Fatal(err)
	}
	for _, grant := range []struct {
		u    *models.User
		mode models.AccessType
	}{{a.bob, models.READABLE}, {writer, models.WRITABLE}} {
		if err := models.AddAccess(&models.Access{UserId: grant.u.Id, RepoId: a.repo.Id, Mode: grant.mode}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.CreateOrganization(&models.User{Name: "laboratory", Type: models.ORGANIZATION, IsActive: true, Email: "lab@example.invalid", Description: "Disposable laboratory organization"}, a.alice); err != nil {
		t.Fatal(err)
	}
	host := a.holder.Service()
	ctx := context.Background()
	work := filepath.Join(a.root, "work")
	run := func(path string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := host.Runner().Run(ctx, path, args, nil, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	run("", "init", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Shared UI laboratory\nNative Mercurial fixture for Task15.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(work, "add")
	run(work, "commit", "-u", "alice", "-m", "initial laboratory")
	target, _ := models.RepositoryStoragePath(host.RepositoryRoot(), a.repo.Id)
	run(work, "push", target)
	service := collaboration.New(a.holder)
	source, err := service.Fork(ctx, a.alice.Id, collaboration.ForkOptions{SourceID: a.repo.Id, OwnerID: a.alice.Id, Name: "proposal-with-a-long-name-for-shared-navigation"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "proposal.txt"), []byte("line one\nline two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(work, "add")
	run(work, "commit", "-u", "alice", "-m", "review proposal")
	sourcePath, _ := models.RepositoryStoragePath(host.RepositoryRoot(), source.Id)
	run(sourcePath, "pull", work)
	selection := `{"Ref":{"Kind":"branch","Name":"default"}}`
	pull, err := models.CreatePullRecord(ctx, a.alice.Id, models.PullRequest{SourceRepoID: source.Id, TargetRepoID: a.repo.Id, Title: "Review the laboratory proposal", Body: "**Owned fixture** for native shared UI acceptance.", SourceSelection: selection, TargetSelection: selection})
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := service.RefreshComparison(ctx, a.alice.Id, pull.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = models.AssignPullReviewers(ctx, a.alice.Id, pull.ID, []int64{writer.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err = models.AddPullComment(ctx, a.bob.Id, pull.ID, "Reader comment for keyboard and mobile review.", nil); err != nil {
		t.Fatal(err)
	}
	label := &models.Label{RepoId: a.repo.Id, Name: "needs review", Color: "#0747a6"}
	if err = models.NewLabel(label); err != nil {
		t.Fatal(err)
	}
	milestone := &models.Milestone{RepoId: a.repo.Id, Index: 1, Name: "Shared UI acceptance", Deadline: time.Now().Add(7 * 24 * time.Hour)}
	if err = models.NewMilestone(milestone); err != nil {
		t.Fatal(err)
	}
	if err = models.NewIssue(&models.Issue{RepoId: a.repo.Id, Index: 1, PosterId: a.alice.Id, Name: "Check the laboratory navigation at narrow widths", Content: "**Task15:** inspect labels, milestones, settings and issue forms.", MilestoneId: milestone.Id}); err != nil {
		t.Fatal(err)
	}
	a.handler = newWebHandler(a.holder)
	if os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		stopDrafts := startDraftPurger(user.NewDraftService().PurgeExpired, func() bool { return a.holder.Service() != nil })
		defer stopDrafts()
		t.Log("DRAFT_FIXTURE: normal login only; memory session lifetime=45s idle, GC interval=2s; draft TTL remains production 30m; owned purger active until fixture exit")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var once sync.Once
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/fixture/stop" {
			w.Write([]byte("stopping owned fixture"))
			once.Do(func() { close(stop) })
			return
		}
		a.handler.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("CUTOVER_BROWSER_URL=http://%s/code/; sign in /code/user/login; alice/admin, writer/write, bob/read; passwords=password; guest=POST logout. Locales ?lang=en-US or ?lang=de-DE; PR /code/alice/project/pulls/1 comparison=%d; issues /code/alice/project/issues/1; organization /code/org/laboratory; shutdown http://%s/fixture/stop; root=%s", listener.Addr(), comparison.ID, listener.Addr(), a.root)
	select {
	case <-stop:
	case <-time.After(60 * time.Minute):
	}
	shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = server.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
