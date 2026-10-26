package hosting

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

func TestHostingCloneLinks(t *testing.T) {
	s := &Service{}
	if err := s.configure(Config{PublicURL: "https://example.test/gogs/", AppSubURL: "/gogs", SSHAddress: ":2222", SSHAdvertisedHost: "2001:db8::1", SSHAdvertisedPort: 2222}); err != nil {
		t.Fatal(err)
	}
	links := s.CloneLinks("an owner", "my#project")
	if links.HTTPS != "https://example.test/gogs/hg/an%20owner/my%23project" || links.SSH != "ssh://hg@[2001:db8::1]:2222/an%20owner/my%23project" {
		t.Fatalf("%+v", links)
	}
	s.cfg.SSHAddress = ""
	if s.CloneLinks("a", "b").SSH != "" {
		t.Fatal("disabled SSH advertised")
	}
}
func TestHostingPartialConstructionFailure(t *testing.T) {
	s, _, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(ctx, s.cfg); err == nil {
		t.Fatal("canceled startup succeeded")
	}
	// Fail after the control listener opens, when stdio config is written.
	path := filepath.Join(filepath.Dir(s.cfg.Mercurial.ConfigPath), "stdio-reader.rc")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), s.cfg); err == nil {
		t.Fatal("invalid session config accepted")
	}
}
func TestHostingReservedName(t *testing.T) {
	if models.IsLegalName("hg") || models.IsLegalName(strings.ToUpper("hg")) {
		t.Fatal("transport namespace accepted as account name")
	}
}

func TestHostingRejectsMissingUnsupportedMercurial(t *testing.T) {
	root := t.TempDir()
	cfg := Config{PublicURL: "https://example.test", RepositoryRoot: filepath.Join(root, "repos"), HostKeyPath: filepath.Join(root, "host"), Mercurial: hg.Config{Executable: filepath.Join(root, "missing"), ConfigPath: filepath.Join(root, "hgrc")}}
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("missing Mercurial accepted")
	}
	exe, _ := os.Executable()
	cfg.Mercurial.Executable = exe
	if err := os.WriteFile(filepath.Join(root, "fake-version"), []byte(`[{"ver":"4.9"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("unsupported Mercurial accepted")
	}
	if _, err := os.Stat(cfg.HostKeyPath); !os.IsNotExist(err) {
		t.Fatalf("invalid Mercurial startup changed host key: %v", err)
	}
}
