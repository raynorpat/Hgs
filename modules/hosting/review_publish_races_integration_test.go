//go:build integration

package hosting

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
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
	"github.com/gogits/gogs/modules/hgbridge"
	gssh "github.com/gogits/gogs/modules/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Hold a REAL native transaction-open callback while publication tries to
// reserve the destination; after the native writer exits the old review must
// be stale. Both transports use the actual native Mercurial push client.
func TestReviewPublishRacesNativePush(t *testing.T) {
	for _, transport := range []string{"https", "ssh"} {
		t.Run(transport, func(t *testing.T) {
			s, actor, e, tp := publicationFixtureSelection(t, false)
			client, err := hg.NewRunner(hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(t.TempDir(), "hgrc")})
			if err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(t.TempDir(), "competitor")
			nativeRun(t, client, "", "clone", tp, local)
			if err = os.WriteFile(filepath.Join(local, "race"), []byte("native race"), 0600); err != nil {
				t.Fatal(err)
			}
			nativeRun(t, client, local, "add", "race")
			nativeRun(t, client, local, "commit", "-u", "alice", "-m", "native race")
			s.http.Close()
			s.stdio.Close()
			s.controller.Close(context.Background())
			entered, release := make(chan struct{}), make(chan struct{})
			var once, unblock sync.Once
			defer unblock.Do(func() { close(release) })
			s.controller, err = hgbridge.NewController(func(ctx context.Context, g hgbridge.Grant) error {
				once.Do(func() {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
				return s.authorize(ctx, g)
			}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			if transport == "https" {
				s.http, err = hgbridge.NewHTTPBridge(s.runner, s.controller, s.hookExecutable)
				if err != nil {
					t.Fatal(err)
				}
				s.http.Registrar = s
				server := httptest.NewTLSServer(s.HTTP())
				defer server.Close()
				remote, _ := url.Parse(server.URL + "/alice/project")
				remote.User = url.UserPassword("alice", "password")
				go func() {
					done <- client.Run(context.Background(), local, []string{"--config", "http_proxy.host=", "push", "--insecure", remote.String()}, nil, nil)
				}()
			} else {
				_, private, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				signer, err := ssh.NewSignerFromKey(private)
				if err != nil {
					t.Fatal(err)
				}
				key := &models.PublicKey{OwnerId: actor, Name: "race", Content: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
				if err = models.AddPublicKey(key); err != nil {
					t.Fatal(err)
				}
				s.ssh.Shutdown(context.Background())
				s.stdio, err = hgbridge.NewStdioBridge(s.runner, s.controller, s.hookExecutable, filepath.Dir(s.cfg.Mercurial.ConfigPath))
				if err != nil {
					t.Fatal(err)
				}
				s.stdio.Registrar = s
				host, err := loadHostKey(s.cfg.HostKeyPath)
				if err != nil {
					t.Fatal(err)
				}
				s.ssh = gssh.NewServer(host, func(ctx context.Context, key ssh.PublicKey) (hgbridge.Principal, error) {
					u, k, e := models.AuthenticateRepositoryKey(key)
					return hgbridge.Principal{UserID: u, KeyID: k}, e
				}, s.resolve, s.stdio)
				s.ssh.Registrar = s
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				go s.ListenSSH(ln)
				keyDir := t.TempDir()
				keyPath := filepath.Join(keyDir, "key")
				known := filepath.Join(keyDir, "known_hosts")
				block, err := ssh.MarshalPrivateKey(private, "")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
					t.Fatal(err)
				}
				sshExe, err := exec.LookPath("ssh")
				var args []string
				if runtime.GOOS == "windows" {
					sshExe = filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe")
					_, err = os.Stat(sshExe)
					u, e := user.Current()
					if e != nil {
						t.Fatal(e)
					}
					if out, e := exec.Command("icacls", keyPath, "/inheritance:r", "/grant:r", "*"+u.Uid+":F").CombinedOutput(); e != nil {
						t.Fatalf("restrict key ACL: %v %s", e, out)
					}
					args = []string{"--config", "sshenv.PROGRAMDATA=" + os.Getenv("ProgramData")}
				}
				if err != nil {
					t.Fatal(err)
				}
				line := knownhosts.Line([]string{knownhosts.Normalize(ln.Addr().String())}, host.PublicKey())
				if err = os.WriteFile(known, []byte(line+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--config", fmt.Sprintf(`ui.ssh="%s" -F none -i "%s" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="%s"`, filepath.ToSlash(sshExe), filepath.ToSlash(keyPath), filepath.ToSlash(known)))
				args = append(args, "push", "ssh://hg@"+ln.Addr().String()+"/alice/project")
				go func() { done <- client.Run(context.Background(), local, args, nil, nil) }()
			}
			select {
			case <-entered:
			case <-time.After(15 * time.Second):
				t.Fatal("native transaction did not reach hook")
			}
			if err = publish(t, s, actor, e, tp); !errors.Is(err, ErrMutationBusy) {
				t.Fatalf("concurrent publication: %v", err)
			}
			unblock.Do(func() { close(release) })
			select {
			case err = <-done:
				if err != nil {
					t.Fatalf("native writer: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("native writer stuck")
			}
			// HTTP worker/SSH teardown is registered until the actual process exits.
			deadline := time.Now().Add(5 * time.Second)
			for {
				s.sessions.mu.Lock()
				busy := false
				for session := range s.sessions.sessions {
					if session.grant.RepoID == e.RepoID {
						busy = true
					}
				}
				s.sessions.mu.Unlock()
				if !busy {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("native process was not reaped")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err = publish(t, s, actor, e, tp); !errors.Is(err, ErrStaleRepository) {
				t.Fatalf("changed destination: %v", err)
			}
			nodes := nativeRun(t, client, tp, "log", "-T", "{node}\n")
			if strings.Contains(nodes, string(e.Prepared.Result)) {
				t.Fatal("stale review history published")
			}
			if heads := nativeRun(t, client, tp, "heads", "-T", "{node}\n"); len(strings.Fields(heads)) != 1 {
				t.Fatalf("extra branch head: %s", heads)
			}
			nativeRun(t, client, tp, "verify")
		})
	}
}
