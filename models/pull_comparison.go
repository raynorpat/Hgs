package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
)

// PullComparison pins one immutable snapshot. SnapshotComplete is independent
// of preview readiness: a too-large preview is unavailable, but its complete
// snapshot can still serve authorized raw content. OwnershipNonce is internal.
// CurrentComparisonID is an observation, never permission to publish a merge.
type ComparisonOutcome string

const (
	ComparisonChanges    ComparisonOutcome = "changes"
	ComparisonIntegrated ComparisonOutcome = "already_integrated"
)

type PullComparison struct {
	ID                               int64 `xorm:"pk autoincr 'id'"`
	PullID                           int64 `xorm:"unique(pull_comparison_revision) 'pull_id'"`
	Revision                         int64 `xorm:"unique(pull_comparison_revision)"`
	SourceNode, TargetNode, BaseNode hg.NodeID
	ArtifactID                       string            `xorm:"unique VARCHAR(64) 'artifact_id'"`
	OwnershipNonce                   string            `xorm:"VARCHAR(64)" json:"-"`
	State                            string            `xorm:"VARCHAR(16)"`
	Outcome                          ComparisonOutcome `xorm:"VARCHAR(24)"`
	Failure                          string            `xorm:"VARCHAR(32)"`
	SnapshotComplete                 bool
	Created                          time.Time `xorm:"created"`
}

// Comparison privacy is checked independently of read authority. A leaf may
// change visibility while all actors retain both reads.
func pullComparisonPrivacy(e Engine, p PullRequest, locked bool) error {
	if p.SourceDeleted {
		return ErrPullState
	}
	var source, target Repository
	for _, endpoint := range []struct {
		id   int64
		repo *Repository
	}{{p.SourceRepoID, &source}, {p.TargetRepoID, &target}} {
		ok, err := policyGet(e, locked, "repository", "id = ? AND state = ?", endpoint.repo, endpoint.id, RepositoryReady)
		if err != nil {
			return err
		}
		if !ok {
			return ErrPullNotExist
		}
	}
	if source.IsPrivate != target.IsPrivate {
		return ErrPullState
	}
	return nil
}

func ValidatePullComparisonPrivacy(ctx context.Context, actor, id int64) error {
	p, err := GetPullRecordByID(ctx, actor, id)
	if err != nil {
		return err
	}
	return pullComparisonPrivacy(x, p, false)
}

func comparisonRandomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ReservePullComparison owns only a name, not an existing filesystem entry.
// The caller must exclusively allocate and mark its wrapper before native work.
// Both current reads and an enabled actor are required; it grants no write or
// publication authority. A pending current version disables prior readiness.
func ReservePullComparison(ctx context.Context, actor, pullID int64, expected ...PullRequest) (c PullComparison, err error) {
	c.PullID = pullID
	c.State = "pending"
	c.Created = time.Now().UTC()
	if c.ArtifactID, err = comparisonRandomID(); err != nil {
		return PullComparison{}, err
	}
	if c.OwnershipNonce, err = comparisonRandomID(); err != nil {
		return PullComparison{}, err
	}
	err = mutatePull(ctx, actor, pullID, func(sess *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		if _, _, err := PullSelections(p); err != nil {
			return err
		}
		if err := pullComparisonPrivacy(sess, p, true); err != nil {
			return err
		}
		if p.State == PullMerged {
			return ErrPullState
		}
		if len(expected) > 0 && !SamePullSelection(p, expected[0]) {
			return ErrPullConflict
		}
		if active, err := sess.Where("active_key = ?", mergeActiveKey(p.ID)).Get(new(MergeOperation)); err != nil {
			return err
		} else if active {
			return ErrMergeActive
		}
		var last PullComparison
		query := "SELECT * FROM pull_comparison WHERE pull_id = ? ORDER BY revision DESC LIMIT 1"
		if DbCfg.Type != "sqlite3" {
			query += " FOR UPDATE"
		}
		if _, err := sess.Sql(query, pullID).Get(&last); err != nil {
			return err
		}
		c.Revision = last.Revision + 1
		if _, err := sess.Insert(&c); err != nil {
			return err
		}
		_, err := sess.Id(p.ID).Cols("current_comparison_id").Update(&PullRequest{CurrentComparisonID: c.ID})
		return err
	})
	if err != nil {
		return PullComparison{}, err
	}
	return c, nil
}

// CompletePullComparison publishes immutable metadata after native snapshot
// validation. Only the latest pending allocation can complete; authority is
// rechecked in this short transaction. No native work runs under DB locks.
func CompletePullComparison(ctx context.Context, actor int64, c PullComparison) error {
	if !c.SnapshotComplete || (c.State != "ready" && !(c.State == "unavailable" && c.Failure == "comparison-too-large")) || (c.State == "ready" && c.Failure != "") {
		return ErrPullInvalid
	}
	for _, node := range []hg.NodeID{c.SourceNode, c.TargetNode, c.BaseNode} {
		if _, err := hg.ParseNodeID(string(node)); err != nil {
			return ErrPullInvalid
		}
	}
	if (c.Outcome != "" && c.Outcome != ComparisonChanges && c.Outcome != ComparisonIntegrated) || (c.Outcome == ComparisonIntegrated && (c.State != "ready" || c.BaseNode != c.SourceNode)) || (c.SourceNode == c.TargetNode && c.Outcome != ComparisonIntegrated) {
		return ErrPullInvalid
	}
	return mutatePull(ctx, actor, c.PullID, func(sess *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		if err := pullComparisonPrivacy(sess, p, true); err != nil {
			return err
		}
		var old PullComparison
		ok, err := policyGet(sess, true, "pull_comparison", "id = ? AND pull_id = ?", &old, c.ID, c.PullID)
		if err != nil {
			return err
		}
		if !ok || p.CurrentComparisonID != c.ID || old.State != "pending" {
			return ErrPullConflict
		}
		if old.ArtifactID != c.ArtifactID || old.OwnershipNonce != c.OwnershipNonce || old.Revision != c.Revision {
			return ErrPullInvalid
		}
		_, err = sess.Id(c.ID).Cols("source_node", "target_node", "base_node", "state", "outcome", "failure", "snapshot_complete").Update(&c)
		if err == nil && c.State == "ready" {
			err = expirePullApprovals(sess, p.ID, c)
		}
		return err
	})
}
func GetPullComparison(ctx context.Context, actor, pullID, id int64) (PullComparison, error) {
	p, err := GetPullRecordByID(ctx, actor, pullID)
	if err != nil {
		return PullComparison{}, err
	}
	if p.SourceDeleted && (p.State != PullMerged || id != p.CurrentComparisonID) {
		return PullComparison{}, ErrPullNotExist
	}
	if !p.SourceDeleted {
		if err := pullComparisonPrivacy(x, p, false); err != nil {
			return PullComparison{}, err
		}
	}
	var c PullComparison
	ok, err := x.Where("id = ? AND pull_id = ?", id, pullID).Get(&c)
	if err != nil {
		return PullComparison{}, err
	}
	if !ok {
		return PullComparison{}, ErrPullNotExist
	}
	return c, nil
}

// FailPullComparison is a fail-closed internal cleanup operation, including
// after actor revocation. It cannot publish content or change any frozen pins.
func FailPullComparison(ctx context.Context, id int64, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reason != "interrupted" && reason != "snapshot-failed" && reason != "artifact-invalid" && reason != "selection-changed" {
		return ErrPullInvalid
	}
	affected, err := x.Where("id = ? AND state = ?", id, "pending").Cols("state", "failure").Update(&PullComparison{State: "unavailable", Failure: reason})
	if err == nil && affected != 1 {
		return ErrPullConflict
	}
	return err
}

// ComparisonAllocations is for pre-admission recovery only. Its durable rows
// are not filesystem ownership proof; recovery must also verify each marker.
func ComparisonAllocations(ctx context.Context, after int64) ([]PullComparison, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows []PullComparison
	err := x.Where("id > ?", after).Asc("id").Limit(100).Find(&rows)
	return rows, err
}
func InvalidateComparisonArtifact(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	affected, err := x.Id(id).Cols("state", "failure", "snapshot_complete").Update(&PullComparison{State: "unavailable", Failure: "artifact-invalid", SnapshotComplete: false})
	if err == nil && affected != 1 {
		return ErrPullConflict
	}
	return err
}
