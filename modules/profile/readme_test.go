package profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/hg"
)

const testNode hg.NodeID = "1111111111111111111111111111111111111111"

func TestProfileReadmeBoundedRead(t *testing.T) {
	out := &boundedReadme{}
	source := io.LimitReader(strings.NewReader(strings.Repeat("x", 2*hg.MaxRenderedFileBytes)), 2*hg.MaxRenderedFileBytes)
	n, err := io.Copy(out, source)
	if !errors.Is(err, hg.ErrOutputLimit) || n != hg.MaxRenderedFileBytes+1 || out.Len() != hg.MaxRenderedFileBytes+1 {
		t.Fatalf("retained %d bytes, copied %d, err=%v", out.Len(), n, err)
	}
}

// The fake executable exercises the actual managed runner and file bounds.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "-R" {
		args := os.Args[3:]
		dir := filepath.Dir(os.Getenv("HGRCPATH"))
		read := func(name string) string { b, _ := os.ReadFile(filepath.Join(dir, name)); return string(b) }
		if read("fail") == args[0] {
			os.Exit(3)
		}
		switch args[0] {
		case "branches":
			fmt.Print(read("heads"))
		case "log":
			if args[len(args)-1] == "{node}" {
				fmt.Print(testNode)
			}
		case "manifest":
			fmt.Print(read("manifest"))
		case "cat":
			fmt.Print(read("content"))
		default:
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T, manifest, body string) (*ReadmeService, string) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(dir, "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"heads": fmt.Sprintf(`{"rev":1,"node":%q,"branch":"default","head":"1"}`, testNode), "manifest": manifest, "content": body} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".hg"), 0700); err != nil {
		t.Fatal(err)
	}
	resolve := func(context.Context, int64, int64, string) (int64, string, error) { return 7, repo, nil }
	return NewReadmeService(runner, resolve, testURL), dir
}

func testURL(id int64, node hg.NodeID, p string, raw bool) (string, error) {
	root := "/code/Alice/Alice"
	if node == "" {
		if p != "" || raw {
			return "", hg.ErrNotFound
		}
		return root, nil
	}
	action := "src"
	if raw {
		action = "raw"
	}
	return root + "/" + action + "/" + string(node) + "/" + p, nil
}

func TestProfileReadmeConvention(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, body, state, path string
		org                               bool
	}{
		{"user", "644   README.md\n", "# Welcome", "ready", "README.md", false},
		{"org", "644   profile/README.md\n", "# Team", "ready", "profile/README.md", true},
		{"wrong case", "644   readme.md\n", "hidden", "absent", "", false},
		{"org root", "644   README.md\n", "hidden", "absent", "", true},
		{"empty", "644   README.md\n", "", "absent", "", false},
		{"binary", "644   README.md\n", "a\x00b", "unavailable", "README.md", false},
		{"invalid UTF8", "644   README.md\n", "\xff", "unavailable", "README.md", false},
		{"symlink", "644 @ README.md\n", "target", "unavailable", "README.md", false},
		{"directory", "644   README.md/file\n", "target", "unavailable", "README.md", false},
		{"oversize", "644   README.md\n", strings.Repeat("x", hg.MaxRenderedFileBytes+1), "unavailable", "README.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := fixture(t, tc.manifest, tc.body)
			got, err := s.Load(context.Background(), 1, 2, "Alice", tc.org)
			if err != nil || got.State != tc.state || got.Path != tc.path {
				t.Fatalf("%+v %v", got, err)
			}
			if got.State == "absent" && (got.RepositoryID != 0 || got.SourceURL != "" || got.HTML != "") {
				t.Fatal("absent leaked identifiers")
			}
		})
	}
}

func TestProfileReadmeAuthorization(t *testing.T) {
	s, _ := fixture(t, "644   README.md\n", "private secret")
	s.resolve = func(context.Context, int64, int64, string) (int64, string, error) { return 0, "", nil }
	got, err := s.Load(context.Background(), 0, 2, "Alice", false)
	if err != nil || got.State != "absent" || got.RepositoryID != 0 || got.HTML != "" || got.SourceURL != "" {
		t.Fatalf("denial: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, _ = s.Load(ctx, 1, 2, "Alice", false)
	if got.HTML != "" || got.SourceURL != "" {
		t.Fatal("canceled content")
	}
}

func TestProfileReadmeCancellationAndConvention(t *testing.T) {
	s, _ := fixture(t, "644   README.md\n", "visible")
	original := s.resolve
	s.resolve = func(ctx context.Context, actor, owner int64, name string) (int64, string, error) {
		if actor != 12 || owner != 34 || name != "Alice" {
			t.Fatalf("resolution context %d %d %q", actor, owner, name)
		}
		return original(ctx, actor, owner, name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.contentURL = func(id int64, node hg.NodeID, p string, raw bool) (string, error) {
		if node == "" {
			cancel()
		}
		return testURL(id, node, p, raw)
	}
	got, err := s.Load(ctx, 12, 34, "Alice", false)
	if !errors.Is(err, context.Canceled) || got.HTML != "" || got.SourceURL != "" || got.RepositoryID != 0 {
		t.Fatalf("cancellation exposed data: %+v %v", got, err)
	}
}

func TestProfileReadmeRendering(t *testing.T) {
	body := "[sibling](guide.md?q=a#part) ![image](../images/a.png)\n\n<a href=\"other.md\">raw link</a><img src=\"image.png\" onerror=\"evil()\">\n\n[escape](../../secret) [meta](../.HG/store) [encoded](a%2fb) [reserved](a?path=evil) [bad](javascript:alert) [local](#local) [site](/help) [mail](mailto:a@example.invalid)\n\n #12\n\n<script>evil()</script>"
	s, _ := fixture(t, "644   profile/README.md\n", body)
	got, err := s.Load(context.Background(), 1, 2, "Alice", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/src/" + string(testNode) + "/profile/guide.md?q=a#part", "/raw/" + string(testNode) + "/images/a.png", "/src/" + string(testNode) + "/profile/other.md", "/raw/" + string(testNode) + "/profile/image.png", `href="#local"`, `href="/help"`, `href="mailto:a@example.invalid"`, `href="/code/Alice/Alice/issues/12"`} {
		if !strings.Contains(string(got.HTML), want) {
			t.Errorf("missing %q: %s", want, got.HTML)
		}
	}
	for _, bad := range []string{"javascript:", "onerror", "<script", "../../", ".HG/store", "path=evil", "a%2fb"} {
		if strings.Contains(string(got.HTML), bad) {
			t.Errorf("unsafe %q: %s", bad, got.HTML)
		}
	}
}

func TestProfileReadmeLifecycle(t *testing.T) {
	s, dir := fixture(t, "644   README.md\n", "first")
	got, err := s.Load(context.Background(), 1, 2, "Alice", false)
	if err != nil || !strings.Contains(string(got.HTML), "first") {
		t.Fatal(got, err)
	}
	os.WriteFile(filepath.Join(dir, "content"), []byte("second"), 0600)
	got, err = s.Load(context.Background(), 1, 2, "Alice", false)
	if err != nil || !strings.Contains(string(got.HTML), "second") {
		t.Fatal(got, err)
	}
	os.WriteFile(filepath.Join(dir, "fail"), []byte("branches"), 0600)
	got, err = s.Load(context.Background(), 1, 2, "Alice", false)
	if err == nil || got.State != "unavailable" || got.HTML != "" || got.SourceURL != "" {
		t.Fatal(got, err)
	}
	s.resolve = func(context.Context, int64, int64, string) (int64, string, error) {
		return 0, "", errors.New("storage private path")
	}
	got, err = s.Load(context.Background(), 1, 2, "Alice", false)
	if err == nil || got.HTML != "" || got.SourceURL != "" || got.RepositoryID != 0 {
		t.Fatal(got, err)
	}
}

func TestProfileReadmeURLBoundary(t *testing.T) {
	for _, bad := range []string{"../../outside", "../.hg/store", "a%2fb", "%2fescape", "%2f..%2f.hg/hgrc", "a%5cb", "a%00b", "a%ff", "a%zz", "a?%70ath=x", "a?path=x&path=y", "a?ref_kind=branch&ref_name=default", `a\b`} {
		if got, err := resolveContentURL(bad, "profile", 7, testNode, false, testURL); err == nil {
			t.Errorf("accepted %q as %q", bad, got)
		}
	}
	for _, tc := range []struct{ in, want string }{{"../images/a.png", "/src/" + string(testNode) + "/images/a.png"}, {"#a", "#a"}, {"/help", "/help"}, {"//example.invalid/a", "https://example.invalid/a"}, {"https://example.invalid/a", "https://example.invalid/a"}} {
		got, err := resolveContentURL(tc.in, "profile", 7, testNode, false, testURL)
		if err != nil || !strings.HasSuffix(got, tc.want) {
			t.Errorf("%q: %q %v", tc.in, got, err)
		}
	}
}
