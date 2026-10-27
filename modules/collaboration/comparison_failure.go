package collaboration

import (
	"context"
	"errors"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/log"
)

var (
	ErrComparisonCleanup     = errors.New("pull comparison artifact cleanup failed")
	ErrComparisonPersistence = errors.New("pull comparison state persistence failed")
)

// Keep the original operation's errors.Is identity, but never interpolate its
// possibly private SQL/native/path diagnostics into the public error string.
type comparisonFailureError struct {
	operation            error
	cleanup, persistence bool
}

func (e *comparisonFailureError) Error() string {
	message := ErrComparisonUnavailable.Error()
	if e.operation != nil {
		message = comparisonError(e.operation).Error()
	}
	if e.cleanup {
		message += "; " + ErrComparisonCleanup.Error()
	}
	if e.persistence {
		message += "; " + ErrComparisonPersistence.Error()
	}
	return message
}
func (e *comparisonFailureError) Unwrap() []error {
	var causes []error
	if e.operation != nil {
		causes = append(causes, e.operation)
	}
	if e.cleanup {
		causes = append(causes, ErrComparisonCleanup)
	}
	if e.persistence {
		causes = append(causes, ErrComparisonPersistence)
	}
	return causes
}

// reserved must be the last acknowledged pending row, not mutable candidate
// metadata. On a persistence error it remains the last confirmed observation:
// the caller must not infer a terminal state or a rollback from that error.
func finishComparisonFailure(root string, reserved models.PullComparison, owned bool, operationErr error) (models.PullComparison, error) {
	failure := &comparisonFailureError{operation: operationErr}
	reason := "snapshot-failed"
	if owned {
		if err := removeComparisonAllocation(root, reserved); err != nil {
			failure.cleanup = true
			reason = "artifact-invalid"
		}
	}
	// Failure cleanup is allowed after the actor/operation context was revoked.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), hg.BrowserCommandTimeout)
	defer cancel()
	if err := models.FailPullComparison(cleanupCtx, reserved.ID, reason); err != nil {
		failure.persistence = true
		return reserved, failure
	}
	reserved.State = "unavailable"
	reserved.Failure = reason
	reserved.SnapshotComplete = false
	return reserved, failure
}

// Startup may retain a failed allocation only after repair-needed status is
// durable. Operator diagnostics contain its internal numeric ID, never paths
// or raw SQL/native errors. Persistence failures prevent service admission.
func markComparisonRepairNeeded(ctx context.Context, c models.PullComparison, cleanup bool) error {
	if err := models.InvalidateComparisonArtifact(ctx, c.ID); err != nil {
		return &comparisonFailureError{operation: err, cleanup: cleanup, persistence: true}
	}
	logComparisonRepairNeeded(c.ID)
	return nil
}
func logComparisonRepairNeeded(id int64) {
	log.Error(4, "Pull comparison %d requires artifact repair; retained", id)
}
