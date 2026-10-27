// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"errors"
	"fmt"
	"github.com/go-xorm/xorm"
	"sort"
)

// ReviewPublication is immutable operation identity plus a durable completion bit.
// Payload is bounded server-generated JSON; it contains no credential or grant.
type ReviewPublication struct {
	ID             string `xorm:"pk VARCHAR(64)"`
	ActorID        int64
	Payload        string `xorm:"TEXT"`
	Complete       bool
	Started        bool
	Aborted        bool
	Failed         bool
	RepairRequired bool
}

// ReviewReservation fences an entire store, including every selector in it.
type ReviewReservation struct {
	RepoID      int64  `xorm:"pk"`
	OperationID string `xorm:"VARCHAR(64) index 'operation_id'"`
}

var ErrReviewReserved = errors.New("repository reserved by a review publication")
var ErrReviewPublicationTerminal = errors.New("review publication is terminal; a fresh authorized review is required")

func BeginReviewPublication(op ReviewPublication, ids []int64) (bool, error) {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return false, err
	}
	defer sess.Rollback()
	var existing ReviewPublication
	found, err := sess.Id(op.ID).Get(&existing)
	if err != nil {
		return false, err
	}
	if found {
		if existing.ActorID != op.ActorID || existing.Payload != op.Payload {
			return false, errors.New("review operation identity mismatch")
		}
		if existing.Failed {
			return false, ErrReviewPublicationTerminal
		}
		if existing.Aborted {
			return false, errors.New("review operation was aborted; prepare a fresh operation")
		}
		return existing.Complete, nil
	}
	// Row updates serialize with repository tombstones on every supported backend.
	unique := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			return false, ErrReviewReserved
		}
		unique[id] = true
	}
	ordered := make([]int64, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, id := range ordered {
		if e := lockReadyReviewRepository(sess, id); e != nil {
			return false, e
		}
		if reserved, e := forkReserved(sess, id); e != nil {
			return false, e
		} else if reserved {
			return false, ErrReviewReserved
		}
		if _, e := sess.Insert(&ReviewReservation{RepoID: id, OperationID: op.ID}); e != nil {
			return false, fmt.Errorf("%w: %v", ErrReviewReserved, e)
		}
	}
	if _, err = sess.Insert(&op); err != nil {
		return false, err
	}
	return false, sess.Commit()
}
func PendingReviewReservations() ([]ReviewReservation, error) {
	var rows []ReviewReservation
	err := x.Find(&rows)
	return rows, err
}
func RepositoryReviewReserved(id int64) (bool, error) {
	return x.Id(id).Get(new(ReviewReservation))
}
func CompleteReviewPublication(id string) error {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	if n, err := sess.Id(id).And("started = ?", true).And("failed = ?", false).And("aborted = ?", false).Cols("complete").Update(&ReviewPublication{Complete: true}); err != nil {
		return err
	} else if n != 1 {
		return errors.New("review operation missing")
	}
	if _, err := sess.Where("operation_id = ?", id).Delete(new(ReviewReservation)); err != nil {
		return err
	}
	return sess.Commit()
}

// ReviewPublicationStatus is the bounded, local recovery identity used by PR
// reconciliation. It neither fetches network data nor grants write authority.
func ReviewPublicationStatus(id string) (ReviewPublication, error) {
	var op ReviewPublication
	ok, err := x.Id(id).Get(&op)
	if err != nil {
		return op, err
	}
	if !ok {
		return op, errors.New("review operation missing")
	}
	return op, nil
}
func StartReviewPublication(id string) error {
	op, err := ReviewPublicationStatus(id)
	if err != nil {
		return err
	}
	if op.Failed || op.Aborted || op.Complete {
		return ErrReviewPublicationTerminal
	}
	if op.Started {
		return nil
	}
	n, err := x.Id(id).And("failed = ?", false).And("complete = ?", false).And("aborted = ?", false).Cols("started").Update(&ReviewPublication{Started: true})
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("review operation missing")
	}
	return nil
}
func AbortReviewPublication(id string) error {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	if n, err := sess.Id(id).And("started = ?", false).Cols("aborted").Update(&ReviewPublication{Aborted: true}); err != nil {
		return err
	} else if n != 1 {
		return errors.New("cannot abort started publication")
	}
	if _, err := sess.Where("operation_id = ?", id).Delete(new(ReviewReservation)); err != nil {
		return err
	}
	return sess.Commit()
}

// reserve-aware tombstoning uses the same repository row lock as acquisition.
// A pending review must remain ready so its original authorized operation can
// recover; an attempted deletion must not strand it in deleting state.
func tombstoneReviewSafe(repoID int64) error {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	if err := lockReadyReviewRepository(sess, repoID); err != nil {
		return err
	}
	if reserved, err := sess.Id(repoID).Get(new(ReviewReservation)); err != nil {
		return err
	} else if reserved {
		return ErrReviewReserved
	}
	if reserved, err := forkReserved(sess, repoID); err != nil {
		return err
	} else if reserved {
		return ErrReviewReserved
	}
	if err := forkDependents(sess, repoID); err != nil {
		return err
	}
	if err := setRepositoryState(sess, repoID, RepositoryReady, RepositoryDeleting); err != nil {
		return err
	}
	return sess.Commit()
}

func lockReadyReviewRepository(sess *xorm.Session, id int64) error {
	if DbCfg.Type == "sqlite3" {
		result, err := sess.Exec("UPDATE repository SET id = id WHERE id = ? AND state = ?", id, RepositoryReady)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrReviewReserved
		}
		return nil
	}
	// Locking reads use current state on PostgreSQL/MySQL. In particular MySQL
	// reports zero changed rows for a no-op UPDATE without clientFoundRows.
	ok, err := sess.Sql("SELECT id FROM repository WHERE id = ? AND state = ? FOR UPDATE", id, RepositoryReady).Get(new(Repository))
	if err != nil {
		return err
	}
	if !ok {
		return ErrReviewReserved
	}
	return nil
}

// ConcludeRevokedReviewPublication records observed facts, never a reusable
// authority or an instruction to finish publishing. Failed identities cannot
// be resumed. Only an uncertain target retains its reservation.
func ConcludeRevokedReviewPublication(id string, targetID int64, complete, repair bool) error {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	outcome := &ReviewPublication{Complete: complete, Failed: !complete, RepairRequired: repair}
	n, err := sess.Id(id).And("started = ?", true).And("failed = ?", false).And("complete = ?", false).Cols("complete", "failed", "repair_required").Update(outcome)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrReviewPublicationTerminal
	}
	var rows []ReviewReservation
	if err = sess.Where("operation_id = ?", id).Find(&rows); err != nil {
		return err
	}
	for _, row := range rows {
		if repair && row.RepoID == targetID {
			continue
		}
		if _, err = sess.Id(row.RepoID).Delete(new(ReviewReservation)); err != nil {
			return err
		}
	}
	return sess.Commit()
}
