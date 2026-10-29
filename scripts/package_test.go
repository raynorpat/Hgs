//go:build ignore

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var testBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gogs-package-tests-")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "gogs")
	if runtime.GOOS == "windows" {
		testBinary += ".exe"
	}
	c := exec.Command("go", "build", "-o", testBinary, "github.com/gogits/gogs")
	if out, err := c.CombinedOutput(); err != nil {
		panic(string(out) + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"public/css/site.css", "templates/.VERSION", "conf/app.ini", "conf/locale/TRANSLATORS.md", "LICENSE", "README.md", "README_ZH.md", "CONTRIBUTORS.md", "docs/mercurial/installation.md", "docs/mercurial/operations.md", "docs/mercurial/compatibility.md", "docs/mercurial/operator-notes.md", "docs/mercurial/transport-evidence.md", "docs/mercurial/removed-features.md", "docs/mercurial/build-baseline.md", "docs/mercurial/shared-ui-evidence.md", "docs/mercurial/review-operation-evidence.md"} {
		put(t, filepath.Join(root, name), name)
	}
	return root
}

func put(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// Blind root/conf recursion would leak these files; the archive is inspected,
// rather than asserting how the implementation spells its allowlist.
func TestPackageAllowlist(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"custom/conf/app.ini", "data/gogs.db", "repos/1/.hg/store/data", "conf/key.pem", "conf/private.ini", "public/img/avatar/private.image", "public/avatar/private.image", "public/img/avatar/hgs-maintenance.png", "public/avatar/hgs-error.png", ".git/config", "docs/mercurial/scratch.md", "docs/prototypes/status-pages/hgs-maintenance.png"} {
		put(t, filepath.Join(root, name), "SECRET-CANARY")
	}
	out := filepath.Join(root, "dist", "release.zip")
	if err := packageRelease(root, testBinary, out); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seen := map[string]bool{}
	for _, f := range r.File {
		seen[f.Name] = true
		if !f.Mode().IsRegular() || strings.Contains(f.Name, "\\") || strings.Contains(f.Name, "..") {
			t.Fatalf("unsafe entry %s", f.Name)
		}
		if strings.Contains(f.Name, "private") || strings.HasPrefix(f.Name, "public/img/avatar/") || strings.HasPrefix(f.Name, "public/avatar/") || strings.HasPrefix(f.Name, "docs/prototypes/") || strings.HasPrefix(f.Name, "custom/") || strings.HasPrefix(f.Name, "data/") || strings.HasPrefix(f.Name, "repos/") || strings.HasPrefix(f.Name, ".git/") || strings.HasPrefix(f.Name, "dist/") || strings.HasSuffix(f.Name, ".pem") || strings.HasSuffix(f.Name, "scratch.md") {
			t.Fatalf("leaked %s", f.Name)
		}
	}
	for _, name := range []string{filepath.Base(testBinary), "templates/.VERSION", "conf/app.ini", "conf/locale/TRANSLATORS.md", "LICENSE", "CONTRIBUTORS.md", "public/css/site.css", "docs/mercurial/operations.md"} {
		if !seen[name] {
			t.Errorf("missing %s", name)
		}
	}
	for _, f := range r.File {
		if f.Name == filepath.Base(testBinary) && f.Mode().Perm()&0111 == 0 {
			t.Error("binary lost executable permission")
		}
	}
	if err := packageRelease(root, testBinary, out); err == nil {
		t.Fatal("overwrote existing archive")
	}
}

// Missing/replaced artwork, dropped attribution, or prototype-only packaging
// must fail at the release boundary, using the exact approved PNG identities.
func TestHgsStatusAssets(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "status.zip")
	if err := packageRelease(root, testBinary, out); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	entries := map[string][]byte{}
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "docs/prototypes/") {
			t.Errorf("packaged prototype %s", f.Name)
		}
		if !strings.HasPrefix(f.Name, "public/img/hgs-") {
			continue
		}
		if _, exists := entries[f.Name]; exists {
			t.Fatalf("duplicate asset %s", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name] = body
	}
	for _, asset := range []struct {
		name string
		size int
		hash string
	}{
		{"hgs-maintenance.png", 1139822, "7aa33c7d02e15417481e3993bec1f50e46db32a1c6aef2bb1537cbc024a65f33"},
		{"hgs-error.png", 1325581, "bbe04093ade6c7a53965fcaef36bf14c268abdd3acd4f0aab985762a2104225c"},
		{"hgs-not-found.png", 1036863, "2ba57fcb17e12c5fe6fef26763ad3716e4f1c74a156c65c924d63986b79b970a"},
	} {
		t.Run(asset.name, func(t *testing.T) {
			name := "public/img/" + asset.name
			body, ok := entries[name]
			if !ok {
				t.Fatalf("missing local release asset %s", name)
			}
			if len(body) != asset.size || fmt.Sprintf("%x", sha256.Sum256(body)) != asset.hash {
				t.Fatal("release PNG differs from exact approved artwork")
			}
			source, err := os.ReadFile(filepath.Join(root, "docs/prototypes/status-pages", asset.name))
			if err != nil || !bytes.Equal(body, source) {
				t.Fatalf("release PNG differs from prototype source: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != 1218 || img.Bounds().Dy() != 1292 {
				t.Fatalf("incorrect decoded dimensions: %v", img.Bounds())
			}
			var transparent, opaque bool
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
					_, _, _, a := img.At(x, y).RGBA()
					transparent = transparent || a == 0
					opaque = opaque || a == 65535
				}
			}
			if !transparent || !opaque {
				t.Fatal("missing transparent background or opaque artwork")
			}
			t.Logf("exact approved PNG: %d bytes, SHA256 %s, decoded %dx%d, transparent background", len(body), asset.hash, img.Bounds().Dx(), img.Bounds().Dy())
		})
	}
	credit := entries["public/img/hgs-mascot.LICENSE.md"]
	for _, required := range []string{"Renee French", "https://creativecommons.org/licenses/by/4.0/", "https://go.dev/blog/gopher", "2026-10-26", "hgs-maintenance.png", "hgs-error.png", "hgs-not-found.png", "white laboratory coat", "does not imply endorsement"} {
		if !bytes.Contains(credit, []byte(required)) {
			t.Errorf("release attribution missing %q", required)
		}
	}
	prompts, err := os.ReadFile(filepath.Join(root, "docs/prototypes/status-pages/PROMPTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(credit, prompts) {
		t.Error("release metadata did not retain exact status illustration prompts/provenance")
	}
}

func TestPackageRejectsUnsafeBoundaries(t *testing.T) {
	for _, name := range []string{"public/output.zip", "conf/app.ini", "templates/output.zip", "docs/mercurial/output.zip"} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			if err := packageRelease(root, testBinary, filepath.Join(root, name)); err == nil {
				t.Fatal("accepted output in an input tree")
			}
		})
	}
	t.Run("secret as executable", func(t *testing.T) {
		root := fixture(t)
		secret := filepath.Join(root, "key.pem")
		put(t, secret, "private key")
		if err := packageRelease(root, secret, filepath.Join(root, "release.zip")); err == nil {
			t.Fatal("accepted a non-application binary")
		}
	})
	t.Run("binary overwritten", func(t *testing.T) {
		if err := packageRelease(fixture(t), testBinary, testBinary); err == nil {
			t.Fatal("accepted binary as output")
		}
	})
	t.Run("missing version asset", func(t *testing.T) {
		root := fixture(t)
		os.Remove(filepath.Join(root, "templates/.VERSION"))
		out := filepath.Join(root, "release.zip")
		if err := packageRelease(root, testBinary, out); err == nil {
			t.Fatal("accepted incomplete assets")
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("left partial archive")
		}
	})
	for _, name := range []string{"public/link", "templates", "conf/locale", "public/key.pem", "public/.hg/config", "public/python313.dll", "public/credentials.txt", "templates/operator.ini"} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			if strings.HasSuffix(name, ".pem") || strings.Contains(name, ".hg") || strings.HasSuffix(name, ".dll") || strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".ini") {
				put(t, filepath.Join(root, name), "secret")
			} else {
				target := t.TempDir()
				os.RemoveAll(filepath.Join(root, name))
				if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if err := packageRelease(root, testBinary, filepath.Join(root, "release.zip")); err == nil {
				t.Fatal("accepted unsafe source")
			}
		})
	}
}

// SkipDir on a non-directory skips sibling assets; it must not let a malformed
// cache entry silently truncate the public/img archive traversal.
func TestPackageRejectsNonDirectoryCache(t *testing.T) {
	root := fixture(t)
	put(t, filepath.Join(root, "public/img/avatar"), "not a directory")
	put(t, filepath.Join(root, "public/img/avatar_default.jpg"), "default image")
	if err := packageRelease(root, testBinary, filepath.Join(root, "release.zip")); err == nil {
		t.Fatal("accepted non-directory cache and skipped sibling image")
	}
}

func TestPackageCurrentDocumentationLayout(t *testing.T) {
	root := fixture(t)
	if err := os.Remove(filepath.Join(root, "README_ZH.md")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "release.zip")
	if err := packageRelease(root, testBinary, out); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seen := map[string]bool{}
	for _, f := range r.File {
		seen[f.Name] = true
	}
	if !seen["README.md"] || !seen["conf/locale/TRANSLATORS.md"] || seen["README_ZH.md"] {
		t.Fatal("current documentation or attribution layout not preserved")
	}
}

func TestPackageRequiresTranslatorAttribution(t *testing.T) {
	root := fixture(t)
	if err := os.Remove(filepath.Join(root, "conf/locale/TRANSLATORS.md")); err != nil {
		t.Fatal(err)
	}
	if err := packageRelease(root, testBinary, filepath.Join(root, "release.zip")); err == nil {
		t.Fatal("packaged without translator attribution")
	}
}

func TestPackageSharedUIAssetsAndEvidence(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "shared-ui.zip")
	if err = packageRelease(root, testBinary, out); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	seen := map[string]int{}
	for _, f := range archive.File {
		seen[f.Name]++
	}
	for name := range seen {
		if strings.HasSuffix(name, "_test.cjs") {
			t.Errorf("packaged test source %s", name)
		}
	}
	for _, name := range []string{"public/css/app.css", "public/js/app/shell.js", "public/js/app/forms.js", "public/js/app/markdown.js", "public/js/app/accounts.js", "public/js/app/repository.js", "public/js/app/prettify.js", "public/js/app/issues.js", "public/js/app/fork.js", "public/js/app/pulls.js", "public/img/ui-icons.svg", "templates/shared/head.tmpl", "conf/locale/locale_en-US.ini", "conf/locale/locale_de-DE.ini", "docs/mercurial/shared-ui-evidence.md", "docs/mercurial/review-operation-evidence.md"} {
		if seen[name] != 1 {
			t.Errorf("package %s count=%d", name, seen[name])
		}
	}
}

func TestLandingAssetPackaging(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "landing.zip")
	if err := packageRelease(root, testBinary, out); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	seen := map[string]int{}
	for _, f := range z.File {
		seen[f.Name]++
	}
	for _, name := range []string{"public/img/hgs-mascot.png", "public/img/hgs-mascot.LICENSE.md", "public/img/hgs-beaker.svg", "public/img/hgs-beaker-inverse.svg", "public/img/favicon.png", "public/img/favicon-16.png", "public/img/favicon-32.png", "public/img/favicon.ico"} {
		if seen[name] != 1 {
			t.Errorf("archive %s count=%d", name, seen[name])
		}
	}
	for _, name := range []string{"public/img/unapproved.md", "public/img/unapproved.ico"} {
		f := fixture(t)
		put(t, filepath.Join(f, filepath.FromSlash(name)), "not approved")
		if err := packageRelease(f, testBinary, filepath.Join(t.TempDir(), "bad.zip")); err == nil {
			t.Errorf("accepted arbitrary asset %s", name)
		}
	}
}
