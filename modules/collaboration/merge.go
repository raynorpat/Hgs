package collaboration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/log"
)

var (
	// ErrMergeStale means the pinned comparison no longer matches the live
	// source or target; refresh the comparison and review again.
	ErrMergeStale = errors.New("pull request changed; refresh the comparison")
	ErrMergeBusy  = errors.New("merge is already running")
)

type MergeRequest struct {
	PullID, ComparisonID int64
	Source, Target       hg.NodeID
	IdempotencyKey       string
}

// MergeResult.State is "pending" while this process works on the operation,
// "recovering" when a durable non-terminal operation has no worker yet,
// "merged" and "failed" once terminal.
type MergeResult struct {
	OperationID string
	State       string
	Node        hg.NodeID
	Code        string
}

// mergeFault is a test seam that simulates a process crash after a durable boundary.
var mergeFault func(stage string) error

func faultAt(stage string) error {
	if mergeFault != nil {
		return mergeFault(stage)
	}
	return nil
}

var idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// Collaboration services are built per request, so worker liveness is process-wide.
var mergeWorkers = struct {
	sync.Mutex
	ids map[string]bool
}{ids: map[string]bool{}}

func claimMergeWorker(id string) bool {
	mergeWorkers.Lock()
	defer mergeWorkers.Unlock()
	if mergeWorkers.ids[id] {
		return false
	}
	mergeWorkers.ids[id] = true
	return true
}
func mergeWorkerActive(id string) bool {
	mergeWorkers.Lock()
	defer mergeWorkers.Unlock()
	return mergeWorkers.ids[id]
}
func releaseMergeWorker(id string) {
	mergeWorkers.Lock()
	delete(mergeWorkers.ids, id)
	mergeWorkers.Unlock()
}

func (s *Service) host() (*hosting.Service, error) {
	if s == nil || s.Provider == nil {
		return nil, ErrUnavailable
	}
	host := s.Provider.Service()
	if host == nil {
		return nil, ErrUnavailable
	}
	return host, nil
}

// MergePull admits the merge under current authority, then drives it. Repeating
// the same key and payload resumes the same operation; it never prepares anew.
func (s *Service) MergePull(ctx context.Context, actor int64, in MergeRequest) (MergeResult, error) {
	host, err := s.host()
	if err != nil {
		return MergeResult{}, err
	}
	if !idempotencyKey.MatchString(in.IdempotencyKey) {
		return MergeResult{}, models.ErrPullInvalid
	}
	for _, n := range []hg.NodeID{in.Source, in.Target} {
		if _, err := hg.ParseNodeID(string(n)); err != nil {
			return MergeResult{}, models.ErrPullInvalid
		}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return MergeResult{}, err
	}
	op, err := models.BeginMergeOperation(ctx, actor, models.MergeAdmission{ID: hex.EncodeToString(raw[:]), IdempotencyKey: in.IdempotencyKey, PullID: in.PullID, ComparisonID: in.ComparisonID, SourceNode: in.Source, TargetNode: in.Target})
	if err != nil {
		return MergeResult{}, err
	}
	return s.driveMerge(ctx, host, op)
}

func (s *Service) MergeStatus(ctx context.Context, actor, pullID int64, operationID string) (MergeResult, error) {
	op, err := models.GetMergeOperation(ctx, actor, pullID, operationID)
	if err != nil {
		return MergeResult{}, err
	}
	return mergeResult(op), nil
}

func mergeResult(op models.MergeOperation) MergeResult {
	r := MergeResult{OperationID: op.ID, Node: op.ResultNode, Code: op.ErrorCode}
	switch {
	case op.State == models.MergeFinalized:
		r.State = "merged"
	case op.State == models.MergeFailed:
		r.State, r.Node = "failed", ""
	case mergeWorkerActive(op.ID):
		r.State = "pending"
	default:
		r.State = "recovering"
	}
	return r
}

// driveMerge advances one operation as far as it safely can. Errors that leave
// the operation non-terminal mean the outcome is unknown or not yet attempted;
// callers repeat the same request or let ReconcileOperations resume it.
func (s *Service) driveMerge(ctx context.Context, host *hosting.Service, op models.MergeOperation) (MergeResult, error) {
	if !claimMergeWorker(op.ID) {
		return mergeResult(op), ErrMergeBusy
	}
	final, err := s.driveMergeClaimed(ctx, host, op)
	releaseMergeWorker(op.ID)
	return mergeResult(reloadMerge(final)), err
}

func (s *Service) driveMergeClaimed(ctx context.Context, host *hosting.Service, op models.MergeOperation) (models.MergeOperation, error) {
	ctx, cancel := context.WithTimeout(ctx, host.ReviewOperationTimeout())
	defer cancel()
	var err error
	if err = faultAt("admitted"); err != nil {
		return op, err
	}
	if op.State == models.MergeUnstarted {
		if op, err = s.prepareMerge(ctx, host, op); err != nil {
			return op, err
		}
		if err = faultAt("prepared"); err != nil {
			return op, err
		}
	}
	if op.State == models.MergePrepared {
		if err = s.publishMerge(ctx, host, op); err != nil {
			op = reloadMerge(op)
			return op, err
		}
		op = reloadMerge(op)
		if err = faultAt("published"); err != nil {
			return op, err
		}
	}
	if op.State == models.MergePublished {
		if err = models.FinalizeMerge(op.ID); err != nil {
			return op, err
		}
		op = reloadMerge(op)
		removePreparedMerge(host, op.ID)
	}
	return op, nil
}

func reloadMerge(op models.MergeOperation) models.MergeOperation {
	if cur, err := models.LoadMergeOperation(op.ID); err == nil {
		return cur
	}
	return op
}

func preparedMergeRoot(host *hosting.Service) string {
	return filepath.Join(host.ReviewArtifactRoot(), "prepared")
}

// removePreparedMerge deletes only <root>/prepared/<id> for a terminal operation.
func removePreparedMerge(host *hosting.Service, id string) {
	op, err := models.LoadMergeOperation(id)
	if err != nil {
		return
	}
	if err := removeOwnedPrepared(host, op); err != nil {
		log.Warn("remove prepared merge %s: %v", id, err)
	}
}

func failMerge(host *hosting.Service, op models.MergeOperation, code string, cause error) (models.MergeOperation, error) {
	if err := models.FailMerge(op.ID, code); err != nil {
		return op, err
	}
	if code != "publication-failed" {
		removePreparedMerge(host, op.ID)
	}
	return reloadMerge(op), cause
}

func (s *Service) prepareMerge(ctx context.Context, host *hosting.Service, op models.MergeOperation) (models.MergeOperation, error) {
	actor := op.ActorID
	readCtx, _, p, finish, err := s.comparisonRead(ctx, actor, op.PullID)
	if err != nil {
		if errors.Is(err, models.ErrPullNotExist) || errors.Is(err, models.ErrInvalidCredential) {
			return failMerge(host, op, "authority-lost", err)
		}
		return op, err
	}
	// Both leases end before the guarded mutation takes its own fences.
	defer finish()
	c, err := models.GetPullComparison(readCtx, actor, op.PullID, op.ComparisonID)
	if err != nil {
		return op, err
	}
	snapshot, err := comparisonSnapshot(host.ReviewArtifactRoot(), c)
	if err != nil {
		return failMerge(host, op, "comparison-unavailable", ErrComparisonUnavailable)
	}
	source, target, err := comparisonEndpoints(readCtx, host, p)
	if err != nil {
		return op, comparisonError(err)
	}
	if source.Node != op.SourceNode || target.Node != op.TargetNode {
		return failMerge(host, op, "stale", ErrMergeStale)
	}
	user, err := models.GetUserById(actor)
	if err != nil {
		return failMerge(host, op, "authority-lost", err)
	}
	output, err := allocatePrepared(host, op)
	if err != nil {
		return op, err
	}
	prepared, err := host.Runner().PrepareReviewMerge(readCtx, snapshot, output, mergeAuthor(user), mergeMessage(p))
	if err != nil {
		switch {
		case errors.Is(err, hg.ErrMergeConflict):
			return failMerge(host, op, "conflict", err)
		case errors.Is(err, hg.ErrAmbiguousHead):
			return failMerge(host, op, "ambiguous-head", err)
		case errors.Is(err, hg.ErrUnrelatedHistory):
			return failMerge(host, op, "unrelated-history", err)
		case errors.Is(err, hg.ErrUnsafeCheckout):
			return failMerge(host, op, "unsafe-checkout", err)
		}
		return op, err
	}
	expectation := hosting.MutationExpectation{
		RepoID: p.TargetRepoID, Selection: target.Selection, Node: target.Node, OperationID: op.ID,
		Source:   hosting.MutationEndpoint{RepoID: p.SourceRepoID, Selection: source.Selection, Node: source.Node},
		Prepared: prepared,
	}
	preparedJSON, _ := json.Marshal(prepared)
	expectationJSON, err := json.Marshal(expectation)
	if err != nil {
		return op, err
	}
	if err = models.SetMergePrepared(op.ID, prepared.Result, prepared.Bookmark, string(preparedJSON), string(expectationJSON)); err != nil {
		return op, err
	}
	return reloadMerge(op), nil
}

// publishMerge runs the original stored expectation through the hosting guard.
// A failure after the guard started is ambiguous and leaves the operation
// prepared; only provable non-starts or terminal hosting outcomes end it.
func (s *Service) publishMerge(ctx context.Context, host *hosting.Service, op models.MergeOperation) error {
	var e hosting.MutationExpectation
	if err := json.Unmarshal([]byte(op.Expectation), &e); err != nil || e.OperationID != op.ID {
		return fmt.Errorf("stored merge expectation invalid: %v", err)
	}
	err := host.WithRepositoryMutation(ctx, op.ActorID, e, func(ctx context.Context, env map[string]string) error {
		p, _ := models.RepositoryStoragePath(host.RepositoryRoot(), e.RepoID)
		return host.Runner().PublishReview(ctx, hg.PublishReviewRequest{Prepared: e.Prepared, TargetPath: p, Expected: hg.ReviewEndpoint{Path: p, Selection: e.Selection, Node: e.Node}, OperationID: e.OperationID}, env)
	})
	if fault := faultAt("mutated"); fault != nil {
		return fault
	}
	st, statusErr := models.ReviewPublicationStatus(op.ID)
	switch {
	case statusErr == nil && st.Complete:
		return models.SetMergePublished(op.ID)
	case statusErr == nil && st.Failed:
		_, err = failMerge(host, op, "publication-failed", err)
		return err
	case statusErr == nil && st.Aborted && !st.Started:
		code := "publication-aborted"
		if errors.Is(err, hosting.ErrStaleRepository) {
			code, err = "stale", ErrMergeStale
		}
		_, err = failMerge(host, op, code, err)
		return err
	case statusErr != nil && !errors.Is(statusErr, models.ErrReviewPublicationMissing):
		return statusErr
	case err == nil:
		return errors.New("merge publication finished without a durable record")
	case statusErr == nil && st.Started:
		return err
	case errors.Is(err, hosting.ErrStaleRepository):
		_, err = failMerge(host, op, "stale", ErrMergeStale)
		return err
	case errors.Is(err, hgbridge.ErrDenied):
		_, err = failMerge(host, op, "authority-lost", err)
		return err
	}
	return err
}

// ReconcileOperations resumes every non-terminal merge. Hosting has already
// concluded publications whose actor lost access, so their recorded outcome is
// finalized or failed here without the actor's grant.
func (s *Service) ReconcileOperations(ctx context.Context) error {
	host, err := s.host()
	if err != nil {
		return err
	}
	var first error
	for after := ""; ; {
		rows, err := models.ActiveMergeOperations(after)
		if err != nil {
			return err
		}
		for _, op := range rows {
			after = op.ID
			if _, err := s.driveMerge(ctx, host, op); err != nil && !errors.Is(err, ErrMergeBusy) {
				log.Warn("reconcile merge %s: %v", op.ID, err)
				if first == nil {
					first = err
				}
			}
		}
		if len(rows) < 100 {
			return first
		}
	}
}

func mergeAuthor(u *models.User) string {
	clean := strings.NewReplacer("<", "", ">", "", "\r", "", "\n", "", "\x00", "").Replace
	name := strings.TrimSpace(clean(u.Name))
	email := strings.TrimSpace(clean(u.Email))
	if email == "" {
		email = name + "@localhost"
	}
	return name + " <" + email + ">"
}

func mergeMessage(p models.PullRequest) string {
	title := strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(p.Title)
	return fmt.Sprintf("Merge pull request #%d: %s", p.Number, strings.TrimSpace(title))
}
