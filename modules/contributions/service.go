// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/log"
	"sync"
	"time"
)

type RepositoryPath func(context.Context, int64) (string, error)
type ReadAccess func(context.Context, int64, int64) (bool, error)
type MaintenanceRead func(context.Context, int64, func(context.Context, string) error) error

type Service struct {
	runner    *hg.Runner
	path      RepositoryPath
	read      ReadAccess
	now       func() time.Time
	mu        sync.Mutex
	admission MaintenanceRead
	started   bool
	scan      chan struct{}
}

func NewService(runner *hg.Runner, path RepositoryPath, read ReadAccess, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{runner: runner, path: path, read: read, now: now, scan: make(chan struct{}, 1)}
}

// SetMaintenanceRead is mandatory and may only be configured once, before use.
func (s *Service) SetMaintenanceRead(admission MaintenanceRead) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if admission == nil || s.started || s.admission != nil {
		return errors.New("contributions: maintenance admission already configured or unavailable")
	}
	s.admission = admission
	return nil
}

func (s *Service) IndexObject(ctx context.Context, kind string, objectID int64) error {
	return models.IndexContributionObject(ctx, kind, objectID)
}

func (s *Service) Reconcile(ctx context.Context) error {
	for after := int64(0); ; {
		next, more, err := models.BackfillContributionReviews(ctx, after)
		if err != nil {
			return err
		}
		if !more {
			break
		}
		after = next
	}
	for _, kind := range []string{"repository", "issue", "pull", "review"} {
		for after := int64(0); ; {
			ids, err := models.ContributionSourceIDs(ctx, kind, after, 100)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err = models.EnqueueContribution(ctx, kind, id); err != nil {
					return err
				}
				after = id
			}
			if len(ids) < 100 {
				break
			}
		}
	}
	return nil
}

func (s *Service) drain(ctx context.Context) error {
	for after := int64(0); ; {
		jobs, err := models.PendingContributionJobs(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if err = ctx.Err(); err != nil {
				return err
			}
			after = job.ID
			if job.Kind == "repository" {
				err = s.IndexRepository(ctx, job.ObjectID)
			} else {
				err = s.IndexObject(ctx, job.Kind, job.ObjectID)
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Warn("contribution indexing deferred for job %d", job.ID)
				continue
			}
			if err = models.CompleteContributionJob(ctx, job.ID, job.Requested); err != nil {
				return err
			}
		}
		if len(jobs) < 100 {
			return nil
		}
	}
}

// Run owns no detached tasks. Its caller cancels and joins it before hosting/DB close.
func (s *Service) Run(ctx context.Context) {
	reconcile := func() {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			log.Warn("contribution reconciliation unavailable")
		}
	}
	reconcile()
	jobs := time.NewTicker(2 * time.Second)
	defer jobs.Stop()
	rebuild := time.NewTicker(5 * time.Minute)
	defer rebuild.Stop()
	for {
		if err := s.drain(ctx); err != nil && ctx.Err() == nil {
			log.Warn("contribution worker unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-rebuild.C:
			reconcile()
		case <-jobs.C:
		}
	}
}
