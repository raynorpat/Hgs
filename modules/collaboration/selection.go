package collaboration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

func (s *Service) ReselectPull(ctx context.Context, actor int64, expected models.PullRequest, source, target hg.ReviewSelection) error {
	ctx, host, p, finish, err := s.comparisonRead(ctx, actor, expected.ID)
	if err != nil {
		return err
	}
	defer finish()
	if !models.SamePullSelection(p, expected) {
		return models.ErrPullConflict
	}
	src, _ := json.Marshal(source)
	dst, _ := json.Marshal(target)
	candidate := p
	candidate.SourceSelection, candidate.TargetSelection = string(src), string(dst)
	if _, _, err := comparisonEndpoints(ctx, host, candidate); err != nil {
		return comparisonError(err)
	}
	return models.ReselectPullRecord(ctx, actor, expected, source, target)
}

// InspectPullComparison is read-only. Frozen artifact readiness does not prove
// that a selector still resolves to its pins, and GET never refreshes a snapshot.
func (s *Service) InspectPullComparison(ctx context.Context, actor, pullID int64) (models.PullComparison, string, error) {
	ctx, host, p, finish, err := s.comparisonRead(ctx, actor, pullID)
	if err != nil {
		return models.PullComparison{}, "unavailable", err
	}
	defer finish()
	if p.CurrentComparisonID == 0 {
		return models.PullComparison{}, "unprepared", nil
	}
	c, err := models.GetPullComparison(ctx, actor, pullID, p.CurrentComparisonID)
	if err != nil {
		return c, "unavailable", err
	}
	source, target, resolveErr := comparisonEndpoints(ctx, host, p)
	current, err := models.GetPullRecordByID(ctx, actor, pullID)
	if err != nil {
		return models.PullComparison{}, "unavailable", err
	}
	if !models.SamePullSelection(current, p) {
		return c, "stale", nil
	}
	if c.State == "pending" {
		return c, "pending", nil
	}
	if !c.SnapshotComplete {
		return c, "unavailable", nil
	}
	if resolveErr != nil {
		if errors.Is(resolveErr, hg.ErrNotFound) {
			return c, "missing-ref", nil
		}
		if errors.Is(resolveErr, hg.ErrAmbiguousHead) {
			return c, "ambiguous", nil
		}
		return c, "unavailable", comparisonError(resolveErr)
	}
	if source.Node != c.SourceNode || target.Node != c.TargetNode {
		return c, "stale", nil
	}
	if c.State != "ready" || !c.SnapshotComplete {
		return c, c.State, nil
	}
	if _, err := comparisonSnapshot(host.ReviewArtifactRoot(), c); err != nil {
		return c, "unavailable", nil
	}
	return c, "fresh", nil
}

func (s *Service) ComparisonMergeStrategy(ctx context.Context, actor, pullID, id int64) (hg.MergeStrategy, error) {
	ctx, host, p, finish, err := s.comparisonRead(ctx, actor, pullID)
	if err != nil {
		return "", err
	}
	defer finish()
	if p.CurrentComparisonID != id {
		return "", models.ErrPullConflict
	}
	c, err := models.GetPullComparison(ctx, actor, pullID, id)
	if err != nil {
		return "", err
	}
	snapshot, err := comparisonSnapshot(host.ReviewArtifactRoot(), c)
	if err != nil {
		return "", ErrComparisonUnavailable
	}
	strategy, err := host.Runner().ReviewMergeStrategy(ctx, snapshot)
	if err != nil {
		return "", comparisonError(err)
	}
	current, err := models.GetPullRecordByID(ctx, actor, pullID)
	if err != nil {
		return "", err
	}
	if !models.SamePullSelection(current, p) {
		return "", models.ErrPullConflict
	}
	return strategy, nil
}
