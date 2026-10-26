//go:build !windows

package hosting

import "os"

func privateKeyPermissions(path string) error { return os.Chmod(path, 0600) }

func createPrivateKey(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
