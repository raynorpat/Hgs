// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package hg

import (
	"os/exec"
	"sync"
	"syscall"
)

var platformEnv = []string{"TMPDIR", "LANG", "LC_ALL", "LC_CTYPE"}

// processTree is the child's process group. The group is signalled only while
// the leader is unreaped: its zombie keeps the pid, and so the group ID, from
// being reused by an unrelated process.
type processTree struct {
	mu     sync.Mutex
	pgid   int
	reaped bool
}

func startTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{pgid: cmd.Process.Pid}, nil
}

func (t *processTree) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.reaped {
		syscall.Kill(-t.pgid, syscall.SIGKILL)
	}
}

// wait waits for the leader without reaping it, kills descendants it left
// behind, then reaps it.
func (t *processTree) wait(cmd *exec.Cmd) error {
	err := waitExited(t.pgid)
	t.mu.Lock()
	syscall.Kill(-t.pgid, syscall.SIGKILL)
	t.reaped = true
	t.mu.Unlock()
	if werr := cmd.Wait(); err == nil {
		err = werr
	}
	return err
}

// CheckPath accepts every path: Mercurial uses byte paths on Unix.
func CheckPath(string) error { return nil }

func localEncoding() string { return "utf-8" }

// LocalBytes encodes s as Mercurial reads configuration files: in HGENCODING,
// which is UTF-8 here.
func LocalBytes(s string) ([]byte, error) { return []byte(s), nil }
