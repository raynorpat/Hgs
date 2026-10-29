package cmd

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
)

// Seeding only: the browser uses the existing production router and normal login.
func seedProfileReadmeBrowser(t *testing.T, a *viewApp) {
	t.Helper()
	host := a.holder.Service()
	ctx := context.Background()
	team, err := models.GetUserByName("laboratory")
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		if err := host.Runner().Run(ctx, dir, args, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	picture := image.NewRGBA(image.Rect(0, 0, 960, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 960; x++ {
			picture.Set(x, y, color.RGBA{R: 30, G: uint8(90 + y/2), B: uint8(100 + x/8), A: 255})
		}
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, picture); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		owner      *models.User
		name, file string
		private    bool
	}{{a.alice, "alice", "README.md", false}, {team, ".github", "profile/README.md", true}} {
		repo, err := models.CreateHgRepository(ctx, host.Runner(), host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: spec.owner.Id, Name: spec.name, Private: spec.private})
		if err != nil {
			t.Fatal(err)
		}
		work := filepath.Join(a.root, "profile-work-"+spec.owner.Name)
		run("", "init", work)
		file := filepath.Join(work, filepath.FromSlash(spec.file))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		imagePath := "banner.png"
		if spec.private {
			imagePath = "../banner.png"
		}
		title := "Alice's laboratory"
		if spec.private {
			title = "Private organization introduction"
		}
		body := "# " + title + "\n\nA real repository introduction rendered from the pinned Mercurial landing revision.\n\n![Local laboratory banner](" + imagePath + ")\n\n## Current projects\n\n- Source browsing and thoughtful reviews\n- Small, understandable changes\n- [Read this introduction](README.md)\n\n```go\n// " + strings.Repeat("long code sample ", 28) + "\nfunc main() { println(\"hello from the laboratory\") }\n```\n\n| Project | Description | Status | Details |\n| --- | --- | --- | --- |\n| Laboratory | " + strings.Repeat("Wide table content ", 15) + " | Active | Local content stays in its panel |\n\n" + strings.Repeat("### Notes\n\nProfiles introduce the people and organizations behind the work. This paragraph checks comfortable line wrapping at desktop and mobile widths.\n\n", 4)
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, "banner.png"), pngBytes.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		run(work, "add")
		run(work, "commit", "-u", "Fixture", "-m", "profile README fixture")
		dest, err := models.RepositoryStoragePath(host.RepositoryRoot(), repo.Id)
		if err != nil {
			t.Fatal(err)
		}
		run(work, "push", dest)
	}
}
