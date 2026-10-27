package collaboration

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

// Task 11 must replace this explicit refusal with current validated assignments.
var ErrReviewersUnsupported = errors.New("reviewer assignments are not available")

type NewPull struct {
	SourceRepoID, TargetRepoID int64
	Source, Target             hg.ReviewSelection
	Title, Body                string
	ReviewerIDs                []int64
}
type PullQuery = models.PullQuery
type PullPage = models.PullPage

func (s *Service) CreatePull(ctx context.Context, actor int64, input NewPull) (models.PullRequest, error) {
	if len(input.ReviewerIDs) > 0 {
		return models.PullRequest{}, ErrReviewersUnsupported
	}
	if s == nil || s.Provider == nil {
		return models.PullRequest{}, ErrUnavailable
	}
	host := s.Provider.Service()
	if host == nil {
		return models.PullRequest{}, ErrUnavailable
	}
	src, _ := json.Marshal(input.Source)
	dst, _ := json.Marshal(input.Target)
	p := models.PullRequest{SourceRepoID: input.SourceRepoID, TargetRepoID: input.TargetRepoID, Title: input.Title, Body: input.Body, SourceSelection: string(src), TargetSelection: string(dst)}
	if err := models.ValidatePullCreation(actor, p); err != nil {
		return models.PullRequest{}, err
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	release, err := host.RegisterBrowserRead(actor, p.SourceRepoID, cancel)
	if err != nil {
		return models.PullRequest{}, err
	}
	defer release()
	if p.TargetRepoID != p.SourceRepoID {
		release, err := host.RegisterBrowserRead(actor, p.TargetRepoID, cancel)
		if err != nil {
			return models.PullRequest{}, err
		}
		defer release()
	}
	if err := resolvePullEndpoints(readCtx, host.Runner(), host.RepositoryRoot(), p); err != nil {
		return models.PullRequest{}, err
	}
	// No commands run in this transaction; managed read ownership remains until
	// persistence completes, so shutdown/lifecycle cannot outlive the operation.
	return models.CreatePullRecord(readCtx, actor, p)
}
func resolvePullEndpoints(ctx context.Context, runner *hg.Runner, root string, p models.PullRequest) error {
	source, target, err := models.PullSelections(p)
	if err != nil {
		return err
	}
	sourcePath, err := models.RepositoryStoragePath(root, p.SourceRepoID)
	if err != nil {
		return err
	}
	targetPath, err := models.RepositoryStoragePath(root, p.TargetRepoID)
	if err != nil {
		return err
	}
	src, err := runner.ResolveReview(ctx, sourcePath, source)
	if err != nil {
		return err
	}
	dst, err := runner.ResolveReview(ctx, targetPath, target)
	if err != nil {
		return err
	}
	if src.Node == dst.Node {
		return hg.ErrNoChanges
	}
	return ctx.Err()
}
func (s *Service) GetPull(ctx context.Context, actor, target, number int64) (models.PullRequest, error) {
	return models.GetPullRecord(ctx, actor, target, number)
}
func (s *Service) ListPulls(ctx context.Context, actor int64, q PullQuery) (PullPage, error) {
	return models.ListPullRecords(ctx, actor, q)
}
func (s *Service) EditPull(ctx context.Context, actor, id int64, title, body string) error {
	return models.EditPullRecord(ctx, actor, id, title, body)
}
func (s *Service) SetPullState(ctx context.Context, actor, id int64, state models.PullState) error {
	if state != models.PullOpen && state != models.PullDeclined {
		return models.ErrPullState
	}
	if state == models.PullDeclined {
		return models.SetPullRecordState(ctx, actor, id, state)
	}
	p, err := models.GetPullRecordByID(ctx, actor, id)
	if err != nil {
		return err
	}
	if p.State == models.PullMerged {
		return models.ErrPullState
	}
	if err := models.ValidateRepositoryCredential(actor, 0, 0); err != nil {
		return err
	}
	source, err := models.RepositoryAccessFor(actor, p.SourceRepoID)
	if err != nil {
		return err
	}
	target, err := models.RepositoryAccessFor(actor, p.TargetRepoID)
	if err != nil {
		return err
	}
	if !source.Read || !target.Read || (!target.Write && (actor != p.AuthorID || !source.Write)) {
		return models.ErrInvalidCredential
	}
	if s == nil || s.Provider == nil {
		return ErrUnavailable
	}
	host := s.Provider.Service()
	if host == nil {
		return ErrUnavailable
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	release, err := host.RegisterBrowserRead(actor, p.SourceRepoID, cancel)
	if err != nil {
		return err
	}
	defer release()
	if p.SourceRepoID != p.TargetRepoID {
		release, err := host.RegisterBrowserRead(actor, p.TargetRepoID, cancel)
		if err != nil {
			return err
		}
		defer release()
	}
	if err := resolvePullEndpoints(readCtx, host.Runner(), host.RepositoryRoot(), p); err != nil {
		return err
	}
	return models.SetPullRecordState(readCtx, actor, id, state)
}
