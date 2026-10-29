// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"database/sql"
	"time"
)

type ContributionJob struct {
	ID                   int64  `xorm:"pk autoincr 'id'"`
	Kind                 string `xorm:"VARCHAR(16) unique(contribution_job)"`
	ObjectID             int64  `xorm:"unique(contribution_job) 'object_id'"`
	Requested, Completed int64
}

func enqueueContribution(e Engine, kind string, id int64) error {
	if id <= 0 || (kind != "repository" && kind != "issue" && kind != "pull" && kind != "review") {
		return ErrContributionInvalid
	}
	result, err := e.Exec("UPDATE contribution_job SET requested = requested + 1 WHERE kind = ? AND object_id = ?", kind, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n > 0 {
		return err
	}
	_, err = e.Insert(&ContributionJob{Kind: kind, ObjectID: id, Requested: 1})
	return err
}

func EnqueueContribution(ctx context.Context, kind string, objectID int64) error {
	if objectID <= 0 || (kind != "repository" && kind != "issue" && kind != "pull" && kind != "review") {
		return ErrContributionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, owned, err := contributionDatabase(ctx)
	if err != nil {
		return contributionError(err)
	}
	if owned {
		defer db.Close()
	}
	return enqueueContributionSQL(ctx, db, kind, objectID)
}

type contributionExecutor interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func enqueueContributionSQL(ctx context.Context, db contributionExecutor, kind string, objectID int64) error {
	update := "UPDATE contribution_job SET requested = requested + 1 WHERE kind = ? AND object_id = ?"
	insert := "INSERT INTO contribution_job (kind, object_id, requested, completed) VALUES (?, ?, 1, 0)"
	if DbCfg.Type == "postgres" {
		update = "UPDATE contribution_job SET requested = requested + 1 WHERE kind = $1 AND object_id = $2"
		insert = "INSERT INTO contribution_job (kind, object_id, requested, completed) VALUES ($1, $2, 1, 0)"
	}
	result, err := db.ExecContext(ctx, update, kind, objectID)
	if err != nil {
		return contributionError(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n > 0 {
		return contributionError(err)
	}
	_, err = db.ExecContext(ctx, insert, kind, objectID)
	// A concurrent first insertion is resolved by incrementing its unique row.
	if err != nil {
		result, retryErr := db.ExecContext(ctx, update, kind, objectID)
		if retryErr != nil {
			return contributionError(retryErr)
		}
		n, retryErr := result.RowsAffected()
		if retryErr != nil {
			return contributionError(retryErr)
		}
		if n != 1 {
			return ErrContributionStorage
		}
		return nil
	}
	return contributionError(err)
}

func PendingContributionJobs(ctx context.Context, after int64, limit int) ([]ContributionJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var rows []ContributionJob
	err := contributionEngine().Where("id > ? AND requested > completed", after).Asc("id").Limit(limit).Find(&rows)
	return rows, contributionError(err)
}

func CompleteContributionJob(ctx context.Context, id, sequence int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := contributionEngine().Exec("UPDATE contribution_job SET completed = ? WHERE id = ? AND completed < ? AND requested >= ?", sequence, id, sequence, sequence)
	return contributionError(err)
}
