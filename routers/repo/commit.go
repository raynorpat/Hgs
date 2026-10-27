package repo

import (
	"errors"
	"net/url"
	"strconv"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/ui"
)

const (
	COMMITS base.TplName = "repo/commits"
	DIFF    base.TplName = "repo/diff"
)

func Commits(ctx *middleware.Context) {
	ui.BuildShell(ctx, ui.Repository, "changesets")
	q := hg.HistoryQuery{BeforeRevision: -1}
	values, err := url.ParseQuery(ctx.Req.URL.RawQuery)
	if err != nil {
		ctx.Error(400)
		return
	}
	if tokens, has := values["cursor"]; has {
		if len(tokens) != 1 {
			ctx.Error(400)
			return
		}
		q, err = decodeHistoryCursor(tokens[0])
		if err != nil {
			ctx.Error(400, "Invalid or expired history cursor")
			return
		}
		// The cursor owns path identity on later pages; conflicting URL paths fail.
		p, e := middleware.ParseHgPath(middleware.HgRequestURL(ctx.Req.Request), ctx.Repo.RepoLink+"/commits")
		if e != nil || p != "" {
			ctx.Error(400)
			return
		}
	} else {
		q.Path, err = middleware.ParseHgPath(middleware.HgRequestURL(ctx.Req.Request), ctx.Repo.RepoLink+"/commits")
		if err != nil {
			ctx.Error(400, err.Error())
			return
		}
	}
	page, err := ctx.Repo.HgRunner.History(ctx.Repo.HgContext, ctx.Repo.HgPath, q)
	if errors.Is(err, hg.ErrEmptyRepository) {
		ctx.Repo.FinishRead()
		ctx.Data["Commits"] = []hg.Changeset{}
		ctx.HTML(200, COMMITS)
		return
	}
	if err != nil {
		status := 404
		if errors.Is(err, hg.ErrInvalidCursor) {
			status = 400
		}
		ctx.Error(status, "History unavailable")
		return
	}
	ctx.Data["Commits"] = page.Entries
	ctx.Data["IsRepoToolbarCommits"] = true
	ctx.Data["HistoryPath"] = displayPath(q.Path)
	if page.HasMore {
		q.Snapshot = page.Snapshot
		q.SnapshotRevision = page.SnapshotRevision
		q.BeforeRevision = page.NextBefore
		ctx.Data["NextHistoryURL"] = historyURL(ctx.Repo.RepoLink, q)
	}
	ctx.Repo.FinishRead()
	ctx.HTML(200, COMMITS)
}
func Diff(ctx *middleware.Context) {
	ui.BuildShell(ctx, ui.Repository, "changesets")
	parent := 0
	q, err := url.ParseQuery(ctx.Req.URL.RawQuery)
	if err != nil {
		ctx.Error(400)
		return
	}
	if values, has := q["parent"]; has {
		if len(values) != 1 || (values[0] != "0" && values[0] != "1") {
			ctx.Error(400)
			return
		}
		parent, _ = strconv.Atoi(values[0])
	}
	if parent >= len(ctx.Repo.Changeset.Parents) && !(parent == 0 && len(ctx.Repo.Changeset.Parents) == 0) {
		ctx.Error(400)
		return
	}
	patch, err := ctx.Repo.HgRunner.Diff(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, parent)
	if err != nil {
		ctx.Handle(404, "Diff", err)
		return
	}
	ctx.Data["Patch"] = string(patch.Text)
	ctx.Data["DiffTruncated"] = patch.Truncated
	ctx.Data["DiffBinary"] = patch.Binary
	ctx.Data["Parents"] = ctx.Repo.Changeset.Parents
	ctx.Data["SelectedParent"] = parent
	ctx.Data["IsRepoToolbarCommits"] = true
	ctx.Data["SourceURL"] = middleware.HgPathURL(ctx.Repo.RepoLink, "src", ctx.Repo.Node, "")
	ctx.Repo.FinishRead()
	ctx.HTML(200, DIFF)
}
