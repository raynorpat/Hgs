//go:build integration

package hosting

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"golang.org/x/crypto/ssh"
)

// Native SSH holds the sole worker while a second HTTPS principal queues.
// This exercises the actual service's bounded graceful-drain/forced-cleanup
// path, separately from the executable fixture's abrupt process termination.
func TestShutdownUnderLoad(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	s, user, _ := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe; c.Mercurial.MaxProcesses = 1 })
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if err = models.AddPublicKey(&models.PublicKey{OwnerId: user.Id, Name: "load", Content: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.ListenSSH(ln) }()
	conn, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "hg", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
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
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Start("hg -R alice/project serve --stdio"); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(in, "hello\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line == "" {
		t.Fatalf("native stdio worker did not respond: %q %v", line, err)
	}
	server := httptest.NewTLSServer(s.HTTP())
	defer server.Close()
	queued := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest("GET", server.URL+"/alice/project?cmd=capabilities", nil)
		req.SetBasicAuth("alice", "password")
		resp, e := server.Client().Do(req)
		if e == nil {
			resp.Body.Close()
		}
		queued <- e
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.sessions.mu.Lock()
		n := len(s.sessions.sessions)
		s.sessions.mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTPS request never queued behind native SSH worker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err = s.Shutdown(ctx); err == nil {
		t.Fatal("busy shutdown did not report expired drain deadline")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("native workers exceeded cleanup bound")
	}
	select {
	case <-queued:
	case <-time.After(3 * time.Second):
		t.Fatal("queued HTTP handler remained blocked")
	}
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("SSH listener not joined")
	}
	s.sessions.mu.Lock()
	left := len(s.sessions.sessions)
	s.sessions.mu.Unlock()
	if left != 0 {
		t.Fatalf("live sessions after shutdown: %d", left)
	}
	if err = session.Wait(); err == nil {
		t.Fatal("unfinished stdio session survived shutdown")
	}
}
