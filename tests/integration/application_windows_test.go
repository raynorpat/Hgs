//go:build integration && windows

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A test-only supervisor gets a console hidden at creation. The application
// inherits it in a distinct process group, allowing a non-broadcast CTRL_BREAK.
func configureApplicationProcess(c *exec.Cmd) {
	if os.Getenv("GOGS_TEST_OWN_CONSOLE") != "1" {
		helper, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		console := exec.Command(helper, "-test.run=^TestApplicationProcessChild$")
		console.Env = append(os.Environ(), "GOGS_TEST_OWN_CONSOLE=1")
		console.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
		console.Stdin, console.Stdout, console.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := console.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}

func signalApplicationStop(pid int, binary, config string) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var path [32768]uint16
	size := uint32(len(path))
	if windows.QueryFullProcessImageName(h, 0, &path[0], &size) != nil || !strings.EqualFold(windows.UTF16ToString(path[:size]), strings.ReplaceAll(binary, "/", "\\")) {
		return false
	}
	// A held handle prevents this recorded process identity from being recycled
	// during the signal. The PID was recorded by this fixture's own launcher.
	helper, err := os.Executable()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, helper, "-test.run=^TestApplicationStopChild$")
	c.Env = append(os.Environ(), "GOGS_TEST_STOP_PID="+strconv.Itoa(pid), "GOGS_HG_APPLICATION_BINARY="+binary)
	out, err := c.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "owned console stop: %v: %s\n", err, out)
	}
	return err == nil
}

func TestApplicationStopChild(t *testing.T) {
	value := os.Getenv("GOGS_TEST_STOP_PID")
	if value == "" {
		return
	}
	pid, err := strconv.ParseUint(value, 10, 32)
	if err != nil || pid == 0 {
		t.Fatal("invalid owned PID")
	}
	k := windows.NewLazySystemDLL("kernel32.dll")
	k.NewProc("FreeConsole").Call()
	ok, _, e := k.NewProc("AttachConsole").Call(uintptr(pid))
	if ok == 0 {
		t.Fatal(fmt.Errorf("attach owned console: %v", e))
	}
	defer k.NewProc("FreeConsole").Call()
	signal.Ignore(os.Interrupt)
	// Nonzero group: never broadcast a console event, including to this helper.
	ok, _, e = k.NewProc("GenerateConsoleCtrlEvent").Call(windows.CTRL_BREAK_EVENT, uintptr(pid))
	if ok == 0 {
		t.Fatal(fmt.Errorf("signal owned console group: %v", e))
	}
	// Keep the attachment alive until the control event is dispatched.
	time.Sleep(50 * time.Millisecond)
}

func reapApplicationOrphans(markers ...string) ([]int, error) { return nil, nil }

func protectPackageKey(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}
func assertPackageKeyPrivate(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Replace(sd.String(), "D:PAI", "D:P", 1) != want.String() {
		t.Fatalf("host key ACL=%s want=%s", sd.String(), want.String())
	}
	t.Log("host key ACL: protected owner-only DACL")
}
