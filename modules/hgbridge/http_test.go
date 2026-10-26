// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testReader = Grant{Principal: Principal{UserID: 2, TokenID: 5}, RepoID: 9}
	testWriter = Grant{Principal: Principal{UserID: 1, TokenID: 4}, RepoID: 9, CanWrite: true}
	testTarget = Target{RepoID: 9, Path: `C:\repos\9`}
)

// fakeWorkers replaces hg serve with an in-process backend and records every
// worker the bridge starts.
type fakeWorkers struct {
	mu      sync.Mutex
	args    [][]string
	envs    []map[string]string
	started int
	reaped  int
	// idle, when set, kills a worker without touches for that long, as the
	// runner's idle watchdog does.
	idle   time.Duration
	killed int
}

func (f *fakeWorkers) count() (started, reaped int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.reaped
}

func newTestBridge(t *testing.T, backend http.HandlerFunc) (*HTTPBridge, *Controller, *fakeWorkers) {
	t.Helper()
	ctl, err := NewController(func(context.Context, Grant) error { return nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctl.Close(context.Background()) })
	exe, _ := os.Executable()
	b, err := NewHTTPBridge(nil, ctl, exe)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeWorkers{}
	b.start = func(ctx, base context.Context, repoPath string, args []string, env map[string]string) (*worker, error) {
		if repoPath != testTarget.Path {
			t.Errorf("worker for %q, want %q", repoPath, testTarget.Path)
		}
		srv := httptest.NewServer(backend)
		f.mu.Lock()
		f.args = append(f.args, args)
		f.envs = append(f.envs, env)
		f.started++
		idle := f.idle
		f.mu.Unlock()
		var last atomic.Int64
		touch := func() { last.Store(time.Now().UnixNano()) }
		touch()
		stop, done := make(chan struct{}), make(chan struct{})
		var exit sync.Once
		if idle > 0 {
			go func() {
				for {
					select {
					case <-stop:
						return
					case <-time.After(idle / 10):
					}
					if time.Since(time.Unix(0, last.Load())) > idle {
						f.mu.Lock()
						f.killed++
						f.mu.Unlock()
						srv.CloseClientConnections()
						exit.Do(func() { close(done) })
						return
					}
				}
			}()
		}
		return &worker{addr: srv.Listener.Addr().String(), touch: touch, done: done, reap: func() {
			close(stop)
			srv.Close()
			exit.Do(func() { close(done) })
			f.mu.Lock()
			f.reaped++
			f.mu.Unlock()
		}}, nil
	}
	return b, ctl, f
}

// countingWriter records every response the bridge starts and can fail
// writes the way a disconnected client does.
type countingWriter struct {
	*httptest.ResponseRecorder
	headers   int
	failWrite bool
}

func (c *countingWriter) WriteHeader(code int) {
	c.headers++
	c.ResponseRecorder.WriteHeader(code)
}

func (c *countingWriter) Write(b []byte) (int, error) {
	if c.headers == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if c.failWrite {
		return 0, errors.New("client went away")
	}
	return c.ResponseRecorder.Write(b)
}

func serve(t *testing.T, b *HTTPBridge, r *http.Request, g Grant) (*countingWriter, error) {
	t.Helper()
	w := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
	err := b.Serve(context.Background(), w, r, testTarget, g)
	return w, err
}

func wireRequest(method, target, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	return httptest.NewRequest(method, "https://gogs.example"+target, rd)
}

func TestHTTPBridgeChallengesAnonymousBackendDenial(t *testing.T) {
	for _, grant := range []Grant{{RepoID: 9}, testReader, testWriter} {
		b, _, workers := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "denied by Mercurial", http.StatusForbidden)
		})
		w, err := serve(t, b, wireRequest("POST", "?cmd=unbundle", "bundle"), grant)
		want := http.StatusForbidden
		if grant.Principal.UserID == 0 {
			want = http.StatusUnauthorized
		}
		if err != nil || w.Code != want {
			t.Fatalf("grant %+v: status=%d err=%v", grant, w.Code, err)
		}
		if (w.Header().Get("WWW-Authenticate") != "") != (want == http.StatusUnauthorized) {
			t.Fatal("incorrect authentication challenge")
		}
		if started, reaped := workers.count(); started != 1 || reaped != 1 {
			t.Fatalf("backend request replayed or leaked: %d/%d", started, reaped)
		}
		if grant.Principal.UserID == 0 && (len(workers.envs[0]) != 0 || !hasArg(workers.args[0], "web.deny_push=*")) {
			t.Fatal("anonymous worker gained mutation authority")
		}
	}
}

func hasArg(args []string, want string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--config" && args[i+1] == want {
			return true
		}
	}
	return false
}

func TestHTTPBridgeKeepsPrincipalsSeparate(t *testing.T) {
	b, ctl, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })

	for _, g := range []Grant{testWriter, testWriter, testReader} {
		if w, err := serve(t, b, wireRequest("GET", "?cmd=capabilities", ""), g); err != nil || w.Code != 200 {
			t.Fatalf("capabilities: %d, %v", w.Code, err)
		}
	}
	if started, reaped := f.count(); started != 3 || reaped != 3 {
		t.Fatalf("started %d workers and reaped %d, want 3 each", started, reaped)
	}

	w1, w2, readerEnv := f.envs[0][EnvCapability], f.envs[1][EnvCapability], f.envs[2]
	if w1 == "" || w2 == "" || w1 == w2 {
		t.Fatal("writer requests did not get distinct capabilities")
	}
	if readerEnv[EnvCapability] != "" || len(readerEnv) != 0 {
		t.Fatalf("reader worker got session environment %v", readerEnv)
	}
	for _, c := range []string{w1, w2} {
		if ctl.Check(context.Background(), c, testWriter.RepoID, "pretxnopen") == nil {
			t.Fatal("writer capability outlived its request")
		}
	}

	exe, _ := os.Executable()
	writerHooks, _ := HookConfig(exe, true)
	readerHooks, _ := HookConfig(exe, false)
	for i, want := range []map[string]string{writerHooks, writerHooks, readerHooks} {
		for k, v := range want {
			if !hasArg(f.args[i], k+"="+v) {
				t.Errorf("worker %d lacks hook %s=%s", i, k, v)
			}
		}
		if !hasArg(f.args[i], "web.allow-push=*") != (i == 2) {
			t.Errorf("worker %d: push enabled = %v, want %v", i, i != 2, i != 2)
		}
	}
	if !hasArg(f.args[0], "web.push_ssl=false") || !hasArg(f.args[2], "web.deny_push=*") || !hasArg(f.args[2], "web.allow-push=") {
		t.Errorf("unexpected push configuration: writer %q, reader %q", f.args[0], f.args[2])
	}
	if hasArg(f.args[2], "web.push_ssl=false") {
		t.Error("reader worker disables push_ssl")
	}

	other := testWriter
	other.RepoID = 10
	if _, err := serve(t, b, wireRequest("GET", "?cmd=capabilities", ""), other); err == nil {
		t.Fatal("grant for another repository served")
	}
	anonymousWriter := testWriter
	anonymousWriter.Principal = Principal{}
	if _, err := serve(t, b, wireRequest("GET", "?cmd=capabilities", ""), anonymousWriter); err == nil {
		t.Fatal("anonymous writer served")
	}
	if started, _ := f.count(); started != 3 {
		t.Fatal("rejected grant started a worker")
	}
}

func TestHTTPBridgeDoesNotReplayPush(t *testing.T) {
	var mu sync.Mutex
	var forwarded http.Header
	deliveredWrites, mode := 0, ""
	delivered := func() int { mu.Lock(); defer mu.Unlock(); return deliveredWrites }
	b, _, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "unbundle" {
			return
		}
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		deliveredWrites++
		forwarded = r.Header.Clone()
		mu.Unlock()
		switch mode {
		case "crash before response":
			panic(http.ErrAbortHandler)
		case "crash after commit":
			w.Header().Set("Content-Length", "100")
			io.WriteString(w, "1\npartial")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		default:
			io.WriteString(w, "1\nadded 1 changeset\n")
		}
	})
	push := func() *http.Request {
		r := wireRequest("POST", "/?cmd=unbundle", "HG10UN changegroup")
		r.Header.Set("Content-Type", "application/mercurial-0.1")
		r.Header.Set("Authorization", "Basic d3JpdGVyOnNlY3JldA==")
		return r
	}

	for _, tc := range []struct {
		mode      string
		failWrite bool
		wantErr   bool
	}{
		{"crash before response", false, true},
		{"crash after commit", false, false},
		{"client disconnects", true, false},
	} {
		mu.Lock()
		mode, deliveredWrites = tc.mode, 0
		mu.Unlock()
		w := &countingWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: tc.failWrite}
		err := b.Serve(context.Background(), w, push(), testTarget, testWriter)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Serve error %v, want error %v", tc.mode, err, tc.wantErr)
		}
		if tc.wantErr && w.headers != 0 {
			t.Errorf("%s: wrote a response and returned an error", tc.mode)
		}
		if !tc.wantErr && w.headers != 1 {
			t.Errorf("%s: wrote %d responses, want 1", tc.mode, w.headers)
		}
		if delivered() != 1 {
			t.Fatalf("%s: backend received the push %d times", tc.mode, delivered())
		}
	}
	if started, reaped := f.count(); started != 3 || reaped != 3 {
		t.Fatalf("started %d workers, reaped %d; want one per push", started, reaped)
	}

	serve(t, b, wireRequest("GET", "?cmd=capabilities", ""), testReader)
	mu.Lock()
	defer mu.Unlock()
	readerEnv := f.envs[len(f.envs)-1]
	if deliveredWrites != 1 || forwarded.Get("Authorization") != "" || readerEnv["GOGS_HG_CAPABILITY"] != "" {
		t.Fatal("HTTP worker replayed a write or crossed an identity boundary")
	}
}

func TestHTTPBridgeStripsCredentials(t *testing.T) {
	var forwarded http.Header
	var body, query, path string
	b, _, _ := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		body, query, path = string(b), r.URL.RawQuery, r.URL.Path
		w.Header().Set("Content-Type", "application/mercurial-0.2")
		w.Header().Set("WWW-Authenticate", `Basic realm="mercurial"`)
		w.Header().Set("Set-Cookie", "backend=1")
		w.WriteHeader(http.StatusUnauthorized)
	})

	r := wireRequest("POST", "?cmd=batch", "cmds=heads+%3Bknown+nodes%3D")
	for k, v := range map[string]string{
		"Authorization":       "Basic d3JpdGVyOnNlY3JldA==",
		"Proxy-Authorization": "Basic cHJveHk6c2VjcmV0",
		"Cookie":              "i_like_gogs=session",
		"Forwarded":           "for=203.0.113.9;proto=https",
		"X-Forwarded-For":     "203.0.113.9",
		"X-Forwarded-Proto":   "https",
		"X-Forwarded-Host":    "gogs.example",
		"X-Real-IP":           "203.0.113.9",
		"X-Remote-User":       "admin",
		"Connection":          "X-Secret",
		"X-Secret":            "hop",
		"Te":                  "trailers",
		"Content-Type":        "application/mercurial-0.1",
		"Accept-Encoding":     "identity",
		"X-HgArg-1":           "cmds=heads",
		"X-HgProto-1":         "0.1 0.2 comp=zstd,zlib,none,bzip2",
		"X-HgArgs-Post":       "28",
	} {
		r.Header.Set(k, v)
	}
	w, err := serve(t, b, r, testWriter)
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Forwarded", "X-Forwarded-For",
		"X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-Ip", "X-Remote-User", "X-Secret", "Te"} {
		if v := forwarded.Get(k); v != "" {
			t.Errorf("forwarded %s: %q", k, v)
		}
	}
	if c := forwarded.Get("Connection"); c != "close" {
		t.Errorf("forwarded Connection: %q, want only the worker connection's own close", c)
	}
	for k, v := range map[string]string{
		"Content-Type":    "application/mercurial-0.1",
		"Accept-Encoding": "identity",
		"X-Hgarg-1":       "cmds=heads",
		"X-Hgproto-1":     "0.1 0.2 comp=zstd,zlib,none,bzip2",
		"X-Hgargs-Post":   "28",
		"Content-Length":  "28",
	} {
		if got := forwarded.Get(k); got != v {
			t.Errorf("forwarded %s = %q, want %q", k, got, v)
		}
	}
	if body != "cmds=heads+%3Bknown+nodes%3D" || query != "cmd=batch" || path != "/" {
		t.Errorf("forwarded %s?%s body %q", path, query, body)
	}

	if w.Code != http.StatusUnauthorized || w.Header().Get("Content-Type") != "application/mercurial-0.2" {
		t.Errorf("response %d %v", w.Code, w.Header())
	}
	if w.Header().Get("WWW-Authenticate") != "" || w.Header().Get("Set-Cookie") != "" {
		t.Errorf("backend authentication or cookie headers reached the client: %v", w.Header())
	}
}

func TestHTTPBridgeHidesBackendUI(t *testing.T) {
	b, _, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>backend</html>")
	})

	for _, tc := range []struct {
		method, target string
		code           int
	}{
		{"GET", "", 404},
		{"GET", "/", 404},
		{"GET", "?style=raw", 404},
		{"GET", "/static/style.css", 404},
		{"GET", "/static/style.css?cmd=heads", 404},
		{"GET", "/rev/tip?cmd=heads", 404},
		{"GET", "/.hg/hgrc?cmd=heads", 404},
		{"GET", "//?cmd=heads", 404},
		{"GET", "/%2e?cmd=heads", 404},
		{"GET", "?cmd=archive&node=tip&type=zip", 404},
		{"GET", "?cmd=filelog&file=x", 404},
		{"GET", "?cmd=static&file=style.css", 404},
		{"GET", "?cmd=debugwireargs&one=1&two=2", 404},
		{"GET", "?cmd=get_cached_bundle_inline&path=x", 404},
		{"GET", "?cmd=nonexistent", 404},
		{"GET", "?cmd=", 404},
		{"GET", "?cmd=heads&cmd=heads", 404},
		{"GET", "?cmd=heads&cmd=archive", 404},
		{"GET", "?cmd=heads;cmd=archive", 404},
		{"GET", "?cmd=Heads", 404},
		{"GET", "?cmd=heads%00", 404},
		{"PUT", "?cmd=unbundle", 405},
		{"HEAD", "?cmd=heads", 405},
		{"OPTIONS", "?cmd=capabilities", 405},
	} {
		w, err := serve(t, b, wireRequest(tc.method, tc.target, ""), testReader)
		if err != nil || w.Code != tc.code || strings.Contains(w.Body.String(), "backend") {
			t.Errorf("%s %q: %d %q, %v; want %d", tc.method, tc.target, w.Code, w.Body, err, tc.code)
		}
	}

	r := wireRequest("POST", "?cmd=unbundle", "")
	r.Body, r.ContentLength = io.NopCloser(strings.NewReader("data")), -1
	if w, err := serve(t, b, r, testWriter); err != nil || w.Code != http.StatusLengthRequired {
		t.Errorf("unknown-length body: %d, %v", w.Code, err)
	}
	if started, _ := f.count(); started != 0 {
		t.Fatalf("rejected requests started %d workers", started)
	}

	for _, target := range []string{"?cmd=heads", "/?cmd=heads", "?cmd=getbundle&common=00", "?cmd=stream_out"} {
		if w, err := serve(t, b, wireRequest("GET", target, ""), testReader); err != nil || w.Code != 200 {
			t.Errorf("GET %q: %d, %v", target, w.Code, err)
		}
	}
}

func TestHTTPBridgeCommandTable(t *testing.T) {
	for _, cmd := range []string{"batch", "between", "branchmap", "branches", "capabilities", "changegroup",
		"changegroupsubset", "clonebundles", "clonebundles_manifest", "getbundle", "heads", "hello", "known",
		"listkeys", "lookup", "protocaps", "pushkey", "stream_out", "unbundle"} {
		if !wireCommands[cmd] {
			t.Errorf("wire command %q is not allowed", cmd)
		}
	}
}

func TestHTTPBridgeHoldsWorkerDuringTransaction(t *testing.T) {
	b, _, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		// Applying the changegroup and running hooks: no traffic at all.
		time.Sleep(time.Second)
		io.WriteString(w, "1\nadded 1 changeset\n")
	})
	f.idle, b.holdInterval = 150*time.Millisecond, 20*time.Millisecond

	w, err := serve(t, b, wireRequest("POST", "?cmd=unbundle", "HG10UN changegroup"), testWriter)
	if err != nil || w.Code != 200 || w.Body.String() != "1\nadded 1 changeset\n" {
		t.Fatalf("long transaction: %d %q, %v", w.Code, w.Body, err)
	}
	if f.killed != 0 {
		t.Fatal("idle limit killed a worker applying a fully received push")
	}
	for _, target := range []string{"?cmd=getbundle", "?cmd=heads"} {
		if w, err := serve(t, b, wireRequest("GET", target, ""), testReader); err != nil || w.Code != 200 || f.killed != 0 {
			t.Fatalf("GET %s while the worker computes: %d, %v, killed %d", target, w.Code, err, f.killed)
		}
	}

	// A client that stops sending its body leaves the worker idle. A real
	// frontend connection shows Serve does not wait for the stalled client.
	done := make(chan error, 1)
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- b.Serve(context.Background(), w, r, testTarget, testWriter)
	}))
	defer frontend.Close()
	pr, pw := io.Pipe()
	defer pw.Close()
	go pw.Write([]byte("HG10UN"))
	r, _ := http.NewRequest("POST", frontend.URL+"?cmd=unbundle", pr)
	r.ContentLength = 1000
	go http.DefaultClient.Do(r)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled upload served")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled upload was not ended by the idle limit")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.killed != 1 || f.started != f.reaped {
		t.Fatalf("killed %d, started %d, reaped %d", f.killed, f.started, f.reaped)
	}
}

// Hook callbacks alone keep a writer's worker alive, as they do over stdio.
func TestHTTPBridgeHookCallbacksAreActivity(t *testing.T) {
	var ctl *Controller
	var f *fakeWorkers
	b, ctl, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		capability := f.envs[len(f.envs)-1][EnvCapability]
		f.mu.Unlock()
		for i := 0; i < 10; i++ {
			time.Sleep(100 * time.Millisecond)
			if err := ctl.Check(r.Context(), capability, testWriter.RepoID, "pretxnclose"); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
		}
		io.WriteString(w, "ok")
	})
	f.idle, b.holdInterval = 300*time.Millisecond, time.Hour

	w, err := serve(t, b, wireRequest("POST", "?cmd=unbundle", "HG10UN changegroup"), testWriter)
	if err != nil || w.Code != 200 || f.killed != 0 {
		t.Fatalf("worker making callbacks: %d %q, %v, killed %d", w.Code, w.Body, err, f.killed)
	}
}

func TestHTTPBridgeCloseEndsWorkers(t *testing.T) {
	entered := make(chan struct{}, 1)
	b, _, f := newTestBridge(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	})

	done := make(chan error, 1)
	go func() {
		_, err := serve(t, b, wireRequest("POST", "?cmd=unbundle", "HG10UN changegroup"), testWriter)
		done <- err
	}()
	<-entered
	b.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve reported success for an exchange Close aborted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end an in-flight exchange")
	}
	if _, err := serve(t, b, wireRequest("GET", "?cmd=heads", ""), testReader); err == nil {
		t.Fatal("closed bridge served a request")
	}
	if started, reaped := f.count(); started != 1 || reaped != 1 {
		t.Fatalf("started %d, reaped %d", started, reaped)
	}
}
