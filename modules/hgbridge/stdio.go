// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

// StdioBridge serves Mercurial's stdio wire protocol, as SSH sessions carry
// it, through one private hg serve --stdio per session.
type StdioBridge struct {
	runner     *hg.Runner
	controller *Controller
	// rc holds the session config file of readers (false) and writers (true).
	rc map[bool]string

	// base ends every worker when Close cancels it.
	base       context.Context
	cancelBase context.CancelFunc
}

// NewStdioBridge prepares workers that run hookExecutable (the absolute path
// of the application binary) as their transaction hooks. Mercurial serves
// stdio only for exactly "-R <repo> serve --stdio", so the hooks go into two
// session config files, rewritten here, in configDir: an absolute directory
// owned by the application, such as the managed config's.
func NewStdioBridge(runner *hg.Runner, controller *Controller, hookExecutable, configDir string) (*StdioBridge, error) {
	if !filepath.IsAbs(configDir) {
		return nil, fmt.Errorf("hgbridge: stdio config directory %q is not absolute", configDir)
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("hgbridge: create stdio config directory: %v", err)
	}
	b := &StdioBridge{runner: runner, controller: controller, rc: map[bool]string{}}
	for canWrite, name := range map[bool]string{false: "stdio-reader.rc", true: "stdio-writer.rc"} {
		hooks, err := HookConfig(hookExecutable, canWrite)
		if err != nil {
			return nil, err
		}
		// Over stdio the hooks alone decide writes; there is no web.allow-push.
		var lines []string
		for k, v := range hooks {
			lines = append(lines, strings.TrimPrefix(k, "hooks.")+" = "+v+"\n")
		}
		sort.Strings(lines)
		// Command-line --config values reach Mercurial already in its
		// encoding; file contents must be written in it.
		rc, err := hg.LocalBytes("[hooks]\n" + strings.Join(lines, ""))
		if err != nil {
			return nil, err
		}
		path := filepath.Join(configDir, name)
		if err = os.WriteFile(path, rc, 0600); err != nil {
			return nil, fmt.Errorf("hgbridge: write stdio session config: %v", err)
		}
		b.rc[canWrite] = path
	}
	b.base, b.cancelBase = context.WithCancel(context.Background())
	return b, nil
}

// Close kills every running worker, so in-flight pushes may be cut off
// mid-transaction; later ServeStdio calls fail. Call it after the shutdown
// drain period.
func (b *StdioBridge) Close() {
	b.cancelBase()
}

// ServeStdio runs one worker for target with grant's rights, connected to
// input, output and stderr, until it exits. Call it only after the
// application has authorized the session.
//
// exitCode is the worker's exit status. When the worker could not start or
// was killed (by ctx, Close, a failed write to output or stderr, or the
// runner's idle and session limits) err is set and exitCode is -1; a killed
// transaction is left to Mercurial's recovery. ServeStdio returns once the
// worker has exited and its output is copied, waiting at most
// hg.ShutdownTimeout for a stalled output; a read from input may still be
// pending, so the caller must close input's source afterwards.
func (b *StdioBridge) ServeStdio(ctx context.Context, target Target, grant Grant, input io.Reader, output, stderr io.Writer) (exitCode int, err error) {
	if grant.RepoID != target.RepoID {
		return -1, errors.New("hgbridge: grant is for another repository")
	}
	if b.base.Err() != nil {
		return -1, errors.New("hgbridge: stdio bridge is closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(b.base, cancel)()

	rc, env := b.rc[false], map[string]string(nil)
	if grant.CanWrite {
		_, sessionEnv, revoke, err := b.controller.Issue(grant)
		if err != nil {
			return -1, err
		}
		defer revoke()
		rc, env = b.rc[true], sessionEnv
	}
	c, err := b.runner.StartWithConfig(ctx, target.Path, []string{"serve", "--stdio"}, env, rc)
	if err != nil {
		return -1, err
	}

	go func() {
		io.Copy(c.Stdin, input)
		c.Stdin.Close()
	}()
	var copies sync.WaitGroup
	relay := func(w io.Writer, r io.Reader) {
		defer copies.Done()
		if _, err := io.Copy(w, r); err != nil {
			// Nobody receives the worker's output any more.
			cancel()
			io.Copy(io.Discard, r)
		}
	}
	copies.Add(2)
	go relay(output, c.Stdout)
	go relay(stderr, c.Stderr)
	copied := make(chan struct{})
	go func() { copies.Wait(); close(copied) }()

	err = c.Wait()
	timer := time.NewTimer(hg.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-copied:
	case <-timer.C:
	}
	c.Stdout.Close()
	c.Stderr.Close()

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
