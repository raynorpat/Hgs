// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"github.com/go-xorm/xorm"
	"time"
)

type ContributionRepository struct {
	EventID      int64 `xorm:"unique(contribution_membership) index 'event_id'"`
	RepositoryID int64 `xorm:"unique(contribution_membership) index 'repository_id'"`
	Generation   int64
}

type ContributionStage struct {
	EventID      int64 `xorm:"unique(contribution_stage) 'event_id'"`
	RepositoryID int64 `xorm:"unique(contribution_stage) index 'repository_id'"`
	Generation   int64 `xorm:"unique(contribution_stage)"`
}

type ContributionIndexState struct {
	RepositoryID               int64 `xorm:"pk 'repository_id'"`
	Generation, NextGeneration int64
	Snapshot                   string `xorm:"VARCHAR(40)"`
	SnapshotRevision           int64
	LastSuccess                time.Time
	Updating                   bool
	Unavailable                bool
}

func BeginContributionGeneration(ctx context.Context, repo int64) (generation int64, err error) {
	err = contributionTransaction(ctx, func(s *xorm.Session) error {
		if err := lockReadyReviewRepository(s, repo); err != nil {
			return err
		}
		var state ContributionIndexState
		ok, err := s.Id(repo).Get(&state)
		if err != nil {
			return err
		}
		if !ok {
			state.RepositoryID, state.NextGeneration, state.Updating = repo, 1, true
			_, err = s.Insert(&state)
		} else {
			state.NextGeneration++
			state.Updating = true
			state.Unavailable = false
			_, err = s.Id(repo).Cols("next_generation", "updating", "unavailable").Update(&state)
		}
		if err != nil {
			return err
		}
		generation = state.NextGeneration
		_, err = s.Where("repository_id = ?", repo).Delete(new(ContributionStage))
		return err
	})
	return
}

func StageContributionEvents(ctx context.Context, repo, generation int64, events []ContributionEvent) error {
	if len(events) > 100 {
		return ErrContributionInvalid
	}
	return contributionTransaction(ctx, func(s *xorm.Session) error {
		var state ContributionIndexState
		ok, err := s.Id(repo).Get(&state)
		if err != nil {
			return err
		}
		if !ok || state.NextGeneration != generation || !state.Updating {
			return ErrContributionInvalid
		}
		for _, event := range events {
			if event.Kind != "changeset" || len(event.Node) != 40 || event.SourceKey != "changeset:"+event.Node {
				return ErrContributionInvalid
			}
			event.ActorID = 0
			id, err := insertContributionEvent(s, event)
			if err != nil {
				return err
			}
			var existing ContributionStage
			ok, err := s.Where("event_id = ? AND repository_id = ? AND generation = ?", id, repo, generation).Get(&existing)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			if _, err = s.Insert(&ContributionStage{EventID: id, RepositoryID: repo, Generation: generation}); err != nil {
				return err
			}
		}
		return nil
	})
}

func PublishContributionGeneration(ctx context.Context, repo, generation int64, nonce, snapshot string, revision int64, now time.Time) error {
	return contributionTransaction(ctx, func(s *xorm.Session) error {
		if err := lockReadyReviewRepository(s, repo); err != nil {
			return err
		}
		var current Repository
		ok, err := s.Id(repo).Get(&current)
		if err != nil {
			return err
		}
		if !ok || current.StorageNonce != nonce {
			return ErrContributionInvalid
		}
		var state ContributionIndexState
		ok, err = s.Id(repo).Get(&state)
		if err != nil {
			return err
		}
		if !ok || state.NextGeneration != generation || !state.Updating {
			return ErrContributionInvalid
		}
		if _, err = s.Exec("DELETE FROM contribution_repository WHERE repository_id = ? AND event_id IN (SELECT id FROM contribution_event WHERE kind = ?)", repo, "changeset"); err != nil {
			return err
		}
		if _, err = s.Exec("INSERT INTO contribution_repository (event_id, repository_id, generation) SELECT event_id, repository_id, generation FROM contribution_stage WHERE repository_id = ? AND generation = ?", repo, generation); err != nil {
			return err
		}
		state.Generation, state.Snapshot, state.SnapshotRevision, state.LastSuccess = generation, snapshot, revision, now.UTC()
		state.Updating, state.Unavailable = false, false
		if _, err = s.Id(repo).AllCols().Update(&state); err != nil {
			return err
		}
		_, err = s.Where("repository_id = ?", repo).Delete(new(ContributionStage))
		return err
	})
}

func ContributionIndexStatus(ctx context.Context, repo int64) (ContributionIndexState, error) {
	if err := ctx.Err(); err != nil {
		return ContributionIndexState{}, err
	}
	var state ContributionIndexState
	_, err := contributionEngine().Id(repo).Get(&state)
	return state, contributionError(err)
}

func FailContributionGeneration(ctx context.Context, repo int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := contributionEngine().Id(repo).Cols("unavailable").Update(&ContributionIndexState{Unavailable: true})
	return contributionError(err)
}

func deleteContributionRepository(s *xorm.Session, repo int64) error {
	for _, bean := range []interface{}{new(ContributionRepository), new(ContributionStage), new(ContributionIndexState)} {
		if _, err := s.Where("repository_id = ?", repo).Delete(bean); err != nil {
			return err
		}
	}
	if _, err := s.Where("kind = ? AND object_id = ?", "repository", repo).Delete(new(ContributionJob)); err != nil {
		return err
	}
	_, err := s.Exec("DELETE FROM contribution_job WHERE kind = ? AND object_id IN (SELECT id FROM issue WHERE repo_id = ?)", "issue", repo)
	return err
}
