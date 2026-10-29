package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
)

// Startup-only options for owned disposable browser failure-injection fixtures.
type draftFixtureOptions struct {
	idle            int64
	prefix, failure string
	discard         *draftFixtureDiscardObserver
}

func parseDraftFixtureOptions(idle, prefix, failure string) (draftFixtureOptions, error) {
	o := draftFixtureOptions{idle: 45, prefix: "/code", failure: failure}
	var err error
	if idle != "" {
		o.idle, err = strconv.ParseInt(idle, 10, 64)
	}
	if err != nil || o.idle < 10 || o.idle > 300 {
		return o, fmt.Errorf("fixture idle must be10..300 seconds")
	}
	if prefix == "root" {
		o.prefix = ""
	} else if prefix != "" {
		if !regexp.MustCompile(`^/draft-[a-z]+$`).MatchString(prefix) {
			return o, fmt.Errorf("fixture prefix must be root or /draft-<letters>")
		}
		o.prefix = prefix
	}
	switch failure {
	case "", "save-outage", "reject-cookie", "delay-save", "drop-discard":
	default:
		return o, fmt.Errorf("unknown fixture failure mode")
	}
	return o, nil
}
func installDraftFixtureOutage(db *xorm.Engine) error {
	_, err := db.Exec("CREATE TRIGGER draft_fixture_save_outage BEFORE UPDATE OF payload_json ON editor_draft WHEN NEW.discarded = 0 BEGIN SELECT RAISE(ABORT, 'fixture draft save unavailable'); END")
	return err
}

func configureDraftFixtureAttachments(t *testing.T, root string) {
	t.Helper()
	enabled, path, types, files, size := setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles, setting.AttachmentMaxSize
	uploads := filepath.Join(root, "attachments")
	if err := os.MkdirAll(uploads, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles, setting.AttachmentMaxSize = enabled, path, types, files, size
	})
	setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles, setting.AttachmentMaxSize = true, uploads, "image/png", 10, 32
}

type draftFixtureResponse struct {
	http.ResponseWriter
	ctx                          context.Context
	delay                        time.Duration
	rejectCookie, wrote, aborted bool
}

func (w *draftFixtureResponse) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if status == http.StatusOK && w.delay > 0 {
		timer := time.NewTimer(w.delay)
		defer timer.Stop()
		select {
		case <-w.ctx.Done():
			w.aborted = true
			return
		case <-timer.C:
		}
	}
	if w.rejectCookie {
		cookies := w.Header().Values("Set-Cookie")
		w.Header().Del("Set-Cookie")
		for _, cookie := range cookies {
			if strings.HasPrefix(cookie, "hgs_draft=") {
				cookie += "; Domain=draft-cookie-rejected.invalid"
			}
			w.Header().Add("Set-Cookie", cookie)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *draftFixtureResponse) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.aborted {
		return 0, w.ctx.Err()
	}
	return w.ResponseWriter.Write(body)
}
func draftFixtureHandler(base http.Handler, o draftFixtureOptions) http.Handler {
	if o.failure == "drop-discard" {
		path := regexp.MustCompile("^" + regexp.QuoteMeta(o.prefix) + `/user/drafts/([0-9a-f]{64})/discard$`)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			match := path.FindStringSubmatch(r.URL.Path)
			if r.Method != http.MethodPost || match == nil {
				base.ServeHTTP(w, r)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				panic(http.ErrAbortHandler)
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				panic(http.ErrAbortHandler)
			}
			if err = connection.Close(); err != nil {
				panic(http.ErrAbortHandler)
			}
			if o.discard != nil {
				o.discard.dropped(match[1])
			}
		})
	}
	if o.failure != "reject-cookie" && o.failure != "delay-save" {
		return base
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := &draftFixtureResponse{ResponseWriter: w, ctx: r.Context(), rejectCookie: o.failure == "reject-cookie"}
		if o.failure == "delay-save" && r.Method == "POST" && strings.HasPrefix(r.URL.Path, o.prefix+"/user/drafts/") && strings.HasSuffix(r.URL.Path, "/save") {
			// Native auth and model commit run normally. Only acknowledgement
			// emission waits while the real memory session expires naturally.
			response.delay = time.Duration(o.idle+3) * time.Second
		}
		base.ServeHTTP(response, r)
	})
}

// Opt-in, owned real production router fixture. Test-only stop handling wraps
// the handler rather than registering any production fixture API.
func TestSharedUICutoverBrowserFixture(t *testing.T) {
	if os.Getenv("UI_CUTOVER_FIXTURE") != "1" {
		t.Skip("opt-in disposable Task15 browser fixture")
	}
	profileFixture := os.Getenv("UI_PROFILE_FIXTURE") == "1"
	contributionFixture := os.Getenv("UI_CONTRIBUTION_FIXTURE")
	if contributionFixture != "" && (profileFixture || os.Getenv("UI_AVATAR_FIXTURE") != "" || os.Getenv("UI_DRAFT_FIXTURE") != "") {
		t.Fatal("contribution fixture requires exclusive fixture mode")
	}
	avatarFixture := os.Getenv("UI_AVATAR_FIXTURE")
	if avatarFixture != "" && (avatarFixture != "enabled" && avatarFixture != "disabled" || profileFixture || os.Getenv("UI_DRAFT_FIXTURE") == "1") {
		t.Fatal("avatar fixture requires enabled|disabled and exclusive fixture mode")
	}
	if profileFixture && os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		t.Fatal("profile and draft fixture modes are mutually exclusive")
	}
	executable := os.Getenv("GOGS_TEST_HG")
	if executable == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	options, err := parseDraftFixtureOptions("", "", "")
	if os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		options, err = parseDraftFixtureOptions(os.Getenv("UI_DRAFT_IDLE_SECONDS"), os.Getenv("UI_DRAFT_PREFIX"), os.Getenv("UI_DRAFT_FAILURE"))
	}
	if err != nil {
		t.Fatal(err)
	}
	oldAvatar, oldGravatar := setting.AvatarUploadPath, setting.DisableGravatar
	setting.AvatarUploadPath = filepath.Join(t.TempDir(), "avatars")
	setting.DisableGravatar = true
	t.Cleanup(func() { setting.AvatarUploadPath = oldAvatar; setting.DisableGravatar = oldGravatar })
	a := newViewAppPrefix(t, executable, options.prefix)
	setting.Langs = []string{"en-US", "de-DE"}
	setting.Names = []string{"English", "Deutsch"}
	setting.SessionConfig.CookieName = "cutover_" + filepath.Base(filepath.Dir(a.root))
	if os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		setting.SessionConfig.Maxlifetime = options.idle
		setting.SessionConfig.Gclifetime = 2
	}
	if profileFixture || avatarFixture != "" || contributionFixture != "" {
		oldLifetime, oldGC := setting.SessionConfig.Maxlifetime, setting.SessionConfig.Gclifetime
		t.Cleanup(func() { setting.SessionConfig.Maxlifetime, setting.SessionConfig.Gclifetime = oldLifetime, oldGC })
		setting.SessionConfig.Maxlifetime, setting.SessionConfig.Gclifetime = 300, 2
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
	requests, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
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
	if os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		configureDraftFixtureAttachments(t, a.root)
	}
	if profileFixture {
		seedProfileReadmeBrowser(t, a)
	}
	if avatarFixture != "" {
		seedAvatarBrowser(t, a, writer, avatarFixture)
	}
	if contributionFixture != "" {
		seedContributionProfileBrowser(t, a, writer, pull.ID, comparison.ID, contributionFixture)
	}
	if contributionFixture != "" && os.Getenv("UI_CONTRIBUTION_VALIDATE") == "1" {
		return
	}
	a.handler = newWebHandler(a.holder)
	if os.Getenv("UI_DRAFT_FIXTURE") == "1" {
		if options.failure == "save-outage" {
			db := draftFixtureDB(t)
			if err := installDraftFixtureOutage(db); err != nil {
				t.Fatal(err)
			}
		}
		if options.failure == "drop-discard" {
			options.discard = startDraftFixtureDiscardObserver(t, requests, draftFixtureDB(t))
			defer options.discard.stop()
		}
		a.handler = draftFixtureHandler(a.handler, options)
		stopDrafts := startDraftPurger(user.NewDraftService().PurgeExpired, func() bool { return a.holder.Service() != nil })
		defer stopDrafts()
		t.Logf("DRAFT_FIXTURE: normal login only; memory session lifetime=%ds idle, GC interval=2s; failure=%q; prefix=%q; draft TTL remains production30m; owned purger active until fixture exit", options.idle, options.failure, options.prefix)
		if options.failure == "delay-save" {
			t.Log("DELAY: real save commits first; successful acknowledgement waits idle+3 seconds before emission; context cancellation aborts emission")
		}
		if options.failure == "reject-cookie" {
			t.Log("COOKIE: browser rejection simulation via nonmatching Domain on ONLY hgs_draft; native auth/CSRF cookies unchanged")
		}
		if options.failure == "drop-discard" {
			t.Log("DROP_DISCARD: exact native disposal connection closes without success; first affected live saved row is retained, explicitly aged in the owned DB, then observed through the unchanged one-minute purger; no wall-clock 30-minute pass is claimed")
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var once sync.Once
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return requests }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/fixture/stop" {
			w.Write([]byte("stopping owned fixture"))
			once.Do(func() { close(stop) })
			return
		}
		a.handler.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("CUTOVER_BROWSER_URL=http://%s%s/; sign in %s/user/login; alice/admin, writer/write, bob/read; passwords=password; guest=POST logout. Locales ?lang=en-US or ?lang=de-DE; PR %s/alice/project/pulls/1 comparison=%d; issues %s/alice/project/issues/1; organization %s/org/laboratory; shutdown http://%s/fixture/stop; root=%s", listener.Addr(), options.prefix, options.prefix, options.prefix, comparison.ID, options.prefix, options.prefix, listener.Addr(), a.root)
	lifetime := 60 * time.Minute
	if contributionFixture != "" {
		lifetime = 20 * time.Minute
	}
	if profileFixture {
		lifetime = 12 * time.Minute
		t.Logf("PROFILE_FIXTURE: production router, normal login; public /code/alice; private /code/org/laboratory (alice only); absent /code/bob; all three profiles at 390/1440; session idle=300s; auto stop=12m")
	}
	if avatarFixture != "" {
		lifetime = 8 * time.Minute
	}
	select {
	case <-stop:
	case <-time.After(lifetime):
	}
	cancelRequests()
	shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = server.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
