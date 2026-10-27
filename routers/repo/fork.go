package repo

import (
	"errors"
	"strconv"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

const FORK base.TplName = "repo/fork"

func prepareForkForm(ctx *middleware.Context, ownerID int64) bool {
	ui.BuildShell(ctx, ui.Repository, "source")
	ctx.Data["Title"] = ctx.Tr("repo.fork_title")
	ctx.Data["ForkOwnerID"] = ownerID
	if err := ctx.User.GetOrganizations(); err != nil {
		ctx.Handle(500, "Fork owners", err)
		return false
	}
	owners := []*models.User{}
	for _, org := range ctx.User.Orgs {
		if models.ForkOwnerAllowed(ctx.User.Id, org.Id) == nil {
			owners = append(owners, org)
		}
	}
	ctx.Data["Orgs"] = owners
	return true
}
func Fork(ctx *middleware.Context) {
	if !prepareForkForm(ctx, ctx.User.Id) {
		return
	}
	ctx.Data["repo_name"] = ctx.Repo.Repository.Name
	ctx.Data["desc"] = ctx.Repo.Repository.Description
	ctx.HTML(200, FORK)
}
func ForkPost(ctx *middleware.Context, form auth.CreateRepoForm, provider hosting.Provider) {
	if !prepareForkForm(ctx, form.Uid) {
		return
	}
	if ctx.HasError() {
		ctx.HTML(200, FORK)
		return
	}
	if err := models.ForkOwnerAllowed(ctx.User.Id, form.Uid); err != nil {
		ctx.Error(403)
		return
	}
	// This route is metadata-only. Release any upstream browser lease supplied by
	// future composition before acquiring the exclusive source fence.
	if ctx.Repo.FinishRead != nil {
		ctx.Repo.FinishRead()
	}
	created, err := collaboration.New(provider).StartFork(ctx.Req.Context(), ctx.User.Id, collaboration.ForkOptions{SourceID: ctx.Repo.Repository.Id, OwnerID: form.Uid, Name: form.RepoName, Description: form.Description})
	if err == nil {
		ctx.Redirect(setting.AppSubUrl + "/repo/forks/" + strconv.FormatInt(created.Id, 10))
		return
	}
	switch {
	case errors.Is(err, models.ErrRepoAlreadyExist):
		ctx.Data["Err_RepoName"] = true
		ctx.RenderWithErr(ctx.Tr("form.repo_name_been_taken"), FORK, &form)
	case errors.Is(err, models.ErrRepoNameIllegal):
		ctx.Data["Err_RepoName"] = true
		ctx.RenderWithErr(ctx.Tr("form.illegal_repo_name"), FORK, &form)
	case errors.Is(err, collaboration.ErrUnavailable):
		ctx.Error(503)
	case errors.Is(err, models.ErrInvalidCredential):
		ctx.Error(403)
	case errors.Is(err, hosting.ErrMutationBusy), errors.Is(err, models.ErrReviewReserved), errors.Is(err, models.ErrForkPolicy):
		ctx.RenderWithErr(ctx.Tr("repo.fork_busy"), FORK, &form)
	default:
		ctx.Handle(500, "Fork repository", err)
	}
}
func forkProgressState(ctx *middleware.Context) (int64, models.RepositoryState, string, bool) {
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	id, err := strconv.ParseInt(ctx.Params(":id"), 10, 64)
	if err != nil || id <= 0 {
		ctx.Error(404)
		return 0, "", "", false
	}
	state, err := collaboration.ForkStatus(ctx.Req.Context(), ctx.User.Id, id)
	if err != nil {
		ctx.Error(404)
		return 0, "", "", false
	}
	link := ""
	if state == models.RepositoryReady {
		target, err := models.GetRepositoryById(id)
		if err != nil {
			ctx.Error(404)
			return 0, "", "", false
		}
		link, err = target.RepoLink()
		if err != nil {
			ctx.Error(404)
			return 0, "", "", false
		}
	}
	return id, state, link, true
}
func ForkProgress(ctx *middleware.Context) {
	id, state, link, ok := forkProgressState(ctx)
	if !ok {
		return
	}
	ui.BuildShell(ctx, ui.Workspace, "repositories")
	ctx.Data["Title"] = ctx.Tr("repo.fork_title")
	ctx.Data["ForkState"] = state
	ctx.Data["ForkURL"] = link
	ctx.Data["ForkStatusURL"] = setting.AppSubUrl + "/repo/forks/" + strconv.FormatInt(id, 10) + "/status"
	ctx.HTML(200, "repo/fork_progress")
}
func ForkStatus(ctx *middleware.Context) {
	id, state, link, ok := forkProgressState(ctx)
	if !ok {
		return
	}
	ctx.JSON(200, map[string]interface{}{"id": id, "state": state, "url": link})
}
