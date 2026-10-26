// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package hg

import "golang.org/x/sys/unix"

// waitExited blocks until pid exits, leaving it unreaped.
func waitExited(pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kq)
	ev := make([]unix.Kevent_t, 1)
	unix.SetKevent(&ev[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	ev[0].Fflags = unix.NOTE_EXIT
	if _, err = unix.Kevent(kq, ev, nil, nil); err == unix.ESRCH {
		// Already a zombie; it is ours because we have not reaped it.
		return nil
	} else if err != nil {
		return err
	}
	for {
		if _, err = unix.Kevent(kq, nil, ev, nil); err != unix.EINTR {
			return err
		}
	}
}
