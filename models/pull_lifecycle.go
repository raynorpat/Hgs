package models

import (
	"context"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
)

// RepositoryCollaborationCleanup runs outside database locks, while the deleting
// repository and its immutable artifact owner rows still exist.
type RepositoryCollaborationCleanup func(context.Context, int64) error

type PullDeletion struct {
	Pull                 PullRequest
	Comparisons          []PullComparison
	Operations           []MergeOperation
	AcceptedComparisonID int64
}

func lifecyclePulls(s *xorm.Session, repoID int64) ([]PullDeletion, error) {
	var pulls []PullRequest
	q := "SELECT * FROM pull_request WHERE source_repo_id = ? OR target_repo_id = ? ORDER BY id"
	if DbCfg.Type != "sqlite3" {
		q += " FOR UPDATE"
	}
	if err := s.Sql(q, repoID, repoID).Find(&pulls); err != nil {
		return nil, err
	}
	var out []PullDeletion
	for _, p := range pulls {
		d := PullDeletion{Pull: p}
		q = "SELECT * FROM merge_operation WHERE pull_id = ? ORDER BY id"
		if DbCfg.Type != "sqlite3" {
			q += " FOR UPDATE"
		}
		if err := s.Sql(q, p.ID).Find(&d.Operations); err != nil {
			return nil, err
		}
		for _, op := range d.Operations {
			if op.State != MergeFinalized && op.State != MergeFailed {
				return nil, ErrReviewReserved
			}
			var native ReviewPublication
			found, err := policyGet(s, true, "review_publication", "i_d = ?", &native, op.ID)
			if err != nil {
				return nil, err
			}
			if found && (native.RepairRequired || (!native.Complete && !native.Failed && !native.Aborted)) {
				return nil, ErrReviewReserved
			}
			if op.State == MergeFinalized {
				if !found || !native.Complete || native.Failed || native.Aborted || p.State != PullMerged {
					return nil, ErrReviewReserved
				}
				if _, err := hg.ParseNodeID(string(op.ResultNode)); err != nil {
					return nil, ErrReviewReserved
				}
				if d.AcceptedComparisonID != 0 && d.AcceptedComparisonID != op.ComparisonID {
					return nil, ErrReviewReserved
				}
				d.AcceptedComparisonID = op.ComparisonID
			} else if found && native.Complete {
				return nil, ErrReviewReserved
			}
		}
		if p.State == PullMerged && d.AcceptedComparisonID == 0 {
			return nil, ErrReviewReserved
		}
		if err := s.Where("pull_id = ?", p.ID).Find(&d.Comparisons); err != nil {
			return nil, err
		}
		if d.AcceptedComparisonID != 0 {
			found := false
			for _, c := range d.Comparisons {
				if c.ID == d.AcceptedComparisonID && c.SnapshotComplete {
					found = true
				}
			}
			if !found {
				return nil, ErrReviewReserved
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func collaborationLifecycleBusy(s *xorm.Session, id int64) error {
	_, err := lifecyclePulls(s, id)
	return err
}

func lockDeletingRepository(s *xorm.Session, id int64) error {
	if DbCfg.Type == "sqlite3" {
		r, err := s.Exec("UPDATE repository SET id = id WHERE id = ? AND state = ?", id, RepositoryDeleting)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrRepoNotExist
		}
		return nil
	}
	var repo Repository
	ok, err := policyGet(s, true, "repository", "id = ? AND state = ?", &repo, id, RepositoryDeleting)
	if err == nil && !ok {
		return ErrRepoNotExist
	}
	return err
}

// RepositoryPullDeletion retains ownership until the final row transaction.
// It rejects legacy/inconsistent deleting rows with recoverable publications.
func RepositoryPullDeletion(ctx context.Context, repoID int64) ([]PullDeletion, error) {
	if repoID <= 0 {
		return nil, ErrRepoNotExist
	}
	var repo Repository
	if found, err := x.Id(repoID).Get(&repo); err != nil {
		return nil, err
	} else if !found {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := x.NewSession()
	defer s.Close()
	if err := s.Begin(); err != nil {
		return nil, err
	}
	defer s.Rollback()
	if err := lockDeletingRepository(s, repoID); err != nil {
		return nil, err
	}
	out, err := lifecyclePulls(s, repoID)
	if err != nil {
		return nil, err
	}
	return out, s.Commit()
}

// CheckRepositoryCollaborationChange is an early facade check. Actual mutation
// boundaries repeat the predicate under the same ready row lock as admission.
func CheckRepositoryCollaborationChange(ctx context.Context, actor, repoID int64, change string) error {
	switch change {
	case "rename", "transfer", "visibility", "delete":
	default:
		return ErrPullInvalid
	}
	return pullTransaction(ctx, []int64{repoID}, func(s *xorm.Session) error {
		a, err := repositoryAccessFor(s, actor, repoID, true)
		if err != nil {
			return err
		}
		if !a.Admin {
			return ErrInvalidCredential
		}
		return collaborationLifecycleBusy(s, repoID)
	})
}

func deleteRepositoryPullRows(s *xorm.Session, repoID int64) error {
	if err := lockDeletingRepository(s, repoID); err != nil {
		return err
	}
	if _, err := s.Where("repository_id = ? OR source_repository_id = ?", repoID, repoID).Delete(new(EditorDraft)); err != nil {
		return err
	}
	rows, err := lifecyclePulls(s, repoID)
	if err != nil {
		return err
	}
	for _, d := range rows {
		p := d.Pull
		if err := stampContributionObject(s, "pull", p.ID); err != nil {
			return err
		}
		purge := p.TargetRepoID == repoID || d.AcceptedComparisonID == 0
		if purge {
			if _, err := s.Exec("DELETE FROM contribution_job WHERE kind = ? AND object_id IN (SELECT id FROM contribution_first_review WHERE pull_id = ?)", "review", p.ID); err != nil {
				return err
			}
			if _, err := s.Where("kind = ? AND object_id = ?", "pull", p.ID).Delete(new(ContributionJob)); err != nil {
				return err
			}
			if _, err := s.Exec("DELETE FROM contribution_repository WHERE event_id IN (SELECT id FROM contribution_event WHERE kind IN ('pull','review') AND object_id = ?)", p.ID); err != nil {
				return err
			}
			if _, err := s.Where("pull_id = ?", p.ID).Delete(new(ContributionFirstReview)); err != nil {
				return err
			}
			for _, bean := range []interface{}{new(PullComment), new(PullReview), new(PullEvent), new(PullComparison)} {
				if _, err := s.Where("pull_id = ?", p.ID).Delete(bean); err != nil {
					return err
				}
			}
		} else {
			// Retain transcript anchors but never earlier unaccepted payload/pins.
			if _, err := s.Where("pull_id = ? AND id <> ?", p.ID, d.AcceptedComparisonID).Cols("source_node", "target_node", "base_node", "state", "failure", "snapshot_complete").Update(&PullComparison{State: "unavailable", Failure: "source-deleted"}); err != nil {
				return err
			}
			if _, err := s.NoAutoTime().Where("pull_id = ?", p.ID).Cols("assigned").Update(&PullReview{Assigned: false}); err != nil {
				return err
			}
		}
		// MergeOperation and ReviewPublication remain positive denial bindings.
		// Their terminal states and immutable provenance cannot become generic writes.
		if p.TargetRepoID == repoID {
			if _, err := s.Id(p.ID).Delete(new(PullRequest)); err != nil {
				return err
			}
			continue
		}
		p.SourceDeleted = true
		if purge {
			p.State, p.AuthorID, p.Title, p.Body = PullDeclined, 0, "", ""
			p.SourceSelection, p.TargetSelection, p.CurrentComparisonID = "", "", 0
		} else {
			p.CurrentComparisonID = d.AcceptedComparisonID
		}
		if _, err := s.Id(p.ID).NoAutoTime().Cols("source_deleted", "state", "author_id", "title", "body", "source_selection", "target_selection", "current_comparison_id").Update(&p); err != nil {
			return err
		}
		if !purge {
			if err := stampContributionObject(s, "pull", p.ID); err != nil {
				return err
			}
		}
	}
	_, err = s.Where("repo_id = ?", repoID).Delete(new(RepositoryVisit))
	return err
}
