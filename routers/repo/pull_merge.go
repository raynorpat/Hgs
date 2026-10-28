package repo

import (
	"context"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/middleware"
	"mime"
	"path"
	"strings"
)

func pullOperationPresentation(result collaboration.MergeResult) (string, bool) {
	switch result.State {
	case "pending", "recovering", "merged":
		return result.State, true
	case "failed":
		switch result.Code {
		case "conflict":
			return "conflicted", true
		case "stale":
			return "stale", true
		case "publication-failed", "unsafe-checkout", "ambiguous-head", "unrelated-history":
			return "failed", true
		}
		return "failed", false
	}
	return "unavailable", true
}

func PullMerge(ctx *middleware.Context, s *collaboration.Service, f PullForm) {
	if !pullFormSafe(ctx) {
		return
	}
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	if !f.Confirm {
		renderPull(ctx, s, p, &f, 422, ctx.Tr("pulls.confirm_required"))
		return
	}
	result, e := s.StartMerge(ctx.Req.Request.Context(), pullActor(ctx), collaboration.MergeRequest{PullID: p.ID, ComparisonID: f.ComparisonID, Source: hg.NodeID(f.SourceNode), Target: hg.NodeID(f.TargetNode), IdempotencyKey: f.MergeKey})
	if result.OperationID != "" {
		ctx.Redirect(pullURL(ctx, p)+"/operations/"+result.OperationID, 303)
		return
	}
	if e != nil {
		message := ctx.Tr("pulls.merge_not_started")
		if pullErrorStatus(e) >= 500 {
			message = ctx.Tr("pulls.unknown")
		}
		renderPull(ctx, s, p, &f, pullErrorStatus(e), message)
		return
	}
	pullError(ctx, collaboration.ErrUnavailable)
}
func PullOperation(ctx *middleware.Context, s *collaboration.Service) {
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	result, e := s.MergeStatus(ctx.Req.Request.Context(), pullActor(ctx), p.ID, ctx.Params(":operation"))
	if e != nil {
		pullError(ctx, e)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	if strings.Contains(ctx.Req.Header.Get("Accept"), "application/json") {
		ctx.JSON(200, struct{ State, Code, Node, URL string }{result.State, result.Code, string(result.Node), pullURL(ctx, p)})
		return
	}
	renderPull(ctx, s, p, nil, 200, "")
}
func PullDownload(ctx *middleware.Context, s *collaboration.Service) {
	p, ok := loadPull(ctx, s)
	if !ok {
		return
	}
	filename := ctx.Params("*")
	if filename == "" {
		filename = ctx.Query("path")
	}
	started := false
	start := func() {
		started = true
		ctx.Resp.Header().Set("Content-Type", "application/octet-stream")
		ctx.Resp.Header().Set("X-Content-Type-Options", "nosniff")
		ctx.Resp.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": displayPath(path.Base(filename))}))
		ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	}
	e := s.ReadComparisonFileWithLifecycle(ctx.Req.Request.Context(), pullActor(ctx), p.ID, pullID(ctx.Params(":comparison")), ctx.Query("side"), filename, writerFunc(func(b []byte) (int, error) {
		if !started {
			start()
		}
		return ctx.Resp.Write(b)
	}), func(child context.Context) func() {
		return hgbridge.InterruptResponseOnCancel(child, middleware.BrowserResponseController(ctx.Req.Request))
	})
	if e == nil && !started {
		start()
	}
	if e != nil {
		if ctx.Written() {
			middleware.AbortBrowserResponse(ctx.Req.Request)
		} else {
			ctx.Error(404)
		}
	}
}
