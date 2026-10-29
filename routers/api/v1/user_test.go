package v1

import (
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"testing"
)

func TestUserAvatarAPIURL(t *testing.T) {
	oldProtocol := setting.Protocol
	setting.Protocol = setting.HTTP
	t.Cleanup(func() { setting.Protocol = oldProtocol })
	oldSource, oldSub, oldCache, oldDisabled := setting.GravatarSource, setting.AppSubUrl, setting.Service.EnableCacheAvatar, setting.DisableGravatar
	t.Cleanup(func() {
		setting.GravatarSource, setting.AppSubUrl, setting.Service.EnableCacheAvatar, setting.DisableGravatar = oldSource, oldSub, oldCache, oldDisabled
	})
	setting.GravatarSource = setting.OfficialGravatarSource
	setting.AppSubUrl = "/code"
	setting.Service.EnableCacheAvatar = false
	setting.DisableGravatar = false
	u := &models.User{Id: 42, Email: "person@example.invalid"}
	if got := ToApiUser(u).AvatarUrl; got != "https://gravatar.com/avatar/9dc98d058b1d6f4688703fdded7387b1851cddf5e946b37e324c029fc811905f" {
		t.Errorf("official API avatar URL %q", got)
	}
	setting.Service.EnableCacheAvatar = true
	if got := ToApiUser(u).AvatarUrl; got != "/code/avatar/9dc98d058b1d6f4688703fdded7387b1851cddf5e946b37e324c029fc811905f" {
		t.Errorf("cached API avatar URL %q", got)
	}
	u.UseCustomAvatar = true
	if got := ToApiUser(u).AvatarUrl; got != "/code/avatars/42" {
		t.Errorf("custom API avatar URL %q", got)
	}
}
