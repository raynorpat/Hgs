//go:build integration

package integration

import (
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"net/url"
	"strings"
	"testing"
)

func TestSharedAvatarGeometry(t *testing.T) {
	f := accountApp(t, true)
	for _, who := range []string{"", f.member.Name, f.admin.Name} {
		w := f.request(t, "GET", "/profile/member", who, nil, false)
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("profile status %d", w.Code)
		}
		if !strings.Contains(body, `data-shared-avatar`) || !strings.Contains(body, `width="160" height="160"`) {
			t.Error("profile lacks shared fixed-size avatar")
		}
		if who != "" && !strings.Contains(body, `width="28" height="28"`) {
			t.Error("rail lacks fixed-size avatar")
		}
		ownerEdit := strings.Contains(body, `id="profile-avatar"`)
		if ownerEdit != (who == f.member.Name) {
			t.Errorf("avatar edit owner boundary for %q", who)
		}
	}
}

func TestSharedAvatarSourcesAndSettings(t *testing.T) {
	f := accountApp(t, true)
	oldSource := setting.GravatarSource
	t.Cleanup(func() { setting.GravatarSource = oldSource })
	setting.GravatarSource = setting.OfficialGravatarSource
	setting.DisableGravatar = false
	f.member.AvatarEmail = " Avatar@Example.Invalid "
	if err := models.UpdateUser(f.member); err != nil {
		t.Fatal(err)
	}
	hash := "b898f68308dd1260b8d5e5545aae135aad6959ce536861f67a0bb9c5f53ec74f"
	for _, cached := range []bool{false, true} {
		setting.Service.EnableCacheAvatar = cached
		w := f.request(t, "GET", "/profile/member", f.member.Name, nil, false)
		body := w.Body.String()
		prefix := setting.OfficialGravatarSource
		if cached {
			prefix = "/code/avatar/"
		}
		for _, size := range []string{"28", "160"} {
			if !strings.Contains(body, `src="`+prefix+hash+`?size=`+size+`"`) {
				t.Errorf("cached=%v lacks size %s", cached, size)
			}
		}
		if strings.Contains(strings.ToLower(body), "avatar@example.invalid") {
			t.Error("avatar email leaked into public profile markup")
		}
	}
	w := f.request(t, "GET", "/user/settings", f.member.Name, nil, false)
	if !strings.Contains(w.Body.String(), "Avatar@Example.Invalid") {
		t.Error("owner avatar email input lost")
	}
	values := url.Values{"uname": {f.member.Name}, "email": {f.member.Email}, "avatar": {" Person@Example.Invalid "}}
	w = f.request(t, "POST", "/user/settings", f.member.Name, values, true)
	if w.Code != 302 {
		t.Fatalf("profile edit status %d: %s", w.Code, w.Body.String())
	}
	saved, err := models.GetUserById(f.member.Id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Avatar != "9dc98d058b1d6f4688703fdded7387b1851cddf5e946b37e324c029fc811905f" {
		t.Errorf("profile edit key %s", saved.Avatar)
	}
	setting.DisableGravatar = true
	w = f.request(t, "GET", "/profile/member", f.member.Name, nil, false)
	if strings.Contains(w.Body.String(), setting.OfficialGravatarSource) || strings.Contains(w.Body.String(), "/code/avatar/") {
		t.Error("disabled profile emitted a provider/cache URL")
	}
}
