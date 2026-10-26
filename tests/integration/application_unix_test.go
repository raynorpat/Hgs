//go:build integration && !windows

package integration

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func signalApplicationStop(pid int, binary, config string) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil || !strings.Contains(string(out), binary) || !strings.Contains(string(out), config) {
		return false
	}
	return syscall.Kill(pid, syscall.SIGTERM) == nil
}

// Native Mercurial workers own process groups distinct from the web process.
// Graceful application shutdown should reap all of them; on test failure this
// fallback kills only descendants identified by this fixture's unique paths.
func reapApplicationOrphans(markers ...string) ([]int, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,lstart=,args=").Output()
	if err != nil {
		return nil, err
	}
	var killed []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		for _, marker := range markers {
			if strings.Contains(strings.Join(fields[1:], " "), marker) {
				// Recheck PID, start time and command immediately before killing:
				// a reparented/reused PID must not target an unrelated process.
				current, e := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=,lstart=,args=").Output()
				if e != nil || strings.Join(strings.Fields(string(current)), " ") != strings.Join(fields, " ") {
					break
				}
				group, e := syscall.Getpgid(pid)
				if e == nil && group == pid {
					syscall.Kill(-pid, syscall.SIGKILL)
				} else {
					syscall.Kill(pid, syscall.SIGKILL)
				}
				killed = append(killed, pid)
				break
			}
		}
	}
	return killed, nil
}
