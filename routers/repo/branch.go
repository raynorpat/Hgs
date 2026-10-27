package repo

import (
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/ui"
)

func Branches(ctx *middleware.Context) {
	ui.BuildShell(ctx, ui.Repository, "branches")
	refs, err := ctx.Repo.HgRunner.References(ctx.Repo.HgContext, ctx.Repo.HgPath)
	if err != nil {
		ctx.Handle(500, "References", err)
		return
	}
	ctx.Data["References"] = referenceRows(ctx.Repo.RepoLink, refs)
	ctx.Data["IsRepoToolbarBranches"] = true
	ctx.Repo.FinishRead()
	ctx.HTML(200, "repo/branch")
}
