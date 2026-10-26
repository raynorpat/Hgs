//go:build integration

package hosting

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

func nativeHookBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gogs.exe")
	cmd := exec.Command("go", "build", "-o", path, "github.com/gogits/gogs")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build native hook: %v %s", err, out)
	}
	return path
}

func nativeRun(t *testing.T, r *hg.Runner, repo string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := r.Run(context.Background(), repo, append([]string{"--config", "http_proxy.host="}, args...), nil, &out); err != nil {
		t.Fatalf("hg %s: %v %s", args[0], err, out.String())
	}
	return out.String()
}

// The synchronization wrapper blocks a real hook callback, but the final
// decision is the production Service.authorize querying actual model records.
func TestPermissionChangesDuringPush(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	hook := nativeHookBinary(t)
	for _, change := range []string{"access", "password", "token", "ownership", "team"} {
		t.Run(change, func(t *testing.T) {
			s, owner, repo := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe; c.Mercurial.MaxProcesses = 1 })
			writer := &models.User{Name: "bob", Email: "bob@example.test", Passwd: "password", IsActive: true}
			if err := models.CreateUser(writer); err != nil {
				t.Fatal(err)
			}
			var team *models.Team
			if change == "ownership" {
				writer = owner
			} else if change == "team" {
				org, err := models.CreateOrganization(&models.User{Name: "organization", Email: "org@example.test", IsActive: true}, owner)
				if err != nil {
					t.Fatal(err)
				}
				if err = models.TransferOwnership(owner, org.Name, repo); err != nil {
					t.Fatal(err)
				}
				team = &models.Team{OrgId: org.Id, Name: "writers", Authorize: models.ORG_WRITABLE}
				if err = models.NewTeam(team); err != nil {
					t.Fatal(err)
				}
				if err = team.AddMember(writer.Id); err != nil {
					t.Fatal(err)
				}
				if err = team.AddRepository(repo); err != nil {
					t.Fatal(err)
				}
			} else if err := models.AddAccess(&models.Access{UserId: writer.Id, RepoId: repo.Id, Mode: models.WRITABLE}); err != nil {
				t.Fatal(err)
			}
			username, password := writer.Name, "password"
			reader := &models.User{Name: "reader", Email: "reader@example.test", Passwd: "password", IsActive: true}
			if err := models.CreateUser(reader); err != nil {
				t.Fatal(err)
			}
			if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: repo.Id, Mode: models.READABLE}); err != nil {
				t.Fatal(err)
			}
			var token *models.AccessToken
			if change == "token" {
				token = &models.AccessToken{Uid: writer.Id, Name: "push"}
				if err := models.NewAccessToken(token); err != nil {
					t.Fatal(err)
				}
				username, password = token.Sha1, "x"
			}
			s.controller.Close(context.Background())
			s.http.Close()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			ctl, err := hgbridge.NewController(func(ctx context.Context, g hgbridge.Grant) error {
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
			s.controller = ctl
			s.http, err = hgbridge.NewHTTPBridge(s.runner, ctl, hook)
			if err != nil {
				t.Fatal(err)
			}
			s.http.Registrar = s
			server := httptest.NewTLSServer(s.HTTP())
			defer server.Close()
			defer unblock.Do(func() { close(release) })
			ownerName := owner.Name
			if change == "team" {
				ownerName = "organization"
			}
			remote, _ := url.Parse(server.URL + "/" + ownerName + "/project")
			remote.User = url.UserPassword(username, password)
			client, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "client", "hgrc")})
			if err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(t.TempDir(), "source")
			nativeRun(t, client, "", "clone", "--insecure", remote.String(), local)
			if err = os.WriteFile(filepath.Join(local, "data"), []byte(change), 0600); err != nil {
				t.Fatal(err)
			}
			nativeRun(t, client, local, "add", "data")
			nativeRun(t, client, local, "commit", "-u", "writer", "-m", change)
			store := filepath.Join(s.RepositoryRoot(), fmt.Sprint(repo.Id))
			snapshot := func() string {
				return nativeRun(t, client, store, "log", "--hidden", "-T", "{node} {phase}\n") + nativeRun(t, client, store, "bookmarks", "-T", "json")
			}
			before := snapshot()
			done := make(chan error, 1)
			go func() {
				done <- client.Run(context.Background(), local, []string{"--config", "http_proxy.host=", "push", "--insecure", remote.String()}, nil, nil)
			}()
			select {
			case <-entered:
			case <-time.After(15 * time.Second):
				t.Fatal("native push did not reach real hook callback")
			}
			// A different principal queues for the only worker slot while the
			// real write callback is held. Callback authorization must remain
			// independent of worker capacity and unblock both requests.
			readDone := make(chan error, 1)
			go func() {
				req, _ := http.NewRequest("GET", server.URL+"/"+ownerName+"/project?cmd=capabilities", nil)
				req.SetBasicAuth("reader", "password")
				resp, e := server.Client().Do(req)
				if e == nil {
					resp.Body.Close()
					if resp.StatusCode != 200 && !(change == "ownership" && resp.StatusCode == 503) {
						e = fmt.Errorf("reader status %d", resp.StatusCode)
					}
				}
				readDone <- e
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				s.sessions.mu.Lock()
				n := len(s.sessions.sessions)
				s.sessions.mu.Unlock()
				if n >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("second principal did not queue while callback held")
				}
				time.Sleep(10 * time.Millisecond)
			}
			switch change {
			case "access":
				err = models.DeleteAccess(&models.Access{UserId: writer.Id, RepoId: repo.Id})
			case "password":
				writer.Passwd = "replacement"
				writer.EncodePasswd()
				err = models.UpdateUser(writer)
			case "token":
				err = models.DeleteAccessTokenById(token.Id)
			case "ownership":
				repo.Owner = owner
				err = models.TransferOwnership(owner, "bob", repo)
			case "team":
				err = team.RemoveMember(writer.Id)
			}
			if err != nil {
				t.Fatal(err)
			}
			unblock.Do(func() { close(release) })
			select {
			case err = <-done:
				if err == nil {
					t.Fatal("revoked push succeeded")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("revoked push did not terminate")
			}
			select {
			case err = <-readDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("reader remained blocked after callback released")
			}
			if after := snapshot(); before != after {
				t.Fatalf("revoked transaction changed nodes/phases/bookmarks: %q -> %q", before, after)
			}
			// A new connection using the old credentials/permission must also fail.
			if err = client.Run(context.Background(), local, []string{"--config", "http_proxy.host=", "push", "--insecure", remote.String()}, nil, nil); err == nil {
				t.Fatal("old authority opened a new write session")
			}
		})
	}
}

func TestHostingStaleCapabilityAfterRestart(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	hook := nativeHookBinary(t)
	s, owner, repo := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe })
	grant := hgbridge.Grant{Principal: passwordPrincipal(t, owner), RepoID: repo.Id, CanWrite: true}
	old, _, revoke, err := s.controller.Issue(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(context.Background())
	_, env, release, err := restarted.controller.Issue(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r, err := hg.NewRunner(hg.Config{Executable: hook, ConfigPath: filepath.Join(t.TempDir(), "hook", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	run := func() error {
		c, err := r.Start(context.Background(), "", []string{hgbridge.HookCommand, "check", "pretxnopen"}, env)
		if err != nil {
			return err
		}
		go func() { io.Copy(io.Discard, c.Stdout) }()
		go func() { io.Copy(io.Discard, c.Stderr) }()
		return c.Stop(context.Background())
	}
	if err = run(); err != nil {
		t.Fatalf("fresh restarted capability failed: %v", err)
	}
	env[hgbridge.EnvCapability] = old
	if err = run(); err == nil {
		t.Fatal("old capability authorized against restarted controller")
	}
}
