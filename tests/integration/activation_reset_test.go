//go:build integration

package integration

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/setting"
)

func activationEntryFromLogin(t *testing.T, f *accountFixture, prefix string, page *httptest.ResponseRecorder) {
	t.Helper()
	match := regexp.MustCompile(`<a href="([^"]*/user/activate)">`).FindStringSubmatch(page.Body.String())
	if page.Code != 200 || len(match) != 2 || match[1] != prefix+"/user/activate" {
		t.Fatalf("normal sign-in lacks activation recovery entry: status=%d prefix=%q", page.Code, prefix)
	}
	form := f.request(t, "GET", strings.TrimPrefix(match[1], prefix), "", nil, false)
	if form.Code != 200 || !strings.Contains(form.Body.String(), `action="`+prefix+`/user/activate"`) || !strings.Contains(form.Body.String(), `name="password"`) || !strings.Contains(form.Body.String(), `name="uname"`) {
		t.Fatal("sign-in recovery link does not reach credential form", form.Code)
	}
}

func TestPendingActivationEntryPoint(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			f := accountApp(t, true)
			setting.AppSubUrl = prefix
			f.m.SetURLPrefix(prefix)
			oldMail := setting.MailService
			t.Cleanup(func() { setting.MailService = oldMail })
			setting.MailService = nil
			activationEntryFromLogin(t, f, prefix, f.request(t, "GET", "/user/login", "", nil, false))
			activationEntryFromLogin(t, f, prefix, f.request(t, "POST", "/user/login", "", url.Values{"uname": {"missing-user"}, "password": {"wrong-password"}}, true))
		})
	}
}

func TestPendingActivationAfterPasswordReset(t *testing.T) {
	for _, prefix := range []string{"", "/code"} {
		t.Run("prefix="+prefix, func(t *testing.T) { pendingActivationAfterPasswordReset(t, prefix) })
	}
}

func pendingActivationAfterPasswordReset(t *testing.T, prefix string) {
	f := accountApp(t, true)
	setting.AppSubUrl = prefix
	f.m.SetURLPrefix(prefix)
	capture, restore := mailer.CaptureIntegrationMail()
	defer restore()
	oldMail := setting.MailService
	oldURL := setting.AppUrl
	t.Cleanup(func() { setting.MailService = oldMail; setting.AppUrl = oldURL })
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	setting.AppUrl = "https://fixture.example.invalid" + prefix + "/"
	setting.Service.RegisterEmailConfirm = true
	setting.Service.ActiveCodeLives = 180
	mailCode := func(action string) string {
		t.Helper()
		select {
		case message := <-capture:
			match := regexp.MustCompile(regexp.QuoteMeta(action) + `\?code=([a-zA-Z0-9.]+)`).FindStringSubmatch(message.Body)
			if len(match) != 2 {
				t.Fatalf("captured mail missing %s token", action)
			}
			return match[1]
		case <-time.After(time.Second):
			t.Fatalf("missing captured %s mail", action)
			return ""
		}
	}
	values := f.signupValues(t, "pending-reset", "pending-reset@example.invalid")
	if w := f.request(t, "POST", "/user/sign_up", "", values, true); w.Code != 200 {
		t.Fatal("signup", w.Code)
	}
	initialCode := mailCode("/user/activate")
	u, err := models.GetUserByName("pending-reset")
	if err != nil {
		t.Fatal(err)
	}
	// Expire only the owned legacy mail cache entry; no wall-clock throttle pass is claimed.
	if err := f.store.Delete("MailResendLimit_" + u.LowerName); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/user/forget_password", "", nil, false)
	if w := f.request(t, "POST", "/user/forget_password", "", url.Values{"email": {u.Email}}, true); w.Code != 200 {
		t.Fatal("reset request", w.Code)
	}
	resetCode := mailCode("/user/reset_password")
	f.request(t, "GET", "/user/reset_password?code="+resetCode, "", nil, false)
	reset := f.request(t, "POST", "/user/reset_password?code="+resetCode, "", url.Values{"password": {"changed-pending-password"}}, true)
	if reset.Code != 302 {
		t.Fatal("reset", reset.Code)
	}
	if reset.Header().Get("Location") != prefix+"/user/login" {
		t.Fatal("reset return changed", reset.Header().Get("Location"))
	}
	activationEntryFromLogin(t, f, prefix, f.request(t, "GET", strings.TrimPrefix(reset.Header().Get("Location"), prefix), "", nil, false))
	u, err = models.GetUserById(u.Id)
	if err != nil {
		t.Fatal(err)
	}
	if u.IsActive || !u.PendingActivation || !u.ValidtePassword("changed-pending-password") {
		t.Fatalf("normal reset stranded pending signup: active=%v pending=%v err=%v", u.IsActive, u.PendingActivation, err)
	}
	for _, code := range []string{initialCode, resetCode} {
		if models.VerifyUserActiveCode(code) != nil {
			t.Fatal("old proof survived password/Rands rotation")
		}
		if w := f.request(t, "GET", "/user/activate?code="+code, "", nil, false); w.Code == 302 {
			t.Fatal("old activation token accepted")
		}
	}
	if w := f.request(t, "POST", "/user/reset_password?code="+resetCode, "", url.Values{"password": {"replayed-password"}}, true); w.Code == 302 {
		t.Fatal("old reset token replayed")
	}
	if _, err := models.UserSignIn(u.Name, "changed-pending-password"); err == nil {
		t.Fatal("reset granted inactive login")
	}
	if w := f.request(t, "GET", "/user/settings", "", nil, false); w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "/user/login") {
		t.Fatal("reset granted ordinary session", w.Code)
	}
	db, err := xorm.NewEngine("sqlite3", models.DbCfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE email_verification_delivery SET sent_unix = 0 WHERE user_id = ?", u.Id); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/user/activate", "", nil, false)
	if w := f.request(t, "POST", "/user/activate", "", url.Values{"uname": {u.Name}, "password": {"secret-password"}}, true); w.Code != 200 {
		t.Fatal("old password response", w.Code)
	}
	select {
	case <-capture:
		t.Fatal("old password sent mail")
	default:
	}
	var delivery models.EmailVerificationDelivery
	if _, err := db.Id(u.Id).Get(&delivery); err != nil || delivery.SentUnix != 0 {
		t.Fatal("old password reserved delivery", err)
	}
	w := f.request(t, "POST", "/user/activate", "", url.Values{"uname": {u.Name}, "password": {"changed-pending-password"}}, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "changed-pending-password") {
		t.Fatal("new credential resend", w.Code)
	}
	newCode := mailCode("/user/activate")
	if w := f.request(t, "GET", "/user/settings", "", nil, false); w.Code != 302 {
		t.Fatal("resend granted ordinary session", w.Code)
	}
	if w := f.request(t, "GET", "/user/activate?code="+newCode, "", nil, false); w.Code != 302 {
		t.Fatal("new activation", w.Code)
	}
	u, err = models.GetUserById(u.Id)
	if err != nil || !u.IsActive || u.PendingActivation {
		t.Fatal("activation did not finish pending reset signup", err)
	}
}
