//go:build !windows

package hosting

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostingHostKeyPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host")
	if _, err := loadHostKey(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v", info.Mode())
	}
}
