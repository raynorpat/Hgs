package models

import (
	"context"
	"errors"
	"github.com/go-xorm/xorm"
	"strings"
	"time"
	"unicode/utf8"
)

type ReviewState string

const (
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes_requested"
	ReviewInactive         ReviewState = "inactive"
)

// Assignment and decision are independent: unassignment cannot dismiss a block.
type PullReview struct {
	ID           int64 `xorm:"pk autoincr 'id'"`
	PullID       int64 `xorm:"unique(pull_reviewer) 'pull_id'"`
	ReviewerID   int64 `xorm:"unique(pull_reviewer) 'reviewer_id'"`
	ComparisonID int64 `xorm:"'comparison_id'"`
	Assigned     bool
	State        ReviewState `xorm:"VARCHAR(24) index"`
	Updated      time.Time   `xorm:"updated"`
	Eligible     bool        `xorm:"-"`
}

// Eligibility deliberately excludes assignment: an unassigned live block is
// retired only by real authority loss, a new decision, or audited dismissal.
func pullReviewEligible(e Engine, p PullRequest, id int64) (bool, error) {
	if id == p.AuthorID {
		return false, nil
	}
	if _, err := enabledPolicyAccount(e, id, false); err != nil {
		if errors.Is(err, ErrInvalidCredential) {
			return false, nil
		}
		return false, err
	}
	src, dst, err := pullReads(e, id, p, false)
	if errors.Is(err, ErrPullNotExist) || errors.Is(err, ErrUserNotExist) {
		return false, nil
	}
	return err == nil && src.Read && dst.Write, err
}
func assignPullReviewers(e Engine, actor int64, p PullRequest, ids []int64) error {
	_, target, err := pullReads(e, actor, p, false)
	if err != nil {
		return err
	}
	if actor != p.AuthorID && !target.Write {
		return ErrInvalidCredential
	}
	wanted := map[int64]bool{}
	for _, id := range ids {
		ok, err := pullReviewEligible(e, p, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidCredential
		}
		wanted[id] = true
	}
	var existing []PullReview
	if err := e.Where("pull_id = ?", p.ID).Find(&existing); err != nil {
		return err
	}
	for _, r := range existing {
		assigned := wanted[r.ReviewerID]
		delete(wanted, r.ReviewerID)
		if r.Assigned == assigned {
			continue
		}
		r.Assigned = assigned
		if _, err := e.Id(r.ID).Cols("assigned").Update(&r); err != nil {
			return err
		}
		kind := "reviewer-unassigned"
		if assigned {
			kind = "reviewer-assigned"
		}
		if err := pullEvent(e, p.ID, actor, r.ReviewerID, 0, kind, ""); err != nil {
			return err
		}
	}
	for id := range wanted {
		if _, err := e.Insert(&PullReview{PullID: p.ID, ReviewerID: id, Assigned: true, State: ReviewInactive}); err != nil {
			return err
		}
		if err := pullEvent(e, p.ID, actor, id, 0, "reviewer-assigned", ""); err != nil {
			return err
		}
	}
	return nil
}
func AssignPullReviewers(ctx context.Context, actor, pull int64, ids []int64) error {
	return mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		return assignPullReviewers(s, actor, p, ids)
	})
}
func SubmitPullReview(ctx context.Context, actor, pull, comparison int64, state ReviewState) error {
	if state != ReviewApproved && state != ReviewChangesRequested {
		return ErrPullInvalid
	}
	return mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		if p.State != PullOpen {
			return ErrPullState
		}
		eligible, err := pullReviewEligible(s, p, actor)
		if err != nil {
			return err
		}
		if !eligible {
			return ErrInvalidCredential
		}
		var r PullReview
		ok, err := s.Where("pull_id = ? AND reviewer_id = ?", p.ID, actor).Get(&r)
		if err != nil {
			return err
		}
		if !ok || !r.Assigned {
			return ErrInvalidCredential
		}
		var c PullComparison
		ok, err = s.Where("id = ? AND pull_id = ?", comparison, p.ID).Get(&c)
		if err != nil {
			return err
		}
		if !ok || p.CurrentComparisonID != comparison || !c.SnapshotComplete || c.State != "ready" {
			return ErrPullConflict
		}
		r.State = state
		r.ComparisonID = comparison
		if _, err := s.Id(r.ID).Cols("state", "comparison_id").Update(&r); err != nil {
			return err
		}
		return pullEvent(s, p.ID, actor, actor, comparison, "review-submitted", string(state))
	})
}
func DismissPullReview(ctx context.Context, actor, pull, reviewer int64, reason string) error {
	if !utf8.ValidString(reason) || strings.TrimSpace(reason) == "" || len(reason) > 65536 {
		return ErrPullInvalid
	}
	return mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		u, err := enabledPolicyAccount(s, actor, false)
		if err != nil {
			return err
		}
		if !u.IsAdmin {
			return ErrInvalidCredential
		}
		var r PullReview
		ok, err := s.Where("pull_id = ? AND reviewer_id = ?", p.ID, reviewer).Get(&r)
		if err != nil {
			return err
		}
		if !ok {
			return ErrPullNotExist
		}
		if r.State == ReviewInactive {
			return nil
		}
		r.State = ReviewInactive
		if _, err = s.Id(r.ID).Cols("state").Update(&r); err != nil {
			return err
		}
		return pullEvent(s, p.ID, actor, reviewer, r.ComparisonID, "review-dismissed", reason)
	})
}
func expirePullApprovals(e Engine, pull int64, c PullComparison) error {
	var rows []PullReview
	if err := e.Where("pull_id = ? AND state = ?", pull, ReviewApproved).Find(&rows); err != nil {
		return err
	}
	for _, r := range rows {
		var old PullComparison
		ok, err := e.Id(r.ComparisonID).Get(&old)
		if err != nil {
			return err
		}
		if ok && old.SourceNode == c.SourceNode && old.TargetNode == c.TargetNode && old.BaseNode == c.BaseNode {
			continue
		}
		r.State = ReviewInactive
		if _, err := e.Id(r.ID).Cols("state").Update(&r); err != nil {
			return err
		}
		if err := pullEvent(e, pull, 0, r.ReviewerID, r.ComparisonID, "review-outdated", ""); err != nil {
			return err
		}
	}
	return nil
}

// ListPullReviews rechecks current effective authority, persists loss, and
// returns decisions independently of assignment. No approval count is imposed.
func ListPullReviews(ctx context.Context, actor, pull int64) (rows []PullReview, err error) {
	err = mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		if err := retirePullReviewAuthority(s); err != nil {
			return err
		}
		if err := s.Where("pull_id = ?", p.ID).Asc("id").Find(&rows); err != nil {
			return err
		}
		for i := range rows {
			ok, err := pullReviewEligible(s, p, rows[i].ReviewerID)
			if err != nil {
				return err
			}
			rows[i].Eligible = ok && rows[i].Assigned
		}
		return nil
	})
	return
}
func ListPullEvents(ctx context.Context, actor, pull int64) ([]PullEvent, error) {
	if _, err := GetPullRecordByID(ctx, actor, pull); err != nil {
		return nil, err
	}
	var rows []PullEvent
	err := x.Where("pull_id = ?", pull).Asc("id").Find(&rows)
	return rows, err
}

// retirePullReviewAuthority inactivates every live formal review whose reviewer
// is not effectively eligible in e's final state. Callers run it inside the
// transaction that changed account, access or ownership, so a later regrant
// cannot revive the decision; alternate grants keep eligibility and the block.
func retirePullReviewAuthority(e Engine) error {
	var live []PullReview
	if err := e.Where("state = ? OR state = ?", ReviewApproved, ReviewChangesRequested).Find(&live); err != nil {
		return err
	}
	for _, r := range live {
		var p PullRequest
		if ok, err := e.Id(r.PullID).Get(&p); err != nil {
			return err
		} else if !ok {
			continue
		}
		if ok, err := pullReviewEligible(e, p, r.ReviewerID); err != nil {
			return err
		} else if ok {
			continue
		}
		r.State = ReviewInactive
		if _, err := e.Id(r.ID).Cols("state").Update(&r); err != nil {
			return err
		}
		if err := pullEvent(e, p.ID, 0, r.ReviewerID, r.ComparisonID, "review-authority-lost", ""); err != nil {
			return err
		}
	}
	return nil
}

func commitRetiringReviews(sess *xorm.Session) error {
	if err := retirePullReviewAuthority(sess); err != nil {
		return err
	}
	return sess.Commit()
}

// assignedPullReview reports whether actor currently holds an assignment and is
// still eligible, so lists never count a reviewer who lost authority.
func assignedPullReview(p PullRequest, actor int64) (bool, error) {
	if ok, err := x.Where("pull_id = ? AND reviewer_id = ? AND assigned = ?", p.ID, actor, true).Get(new(PullReview)); err != nil || !ok {
		return false, err
	}
	return pullReviewEligible(x, p, actor)
}
