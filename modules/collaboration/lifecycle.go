package collaboration

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/gogits/gogs/models"
)

func (s *Service) BeforeRepositoryChange(ctx context.Context, actorID, repoID int64, change string) error {
	return models.CheckRepositoryCollaborationChange(ctx, actorID, repoID, change)
}

// RepositoryCleanup deliberately captures roots, not a hosting Provider: it is
// also called by hosting.New before the cmd-owned service has been published.
func RepositoryCleanup(repositoryRoot, artifactRoot string, staticRoots ...string) models.RepositoryCollaborationCleanup {
	return func(ctx context.Context, repoID int64) error {
		rows, err := models.RepositoryPullDeletion(ctx, repoID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		excluded := append([]string{repositoryRoot}, staticRoots...)
		if err := prepareComparisonRoot(artifactRoot, excluded...); err != nil {
			return err
		}
		claimed := []string{}
		defer func() {
			for _, id := range claimed {
				releaseMergeWorker(id)
			}
		}()
		for _, d := range rows {
			// Serializes endpoint deletions sharing a pull, even without a merge.
			// Use the existing map, whose native IDs are hex, with a disjoint key.
			id := "lifecycle:" + strconv.FormatInt(d.Pull.ID, 10)
			if !claimMergeWorker(id) {
				return ErrMergeBusy
			}
			claimed = append(claimed, id)
			for _, op := range d.Operations {
				if !claimMergeWorker(op.ID) {
					return ErrMergeBusy
				}
				claimed = append(claimed, op.ID)
			}
		}
		for _, d := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			for _, c := range d.Comparisons {
				if d.Pull.TargetRepoID != repoID && c.ID == d.AcceptedComparisonID {
					continue
				}
				// A missing marker remains a repair refusal, never ownership proof.
				if err := removeComparisonAllocation(artifactRoot, c); err != nil {
					return err
				}
			}
			for _, op := range d.Operations {
				if err := removeOwnedPreparedAt(filepath.Join(artifactRoot, "prepared"), op); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func (s *Service) AfterRepositoryDeleted(ctx context.Context, repoID int64) error {
	host, err := s.host()
	if err != nil {
		return err
	}
	return RepositoryCleanup(host.RepositoryRoot(), host.ReviewArtifactRoot())(ctx, repoID)
}
