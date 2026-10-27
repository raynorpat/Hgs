package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/setting"
)

// These are real SQLite failure-state/owned-filesystem tests, not native
// snapshot tests. The row represents an acknowledged pending reservation.
func comparisonFailureFixture(t *testing.T) (string, *sql.DB, models.PullComparison) {
	t.Helper()
	root := t.TempDir()
	oldDB, oldLog := models.DbCfg, setting.LogRootPath
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(root, "test.db"))
	setting.LogRootPath = root
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { models.CloseEngine(); models.DbCfg = oldDB; setting.LogRootPath = oldLog })
	db, err := sql.Open("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := models.PullComparison{ID: 1, PullID: 1, Revision: 1, ArtifactID: strings.Repeat("a", 64), OwnershipNonce: strings.Repeat("b", 64), State: "pending", Created: time.Now().UTC()}
	if _, err := db.Exec("INSERT INTO pull_comparison (id,pull_id,revision,artifact_id,ownership_nonce,state,failure,snapshot_complete,source_node,target_node,base_node) VALUES (?,?,?,?,?,?,?,0,'','','')", c.ID, c.PullID, c.Revision, c.ArtifactID, c.OwnershipNonce, c.State, ""); err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(root, "artifacts")
	if err := os.Mkdir(artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := allocateComparison(artifacts, c); err != nil {
		t.Fatal(err)
	}
	return artifacts, db, c
}
func comparisonDurableState(t *testing.T, db *sql.DB, id int64) (state, failure string) {
	t.Helper()
	if err := db.QueryRow("SELECT state,failure FROM pull_comparison WHERE id = ?", id).Scan(&state, &failure); err != nil {
		t.Fatal(err)
	}
	return
}
func blockComparisonFailureUpdate(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("CREATE TRIGGER reject_comparison_failure BEFORE UPDATE ON pull_comparison WHEN NEW.state = 'unavailable' BEGIN SELECT RAISE(ABORT, 'private diagnostic /secret/storage'); END"); err != nil {
		t.Fatal(err)
	}
}
func comparisonUnknownChild(t *testing.T, root string, c models.PullComparison) string {
	t.Helper()
	path := filepath.Join(root, c.ArtifactID, "unknown")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Removing an owned allocation can safely refuse unexpected contents. That
// failure must be visible alongside the original error and durably repairable.
func TestComparisonFailureCleanupPersistsRepairNeeded(t *testing.T) {
	root, db, c := comparisonFailureFixture(t)
	unknown := comparisonUnknownChild(t, root, c)
	got, err := finishComparisonFailure(root, c, true, hg.ErrAmbiguousHead)
	state, reason := comparisonDurableState(t, db, c.ID)
	if state != "unavailable" || reason != "artifact-invalid" || got.State != state || got.Failure != reason {
		t.Errorf("cleanup refusal lost: returned=%s/%s durable=%s/%s", got.State, got.Failure, state, reason)
	}
	if !errors.Is(err, hg.ErrAmbiguousHead) || !errors.Is(err, ErrComparisonCleanup) {
		t.Errorf("original/cleanup cause lost: %v", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal(err)
	}
	if err != nil && strings.Contains(err.Error(), root) {
		t.Fatal("private path exposed")
	}
}

// A rejected real UPDATE must not turn the returned pending reservation into a
// fictitious confirmed terminal state. Subsequent recovery may safely finish.
func TestComparisonFailurePersistenceKeepsConfirmedPending(t *testing.T) {
	root, db, c := comparisonFailureFixture(t)
	blockComparisonFailureUpdate(t, db)
	got, err := finishComparisonFailure(root, c, true, hg.ErrNotFound)
	state, reason := comparisonDurableState(t, db, c.ID)
	if state != "pending" || got.State != state || reason != "" || got.Failure != reason || got.SnapshotComplete {
		t.Errorf("unpersisted terminal state: returned=%s/%s durable=%s/%s", got.State, got.Failure, state, reason)
	}
	if !errors.Is(err, hg.ErrNotFound) || !errors.Is(err, ErrComparisonPersistence) {
		t.Errorf("original/persistence cause lost: %v", err)
	}
	if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), root)) {
		t.Errorf("private diagnostic exposed: %v", err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_comparison_failure"); err != nil {
		t.Fatal(err)
	}
	if err := reconcileComparisonArtifact(context.Background(), root, c); err != nil {
		t.Fatal(err)
	}
	state, reason = comparisonDurableState(t, db, c.ID)
	if state != "unavailable" || reason != "interrupted" {
		t.Fatalf("recovery state %s/%s", state, reason)
	}
}

func TestComparisonFailureCleanupAndPersistenceBothReported(t *testing.T) {
	root, db, c := comparisonFailureFixture(t)
	unknown := comparisonUnknownChild(t, root, c)
	blockComparisonFailureUpdate(t, db)
	got, err := finishComparisonFailure(root, c, true, hg.ErrNoChanges)
	state, reason := comparisonDurableState(t, db, c.ID)
	if got.State != state || got.Failure != reason || state != "pending" {
		t.Errorf("returned state diverged: %+v durable=%s/%s", got, state, reason)
	}
	for _, cause := range []error{hg.ErrNoChanges, ErrComparisonCleanup, ErrComparisonPersistence} {
		if !errors.Is(err, cause) {
			t.Errorf("missing cause %v in %v", cause, err)
		}
	}
	if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), root)) {
		t.Errorf("private diagnostic exposed: %v", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal(err)
	}
}

// An already-unavailable row must also record failed startup cleanup. Once
// repair-needed, repeated startup preserves its unknown files and classification.
func TestComparisonRecoveryUnavailableCleanupFailure(t *testing.T) {
	root, db, c := comparisonFailureFixture(t)
	unknown := comparisonUnknownChild(t, root, c)
	if err := models.FailPullComparison(context.Background(), c.ID, "snapshot-failed"); err != nil {
		t.Fatal(err)
	}
	c.State = "unavailable"
	c.Failure = "snapshot-failed"
	for i := 0; i < 2; i++ {
		if err := reconcileComparisonArtifact(context.Background(), root, c); err != nil {
			t.Fatal(err)
		}
		state, reason := comparisonDurableState(t, db, c.ID)
		if state != "unavailable" || reason != "artifact-invalid" {
			t.Errorf("startup %d suppressed removal failure: %s/%s", i, state, reason)
		}
		c.State = state
		c.Failure = reason
		if b, err := os.ReadFile(unknown); err != nil || string(b) != "preserve" {
			t.Fatalf("startup %d changed unknown file: %q %v", i, b, err)
		}
	}
}

func TestComparisonRecoveryPersistenceFailureIsBounded(t *testing.T) {
	root, db, c := comparisonFailureFixture(t)
	comparisonUnknownChild(t, root, c)
	if err := models.FailPullComparison(context.Background(), c.ID, "snapshot-failed"); err != nil {
		t.Fatal(err)
	}
	c.State = "unavailable"
	c.Failure = "snapshot-failed"
	blockComparisonFailureUpdate(t, db)
	err := reconcileComparisonArtifact(context.Background(), root, c)
	if !errors.Is(err, ErrComparisonCleanup) || !errors.Is(err, ErrComparisonPersistence) {
		t.Errorf("startup failure suppressed: %v", err)
	}
	if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), root)) {
		t.Errorf("private diagnostic exposed: %v", err)
	}
	state, reason := comparisonDurableState(t, db, c.ID)
	if state != "unavailable" || reason != "snapshot-failed" {
		t.Errorf("failed update altered row: %s/%s", state, reason)
	}
}

func TestComparisonFailureSuccessfulCleanupPreservesSafeOriginalIdentity(t *testing.T) {
	root, db, c := comparisonFailureFixture(t)
	original := errors.New("native SQL diagnostic /secret/storage")
	got, err := finishComparisonFailure(root, c, true, original)
	state, reason := comparisonDurableState(t, db, c.ID)
	if got.State != state || got.Failure != reason || state != "unavailable" || reason != "snapshot-failed" {
		t.Fatalf("confirmed state mismatch: %+v %s/%s", got, state, reason)
	}
	if !errors.Is(err, original) || errors.Is(err, ErrComparisonCleanup) || errors.Is(err, ErrComparisonPersistence) {
		t.Fatalf("incorrect causes: %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "SQL") || len(err.Error()) > 200 {
		t.Fatalf("unsafe public error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, c.ArtifactID)); !os.IsNotExist(err) {
		t.Fatalf("owned allocation retained: %v", err)
	}
}
