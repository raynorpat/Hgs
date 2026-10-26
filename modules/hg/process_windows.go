// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hg

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformEnv: the Python runtime needs SystemRoot, and Mercurial runs hooks
// through %ComSpec%.
var platformEnv = []string{"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE"}

// processTree is a Job Object holding the child and every descendant; Windows
// does not let them leave it, and closing the handle kills them all.
type processTree struct {
	mu     sync.Mutex
	job    windows.Handle
	closed bool
}

func startTree(cmd *exec.Cmd) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}

	// Start suspended so nothing can spawn before the job is assigned.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW,
	}
	if err = cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if err = assignAndResume(job, uint32(cmd.Process.Pid)); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	return &processTree{job: job}, nil
}

// assignAndResume relies on cmd holding the process handle, so the pid
// cannot be reused before the process is waited for.
func assignAndResume(job windows.Handle, pid uint32) error {
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	err = windows.AssignProcessToJobObject(job, p)
	windows.CloseHandle(p)
	if err != nil {
		return err
	}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	te := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(th)
		windows.CloseHandle(th)
		if err != nil {
			return err
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return err
	}
	return nil
}

func (t *processTree) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		windows.TerminateJobObject(t.job, 1)
	}
}

// wait reaps the child, then kills any descendants it left behind.
func (t *processTree) wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	t.mu.Lock()
	defer t.mu.Unlock()
	windows.TerminateJobObject(t.job, 1)
	windows.CloseHandle(t.job)
	t.closed = true
	return err
}

var procWideCharToMultiByte = windows.NewLazySystemDLL("kernel32.dll").NewProc("WideCharToMultiByte")

// checkPath rejects paths outside the ANSI code page: Mercurial on Windows
// encodes file names with it, and would otherwise fail or mangle them.
func checkPath(p string) error {
	const cpUTF8, wcNoBestFitChars = 65001, 0x400
	if windows.GetACP() == cpUTF8 {
		return nil
	}
	u, err := windows.UTF16FromString(p)
	if err != nil {
		return err
	}
	var usedDefault int32
	n, _, _ := procWideCharToMultiByte.Call(0, wcNoBestFitChars, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)),
		0, 0, 0, uintptr(unsafe.Pointer(&usedDefault)))
	if n == 0 || usedDefault != 0 {
		return fmt.Errorf("hg: path %q is not representable in Windows code page %d, which Mercurial uses for file names", p, windows.GetACP())
	}
	return nil
}

// localEncoding is the ANSI code page: Mercurial decodes the file names it
// receives with HGENCODING, and on Windows those are ANSI-encoded bytes.
func localEncoding() string {
	if acp := windows.GetACP(); acp != 65001 {
		return fmt.Sprintf("cp%d", acp)
	}
	return "utf-8"
}
