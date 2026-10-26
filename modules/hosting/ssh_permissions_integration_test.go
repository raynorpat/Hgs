//go:build integration

package hosting

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	gssh "github.com/gogits/gogs/modules/ssh"
	"golang.org/x/crypto/ssh"
)

// Hold the real pretxnopen callback, revoke a database-backed SSH grant (or
// change visibility and await the live monitor), then ensure no node lands.
// No assertion promises rollback for revocation after an allowed transaction.
func TestPermissionChangesDuringSSHPush(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	hook := nativeHookBinary(t)
	for _, change := range []string{"key", "access", "visibility"} {
		t.Run(change, func(t *testing.T) {
			s, owner, repo := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe })
			writer := &models.User{Name: "bob", Email: "bob@example.test", Passwd: "password", IsActive: true}
			if err := models.CreateUser(writer); err != nil {
				t.Fatal(err)
			}
			if err := models.AddAccess(&models.Access{UserId: writer.Id, RepoId: repo.Id, Mode: models.WRITABLE}); err != nil {
				t.Fatal(err)
			}
			_, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewSignerFromKey(private)
			if err != nil {
				t.Fatal(err)
			}
			key := &models.PublicKey{OwnerId: writer.Id, Name: "push", Content: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
			if err = models.AddPublicKey(key); err != nil {
				t.Fatal(err)
			}
			s.ssh.Shutdown(context.Background())
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
			s.stdio, err = hgbridge.NewStdioBridge(s.runner, s.controller, hook, filepath.Dir(s.cfg.Mercurial.ConfigPath))
			if err != nil {
				t.Fatal(err)
			}
			s.stdio.Registrar = s
			hostSigner, err := loadHostKey(s.cfg.HostKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			s.ssh = gssh.NewServer(hostSigner, func(ctx context.Context, key ssh.PublicKey) (hgbridge.Principal, error) {
				uid, kid, e := models.AuthenticateRepositoryKey(key)
				return hgbridge.Principal{UserID: uid, KeyID: kid}, e
			}, s.resolve, s.stdio)
			s.ssh.Registrar = s
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			served := make(chan error, 1)
			go func() { served <- s.ListenSSH(ln) }()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				s.Shutdown(ctx)
				<-served
			})
			client, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "hgrc")})
			if err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(t.TempDir(), "source")
			nativeRun(t, client, "", "init", local)
			if err = os.WriteFile(filepath.Join(local, "data"), []byte(change), 0600); err != nil {
				t.Fatal(err)
			}
			nativeRun(t, client, local, "add", "data")
			nativeRun(t, client, local, "commit", "-u", owner.Name, "-m", change)
			bundlePath := filepath.Join(t.TempDir(), "push.hg")
			nativeRun(t, client, local, "bundle", "-a", "-t", "none-v2", bundlePath)
			bundle, err := os.ReadFile(bundlePath)
			if err != nil {
				t.Fatal(err)
			}
			sshConfig := &ssh.ClientConfig{User: "hg", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostSigner.PublicKey()), Timeout: 5 * time.Second}
			conn, err := ssh.Dial("tcp", ln.Addr().String(), sshConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			session, err := conn.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			in, err := session.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			session.Stdout = &output
			session.Stderr = io.Discard
			if err = session.Start("hg -R alice/project serve --stdio"); err != nil {
				t.Fatal(err)
			}
			// Standard v1 unbundle framing; bundle bytes were produced by native hg.
			if _, err = fmt.Fprintf(in, "unbundle\nheads 10\n666f726365%d\n", len(bundle)); err != nil {
				t.Fatal(err)
			}
			if _, err = in.Write(bundle); err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(in, "0\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("native SSH push did not reach transaction-open hook")
			}
			switch change {
			case "key":
				err = models.DeletePublicKey(key)
			case "access":
				err = models.DeleteAccess(&models.Access{UserId: writer.Id, RepoId: repo.Id})
			case "visibility":
				repo.IsPrivate = false
				err = models.UpdateRepository(repo)
			}
			if err != nil {
				t.Fatal(err)
			}
			if change != "visibility" {
				unblock.Do(func() { close(release) })
			}
			// EOF lets a denied noninteractive wire session finish; visibility uses
			// the monitor to kill an existing session before the held check allows.
			if change != "visibility" {
				in.Close()
			}
			done := make(chan error, 1)
			go func() { done <- session.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("revoked native SSH session remained active")
			}
			unblock.Do(func() { close(release) })
			store := filepath.Join(s.RepositoryRoot(), fmt.Sprint(repo.Id))
			if got := nativeRun(t, client, store, "log", "--hidden", "-T", "{node} {phase}\n"); got != "" {
				t.Fatalf("revoked SSH push changed history/phases: %s", got)
			}
			if got := nativeRun(t, client, store, "bookmarks", "-T", "json"); got != "[\n]\n" && got != "[]\n" {
				t.Fatalf("revoked SSH push changed bookmarks: %q", got)
			}
			nativeRun(t, client, store, "verify")
			if change == "key" {
				if next, e := ssh.Dial("tcp", ln.Addr().String(), sshConfig); e == nil {
					next.Close()
					t.Fatal("revoked key opened a new SSH connection")
				}
			}
		})
	}
}
