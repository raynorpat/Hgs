// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build integration

// Package integration contains native transport feasibility tests using an
// in-memory authorizer and configured-application acceptance tests using real
// database authorization. The stack helpers below serve only the former.
package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	gssh "github.com/gogits/gogs/modules/ssh"
)

// gogsExe is the application binary the hooks run, built once by TestMain.
var gogsExe string

func TestMain(m *testing.M) {
	executable, executableErr := os.Executable()
	if executableErr != nil {
		panic(executableErr)
	}
	hookBinary := executable + ".hook.exe"
	if len(os.Args) > 1 && os.Args[1] == hgbridge.HookCommand {
		command := exec.Command(hookBinary, os.Args[1:]...)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			if result, ok := err.(*exec.ExitError); ok {
				os.Exit(result.ExitCode())
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
	runTests := func() int {
		binary, err := os.ReadFile(gogsExe)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err = os.WriteFile(hookBinary, binary, 0700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.Remove(hookBinary)
		return m.Run()
	}
	if os.Getenv("GOGS_HG_APPLICATION_BINARY") != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "gogs-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(dir)
		if supplied := os.Getenv("GOGS_TEST_BINARY"); supplied != "" {
			gogsExe, err = filepath.Abs(supplied)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if _, err = os.Stat(gogsExe); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return runTests()
		}
		// Hook commands are quoted for the platform shell; the path has a
		// space and a non-ASCII (ANSI-representable) letter.
		name := "gogs"
		if runtime.GOOS == "windows" {
			name = "gogs.exe"
		}
		gogsExe = filepath.Join(dir, "gogs bin é", name)
		cmd := exec.Command("go", "build", "-o", gogsExe, "github.com/gogits/gogs")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build gogs: %v\n%s", err, out)
			return 1
		}
		return runTests()
	}()
	os.Exit(code)
}

func hgPath(t *testing.T) string {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG must name the Mercurial executable for integration tests")
	}
	return exe
}

// versions is one server/client row of the matrix.
type versions struct {
	name           string
	server, client string
	// legacyClient is a 2.0-era client: bundle1 and pushkey only, no phases.
	legacyClient bool
}

// matrix returns the rows to run. Rows needing an optional old Mercurial are
// logged as unavailable when their variable is unset.
func matrix(t *testing.T) []versions {
	cur := hgPath(t)
	rows := []versions{{name: "server 7.2.4, client 7.2.4", server: cur, client: cur}}
	oldServer, oldClient := os.Getenv("GOGS_TEST_HG_OLD_SERVER"), os.Getenv("GOGS_TEST_HG_OLD_CLIENT")
	if os.Getenv("GOGS_TEST_REQUIRE_LEGACY") == "1" && (oldServer == "" || oldClient == "") {
		t.Fatal("legacy CI requires both GOGS_TEST_HG_OLD_SERVER and GOGS_TEST_HG_OLD_CLIENT")
	}
	if oldServer == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_SERVER (Mercurial 5.0.x) not set; server 5.0.2 rows not run")
	} else {
		rows = append(rows, versions{name: "server 5.0.2, client 7.2.4", server: oldServer, client: cur})
	}
	if oldClient == "" {
		t.Log("unavailable: GOGS_TEST_HG_OLD_CLIENT (Mercurial 2.0.x) not set; client 2.0.2 rows not run")
	} else {
		rows = append(rows, versions{name: "server 7.2.4, client 2.0.2", server: cur, client: oldClient, legacyClient: true})
		if oldServer != "" {
			rows = append(rows, versions{name: "server 5.0.2, client 2.0.2", server: oldServer, client: oldClient, legacyClient: true})
		}
	}
	return rows
}

// hgCmd runs one Mercurial installation with its own isolated managed config.
type hgCmd struct {
	r *hg.Runner
}

func newHgCmd(t *testing.T, exe, configDir string, maxProcesses int) *hgCmd {
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(configDir, "hgrc"), MaxProcesses: maxProcesses})
	if err != nil {
		t.Fatal(err)
	}
	return &hgCmd{r: r}
}

// run returns stdout and stderr together; err is set on a non-zero exit.
func (h *hgCmd) run(ctx context.Context, repo string, args ...string) (string, error) {
	c, err := h.r.Start(ctx, repo, args, nil)
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&stderr, c.Stderr); close(done) }()
	out, _ := io.ReadAll(c.Stdout)
	<-done
	err = c.Stop(ctx)
	return string(out) + stderr.String(), err
}

func (h *hgCmd) must(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := h.run(context.Background(), repo, args...)
	if err != nil {
		t.Fatalf("hg %v: %v\n%s", redact(args), err, out)
	}
	return out
}

func (h *hgCmd) stdout(t *testing.T, repo string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := h.r.Run(context.Background(), repo, args, nil, &out); err != nil {
		t.Fatalf("hg %v: %v", redact(args), err)
	}
	return out.String()
}

// repoState is what a read-only session must never change.
type repoState struct {
	Log, Heads, Bookmarks, Tags, Obsolete string
	// Files maps every file under .hg, except locks and caches, to its hash.
	Files map[string]string
}

// snapshotRepository reads repo with independent hg commands and hashes its
// metadata files. Locks and caches change on reads and are left out.
func snapshotRepository(t *testing.T, h *hgCmd, repo string) repoState {
	t.Helper()
	s := repoState{Files: map[string]string{}}
	queries := []struct {
		dst  *string
		args []string
	}{
		{&s.Log, []string{"log", "--hidden", "-T", "json"}},
		{&s.Heads, []string{"log", "--hidden", "-r", "heads(all())", "-T", "{node}\n"}},
		{&s.Bookmarks, []string{"bookmarks", "-T", "json"}},
		{&s.Tags, []string{"tags", "-T", "json"}},
		{&s.Obsolete, []string{"debugobsolete", "--hidden"}},
	}
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out bytes.Buffer
			errs[i] = h.r.Run(context.Background(), repo, q.args, nil, &out)
			*q.dst = out.String()
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(repo, ".hg")
	err := filepath.WalkDir(meta, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(meta, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "cache" || rel == "wcache" {
				return filepath.SkipDir
			}
			return nil
		}
		switch rel {
		case "lock", "wlock", "store/lock":
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		s.Files[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// diff names the parts of two states that differ.
func (s repoState) diff(o repoState) string {
	var b strings.Builder
	for _, f := range []struct{ name, a, b string }{
		{"log", s.Log, o.Log}, {"heads", s.Heads, o.Heads}, {"bookmarks", s.Bookmarks, o.Bookmarks},
		{"tags", s.Tags, o.Tags}, {"obsolete", s.Obsolete, o.Obsolete},
	} {
		if f.a != f.b {
			fmt.Fprintf(&b, "%s:\n--- before\n%s\n--- after\n%s\n", f.name, f.a, f.b)
		}
	}
	for k, v := range s.Files {
		if o.Files[k] != v {
			fmt.Fprintf(&b, "file .hg/%s changed or removed\n", k)
		}
	}
	for k := range o.Files {
		if _, ok := s.Files[k]; !ok {
			fmt.Fprintf(&b, "file .hg/%s added\n", k)
		}
	}
	return b.String()
}

// Users of the in-memory account store. The writer may write every
// repository until revoked; the reader may only read.
const (
	writerID = 1
	readerID = 2
)

var userIDs = map[string]int64{"writer": writerID, "reader": readerID}

// authorizer is the in-memory stand-in for the database's write decision.
type authorizer struct {
	mu       sync.Mutex
	canWrite map[int64]bool
	calls    int
	// onCheck, when set, runs inside every hook callback with its sequence
	// number in the current session, after the write decision is taken.
	onCheck func(ctx context.Context, call int)
}

func (a *authorizer) writer(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.canWrite[id]
}

func (a *authorizer) setWriter(id int64, ok bool) {
	a.mu.Lock()
	a.canWrite[id] = ok
	a.mu.Unlock()
}

func (a *authorizer) authorize(ctx context.Context, g hgbridge.Grant) error {
	a.mu.Lock()
	allowed := a.canWrite[g.Principal.UserID]
	a.calls++
	call, onCheck := a.calls, a.onCheck
	a.mu.Unlock()
	if onCheck != nil {
		onCheck(ctx, call)
	}
	if !allowed {
		return errors.New("write access revoked")
	}
	return nil
}

func (a *authorizer) reset(onCheck func(context.Context, int)) {
	a.mu.Lock()
	a.calls, a.onCheck = 0, onCheck
	a.mu.Unlock()
}

func (a *authorizer) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// stack is one assembled server: Mercurial runner, controller, HTTP bridge
// behind a TLS frontend with Basic authentication, and the SSH server.
type stack struct {
	t              *testing.T
	root           string
	server, client *hgCmd
	authz          *authorizer
	ctl            *hgbridge.Controller
	http           *hgbridge.HTTPBridge
	stdio          *hgbridge.StdioBridge
	ssh            *gssh.Server

	frontend   *httptest.Server
	cacerts    string
	sshAddr    string
	knownHosts string
	hostKey    ssh.PublicKey
	keys       map[string]string
	signers    map[string]ssh.Signer

	mu       sync.Mutex
	repos    map[string]hgbridge.Target
	requests map[string]int
	sessions int
}

type stackOptions struct {
	maxProcesses int
}

func newStack(t *testing.T, v versions, opts stackOptions) *stack {
	root := t.TempDir()
	s := &stack{
		t:        t,
		root:     root,
		server:   newHgCmd(t, v.server, filepath.Join(root, "server"), opts.maxProcesses),
		client:   newHgCmd(t, v.client, filepath.Join(root, "client"), 0),
		authz:    &authorizer{canWrite: map[int64]bool{writerID: true}},
		keys:     map[string]string{},
		signers:  map[string]ssh.Signer{},
		repos:    map[string]hgbridge.Target{},
		requests: map[string]int{},
	}
	ctx := context.Background()
	if err := s.server.r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	var err error
	if s.ctl, err = hgbridge.NewController(s.authz.authorize, time.Hour); err != nil {
		t.Fatal(err)
	}
	if s.http, err = hgbridge.NewHTTPBridge(s.server.r, s.ctl, gogsExe); err != nil {
		t.Fatal(err)
	}
	if s.stdio, err = hgbridge.NewStdioBridge(s.server.r, s.ctl, gogsExe, filepath.Join(root, "server")); err != nil {
		t.Fatal(err)
	}

	cert, cacerts := localhostCert(t, root)
	s.cacerts = cacerts
	s.frontend = httptest.NewUnstartedServer(http.HandlerFunc(s.serveHTTP))
	// Mercurial matches only DNS names against the certificate.
	s.frontend.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	s.frontend.StartTLS()

	keys := filepath.Join(root, "keys")
	if err := os.Mkdir(keys, 0700); err != nil {
		t.Fatal(err)
	}
	principals := map[string]hgbridge.Principal{}
	for name, id := range userIDs {
		s.keys[name] = filepath.Join(keys, name)
		s.signers[name] = writeKey(t, s.keys[name])
		principals[string(s.signers[name].PublicKey().Marshal())] = hgbridge.Principal{UserID: id, KeyID: id}
	}
	host := newSigner(t)
	s.hostKey = host.PublicKey()
	s.ssh = gssh.NewServer(host, func(_ context.Context, k ssh.PublicKey) (hgbridge.Principal, error) {
		p, ok := principals[string(k.Marshal())]
		if !ok {
			return p, errors.New("unknown key")
		}
		return p, nil
	}, func(_ context.Context, p hgbridge.Principal, owner, repo string) (hgbridge.Target, hgbridge.Grant, error) {
		target, ok := s.target(owner, repo)
		if !ok {
			return hgbridge.Target{}, hgbridge.Grant{}, errors.New("no such repository")
		}
		s.mu.Lock()
		s.sessions++
		s.mu.Unlock()
		return target, hgbridge.Grant{Principal: p, RepoID: target.RepoID, CanWrite: s.authz.writer(p.UserID)}, nil
	}, s.stdio)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.sshAddr = ln.Addr().String()
	go s.ssh.Serve(ln)
	s.knownHosts = filepath.Join(keys, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(s.sshAddr)}, s.hostKey)
	if err := os.WriteFile(s.knownHosts, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { s.shutdown() })
	return s
}

// shutdown stops everything in the production order and then requires that
// no Mercurial or hook process of this stack is left running.
func (s *stack) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.ssh.Shutdown(ctx); err != nil {
		s.t.Errorf("SSH shutdown: %v", err)
	}
	s.frontend.Close()
	s.http.Close()
	s.stdio.Close()
	s.ctl.Close(ctx)
	if left := waitNoProcesses(s.root, filepath.Dir(filepath.Dir(gogsExe))); len(left) > 0 {
		s.t.Errorf("processes left after shutdown:\n%s", strings.Join(left, "\n"))
	} else {
		s.t.Log("process check: no Mercurial worker or hook process left after shutdown")
	}
}

func (s *stack) target(owner, repo string) (hgbridge.Target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.repos[owner+"/"+repo]
	return t, ok
}

// addRepo serves path as owner/<name>.
func (s *stack) addRepo(name, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos["owner/"+name] = hgbridge.Target{RepoID: int64(len(s.repos) + 1), Path: path}
}

var httpUsers = map[string]string{"writer": "writer-secret", "reader": "reader-secret"}

func (s *stack) serveHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/hg/")
	owner, name, _ := strings.Cut(rest, "/")
	target, found := s.target(owner, name)
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	user, pass, _ := r.BasicAuth()
	id, known := userIDs[user]
	if !known || httpUsers[user] != pass {
		w.Header().Set("WWW-Authenticate", `Basic realm="Gogs"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	s.requests[r.URL.Query().Get("cmd")]++
	s.mu.Unlock()
	grant := hgbridge.Grant{Principal: hgbridge.Principal{UserID: id, TokenID: id}, RepoID: target.RepoID, CanWrite: s.authz.writer(id)}
	prefix := "/hg/" + owner + "/" + name
	http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.http.Serve(r.Context(), w, r, target, grant); err != nil {
			s.t.Logf("Serve %s: %v", r.URL.Query().Get("cmd"), err)
			http.Error(w, "repository unavailable", http.StatusBadGateway)
		}
	})).ServeHTTP(w, r)
}

func (s *stack) requestCount(cmd string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[cmd]
}

func (s *stack) sessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions
}

const (
	transportHTTP = "https"
	transportSSH  = "ssh"
)

// remote returns the URL of repository name for user over transport and the
// client options that transport needs.
func (s *stack) remote(t *testing.T, transport, user, name string) (string, []string) {
	if transport == transportHTTP {
		port := s.frontend.Listener.Addr().(*net.TCPAddr).Port
		return fmt.Sprintf("https://%s:%s@localhost:%d/hg/owner/%s", user, httpUsers[user], port, name),
			// The managed config sends every HTTP request to a dead proxy.
			[]string{"--config", "web.cacerts=" + s.cacerts, "--config", "http_proxy.host="}
	}
	return "ssh://hg@" + s.sshAddr + "/owner/" + name, sshCommand(t, s.keys[user], s.knownHosts)
}

// sshCommand returns ui.ssh for OpenSSH using only the given key and host key
// file, never the operator's configuration or agent.
func sshCommand(t *testing.T, key, knownHosts string) []string {
	exe, err := exec.LookPath("ssh")
	var env []string
	if runtime.GOOS == "windows" {
		// Windows OpenSSH, not an MSYS build that happens to be on PATH. It
		// exits silently without ProgramData, which the runner strips.
		exe = filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe")
		_, err = os.Stat(exe)
		env = []string{"--config", "sshenv.PROGRAMDATA=" + os.Getenv("ProgramData")}
	}
	if err != nil {
		t.Fatalf("OpenSSH client for integration tests: %v", err)
	}
	return append(env, "--config", fmt.Sprintf(`ui.ssh="%s" -F none -i "%s" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes `+
		`-o StrictHostKeyChecking=yes -o UserKnownHostsFile="%s"`,
		filepath.ToSlash(exe), filepath.ToSlash(key), filepath.ToSlash(knownHosts)))
}

// writeKey saves an OpenSSH private key for the client and returns its signer.
func writeKey(t *testing.T, path string) ssh.Signer {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows OpenSSH ignores keys whose ACL grants anyone but the owner.
		u, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*"+u.Uid+":F").CombinedOutput(); err != nil {
			t.Fatalf("restrict key ACL: %v\n%s", err, out)
		}
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func newSigner(t *testing.T) ssh.Signer {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// localhostCert returns a self-signed certificate for localhost and the path
// of its PEM file for web.cacerts.
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
		NotAfter:              time.Now().Add(24 * time.Hour),
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

// processes lists command lines of running processes; tests use it to
// show that no worker or hook outlives its session.
func processes() ([]string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"[Console]::OutputEncoding=[Text.Encoding]::UTF8; Get-CimInstance Win32_Process | ForEach-Object { $_.CommandLine }")
	} else {
		cmd = exec.Command("ps", "-A", "-o", "args=")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), nil
}

// waitNoProcesses waits until no process mentions any of markers, the
// directories that worker repositories and the hook executable are under,
// and returns any left.
func waitNoProcesses(markers ...string) []string {
	var left []string
	for deadline := time.Now().Add(hg.StartupTimeout); ; time.Sleep(200 * time.Millisecond) {
		all, err := processes()
		if err != nil {
			return []string{"cannot list processes: " + err.Error()}
		}
		left = left[:0]
		for _, p := range all {
			for _, m := range markers {
				if strings.Contains(p, m) && !strings.Contains(p, "Get-CimInstance") {
					left = append(left, p)
					break
				}
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * hg.StartupTimeout); !cond(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The wire drivers below send requests the current native client never
// emits (writes inside batch, raw pushkey namespaces, stream bundles in
// unbundle). They are test code only: production never parses the protocol.

// httpWire sends one wire command as user through the HTTPS frontend and
// returns the status and body.
func (s *stack) httpWire(t *testing.T, user, name string, params url.Values, body []byte) (int, string) {
	t.Helper()
	pem, err := os.ReadFile(s.cacerts)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}}}
	defer client.CloseIdleConnections()
	u := fmt.Sprintf("https://%s/hg/owner/%s?%s", s.frontend.Listener.Addr(), name, params.Encode())
	// Commands with push permission are POSTed, as hgweb requires.
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(user, httpUsers[user])
	req.Header.Set("Content-Type", "application/mercurial-0.1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// sshWire runs one stdio session as user, sending request and closing
// input, and returns everything the server wrote.
func (s *stack) sshWire(t *testing.T, user, name string, request []byte) (stdout, stderr string) {
	t.Helper()
	c, err := ssh.Dial("tcp", s.sshAddr, &ssh.ClientConfig{
		User:            gssh.ServiceUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(s.signers[user])},
		HostKeyCallback: ssh.FixedHostKey(s.hostKey),
		Timeout:         hg.StartupTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var out, errOut bytes.Buffer
	sess.Stdin, sess.Stdout, sess.Stderr = bytes.NewReader(request), &out, &errOut
	// The exit status is reported in the output; a failed command still
	// exits the server normally.
	sess.Run("hg -R owner/" + name + " serve --stdio")
	return out.String(), errOut.String()
}

// sshCommandBytes encodes one SSH protocol v1 command with its named
// arguments, in the order given.
func sshCommandBytes(cmd string, args ...[2]string) []byte {
	b := []byte(cmd + "\n")
	for _, a := range args {
		b = fmt.Appendf(b, "%s %d\n%s", a[0], len(a[1]), a[1])
	}
	return b
}

// sshResponse returns the first length-prefixed response in out.
func sshResponse(out string) (string, bool) {
	line, rest, ok := strings.Cut(out, "\n")
	n, err := strconv.Atoi(line)
	if !ok || err != nil || n > len(rest) {
		return "", false
	}
	return rest[:n], true
}

// sshPayload frames data as the chunks an SSH unbundle reads.
func sshPayload(data []byte) []byte {
	return append(fmt.Appendf(nil, "%d\n", len(data)), append(data, "0\n"...)...)
}

// withoutJournal drops journal.desc, which Mercurial writes after pretxnopen
// passed and leaves behind when it aborts the transaction. Readers never pass
// pretxnopen, so only refused writers' states are compared without it.
func (s repoState) withoutJournal() repoState {
	s.Files = maps.Clone(s.Files)
	delete(s.Files, "journal.desc")
	return s
}

// remoteChangegroupBundle is a bundle2 with one mandatory remote-changegroup
// part asking the server to fetch a bundle from url.
func remoteChangegroupBundle(url string) []byte {
	params := [][2]string{{"url", url}, {"size", "100"}}
	header := []byte{byte(len("REMOTE-CHANGEGROUP"))}
	header = append(header, "REMOTE-CHANGEGROUP"...)
	header = binary.BigEndian.AppendUint32(header, 0)
	header = append(header, byte(len(params)), 0)
	for _, p := range params {
		header = append(header, byte(len(p[0])), byte(len(p[1])))
	}
	for _, p := range params {
		header = append(header, p[0]+p[1]...)
	}
	b := append([]byte("HG20"), 0, 0, 0, 0)
	b = binary.BigEndian.AppendUint32(b, uint32(len(header)))
	b = append(b, header...)
	b = append(b, 0, 0, 0, 0)    // empty payload
	return append(b, 0, 0, 0, 0) // end of bundle
}

var urlPassword = regexp.MustCompile(`(://[^:/@\s]+:)[^@\s]+@`)

// redact hides URL passwords in a command line before it is logged.
func redact(args []string) string {
	return urlPassword.ReplaceAllString(strings.Join(args, " "), "${1}***@")
}
