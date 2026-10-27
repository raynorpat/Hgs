package hosting

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/setting"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(fakeHostingHg(os.Args[1:]))
	}
	os.Exit(m.Run())
}
func fakeHostingHg(args []string) int {
	if args[0] == "-R" {
		args = args[2:]
	}
	for len(args) > 1 && args[0] == "--config" {
		args = args[2:]
	}
	switch args[0] {
	case "version":
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("HGRCPATH")), "fake-version")); err == nil {
			fmt.Print(string(data))
		} else {
			fmt.Print(`[{"ver":"7.2.4"}]`)
		}
	case "help":
		fmt.Print(`"pretxnopen" "pretxnclose" "pretxnclose-bookmark" "pretxnclose-phase" "prepushkey"`)
	case "showconfig":
		fmt.Print(`[]`)
	case "log":
		fixtureName := "fake-review-references"
		if len(args) == 5 && strings.Contains(args[4], "p1node") {
			fixtureName = "fake-review-result"
		}
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("HGRCPATH")), fixtureName)); err == nil {
			if string(data) == "EXIT" {
				fmt.Fprint(os.Stderr, "abort: injected result read failure")
				return 255
			}
			fmt.Print(string(data))
			return 0
		}
		fmt.Printf(`[{"node":"%s"}]`, strings.Repeat("0", 40))
	case "init":
		if trace := os.Getenv("GOGS_HG_TEST_TRACE"); trace != "" {
			cwd, _ := os.Getwd()
			os.WriteFile(trace, []byte(fmt.Sprintf("%d\n%s", os.Getpid(), cwd)), 0600)
		}
		if err := os.MkdirAll(filepath.Join(args[1], ".hg", "store"), 0700); err != nil {
			return 1
		}
		if err := os.WriteFile(filepath.Join(args[1], ".hg", "requires"), []byte("revlogv1\nstore\n"), 0600); err != nil {
			return 1
		}
	case "serve":
		marker, err := os.CreateTemp(filepath.Dir(os.Getenv("HGRCPATH")), "worker-")
		if err != nil {
			return 1
		}
		marker.Close()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 1
		}
		fmt.Printf("listening at http://localhost/ (bound to %s)\n", ln.Addr())
		http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				http.Error(w, "credential leak", 500)
				return
			}
			if r.URL.Query().Get("cmd") == "getbundle" {
				chunk := make([]byte, 32<<10)
				for i := 0; i < 8192; i++ {
					if _, err := w.Write(chunk); err != nil {
						return
					}
				}
				return
			}
			fmt.Fprint(w, "lookup\n")
		}))
	default:
		return 1
	}
	return 0
}
func fixture(t *testing.T) (*Service, *models.User, *models.Repository) {
	return fixtureConfig(t, nil)
}
func fixtureConfig(t *testing.T, configure func(*Config)) (*Service, *models.User, *models.Repository) {
	t.Helper()
	root := t.TempDir()
	oldDb, oldLog, oldRequire := models.DbCfg, setting.LogRootPath, setting.Service.RequireSignInView
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(root, "test.db"))
	setting.LogRootPath = root
	setting.Service.RequireSignInView = false
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg = oldDb
		setting.LogRootPath = oldLog
		setting.Service.RequireSignInView = oldRequire
	})
	user := &models.User{Name: "alice", Email: "alice@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	cfg := Config{Mercurial: hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "config", "hgrc")}, RepositoryRoot: filepath.Join(root, "repos"), HostKeyPath: filepath.Join(root, "keys", "host"), PublicURL: "https://example.test"}
	if configure != nil {
		configure(&cfg)
	}
	s, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})
	repo, err := models.CreateHgRepository(context.Background(), s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: user.Id, Name: "project", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, user, repo
}
func TestHostingStartsWithoutGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s, _, _ := fixture(t)
	if s.Runner() == nil || s.RepositoryRoot() == "" {
		t.Fatal("missing shared runner")
	}
}
func TestHostingPrivateRepositoryDoesNotLeak(t *testing.T) {
	s, _, _ := fixture(t)
	for _, authenticated := range []bool{false, true} {
		var statuses []int
		for _, name := range []string{"project", "missing"} {
			r := httptest.NewRequest("GET", "https://example.test/?cmd=capabilities", nil)
			if authenticated {
				r.SetBasicAuth("alice", "wrong")
			}
			w := httptest.NewRecorder()
			s.ServeRepository(w, r, "alice", name)
			statuses = append(statuses, w.Code)
		}
		if statuses[0] != 401 || statuses[1] != 401 {
			t.Fatalf("leak: %v", statuses)
		}
	}
	stranger := &models.User{Name: "bob", Email: "bob@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(stranger); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"project", "missing"} {
		r := httptest.NewRequest("GET", "https://example.test/?cmd=capabilities", nil)
		r.SetBasicAuth("bob", "password")
		w := httptest.NewRecorder()
		s.ServeRepository(w, r, "alice", name)
		if w.Code != 404 {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "https://example.test/?cmd=capabilities", nil)
	r.AddCookie(&http.Cookie{Name: "i_like_gogits", Value: "alice"})
	w := httptest.NewRecorder()
	s.ServeRepository(w, r, "alice", "project")
	if w.Code != 401 {
		t.Fatalf("browser cookie authorized: %d", w.Code)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Mercurial.ConfigPath), "worker-*"))
	if len(files) != 0 {
		t.Fatal("unauthorized worker started")
	}
}
func TestHostingReadyIDStorageAndCredentials(t *testing.T) {
	s, user, repo := fixture(t)
	target, g, err := s.resolve(context.Background(), passwordPrincipal(t, user), "alice", "project")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(target.Path) != fmt.Sprint(repo.Id) || !g.CanWrite {
		t.Fatalf("target=%+v grant=%+v", target, g)
	}
	r := httptest.NewRequest("GET", "https://example.test/?cmd=capabilities", nil)
	r.SetBasicAuth("alice", "password")
	r.AddCookie(&http.Cookie{Name: "secret", Value: "browser"})
	w := httptest.NewRecorder()
	s.ServeRepository(w, r, "alice", "project")
	if w.Code != 200 {
		t.Fatalf("authorized request: %d %s", w.Code, w.Body)
	}
	if err := models.DeleteHgRepository(context.Background(), s.RepositoryRoot(), repo.Id, func(context.Context, int64) error { return errors.New("test drain interrupted") }); err == nil {
		t.Fatal("expected interrupted deletion")
	}

	if _, _, err = s.resolve(context.Background(), passwordPrincipal(t, user), "alice", "project"); err == nil {
		t.Fatal("deleting repository resolved")
	}
}
func TestHostingMonitorAnonymousAndRevocation(t *testing.T) {
	s, user, repo := fixture(t)
	repo.IsPrivate = false
	if err := models.UpdateRepository(repo); err != nil {
		t.Fatal(err)
	}
	canceled := make(chan struct{})
	release, err := s.Register(hgbridge.Grant{RepoID: repo.Id}, func() {
		select {
		case <-canceled:
		default:
			close(canceled)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	s.sessions.check()
	select {
	case <-canceled:
		t.Fatal("anonymous public reader was canceled")
	default:
	}
	repo.IsPrivate = true
	if err := models.UpdateRepository(repo); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not enforce changed visibility within bound")
	}
	principal := passwordPrincipal(t, user)
	writerCanceled := make(chan struct{})
	writerRelease, err := s.Register(hgbridge.Grant{Principal: principal, RepoID: repo.Id, CanWrite: true}, func() {
		select {
		case <-writerCanceled:
		default:
			close(writerCanceled)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writerRelease()
	user.IsActive = false
	if err := models.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	if err := s.authorize(context.Background(), hgbridge.Grant{Principal: principal, RepoID: repo.Id, CanWrite: true}); err == nil {
		t.Fatal("stale writer hook allowed")
	}
	select {
	case <-writerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked writer not canceled within bound")
	}
}
func TestHostingHostKeyPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host")
	a, err := loadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(a.PublicKey()) != ssh.FingerprintSHA256(b.PublicKey()) {
		t.Fatal("fingerprint changed")
	}
	if a.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatal("wrong key type")
	}
}

func TestHostingMonitorKeyTokenMetadataAndOriginalGrant(t *testing.T) {
	s, alice, repo := fixture(t)
	token := &models.AccessToken{Uid: alice.Id, Name: "test"}
	if err := models.NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	add := func(g hgbridge.Grant) (func(), <-chan struct{}) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		release, err := s.Register(g, cancel)
		if err != nil {
			t.Fatal(err)
		}
		return release, ctx.Done()
	}
	grant := hgbridge.Grant{Principal: hgbridge.Principal{UserID: alice.Id, TokenID: token.Id}, RepoID: repo.Id, CanWrite: true}
	release, canceled := add(grant)
	if err := models.DeleteAccessTokenById(token.Id); err != nil {
		t.Fatal(err)
	}
	if err := s.authorize(context.Background(), grant); err == nil {
		t.Fatal("revoked token hook allowed")
	}
	s.sessions.check()
	// CancelFunc may be called more than once for a tracked revoked entry.
	s.sessions.check()
	select {
	case <-canceled:
	default:
		t.Fatal("revoked token session retained")
	}
	release()
	// Metadata changes cancel even when the owner keeps effective access.
	grant.Principal = passwordPrincipal(t, alice)
	release, canceled = add(grant)
	repo.IsPrivate = false
	if err := models.UpdateRepository(repo); err != nil {
		t.Fatal(err)
	}
	s.sessions.check()
	select {
	case <-canceled:
	default:
		t.Fatal("visibility change retained session")
	}
	release()
	bob := &models.User{Name: "bob", Email: "bob@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(bob); err != nil {
		t.Fatal(err)
	}
	reader := hgbridge.Grant{Principal: passwordPrincipal(t, bob), RepoID: repo.Id}
	release, canceled = add(reader)
	if err := models.AddAccess(&models.Access{UserId: bob.Id, RepoId: repo.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if err := s.authorize(context.Background(), reader); err == nil {
		t.Fatal("reader grant elevated after permission increase")
	}
	s.sessions.check()
	select {
	case <-canceled:
		t.Fatal("valid reader canceled after permission increase")
	default:
	}
	release()
	grant = hgbridge.Grant{Principal: passwordPrincipal(t, bob), RepoID: repo.Id, CanWrite: true}
	release, canceled = add(grant)
	if err := models.DeleteAccess(&models.Access{UserId: bob.Id, RepoId: repo.Id}); err != nil {
		t.Fatal(err)
	}
	if err := s.authorize(context.Background(), grant); err == nil {
		t.Fatal("write revoked hook allowed")
	}
	s.sessions.check()
	select {
	case <-canceled:
	default:
		t.Fatal("revoked writer retained public read session")
	}
	release()
}

func TestHostingMonitorKeyAndOwnershipChanges(t *testing.T) {
	s, alice, repo := fixture(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := &models.PublicKey{OwnerId: alice.Id, Name: "test", Content: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
	if err = models.AddPublicKey(key); err != nil {
		t.Fatal(err)
	}
	grant := hgbridge.Grant{Principal: hgbridge.Principal{UserID: alice.Id, KeyID: key.Id}, RepoID: repo.Id, CanWrite: true}
	canceled, cancel := context.WithCancel(context.Background())
	release, err := s.Register(grant, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if err = models.DeletePublicKey(key); err != nil {
		t.Fatal(err)
	}
	if err = s.authorize(context.Background(), grant); err == nil {
		t.Fatal("revoked key hook authorized")
	}
	s.sessions.check()
	select {
	case <-canceled.Done():
	default:
		t.Fatal("revoked key retained session")
	}
	release()
	grant.Principal = passwordPrincipal(t, alice)
	canceled, cancel = context.WithCancel(context.Background())
	release, err = s.Register(grant, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	bob := &models.User{Name: "bob", Email: "bob@example.test", Passwd: "password", IsActive: true}
	if err = models.CreateUser(bob); err != nil {
		t.Fatal(err)
	}
	if err = models.TransferOwnership(alice, bob.Name, repo); err != nil {
		t.Fatal(err)
	}
	s.sessions.check()
	select {
	case <-canceled.Done():
	default:
		t.Fatal("ownership change retained session")
	}
}

func passwordPrincipal(t *testing.T, user *models.User) hgbridge.Principal {
	t.Helper()
	identity, err := models.AuthenticateRepositoryHTTPSIdentity(user.Name, "password")
	if err != nil {
		t.Fatal(err)
	}
	return hgbridge.Principal{UserID: identity.UserID, TokenID: identity.TokenID, PasswordVersion: identity.PasswordVersion}
}
