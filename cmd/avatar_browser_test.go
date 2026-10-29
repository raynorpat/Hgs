package cmd

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/avatar"
	"github.com/gogits/gogs/modules/setting"
)

// Seeding only. The existing production router serves all page and image paths,
// and the browser signs in normally. No external image fetch is needed.
func seedAvatarBrowser(t *testing.T, a *viewApp, writer *models.User, mode string) {
	t.Helper()
	oldSource, oldCache, oldDisabled, oldStatic := setting.GravatarSource, setting.Service.EnableCacheAvatar, setting.DisableGravatar, setting.StaticRootPath
	t.Cleanup(func() {
		setting.GravatarSource, setting.Service.EnableCacheAvatar, setting.DisableGravatar, setting.StaticRootPath = oldSource, oldCache, oldDisabled, oldStatic
	})
	setting.GravatarSource = setting.OfficialGravatarSource
	setting.Service.EnableCacheAvatar = true
	setting.DisableGravatar = mode == "disabled"
	assets := filepath.Join(t.TempDir(), "assets")
	for _, name := range []string{"public", "templates"} {
		if err := os.CopyFS(filepath.Join(assets, name), os.DirFS(filepath.Join(oldStatic, name))); err != nil {
			t.Fatal(err)
		}
	}
	setting.StaticRootPath = assets
	cache := filepath.Join(assets, "public", "img", "avatar")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	team, err := models.GetUserByName("laboratory")
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range []*models.User{a.alice, a.bob, writer, team} {
		pic := image.NewRGBA(image.Rect(0, 0, 200, 200))
		for y := 0; y < 200; y++ {
			for x := 0; x < 200; x++ {
				pic.Set(x, y, color.RGBA{uint8(30 + i*50), uint8(80 + x/2), uint8(140 + y/3), 255})
			}
		}
		var data bytes.Buffer
		if err := png.Encode(&data, pic); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, avatar.ProviderHash(u.Email)+".image"), data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if u == a.alice {
			if err := u.UploadAvatar(data.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		if u == writer {
			u.UseCustomAvatar = true
		} // No per-ID upload exists: browser receives native 404 and fires image error.
		if err := models.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	empty := &models.User{Name: "emptyavatar", Email: "", Passwd: "password", IsActive: true}
	if err := models.CreateUser(empty); err != nil {
		t.Fatal(err)
	}
	issue, err := models.GetIssueByIndex(a.repo.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateComment(a.bob.Id, a.repo.Id, issue.Id, models.COMMENT_TYPE_COMMENT, "Cached avatar comment", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateComment(writer.Id, a.repo.Id, issue.Id, models.COMMENT_TYPE_COMMENT, "Missing upload avatar comment", nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("AVATAR_FIXTURE mode=%s; alice=valid custom upload; bob=seeded official cache; writer=selected missing upload/native404; emptyavatar=empty email; organization=seeded cache; profile /code/{alice,bob,writer,emptyavatar}; issue /code/alice/project/issues/1; PR /code/alice/project/pulls/1 author=alice/comment=bob/reviewer=writer; normal login password=password; owned assets=%s; auto stop8m", mode, assets)
}
