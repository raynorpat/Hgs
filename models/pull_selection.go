package models

import (
	"context"
	"encoding/json"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
	"strings"
)

// ResolvePullSource resolves an exact owner/repository name under the same live
// endpoint, ancestry and family policy used by final PR creation. It enumerates
// no catalog and returns no repository metadata on denial.
func ResolvePullSource(ctx context.Context, actor, target int64, name string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parts := strings.Split(strings.TrimSpace(name), "/")
	if len(name) > 512 || len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(name, `\\:?#`) {
		return 0, ErrPullInvalid
	}
	access, err := RepositoryAccessFor(actor, target)
	if err != nil || !access.Read {
		return 0, ErrPullNotExist
	}
	owner, err := GetUserByName(parts[0])
	if err != nil {
		return 0, ErrPullNotExist
	}
	repo, err := GetRepositoryByName(owner.Id, parts[1])
	if err != nil {
		return 0, ErrPullNotExist
	}
	if err := pullCreationAllowed(x, actor, PullRequest{SourceRepoID: repo.Id, TargetRepoID: target}, false); err != nil {
		return 0, ErrPullNotExist
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return repo.Id, nil
}

// SamePullSelection binds a native observation to the saved selection and
// comparison it started with. Repository identities never change.
func SamePullSelection(a, b PullRequest) bool {
	return a.ID == b.ID && a.SourceRepoID == b.SourceRepoID && a.TargetRepoID == b.TargetRepoID && a.SourceSelection == b.SourceSelection && a.TargetSelection == b.TargetSelection && a.CurrentComparisonID == b.CurrentComparisonID
}

func ReselectPullRecord(ctx context.Context, actor int64, expected PullRequest, source, target hg.ReviewSelection) error {
	src, _ := json.Marshal(source)
	dst, _ := json.Marshal(target)
	next := expected
	next.SourceSelection, next.TargetSelection = string(src), string(dst)
	if _, _, err := PullSelections(next); err != nil {
		return err
	}
	return mutatePull(ctx, actor, expected.ID, func(s *xorm.Session, p PullRequest, sourceAccess, targetAccess RepositoryAccess) error {
		if !SamePullSelection(p, expected) {
			return ErrPullConflict
		}
		if p.State != PullOpen {
			return ErrPullState
		}
		oldSource, oldTarget, err := PullSelections(p)
		if err != nil {
			return err
		}
		if target.Ref != oldTarget.Ref {
			return ErrPullInvalid
		}
		author := actor == p.AuthorID && sourceAccess.Write
		if (!author && source != oldSource) || (!author && !targetAccess.Write) {
			return ErrInvalidCredential
		}
		active, err := s.Where("active_key = ?", mergeActiveKey(p.ID)).Get(new(MergeOperation))
		if err != nil {
			return err
		}
		if active {
			return ErrMergeActive
		}
		if source == oldSource && target == oldTarget {
			return nil
		}
		_, err = s.Id(p.ID).Cols("source_selection", "target_selection", "current_comparison_id").Update(&PullRequest{SourceSelection: string(src), TargetSelection: string(dst)})
		if err != nil {
			return err
		}
		return pullEvent(s, p.ID, actor, 0, 0, "selection-changed", "")
	})
}
