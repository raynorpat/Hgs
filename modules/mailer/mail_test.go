package mailer

import (
	"html/template"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/setting"
)

func TestExistingMailBranding(t *testing.T) {
	oldName, oldURL, oldService, oldMail, oldQueue := setting.AppName, setting.AppUrl, setting.Service, setting.MailService, mailQueue
	t.Cleanup(func() {
		setting.AppName, setting.AppUrl, setting.Service, setting.MailService, mailQueue = oldName, oldURL, oldService, oldMail, oldQueue
	})
	setting.AppUrl = "https://mail.example.invalid/code/"
	setting.MailService = &setting.Mailer{From: "service@example.invalid"}
	setting.Service.ActiveCodeLives, setting.Service.ResetPwdCodeLives = 180, 120
	mailQueue = make(chan *Message, 10) // fake sink: no SMTP worker
	u := &models.User{Id: 7, Name: "Reader <script>x</script>", LowerName: "reader", Email: "reader@example.invalid", Passwd: "secret", Rands: "salt"}
	owner := &models.User{Name: "owner"}
	repo := &models.Repository{Name: "project"}
	issue := &models.Issue{Name: "Subject <script>bad</script>", Index: 3}
	for _, name := range []string{"Hgs", "Operator <Lab>"} {
		setting.AppName = name
		m := macaron.New()
		m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []template.FuncMap{base.TemplateFuncs}}))
		m.Get("/", func(r macaron.Render) {
			for _, tc := range []struct {
				name, action string
				recipients   []string
				send         func()
			}{
				{"register", "user/activate?code=", []string{u.Email}, func() { SendRegisterMail(r, u) }},
				{"activate", "user/activate?code=", []string{u.Email}, func() { SendActiveMail(r, u) }},
				{"secondary", "user/activate_email?code=", []string{"secondary+tag@example.invalid"}, func() { SendActivateEmail(r, u, &models.EmailAddress{Email: "secondary+tag@example.invalid"}) }},
				{"reset", "user/reset_password?code=", []string{u.Email}, func() { SendResetPasswdMail(r, u) }},
				{"mention", "owner/project/issues/3", []string{"mentioned@example.invalid"}, func() {
					if err := SendIssueMentionMail(r, u, owner, repo, issue, []string{"mentioned@example.invalid"}); err != nil {
						t.Error(err)
					}
				}},
				{"collaborator", "owner/project", []string{u.Email}, func() {
					if err := SendCollaboratorMail(r, u, owner, repo); err != nil {
						t.Error(err)
					}
				}},
			} {
				tc.send()
				select {
				case msg := <-mailQueue:
					if !reflect.DeepEqual(msg.To, tc.recipients) {
						t.Errorf("%s recipients=%v", tc.name, msg.To)
					}
					for _, want := range []string{`src="https://mail.example.invalid/code/img/favicon-32.png"`, template.HTMLEscapeString(name), "https://mail.example.invalid/code/" + tc.action, `alt=""`, "GoGits"} {
						if !strings.Contains(msg.Body, want) {
							t.Errorf("%s missing %q", tc.name, want)
						}
					}
					if strings.Contains(msg.Body, "<script>") || strings.Contains(msg.Body, "View it on Gogs") || strings.Contains(msg.Body, "/code//img/") {
						t.Errorf("%s unsafe/obsolete mail: %s", tc.name, msg.Body)
					}
					if !strings.Contains(msg.Body, ">View ") && !strings.Contains(msg.Body, ">Verify ") && !strings.Contains(msg.Body, ">Reset ") {
						t.Errorf("%s lacks readable action text", tc.name)
					}
					if tc.name != "mention" && tc.name != "collaborator" && !strings.Contains(msg.Body, "<strong>3 hours</strong>") {
						t.Errorf("%s existing activation-code lifetime changed", tc.name)
					}
				case <-time.After(time.Second):
					t.Errorf("%s no delivery", tc.name)
				}
			}
			if err := SendIssueMentionMail(r, u, owner, repo, issue, nil); err != nil {
				t.Error(err)
			}
			select {
			case <-mailQueue:
				t.Error("empty mention recipients delivered")
			default:
			}
		})
		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
}

func TestExistingMailBrandingIssueWatchers(t *testing.T) {
	root := t.TempDir()
	oldDB, oldHas, oldLog, oldName, oldURL, oldSub, oldMail, oldQueue := models.DbCfg, models.HasEngine, setting.LogRootPath, setting.AppName, setting.AppUrl, setting.AppSubUrl, setting.MailService, mailQueue
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg, models.HasEngine, setting.LogRootPath, setting.AppName, setting.AppUrl, setting.AppSubUrl, setting.MailService, mailQueue = oldDB, oldHas, oldLog, oldName, oldURL, oldSub, oldMail, oldQueue
	})
	models.DbCfg.Type, models.DbCfg.Path, setting.LogRootPath = "sqlite3", filepath.Join(root, "mail.db"), root
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	db, err := xorm.NewEngine("sqlite3", models.DbCfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actor := &models.User{Name: "actor", Email: "actor@example.invalid", Passwd: "password", IsActive: true}
	watcher := &models.User{Name: "watcher", Email: "watcher@example.invalid", Passwd: "password", IsActive: true}
	for _, u := range []*models.User{actor, watcher} {
		if err := models.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	repo := &models.Repository{Name: "lab", LowerName: "lab", OwnerId: actor.Id, State: models.RepositoryReady}
	if _, err := db.Insert(repo); err != nil {
		t.Fatal(err)
	}
	mailQueue = make(chan *Message, 10)
	setting.AppName = "Operator <Lab>"
	setting.MailService = &setting.Mailer{From: "service@example.invalid"}
	issue := &models.Issue{Index: 4, Name: "Issue", Content: "hello <script>alert(1)</script> & text @watcher #12 0123456789abcdef0123456789abcdef01234567\nhttps://external.example.invalid/commit/abc1234#L9\nhttps://bad%zz.invalid/commit/abcdef0"}
	if tos, err := SendIssueNotifyMail(actor, actor, repo, issue); err != nil || len(tos) != 0 {
		t.Fatalf("empty watchers %v %v", tos, err)
	}
	for _, u := range []*models.User{actor, watcher} {
		if err := models.WatchRepo(u.Id, repo.Id, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, appURL, subURL                     string
		action, image, mention, issue, changeset string
	}{
		{"root", "https://mail.example.invalid/", "", "https://mail.example.invalid/actor/lab/issues/4", "https://mail.example.invalid/img/favicon-32.png", "https://mail.example.invalid/watcher", "https://mail.example.invalid/actor/lab/issues/12", "https://mail.example.invalid/actor/lab/commit/0123456789abcdef0123456789abcdef01234567"},
		{"subURL", "https://mail.example.invalid/code/", "/code", "https://mail.example.invalid/code/actor/lab/issues/4", "https://mail.example.invalid/code/img/favicon-32.png", "https://mail.example.invalid/code/watcher", "https://mail.example.invalid/code/actor/lab/issues/12", "https://mail.example.invalid/code/actor/lab/commit/0123456789abcdef0123456789abcdef01234567"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setting.AppUrl, setting.AppSubUrl = tc.appURL, tc.subURL
			tos, err := SendIssueNotifyMail(actor, actor, repo, issue)
			if err != nil || !reflect.DeepEqual(tos, []string{watcher.Email}) {
				t.Fatalf("watchers %v %v", tos, err)
			}
			select {
			case msg := <-mailQueue:
				if !reflect.DeepEqual(msg.To, []string{watcher.Email}) || msg.From != actor.Email || msg.Subject != "[lab] Issue(#4)" {
					t.Fatalf("delivery changed: %+v", msg)
				}
				for _, want := range []string{"Operator &lt;Lab&gt;", tc.image, `href="` + tc.action + `"`, `href="` + tc.mention + `"`, `href="` + tc.issue + `"`, `href="` + tc.changeset + `"`, `href="https://external.example.invalid/commit/abc1234#L9"`, ">abcdef0</a>", "View the issue on", "&lt;script&gt;", "&amp; text", "GoGits"} {
					if !strings.Contains(msg.Body, want) {
						t.Errorf("issue notify missing %q", want)
					}
				}
				for _, match := range regexp.MustCompile(`href="([^"]+)"`).FindAllStringSubmatch(msg.Body, -1) {
					u, err := url.Parse(match[1])
					if err != nil || !u.IsAbs() || u.Host == "" || strings.Contains(u.Path, "/code/code/") {
						t.Errorf("unusable or duplicated-prefix email href %q", match[1])
					}
				}
				if strings.Contains(msg.Body, "<script>") {
					t.Fatal("unescaped issue content")
				}
				if strings.Contains(msg.Body, `href="https://bad%zz.invalid`) {
					t.Error("malformed content reference retained a broken link")
				}
			case <-time.After(time.Second):
				t.Fatal("no watcher mail")
			}
			// This amendment is email-scoped: the existing web renderer keeps its
			// route-relative links and still uses the configured sub-URL for mentions.
			web := string(base.RenderSpecialLink([]byte("@watcher #12 0123456789abcdef0123456789abcdef01234567"), "actor/lab"))
			for _, want := range []string{`href="` + tc.subURL + `/watcher"`, `href="actor/lab/issues/12"`, `href="actor/lab/commit/0123456789abcdef0123456789abcdef01234567"`} {
				if !strings.Contains(web, want) {
					t.Errorf("web reference behavior changed: missing %q", want)
				}
			}
		})
	}
	select {
	case <-mailQueue:
		t.Fatal("extra notification")
	default:
	}
}
