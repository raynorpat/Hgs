//go:build integration

package cmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
	"golang.org/x/crypto/ssh"
)

func TestHostingNativeComposition(t *testing.T) {
	hgExe := os.Getenv("GOGS_TEST_HG")
	if hgExe == "" {
		t.Fatal("GOGS_TEST_HG must name native Mercurial")
	}
	root := t.TempDir()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(root, "test.db"))
	setting.LogRootPath = root
	if err = models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { models.CloseEngine() })
	setting.StaticRootPath = repoRoot
	setting.ConfRootPath = filepath.Join(repoRoot, "conf")
	setting.CustomPath = root
	setting.CacheAdapter = "memory"
	setting.SessionConfig.Provider = "memory"
	setting.Langs = []string{"en-US"}
	setting.Names = []string{"English"}
	setting.Service.RequireSignInView = false
	setting.Mercurial.Executable = hgExe
	setting.Mercurial.ConfigPath = filepath.Join(root, "config", "hgrc")
	setting.Mercurial.ReviewArtifactRoot = filepath.Join(root, "comparisons")
	setting.Mercurial.MaxProcesses = 2
	setting.Mercurial.SessionTimeout = time.Minute
	setting.RepoRootPath = filepath.Join(root, "repos")
	setting.SSHHostKeyPath = filepath.Join(root, "host_ed25519")
	setting.AppUrl = "https://example.test/gogs/"
	setting.AppSubUrl = "/gogs"
	setting.SSHAddress = "127.0.0.1:0"
	setting.SSHAdvertisedHost = "127.0.0.1"
	setting.SSHPort = 2222
	setting.DisableSSH = false
	alice := &models.User{Name: "alice", Email: "alice@example.test", Passwd: "password", IsActive: true}
	if err = models.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	holder := &hostingHolder{errors: make(chan error, 1)}
	var initializers sync.WaitGroup
	for i := 0; i < 4; i++ {
		initializers.Add(1)
		go func() {
			defer initializers.Done()
			if err := holder.Initialize(); err != nil {
				t.Error(err)
			}
		}()
	}
	initializers.Wait()
	service := holder.Service()
	if service == nil {
		t.Fatal("no service after installation callback")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		holder.Shutdown(ctx)
	})
	repo, err := models.CreateHgRepository(context.Background(), service.Runner(), service.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: alice.Id, Name: "project", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := newWebHandler(holder)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	remote, _ := url.Parse(server.URL + "/gogs/hg/alice/project")
	remote.User = url.UserPassword("alice", "password")
	client, err := hg.NewRunner(hg.Config{Executable: hgExe, ConfigPath: filepath.Join(root, "client", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	run := func(repo string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := client.Run(context.Background(), repo, append([]string{"--config", "http_proxy.host="}, args...), nil, &out); err != nil {
			t.Fatalf("hg %v: %v %s", args, err, out.String())
		}
		return out.String()
	}
	source := filepath.Join(root, "source")
	run("", "init", source)
	if err = os.WriteFile(filepath.Join(source, "a.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(source, "add", "a.txt")
	run(source, "commit", "-m", "first", "-u", "tester")
	run(source, "push", "--insecure", remote.String())
	clone := filepath.Join(root, "clone")
	run("", "clone", "--insecure", remote.String(), clone)
	if content, err := os.ReadFile(filepath.Join(clone, "a.txt")); err != nil || string(content) != "hello\n" {
		t.Fatalf("native HTTPS clone: %q %v", content, err)
	}
	// The same production dispatcher accepts a proxy-stripped path once.
	req := httptest.NewRequest("GET", "https://host/hg/alice/project?cmd=capabilities", nil)
	req.SetBasicAuth("alice", "password")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("stripped prefix: %d %s", w.Code, w.Body)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	stored := &models.PublicKey{OwnerId: alice.Id, Name: "test", Content: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
	if err = models.AddPublicKey(stored); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "client_key")
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		current, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("icacls", keyPath, "/inheritance:r", "/grant:r", "*"+current.Uid+":F").CombinedOutput(); err != nil {
			t.Fatalf("key ACL: %v %s", err, out)
		}
	}
	hostData, err := os.ReadFile(setting.SSHHostKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostData)
	if err != nil {
		t.Fatal(err)
	}
	addr := holder.sshListener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	known := filepath.Join(root, "known_hosts")
	if err = os.WriteFile(known, []byte("[127.0.0.1]:"+port+" "+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sshExe, err := exec.LookPath("ssh")
	var sshEnv []string
	if runtime.GOOS == "windows" {
		sshExe = filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe")
		_, err = os.Stat(sshExe)
		sshEnv = []string{"--config", "sshenv.PROGRAMDATA=" + os.Getenv("ProgramData")}
	}
	if err != nil {
		t.Fatal(err)
	}
	sshCommand := fmt.Sprintf(`"%s" -F none -i "%s" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="%s"`, filepath.ToSlash(sshExe), filepath.ToSlash(keyPath), filepath.ToSlash(known))
	sshURL := "ssh://hg@127.0.0.1:" + port + "/alice/project"
	sshClone := filepath.Join(root, "ssh-clone")
	run("", append(sshEnv, "--config", "ui.ssh="+sshCommand, "clone", sshURL, sshClone)...)
	run(sshClone, append(sshEnv, "--config", "ui.ssh="+sshCommand, "pull")...)
	if content, err := os.ReadFile(filepath.Join(sshClone, "a.txt")); err != nil || string(content) != "hello\n" {
		t.Fatalf("native SSH clone: %q %v", content, err)
	}
	if err = models.DeleteHgRepository(context.Background(), service.RepositoryRoot(), repo.Id, service.DrainRepository); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(service.RepositoryRoot(), fmt.Sprint(repo.Id))); !os.IsNotExist(err) {
		t.Fatalf("repository storage survived deletion: %v", err)
	}
	// Old server/client runtimes and native Unix runners are not present here.
	t.Log("native Mercurial 7.2.4 HTTPS push/clone and SSH clone/pull passed; old/native Unix matrix unavailable")
}
