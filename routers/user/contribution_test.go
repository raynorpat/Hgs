package user_test

import (
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
	"testing"
)

func TestContributionVerifyDisabled(t *testing.T) {
	f := newDiscoveryFixture(t, "/code")
	old := setting.MailService
	setting.MailService = nil
	t.Cleanup(func() { setting.MailService = old })
	f.m.Post("/user/settings/email/verify", middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true}), user.SettingsEmailVerify)
	w := f.request("POST", "/user/settings/email/verify", f.alice.Name, "id=0", nil)
	if w.Code != 400 {
		t.Fatalf("missing CSRF accepted: %d", w.Code)
	}
}
