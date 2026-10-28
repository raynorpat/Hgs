package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/hg"
	"github.com/lib/pq"
	"modernc.org/sqlite"
)

type PullState string

const (
	PullOpen     PullState = "open"
	PullMerged   PullState = "merged"
	PullDeclined PullState = "declined"
)

var (
	ErrPullNotExist = errors.New("pull request not found")
	ErrPullInvalid  = errors.New("invalid pull request")
	ErrPullState    = errors.New("invalid pull request state transition")
	ErrPullConflict = errors.New("pull request transaction conflict; retry")
	ErrPullFilter   = errors.New("unsupported pull request filter")
)

// PullRequest is separate from Issue. Number belongs to the immutable target
// repository ID; selections are serialized hg.ReviewSelection data, not revsets.
// CurrentComparisonID == 0 honestly denotes an unprepared comparison.
type PullRequest struct {
	SourceDeleted       bool
	ID                  int64     `xorm:"pk autoincr 'id'"`
	TargetRepoID        int64     `xorm:"unique(pull_target_number) 'target_repo_id'"`
	SourceRepoID        int64     `xorm:"index 'source_repo_id'"`
	Number              int64     `xorm:"unique(pull_target_number)"`
	AuthorID            int64     `xorm:"index 'author_id'"`
	Title               string    `xorm:"VARCHAR(255)"`
	Body                string    `xorm:"MEDIUMTEXT"`
	SourceSelection     string    `xorm:"TEXT"`
	TargetSelection     string    `xorm:"TEXT"`
	CurrentComparisonID int64     `xorm:"'current_comparison_id'"`
	State               PullState `xorm:"VARCHAR(16) index"`
	Created             time.Time `xorm:"created"`
	Updated             time.Time `xorm:"updated"`
	// ReviewerIDs is accepted only by CreatePullRecord; assignments live in PullReview.
	ReviewerIDs []int64 `xorm:"-"`
}
type PullQuery struct {
	RepoID         int64
	State, Filter  string
	Page, PageSize int
}
type PullPage struct {
	Requests              []PullRequest
	Total, Page, PageSize int
}

func validPullText(title, body string) bool {
	return utf8.ValidString(title) && utf8.ValidString(body) && strings.TrimSpace(title) != "" && utf8.RuneCountInString(title) <= 255 && len(body) <= 64*1024
}

// PullSelections decodes bounded, typed selectors. Native existence and head
// disambiguation must additionally be checked by the managed hosting reader.
func PullSelections(p PullRequest) (source, target hg.ReviewSelection, err error) {
	for i, raw := range []string{p.SourceSelection, p.TargetSelection} {
		var selection hg.ReviewSelection
		if len(raw) > 16384 || json.Unmarshal([]byte(raw), &selection) != nil || selection.Ref.Name == "" || strings.ContainsRune(selection.Ref.Name, 0) {
			return source, target, ErrPullInvalid
		}
		switch selection.Ref.Kind {
		case hg.RefNode:
			if _, e := hg.ParseNodeID(selection.Ref.Name); e != nil {
				return source, target, ErrPullInvalid
			}
		case hg.RefBranch, hg.RefBookmark, hg.RefTag:
		default:
			return source, target, ErrPullInvalid
		}
		if selection.Head != "" {
			if _, e := hg.ParseNodeID(string(selection.Head)); e != nil {
				return source, target, ErrPullInvalid
			}
		}
		if i == 0 {
			source = selection
		} else {
			target = selection
		}
	}
	return source, target, nil
}
func pullReads(e Engine, actor int64, p PullRequest, locked bool) (RepositoryAccess, RepositoryAccess, error) {
	if p.SourceDeleted && !locked {
		// Only the final deletion transaction may grant this read-only exception.
		var source Repository
		if exists, err := e.Id(p.SourceRepoID).Get(&source); err != nil {
			return RepositoryAccess{}, RepositoryAccess{}, err
		} else if exists {
			return RepositoryAccess{}, RepositoryAccess{}, ErrPullNotExist
		}
		target, err := repositoryAccessFor(e, actor, p.TargetRepoID, false)
		if err == nil && !target.Read {
			err = ErrPullNotExist
		}
		return RepositoryAccess{}, target, err
	}
	source, err := repositoryAccessFor(e, actor, p.SourceRepoID, locked)
	if err != nil {
		if errors.Is(err, ErrRepoNotExist) || errors.Is(err, ErrForkLineage) {
			err = ErrPullNotExist
		}
		return source, RepositoryAccess{}, err
	}
	target, err := repositoryAccessFor(e, actor, p.TargetRepoID, locked)
	if errors.Is(err, ErrRepoNotExist) || errors.Is(err, ErrForkLineage) {
		err = ErrPullNotExist
	}
	if err == nil && (!source.Read || !target.Read) {
		err = ErrPullNotExist
	}
	return source, target, err
}
func pullCreationAllowed(e Engine, actor int64, p PullRequest, locked bool) error {
	if _, err := enabledPolicyAccount(e, actor, locked); err != nil {
		return err
	}
	source, _, err := pullReads(e, actor, p, locked)
	if err != nil {
		return err
	}
	if !source.Write {
		return ErrInvalidCredential
	}
	var family int64
	var privacy bool
	for i, id := range []int64{p.SourceRepoID, p.TargetRepoID} {
		var repo Repository
		ok, err := policyGet(e, locked, "repository", "id = ? AND state = ?", &repo, id, RepositoryReady)
		if err != nil {
			return err
		}
		if !ok {
			return ErrPullNotExist
		}
		ancestors, err := forkAncestors(e, id, locked)
		if err != nil {
			return err
		}
		root := id
		if len(ancestors) > 0 {
			root = ancestors[len(ancestors)-1]
		}
		if i == 0 {
			family = root
			privacy = repo.IsPrivate
		} else if root != family || repo.IsPrivate != privacy {
			return ErrPullInvalid
		}
	}
	return nil
}

// ValidatePullCreation is an early check only; persistence repeats the same
// current authority and lineage predicates under its short transaction.
func ValidatePullCreation(actor int64, p PullRequest) error {
	if err := validateNewPull(p); err != nil {
		return err
	}
	return pullCreationAllowed(x, actor, p, false)
}
func validateNewPull(p PullRequest) error {
	if p.SourceRepoID <= 0 || p.TargetRepoID <= 0 || p.ID != 0 || p.Number != 0 || p.AuthorID != 0 || p.State != "" || p.CurrentComparisonID != 0 || p.SourceDeleted || !validPullText(p.Title, p.Body) {
		return ErrPullInvalid
	}
	source, target, err := PullSelections(p)
	if err != nil {
		return err
	}
	if p.SourceRepoID == p.TargetRepoID && source == target {
		return ErrPullInvalid
	}
	return nil
}
func pullDBError(err error) error {
	if err == nil {
		return nil
	}
	var my *mysql.MySQLError
	var pg *pq.Error
	var sq *sqlite.Error
	if (errors.As(err, &my) && (my.Number == 1062 || my.Number == 1205 || my.Number == 1213)) ||
		(errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "40001" || pg.Code == "40P01" || pg.Code == "55P03")) ||
		(errors.As(err, &sq) && (sq.Code()&255 == 5 || sq.Code()&255 == 6 || sq.Code()&255 == 19)) {
		return fmt.Errorf("%w: %v", ErrPullConflict, err)
	}
	return err
}
func pullTransaction(ctx context.Context, ids []int64, apply func(*xorm.Session) error) (err error) {
	return pullTransactionEngine(x, ctx, ids, apply)
}

func pullTransactionEngine(engine *xorm.Engine, ctx context.Context, ids []int64, apply func(*xorm.Session) error) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	sess := engine.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return pullDBError(err)
	}
	defer sess.Rollback()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue
		}
		if err = lockReadyReviewRepository(sess, id); err != nil {
			return pullDBError(err)
		}
	}
	if err = apply(sess); err != nil {
		return pullDBError(err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return pullDBError(sess.Commit())
}

// CreatePullRecord persists only a new pending proposal after managed endpoint
// validation by collaboration.Service. It issues no native commands or grants.
func CreatePullRecord(ctx context.Context, actor int64, p PullRequest) (PullRequest, error) {
	if err := validateNewPull(p); err != nil {
		return PullRequest{}, err
	}
	err := pullTransaction(ctx, []int64{p.SourceRepoID, p.TargetRepoID}, func(sess *xorm.Session) error {
		if err := pullCreationAllowed(sess, actor, p, true); err != nil {
			return err
		}
		var last PullRequest
		query := "SELECT * FROM pull_request WHERE target_repo_id = ? ORDER BY number DESC LIMIT 1"
		if DbCfg.Type != "sqlite3" {
			query += " FOR UPDATE"
		}
		if _, err := sess.Sql(query, p.TargetRepoID).Get(&last); err != nil {
			return err
		}
		p.Number = last.Number + 1
		p.AuthorID = actor
		p.State = PullOpen
		if _, err := sess.Insert(&p); err != nil {
			return err
		}
		return assignPullReviewers(sess, actor, p, p.ReviewerIDs)
	})
	if err != nil {
		return PullRequest{}, err
	}
	return p, nil
}
func GetPullRecord(ctx context.Context, actor, target, number int64) (PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return PullRequest{}, err
	}
	var p PullRequest
	ok, err := x.Where("target_repo_id = ? AND number = ?", target, number).Get(&p)
	if err != nil {
		return PullRequest{}, err
	}
	if !ok {
		return PullRequest{}, ErrPullNotExist
	}
	if _, _, err = pullReads(x, actor, p, false); err != nil {
		return PullRequest{}, err
	}
	return p, nil
}
func GetPullRecordByID(ctx context.Context, actor, id int64) (PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return PullRequest{}, err
	}
	var p PullRequest
	ok, err := x.Id(id).Get(&p)
	if err != nil {
		return PullRequest{}, err
	}
	if !ok {
		return PullRequest{}, ErrPullNotExist
	}
	if _, _, err = pullReads(x, actor, p, false); err != nil {
		return PullRequest{}, err
	}
	return p, nil
}
func ListPullRecords(ctx context.Context, actor int64, q PullQuery) (PullPage, error) {
	out := PullPage{Requests: []PullRequest{}, Page: q.Page, PageSize: q.PageSize}
	if out.Page < 1 {
		out.Page = 1
	}
	if out.Page > 1000000 {
		out.Page = 1000000
	}
	if out.PageSize <= 0 {
		out.PageSize = 30
	}
	if out.PageSize > 100 {
		out.PageSize = 100
	}
	if q.RepoID < 0 {
		return out, ErrPullInvalid
	}
	if q.State != "" && q.State != "all" && q.State != string(PullOpen) && q.State != string(PullMerged) && q.State != string(PullDeclined) {
		return out, ErrPullInvalid
	}
	if q.Filter != "" && q.Filter != "all" && q.Filter != "created" && q.Filter != "assigned" && q.Filter != "needs_review" {
		return out, ErrPullFilter
	}
	if (q.Filter == "created" || q.Filter == "assigned" || q.Filter == "needs_review") && actor <= 0 {
		return out, ErrInvalidCredential
	}
	if q.RepoID > 0 {
		a, err := RepositoryAccessFor(actor, q.RepoID)
		if err != nil {
			return out, err
		}
		if !a.Read {
			return out, ErrPullNotExist
		}
	}
	start := (out.Page - 1) * out.PageSize
	for offset := 0; ; offset += 100 {
		if err := ctx.Err(); err != nil {
			return PullPage{}, err
		}
		var batch []PullRequest
		sess := x.NewSession()
		if q.RepoID > 0 {
			sess.Where("target_repo_id = ?", q.RepoID)
		}
		if q.State != "" && q.State != "all" {
			sess.And("state = ?", q.State)
		}
		if q.Filter == "created" {
			sess.And("author_id = ?", actor)
		}
		err := sess.Desc("id").Limit(100, offset).Find(&batch)
		sess.Close()
		if err != nil {
			return PullPage{}, err
		}
		for _, p := range batch {
			if q.Filter == "needs_review" {
				if ok, err := needsPullReview(p, actor); err != nil {
					return PullPage{}, err
				} else if !ok {
					continue
				}
			}
			if q.Filter == "assigned" {
				if ok, err := assignedPullReview(p, actor); err != nil {
					return PullPage{}, err
				} else if !ok {
					continue
				}
			}
			if _, _, err := pullReads(x, actor, p, false); errors.Is(err, ErrPullNotExist) {
				continue
			} else if err != nil {
				return PullPage{}, err
			}
			if out.Total >= start && len(out.Requests) < out.PageSize {
				out.Requests = append(out.Requests, p)
			}
			out.Total++
		}
		if len(batch) < 100 {
			return out, nil
		}
	}
}
func mutatePull(ctx context.Context, actor, id int64, apply func(*xorm.Session, PullRequest, RepositoryAccess, RepositoryAccess) error) error {
	p, err := GetPullRecordByID(ctx, actor, id)
	if err != nil {
		return err
	}
	return pullTransaction(ctx, []int64{p.SourceRepoID, p.TargetRepoID}, func(sess *xorm.Session) error {
		if _, err := enabledPolicyAccount(sess, actor, true); err != nil {
			return err
		}
		var current PullRequest
		ok, err := policyGet(sess, true, "pull_request", "id = ?", &current, id)
		if err != nil {
			return err
		}
		if !ok || current.SourceRepoID != p.SourceRepoID || current.TargetRepoID != p.TargetRepoID {
			return ErrPullNotExist
		}
		source, target, err := pullReads(sess, actor, current, true)
		if err != nil {
			return err
		}
		return apply(sess, current, source, target)
	})
}
func EditPullRecord(ctx context.Context, actor, id int64, title, body string) error {
	if !validPullText(title, body) {
		return ErrPullInvalid
	}
	return mutatePull(ctx, actor, id, func(sess *xorm.Session, p PullRequest, _, _ RepositoryAccess) error {
		if p.AuthorID != actor {
			return ErrInvalidCredential
		}
		_, err := sess.Id(id).Cols("title", "body").Update(&PullRequest{Title: title, Body: body})
		return err
	})
}

// SetPullRecordState is metadata persistence after service endpoint validation
// for reopening. Merged is deliberately unreachable through this generic API.
func SetPullRecordState(ctx context.Context, actor, id int64, state PullState) error {
	if state != PullOpen && state != PullDeclined {
		return ErrPullState
	}
	return mutatePull(ctx, actor, id, func(sess *xorm.Session, p PullRequest, source, target RepositoryAccess) error {
		if p.State == PullMerged {
			return ErrPullState
		}
		if actor != p.AuthorID && !target.Write {
			return ErrInvalidCredential
		}
		if state == PullOpen && !target.Write && (!source.Write || actor != p.AuthorID) {
			return ErrInvalidCredential
		}
		if p.State == state {
			return nil
		}
		_, err := sess.Id(id).Cols("state").Update(&PullRequest{State: state})
		return err
	})
}
