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
	return pullReviewEligibleWithLock(e, p, id, false)
}

func pullReviewEligibleWithLock(e Engine, p PullRequest, id int64, locked bool) (bool, error) {
	if id == p.AuthorID {
		return false, nil
	}
	if _, err := enabledPolicyAccount(e, id, locked); err != nil {
		if errors.Is(err, ErrInvalidCredential) {
			return false, nil
		}
		return false, err
	}
	src, dst, err := pullReads(e, id, p, locked)
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
		ok, err := pullReviewEligibleWithLock(e, p, id, true)
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
		if !assigned && r.State == ReviewChangesRequested {
			eligible, err := pullReviewEligibleWithLock(e, p, r.ReviewerID, true)
			if err != nil {
				return err
			}
			if eligible {
				return ErrPullConflict
			}
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
		if err := pullComparisonPrivacy(s, p, true); err != nil {
			return err
		}
		if p.State != PullOpen {
			return ErrPullState
		}
		eligible, err := pullReviewEligibleWithLock(s, p, actor, true)
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
	return mutatePull(ctx, actor, pull, func(s *xorm.Session, p PullRequest, _, target RepositoryAccess) error {
		if !target.Admin {
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
	p, err := GetPullRecordByID(ctx, actor, pull)
	if err != nil {
		return nil, err
	}
	if p.SourceDeleted {
		if p.State != PullMerged {
			return nil, nil
		}
		err = x.Where("pull_id = ?", p.ID).Asc("id").Find(&rows)
		return rows, err
	}

	// Reading a public transcript does not require a mutation-capable actor.
	// Keep policy maintenance inside the same current-read transaction and scope
	// it to this pull; mutation entry points still use mutatePull unchanged.
	err = pullTransaction(ctx, []int64{p.SourceRepoID, p.TargetRepoID}, func(s *xorm.Session) error {
		if actor != 0 {
			if _, err := enabledPolicyAccount(s, actor, true); err != nil {
				return err
			}
		}
		var current PullRequest
		ok, err := policyGet(s, true, "pull_request", "id = ?", &current, p.ID)
		if err != nil {
			return err
		}
		if !ok || current.SourceRepoID != p.SourceRepoID || current.TargetRepoID != p.TargetRepoID || current.SourceDeleted != p.SourceDeleted {
			return ErrPullNotExist
		}
		if _, _, err = pullReads(s, actor, current, true); err != nil {
			return err
		}
		p = current
		if err := retirePullReviewAuthorityFor(s, p.ID); err != nil {
			return err
		}
		if err := s.Where("pull_id = ?", p.ID).Asc("id").Find(&rows); err != nil {
			return err
		}
		for i := range rows {
			ok, err := pullReviewEligibleWithLock(s, p, rows[i].ReviewerID, true)
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
// Submission holds its current authority rows until commit. A permission write
// waits on those rows before reaching this scan, which must see the resulting
// decision even if that writer already established a repeatable-read snapshot.
// All subsequent policy reads must also be current, including alternate grants.
// Opposing lock orders may deadlock; callers must propagate failure and roll back
// the permission change together with retirement and its audit events.
func retirePullReviewAuthority(e *xorm.Session) error {
	return retirePullReviewAuthorityFor(e, 0)
}

// A positive pull ID limits read-triggered maintenance to its authorized transcript.
// Policy mutations retain the global scan through retirePullReviewAuthority.
func retirePullReviewAuthorityFor(e *xorm.Session, pullID int64) error {
	var live []PullReview
	var query *xorm.Session
	predicate := "(state = ? OR state = ?)"
	args := []interface{}{ReviewApproved, ReviewChangesRequested}
	if pullID > 0 {
		predicate += " AND pull_id = ?"
		args = append(args, pullID)
	}
	if DbCfg.Type == "sqlite3" {
		query = e.Where(predicate, args...).Asc("id")
	} else {
		query = e.Sql("SELECT * FROM pull_review WHERE "+predicate+" ORDER BY id FOR UPDATE", args...)
	}
	if err := query.Find(&live); err != nil {
		return err
	}
	for _, r := range live {
		var p PullRequest
		if ok, err := policyGet(e, true, "pull_request", "id = ?", &p, r.PullID); err != nil {
			return err
		} else if !ok {
			continue
		}
		// Completed deleted-source history is a read-only accepted transcript.
		if p.SourceDeleted && p.State == PullMerged {
			continue
		}
		if ok, err := pullReviewEligibleWithLock(e, p, r.ReviewerID, true); err != nil {
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
	defer sess.Rollback()
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

// Needs-review uses persisted pins, not native scans. Same-pin comparison
// replacement retains a decision; unresolved changes wait for changed pins.
func needsPullReview(p PullRequest, actor int64) (bool, error) {
	if p.State != PullOpen {
		return false, nil
	}
	if err := pullComparisonPrivacy(x, p, false); err != nil {
		if errors.Is(err, ErrPullState) || errors.Is(err, ErrPullNotExist) {
			return false, nil
		}
		return false, err
	}
	if eligible, err := assignedPullReview(p, actor); err != nil || !eligible {
		return false, err
	}
	var review PullReview
	if _, err := x.Where("pull_id = ? AND reviewer_id = ?", p.ID, actor).Get(&review); err != nil {
		return false, err
	}
	var current PullComparison
	if found, err := x.Where("id = ? AND pull_id = ? AND state = ? AND snapshot_complete = ?", p.CurrentComparisonID, p.ID, "ready", true).Get(&current); err != nil || !found {
		return false, err
	}
	if review.State == "" || review.State == ReviewInactive || review.ComparisonID == 0 {
		return true, nil
	}
	var decided PullComparison
	found, err := x.Where("id = ? AND pull_id = ?", review.ComparisonID, p.ID).Get(&decided)
	if err != nil {
		return false, err
	}
	return !found || decided.SourceNode != current.SourceNode || decided.TargetNode != current.TargetNode, nil
}
