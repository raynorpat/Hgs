package models

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
)

type MergeState string

const (
	MergeUnstarted MergeState = "unstarted"
	MergePrepared  MergeState = "prepared"
	MergePublished MergeState = "published"
	MergeFinalized MergeState = "finalized"
	MergeFailed    MergeState = "failed"
)

var (
	ErrMergeIdempotency = errors.New("idempotency key was used for a different merge")
	ErrMergeBlocked     = errors.New("pull request merge is blocked")
	ErrMergeActive      = errors.New("another merge of this pull request is in progress")
	ErrMergePartial     = errors.New("previous merge partially published; resolve locally and refresh changed endpoints")
)

// MergeOperation is the durable PR-level identity of one merge. Prepared and
// Expectation are server-generated JSON kept verbatim so a retry or restart
// reuses the original prepared node and hosting operation, never a new one.
// ActiveKey is unique, so at most one non-terminal operation exists per pull.
type MergeOperation struct {
	ID             string     `xorm:"pk VARCHAR(64) 'id'"`
	PullID         int64      `xorm:"unique(pull_merge_key) 'pull_id'"`
	IdempotencyKey string     `xorm:"unique(pull_merge_key) VARCHAR(64)"`
	ActiveKey      string     `xorm:"unique VARCHAR(80)"`
	ActorID        int64      `xorm:"'actor_id'"`
	ComparisonID   int64      `xorm:"'comparison_id'"`
	SourceNode     hg.NodeID  `xorm:"VARCHAR(40)"`
	TargetNode     hg.NodeID  `xorm:"VARCHAR(40)"`
	ResultNode     hg.NodeID  `xorm:"VARCHAR(40)"`
	Bookmark       string     `xorm:"TEXT"`
	ArtifactID     string     `xorm:"VARCHAR(64)"`
	Prepared       string     `xorm:"TEXT"`
	Expectation    string     `xorm:"TEXT"`
	State          MergeState `xorm:"VARCHAR(16) index"`
	ErrorCode      string     `xorm:"VARCHAR(32)"`
	Created        time.Time  `xorm:"created"`
	Updated        time.Time  `xorm:"updated"`
}

type MergeAdmission struct {
	ID, IdempotencyKey     string
	PullID, ComparisonID   int64
	SourceNode, TargetNode hg.NodeID
}

func mergeActiveKey(pull int64) string { return "pull:" + strconv.FormatInt(pull, 10) }

func LatestPullMerge(ctx context.Context, actor, pull int64) (MergeOperation, error) {
	p, err := GetPullRecordByID(ctx, actor, pull)
	if err != nil {
		return MergeOperation{}, err
	}
	if p.SourceDeleted && p.State != PullMerged {
		return MergeOperation{}, ErrPullNotExist
	}
	var op MergeOperation
	if p.SourceDeleted {
		_, err := x.Where("pull_id = ? AND comparison_id = ? AND state = ?", p.ID, p.CurrentComparisonID, MergeFinalized).Get(&op)
		return op, err
	}
	if found, err := x.Where("active_key = ?", mergeActiveKey(pull)).Get(&op); err != nil || found {
		return op, err
	}
	_, err = x.Where("pull_id = ?", pull).Desc("created", "id").Get(&op)
	return op, err
}

// BeginMergeOperation admits a merge under current authority and durable state,
// or returns the existing operation for the same actor, key and payload.
func BeginMergeOperation(ctx context.Context, actor int64, in MergeAdmission) (op MergeOperation, err error) {
	if in.ID == "" || in.IdempotencyKey == "" || in.PullID <= 0 || in.ComparisonID <= 0 {
		return op, ErrPullInvalid
	}
	err = mutatePull(ctx, actor, in.PullID, func(s *xorm.Session, p PullRequest, _, target RepositoryAccess) error {
		found, err := s.Where("pull_id = ? AND idempotency_key = ?", p.ID, in.IdempotencyKey).Get(&op)
		if err != nil {
			return err
		}
		if found {
			if op.ActorID != actor || op.ComparisonID != in.ComparisonID || op.SourceNode != in.SourceNode || op.TargetNode != in.TargetNode {
				return ErrMergeIdempotency
			}
			return nil
		}
		var partial MergeOperation
		if found, err := s.Where("pull_id = ? AND source_node = ? AND target_node = ? AND error_code = ?", p.ID, in.SourceNode, in.TargetNode, "publication-failed").Get(&partial); err != nil {
			return err
		} else if found {
			return ErrMergePartial
		}
		c, err := validateMergePolicy(s, p, target, in.ComparisonID, in.SourceNode, in.TargetNode)
		if err != nil {
			return err
		}
		if ok, err := s.Where("active_key = ?", mergeActiveKey(p.ID)).Get(new(MergeOperation)); err != nil {
			return err
		} else if ok {
			return ErrMergeActive
		}
		op = MergeOperation{ID: in.ID, PullID: p.ID, IdempotencyKey: in.IdempotencyKey, ActiveKey: mergeActiveKey(p.ID), ActorID: actor, ComparisonID: c.ID, SourceNode: c.SourceNode, TargetNode: c.TargetNode, ArtifactID: in.ID, State: MergeUnstarted}
		if _, err := s.Insert(&op); err != nil {
			return err
		}
		return pullEvent(s, p.ID, actor, 0, c.ID, "merge-requested", "")
	})
	return op, err
}

func validateMergePolicy(s *xorm.Session, p PullRequest, target RepositoryAccess, comparison int64, sourceNode, targetNode hg.NodeID) (c PullComparison, err error) {
	err = func() error {
		if p.State != PullOpen {
			return ErrPullState
		}
		if !target.Write {
			return ErrInvalidCredential
		}
		ok, err := s.Where("id = ? AND pull_id = ?", comparison, p.ID).Get(&c)
		if err != nil {
			return err
		}
		if !ok || p.CurrentComparisonID != c.ID || c.State != "ready" || !c.SnapshotComplete || c.SourceNode != sourceNode || c.TargetNode != targetNode {
			return ErrPullConflict
		}
		var repos [2]Repository
		for i, id := range []int64{p.SourceRepoID, p.TargetRepoID} {
			if ok, err := policyGet(s, true, "repository", "id = ? AND state = ?", &repos[i], id, RepositoryReady); err != nil {
				return err
			} else if !ok {
				return ErrPullNotExist
			}
		}
		if repos[0].IsPrivate != repos[1].IsPrivate {
			return ErrPullInvalid
		}
		var blocks []PullReview
		if err := s.Where("pull_id = ? AND state = ?", p.ID, ReviewChangesRequested).Find(&blocks); err != nil {
			return err
		}
		for _, r := range blocks {
			if ok, err := pullReviewEligible(s, p, r.ReviewerID); err != nil {
				return err
			} else if ok {
				return ErrMergeBlocked
			}
		}
		return nil
	}()
	return c, err
}

// ValidateMergePublication binds existing PR operations to the native guard,
// including old serialized expectations. Only a successful absent-row lookup
// leaves a generic hosting operation unbound. The same repository/PR locks as
// review, comparison and privacy mutations serialize each authorization check.
func ValidateMergePublication(ctx context.Context, actor int64, id string, sourceRepo, targetRepo int64, source, target, result hg.NodeID) error {
	var op MergeOperation
	found, err := x.Id(id).Get(&op)
	if err != nil || !found {
		return err
	}
	if op.ActorID != actor || op.SourceNode != source || op.TargetNode != target || op.ResultNode != result || op.State != MergePrepared {
		return ErrMergeBlocked
	}
	return mutatePull(ctx, actor, op.PullID, func(s *xorm.Session, p PullRequest, _, access RepositoryAccess) error {
		if p.SourceRepoID != sourceRepo || p.TargetRepoID != targetRepo {
			return ErrMergeBlocked
		}
		_, err := validateMergePolicy(s, p, access, op.ComparisonID, source, target)
		return err
	})
}

func GetMergeOperation(ctx context.Context, actor, pull int64, id string) (MergeOperation, error) {
	p, err := GetPullRecordByID(ctx, actor, pull)
	if err != nil {
		return MergeOperation{}, err
	}
	if p.SourceDeleted && p.State != PullMerged {
		return MergeOperation{}, ErrPullNotExist
	}
	var op MergeOperation
	ok, err := x.Where("id = ? AND pull_id = ?", id, pull).Get(&op)
	if err == nil && p.SourceDeleted && (op.State != MergeFinalized || op.ComparisonID != p.CurrentComparisonID) {
		return MergeOperation{}, ErrPullNotExist
	}
	if err == nil && !ok {
		err = ErrPullNotExist
	}
	return op, err
}

// ActiveMergeOperations lists non-terminal operations for startup recovery. It
// is deliberately not actor-gated: recovery must not depend on current grants.
func ActiveMergeOperations(after string) (rows []MergeOperation, err error) {
	err = x.Where("id > ? AND state <> ? AND state <> ?", after, MergeFinalized, MergeFailed).Asc("id").Limit(100).Find(&rows)
	return
}

func transitionMerge(id string, from []MergeState, apply func(*xorm.Session, MergeOperation) error) error {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	var op MergeOperation
	if ok, err := policyGet(sess, true, "merge_operation", "id = ?", &op, id); err != nil {
		return err
	} else if !ok {
		return ErrPullNotExist
	}
	allowed := false
	for _, s := range from {
		allowed = allowed || op.State == s
	}
	if !allowed {
		return ErrPullState
	}
	if err := apply(sess, op); err != nil {
		return err
	}
	return sess.Commit()
}

// SetMergePrepared keeps the first prepared result: a repeat with the same
// bytes is a no-op and a different one is rejected, never regenerated.
func SetMergePrepared(id string, result hg.NodeID, bookmark, prepared, expectation string) error {
	return transitionMerge(id, []MergeState{MergeUnstarted, MergePrepared}, func(s *xorm.Session, op MergeOperation) error {
		if op.State == MergePrepared {
			if op.Prepared != prepared {
				return ErrPullConflict
			}
			return nil
		}
		op.State, op.ResultNode, op.Bookmark, op.Prepared, op.Expectation = MergePrepared, result, bookmark, prepared, expectation
		_, err := s.Id(id).Cols("state", "result_node", "bookmark", "prepared", "expectation").Update(&op)
		return err
	})
}

func SetMergePublished(id string) error {
	return transitionMerge(id, []MergeState{MergePrepared, MergePublished}, func(s *xorm.Session, op MergeOperation) error {
		if op.State == MergePublished {
			return nil
		}
		op.State = MergePublished
		_, err := s.Id(id).Cols("state").Update(&op)
		return err
	})
}

// FinalizeMerge marks the pull merged after confirmed publication. It checks
// no current grant: the published fact must be recorded even if the actor has
// since lost access.
func FinalizeMerge(id string) error {
	return transitionMerge(id, []MergeState{MergePublished, MergeFinalized}, func(s *xorm.Session, op MergeOperation) error {
		if op.State == MergeFinalized {
			return nil
		}
		op.State, op.ActiveKey = MergeFinalized, "done:"+op.ID
		if _, err := s.Id(id).Cols("state", "active_key").Update(&op); err != nil {
			return err
		}
		if _, err := s.Id(op.PullID).Cols("state").Update(&PullRequest{State: PullMerged}); err != nil {
			return err
		}
		if err := stampContributionObject(s, "pull", op.PullID); err != nil {
			return err
		}
		return pullEvent(s, op.PullID, op.ActorID, 0, op.ComparisonID, "merged", string(op.ResultNode))
	})
}

// FailMerge records a terminal outcome. publication-failed retains provenance
// and prevents a fresh key from regenerating a result for the same pins.
func FailMerge(id, code string) error {
	return transitionMerge(id, []MergeState{MergeUnstarted, MergePrepared, MergeFailed}, func(s *xorm.Session, op MergeOperation) error {
		if op.State == MergeFailed {
			return nil
		}
		op.State, op.ErrorCode, op.ActiveKey = MergeFailed, code, "done:"+op.ID
		if _, err := s.Id(id).Cols("state", "error_code", "active_key").Update(&op); err != nil {
			return err
		}
		return pullEvent(s, op.PullID, op.ActorID, 0, op.ComparisonID, "merge-failed", code)
	})
}

// LoadMergeOperation reads current durable state without an actor gate; callers
// are the operation's own driver, which already holds its identity.
func LoadMergeOperation(id string) (op MergeOperation, err error) {
	ok, err := x.Id(id).Get(&op)
	if err == nil && !ok {
		err = ErrPullNotExist
	}
	return
}
