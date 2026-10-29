// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-xorm/xorm"
)

// ContributionEvent is internal index data, never a public response DTO.
// Changeset ActorID is deliberately zero: attribution always joins live proof.
type ContributionEvent struct {
	ID           int64     `xorm:"pk autoincr 'id'"`
	ActorID      int64     `xorm:"index 'actor_id'"`
	ObjectID     int64     `xorm:"index 'object_id'"`
	Kind         string    `xorm:"VARCHAR(16) index"`
	SourceKey    string    `xorm:"VARCHAR(128) unique"`
	Node         string    `xorm:"VARCHAR(40)"`
	AuthorEmail  string    `xorm:"VARCHAR(254) index" json:"-"`
	OccurredAt   time.Time `xorm:"index"`
	OccurredUnix int64     `xorm:"index"`
	UTCDate      string    `xorm:"VARCHAR(10) index 'utc_date'"`
	UTCMonth     string    `xorm:"VARCHAR(7) index 'utc_month'"`
}

type ContributionFirstReview struct {
	ID         int64 `xorm:"pk autoincr 'id'"`
	ReviewerID int64 `xorm:"unique(first_review) 'reviewer_id'"`
	PullID     int64 `xorm:"unique(first_review) 'pull_id'"`
	OccurredAt time.Time
}

var ErrContributionStorage = errors.New("contributions: storage unavailable")
var ErrContributionInvalid = errors.New("contributions: invalid source")

// Keep private lookup material out of diagnostic SQL and driver error values.
func contributionEngine() *xorm.Engine {
	e := *x
	e.ShowSQL, e.ShowDebug, e.ShowErr, e.ShowInfo, e.ShowWarn = false, false, false, false, false
	e.Logger = xorm.NewSimpleLogger(io.Discard)
	return &e
}

func contributionError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrContributionChanged, ErrContributionInvalid, ErrContributionStorage, context.Canceled, context.DeadlineExceeded, ErrEmailProofInvalid, ErrEmailProofLimited, ErrEmailNotExist, ErrEmailNotActivated, ErrEmailAlreadyUsed, ErrUserNotExist} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrContributionStorage
}

func contributionTransaction(ctx context.Context, apply func(*xorm.Session) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := contributionEngine().NewSession()
	defer s.Close()
	if err := s.Begin(); err != nil {
		return contributionError(err)
	}
	defer s.Rollback()
	if err := apply(s); err != nil {
		return contributionError(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return contributionError(s.Commit())
}

func insertContributionEvent(e Engine, row ContributionEvent) (int64, error) {
	row.UTCDate, row.UTCMonth, row.OccurredUnix = row.OccurredAt.UTC().Format("2006-01-02"), row.OccurredAt.UTC().Format("2006-01"), row.OccurredAt.Unix()
	var old ContributionEvent
	ok, err := e.Where("source_key = ?", row.SourceKey).Get(&old)
	if err != nil {
		return 0, err
	}
	if ok {
		earlier := row.Kind == "review" && !row.OccurredAt.IsZero() && (old.OccurredAt.IsZero() || row.OccurredAt.Before(old.OccurredAt))
		if earlier || old.UTCDate == "" {
			if !earlier {
				row.OccurredAt = old.OccurredAt
				row.UTCDate, row.UTCMonth, row.OccurredUnix = old.OccurredAt.UTC().Format("2006-01-02"), old.OccurredAt.UTC().Format("2006-01"), old.OccurredAt.Unix()
			}
			if _, err := e.Id(old.ID).Cols("occurred_at", "occurred_unix", "utc_date", "utc_month").Update(&row); err != nil {
				return 0, err
			}
			if s, ok := e.(*xorm.Session); ok {
				kind := row.Kind
				if kind == "review" {
					kind = "pull"
				}
				if kind == "changeset" {
					if err := stampContributionChangesetMemberships(s, old); err != nil {
						return 0, err
					}
				} else {
					if err := stampContributionObject(s, kind, row.ObjectID); err != nil {
						return 0, err
					}
				}
			}
		}
		return old.ID, nil
	}
	_, err = e.Insert(&row)
	return row.ID, err
}

func firstContributionReview(e Engine, reviewer, pull int64, at time.Time) error {
	var first ContributionFirstReview
	ok, err := e.Where("reviewer_id = ? AND pull_id = ?", reviewer, pull).Get(&first)
	if err != nil {
		return err
	}
	if !ok {
		first = ContributionFirstReview{ReviewerID: reviewer, PullID: pull, OccurredAt: at}
		if _, err = e.Insert(&first); err != nil {
			return err
		}
	} else if at.Before(first.OccurredAt) {
		first.OccurredAt = at
		if _, err = e.Id(first.ID).Cols("occurred_at").Update(&first); err != nil {
			return err
		}
		if s, ok := e.(*xorm.Session); ok {
			if err := stampContributionObject(s, "pull", pull); err != nil {
				return err
			}
		}
	}
	return enqueueContribution(e, "review", first.ID)
}

type contributionObject struct {
	Event                            ContributionEvent
	RepositoryID, SourceRepositoryID int64
	SourceDeleted                    bool
	Kind                             string
	ID                               int64
}

func readContributionObject(e Engine, kind string, id int64) (object contributionObject, ok bool, err error) {
	object.Kind, object.ID = kind, id
	row := ContributionEvent{Kind: kind, ObjectID: id, SourceKey: fmt.Sprintf("%s:%d", kind, id)}
	switch kind {
	case "issue":
		var v Issue
		ok, err = e.Id(id).Get(&v)
		if v.IsPull {
			ok = false
		}
		row.ActorID, row.OccurredAt, object.RepositoryID = v.PosterId, v.Created, v.RepoId
	case "pull", "review":
		pullID := id
		var review ContributionFirstReview
		if kind == "review" {
			ok, err = e.Id(id).Get(&review)
			if err != nil || !ok {
				return object, ok, err
			}
			pullID = review.PullID
		}
		var p PullRequest
		ok, err = e.Id(pullID).Get(&p)
		if p.SourceDeleted && p.State != PullMerged {
			ok = false
		}
		object.RepositoryID, object.SourceRepositoryID, object.SourceDeleted = p.TargetRepoID, p.SourceRepoID, p.SourceDeleted
		row.ActorID, row.OccurredAt = p.AuthorID, p.Created
		if kind == "review" {
			row.SourceKey = fmt.Sprintf("review:%d:%d", review.ReviewerID, review.PullID)
			row.ObjectID, row.ActorID, row.OccurredAt = review.PullID, review.ReviewerID, review.OccurredAt
		}
	case "repository":
		var v Repository
		ok, err = e.Id(id).Get(&v)
		if v.State != RepositoryReady || v.CreatorID <= 0 {
			ok = false
		}
		row.ActorID, row.OccurredAt, object.RepositoryID = v.CreatorID, v.Created, v.Id
	default:
		return object, false, ErrContributionInvalid
	}
	object.Event = row
	return
}

// IndexContributionObject reads canonical records; recipient Actions never enter it.
func IndexContributionObject(ctx context.Context, kind string, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	object, ok, err := readContributionObject(contributionEngine(), kind, id)
	if err != nil || !ok {
		return contributionError(err)
	}
	return publishContributionObject(ctx, object)
}

func publishContributionObject(ctx context.Context, object contributionObject) error {
	ids := []int64{object.RepositoryID}
	if object.SourceRepositoryID > 0 && !object.SourceDeleted {
		ids = append(ids, object.SourceRepositoryID)
	}
	// Discovery is only a lock hint. The first canonical read in this transaction
	// occurs after sorted repository locks, shared with lifecycle deletion. This
	// also avoids establishing a stale repeatable-read snapshot on MySQL.
	return contributionError(pullTransactionEngine(contributionEngine(), ctx, ids, func(s *xorm.Session) error {
		current, ok, err := readContributionObject(s, object.Kind, object.ID)
		if err != nil || !ok {
			return err
		}
		if current.RepositoryID != object.RepositoryID || current.SourceRepositoryID != object.SourceRepositoryID || current.SourceDeleted != object.SourceDeleted {
			return ErrContributionInvalid
		}
		if current.SourceDeleted {
			// Only finalized source deletion grants target-only merged history.
			if exists, err := s.Id(current.SourceRepositoryID).Get(new(Repository)); err != nil {
				return err
			} else if exists {
				return ErrContributionInvalid
			}
		}
		object = current
		eventID, err := insertContributionEvent(s, object.Event)
		if err != nil {
			return err
		}
		var membership ContributionRepository
		ok, err = s.Where("event_id = ? AND repository_id = ?", eventID, object.RepositoryID).Get(&membership)
		if err != nil || ok {
			return err
		}
		source := object.SourceRepositoryID
		if object.SourceDeleted {
			source = 0
		}
		if err = stampContributionClass(s, object.RepositoryID, source, object.Event); err != nil {
			return err
		}
		_, err = s.Insert(&ContributionRepository{EventID: eventID, RepositoryID: object.RepositoryID})
		return err
	}))
}

// ContributionSourceIDs supports bounded backfill without materializing history.
func ContributionSourceIDs(ctx context.Context, kind string, after int64, limit int) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var table, predicate string
	switch kind {
	case "repository":
		table, predicate = "repository", "state = 'ready'"
	case "issue":
		table, predicate = "issue", "is_pull = false"
	case "pull":
		table, predicate = "pull_request", "1 = 1"
	case "review":
		table, predicate = "contribution_first_review", "1 = 1"
	default:
		return nil, ErrContributionInvalid
	}
	var rows []struct {
		ID int64 `xorm:"'id'"`
	}
	err := contributionEngine().Sql(fmt.Sprintf("SELECT id FROM %s WHERE id > ? AND %s ORDER BY id LIMIT %d", table, predicate, limit), after).Find(&rows)
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	return ids, contributionError(err)
}

func BackfillContributionReviews(ctx context.Context, after int64) (int64, bool, error) {
	var rows []PullEvent
	err := contributionEngine().Where("id > ? AND kind = ?", after, "review-submitted").Asc("id").Limit(100).Find(&rows)
	if err != nil {
		return after, false, contributionError(err)
	}
	for _, row := range rows {
		after = row.ID
		if row.ReviewerID <= 0 || row.ActorID != row.ReviewerID || (row.Reason != string(ReviewApproved) && row.Reason != string(ReviewChangesRequested)) {
			continue
		}
		if err := contributionTransaction(ctx, func(s *xorm.Session) error {
			return firstContributionReview(s, row.ReviewerID, row.PullID, row.Created)
		}); err != nil {
			return after, false, err
		}
	}
	return after, len(rows) == 100, nil
}
