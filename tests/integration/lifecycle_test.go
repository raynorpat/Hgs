//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"golang.org/x/crypto/ssh"
)

// Send exactly one native-produced bundle, observe its committed node, then
// disconnect before reading the response and restart the actual application.
func TestHostingCommittedDisconnect(t *testing.T) {
	a := newApplication(t, matrix(t)[0])
	for _, transport := range []string{transportHTTP, transportSSH} {
		t.Run(transport, func(t *testing.T) {
			remote, opts := a.remote(t, transport, "writer", "public")
			local := filepath.Join(a.root, "ambiguous-"+transport)
			a.client.must(t, "", append(append([]string{}, opts...), "clone", remote, local)...)
			if err := os.WriteFile(filepath.Join(local, "data"), []byte(transport), 0600); err != nil {
				t.Fatal(err)
			}
			a.client.must(t, local, "add", "data")
			a.client.must(t, local, "commit", "-u", "writer", "-m", transport)
			tip := a.client.stdout(t, local, "log", "-r", "tip", "-T", "{node}")
			bundlePath := filepath.Join(a.root, transport+".hg")
			a.client.must(t, local, "bundle", "-a", "-t", "none-v2", bundlePath)
			bundle, err := os.ReadFile(bundlePath)
			if err != nil {
				t.Fatal(err)
			}
			var disconnect func()
			var failedResponse func() string
			const force = "666f726365"
			if transport == transportHTTP {
				c, err := tls.Dial("tcp", a.address, &tls.Config{InsecureSkipVerify: true})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				disconnect = func() { c.Close() }
				failedResponse = func() string { c.SetReadDeadline(time.Now().Add(time.Second)); b, _ := io.ReadAll(c); return string(b) }
				fmt.Fprintf(c, "POST /hg/owner/public?cmd=unbundle&heads=%s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Basic %s\r\nContent-Type: application/mercurial-0.1\r\nContent-Length: %d\r\n\r\n", force, base64.StdEncoding.EncodeToString([]byte("writer:password")), len(bundle))
				if _, err = c.Write(bundle); err != nil {
					t.Fatal(err)
				}
			} else {
				key, err := os.ReadFile(a.keys["writer"])
				if err != nil {
					t.Fatal(err)
				}
				signer, err := ssh.ParsePrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				c, err := ssh.Dial("tcp", a.sshAddress, &ssh.ClientConfig{User: "hg", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				disconnect = func() { c.Close() }
				s, err := c.NewSession()
				if err != nil {
					t.Fatal(err)
				}
				in, err := s.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Start("hg -R owner/public serve --stdio"); err != nil {
					t.Fatal(err)
				}
				request := append(sshCommandBytes("unbundle", [2]string{"heads", force}), sshPayload(bundle)...)
				if _, err = bytes.NewReader(request).WriteTo(in); err != nil {
					t.Fatal(err)
				}
			}
			store := filepath.Join(a.repoRoot, fmt.Sprint(a.repos["public"].Id))
			committed := func() bool {
				out, err := a.inspect.run(context.Background(), store, "log", "-r", tip, "-T", "{node}")
				return err == nil && out == tip
			}
			deadline := time.Now().Add(30 * time.Second)
			for !committed() {
				if time.Now().After(deadline) {
					if failedResponse != nil {
						t.Fatalf("push did not commit; diagnostic response: %s", failedResponse())
					}
					t.Fatal("push did not commit")
				}
				time.Sleep(50 * time.Millisecond)
			}
			disconnect()
			a.stop()
			a.start(t)
			if got := a.inspect.stdout(t, store, "log", "-r", tip, "-T", "{node}"); got != tip {
				t.Fatal("committed push lost after disconnect/restart")
			}
			a.inspect.must(t, store, "verify")
		})
	}
}

func TestHostingCredentialAndVisibilityChanges(t *testing.T) {
	a := newApplication(t, matrix(t)[0])
	for _, transport := range []string{transportHTTP, transportSSH} {
		remote, opts := a.remote(t, transport, "writer", "private")
		a.client.must(t, "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "before-"+transport))...)
	}
	if err := models.DeletePublicKey(&models.PublicKey{Id: a.keyIDs["writer"], OwnerId: a.users["writer"].Id}); err != nil {
		t.Fatal(err)
	}
	remote, opts := a.remote(t, transportSSH, "writer", "private")
	if out, err := a.client.run(context.Background(), "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "revoked-key"))...); err == nil {
		t.Fatalf("revoked key accepted: %s", out)
	}
	token := &models.AccessToken{Uid: a.users["writer"].Id, Name: "acceptance"}
	if err := models.NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, Timeout: 10 * time.Second}
	check := func(want int) {
		t.Helper()
		req, _ := http.NewRequest("GET", "https://"+a.address+"/hg/owner/private?cmd=capabilities", nil)
		req.SetBasicAuth(token.Sha1, "x")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("token request status %d want %d", resp.StatusCode, want)
		}
	}
	check(200)
	if err := models.DeleteAccessTokenById(token.Id); err != nil {
		t.Fatal(err)
	}
	check(401)
	repo := a.repos["public"]
	repo.IsPrivate = true
	if err := models.UpdateRepository(repo); err != nil {
		t.Fatal(err)
	}
	remote, opts = a.remote(t, transportHTTP, "anonymous", "public")
	if _, err := a.client.run(context.Background(), "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "now-private"))...); err == nil {
		t.Fatal("visibility change admitted new anonymous session")
	}
	a.stop()
	a.start(t)
	check(401)
	// Persistent SSH host identity still permits an unchanged owner's key.
	remote, opts = a.remote(t, transportSSH, "owner", "private")
	a.client.must(t, "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "after-restart"))...)
	if c, err := net.DialTimeout("tcp", a.sshAddress, time.Second); err != nil {
		t.Fatal(err)
	} else {
		c.Close()
	}
}
