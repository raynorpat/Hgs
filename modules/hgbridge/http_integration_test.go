// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

package hgbridge

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

func TestHTTPBridgeNativeClients(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	gogs := buildGogs(t)
	t.Run("current server, current client", func(t *testing.T) { checkHTTPBridge(t, exe, exe, gogs) })
	if old := os.Getenv("GOGS_TEST_HG_OLD_CLIENT"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_CLIENT (Mercurial 2.0.x client) not set")
	} else {
		t.Run("current server, old client", func(t *testing.T) { checkHTTPBridge(t, exe, old, gogs) })
	}
	if old := os.Getenv("GOGS_TEST_HG_OLD_SERVER"); old == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_SERVER (Mercurial 5.0.x) not set")
	} else {
		t.Run("old server, current client", func(t *testing.T) { checkHTTPBridge(t, old, exe, gogs) })
	}
}

func checkHTTPBridge(t *testing.T, serverExe, clientExe, gogs string) {
	root := filepath.Join(t.TempDir(), "http é")
	ctx := context.Background()
	newHarness := func(exe, name string) *hgHarness {
		r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(root, name, "hgrc")})
		if err != nil {
			t.Fatal(err)
		}
		return &hgHarness{t: t, r: r, ctx: ctx}
	}
	server, client := newHarness(serverExe, "server"), newHarness(clientExe, "client")
	if err := server.r.Check(ctx); err != nil {
		t.Fatal(err)
	}

	ctl, err := NewController(func(context.Context, Grant) error { return nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close(ctx)
	b, err := NewHTTPBridge(server.r, ctl, gogs)
	if err != nil {
		t.Fatal(err)
	}
	var active, started atomic.Int64
	start := b.start
	b.start = func(ctx context.Context, repoPath string, args []string, env map[string]string) (*worker, error) {
		wk, err := start(ctx, repoPath, args, env)
		if err == nil {
			started.Add(1)
			active.Add(1)
			reap := wk.reap
			wk.reap = func() { reap(); active.Add(-1) }
		}
		return wk, err
	}

	dest := filepath.Join(root, "repos", "9")
	server.must("", nil, nil, "init", dest)
	if err := os.WriteFile(filepath.Join(dest, "seed"), []byte("seed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server.must(dest, nil, nil, "commit", "-A", "-m", "seed", "-u", "seed")
	target := Target{RepoID: 9, Path: dest}

	users := map[string]Grant{
		"writer:writer-secret": {Principal: Principal{UserID: 1, TokenID: 1}, RepoID: 9, CanWrite: true},
		"reader:reader-secret": {Principal: Principal{UserID: 2, TokenID: 2}, RepoID: 9},
	}
	const prefix = "/hg/owner/repo"
	frontend := httptest.NewUnstartedServer(http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		grant, ok := users[user+":"+pass]
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Gogs"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if err := b.Serve(r.Context(), w, r, target, grant); err != nil {
			t.Logf("Serve %s: %v", r.URL.Query().Get("cmd"), err)
			http.Error(w, "repository unavailable", http.StatusBadGateway)
		}
	})))
	// Mercurial matches only DNS names, not IP addresses, against the
	// certificate. The one certificate needs no SNI, for clients that predate it.
	cert, cacerts := localhostCert(t, root)
	frontend.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	frontend.StartTLS()
	defer frontend.Close()
	// The managed config sends every HTTP request to a dead proxy; the test
	// clients must reach the frontend directly.
	tlsCfg := map[string]string{"web.cacerts": cacerts, "http_proxy.host": ""}
	url := func(user string) string {
		return "https://" + user + "@localhost:" + port(frontend) + prefix
	}
	writer, reader := url("writer:writer-secret"), url("reader:reader-secret")

	commit := func(repo, name string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		client.must(repo, nil, nil, "commit", "-A", "-m", name, "-u", "tester")
		return client.must(repo, nil, nil, "log", "-r", "tip", "--template", "{node}")
	}
	destTip := func() string {
		return server.must(dest, nil, nil, "log", "-r", "tip", "-T", "{node}")
	}
	seed := destTip()

	readerClone := filepath.Join(root, "reader clone")
	client.must("", tlsCfg, nil, "clone", reader, readerClone)
	if got := client.must(readerClone, nil, nil, "log", "-r", "tip", "--template", "{node}"); got != seed {
		t.Fatalf("reader clone tip %s, want %s", got, seed)
	}

	commit(readerClone, "reader change")
	before := server.snapshot(dest)
	// The reader worker refuses push at hgweb's gate, before the deny hooks.
	if out, err := client.hg(readerClone, tlsCfg, nil, "push", reader); err == nil {
		t.Errorf("reader push succeeded: %s", out)
	} else if !strings.Contains(err.Error(), "HTTP Error 403") {
		t.Errorf("reader push not refused by the worker: %v", err)
	}
	if server.snapshot(dest) != before {
		t.Fatal("reader push changed the repository")
	}

	writerClone := filepath.Join(root, "writer clone")
	client.must("", tlsCfg, nil, "clone", writer, writerClone)
	pushed := commit(writerClone, "writer change")
	client.must(writerClone, tlsCfg, nil, "push", writer)
	if got := destTip(); got != pushed {
		t.Fatalf("writer push: repository tip %s, want %s", got, pushed)
	}
	pushed = commit(writerClone, "bundle1 change")
	client.must(writerClone, tlsCfg, nil, "push", "--config", "devel.legacy.exchange=bundle1", writer)
	if got := destTip(); got != pushed {
		t.Fatalf("bundle1 writer push: repository tip %s, want %s", got, pushed)
	}

	secondClone := filepath.Join(root, "second clone")
	client.must("", tlsCfg, nil, "clone", reader, secondClone)
	client.must(readerClone, tlsCfg, nil, "pull", reader)
	for _, repo := range []string{secondClone, readerClone} {
		if got := client.must(repo, nil, nil, "log", "-r", "tip", "--template", "{node}"); got != pushed {
			t.Errorf("%s: tip %s after the writer's push, want %s", filepath.Base(repo), got, pushed)
		}
	}
	server.must(dest, nil, nil, "verify", "-q")

	// A writer that disconnects mid-upload leaves no worker and no change.
	before = server.snapshot(dest)
	stall := func() (disconnect func()) {
		n := started.Load()
		conn, err := tls.Dial("tcp", frontend.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(conn, "POST %s?cmd=unbundle HTTP/1.1\r\nHost: %s\r\nAuthorization: Basic %s\r\n"+
			"Content-Type: application/mercurial-0.1\r\nX-HgArg-1: heads=666f726365\r\nContent-Length: 1000000\r\n\r\nHG10UN",
			prefix, frontend.Listener.Addr(), base64.StdEncoding.EncodeToString([]byte("writer:writer-secret")))
		waitFor(t, "stalled push to start a worker", func() bool { return started.Load() > n })
		return func() { conn.Close() }
	}
	stall()()
	waitFor(t, "disconnected push's worker to be reaped", func() bool { return active.Load() == 0 })

	// Close ends a worker whose client is still connected but sends nothing.
	defer stall()()
	b.Close()
	waitFor(t, "Close to reap the stalled push's worker", func() bool { return active.Load() == 0 })
	if server.snapshot(dest) != before {
		t.Fatal("interrupted push changed the repository")
	}
	server.must(dest, nil, nil, "verify", "-q")
}

func port(s *httptest.Server) string {
	return s.Listener.Addr().(*net.TCPAddr).AddrPort().String()[len("127.0.0.1:"):]
}

// localhostCert returns a self-signed RSA certificate for localhost and the
// path of its PEM file for web.cacerts.
func localhostCert(t *testing.T, dir string) (tls.Certificate, string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "frontend.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, path
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * hg.StartupTimeout); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
