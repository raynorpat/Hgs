package models

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"github.com/gogits/gogs/modules/setting"
	"image"
	"image/png"
	"strings"
	"testing"
)

func avatarSettings(t *testing.T) {
	oldSub, oldSource, oldDisabled, oldCache := setting.AppSubUrl, setting.GravatarSource, setting.DisableGravatar, setting.Service.EnableCacheAvatar
	t.Cleanup(func() {
		setting.AppSubUrl, setting.GravatarSource, setting.DisableGravatar, setting.Service.EnableCacheAvatar = oldSub, oldSource, oldDisabled, oldCache
	})
	setting.AppSubUrl = "/code"
	setting.GravatarSource = "https://gravatar.com/avatar/"
	setting.DisableGravatar = false
	setting.Service.EnableCacheAvatar = false
}
func TestUserAvatarPrecedence(t *testing.T) {
	avatarSettings(t)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("avatar@example.invalid")))
	u := &User{Id: 42, Name: "person", Email: "account@example.invalid", AvatarEmail: " Avatar@Example.Invalid ", Avatar: "stale-md5"}
	for _, tc := range []struct {
		name                    string
		custom, disabled, cache bool
		want                    string
	}{
		{"upload", true, true, true, "/code/avatars/42"}, {"disabled", false, true, true, "/code/img/avatar_default.jpg"}, {"cache", false, false, true, "/code/avatar/" + hash}, {"direct", false, false, false, "https://gravatar.com/avatar/" + hash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u.UseCustomAvatar = tc.custom
			setting.DisableGravatar = tc.disabled
			setting.Service.EnableCacheAvatar = tc.cache
			got := u.AvatarLink()
			if !strings.HasPrefix(got, tc.want) || strings.Contains(got, "@") {
				t.Errorf("avatar URL %q, want %q", got, tc.want)
			}
		})
	}
	u.Email = ""
	u.AvatarEmail = ""
	if got := u.AvatarLink(); got != "/code/img/avatar_default.jpg" {
		t.Errorf("empty email URL %q", got)
	}
}
func TestGravatarConfiguration(t *testing.T) {
	avatarSettings(t)
	u := &User{Email: "person@example.invalid", Avatar: "old"}
	for _, source := range []string{"http://gravatar.com/avatar/", "https://evil.invalid/avatar/", "//gravatar.com/avatar/", "https://gravatar.com/avatar/?url=https://evil.invalid"} {
		setting.GravatarSource = source
		if got := u.AvatarLink(); got != "/code/img/avatar_default.jpg" {
			t.Errorf("unsafe source %s emitted %s", source, got)
		}
	}
}
func TestUserAvatarStoredKeys(t *testing.T) {
	newTestDatabase(t)
	avatarSettings(t)
	u := &User{Name: "avatarperson", Email: "Account@Example.Invalid", Passwd: "password"}
	if err := CreateUser(u); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("account@example.invalid")))
	if u.Avatar != want {
		t.Errorf("created key %s want %s", u.Avatar, want)
	}
	u.AvatarEmail = " Avatar@Example.Invalid "
	if err := UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	saved, err := GetUserById(u.Id)
	if err != nil {
		t.Fatal(err)
	}
	want = fmt.Sprintf("%x", sha256.Sum256([]byte("avatar@example.invalid")))
	if saved.Avatar != want {
		t.Errorf("updated key %s want %s", saved.Avatar, want)
	}
}

func TestUserAvatarProviderChangeAndOrganization(t *testing.T) {
	newTestDatabase(t)
	avatarSettings(t)
	u := &User{Name: "providerperson", Email: "Person@Example.Invalid", Passwd: "password", IsActive: true}
	if err := CreateUser(u); err != nil {
		t.Fatal(err)
	}
	org, err := CreateOrganization(&User{Name: "avatarorg", Email: "Org@Example.Invalid", Type: ORGANIZATION}, u)
	if err != nil {
		t.Fatal(err)
	}
	if org.Avatar != fmt.Sprintf("%x", sha256.Sum256([]byte("org@example.invalid"))) {
		t.Errorf("organization key %s", org.Avatar)
	}
	org.AvatarEmail = "Person@Example.Invalid"
	if err := UpdateUser(org); err != nil {
		t.Fatal(err)
	}
	setting.GravatarSource = setting.LegacyGravatarSource
	for _, person := range []*User{u, org} {
		if got := person.AvatarLink(); got != "https://gravatar.duoshuo.com/avatar/2da3d935e5683aa6ba2f6858d90d78a0" {
			t.Errorf("legacy URL %s", got)
		}
		persisted, err := GetUserById(person.Id)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Avatar != person.Avatar {
			t.Fatal("render mutated stored identifier")
		}
		if err := UpdateUser(person); err != nil {
			t.Fatal(err)
		}
		persisted, err = GetUserById(person.Id)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Avatar != "2da3d935e5683aa6ba2f6858d90d78a0" {
			t.Errorf("legacy persisted key %s", persisted.Avatar)
		}
	}
}

func TestUserAvatarUploadRefreshesKey(t *testing.T) {
	newTestDatabase(t)
	avatarSettings(t)
	oldPath := setting.AvatarUploadPath
	setting.AvatarUploadPath = t.TempDir()
	t.Cleanup(func() { setting.AvatarUploadPath = oldPath })
	u := &User{Name: "uploadperson", Email: "person@example.invalid", Passwd: "password"}
	if err := CreateUser(u); err != nil {
		t.Fatal(err)
	}
	setting.GravatarSource = setting.LegacyGravatarSource
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	if err := u.UploadAvatar(data.Bytes()); err != nil {
		t.Fatal(err)
	}
	saved, err := GetUserById(u.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.UseCustomAvatar || saved.Avatar != "2da3d935e5683aa6ba2f6858d90d78a0" {
		t.Errorf("uploaded avatar preference=%v key=%s", saved.UseCustomAvatar, saved.Avatar)
	}
}
