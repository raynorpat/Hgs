//go:build integration

package integration

import (
	"bufio"
	"context"
	"fmt"
	"github.com/Unknwon/macaron"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/contributions"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/csrf"
	"html"
	"net"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestContributionQueryCanonical(t *testing.T) {
	f := accountApp(t, true)
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	r := &models.Repository{OwnerId: f.member.Id, Name: "calendar", LowerName: "calendar", State: models.RepositoryReady}
	if _, err = db.Insert(r); err != nil {
		t.Fatal(err)
	}
	i := &models.Issue{RepoId: r.Id, Index: 1, PosterId: f.member.Id, Name: "Indexed opening", Created: now.Add(-time.Hour)}
	if _, err = db.NoAutoTime().Insert(i); err != nil {
		t.Fatal(err)
	}
	if err = models.IndexContributionObject(context.Background(), "issue", i.Id); err != nil {
		t.Fatal(err)
	}
	s := contributions.NewService(nil, nil, func(ctx context.Context, viewer, repo int64) (bool, error) {
		a, e := models.RepositoryAccessFor(viewer, repo)
		return a.Read, e
	}, time.Now)
	p, err := s.Read(context.Background(), 0, contributions.Scope{OwnerID: f.member.Id}, contributions.Query{})
	if err != nil || p.Totals.Issues != 1 || p.State != "updating" {
		t.Fatalf("canonical cold query: %+v %v", p, err)
	}
	if _, err = db.Id(i.Id).Delete(new(models.Issue)); err != nil {
		t.Fatal(err)
	}
	p, err = s.Read(context.Background(), 0, contributions.Scope{OwnerID: f.member.Id}, contributions.Query{})
	if err != nil || p.Totals.Issues != 0 {
		t.Fatalf("deleted issue remained visible before index cleanup: %+v %v", p, err)
	}
}

func TestContributionEmailProof(t *testing.T) {
	f := accountApp(t, true)
	old := setting.MailService
	t.Cleanup(func() { setting.MailService = old })
	setting.MailService = nil
	f.m.Post("/user/settings/email/verify", middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true}), func(c *macaron.Context, x csrf.CSRF) { csrf.Validate(c, x) }, user.SettingsEmailVerify)
	f.m.Get("/user/activate_email", user.ActivateEmail)
	page := f.request(t, "GET", "/user/settings/email", f.member.Name, nil, false)
	if strings.Contains(page.Body.String(), `action="/code/user/settings/email/verify"`) {
		t.Fatal("disabled mail offers a verification action")
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, false); w.Code != 400 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, true); w.Code != 503 {
		t.Fatalf("disabled mail: %d", w.Code)
	}
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	sink, restore := mailer.CaptureIntegrationMail()
	defer restore()
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"999999"}}, true); w.Code != 404 {
		t.Fatalf("foreign selection: %d", w.Code)
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, true); w.Code != 302 {
		t.Fatalf("verification send: %d %s", w.Code, w.Body)
	}
	message := <-sink
	if len(message.To) != 1 || message.To[0] != f.member.Email || strings.Contains(message.Info, f.member.Email) {
		t.Fatalf("mail recipient/privacy: %+v", message)
	}
	// Deliver only to an owned loopback SMTP sink; no external account is used.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	setting.MailService.Host = listener.Addr().String()
	smtpResult := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			smtpResult <- "accept failed"
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "220 owned-fixture ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		var body strings.Builder
		data := false
		for scanner.Scan() {
			line := scanner.Text()
			if data {
				if line == "." {
					data = false
					fmt.Fprint(conn, "250 accepted\r\n")
				} else {
					body.WriteString(line)
					body.WriteByte('\n')
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprint(conn, "250 owned-fixture\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				fmt.Fprint(conn, "250 accepted\r\n")
			case line == "DATA":
				data = true
				fmt.Fprint(conn, "354 send message\r\n")
			case line == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				smtpResult <- body.String()
				return
			default:
				fmt.Fprint(conn, "500 unsupported\r\n")
			}
		}
		smtpResult <- "incomplete SMTP exchange"
	}()
	if n, err := mailer.Send(message); err != nil || n != 1 {
		t.Fatalf("owned fake SMTP delivery: n=%d err=%v", n, err)
	}
	if received := <-smtpResult; !strings.Contains(received, "user/activate_email?code=") {
		t.Fatal("fake SMTP did not receive confirmation")
	}
	start := strings.Index(message.Body, "user/activate_email?code=")
	if start < 0 {
		t.Fatal("confirmation link absent")
	}
	fragment := message.Body[start:]
	end := strings.Index(fragment, "\"")
	if end < 0 {
		t.Fatal("link missing closing quote")
	}
	link, err := url.Parse("/" + fragment[:end])
	if err != nil {
		t.Fatal(err)
	}
	if link.Query().Get("email") != "" {
		t.Fatal("new confirmation URL leaks lookup address")
	}
	if w := f.request(t, "POST", "/user/settings/email/verify", f.member.Name, url.Values{"id": {"0"}}, true); w.Code != 429 {
		t.Fatalf("resend limit: %d", w.Code)
	}
	if w := f.request(t, "GET", link.String(), "", nil, false); w.Code != 302 {
		t.Fatalf("confirmation: %d", w.Code)
	}
	if id, err := models.VerifiedContributionAuthor(context.Background(), f.member.Email); err != nil || id != f.member.Id {
		t.Fatalf("proof: %d %v", id, err)
	}
	select {
	case <-sink:
		t.Fatal("unsolicited duplicate mail")
	default:
	}
}

func TestContributionSecondaryThrottle(t *testing.T) {
	f := accountApp(t, true)
	oldMail, oldConfirm := setting.MailService, setting.Service.RegisterEmailConfirm
	t.Cleanup(func() { setting.MailService, setting.Service.RegisterEmailConfirm = oldMail, oldConfirm })
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	setting.Service.RegisterEmailConfirm = true
	sink, restore := mailer.CaptureIntegrationMail()
	defer restore()
	f.request(t, "GET", "/user/settings/email", f.member.Name, nil, false)
	for i, email := range []string{"first-secondary@example.invalid", "second-secondary@example.invalid"} {
		w := f.request(t, "POST", "/user/settings/email", f.member.Name, url.Values{"email": {email}}, true)
		want := 302
		if i == 1 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("add%d: %d %s", i, w.Code, w.Body)
		}
	}
	select {
	case <-sink:
	case <-time.After(time.Second):
		t.Fatal("first confirmation not queued")
	}
	emails, err := models.GetEmailAddresses(f.member.Id)
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range emails {
		if email.Email == "second-secondary@example.invalid" {
			t.Fatal("throttled request persisted an inactive address without activation mail")
		}
	}
	select {
	case <-sink:
		t.Fatal("throttled request sent mail")
	default:
	}
}

// Ordinary GET filters must reach the real contribution service, not legacy feeds.
func TestProfileContributionHTTP(t *testing.T) {
	f := accountApp(t, true)
	f.m.Get("/:username", user.Profile)
	w := f.request(t, "GET", "/member?tab=activity&year=2024&day=2024-02-29&kind=issue", "", nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data-contribution-calendar") || !strings.Contains(w.Body.String(), `value="2024-02-29"`) {
		t.Fatalf("missing server-rendered filtered calendar: HTTP %d", w.Code)
	}
	for _, query := range []string{"year=nope", "year=0", "day=2024-02-30&year=2024", "kind=push", "tab=people"} {
		w = f.request(t, "GET", "/member?"+query, "", nil, false)
		if w.Code != 400 {
			t.Errorf("invalid %s: HTTP %d", query, w.Code)
		}
	}
}

func TestProfileContributionStates(t *testing.T) {
	f := accountApp(t, true)
	w := f.request(t, "GET", "/profile/member?tab=activity", "", nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data-contribution-state") {
		t.Fatalf("missing honest contribution state: HTTP %d", w.Code)
	}
}

func TestOrganizationPeopleLinks(t *testing.T) {
	f := accountApp(t, true)
	team, err := models.CreateOrganization(&models.User{Name: "laboratory", Email: "org@example.invalid", Type: models.ORGANIZATION, IsActive: true}, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if err = team.AddMember(f.member.Id); err != nil {
		t.Fatal(err)
	}
	if err = models.ChangeOrgUserStatus(team.Id, f.member.Id, true); err != nil {
		t.Fatal(err)
	}
	w := f.request(t, "GET", "/org/laboratory?tab=people", "", nil, false)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "data-profile-people") || !strings.Contains(body, `href="/code/member"`) || !strings.Contains(body, `href="/code/org/laboratory?tab=activity"`) {
		t.Fatalf("visible People links absent: HTTP %d", w.Code)
	}
	if strings.Contains(body, `href="/code/administrator"`) || strings.Contains(body, "/members/action/") || strings.Contains(body, "/invitations/new") {
		t.Error("public People exposes private membership or management")
	}
}

type uiContributionProvider struct{ reader contributions.Reader }

func (p uiContributionProvider) Contributions() contributions.Reader { return p.reader }

func contributionReadAccess(ctx context.Context, viewer, repo int64) (bool, error) {
	a, e := models.RepositoryAccessFor(viewer, repo)
	return a.Read, e
}

// Real canonical objects test counts, representative caps, pagination, escaping and live revocation.
func TestProfileContributionHTTPData(t *testing.T) {
	oldKey := setting.SecretKey
	setting.SecretKey = "task23-disposable-query-key"
	t.Cleanup(func() { setting.SecretKey = oldKey })
	f := accountApp(t, true)
	f.m.Get("/:username", user.Profile)
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	day := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	var first *models.Repository
	for n := 0; n < 28; n++ {
		r := &models.Repository{OwnerId: f.member.Id, Name: fmt.Sprintf("calendar-%02d", n), LowerName: fmt.Sprintf("calendar-%02d", n), State: models.RepositoryReady}
		if _, err = db.Insert(r); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			first = r
		}
		count := 1
		if n == 0 {
			count = 30
		}
		for j := 1; j <= count; j++ {
			i := &models.Issue{RepoId: r.Id, Index: int64(j), PosterId: f.member.Id, Name: fmt.Sprintf("Issue <script> %02d-%02d", n, j), Created: day}
			if _, err = db.NoAutoTime().Insert(i); err != nil {
				t.Fatal(err)
			}
			if err = models.IndexContributionObject(context.Background(), "issue", i.Id); err != nil {
				t.Fatal(err)
			}
		}
	}
	private := &models.Repository{OwnerId: f.member.Id, Name: "secret-count", LowerName: "secret-count", IsPrivate: true, State: models.RepositoryReady}
	if _, err = db.Insert(private); err != nil {
		t.Fatal(err)
	}
	hidden := &models.Issue{RepoId: private.Id, Index: 1, PosterId: f.member.Id, Name: "Never public title", Created: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if _, err = db.NoAutoTime().Insert(hidden); err != nil {
		t.Fatal(err)
	}
	if err = models.IndexContributionObject(context.Background(), "issue", hidden.Id); err != nil {
		t.Fatal(err)
	}
	w := f.request(t, "GET", "/member?tab=activity&year=2024&kind=issue", "", nil, false)
	body := w.Body.String()
	for _, want := range []string{"57 contributions", "Showing 25 of 30", "Issue &lt;script&gt;", `href="/code/member/calendar-00/issues/30"`, `href="/code/member/calendar-00/issues"`, `2024-02-29: 57 contributions`} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("missing %q HTTP %d", want, w.Code)
		}
	}
	if strings.Contains(body, "secret-count") || strings.Contains(body, "Never public") || strings.Contains(body, `value="2020"`) {
		t.Error("private names/count years leaked")
	}
	matches := regexp.MustCompile(`class="hg-button hg-profile-more" href="([^"]+)"`).FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatal("missing ordinary pagination link")
	}
	next := strings.TrimPrefix(html.UnescapeString(matches[1]), "/code")
	next = strings.Split(next, "#")[0]
	w = f.request(t, "GET", next, "", nil, false)
	if w.Code != 200 || strings.Count(w.Body.String(), `class="hg-profile-event"`) != 3 || !strings.Contains(w.Body.String(), "57 contributions") {
		t.Fatalf("whole-group pagination lost groups/count: HTTP %d", w.Code)
	}
	w = f.request(t, "GET", "/member?tab=activity&year=2024&day=2024-02-28&kind=issue", "", nil, false)
	if !strings.Contains(w.Body.String(), "0 contributions") || strings.Contains(w.Body.String(), "Issue &lt;") {
		t.Error("day selection not applied")
	}
	w = f.request(t, "GET", "/member?tab=activity&year=2024&kind=review", "", nil, false)
	if !strings.Contains(w.Body.String(), "0 contributions") || strings.Contains(w.Body.String(), "Issue &lt;") {
		t.Error("kind selection not applied")
	}
	if _, err = db.Id(first.Id).Cols("is_private").Update(&models.Repository{IsPrivate: true}); err != nil {
		t.Fatal(err)
	}
	w = f.request(t, "GET", next, "", nil, false)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Restart activity") || strings.Contains(w.Body.String(), "calendar-00") {
		t.Errorf("stale cursor leaked revoked results: HTTP %d", w.Code)
	}
	w = f.request(t, "GET", "/member?tab=activity&year=2024&kind=issue", "", nil, false)
	if !strings.Contains(w.Body.String(), "27 contributions") || strings.Contains(w.Body.String(), "calendar-00") {
		t.Error("same-viewer revocation did not remove counts and links")
	}
}
