package repo

import (
	"fmt"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/middleware"
)

func PullReviewers(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	pullMutation(ctx, s, f, func(p models.PullRequest) error {
		ids, e := pullReviewerIDs(f.Reviewers)
		if e != nil {
			return e
		}
		return s.AssignReviewers(ctx.Req.Request.Context(), pullActor(ctx), p.ID, ids)
	})
}
func PullReview(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	pullMutation(ctx, s, f, func(p models.PullRequest) error {
		c, state, e := s.InspectPullComparison(ctx.Req.Request.Context(), pullActor(ctx), p.ID)
		if e != nil {
			return e
		}
		if state != "fresh" || c.ID != f.ComparisonID {
			return models.ErrPullConflict
		}
		return s.ReviewPull(ctx.Req.Request.Context(), pullActor(ctx), p.ID, f.ComparisonID, models.ReviewState(f.State))
	})
}
func PullDismiss(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	pullMutation(ctx, s, f, func(p models.PullRequest) error {
		return s.DismissReview(ctx.Req.Request.Context(), pullActor(ctx), p.ID, f.ReviewerID, f.Reason)
	})
}
func PullResolve(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	pullMutation(ctx, s, f, func(p models.PullRequest) error {
		return s.ResolveThread(ctx.Req.Request.Context(), pullActor(ctx), p.ID, pullID(ctx.Params(":thread")), f.Resolved)
	})
}
func PullComment(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	if !pullFormSafe(ctx) {
		return
	}
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	var anchor *collaboration.InlineAnchor
	if f.ComparisonID != 0 || f.Path != "" || f.Side != "" || f.Line != 0 {
		anchor = &collaboration.InlineAnchor{ComparisonID: f.ComparisonID, Path: f.Path, Side: f.Side, Line: f.Line}
	}
	id, e := s.CommentPull(ctx.Req.Request.Context(), pullActor(ctx), p.ID, f.Content, anchor)
	if e != nil {
		ctx.Data["PullCommentSubmitted"] = true
		renderPull(ctx, s, p, &f, pullErrorStatus(e), pullFailureMessage(ctx, e))
		return
	}
	ctx.Redirect(fmt.Sprintf("%s#comment-%d", pullURL(ctx, p), id), 303)
}
