//go:build integration

package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
	"golang.org/x/crypto/ssh"
)

// application owns an actual configured web executable, SQLite database and
// native transport clients. Authorization comes only from the shared database.
// GOGS_TEST_BINARY + GOGS_TEST_ASSETS permit an extracted package to use this
// fixture without changing the application or adding production test APIs.
type application struct {
	t                                                                   *testing.T
	root, binary, config, repoRoot, address, sshAddress, known, cacerts string
	client, inspect                                                     *hgCmd
	server                                                              *hg.Runner
	child                                                               *hg.Child
	lastCleanStop                                                       bool
	direct                                                              bool
	output                                                              sync.WaitGroup
	users                                                               map[string]*models.User
	keys                                                                map[string]string
	keyIDs                                                              map[string]int64
	repos                                                               map[string]*models.Repository
}

func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}

func newApplication(t *testing.T, v versions) *application {
	t.Helper()
	a := &application{t: t, root: t.TempDir(), users: map[string]*models.User{}, keys: map[string]string{}, keyIDs: map[string]int64{}, repos: map[string]*models.Repository{}}
	a.address, a.sshAddress = freeAddress(t), freeAddress(t)
	assets := os.Getenv("GOGS_TEST_ASSETS")
	if os.Getenv("GOGS_TEST_BINARY") != "" && assets == "" {
		t.Fatal("GOGS_TEST_BINARY requires explicit GOGS_TEST_ASSETS; packaged acceptance must not fall back to checkout assets")
	}
	if assets == "" {
		assets, _ = filepath.Abs("../..")
	}
	a.direct = os.Getenv("GOGS_TEST_PACKAGE_DIRECT") == "1"
	app := filepath.Join(a.root, "app")
	if err := os.MkdirAll(app, 0700); err != nil {
		t.Fatal(err)
	}
	var err error
	if a.direct {
		a.binary, err = filepath.Abs(gogsExe)
		if err != nil {
			t.Fatal(err)
		}
		assets, err = filepath.Abs(assets)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("GOGS_TEST_BINARY") == "" || filepath.Dir(a.binary) != assets {
			t.Fatal("direct package requires binary and assets in the same extracted directory")
		}
	} else {
		a.binary = filepath.Join(app, filepath.Base(gogsExe))
		b, err := os.ReadFile(gogsExe)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(a.binary, b, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.CopyFS(filepath.Join(app, "conf"), os.DirFS(filepath.Join(assets, "conf"))); err != nil {
			t.Fatal(err)
		}
	}
	cert, certPath := localhostCert(t, a.root)
	a.cacerts = certPath
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(a.root, "tls-key.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	a.repoRoot = filepath.Join(a.root, "repos")
	oldDB, oldLog, oldRoot := models.DbCfg, setting.LogRootPath, setting.RepoRootPath
	models.DbCfg.Type, models.DbCfg.Path = "sqlite3", filepath.ToSlash(filepath.Join(a.root, "application.db"))
	setting.LogRootPath, setting.RepoRootPath = filepath.Join(a.root, "model-log"), a.repoRoot
	if err = models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.stop()
		models.CloseEngine()
		models.DbCfg, setting.LogRootPath, setting.RepoRootPath = oldDB, oldLog, oldRoot
	})
	// The first administrator must not accidentally be an acceptance principal.
	for _, name := range []string{"siteadmin", "owner", "writer", "reader", "stranger"} {
		u := &models.User{Name: name, Email: name + "@example.test", Passwd: "password", IsActive: true}
		if err = models.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		a.users[name] = u
		path := filepath.Join(a.root, name+"-key")
		signer := writeKey(t, path)
		a.keys[name] = path
		stored := &models.PublicKey{OwnerId: u.Id, Name: "acceptance", Content: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
		if err = models.AddPublicKey(stored); err != nil {
			t.Fatal(err)
		}
		a.keyIDs[name] = stored.Id
	}
	r := newHgCmd(t, v.server, filepath.Join(a.root, "provision"), 2)
	for _, visibility := range []string{"public", "private"} {
		repo, err := models.CreateHgRepository(context.Background(), r.r, a.repoRoot, models.CreateHgRepositoryOptions{OwnerID: a.users["owner"].Id, Name: visibility, Private: visibility == "private"})
		if err != nil {
			t.Fatal(err)
		}
		a.repos[visibility] = repo
		for _, role := range []string{"writer", "reader"} {
			mode := models.READABLE
			if role == "writer" {
				mode = models.WRITABLE
			}
			if err = models.AddAccess(&models.Access{UserId: a.users[role].Id, RepoId: repo.Id, Mode: mode}); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, httpPort, _ := net.SplitHostPort(a.address)
	_, sshPort, _ := net.SplitHostPort(a.sshAddress)
	a.config = filepath.Join(a.root, "app.ini")
	runUser := os.Getenv("USER")
	if runUser == "" {
		runUser = os.Getenv("USERNAME")
	}
	config := fmt.Sprintf(`RUN_MODE = test
RUN_USER = %s
[server]
PROTOCOL = https
HTTP_ADDR = 127.0.0.1
HTTP_PORT = %s
ROOT_URL = https://localhost:%s/
STATIC_ROOT_PATH = %s
CERT_FILE = %s
KEY_FILE = %s
SSH_ADDRESS = %s
SSH_HOST = 127.0.0.1
SSH_PORT = %s
SSH_HOST_KEY_PATH = %s
OFFLINE_MODE = true
[database]
DB_TYPE = sqlite3
PATH = %s
[repository]
ROOT = %s
[security]
INSTALL_LOCK = true
SECRET_KEY = acceptance-local-secret
[mercurial]
EXECUTABLE = %s
CONFIG_PATH = %s
MAX_PROCESSES = 2
SESSION_TIMEOUT = 2
[session]
PROVIDER = memory
[log]
MODE = console
ROOT_PATH = %s
`, runUser, httpPort, httpPort, filepath.ToSlash(assets), filepath.ToSlash(certPath), filepath.ToSlash(keyPath), a.sshAddress, sshPort, filepath.ToSlash(filepath.Join(a.root, "hostkey")), models.DbCfg.Path, filepath.ToSlash(a.repoRoot), filepath.ToSlash(v.server), filepath.ToSlash(filepath.Join(a.root, "managed", "hgrc")), filepath.ToSlash(filepath.Join(a.root, "log")))
	if a.direct {
		config = strings.Replace(config, "PROVIDER = memory", "PROVIDER = file\nPROVIDER_CONFIG = "+filepath.ToSlash(filepath.Join(a.root, "sessions")), 1)
	}
	if err = os.WriteFile(a.config, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	helper, _ := os.Executable()
	a.server, err = hg.NewRunner(hg.Config{Executable: helper, ConfigPath: filepath.Join(a.root, "supervisor", "hgrc"), SessionTimeout: 30 * time.Minute, IdleTimeout: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	a.client = newHgCmd(t, v.client, filepath.Join(a.root, "client"), 4)
	a.inspect = newHgCmd(t, hgPath(t), filepath.Join(a.root, "inspect"), 4)
	a.start(t)
	hostKey, err := os.ReadFile(filepath.Join(a.root, "hostkey"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	a.known = filepath.Join(a.root, "known_hosts")
	if err = os.WriteFile(a.known, []byte("[127.0.0.1]:"+sshPort+" "+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return a
}

func (a *application) start(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(a.root, "application.pid")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(a.address)
	applicationEnv := os.Environ()
	if a.direct {
		// Server runtime has no Git or SSH utilities on PATH. Native test
		// clients retain their separate environment and explicit SSH path.
		for k, v := range map[string]string{"PATH": os.Getenv("GOGS_TEST_SERVER_PATH"), "HOME": filepath.Join(a.root, "operator-home"), "USERPROFILE": filepath.Join(a.root, "operator-home"), "GOGS_CUSTOM": filepath.Join(a.root, "custom")} {
			applicationEnv = replaceEnv(applicationEnv, k, v)
			if k == "PATH" {
				continue
			}
			if err := os.MkdirAll(v, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	env, _ := json.Marshal(applicationEnv)
	c, err := a.server.Start(context.Background(), "", []string{"-test.run=^TestApplicationProcessChild$"}, map[string]string{"GOGS_HG_APPLICATION_BINARY": a.binary, "GOGS_HG_APPLICATION_CWD": a.root, "GOGS_HG_APPLICATION_CONFIG": a.config, "GOGS_HG_APPLICATION_PORT": port, "GOGS_HG_APPLICATION_ENV": string(env)})
	if err != nil {
		t.Fatal(err)
	}
	a.child = c
	f, err := os.OpenFile(filepath.Join(a.root, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	a.output.Add(1)
	go func() {
		defer a.output.Done()
		defer f.Close()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); io.Copy(f, c.Stdout) }()
		go func() { defer wg.Done(); io.Copy(f, c.Stderr) }()
		wg.Wait()
	}()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("https://" + a.address + "/user/login")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.stop()
	b, _ := os.ReadFile(filepath.Join(a.root, "server.log"))
	t.Fatalf("configured web process did not become ready:\n%s", b)
}

// Keep the native runner's process-tree ownership while restoring the normal
// application environment. Mercurial's deliberately narrow environment is not
// the web application's runtime contract (USER/HOME are needed at startup).
func TestApplicationProcessChild(t *testing.T) {
	binary := os.Getenv("GOGS_HG_APPLICATION_BINARY")
	if binary == "" {
		return
	}
	c := exec.Command(binary, "web", "--config", os.Getenv("GOGS_HG_APPLICATION_CONFIG"), "--port", os.Getenv("GOGS_HG_APPLICATION_PORT"))
	if err := json.Unmarshal([]byte(os.Getenv("GOGS_HG_APPLICATION_ENV")), &c.Env); err != nil {
		t.Fatal(err)
	}
	c.Dir = os.Getenv("GOGS_HG_APPLICATION_CWD")
	configureApplicationProcess(c)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("GOGS_HG_APPLICATION_CONFIG")), "application.pid"), []byte(strconv.Itoa(c.Process.Pid)), 0600); err != nil {
		c.Process.Kill()
		c.Wait()
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
}
func (a *application) stop() {
	if a.child == nil {
		return
	}
	pidBytes, _ := os.ReadFile(filepath.Join(a.root, "application.pid"))
	pid, _ := strconv.Atoi(string(pidBytes))
	graceful := signalApplicationStop(pid, a.binary, a.config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if !graceful {
		cancel()
	}
	stopErr := a.child.Stop(ctx)
	a.lastCleanStop = graceful && stopErr == nil
	a.t.Logf("application stop: signal=%t clean-exit=%t (%v)", graceful, a.lastCleanStop, stopErr)
	cancel()
	a.output.Wait()
	a.child = nil
	if killed, err := reapApplicationOrphans(a.binary, a.repoRoot); err != nil || len(killed) != 0 {
		a.t.Errorf("application required orphan cleanup: %v %v", killed, err)
	}
	if left := waitNoProcesses(a.binary, a.repoRoot); len(left) != 0 {
		a.t.Errorf("owned process tree survived: %v", left)
	}
}
func (a *application) remote(t *testing.T, transport, user, repo string) (string, []string) {
	if transport == transportSSH {
		return "ssh://hg@" + a.sshAddress + "/owner/" + repo, sshCommand(t, a.keys[user], a.known)
	}
	_, port, _ := net.SplitHostPort(a.address)
	u := &url.URL{Scheme: "https", Host: "localhost:" + port, Path: "/hg/owner/" + repo}
	if user != "anonymous" {
		u.User = url.UserPassword(user, "password")
	}
	return u.String(), []string{"--config", "web.cacerts=" + a.cacerts, "--config", "http_proxy.host="}
}

func TestHostingAcceptance(t *testing.T) {
	for _, v := range matrix(t) {
		t.Run(v.name, func(t *testing.T) {
			a := newApplication(t, v)
			for _, transport := range []string{transportHTTP, transportSSH} {
				for _, visibility := range []string{"public", "private"} {
					t.Run(transport+"-"+visibility, func(t *testing.T) {
						for _, role := range []string{"owner", "writer", "reader", "stranger", "anonymous"} {
							if transport == transportSSH && role == "anonymous" {
								continue
							}
							remote, opts := a.remote(t, transport, role, visibility)
							local := filepath.Join(a.root, transport+"-"+visibility+"-"+role)
							start := time.Now()
							out, err := a.client.run(context.Background(), "", append(append([]string{}, opts...), "clone", remote, local)...)
							allowed := visibility == "public" || role == "owner" || role == "writer" || role == "reader"
							if (err == nil) != allowed {
								t.Fatalf("%s clone allowed=%t: %v %s", role, allowed, err, out)
							}
							if !allowed {
								continue
							}
							t.Logf("%s clone: %s", role, time.Since(start))
							if err = os.WriteFile(filepath.Join(local, "data"), []byte(transport+role), 0600); err != nil {
								t.Fatal(err)
							}
							a.client.must(t, local, "add", "data")
							a.client.must(t, local, "commit", "-u", role, "-m", transport+role)
							store := filepath.Join(a.repoRoot, fmt.Sprint(a.repos[visibility].Id))
							before := snapshotRepository(t, a.inspect, store)
							start = time.Now()
							out, err = a.client.run(context.Background(), local, append(append([]string{}, opts...), "push", remote)...)
							write := role == "owner" || role == "writer"
							if (err == nil) != write {
								t.Fatalf("%s push allowed=%t: %v %s", role, write, err, out)
							}
							if !write {
								if diff := before.diff(snapshotRepository(t, a.inspect, store)); diff != "" {
									t.Fatalf("denied %s mutated repository: %s", role, diff)
								}
							} else {
								t.Logf("%s push: %s", role, time.Since(start))
							}
						}
					})
				}
			}
			before := snapshotRepository(t, a.inspect, filepath.Join(a.repoRoot, fmt.Sprint(a.repos["public"].Id)))
			a.stop()
			a.start(t)
			if diff := before.diff(snapshotRepository(t, a.inspect, filepath.Join(a.repoRoot, fmt.Sprint(a.repos["public"].Id)))); diff != "" {
				t.Fatalf("accepted changes lost on process restart: %s", diff)
			}
		})
	}
}

func replaceEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		k, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(k, key) {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}
