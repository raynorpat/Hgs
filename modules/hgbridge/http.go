// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/log"
)

// Target is the resolved repository of one transport request.
type Target struct {
	RepoID int64
	// Path is the absolute repository directory.
	Path string
}

// wireCommands are the top-level HTTP wire commands a worker may receive:
// the @wireprotocommand registry of mercurial/wireprotov1server.py in
// Mercurial 7.2.4, which also holds every command 2.0-era clients send
// (between, branches, changegroup, changegroupsubset, bundle1 unbundle,
// pushkey with x-hgarg headers). debugwireargs is a test aid, and
// get_cached_bundle_inline serves files from a server bundle cache Gogs never
// configures. Any other cmd value would reach hgweb's HTML interface.
var wireCommands = map[string]bool{
	"batch":                 true,
	"between":               true,
	"branchmap":             true,
	"branches":              true,
	"capabilities":          true,
	"changegroup":           true,
	"changegroupsubset":     true,
	"clonebundles":          true,
	"clonebundles_manifest": true,
	"getbundle":             true,
	"heads":                 true,
	"hello":                 true,
	"known":                 true,
	"listkeys":              true,
	"lookup":                true,
	"protocaps":             true,
	"pushkey":               true,
	"stream_out":            true,
	"unbundle":              true,
}

// HTTPBridge serves Mercurial's HTTP wire protocol by starting one private
// hg serve per request for exactly the request's repository and grant.
type HTTPBridge struct {
	controller                 *Controller
	readerConfig, writerConfig []string

	// start launches a worker; tests replace it with an in-process backend.
	start func(ctx context.Context, repoPath string, args []string, env map[string]string) (*worker, error)
}

// worker is a started backend: its loopback address, a way to tell the
// runner that proxied traffic is flowing, and reap, which kills and waits.
type worker struct {
	addr  string
	touch func()
	reap  func()
}

// NewHTTPBridge prepares workers that run hookExecutable (the absolute path
// of the application binary) as their transaction hooks.
func NewHTTPBridge(runner *hg.Runner, controller *Controller, hookExecutable string) (*HTTPBridge, error) {
	reader, err := HookConfig(hookExecutable, false)
	if err != nil {
		return nil, err
	}
	writer, err := HookConfig(hookExecutable, true)
	if err != nil {
		return nil, err
	}
	b := &HTTPBridge{
		controller:   controller,
		readerConfig: workerArgs(reader, "web.deny_push=*", "web.allow-push="),
		// Public TLS terminates in Go; the worker only listens on loopback.
		writerConfig: workerArgs(writer, "web.allow-push=*", "web.push_ssl=false"),
	}
	b.start = func(ctx context.Context, repoPath string, args []string, env map[string]string) (*worker, error) {
		return startWorker(ctx, runner, repoPath, args, env)
	}
	return b, nil
}

func workerArgs(hooks map[string]string, extra ...string) []string {
	settings := extra
	for k, v := range hooks {
		settings = append(settings, k+"="+v)
	}
	sort.Strings(settings)
	var args []string
	for _, s := range settings {
		args = append(args, "--config", s)
	}
	return append(args, "serve", "--address", "127.0.0.1", "--port", "0")
}

// Serve proxies one Mercurial wire request to a worker started for target
// with grant's rights. Call it only after the application has authorized the
// request; r's URL path must be relative to the repository's transport root,
// as http.StripPrefix leaves it. Requests that are not a single allowed wire
// command get 404 or 405 without starting a worker.
//
// An error means nothing was written and the caller should respond. Once the
// response has started, failures are logged and Serve returns nil. ctx bounds
// waiting for and starting the worker; after that the exchange is bounded by
// the runner's idle and session limits, so a push Mercurial has received runs
// to completion even if the client disconnects. Nothing is ever retried.
func (b *HTTPBridge) Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, target Target, grant Grant) error {
	cmd, query, ok := wireCommand(r.URL)
	if !ok {
		http.NotFound(w, r)
		return nil
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
	// Mercurial's server reads request bodies by Content-Length only.
	if r.ContentLength < 0 {
		http.Error(w, "length required", http.StatusLengthRequired)
		return nil
	}
	if grant.RepoID != target.RepoID {
		return errors.New("hgbridge: grant is for another repository")
	}

	args, env := b.readerConfig, map[string]string(nil)
	if grant.CanWrite {
		_, sessionEnv, revoke, err := b.controller.Issue(grant)
		if err != nil {
			return err
		}
		defer revoke()
		args, env = b.writerConfig, sessionEnv
	}
	wk, err := b.start(ctx, target.Path, args, env)
	if err != nil {
		return err
	}
	defer wk.reap()

	var body io.Reader = http.NoBody
	if r.ContentLength > 0 {
		body = &touchReader{r.Body, wk.touch}
	}
	out, err := http.NewRequest(r.Method, "http://"+wk.addr+"/?"+query, body)
	if err != nil {
		return err
	}
	out.ContentLength = r.ContentLength
	for k, v := range r.Header {
		if forwardRequestHeader(k) {
			out.Header[k] = v
		}
	}
	// One connection per worker and no keep-alive: the transport never
	// resends a request on another connection.
	transport := &http.Transport{
		DialContext:        (&net.Dialer{Timeout: hg.StartupTimeout}).DialContext,
		DisableKeepAlives:  true,
		DisableCompression: true,
	}
	resp, err := transport.RoundTrip(out)
	if err != nil {
		return fmt.Errorf("hgbridge: %s for repository %d: %v", cmd, target.RepoID, err)
	}
	defer resp.Body.Close()

	for _, k := range []string{"Content-Type", "Content-Length"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if err = copyFlushing(w, resp.Body, wk.touch); err != nil {
		log.Error(4, "hgbridge: %s response for repository %d ended early: %v", cmd, target.RepoID, err)
	}
	return nil
}

// wireCommand accepts only the transport root with exactly one allowed cmd
// and returns the query re-encoded as parsed, so the backend cannot read it
// differently.
func wireCommand(u *url.URL) (cmd, query string, ok bool) {
	if u.Path != "" && u.Path != "/" {
		return "", "", false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["cmd"]) != 1 || !wireCommands[q.Get("cmd")] {
		return "", "", false
	}
	return q.Get("cmd"), q.Encode(), true
}

// forwardRequestHeader allows only the headers Mercurial's wire protocol
// reads. Credentials, cookies, forwarding and hop-by-hop headers never reach
// a worker; Content-Length is set from the request itself.
func forwardRequestHeader(k string) bool {
	switch k = http.CanonicalHeaderKey(k); k {
	case "Content-Type", "Accept", "Accept-Encoding", "X-Hgargs-Post":
		return true
	}
	return strings.HasPrefix(k, "X-Hgarg-") || strings.HasPrefix(k, "X-Hgproto-")
}

type touchReader struct {
	r     io.Reader
	touch func()
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.touch()
	}
	return n, err
}

// copyFlushing streams a Mercurial response, which may be generated slowly,
// without buffering it.
func copyFlushing(w http.ResponseWriter, r io.Reader, touch func()) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			touch()
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if ferr := rc.Flush(); ferr != nil && !errors.Is(ferr, http.ErrNotSupported) {
				return ferr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// listeningPort matches the address hg serve announces once it is listening:
// "listening at http://<fqdn>:<port>/ (bound to 127.0.0.1:<port>)".
var listeningPort = regexp.MustCompile(`\(bound to 127\.0\.0\.1:(\d+)\)`)

const maxWorkerStderr = 4 << 10

func startWorker(ctx context.Context, runner *hg.Runner, repoPath string, args []string, env map[string]string) (*worker, error) {
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, cancel)
	c, err := runner.Start(wctx, repoPath, args, env)
	if err != nil {
		stop()
		cancel()
		return nil, err
	}

	port := make(chan string, 1)
	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		sc := bufio.NewScanner(c.Stdout)
		for sc.Scan() {
			if m := listeningPort.FindStringSubmatch(sc.Text()); m != nil {
				port <- m[1]
				break
			}
		}
		// The access log keeps arriving and must be drained.
		io.Copy(io.Discard, c.Stdout)
	}()
	stderr := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(c.Stderr, maxWorkerStderr))
		io.Copy(io.Discard, c.Stderr)
		stderr <- b
	}()
	reap := func() []byte {
		stop()
		cancel()
		c.Wait()
		<-stdoutDone
		msg := <-stderr
		c.Stdout.Close()
		c.Stderr.Close()
		return msg
	}

	timer := time.NewTimer(hg.StartupTimeout)
	defer timer.Stop()
	select {
	case p := <-port:
		// From here on only the runner's limits end the worker.
		if !stop() {
			reap()
			return nil, ctx.Err()
		}
		return &worker{addr: "127.0.0.1:" + p, touch: c.Touch, reap: func() { reap() }}, nil
	case <-stdoutDone:
		err = errors.New("exited before listening")
	case <-timer.C:
		err = errors.New("did not start listening in time")
	case <-wctx.Done():
		err = ctx.Err()
	}
	msg := reap()
	return nil, fmt.Errorf("hgbridge: hg serve %s: %v: %s", repoPath, err, strings.TrimSpace(string(msg)))
}
