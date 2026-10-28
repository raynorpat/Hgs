package collaboration

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

const preparedOwnerFile = "merge-owner.json"

// OperationID is the random operation nonce. A DB identity alone never owns
// an existing directory: allocation must create this exact marker exclusively.
func preparedOwnerBytes(op models.MergeOperation) []byte {
	b, _ := json.Marshal(struct {
		Version                 int
		OperationID, ArtifactID string
		PullID, ComparisonID    int64
	}{1, op.ID, op.ArtifactID, op.PullID, op.ComparisonID})
	return b
}

func preparedAllocation(host *hosting.Service, op models.MergeOperation) (string, error) {
	return preparedAllocationAt(preparedMergeRoot(host), op)
}

func preparedAllocationAt(root string, op models.MergeOperation) (string, error) {
	b, err := hex.DecodeString(op.ID)
	if err != nil || len(b) != 16 || hex.EncodeToString(b) != op.ID || op.ArtifactID != op.ID {
		return "", hg.ErrUnsafeCheckout
	}
	if err := comparisonPlainPath(root); err != nil {
		return "", err
	}
	return filepath.Join(root, op.ID), nil
}

func removeOwnedPrepared(host *hosting.Service, op models.MergeOperation) error {
	return removeOwnedPreparedAt(preparedMergeRoot(host), op)
}

func removeOwnedPreparedAt(root string, op models.MergeOperation) error {
	path, err := preparedAllocationAt(root, op)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := comparisonPlainPath(path); err != nil {
		return err
	}
	marker := filepath.Join(path, preparedOwnerFile)
	fi, err := os.Lstat(marker)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return hg.ErrUnsafeCheckout
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, preparedOwnerBytes(op)) {
		return hg.ErrUnsafeCheckout
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != preparedOwnerFile && entry.Name() != "native" {
			return hg.ErrUnsafeCheckout
		}
	}
	if err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !comparisonContains(path, p) || (fi.Mode().Type() != 0 && fi.Mode().Type() != fs.ModeDir) {
			return hg.ErrUnsafeCheckout
		}
		return nil
	}); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(path, "native")); err != nil {
		return err
	}
	if err := os.Remove(marker); err != nil {
		return err
	}
	return os.Remove(path)
}

func allocatePrepared(host *hosting.Service, op models.MergeOperation) (string, error) {
	if err := prepareComparisonRoot(preparedMergeRoot(host), host.RepositoryRoot()); err != nil {
		return "", err
	}
	if err := removeOwnedPrepared(host, op); err != nil {
		return "", err
	}
	path, err := preparedAllocation(host, op)
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(path, preparedOwnerFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(preparedOwnerBytes(op))
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return filepath.Join(path, "native"), nil
}
