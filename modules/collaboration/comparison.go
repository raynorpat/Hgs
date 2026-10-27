package collaboration

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

var ErrComparisonUnavailable = errors.New("pull comparison unavailable")

// comparisonRead retains both managed leases until every native process has
// joined. It does not capture the cmd-owned provider at handler construction.
func (s *Service) comparisonRead(ctx context.Context, actor, pullID int64) (context.Context, *hosting.Service, models.PullRequest, func(), error) {
	if s == nil || s.Provider == nil {
		return nil, nil, models.PullRequest{}, nil, ErrUnavailable
	}
	host := s.Provider.Service()
	if host == nil {
		return nil, nil, models.PullRequest{}, nil, ErrUnavailable
	}
	p, err := models.GetPullRecordByID(ctx, actor, pullID)
	if err != nil {
		return nil, nil, p, nil, err
	}
	if err := validateComparisonRoot(host.ReviewArtifactRoot(), host.RepositoryRoot()); err != nil {
		return nil, nil, p, nil, ErrComparisonUnavailable
	}
	readCtx, cancel := context.WithTimeout(ctx, host.ReviewOperationTimeout())
	release, err := host.RegisterBrowserRead(actor, p.SourceRepoID, cancel)
	if err != nil {
		cancel()
		return nil, nil, p, nil, err
	}
	finish := func() { cancel(); release() }
	if p.TargetRepoID != p.SourceRepoID {
		other, err := host.RegisterBrowserRead(actor, p.TargetRepoID, cancel)
		if err != nil {
			finish()
			return nil, nil, p, nil, err
		}
		finish = func() { cancel(); other(); release() }
	}
	return readCtx, host, p, finish, nil
}
func comparisonEndpoints(ctx context.Context, host *hosting.Service, p models.PullRequest) (hg.ReviewEndpoint, hg.ReviewEndpoint, error) {
	src, dst, err := models.PullSelections(p)
	if err != nil {
		return hg.ReviewEndpoint{}, hg.ReviewEndpoint{}, err
	}
	sourcePath, err := models.RepositoryStoragePath(host.RepositoryRoot(), p.SourceRepoID)
	if err != nil {
		return hg.ReviewEndpoint{}, hg.ReviewEndpoint{}, err
	}
	targetPath, err := models.RepositoryStoragePath(host.RepositoryRoot(), p.TargetRepoID)
	if err != nil {
		return hg.ReviewEndpoint{}, hg.ReviewEndpoint{}, err
	}
	a, err := host.Runner().ResolveReview(ctx, sourcePath, src)
	if err != nil {
		return a, hg.ReviewEndpoint{}, err
	}
	b, err := host.Runner().ResolveReview(ctx, targetPath, dst)
	return a, b, err
}
func comparisonError(err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, hg.ErrAmbiguousHead, hg.ErrNoChanges, hg.ErrUnrelatedHistory, hg.ErrNotFound, hg.ErrComparisonTooLarge, hg.ErrUnsupportedEncoding} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrComparisonUnavailable
}

// RefreshComparison observes mutable selections and creates an immutable
// version only when pins changed or the current artifact is invalid. A native
// observation is not a writer fence: guarded merge must re-resolve and use the
// Task 2 exact prepared-publication contract; old versions remain readable.
func (s *Service) RefreshComparison(ctx context.Context, actor, pullID int64) (c models.PullComparison, resultErr error) {
	ctx, host, p, finish, err := s.comparisonRead(ctx, actor, pullID)
	if err != nil {
		return c, err
	}
	defer finish()
	if err := models.ValidateRepositoryCredential(actor, 0, 0); err != nil {
		return c, err
	}
	source, target, resolveErr := comparisonEndpoints(ctx, host, p)
	if resolveErr == nil && p.CurrentComparisonID > 0 {
		old, err := models.GetPullComparison(ctx, actor, pullID, p.CurrentComparisonID)
		if err == nil && old.SnapshotComplete && old.SourceNode == source.Node && old.TargetNode == target.Node && (old.State == "ready" || old.Failure == "comparison-too-large") {
			if _, err := comparisonSnapshot(host.ReviewArtifactRoot(), old); err == nil {
				if old.State == "ready" {
					return old, nil
				}
				return old, hg.ErrComparisonTooLarge
			}
		}
	}
	c, err = models.ReservePullComparison(ctx, actor, pullID)
	if err != nil {
		return c, err
	}
	owned := false
	complete := false
	defer func() {
		if complete {
			return
		}
		if owned {
			_ = removeComparisonAllocation(host.ReviewArtifactRoot(), c)
		}
		// Failure cleanup is permitted even after the actor/context was revoked.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), hg.BrowserCommandTimeout)
		defer cancel()
		_ = models.FailPullComparison(cleanupCtx, c.ID, "snapshot-failed")
		c.State = "unavailable"
		c.Failure = "snapshot-failed"
		c.SnapshotComplete = false
	}()
	if resolveErr != nil {
		return c, comparisonError(resolveErr)
	}
	if source.Node == target.Node {
		return c, hg.ErrNoChanges
	}
	if err := allocateComparison(host.ReviewArtifactRoot(), c); err != nil {
		return c, ErrComparisonUnavailable
	}
	owned = true
	snapshot, err := host.Runner().SnapshotReview(ctx, source, target, filepath.Join(host.ReviewArtifactRoot(), c.ArtifactID, "snapshot"))
	if err != nil {
		return c, comparisonError(err)
	}
	c.SourceNode = snapshot.Source
	c.TargetNode = snapshot.Target
	c.BaseNode = snapshot.Base
	if err := hg.ValidateReviewSnapshot(snapshot); err != nil {
		return c, ErrComparisonUnavailable
	}
	_, previewErr := host.Runner().CompareReview(ctx, snapshot)
	if previewErr != nil && !errors.Is(previewErr, hg.ErrComparisonTooLarge) {
		return c, comparisonError(previewErr)
	}
	currentSource, currentTarget, err := comparisonEndpoints(ctx, host, p)
	if err != nil {
		return c, comparisonError(err)
	}
	if source.Node != currentSource.Node || target.Node != currentTarget.Node {
		return c, ErrComparisonUnavailable
	}
	c.State = "ready"
	c.SnapshotComplete = true
	if previewErr != nil {
		c.State = "unavailable"
		c.Failure = "comparison-too-large"
	}
	if err := models.CompletePullComparison(ctx, actor, c); err != nil {
		return c, err
	}
	complete = true
	return c, previewErr
}

func (s *Service) Comparison(ctx context.Context, actor, pullID, id int64) (models.PullComparison, hg.ReviewComparison, error) {
	ctx, host, _, finish, err := s.comparisonRead(ctx, actor, pullID)
	if err != nil {
		return models.PullComparison{}, hg.ReviewComparison{}, err
	}
	defer finish()
	c, err := models.GetPullComparison(ctx, actor, pullID, id)
	if err != nil {
		return c, hg.ReviewComparison{}, err
	}
	snapshot, err := comparisonSnapshot(host.ReviewArtifactRoot(), c)
	if err != nil {
		return c, hg.ReviewComparison{}, ErrComparisonUnavailable
	}
	preview, err := host.Runner().CompareReview(ctx, snapshot)
	// Native work can take time. Recheck before any buffered content is returned.
	if _, accessErr := models.GetPullRecordByID(ctx, actor, pullID); accessErr != nil {
		return models.PullComparison{}, hg.ReviewComparison{}, accessErr
	}
	if err != nil {
		return c, preview, comparisonError(err)
	}
	return c, preview, nil
}

type comparisonWriter struct {
	ctx           context.Context
	actor, pullID int64
	out           io.Writer
	err           error
}

func (w *comparisonWriter) Write(p []byte) (int, error) {
	if _, err := models.GetPullRecordByID(w.ctx, w.actor, w.pullID); err != nil {
		w.err = err
		return 0, err
	}
	n, err := w.out.Write(p)
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

// ReadComparisonFile accepts base (diff left), source (diff right), and target
// (explicit destination content). CompareReview is base-to-source, never
// target-to-source. It streams raw content with no HTML buffer. Callers rendering
// text must bound their writer. HTTP callers must interrupt blocked writes on
// cancellation and abort responses on post-write errors, like RawFile does.
// Per-write authority cannot recall bytes already sent before revocation.
func (s *Service) ReadComparisonFile(ctx context.Context, actor, pullID, id int64, side, path string, out io.Writer) error {
	if side != "source" && side != "target" && side != "base" {
		return hg.ErrNotFound
	}
	if out == nil {
		return hg.ErrNotFound
	}
	ctx, host, _, finish, err := s.comparisonRead(ctx, actor, pullID)
	if err != nil {
		return err
	}
	defer finish()
	c, err := models.GetPullComparison(ctx, actor, pullID, id)
	if err != nil {
		return err
	}
	snapshot, err := comparisonSnapshot(host.ReviewArtifactRoot(), c)
	if err != nil {
		return ErrComparisonUnavailable
	}
	node := c.SourceNode
	if side == "target" {
		node = c.TargetNode
	}
	if side == "base" {
		node = c.BaseNode
	}
	writer := &comparisonWriter{ctx: ctx, actor: actor, pullID: pullID, out: out}
	err = host.Runner().ReadFile(ctx, snapshot.Path, node, path, writer)
	if writer.err != nil {
		return writer.err
	}
	if err != nil {
		return comparisonError(err)
	}
	return nil
}
