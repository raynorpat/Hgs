//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"github.com/Unknwon/macaron"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
	"github.com/gogits/gogs/routers"
	"github.com/gogits/gogs/routers/admin"
	"github.com/gogits/gogs/routers/org"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/binding"
	"github.com/macaron-contrib/cache"
	"github.com/macaron-contrib/captcha"
	"github.com/macaron-contrib/csrf"
	"github.com/macaron-contrib/i18n"
	"github.com/macaron-contrib/session"
	"golang.org/x/crypto/ssh"
	"html"
	htmltemplate "html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type accountFixture struct {
	store       cache.Cache
	root        string
	initialized int

	m             *macaron.Macaron
	admin, member *models.User
	cookies       map[string]*http.Cookie
	token         string
}

func accountApp(t *testing.T, installed bool) *accountFixture {
	t.Helper()
	root := t.TempDir()
	oldDB, oldHas, oldLog, oldRoot, oldSub, oldLock, oldService, oldAvatar := models.DbCfg, models.HasEngine, setting.LogRootPath, setting.RepoRootPath, setting.AppSubUrl, setting.InstallLock, setting.Service, setting.DisableGravatar
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg, models.HasEngine, setting.LogRootPath, setting.RepoRootPath, setting.AppSubUrl, setting.InstallLock, setting.Service, setting.DisableGravatar = oldDB, oldHas, oldLog, oldRoot, oldSub, oldLock, oldService, oldAvatar
	})
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.Join(root, "accounts.db")
	setting.LogRootPath = root
	setting.RepoRootPath = filepath.Join(root, "repos")
	setting.AppSubUrl = "/code"
	setting.InstallLock = installed
	setting.DisableGravatar = true
	setting.Service.EnableNotifyMail = false
	setting.Service.RegisterEmailConfirm = false
	setting.Service.DisableRegistration = false
	f := &accountFixture{root: root, cookies: map[string]*http.Cookie{}}
	if installed {
		if err := models.NewEngine(); err != nil {
			t.Fatal(err)
		}
		models.HasEngine = true
		f.admin = &models.User{Name: "administrator", Email: "admin@example.invalid", Passwd: "password", IsActive: true, IsAdmin: true}
		f.member = &models.User{Name: "member", Email: "member@example.invalid", Passwd: "password", IsActive: true}
		for _, u := range []*models.User{f.admin, f.member} {
			if err := models.CreateUser(u); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		models.HasEngine = false
	}
	m := macaron.New()
	m.SetURLPrefix("/code")
	m.Use(macaron.Renderer(macaron.RenderOptions{Directory: "../../templates", Funcs: []htmltemplate.FuncMap{base.TemplateFuncs}}))
	m.Use(middleware.Localization(i18n.Options{Directory: "../../conf/locale", Langs: []string{"en-US", "de-DE"}, Names: []string{"English", "Deutsch"}}))
	m.Use(cache.Cacher())
	m.Use(captcha.Captchaer(captcha.Options{SubURL: "/code"}))
	m.Use(session.Sessioner(session.Options{Provider: "memory", CookieName: "account_fixture", CookiePath: "/code"}))
	m.Use(csrf.Csrfer(csrf.Options{Secret: "isolated-account-fixture", SetCookie: true, CookiePath: "/code"}))
	m.Use(middleware.Contexter())
	m.Use(func(ctx *middleware.Context, store cache.Cache) {
		f.store = store
		ctx.PrepareHTML = user.PrepareDiscovery
		ctx.PreparePage = ui.PrepareAccountPage
	})
	protected := func(c *macaron.Context, x csrf.CSRF) { csrf.Validate(c, x) }
	signed := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	admins := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true, AdminRequire: true})
	m.Get("/user/sign_up", user.SignUp)
	m.Post("/user/sign_up", protected, binding.BindIgnErr(auth.RegisterForm{}), user.SignUpPost)
	m.Get("/user/login", user.SignIn)
	m.Post("/user/login", protected, binding.BindIgnErr(auth.SignInForm{}), user.SignInPost)
	m.Get("/user/forget_password", user.ForgotPasswd)
	m.Post("/user/forget_password", protected, user.ForgotPasswdPost)
	m.Get("/user/reset_password", user.ResetPasswd)
	m.Post("/user/reset_password", protected, user.ResetPasswdPost)
	m.Get("/user/activate", user.Activate)
	m.Post("/user/activate", protected, user.Activate)
	m.Post("/user/settings/avatar", signed, binding.BindIgnErr(auth.UploadAvatarForm{}), user.SettingsAvatar)
	m.Get("/user/settings", signed, user.Settings)
	m.Post("/user/settings", signed, binding.BindIgnErr(auth.UpdateProfileForm{}), user.SettingsPost)
	m.Get("/fixture/social/:id", func(ctx *middleware.Context) {
		ctx.Session.Set("socialId", int64(ctx.ParamsInt64(":id")))
		ctx.Session.Set("socialName", "Fixture Provider")
		ctx.Session.Set("socialEmail", "provider@example.invalid")
		ctx.Redirect("/code/user/sign_up")
	})
	m.Get("/profile/:username", user.Profile)
	m.Get("/org/:org", middleware.OrgAssignment(false), org.Home)
	m.Get("/org/create", signed, org.Create)
	m.Post("/org/create", signed, binding.BindIgnErr(auth.CreateOrgForm{}), org.CreatePost)
	m.Group("/org/:org", func() {
		m.Get("/members", org.Members)
		m.Post("/members/action/:action", org.MembersAction)
		m.Get("/teams", org.Teams)
		m.Get("/teams/:team", org.TeamMembers)
		m.Get("/teams/:team/repositories", org.TeamRepositories)
		m.Post("/teams/:team/action/:action", org.TeamsAction)
		m.Post("/teams/:team/action/repo/:action", org.TeamsRepoAction)
	}, signed, middleware.OrgAssignment(true, true))
	m.Group("/org/:org", func() {
		m.Get("/teams/:team/edit", org.EditTeam)
		m.Post("/teams/:team/edit", binding.BindIgnErr(auth.CreateTeamForm{}), org.EditTeamPost)
		m.Get("/teams/new", org.NewTeam)
		m.Post("/teams/new", binding.BindIgnErr(auth.CreateTeamForm{}), org.NewTeamPost)
		m.Get("/settings", org.Settings)
		m.Post("/settings", binding.BindIgnErr(auth.UpdateOrgSettingForm{}), org.SettingsPost)
	}, signed, middleware.OrgAssignment(true, true, true))
	m.Get("/user/settings/ssh", signed, user.SettingsSSHKeys)
	m.Post("/user/settings/ssh", signed, binding.BindIgnErr(auth.AddSSHKeyForm{}), user.SettingsSSHKeysPost)
	m.Get("/user/settings/social", signed, user.SettingsSocial)
	m.Post("/user/settings/social", signed, user.SettingsSocial)
	m.Get("/user/settings/applications", signed, user.SettingsApplications)
	m.Post("/user/settings/applications", signed, binding.BindIgnErr(auth.NewAccessTokenForm{}), user.SettingsApplicationsPost)
	m.Get("/admin", admins, admin.Dashboard)
	m.Post("/admin", admins, admin.Dashboard)
	m.Get("/admin/users", admins, admin.Users)
	m.Get("/admin/config", admins, admin.Config)
	m.Get("/admin/orgs", admins, admin.Organizations)
	m.Get("/admin/repos", admins, admin.Repositories)
	m.Get("/admin/monitor", admins, admin.Monitor)
	m.Get("/admin/notices", admins, admin.Notices)
	m.Post("/admin/users/:userid/delete", admins, admin.DeleteUser)
	m.Get("/admin/users/:userid", admins, admin.EditUser)
	m.Post("/admin/users/:userid", admins, binding.BindIgnErr(auth.AdminEditUserForm{}), admin.EditUserPost)
	m.Get("/admin/auths", admins, admin.Authentications)
	m.Get("/admin/auths/new", admins, admin.NewAuthSource)
	m.Post("/admin/auths/new", admins, binding.BindIgnErr(auth.AuthenticationForm{}), admin.NewAuthSourcePost)
	m.Post("/admin/auths/:authid/delete", admins, admin.DeleteAuthSource)
	m.Get("/admin/auths/:authid", admins, admin.EditAuthSource)
	m.Post("/admin/auths/:authid", admins, binding.BindIgnErr(auth.AuthenticationForm{}), admin.EditAuthSourcePost)
	m.Get("/user/settings/password", signed, user.SettingsPassword)
	m.Get("/user/settings/email", signed, user.SettingsEmails)
	m.Post("/user/settings/email", signed, binding.BindIgnErr(auth.AddEmailForm{}), user.SettingsEmailPost)
	m.Get("/user/settings/delete", signed, user.SettingsDelete)
	m.Get("/admin/users/new", admins, admin.NewUser)
	m.Post("/admin/users/new", admins, binding.BindIgnErr(auth.RegisterForm{}), admin.NewUserPost)
	m.Get("/install", routers.InstallInit, routers.Install)
	m.Post("/install", routers.InstallInit, protected, binding.BindIgnErr(auth.InstallForm{}), routers.InstallPost)
	m.Map(routers.InitializeHosting(func() error { f.initialized++; return nil }))
	for _, status := range []int{401, 403, 404, 500} {
		code := status
		m.Get(fmt.Sprintf("/early/%d", code), func(ctx *middleware.Context) { ctx.Handle(code, "fixture", nil) })
	}
	f.m = m
	return f
}
func (f *accountFixture) request(t *testing.T, method, path, who string, values url.Values, valid bool) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	if method == "POST" && valid {
		values.Set("_csrf", f.token)
	}
	r := httptest.NewRequest(method, "/code"+path, strings.NewReader(values.Encode()))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if who != "" {
		r.SetBasicAuth(who, "password")
	}
	for _, c := range f.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		f.cookies[c.Name] = c
	}
	match := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(match) == 2 {
		f.token = html.UnescapeString(match[1])
	}
	return w
}
func sharedAccountPage(t *testing.T, w *httptest.ResponseRecorder, reduced bool) {
	t.Helper()
	s := w.Body.String()
	if w.Code != 200 || strings.Count(s, "<!doctype html>") != 1 || !strings.Contains(s, `data-hg-shell`) {
		t.Errorf("shared page missing: status %d body %.220s", w.Code, s)
	}
	if reduced && strings.Contains(s, `class="hg-rail"`) {
		t.Error("reduced page loads account rail")
	}
}
func TestSharedUIAdminAuthorization(t *testing.T) {
	f := accountApp(t, true)
	for _, p := range []string{"/admin", "/admin/users", "/admin/users/new", fmt.Sprintf("/admin/users/%d", f.member.Id), "/admin/config", "/admin/orgs", "/admin/repos", "/admin/monitor", "/admin/notices", "/admin/auths", "/admin/auths/new"} {
		w := f.request(t, "GET", p, "member", nil, false)
		if w.Code != 403 {
			t.Errorf("nonadmin %s = %d", p, w.Code)
		}
		sharedAccountPage(t, f.request(t, "GET", p, "administrator", nil, false), false)
	}
}
func TestSharedUIAdminMutationMethods(t *testing.T) {
	f := accountApp(t, true)
	inactive := &models.User{Name: "inactive", Email: "inactive@example.invalid", Passwd: "password"}
	if err := models.CreateUser(inactive); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/admin?op=2", "administrator", nil, false)
	if _, err := models.GetUserById(inactive.Id); err != nil {
		t.Fatal("GET maintenance deleted inactive account")
	}
	w := f.request(t, "POST", "/admin", "administrator", url.Values{"op": {"2"}}, false)
	if w.Code != 400 {
		t.Fatalf("CSRF denial = %d", w.Code)
	}
	if _, err := models.GetUserById(inactive.Id); err != nil {
		t.Fatal("denied POST mutated")
	}
	f.request(t, "GET", "/admin", "administrator", nil, false)
	w = f.request(t, "POST", "/admin", "administrator", url.Values{"op": {"2"}}, true)
	if w.Code != 302 {
		t.Fatalf("maintenance %d", w.Code)
	}
	if _, err := models.GetUserById(inactive.Id); err != models.ErrUserNotExist {
		t.Fatal("maintenance not applied", err)
	}
}
func TestSharedUIAccountsAndOrganizations(t *testing.T) {
	f := accountApp(t, true)
	for _, p := range []string{"/user/settings", "/user/settings/ssh", "/user/settings/social", "/user/settings/applications"} {
		sharedAccountPage(t, f.request(t, "GET", p, "member", nil, false), false)
	}
	token := &models.AccessToken{Uid: f.member.Id, Name: "owned token"}
	if err := models.NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", fmt.Sprintf("/user/settings/applications?remove=%d", token.Id), "member", nil, false)
	if _, err := models.GetAccessTokenBySha(token.Sha1); err != nil {
		t.Fatal("GET removed access token")
	}
}
func TestSharedUISignup(t *testing.T) {
	f := accountApp(t, true)
	sharedAccountPage(t, f.request(t, "GET", "/user/sign_up", "", nil, false), true)
	secret := "never-reflect-this-password"
	w := f.request(t, "POST", "/user/sign_up?from=home", "", url.Values{"uname": {"safe@example.invalid"}, "password": {secret}}, true)
	sharedAccountPage(t, w, true)
	if strings.Contains(w.Body.String(), secret) {
		t.Error("signup reflects password")
	}
	if !strings.Contains(w.Body.String(), `value="safe@example.invalid"`) {
		t.Error("safe email lost")
	}
	setting.Service.DisableRegistration = true
	w = f.request(t, "GET", "/user/sign_up", "", nil, false)
	sharedAccountPage(t, w, true)
	if strings.Contains(w.Body.String(), `name="password"`) {
		t.Error("disabled registration renders form")
	}
}
func TestSharedUIInstaller(t *testing.T) {
	f := accountApp(t, false)
	models.DbCfg.Passwd = "configured-database-secret"
	w := f.request(t, "GET", "/install", "", nil, false)
	sharedAccountPage(t, w, true)
	if strings.Contains(w.Body.String(), models.DbCfg.Passwd) {
		t.Error("installer reflects configured database secret")
	}
	w = f.request(t, "POST", "/install", "", url.Values{"db_type": {"PostgreSQL"}, "db_host": {"safe-host"}, "db_passwd": {"submitted-db-secret"}, "admin_passwd": {"submitted-admin-secret"}, "smtp_passwd": {"submitted-mail-secret"}}, true)
	sharedAccountPage(t, w, true)
	for _, s := range []string{"submitted-db-secret", "submitted-admin-secret", "submitted-mail-secret"} {
		if strings.Contains(w.Body.String(), s) {
			t.Error("installer reflects", s)
		}
	}
	setting.InstallLock = true
	w = f.request(t, "GET", "/install", "", nil, false)
	if w.Code != 404 {
		t.Error("locked install allowed", w.Code)
	}
}
func TestSharedUIEarlyStatus(t *testing.T) {
	f := accountApp(t, false)
	for _, code := range []int{401, 403, 404, 500} {
		w := f.request(t, "GET", fmt.Sprintf("/early/%d", code), "", nil, false)
		if w.Code != code || !strings.Contains(w.Body.String(), `/code/css/app.css`) || strings.Contains(w.Body.String(), `class="hg-rail"`) {
			t.Errorf("early %d: %d %.200s", code, w.Code, w.Body.String())
		}
	}
}

func (f *accountFixture) signupValues(t *testing.T, name, email string) url.Values {
	t.Helper()
	f.request(t, "GET", "/user/sign_up", "", nil, false)
	if err := f.store.Put("captcha_fixture", string([]byte{1, 2, 3, 4, 5, 6}), 600); err != nil {
		t.Fatal(err)
	}
	return url.Values{"uname": {name}, "email": {email}, "password": {"secret-password"}, "retype": {"secret-password"}, "captcha_id": {"fixture"}, "captcha": {"123456"}}
}
func TestSharedUISignupValidationAndMail(t *testing.T) {
	f := accountApp(t, true)
	capture, restore := mailer.CaptureIntegrationMail()
	defer restore()
	oldMail, oldURL := setting.MailService, setting.AppUrl
	t.Cleanup(func() { setting.MailService, setting.AppUrl = oldMail, oldURL })
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	setting.AppUrl = "http://fixture.invalid/code/"
	setting.Service.ActiveCodeLives = 180
	values := f.signupValues(t, "member", "unique@example.invalid")
	w := f.request(t, "POST", "/user/sign_up", "", values, true)
	sharedAccountPage(t, w, true)
	if strings.Contains(w.Body.String(), "secret-password") || strings.Contains(w.Body.String(), `value="123456"`) {
		t.Fatal("secret reflected")
	}
	values = f.signupValues(t, "new-user", "new@example.invalid")
	values.Set("captcha", "wrong")
	w = f.request(t, "POST", "/user/sign_up", "", values, true)
	if _, err := models.GetUserByName("new-user"); err != models.ErrUserNotExist {
		t.Fatal("invalid CAPTCHA created user")
	}
	setting.Service.RegisterEmailConfirm = true
	values = f.signupValues(t, "new-user", "new@example.invalid")
	w = f.request(t, "POST", "/user/sign_up", "", values, true)
	sharedAccountPage(t, w, true)
	select {
	case msg := <-capture:
		if !strings.Contains(msg.Body, "/user/activate?code=") {
			t.Fatal("activation mail lacks real token")
		}
	case <-time.After(time.Second):
		t.Fatal("no activation mail captured")
	}
	u, err := models.GetUserByName("new-user")
	if err != nil || u.IsActive {
		t.Fatal("registration activation policy", err)
	}
	code := mailer.CreateUserActiveCode(u, nil)
	w = f.request(t, "GET", "/user/activate?code="+code, "", nil, false)
	if w.Code != 302 {
		t.Fatal("activation link failed", w.Code)
	}
	u, _ = models.GetUserById(u.Id)
	if !u.IsActive {
		t.Fatal("activation did not persist")
	}
	// Reset keeps the token in the POST action, never the language switcher.
	f.cookies = map[string]*http.Cookie{}
	f.request(t, "GET", "/user/forget_password", "", nil, false)
	w = f.request(t, "POST", "/user/forget_password", "", url.Values{"email": {f.member.Email}}, true)
	sharedAccountPage(t, w, true)
	select {
	case msg := <-capture:
		if !strings.Contains(msg.Body, "/user/reset_password?code=") {
			t.Fatal("reset mail lacks token")
		}
	case <-time.After(time.Second):
		t.Fatal("no reset mail")
	}
	reset := mailer.CreateUserActiveCode(f.member, nil)
	w = f.request(t, "GET", "/user/reset_password?code="+reset, "", nil, false)
	sharedAccountPage(t, w, true)
	if strings.Contains(w.Body.String(), "code="+reset+"&amp;lang") {
		t.Fatal("token in language link")
	}
	w = f.request(t, "POST", "/user/reset_password?code="+reset, "", url.Values{"password": {"changed-password"}}, true)
	if w.Code != 302 {
		t.Fatal("reset", w.Code)
	}
	updated, _ := models.GetUserById(f.member.Id)
	if !updated.ValidtePassword("changed-password") {
		t.Fatal("password unchanged")
	}
	// Provider registration binds an existing callback record and stays active.
	f.cookies = map[string]*http.Cookie{}
	social := &models.Oauth2{Uid: -1, Type: int(models.GITHUB), Identity: "fixture-provider", Token: "provider-secret"}
	if err := models.AddOauth2(social); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", fmt.Sprintf("/fixture/social/%d", social.Id), "", nil, false)
	values = f.signupValues(t, "social-user", "social@example.invalid")
	w = f.request(t, "POST", "/user/sign_up", "", values, true)
	if w.Code != 302 {
		t.Fatal("social signup", w.Code, w.Body.String())
	}
	bound, err := models.GetOauth2ById(social.Id)
	if err != nil || bound.Uid < 1 {
		t.Fatal("provider not bound", err)
	}
	u, _ = models.GetUserById(bound.Uid)
	if !u.IsActive {
		t.Fatal("social registration changed activation policy")
	}
}
func TestSharedUIAccountsForeignDeletion(t *testing.T) {
	f := accountApp(t, true)
	token := &models.AccessToken{Uid: f.admin.Id, Name: "foreign-token"}
	if err := models.NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	social := &models.Oauth2{Uid: f.admin.Id, Type: int(models.GITHUB), Identity: "foreign-provider", Token: "private-provider-token"}
	if err := models.AddOauth2(social); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		id   int64
	}{{"applications", token.Id}, {"social", social.Id}} {
		f.request(t, "GET", "/user/settings/"+tc.path, "member", nil, false)
		w := f.request(t, "POST", "/user/settings/"+tc.path, "member", url.Values{"remove": {fmt.Sprint(tc.id)}}, true)
		if w.Code != 404 {
			t.Errorf("foreign %s deletion %d", tc.path, w.Code)
		}
	}
	if _, err := models.GetAccessTokenBySha(token.Sha1); err != nil {
		t.Fatal("foreign token deleted")
	}
	if _, err := models.GetOauth2ById(social.Id); err != nil {
		t.Fatal("foreign OAuth deleted")
	}
	for _, tc := range []struct {
		path string
		id   int64
	}{{"applications", token.Id}, {"social", social.Id}} {
		f.request(t, "GET", "/user/settings/"+tc.path, "administrator", nil, false)
		w := f.request(t, "POST", "/user/settings/"+tc.path, "administrator", url.Values{"remove": {fmt.Sprint(tc.id)}}, true)
		if w.Code != 302 {
			t.Errorf("owned %s deletion %d", tc.path, w.Code)
		}
	}
	if _, err := models.GetAccessTokenBySha(token.Sha1); err != models.ErrAccessTokenNotExist {
		t.Fatal("token not deleted", err)
	}
	if _, err := models.GetOauth2ById(social.Id); err != models.ErrOauth2RecordNotExist {
		t.Fatal("OAuth not deleted", err)
	}
}
func TestSharedUIAccountsOrganizationsWorkflow(t *testing.T) {
	f := accountApp(t, true)
	f.request(t, "GET", "/org/create", "administrator", nil, false)
	w := f.request(t, "POST", "/org/create", "administrator", url.Values{"org_name": {"laboratory"}, "email": {"lab@example.invalid"}}, true)
	if w.Code != 302 {
		t.Fatal("create org", w.Code, w.Body.String())
	}
	org, err := models.GetUserByName("laboratory")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/org/laboratory", "/org/laboratory/settings", "/org/laboratory/members", "/org/laboratory/teams", "/org/laboratory/teams/new", "/org/laboratory/teams/owners", "/org/laboratory/teams/owners/repositories"} {
		sharedAccountPage(t, f.request(t, "GET", path, "administrator", nil, false), false)
	}
	w = f.request(t, "POST", "/org/laboratory/teams/new", "administrator", url.Values{"team_name": {"readers"}, "desc": {"Read team"}, "permission": {"read"}}, true)
	if w.Code != 302 {
		t.Fatal("team creation", w.Code, w.Body.String())
	}
	team, err := org.GetTeam("readers")
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, "POST", "/org/laboratory/teams/readers/action/add", "administrator", url.Values{"uid": {fmt.Sprint(f.admin.Id)}, "uname": {"member"}}, false)
	if w.Code != 400 || team.IsMember(f.member.Id) {
		t.Fatal("denied team add mutated")
	}
	w = f.request(t, "POST", "/org/laboratory/teams/readers/action/add", "administrator", url.Values{"uid": {fmt.Sprint(f.admin.Id)}, "uname": {"member"}}, true)
	if w.Code != 302 || !team.IsMember(f.member.Id) {
		t.Fatal("team add failed", w.Code)
	}
	w = f.request(t, "POST", "/org/laboratory/teams/readers/action/remove", "member", url.Values{"uid": {fmt.Sprint(f.admin.Id)}}, true)
	if w.Code != 404 {
		t.Fatal("nonowner removed member", w.Code)
	}
	w = f.request(t, "POST", "/org/laboratory/members/action/public", "member", url.Values{"uid": {fmt.Sprint(f.member.Id)}}, true)
	if w.Code != 302 || !f.member.IsPublicMember(org.Id) {
		t.Fatal("public membership action failed", w.Code)
	}
	sharedAccountPage(t, f.request(t, "GET", "/org/laboratory", "", nil, false), false)
}

func installerValues(root string) url.Values {
	run := os.Getenv("USER")
	if run == "" {
		run = os.Getenv("USERNAME")
	}
	return url.Values{"db_type": {"SQLite3"}, "db_path": {filepath.Join(root, "fresh.db")}, "repo_root_path": {filepath.Join(root, "repositories")}, "run_user": {run}, "domain": {"localhost"}, "http_port": {"3000"}, "app_url": {"http://localhost:3000/code/"}, "admin_name": {"setup-admin"}, "admin_email": {"setup@example.invalid"}, "admin_passwd": {"setup-password"}, "admin_confirm_passwd": {"setup-password"}}
}
func TestSharedUIInstallerValidation(t *testing.T) {
	f := accountApp(t, false)
	old := setting.CustomConf
	setting.CustomConf = filepath.Join(f.root, "custom", "app.ini")
	defer func() { setting.CustomConf = old }()
	f.request(t, "GET", "/install", "", nil, false)
	for _, tc := range []struct{ name, key, value string }{{"database", "db_type", "Unknown"}, {"path", "repo_root_path", filepath.Join(f.root, "blocked")}, {"administrator", "admin_name", "admin"}, {"password", "admin_confirm_passwd", "mismatch"}, {"URL", "app_url", "javascript:alert(1)"}, {"SSL", "ssl_mode", "invalid"}, {"port", "http_port", "70000"}} {
		t.Run(tc.name, func(t *testing.T) {
			values := installerValues(f.root)
			values.Set(tc.key, tc.value)
			if tc.name == "path" {
				if err := os.WriteFile(tc.value, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "SSL" {
				values.Set("db_type", "PostgreSQL")
				values.Set("db_host", "127.0.0.1:1")
			}
			w := f.request(t, "POST", "/install", "", values, true)
			sharedAccountPage(t, w, true)
			if !strings.Contains(w.Body.String(), `role="alert"`) {
				t.Fatal("invalid installer submission has no feedback")
			}
			if tc.name == "port" && !strings.Contains(accountControl(t, w.Body.String(), "http_port"), `aria-describedby="http_port-error"`) {
				t.Error("invalid port has no field association")
			}
			if _, err := os.Stat(setting.CustomConf); !os.IsNotExist(err) {
				t.Fatal("invalid setup wrote config", err)
			}
			if f.initialized != 0 {
				t.Fatal("invalid setup initialized hosting")
			}
		})
	}
}
func TestSharedUIInstallerSuccess(t *testing.T) {
	if os.Getenv("HGS_INSTALL_TEST_CHILD") == "" {
		root := t.TempDir()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(root, "install-test.exe")
		if err = os.WriteFile(exe, data, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Join(root, "conf"), 0700); err != nil {
			t.Fatal(err)
		}
		config, err := os.ReadFile("../../conf/app.ini")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, "conf", "app.ini"), config, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSharedUIInstallerSuccess$", "-test.v")
		cmd.Env = append(os.Environ(), "HGS_INSTALL_TEST_CHILD="+root, "GOGS_CUSTOM="+filepath.Join(root, "custom"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated installer process: %v\n%s", err, out)
		}
		t.Log(strings.TrimSpace(string(out)))
		return
	}
	f := accountApp(t, false)
	root := os.Getenv("HGS_INSTALL_TEST_CHILD")
	setting.CustomConf = filepath.Join(root, "custom", "conf", "app.ini")
	f.request(t, "GET", "/install", "", nil, false)
	values := installerValues(root)
	w := f.request(t, "POST", "/install", "", values, true)
	if w.Code != 302 || w.Header().Get("Location") != "http://localhost:3000/code/user/login" {
		t.Fatalf("setup result %d %s", w.Code, w.Body.String())
	}
	if !setting.InstallLock || f.initialized != 1 {
		t.Fatal("install lifecycle incomplete")
	}
	u, err := models.GetUserByName("setup-admin")
	if err != nil || !u.IsAdmin || !u.IsActive || !u.ValidtePassword("setup-password") {
		t.Fatal("initial administrator contract", err)
	}
	w = f.request(t, "GET", "/install", "", nil, false)
	if w.Code != 404 {
		t.Fatal("installed setup accessible", w.Code)
	}
}
func TestSharedUIAccountsCurrentReadPolicy(t *testing.T) {
	f := accountApp(t, true)
	db, err := xorm.NewEngine("sqlite3", models.DbCfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org, err := models.CreateOrganization(&models.User{Name: "privacy-lab", Email: "privacy@example.invalid", Type: models.ORGANIZATION, IsActive: true}, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if err = org.AddMember(f.member.Id); err != nil {
		t.Fatal(err)
	}
	team := &models.Team{OrgId: org.Id, Name: "restricted", Authorize: models.ORG_READABLE}
	if err = models.NewTeam(team); err != nil {
		t.Fatal(err)
	}
	repo := &models.Repository{OwnerId: org.Id, Name: "private-team-project", LowerName: "private-team-project", State: models.RepositoryReady, IsPrivate: true}
	if _, err = db.Insert(repo); err != nil {
		t.Fatal(err)
	}
	if err = team.AddRepository(repo); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/org/privacy-lab", "/org/privacy-lab/teams/restricted/repositories"} {
		w := f.request(t, "GET", path, "member", nil, false)
		sharedAccountPage(t, w, false)
		if strings.Contains(w.Body.String(), "private-team-project") {
			t.Error("unauthorized repository exposed", path)
		}
	}
	// A listed team repository becoming unavailable is removed from rows and counts.
	if _, err = db.Id(repo.Id).Cols("state").Update(&models.Repository{State: models.RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	wOwner := f.request(t, "GET", "/org/privacy-lab/teams/restricted/repositories", "administrator", nil, false)
	sharedAccountPage(t, wOwner, false)
	if strings.Contains(wOwner.Body.String(), "private-team-project") {
		t.Fatal("unready team repository exposed")
	}
	// Rejected inactive credentials have anonymous authority on public pages;
	// direct model reads must still reject the named inactive actor.
	public := &models.Repository{OwnerId: f.admin.Id, Name: "public-profile-project", LowerName: "public-profile-project", State: models.RepositoryReady}
	private := &models.Repository{OwnerId: f.admin.Id, Name: "private-profile-project", LowerName: "private-profile-project", State: models.RepositoryReady, IsPrivate: true}
	if _, err = db.Insert(public, private); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/user/login", "", nil, false)
	login := f.request(t, "POST", "/user/login", "", url.Values{"uname": {"member"}, "password": {"password"}}, true)
	if login.Code != 302 {
		t.Fatalf("member session setup: %d", login.Code)
	}
	sessionCookies := f.cookies
	f.member.IsActive = false
	if err = models.UpdateUser(f.member); err != nil {
		t.Fatal(err)
	}
	access, err := models.RepositoryAccessFor(f.member.Id, public.Id)
	if err != nil || access.Read {
		t.Fatalf("named inactive actor gained model read: %+v %v", access, err)
	}
	for _, mode := range []string{"basic", "session"} {
		who := "member"
		f.cookies = map[string]*http.Cookie{}
		if mode == "session" {
			who = ""
			f.cookies = sessionCookies
		}
		w := f.request(t, "GET", "/profile/administrator", who, nil, false)
		sharedAccountPage(t, w, false)
		if !strings.Contains(w.Body.String(), public.Name) {
			t.Errorf("%s rejected credentials changed anonymous public access", mode)
		}
		for _, hidden := range []string{private.Name, f.admin.Email, `data-popout-target="hg-account"`} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Errorf("%s rejected credentials retained private/signed authority: %s", mode, hidden)
			}
		}
	}

}
func TestSharedUIAdminFormValidation(t *testing.T) {
	f := accountApp(t, true)
	for _, path := range []string{"/admin/orgs", "/admin/repos", "/admin/monitor", "/admin/notices", "/admin/auths", "/admin/auths/new", "/admin/users/new", fmt.Sprintf("/admin/users/%d", f.member.Id), "/user/settings/password", "/user/settings/email", "/user/settings/delete"} {
		sharedAccountPage(t, f.request(t, "GET", path, "administrator", nil, false), false)
	}
	source := &models.LoginSource{Type: models.SMTP, Name: "Fixture SMTP", IsActived: true, Cfg: &models.SMTPConfig{Host: "smtp.example.invalid", Port: 587, Auth: "PLAIN", TLS: true}}
	if err := models.CreateSource(source); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/admin/auths/%d", source.Id)
	sharedAccountPage(t, f.request(t, "GET", path, "administrator", nil, false), false)
	w := f.request(t, "POST", path, "administrator", url.Values{"id": {fmt.Sprint(source.Id)}, "type": {"3"}, "name": {""}, "smtphost": {"preserved.example.invalid"}, "smtpport": {"587"}, "smtpauth": {"LOGIN"}}, true)
	sharedAccountPage(t, w, false)
	if !strings.Contains(w.Body.String(), `action="/code`+path+`"`) || !strings.Contains(w.Body.String(), `value="preserved.example.invalid"`) {
		t.Error("authentication validation lost form/action")
	}
	if !regexp.MustCompile(`<option value="LOGIN"\s+selected`).MatchString(w.Body.String()) {
		t.Error("submitted SMTP mechanism lost on edit")
	}
	w = f.request(t, "POST", "/admin/auths/new", "administrator", url.Values{"type": {"3"}, "name": {""}, "smtpauth": {"LOGIN"}}, true)
	if !regexp.MustCompile(`<option value="LOGIN"\s+selected`).MatchString(w.Body.String()) {
		t.Error("submitted SMTP mechanism lost on new")
	}
	path = fmt.Sprintf("/admin/users/%d", f.member.Id)
	w = f.request(t, "POST", path, "administrator", url.Values{"email": {"invalid-email"}, "password": {"never-reflect-admin-password"}}, true)
	sharedAccountPage(t, w, false)
	if !strings.Contains(w.Body.String(), `action="/code`+path+`"`) || !strings.Contains(w.Body.String(), `value="invalid-email"`) {
		t.Error("admin user validation lost form/action")
	}
	if strings.Contains(w.Body.String(), "never-reflect-admin-password") {
		t.Error("admin password reflected")
	}
}
func TestSharedUIAdminCreateProviderContract(t *testing.T) {
	f := accountApp(t, true)
	source := &models.LoginSource{Type: models.SMTP, Name: "Fixture provider", IsActived: true, Cfg: &models.SMTPConfig{Host: "smtp.example.invalid", Port: 587, Auth: "PLAIN"}}
	if err := models.CreateSource(source); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/admin/users/new", "administrator", nil, false)
	for _, choice := range []string{"malformed", "2-99999", fmt.Sprintf("2-%d", source.Id)} {
		w := f.request(t, "POST", "/admin/users/new", "administrator", url.Values{"uname": {"external-user"}, "email": {"external@example.invalid"}, "password": {"initial-password"}, "retype": {"initial-password"}, "logintype": {choice}, "loginname": {"provider-login"}}, true)
		sharedAccountPage(t, w, false)
		if _, err := models.GetUserByName("external-user"); err != models.ErrUserNotExist {
			t.Fatal("invalid provider created account", choice, err)
		}
	}
	choice := fmt.Sprintf("3-%d", source.Id)
	w := f.request(t, "POST", "/admin/users/new", "administrator", url.Values{"uname": {"external-user"}, "email": {"bad-email"}, "password": {"initial-password"}, "retype": {"initial-password"}, "logintype": {choice}, "loginname": {"provider-login"}}, true)
	sharedAccountPage(t, w, false)
	if !strings.Contains(w.Body.String(), `value="`+choice+`" selected`) || !strings.Contains(w.Body.String(), `value="provider-login"`) {
		t.Fatal("provider choice/login name lost")
	}
	w = f.request(t, "POST", "/admin/users/new", "administrator", url.Values{"uname": {"external-user"}, "email": {"external@example.invalid"}, "password": {"initial-password"}, "retype": {"initial-password"}, "logintype": {choice}, "loginname": {"provider-login"}}, true)
	if w.Code != 302 {
		t.Fatal("provider create", w.Code, w.Body.String())
	}
	u, err := models.GetUserByName("external-user")
	if err != nil || u.LoginType != models.SMTP || u.LoginSource != source.Id || u.LoginName != "provider-login" {
		t.Fatal("provider contract lost", err)
	}
}
func TestSharedUIAccountsSettingsAndKeys(t *testing.T) {
	f := accountApp(t, true)
	f.request(t, "GET", "/user/settings", "member", nil, false)
	w := f.request(t, "POST", "/user/settings", "member", url.Values{"uname": {"member"}, "fullname": {"Updated Person"}, "email": {"member@example.invalid"}, "avatar": {"avatar@example.invalid"}, "location": {"Lab"}}, true)
	if w.Code != 302 {
		t.Fatal("profile save", w.Code)
	}
	u, err := models.GetUserById(f.member.Id)
	if err != nil || u.FullName != "Updated Person" || u.AvatarEmail != "avatar@example.invalid" {
		t.Fatal("profile not saved", err)
	}
	f.request(t, "GET", "/user/settings/ssh", "member", nil, false)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, "POST", "/user/settings/ssh", "member", url.Values{"title": {"fixture-key"}, "content": {string(ssh.MarshalAuthorizedKey(key))}}, true)
	if w.Code != 302 {
		t.Fatal("key create", w.Code, w.Body.String())
	}
	keys, err := models.ListPublicKeys(f.member.Id)
	if err != nil || len(keys) != 1 {
		t.Fatal("key missing", err)
	}
	w = f.request(t, "POST", "/user/settings/ssh", "member", url.Values{"_method": {"DELETE"}, "id": {fmt.Sprint(keys[0].Id)}}, true)
	if w.Code != 302 {
		t.Fatal("key delete", w.Code)
	}
	keys, err = models.ListPublicKeys(f.member.Id)
	if err != nil || len(keys) != 0 {
		t.Fatal("key retained", err)
	}
	f.request(t, "GET", "/user/settings/email", "member", nil, false)
	w = f.request(t, "POST", "/user/settings/email", "member", url.Values{"email": {"second@example.invalid"}}, true)
	if w.Code != 302 {
		t.Fatal("email add", w.Code)
	}
	emails, err := models.GetEmailAddresses(f.member.Id)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, email := range emails {
		if email.Email == "second@example.invalid" {
			id = email.Id
		}
	}
	if id == 0 {
		t.Fatal("email missing")
	}
	w = f.request(t, "POST", "/user/settings/email", "administrator", url.Values{"_method": {"DELETE"}, "id": {fmt.Sprint(id)}}, true)
	if w.Code != 404 {
		t.Fatal("foreign email removal allowed", w.Code)
	}
}
func TestSharedUIAccountsLocalesAndInstallerOptions(t *testing.T) {
	f := accountApp(t, false)
	oldName := setting.AppName
	setting.AppName = "Operator Laboratory"
	defer func() { setting.AppName = oldName }()
	for _, lang := range []string{"en-US", "de-DE"} {
		w := f.request(t, "GET", "/install?lang="+lang, "", nil, false)
		sharedAccountPage(t, w, true)
		if !strings.Contains(w.Body.String(), `lang="`+lang+`"`) || !strings.Contains(w.Body.String(), "Operator Laboratory") || strings.Contains(w.Body.String(), ">ui.") {
			t.Fatal("locale/instance identity", lang, w.Body.String())
		}
	}
	for _, db := range []string{"MySQL", "PostgreSQL", "SQLite3"} {
		values := url.Values{"db_type": {db}, "db_host": {"retained-host"}, "db_user": {"retained-user"}, "db_name": {"retained-name"}, "db_path": {"retained-path"}, "ssl_mode": {"verify-full"}, "smtp_email": {"mail@example.invalid"}, "smtp_passwd": {"hidden-mail-password"}}
		w := f.request(t, "POST", "/install", "", values, true)
		sharedAccountPage(t, w, true)
		if !strings.Contains(w.Body.String(), `value="`+db+`"selected`) || !strings.Contains(w.Body.String(), `value="retained-host"`) || !strings.Contains(w.Body.String(), `value="verify-full" selected`) {
			t.Errorf("installer option/value state lost %s", db)
		}
		if strings.Contains(w.Body.String(), "hidden-mail-password") {
			t.Fatal("mail password reflected")
		}
	}
}

// Inspect the real rendered control, without depending on attribute ordering.
func accountControl(t *testing.T, body, name string) string {
	t.Helper()
	for _, input := range regexp.MustCompile(`<input\b[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(input, `name="`+name+`"`) {
			return input
		}
	}
	t.Fatalf("missing input %q", name)
	return ""
}
func accountValue(t *testing.T, body, name, want string) {
	t.Helper()
	input := accountControl(t, body, name)
	if !strings.Contains(input, `value="`+html.EscapeString(want)+`"`) {
		t.Errorf("%s lost value %q: %s", name, want, input)
	}
}
func accountChecked(t *testing.T, body, name string, want bool) {
	t.Helper()
	if got := strings.Contains(accountControl(t, body, name), "checked"); got != want {
		t.Errorf("%s checked=%v, want %v", name, got, want)
	}
}
func accountPermission(t *testing.T, body string) string {
	t.Helper()
	selected := ""
	for _, input := range regexp.MustCompile(`<input\b[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(input, `name="permission"`) && strings.Contains(input, "checked") {
			if selected != "" {
				t.Fatal("multiple permission radios checked")
			}
			match := regexp.MustCompile(`value="([^"]+)"`).FindStringSubmatch(input)
			if len(match) != 2 {
				t.Fatal("permission without value")
			}
			selected = match[1]
		}
	}
	return selected
}
func TestSharedUIAccountsRedirectFlashState(t *testing.T) {
	f := accountApp(t, true)
	f.member.FullName, f.member.Website, f.member.Location = "Saved Profile", "https://profile.example.invalid", "Saved Location"
	f.member.AvatarEmail = "avatar@example.invalid"
	f.member.IsAdmin = true
	if err := models.UpdateUser(f.member); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/user/settings", "member", nil, false)
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	if err := writer.WriteField("_csrf", f.token); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("avatar", "not-an-image.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("This is not an image.")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/code/user/settings/avatar", &upload)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.SetBasicAuth("member", "password")
	for _, c := range f.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		f.cookies[c.Name] = c
	}
	if w.Code != 302 || w.Header().Get("Location") != "/code/user/settings" {
		t.Fatalf("avatar redirect: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, "GET", "/user/settings", "member", nil, false)
	sharedAccountPage(t, w, false)
	if !strings.Contains(w.Body.String(), "hg-alert-danger") {
		t.Error("avatar error flash missing")
	}
	for name, value := range map[string]string{"uname": "member", "fullname": "Saved Profile", "email": f.member.Email, "website": f.member.Website, "location": f.member.Location, "avatar": f.member.AvatarEmail} {
		accountValue(t, w.Body.String(), name, value)
	}
	// A current-request RenderWithErr must still show submitted safe fields.
	w = f.request(t, "POST", "/user/settings", "member", url.Values{"uname": {"administrator"}, "fullname": {"Submitted Profile"}, "email": {"submitted@example.invalid"}, "website": {"https://submitted.example.invalid"}, "location": {"Submitted Location"}}, true)
	sharedAccountPage(t, w, false)
	accountValue(t, w.Body.String(), "fullname", "Submitted Profile")
	accountValue(t, w.Body.String(), "email", "submitted@example.invalid")
	if !strings.Contains(w.Body.String(), "hg-alert-danger") {
		t.Error("current form error missing")
	}

	// The member owns an organization, so the real protected deletion must reject it.
	if _, err = models.CreateOrganization(&models.User{Name: "retained-org", Email: "org@example.invalid", Type: models.ORGANIZATION, IsActive: true}, f.member); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/admin/users/%d", f.member.Id)
	f.request(t, "GET", path, "administrator", nil, false)
	w = f.request(t, "POST", path+"/delete", "administrator", nil, true)
	if w.Code != 302 || w.Header().Get("Location") != "/code"+path {
		t.Fatalf("user delete redirect: %d", w.Code)
	}
	w = f.request(t, "GET", path, "administrator", nil, false)
	sharedAccountPage(t, w, false)
	if !strings.Contains(w.Body.String(), "hg-alert-danger") {
		t.Error("user deletion error missing")
	}
	for name, value := range map[string]string{"email": f.member.Email, "website": f.member.Website, "location": f.member.Location, "avatar": f.member.AvatarEmail} {
		accountValue(t, w.Body.String(), name, value)
	}
	accountChecked(t, w.Body.String(), "active", true)
	accountChecked(t, w.Body.String(), "admin", true)

	source := &models.LoginSource{Type: models.SMTP, Name: "Retained Source", IsActived: true, AllowAutoRegister: true, Cfg: &models.SMTPConfig{Host: "retained.example.invalid", Port: 587, Auth: "PLAIN", TLS: true}}
	if err = models.CreateSource(source); err != nil {
		t.Fatal(err)
	}
	f.member.LoginSource = source.Id
	if err = models.UpdateUser(f.member); err != nil {
		t.Fatal(err)
	}
	path = fmt.Sprintf("/admin/auths/%d", source.Id)
	f.request(t, "GET", path, "administrator", nil, false)
	w = f.request(t, "POST", path+"/delete", "administrator", nil, true)
	if w.Code != 302 || w.Header().Get("Location") != "/code"+path {
		t.Fatalf("source delete redirect: %d", w.Code)
	}
	w = f.request(t, "GET", path, "administrator", nil, false)
	sharedAccountPage(t, w, false)
	if !strings.Contains(w.Body.String(), "hg-alert-danger") {
		t.Error("source deletion error missing")
	}
	accountValue(t, w.Body.String(), "name", "Retained Source")
	accountValue(t, w.Body.String(), "smtphost", "retained.example.invalid")
	accountValue(t, w.Body.String(), "smtpport", "587")
	for _, name := range []string{"tls", "allowautoregister", "is_actived"} {
		accountChecked(t, w.Body.String(), name, true)
	}
	if _, err = models.GetUserById(f.member.Id); err != nil {
		t.Fatal("protected account deleted", err)
	}
	if _, err = models.GetLoginSourceById(source.Id); err != nil {
		t.Fatal("protected source deleted", err)
	}
}

func TestSharedUIAccountsTeamPermissionRetry(t *testing.T) {
	f := accountApp(t, true)
	organization, err := models.CreateOrganization(&models.User{Name: "permission-org", Email: "permissions@example.invalid", Type: models.ORGANIZATION, IsActive: true}, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	base := "/org/permission-org/teams"
	for _, permission := range []string{"write", "admin"} {
		w := f.request(t, "GET", base+"/new", "administrator", nil, false)
		if got := accountPermission(t, w.Body.String()); got != "read" {
			t.Fatalf("initial default = %s", got)
		}
		w = f.request(t, "POST", base+"/new", "administrator", url.Values{"team_name": {""}, "desc": {"Keep this description"}, "permission": {permission}}, true)
		sharedAccountPage(t, w, false)
		accountValue(t, w.Body.String(), "desc", "Keep this description")
		selected := accountPermission(t, w.Body.String())
		if selected != permission {
			t.Errorf("new team retained %q, want %q", selected, permission)
		}
		// Retry exactly the authority rendered to the user after correcting the name.
		w = f.request(t, "POST", base+"/new", "administrator", url.Values{"team_name": {"team-" + permission}, "desc": {"Keep this description"}, "permission": {selected}}, true)
		if w.Code != 302 {
			t.Fatalf("new team retry %d", w.Code)
		}
		team, err := models.GetTeam(organization.Id, "team-"+permission)
		if err != nil {
			t.Fatal(err)
		}
		want := models.ORG_WRITABLE
		if permission == "admin" {
			want = models.ORG_ADMIN
		}
		if team.Authorize != want {
			t.Errorf("new team persisted %v, want %v", team.Authorize, want)
		}
	}
	if err := models.NewTeam(&models.Team{OrgId: organization.Id, Name: "reduction-team", Authorize: models.ORG_ADMIN}); err != nil {
		t.Fatal(err)
	}
	path := base + "/reduction-team/edit"
	w := f.request(t, "GET", path, "administrator", nil, false)
	if got := accountPermission(t, w.Body.String()); got != "admin" {
		t.Fatalf("initial edit = %s", got)
	}
	w = f.request(t, "POST", path, "administrator", url.Values{"team_name": {"reduction-team"}, "desc": {strings.Repeat("x", 256)}, "permission": {"read"}}, true)
	sharedAccountPage(t, w, false)
	selected := accountPermission(t, w.Body.String())
	if selected != "read" {
		t.Errorf("permission reduction retained %q, want read", selected)
	}
	team, err := models.GetTeam(organization.Id, "reduction-team")
	if err != nil {
		t.Fatal(err)
	}
	if team.Authorize != models.ORG_ADMIN {
		t.Error("invalid edit changed persisted permission")
	}
	w = f.request(t, "POST", path, "administrator", url.Values{"team_name": {"reduction-team"}, "desc": {"Corrected description"}, "permission": {selected}}, true)
	if w.Code != 302 {
		t.Fatalf("edit retry %d", w.Code)
	}
	team, err = models.GetTeam(organization.Id, "reduction-team")
	if err != nil {
		t.Fatal(err)
	}
	if team.Authorize != models.ORG_READABLE {
		t.Errorf("retry persisted %v, want read", team.Authorize)
	}
}

func TestSharedUISignupAutocomplete(t *testing.T) {
	f := accountApp(t, true)
	w := f.request(t, "GET", "/user/sign_up", "", nil, false)
	for name, token := range map[string]string{"uname": "username", "email": "email", "password": "new-password", "retype": "new-password"} {
		if !strings.Contains(accountControl(t, w.Body.String(), name), `autocomplete="`+token+`"`) {
			t.Errorf("%s missing autocomplete %s", name, token)
		}
	}
}

func TestSharedUIAdminTableContainers(t *testing.T) {
	f := accountApp(t, true)
	wrapper := regexp.MustCompile(`<div class="hg-table-scroll" tabindex="0" role="region" aria-label="[^"]+">\s*<table class="hg-table`)
	for _, path := range []string{"/admin", "/admin/users", "/admin/orgs", "/admin/repos", "/admin/auths", "/admin/notices", "/admin/monitor"} {
		w := f.request(t, "GET", path, "administrator", nil, false)
		sharedAccountPage(t, w, false)
		tables := strings.Count(w.Body.String(), `<table class="hg-table`)
		if tables == 0 || len(wrapper.FindAllString(w.Body.String(), -1)) != tables {
			t.Errorf("%s tables lack keyboard-focusable labelled containers", path)
		}
	}
}
