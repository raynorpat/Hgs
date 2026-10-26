// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gogits/gogs/modules/log"
)

var (
	ErrQueueTimeout   = errors.New("hg: no free Mercurial process slot")
	ErrOutputLimit    = errors.New("hg: command output exceeds limit")
	ErrIdleTimeout    = errors.New("hg: Mercurial process idle too long")
	ErrSessionTimeout = errors.New("hg: Mercurial session exceeded its time limit")
	ErrStopped        = errors.New("hg: Mercurial process stopped")
)

// testedVersions are the Mercurial releases the integration matrix covers.
var testedVersions = []string{"5.0.2", "7.2.4"}

// requiredHooks are the transaction hooks the access policy installs.
var requiredHooks = []string{"pretxnopen", "pretxnclose", "pretxnclose-bookmark", "pretxnclose-phase", "prepushkey"}

// inheritedEnv is the documented allowlist of OS/runtime variables passed to
// Mercurial; platformEnv adds per-OS entries. Everything else, including all
// HG*, CHG*, RHG* and PYTHON* variables of Python 2 and 3, is dropped.
var inheritedEnv = []string{"PATH"}

// Runner starts Mercurial processes. Hook processes that Mercurial itself
// launches are not runner starts and never take a slot.
type Runner struct {
	cfg   Config
	slots chan struct{}

	// Tests shorten these; production uses the package constants.
	queueTimeout, idleTimeout time.Duration
}

func NewRunner(cfg Config) (*Runner, error) {
	if !filepath.IsAbs(cfg.Executable) {
		return nil, fmt.Errorf("hg: Mercurial executable %q is not an absolute path", cfg.Executable)
	}
	if fi, err := os.Stat(cfg.Executable); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("hg: Mercurial executable %q is missing: %v", cfg.Executable, err)
	}
	if !filepath.IsAbs(cfg.ConfigPath) || strings.ContainsRune(cfg.ConfigPath, os.PathListSeparator) {
		return nil, fmt.Errorf("hg: managed config path %q must be absolute and contain no %q", cfg.ConfigPath, os.PathListSeparator)
	}
	if err := checkPath(cfg.ConfigPath); err != nil {
		return nil, err
	}
	if cfg.MaxProcesses < 0 || cfg.SessionTimeout < 0 {
		return nil, errors.New("hg: process limits must not be negative")
	}
	if cfg.MaxProcesses == 0 {
		cfg.MaxProcesses = DefaultMaxProcesses
	}
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = DefaultSessionTimeout
	}
	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0700); err != nil {
		return nil, fmt.Errorf("hg: create managed config directory: %v", err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte(managedConfig), 0600); err != nil {
		return nil, fmt.Errorf("hg: write managed config: %v", err)
	}
	return &Runner{
		cfg:          cfg,
		slots:        make(chan struct{}, cfg.MaxProcesses),
		queueTimeout: QueueTimeout,
		idleTimeout:  TransferIdleTimeout,
	}, nil
}

// Check verifies the executable is a supported Mercurial with the features
// the application relies on.
func (r *Runner) Check(ctx context.Context) error {
	unsupported := func(format string, v ...interface{}) error {
		return fmt.Errorf("Mercurial at %s is missing or unsupported (Gogs requires Mercurial 5.0 or later): %s",
			r.cfg.Executable, fmt.Sprintf(format, v...))
	}

	out, err := r.probe(ctx, "", "version", "-T", "json")
	if err != nil {
		return unsupported("%v", err)
	}
	var info []struct {
		Ver string `json:"ver"`
	}
	if json.Unmarshal(out, &info) != nil || len(info) != 1 {
		return unsupported("unrecognized version output %q", out)
	}
	tested, err := parseVersion(info[0].Ver)
	if err != nil {
		return unsupported("%v", err)
	}
	if !tested {
		log.Warn("Mercurial %s is untested; tested releases are %s", info[0].Ver, strings.Join(testedVersions, ", "))
	}

	if out, err = r.probe(ctx, "", "help", "config.hooks"); err != nil {
		return unsupported("hook help: %v", err)
	}
	for _, h := range requiredHooks {
		if !bytes.Contains(out, []byte(`"`+h+`"`)) {
			return unsupported("hook %q is not available", h)
		}
	}

	dir, err := os.MkdirTemp(filepath.Dir(r.cfg.ConfigPath), "check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	repo := filepath.Join(dir, "repo")
	if _, err = r.probe(ctx, "", "init", repo); err != nil {
		return unsupported("init: %v", err)
	}
	if out, err = r.probe(ctx, repo, "log", "-r", "null", "-T", "json"); err != nil {
		return unsupported("JSON templates: %v", err)
	}
	var nodes []struct {
		Node string `json:"node"`
	}
	if json.Unmarshal(out, &nodes) != nil || len(nodes) != 1 || nodes[0].Node != strings.Repeat("0", 40) {
		return unsupported("unrecognized JSON template output %q", out)
	}

	log.Info("Mercurial %s: %s", info[0].Ver, r.cfg.Executable)
	return nil
}

func (r *Runner) probe(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, StartupTimeout)
	defer cancel()
	var out bytes.Buffer
	err := r.Run(ctx, repoPath, args, nil, &out)
	return out.Bytes(), err
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)(\.\d+)?([+-].*|rc\d*)?$`)

// parseVersion rejects versions below 5.0 and reports whether v is tested.
func parseVersion(v string) (tested bool, err error) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return false, fmt.Errorf("unrecognized version %q", v)
	}
	if major, _ := strconv.Atoi(m[1]); major < 5 {
		return false, fmt.Errorf("version %s is too old", v)
	}
	for _, t := range testedVersions {
		if v == t {
			return true, nil
		}
	}
	return false, nil
}

// Run runs a short command, feeding input and copying at most
// MaxCommandOutput bytes of stdout to output. Stderr is drained and its head
// is included in a failure.
func (r *Runner) Run(ctx context.Context, repoPath string, args []string, input io.Reader, output io.Writer) error {
	c, err := r.Start(ctx, repoPath, args, nil)
	if err != nil {
		return err
	}
	defer c.Stdout.Close()
	defer c.Stderr.Close()
	if output == nil {
		output = io.Discard
	}

	go func() {
		if input != nil {
			io.Copy(c.Stdin, input)
		}
		c.Stdin.Close()
	}()
	stderr := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(c.Stderr, maxStderrBytes))
		io.Copy(io.Discard, c.Stderr)
		stderr <- b
	}()

	_, copyErr := io.Copy(&limitedWriter{w: output, n: MaxCommandOutput}, c.Stdout)
	if copyErr != nil {
		c.kill(copyErr)
	}
	msg := bytes.TrimSpace(<-stderr)
	if err = c.Wait(); copyErr != nil {
		err = copyErr
	}
	if err != nil {
		if len(msg) > 0 {
			return fmt.Errorf("hg %s: %w: %s", args[0], err, msg)
		}
		return fmt.Errorf("hg %s: %w", args[0], err)
	}
	return nil
}

type limitedWriter struct {
	w io.Writer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	limited := int64(len(p)) > l.n
	if limited {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= int64(n)
	if err == nil && limited {
		err = ErrOutputLimit
	}
	return n, err
}

// Start starts Mercurial for a caller that owns the stdio copy loops. Only
// GOGS_HG_* keys are accepted in env. The process is killed when ctx ends,
// after TransferIdleTimeout without stdio activity (see Touch), or after the
// session ceiling. Callers that do not Stop the child must close Stdout and
// Stderr after reading them.
func (r *Runner) Start(ctx context.Context, repoPath string, args []string, env map[string]string) (*Child, error) {
	cmd, err := r.command(repoPath, args, env)
	if err != nil {
		return nil, err
	}

	timer := time.NewTimer(r.queueTimeout)
	defer timer.Stop()
	select {
	case r.slots <- struct{}{}:
	case <-timer.C:
		return nil, ErrQueueTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c, err := startChild(cmd)
	if err != nil {
		<-r.slots
		return nil, fmt.Errorf("hg: start Mercurial: %v", err)
	}
	go func() {
		c.err = c.tree.wait(cmd)
		c.Stdin.Close()
		<-r.slots
		close(c.done)
	}()
	go c.watch(ctx, r.idleTimeout, r.cfg.SessionTimeout)
	return c, nil
}

func (r *Runner) command(repoPath string, args []string, env map[string]string) (*exec.Cmd, error) {
	dir := filepath.Dir(r.cfg.ConfigPath)
	if repoPath != "" {
		if !filepath.IsAbs(repoPath) {
			return nil, fmt.Errorf("hg: repository path %q is not absolute", repoPath)
		}
		if err := checkPath(repoPath); err != nil {
			return nil, err
		}
		dir = repoPath
		args = append([]string{"--repository", repoPath}, args...)
	}

	environ := []string{
		"HGRCPATH=" + r.cfg.ConfigPath,
		"HGPLAIN=1",
		"HGENCODING=" + localEncoding(),
		"PYTHONNOUSERSITE=1",
	}
	for _, k := range append(inheritedEnv, platformEnv...) {
		if v, ok := os.LookupEnv(k); ok {
			environ = append(environ, k+"="+v)
		}
	}
	for k, v := range env {
		if !strings.HasPrefix(k, "GOGS_HG_") || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("hg: environment variable %q is not allowed", k)
		}
		environ = append(environ, k+"="+v)
	}

	cmd := exec.Command(r.cfg.Executable, args...)
	cmd.Dir = dir
	cmd.Env = environ
	return cmd, nil
}

// Child is a running Mercurial process tree.
type Child struct {
	Stdin          io.WriteCloser
	Stdout, Stderr io.ReadCloser

	tree    *processTree
	started time.Time
	active  atomic.Int64 // monotonic time since started of the last stdio activity

	done  chan struct{}
	err   error
	mu    sync.Mutex
	cause error
}

func startChild(cmd *exec.Cmd) (*Child, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	tree, err := startTree(cmd)
	inR.Close()
	outW.Close()
	errW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		errR.Close()
		return nil, err
	}

	c := &Child{tree: tree, started: time.Now(), done: make(chan struct{})}
	c.Stdin = &pipeWriter{inW, c}
	c.Stdout = &pipeReader{outR, c}
	c.Stderr = &pipeReader{errR, c}
	return c, nil
}

// Touch records activity that does not pass through stdio, such as traffic
// a caller proxies to a backend socket.
func (c *Child) Touch() {
	c.active.Store(int64(time.Since(c.started)))
}

func (c *Child) idle() time.Duration {
	return time.Since(c.started) - time.Duration(c.active.Load())
}

func (c *Child) watch(ctx context.Context, idle, session time.Duration) {
	sessionTimer := time.NewTimer(session)
	defer sessionTimer.Stop()
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ctx.Done():
			c.kill(ctx.Err())
			return
		case <-sessionTimer.C:
			c.kill(ErrSessionTimeout)
			return
		case <-idleTimer.C:
			if left := idle - c.idle(); left > 0 {
				idleTimer.Reset(left)
				continue
			}
			c.kill(ErrIdleTimeout)
			return
		}
	}
}

// kill ends the whole process tree; the first cause is what Wait reports.
func (c *Child) kill(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	if c.cause == nil {
		c.cause = cause
		c.tree.kill()
	}
}

// Wait waits until the process and its descendants are gone. It reports the
// kill cause (context error, ErrIdleTimeout, ErrSessionTimeout, ErrStopped,
// ErrOutputLimit) in preference to the exit status.
func (c *Child) Wait() error {
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cause != nil {
		return c.cause
	}
	return c.err
}

// Stop closes stdin, gives the child until ctx ends (at most ShutdownTimeout)
// to exit, then kills the tree. It returns Wait's result.
func (c *Child) Stop(ctx context.Context) error {
	c.Stdin.Close()
	ctx, cancel := context.WithTimeout(ctx, ShutdownTimeout)
	defer cancel()
	select {
	case <-c.done:
	case <-ctx.Done():
		c.kill(ErrStopped)
	}
	err := c.Wait()
	c.Stdout.Close()
	c.Stderr.Close()
	return err
}

type pipeReader struct {
	f *os.File
	c *Child
}

func (p *pipeReader) Read(b []byte) (int, error) {
	n, err := p.f.Read(b)
	if n > 0 {
		p.c.Touch()
	}
	return n, err
}

func (p *pipeReader) Close() error { return p.f.Close() }

type pipeWriter struct {
	f *os.File
	c *Child
}

func (p *pipeWriter) Write(b []byte) (int, error) {
	n, err := p.f.Write(b)
	if n > 0 {
		p.c.Touch()
	}
	return n, err
}

func (p *pipeWriter) Close() error { return p.f.Close() }
