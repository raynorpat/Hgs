package hg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCommonRoot(t *testing.T) {
	tests := []struct{ a, b, want string }{
		{"/srv/hg/repos", "/srv/hg/review", "/srv/hg"},
		{"/srv/hg", "/srv/hg/review", "/srv/hg"},
	}
	if runtime.GOOS == "windows" {
		tests = []struct{ a, b, want string }{
			{`C:\srv\hg\repos`, `C:\srv\hg\review`, `C:\srv\hg`},
			{`C:\Hg\A`, `c:\hg\B`, `C:\Hg`},
		}
	}
	for _, tt := range tests {
		got, err := CommonRoot(tt.a, tt.b)
		if err != nil || !strings.EqualFold(got, tt.want) {
			t.Errorf("CommonRoot(%q, %q) = %q, %v; want %q", tt.a, tt.b, got, err, tt.want)
		}
	}
	none := [][2]string{{"/srv/hg", "/opt/x"}}
	if runtime.GOOS == "windows" {
		none = [][2]string{{`C:\srv\hg`, `C:\opt\x`}, {`C:\a\b`, `D:\a\b`}}
	}
	for _, p := range none {
		if _, err := CommonRoot(p[0], p[1]); !errors.Is(err, ErrNoCommonRoot) {
			t.Errorf("CommonRoot(%q, %q) = %v; want ErrNoCommonRoot", p[0], p[1], err)
		}
	}
}

func TestCommonRootFollowsSymlinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(base, "store")
	if err := os.MkdirAll(filepath.Join(store, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "other", "link")
	if err := os.Symlink(store, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := CommonRoot(filepath.Join(link, "repos"), filepath.Join(store, "review"))
	if err != nil || got != store {
		t.Fatalf("CommonRoot = %q, %v; want %q", got, err, store)
	}
	c := NewReadClient(store)
	if rel, err := c.relative(filepath.Join(link, "repos", "a")); err != nil || rel != "repos/a" {
		t.Fatalf("relative through symlink = %q, %v", rel, err)
	}
}

func hgqlserveBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("GOGS_TEST_HGQLSERVE")
	if bin == "" {
		t.Skip("GOGS_TEST_HGQLSERVE is not set")
	}
	return bin
}

func startTestReadServer(t *testing.T) (*ReadServer, *ReadClient, string) {
	t.Helper()
	bin := hgqlserveBinary(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewReadClient(root)
	s, err := StartReadServer(context.Background(), bin, root, filepath.Join(t.TempDir(), "tok"), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(context.Background()) })
	return s, client, root
}

func TestStartMissingBinary(t *testing.T) {
	for _, bin := range []string{"", filepath.Join(t.TempDir(), "nope")} {
		root := t.TempDir()
		_, err := StartReadServer(context.Background(), bin, root, t.TempDir(), NewReadClient(root))
		if err == nil || !strings.Contains(err.Error(), "HGQLSERVE") {
			t.Errorf("binary %q: error %v lacks HGQLSERVE", bin, err)
		}
	}
}

func TestReadsWaitForReady(t *testing.T) {
	_, client, _ := startTestReadServer(t)
	var out struct {
		Typename string `json:"__typename"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), StartupTimeout)
	defer cancel()
	if err := client.Query(ctx, "Ready", "query Ready { __typename }", nil, &out); err != nil {
		t.Fatalf("query right after start: %v", err)
	}
}

func TestTokenFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	bin := hgqlserveBinary(t)
	root, dir := t.TempDir(), filepath.Join(t.TempDir(), "tok")
	s, err := StartReadServer(context.Background(), bin, root, dir, NewReadClient(root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	fi, err := os.Stat(filepath.Join(dir, "token"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v, %v", fi, err)
	}
}

func TestRestartAfterExit(t *testing.T) {
	s, client, _ := startTestReadServer(t)
	s.mu.Lock()
	s.child.kill(ErrStopped)
	s.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var out struct{}
		err := client.Query(context.Background(), "Ready", "query Ready { __typename }", nil, &out)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStopEndsChild(t *testing.T) {
	s, client, _ := startTestReadServer(t)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	var out struct{}
	if err := client.Query(context.Background(), "Ready", "query Ready { __typename }", nil, &out); !errors.Is(err, ErrReadService) {
		t.Fatalf("query after Stop = %v; want ErrReadService", err)
	}
}
