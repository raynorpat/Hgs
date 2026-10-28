// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

const (
	SETTINGS_OPTIONS base.TplName = "repo/settings/options"
	COLLABORATION    base.TplName = "repo/settings/collaboration"
)

func Settings(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsOptions"] = true
	ui.BuildShell(ctx, ui.Repository, "settings")
	ctx.Data["UIRepoName"] = ctx.Repo.Repository.Name
	ctx.Data["UIRepoDescription"] = ctx.Repo.Repository.Description
	ctx.Data["UIRepoWebsite"] = ctx.Repo.Repository.Website
	ctx.Data["UIRepoPrivate"] = ctx.Repo.Repository.IsPrivate
	ctx.HTML(200, SETTINGS_OPTIONS)
}

func SettingsPost(ctx *middleware.Context, form auth.RepoSettingForm, provider hosting.Provider) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsOptions"] = true
	ui.BuildShell(ctx, ui.Repository, "settings")
	ctx.Data["UIRepoName"] = ctx.Repo.Repository.Name
	ctx.Data["UIRepoDescription"] = ctx.Repo.Repository.Description
	ctx.Data["UIRepoWebsite"] = ctx.Repo.Repository.Website
	ctx.Data["UIRepoPrivate"] = ctx.Repo.Repository.IsPrivate

	switch ctx.Query("action") {
	case "update":
		ctx.Data["UIRepoName"], ctx.Data["UIRepoDescription"] = form.RepoName, form.Description
		ctx.Data["UIRepoWebsite"], ctx.Data["UIRepoPrivate"] = form.Website, form.Private
		if ctx.HasError() {
			ctx.HTML(200, SETTINGS_OPTIONS)
			return
		}

		newRepoName := form.RepoName
		// Check if repository name has been changed.
		if ctx.Repo.Repository.Name != newRepoName {
			isExist, err := models.IsRepositoryExist(ctx.Repo.Owner, newRepoName)
			if err != nil {
				ctx.Handle(500, "IsRepositoryExist", err)
				return
			} else if isExist {
				ctx.Data["Err_RepoName"] = true
				ctx.RenderWithErr(ctx.Tr("form.repo_name_been_taken"), SETTINGS_OPTIONS, nil)
				return
			} else if err = models.ChangeRepositoryName(ctx.Repo.Repository.Id, newRepoName); err != nil {
				if renderForkLifecycleError(ctx, err) {
					return
				}
				if err == models.ErrRepoNameIllegal {
					ctx.Data["Err_RepoName"] = true
					ctx.RenderWithErr(ctx.Tr("form.illegal_repo_name"), SETTINGS_OPTIONS, nil)
				} else {
					ctx.Handle(500, "ChangeRepositoryName", err)
				}
				return
			}
			log.Trace("Repository name changed: %s/%s -> %s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name, newRepoName)
			ctx.Repo.Repository.Name = newRepoName
			ctx.Repo.Repository.LowerName = strings.ToLower(newRepoName)
		}

		ctx.Repo.Repository.Description = form.Description
		ctx.Repo.Repository.Website = form.Website
		ctx.Repo.Repository.IsPrivate = form.Private
		if err := models.UpdateRepository(ctx.Repo.Repository); err != nil {
			if renderForkLifecycleError(ctx, err) {
				return
			}
			ctx.Handle(404, "UpdateRepository", err)
			return
		}
		log.Trace("Repository updated: %s/%s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)

		ctx.Flash.Success(ctx.Tr("repo.settings.update_settings_success"))
		ctx.Redirect(fmt.Sprintf("%s/%s/%s/settings", setting.AppSubUrl, ctx.Repo.Owner.Name, ctx.Repo.Repository.Name))
	case "transfer":
		if ctx.Repo.Repository.Name != form.RepoName {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_repo_name"), SETTINGS_OPTIONS, nil)
			return
		}

		newOwner := ctx.Query("new_owner_name")
		isExist, err := models.IsUserExist(newOwner)
		if err != nil {
			ctx.Handle(500, "IsUserExist", err)
			return
		} else if !isExist {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_owner_name"), SETTINGS_OPTIONS, nil)
			return
		} else if !ctx.User.ValidtePassword(ctx.Query("password")) {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_password"), SETTINGS_OPTIONS, nil)
			return
		} else if err = models.TransferOwnership(ctx.User, newOwner, ctx.Repo.Repository); err != nil {
			if renderForkLifecycleError(ctx, err) {
				return
			}
			if err == models.ErrRepoAlreadyExist {
				ctx.RenderWithErr(ctx.Tr("repo.settings.new_owner_has_same_repo"), SETTINGS_OPTIONS, nil)
			} else {
				ctx.Handle(500, "TransferOwnership", err)
			}
			return
		}
		log.Trace("Repository transfered: %s/%s -> %s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name, newOwner)
		ctx.Flash.Success(ctx.Tr("repo.settings.transfer_succeed"))
		ctx.Redirect(setting.AppSubUrl + "/")
	case "delete":
		if ctx.Repo.Repository.Name != form.RepoName {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_repo_name"), SETTINGS_OPTIONS, nil)
			return
		}

		if ctx.Repo.Owner.IsOrganization() {
			if !ctx.Repo.Owner.IsOwnedBy(ctx.User.Id) {
				ctx.Error(404)
				return
			}
			if !ctx.User.ValidtePassword(ctx.Query("password")) {
				ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_password"), SETTINGS_OPTIONS, nil)
				return
			}
		} else {
			if !ctx.Repo.Owner.ValidtePassword(ctx.Query("password")) {
				ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_password"), SETTINGS_OPTIONS, nil)
				return
			}
		}

		service := provider.Service()
		if service == nil {
			ctx.Error(503)
			return
		}
		drainCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := models.DeleteHgRepository(drainCtx, service.RepositoryRoot(), ctx.Repo.Repository.Id, service.DrainRepository, service.RepositoryCleanup); err != nil {
			if renderForkLifecycleError(ctx, err) {
				return
			}
			ctx.Handle(500, "DeleteRepository", err)
			return
		}
		log.Trace("Repository deleted: %s/%s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)
		if ctx.Repo.Owner.IsOrganization() {
			ctx.Redirect(setting.AppSubUrl + "/org/" + ctx.Repo.Owner.Name + "/dashboard")
		} else {
			ctx.Redirect(setting.AppSubUrl + "/")
		}
	}
}

func SettingsCollaboration(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsCollaboration"] = true
	ui.BuildShell(ctx, ui.Repository, "settings")

	repoId := ctx.Repo.Repository.Id

	if ctx.Req.Method == "POST" && ctx.Query("remove") == "" {
		name := strings.ToLower(ctx.Query("collaborator"))
		if len(name) == 0 || ctx.Repo.Owner.LowerName == name {
			ctx.Redirect(setting.AppSubUrl + ctx.Req.URL.Path)
			return
		}

		u, err := models.GetUserByName(name)
		if err != nil {
			if err == models.ErrUserNotExist {
				ctx.Flash.Error(ctx.Tr("form.user_not_exist"))
				ctx.Redirect(setting.AppSubUrl + ctx.Req.URL.Path)
			} else {
				ctx.Handle(500, "GetUserByName", err)
			}
			return
		}

		has, err := models.HasAccess(u.Id, repoId, models.WRITABLE)
		if err != nil {
			ctx.Handle(500, "HasAccess", err)
			return
		} else if has {
			ctx.Redirect(setting.AppSubUrl + ctx.Req.URL.Path)
			return
		}

		// Check if user is organization member.
		if ctx.Repo.Owner.IsOrganization() && ctx.Repo.Owner.IsOrgMember(u.Id) {
			ctx.Flash.Info(ctx.Tr("repo.settings.user_is_org_member"))
			ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
			return
		}

		if err = models.AddAccess(&models.Access{UserId: u.Id, RepoId: repoId,
			Mode: models.WRITABLE}); err != nil {
			ctx.Handle(500, "AddAccess", err)
			return
		}

		if setting.Service.EnableNotifyMail {
			if err = mailer.SendCollaboratorMail(ctx.Render, u, ctx.User, ctx.Repo.Repository); err != nil {
				ctx.Handle(500, "SendCollaboratorMail", err)
				return
			}
		}

		ctx.Flash.Success(ctx.Tr("repo.settings.add_collaborator_success"))
		ctx.Redirect(setting.AppSubUrl + ctx.Req.URL.Path)
		return
	}

	// Delete collaborator.
	remove := ""
	if ctx.Req.Method == "POST" {
		remove = strings.ToLower(ctx.Query("remove"))
	}
	if len(remove) > 0 && remove != ctx.Repo.Owner.LowerName {
		needDelete := true
		if ctx.User.IsOrganization() {
			// Check if user belongs to a team that has access to this repository.
			auth, err := models.GetHighestAuthorize(ctx.Repo.Owner.Id, ctx.User.Id, ctx.Repo.Repository.Id, 0)
			if err != nil {
				ctx.Handle(500, "GetHighestAuthorize", err)
				return
			}
			if auth > 0 {
				needDelete = false
			}
		}

		if needDelete {
			u, err := models.GetUserByName(remove)
			if err == nil {
				err = models.DeleteAccess(&models.Access{UserId: u.Id, RepoId: repoId})
			}
			if err != nil && err != models.ErrUserNotExist {
				ctx.Handle(500, "DeleteAccess", err)
				return
			}
		}
		ctx.Flash.Success(ctx.Tr("repo.settings.remove_collaborator_success"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		return
	}

	us, err := models.GetCollaborators(repoId)
	if err != nil {
		ctx.Handle(500, "GetCollaborators", err)
		return
	}

	collaborators := make([]*models.User, 0, len(us))
	for _, u := range us {
		// Does not show organization members.
		if ctx.Repo.Owner.IsOrganization() && ctx.Repo.Owner.IsOrgMember(u.Id) {
			continue
		}
		collaborators = append(collaborators, u)
	}
	ctx.Data["Collaborators"] = collaborators
	ctx.HTML(200, COLLABORATION)
}

func renderForkLifecycleError(ctx *middleware.Context, err error) bool {
	key := ""
	switch {
	case errors.Is(err, models.ErrForkDependent):
		key = "repo.fork_dependents"
	case errors.Is(err, models.ErrForkPolicy):
		key = "repo.fork_lifecycle_restricted"
	case errors.Is(err, models.ErrReviewReserved), errors.Is(err, hosting.ErrMutationBusy), errors.Is(err, collaboration.ErrMergeBusy):
		key = "repo.fork_busy"
	default:
		return false
	}
	ctx.RenderWithErr(ctx.Tr(key), SETTINGS_OPTIONS, nil)
	return true
}
