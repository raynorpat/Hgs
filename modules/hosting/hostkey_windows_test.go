//go:build windows

package hosting

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHostingHostKeyPrivateACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host")
	if _, err := loadHostKey(path); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + current.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	// Windows may record the automatic-inheritance bookkeeping bit even with a protected DACL.
	if strings.Replace(sd.String(), "D:PAI", "D:P", 1) != expected.String() {
		t.Fatalf("host key ACL: %s, want %s", sd.String(), expected.String())
	}
}

func TestHostingHostKeyPrivateAtCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new-key")
	file, err := createPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + current.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Replace(descriptor.String(), "D:PAI", "D:P", 1) != expected.String() {
		t.Fatalf("key was created with inherited access: %s", descriptor.String())
	}
}
