//go:build integration

package integration

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"fmt"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/setting"
)

func TestPendingActivationRecovery(t *testing.T) {
	f := accountApp(t, true)
	capture, restore := mailer.CaptureIntegrationMail()
	defer restore()
	oldMail := setting.MailService
	t.Cleanup(func() { setting.MailService = oldMail })
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	setting.Service.RegisterEmailConfirm = true
	setting.Service.ActiveCodeLives = 180
	values := f.signupValues(t, "pending-user", "pending@example.invalid")
	f.request(t, "POST", "/user/sign_up", "", values, true)
	select {
	case <-capture:
	case <-time.After(time.Second):
		t.Fatal("initial mail missing")
	}
	pending, err := models.GetUserByName("pending-user")
	if err != nil {
		t.Fatal(err)
	}
	expired := mailer.CreateUserActiveCode(pending, base.DateFormat(time.Now().Add(-4*time.Hour), "YmdHi"))
	w := f.request(t, "GET", "/user/activate?code="+expired, "", nil, false)
	if !strings.Contains(w.Body.String(), `name="password"`) {
		t.Fatal("expired/lost token has no credential recovery form")
	}
	values = url.Values{"uname": {"pending-user"}, "password": {"secret-password"}}
	w = f.request(t, "POST", "/user/activate", "", values, false)
	if w.Code != 400 {
		t.Fatalf("CSRF %d", w.Code)
	}
	f.request(t, "GET", "/user/activate", "", nil, false)
	w = f.request(t, "POST", "/user/activate", "", values, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret-password") {
		t.Fatalf("resend response %d", w.Code)
	}
	select {
	case <-capture:
		t.Fatal("durable throttle bypassed")
	default:
	}
	db, err := xorm.NewEngine("sqlite3", models.DbCfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE email_verification_delivery SET sent_unix = 0"); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/user/activate", "", values, true)
	select {
	case <-capture:
	case <-time.After(time.Second):
		t.Fatal("lost-token resend unreachable")
	}
	u, err := models.GetUserByName("pending-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = models.UserSignIn(u.Name, "secret-password"); err == nil {
		t.Fatal("resend granted inactive login")
	}
	code := mailer.CreateUserActiveCode(u, nil)
	w = f.request(t, "GET", "/user/activate?code="+code, "", nil, false)
	if w.Code != 302 {
		t.Fatalf("resent activation %d", w.Code)
	}
	u, _ = models.GetUserById(u.Id)
	if !u.IsActive || u.PendingActivation {
		t.Fatal("activation did not clear pending state")
	}
}

func TestPendingActivationDisabledAndNoMail(t *testing.T) {
	f := accountApp(t, true)
	capture, restore := mailer.CaptureIntegrationMail()
	defer restore()
	oldMail := setting.MailService
	t.Cleanup(func() { setting.MailService = oldMail })
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	setting.Service.RegisterEmailConfirm = true
	values := f.signupValues(t, "pending-disabled", "pending-disabled@example.invalid")
	f.request(t, "POST", "/user/sign_up", "", values, true)
	<-capture
	u, err := models.GetUserByName("pending-disabled")
	if err != nil {
		t.Fatal(err)
	}
	code := mailer.CreateUserActiveCode(u, nil)
	db, err := xorm.NewEngine("sqlite3", models.DbCfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE email_verification_delivery SET sent_unix = 0"); err != nil {
		t.Fatal(err)
	}
	values = url.Values{"uname": {u.Name}, "password": {"secret-password"}}
	setting.MailService = nil
	w := f.request(t, "POST", "/user/activate", "", values, true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var delivery models.EmailVerificationDelivery
	if _, err = db.Id(u.Id).Get(&delivery); err != nil || delivery.SentUnix != 0 {
		t.Fatalf("no-mail reserved throttle %+v %v", delivery, err)
	}
	setting.MailService = &setting.Mailer{From: "fixture@example.invalid"}
	path := fmt.Sprintf("/admin/users/%d", u.Id)
	f.request(t, "GET", path, "administrator", nil, false)
	w = f.request(t, "POST", path, "administrator", url.Values{"email": {u.Email}, "avatar": {u.Email}, "logintype": {"0-0"}}, true)
	if w.Code != 302 {
		t.Fatalf("admin disable %d %s", w.Code, w.Body.String())
	}
	u, _ = models.GetUserById(u.Id)
	if u.PendingActivation || u.IsActive {
		t.Fatal("admin disable retained pending proof")
	}
	for _, submitted := range []url.Values{values, {"uname": {"missing"}, "password": {"secret-password"}}, {"uname": {u.Name}, "password": {"wrong"}}} {
		w = f.request(t, "POST", "/user/activate", "", submitted, true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "If these credentials") {
			t.Fatal("nondisclosing response changed")
		}
	}
	select {
	case <-capture:
		t.Fatal("disabled/wrong credentials sent mail")
	default:
	}
	w = f.request(t, "GET", "/user/activate?code="+code, "", nil, false)
	if w.Code == 302 {
		t.Fatal("disabled account accepted old activation token")
	}
}
