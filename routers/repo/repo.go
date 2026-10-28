// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"fmt"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

const (
	CREATE base.TplName = "repo/create"
)

func checkContextUser(ctx *middleware.Context, uid int64) (*models.User, error) {
	ctxUser := ctx.User
	if uid > 0 {
		org, err := models.GetUserById(uid)
		if err != models.ErrUserNotExist {
			if err != nil {
				return nil, fmt.Errorf("GetUserById: %v", err)
			}
			ctxUser = org
		}
	}
	return ctxUser, nil
}

func Create(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("new_repo")
	ui.BuildShell(ctx, ui.Workspace, "repositories")

	ctxUser, err := checkContextUser(ctx, ctx.QueryInt64("org"))
	if err != nil {
		ctx.Handle(500, "checkContextUser", err)
		return
	}
	ctx.Data["ContextUser"] = ctxUser

	if err := ctx.User.GetOrganizations(); err != nil {
		ctx.Handle(500, "GetOrganizations", err)
		return
	}
	ctx.Data["Orgs"] = ctx.User.Orgs

	ctx.HTML(200, CREATE)
}

func CreatePost(ctx *middleware.Context, form auth.CreateRepoForm, provider hosting.Provider) {
	ctx.Data["Title"] = ctx.Tr("new_repo")
	ui.BuildShell(ctx, ui.Workspace, "repositories")

	ctxUser := ctx.User
	// Not equal means current user is an organization.
	if form.Uid != ctx.User.Id {
		var err error
		ctxUser, err = checkContextUser(ctx, form.Uid)
		if err != nil {
			ctx.Handle(500, "checkContextUser", err)
			return
		}
	}
	ctx.Data["ContextUser"] = ctxUser

	if err := ctx.User.GetOrganizations(); err != nil {
		ctx.Handle(500, "GetOrganizations", err)
		return
	}
	ctx.Data["Orgs"] = ctx.User.Orgs

	if ctx.HasError() {
		ctx.HTML(200, CREATE)
		return
	}

	if ctxUser.Id != ctx.User.Id && !ctxUser.IsOrganization() {
		ctx.Error(403)
		return
	}
	if ctxUser.IsOrganization() {
		// Check ownership of organization.
		if !ctxUser.IsOwnedBy(ctx.User.Id) {
			ctx.Error(403)
			return
		}
	}

	service := provider.Service()
	if service == nil {
		ctx.Error(503)
		return
	}
	repo, err := models.CreateHgRepository(ctx.Req.Context(), service.Runner(), service.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: ctxUser.Id, Name: form.RepoName, Description: form.Description, Private: form.Private})

	if err == nil {
		log.Trace("Repository created: %s/%s", ctxUser.Name, repo.Name)
		ctx.Redirect(setting.AppSubUrl + "/" + ctxUser.Name + "/" + repo.Name)
		return
	} else if err == models.ErrRepoAlreadyExist {
		ctx.Data["Err_RepoName"] = true
		ctx.RenderWithErr(ctx.Tr("form.repo_name_been_taken"), CREATE, &form)
		return
	} else if err == models.ErrRepoNameIllegal {
		ctx.Data["Err_RepoName"] = true
		ctx.RenderWithErr(ctx.Tr("form.illegal_repo_name"), CREATE, &form)
		return
	}

	ctx.Handle(500, "CreatePost", err)
}

func Action(ctx *middleware.Context) {
	if ctx.Req.Method != "POST" {
		ctx.Error(405)
		return
	}
	var err error
	switch ctx.Params(":action") {
	case "watch":
		err = models.WatchRepo(ctx.User.Id, ctx.Repo.Repository.Id, true)
	case "unwatch":
		err = models.WatchRepo(ctx.User.Id, ctx.Repo.Repository.Id, false)
	case "star":
		err = models.StarRepo(ctx.User.Id, ctx.Repo.Repository.Id, true)
	case "unstar":
		err = models.StarRepo(ctx.User.Id, ctx.Repo.Repository.Id, false)
	case "desc":
		if !ctx.Repo.IsOwner {
			ctx.Error(404)
			return
		}

		ctx.Repo.Repository.Description = ctx.Query("desc")
		ctx.Repo.Repository.Website = ctx.Query("site")
		err = models.UpdateRepository(ctx.Repo.Repository)
	default:
		ctx.Error(404)
		return
	}

	if err != nil {
		log.Error(4, "Action(%s): %v", ctx.Params(":action"), err)
		ctx.Flash.Error(ctx.Tr("ui.action_failed"))
		ctx.Redirect(ctx.Repo.RepoLink)
		return
	}
	if ctx.Req.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		ctx.JSON(200, map[string]interface{}{
			"ok": true,
		})
		return
	}
	ctx.Redirect(ctx.Repo.RepoLink)
}
