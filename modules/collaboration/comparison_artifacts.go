package collaboration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"

	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

const comparisonOwnerFile = "comparison-owner.json"

type comparisonOwner struct {
	Version              int
	ComparisonID, PullID int64
	ArtifactID, Nonce    string
}

func comparisonOwnerBytes(c models.PullComparison) []byte {
	b, _ := json.Marshal(comparisonOwner{1, c.ID, c.PullID, c.ArtifactID, c.OwnershipNonce})
	return b
}
func comparisonIDValid(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == id
}
func comparisonContains(root, path string) bool {
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}
func comparisonPlainPath(path string) error {
	if !filepath.IsAbs(path) {
		return hg.ErrUnsafeCheckout
	}
	if err := hg.CheckPath(path); err != nil {
		return err
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode().Type() != fs.ModeDir {
			return hg.ErrUnsafeCheckout
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}
func validateComparisonRoot(root string, excluded ...string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) == filepath.Dir(filepath.Clean(root)) {
		return hg.ErrUnsafeCheckout
	}
	root = filepath.Clean(root)
	for _, other := range excluded {
		if other == "" {
			continue
		}
		if !filepath.IsAbs(other) {
			return hg.ErrUnsafeCheckout
		}
		if comparisonContains(root, other) || comparisonContains(other, root) {
			return hg.ErrUnsafeCheckout
		}
	}
	return comparisonPlainPath(root)
}
func prepareComparisonRoot(root string, excluded ...string) error {
	if !filepath.IsAbs(root) {
		return hg.ErrUnsafeCheckout
	}
	// Check the existing ancestry before MkdirAll can follow a link/junction.
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		_, err := os.Lstat(p)
		if os.IsNotExist(err) && filepath.Dir(p) != p {
			continue
		}
		if err != nil {
			return err
		}
		if err := comparisonPlainPath(p); err != nil {
			return err
		}
		break
	}
	// Validate overlap before creating anything.
	for _, other := range excluded {
		if other != "" && (!filepath.IsAbs(other) || comparisonContains(root, other) || comparisonContains(other, root)) {
			return hg.ErrUnsafeCheckout
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := validateComparisonRoot(root, excluded...); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(root)
		if err != nil {
			return err
		}
		if fi.Mode().Perm()&0077 != 0 {
			return hg.ErrUnsafeCheckout
		}
	}
	return nil
}
func comparisonAllocation(root string, c models.PullComparison) (string, error) {
	if !comparisonIDValid(c.ArtifactID) || !comparisonIDValid(c.OwnershipNonce) || c.ID <= 0 || c.PullID <= 0 {
		return "", hg.ErrUnsafeCheckout
	}
	if err := comparisonPlainPath(root); err != nil {
		return "", err
	}
	return filepath.Join(root, c.ArtifactID), nil
}
func allocateComparison(root string, c models.PullComparison) error {
	path, err := comparisonAllocation(root, c)
	if err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	// A crash before this exclusive marker is complete leaves an unowned path;
	// reconciliation preserves it rather than guessing that it is ours.
	f, err := os.OpenFile(filepath.Join(path, comparisonOwnerFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(comparisonOwnerBytes(c))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
func validateComparisonOwner(root string, c models.PullComparison) (string, error) {
	path, err := comparisonAllocation(root, c)
	if err != nil {
		return "", err
	}
	if err := comparisonPlainPath(path); err != nil {
		return "", err
	}
	marker := filepath.Join(path, comparisonOwnerFile)
	fi, err := os.Lstat(marker)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return "", hg.ErrUnsafeCheckout
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(b, comparisonOwnerBytes(c)) {
		return "", hg.ErrUnsafeCheckout
	}
	return path, nil
}
func comparisonSnapshot(root string, c models.PullComparison) (hg.ReviewSnapshot, error) {
	if !c.SnapshotComplete || (c.State != "ready" && !(c.State == "unavailable" && c.Failure == "comparison-too-large")) {
		return hg.ReviewSnapshot{}, ErrComparisonUnavailable
	}
	path, err := validateComparisonOwner(root, c)
	if err != nil {
		return hg.ReviewSnapshot{}, err
	}
	s := hg.ReviewSnapshot{Path: filepath.Join(path, "snapshot", "repo"), Source: c.SourceNode, Target: c.TargetNode, Base: c.BaseNode}
	return s, hg.ValidateReviewSnapshot(s)
}
func removeComparisonAllocation(root string, c models.PullComparison) error {
	path, err := comparisonAllocation(root, c)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	path, err = validateComparisonOwner(root, c)
	if err != nil {
		return err
	}
	// Unknown wrapper children and links anywhere in the owned tree are never
	// followed or removed. The root and immutable ready allocations are untouched.
	children, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child.Name() != comparisonOwnerFile && child.Name() != "snapshot" {
			return hg.ErrUnsafeCheckout
		}
	}
	if err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Type() != 0 && info.Mode().Type() != fs.ModeDir {
			return hg.ErrUnsafeCheckout
		}
		if !comparisonContains(path, p) {
			return hg.ErrUnsafeCheckout
		}
		return nil
	}); err != nil {
		return err
	}
	// Removal can be retried after interruption while the durable DB row remains.
	// Remove the marker last so a partial snapshot removal still proves ownership.
	if err := os.RemoveAll(filepath.Join(path, "snapshot")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(path, comparisonOwnerFile)); err != nil {
		return err
	}
	return os.Remove(path)
}

// ReconcileComparisons is cmd-owned pre-admission recovery, after hosting/DB
// initialization and before listeners/service publication. Never call it from
// a request or concurrently with artifact operations. It performs no sweeps:
// only DB allocations with matching wrapper markers may be removed. Unknown
// files, missing markers, links and the separate prepared-merge namespace stay.
func ReconcileComparisons(ctx context.Context, host *hosting.Service, staticRoots ...string) error {
	if host == nil {
		return ErrUnavailable
	}
	excluded := append([]string{host.RepositoryRoot()}, staticRoots...)
	if err := prepareComparisonRoot(host.ReviewArtifactRoot(), excluded...); err != nil {
		return err
	}
	for after := int64(0); ; {
		rows, err := models.ComparisonAllocations(ctx, after)
		if err != nil {
			return err
		}
		for _, c := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			after = c.ID
			if err := reconcileComparisonArtifact(ctx, host.ReviewArtifactRoot(), c); err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func reconcileComparisonArtifact(ctx context.Context, root string, c models.PullComparison) error {
	if c.SnapshotComplete {
		if _, err := comparisonSnapshot(root, c); err != nil {
			return markComparisonRepairNeeded(ctx, c, false)
		}
		return nil
	}
	if c.State == "unavailable" && c.Failure == "artifact-invalid" {
		logComparisonRepairNeeded(c.ID)
		return nil
	}
	if err := removeComparisonAllocation(root, c); err != nil {
		return markComparisonRepairNeeded(ctx, c, true)
	}
	if c.State == "pending" {
		if err := models.FailPullComparison(ctx, c.ID, "interrupted"); err != nil {
			return &comparisonFailureError{operation: err, persistence: true}
		}
	}
	return nil
}
