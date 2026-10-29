// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"path/filepath"
)

func (s *Service) IndexRepository(ctx context.Context, repoID int64) error {
	s.mu.Lock()
	s.started = true
	admit := s.admission
	s.mu.Unlock()
	if admit == nil || s.path == nil || s.runner == nil || repoID <= 0 {
		return errors.New("contributions: maintenance admission required")
	}
	select {
	case s.scan <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.scan }()
	err := admit(ctx, repoID, func(ctx context.Context, admitted string) error {
		path, err := s.path(ctx, repoID)
		if err != nil {
			return err
		}
		if filepath.Clean(path) != filepath.Clean(admitted) {
			return models.ErrContributionInvalid
		}
		repo, err := models.GetRepositoryById(repoID)
		if err != nil {
			return err
		}
		if repo.State != models.RepositoryReady {
			return models.ErrContributionInvalid
		}
		generation, err := models.BeginContributionGeneration(ctx, repoID)
		if err != nil {
			return err
		}
		query := hg.HistoryQuery{BeforeRevision: -1, Limit: 100}
		for {
			page, err := s.runner.History(ctx, path, query)
			if errors.Is(err, hg.ErrEmptyRepository) {
				break
			}
			if err != nil {
				return err
			}
			events := make([]models.ContributionEvent, 0, len(page.Entries))
			for _, change := range page.Entries {
				events = append(events, models.ContributionEvent{Kind: "changeset", SourceKey: "changeset:" + string(change.Node), Node: string(change.Node), AuthorEmail: authorEmail(change.User), OccurredAt: change.Date.UTC()})
			}
			if err = models.StageContributionEvents(ctx, repoID, generation, events); err != nil {
				return err
			}
			query.Snapshot, query.SnapshotRevision = page.Snapshot, page.SnapshotRevision
			if !page.HasMore {
				break
			}
			if page.NextBefore < 0 || (query.BeforeRevision >= 0 && page.NextBefore >= query.BeforeRevision) {
				return hg.ErrInvalidCursor
			}
			query.BeforeRevision = page.NextBefore
		}
		checked, err := s.path(ctx, repoID)
		if err != nil {
			return err
		}
		if filepath.Clean(checked) != filepath.Clean(admitted) {
			return models.ErrContributionInvalid
		}
		if err = models.PublishContributionGeneration(ctx, repoID, generation, repo.StorageNonce, string(query.Snapshot), query.SnapshotRevision, s.now()); err != nil {
			return err
		}
		return s.IndexObject(ctx, "repository", repoID)
	})
	if err != nil && ctx.Err() == nil {
		_ = models.FailContributionGeneration(ctx, repoID)
	}
	return err
}
