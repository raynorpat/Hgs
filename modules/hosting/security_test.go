package hosting

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/modules/hgbridge"
)

func TestHostingUntrustedForwardingStartsNoWorker(t *testing.T) {
	s, _, _ := fixture(t)
	r := httptest.NewRequest("GET", "http://example.test/?cmd=capabilities", nil)
	r.RemoteAddr = "192.0.2.7:80"
	r.SetBasicAuth("alice", "password")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.ServeRepository(w, r, "alice", "project")
	if w.Code != 403 {
		t.Fatalf("forged proxy: %d", w.Code)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Mercurial.ConfigPath), "worker-*"))
	if len(files) != 0 {
		t.Fatal("untrusted TLS assertion started worker")
	}
	cfg := s.cfg
	cfg.TrustedProxyCIDRs = []string{"192.0.2.0/24"}
	if err := s.configure(cfg); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.ServeRepository(w, r, "alice", "project")
	if w.Code != 200 {
		t.Fatalf("trusted TLS proxy failed: %d %s", w.Code, w.Body)
	}
}
func TestHostingShutdownJoinsAndStopsAdmissions(t *testing.T) {
	s, alice, repo := fixture(t)
	stopped, cancel := context.WithCancel(context.Background())
	release, err := s.Register(hgbridge.Grant{Principal: hgbridge.Principal{UserID: alice.Id}, RepoID: repo.Id}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, stop := context.WithCancel(context.Background())
	go func() { done <- s.Shutdown(ctx) }()
	stop()
	<-stopped.Done()
	select {
	case <-done:
		t.Fatal("shutdown returned before worker release")
	default:
	}
	release()
	if err := <-done; err == nil {
		t.Fatal("shutdown deadline not reported")
	}
	if _, err = s.Register(hgbridge.Grant{Principal: hgbridge.Principal{UserID: alice.Id}, RepoID: repo.Id}, func() {}); err == nil {
		t.Fatal("admission after shutdown")
	}
}
func TestHostingHostKeyErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := loadHostKey(root); err == nil {
		t.Fatal("directory key accepted")
	}
	path := filepath.Join(root, "bad")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHostKey(path); err == nil {
		t.Fatal("invalid key accepted")
	}
}
