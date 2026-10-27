package collaboration

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
)

var ErrUnavailable = errors.New("repository hosting unavailable")

type ForkOptions struct {
	SourceID, OwnerID int64
	Name, Description string
}

func (s *Service) Fork(ctx context.Context, actorID int64, opts ForkOptions) (*models.Repository, error) {
	if s == nil || s.Provider == nil {
		return nil, ErrUnavailable
	}
	host := s.Provider.Service()
	if host == nil {
		return nil, ErrUnavailable
	}
	return host.ForkRepository(ctx, actorID, opts.SourceID, models.CreateHgRepositoryOptions{OwnerID: opts.OwnerID, Name: opts.Name, Description: opts.Description})
}
func ForkStatus(ctx context.Context, actorID, repoID int64) (models.RepositoryState, error) {
	return models.ForkRepositoryStatus(ctx, actorID, repoID)
}

// StartFork is the form's managed asynchronous entry point; it shares the same
// durable allocation, source fence and final authority gate as Fork.
func (s *Service) StartFork(ctx context.Context, actorID int64, opts ForkOptions) (*models.Repository, error) {
	if s == nil || s.Provider == nil {
		return nil, ErrUnavailable
	}
	host := s.Provider.Service()
	if host == nil {
		return nil, ErrUnavailable
	}
	return host.StartForkRepository(ctx, actorID, opts.SourceID, models.CreateHgRepositoryOptions{OwnerID: opts.OwnerID, Name: opts.Name, Description: opts.Description})
}
