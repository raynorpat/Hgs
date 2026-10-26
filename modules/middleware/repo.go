// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package middleware

import (
	"net/url"
	"strings"

	"github.com/Unknwon/macaron"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/setting"
)

func ApiRepoAssignment() macaron.Handler {
	return func(ctx *Context) {
		userName := ctx.Params(":username")
		repoName := ctx.Params(":reponame")

		var (
			u   *models.User
			err error
		)

		if ctx.IsSigned {
			ctx.Repo.IsTrueOwner = ctx.User.LowerName == strings.ToLower(userName)
		}

		if !ctx.Repo.IsTrueOwner {
			u, err = models.GetUserByName(userName)
			if err != nil {
				if err == models.ErrUserNotExist {
					ctx.Error(404)
				} else {
					ctx.JSON(500, &base.ApiJsonErr{"GetUserByName: " + err.Error(), base.DOC_URL})
				}
				return
			}
		} else {
			u = ctx.User
		}
		ctx.Repo.Owner = u

		// Organization owner team members are true owners as well.
		if ctx.IsSigned && ctx.Repo.Owner.IsOrganization() && ctx.Repo.Owner.IsOwnedBy(ctx.User.Id) {
			ctx.Repo.IsTrueOwner = true
		}

		// Get repository.
		repo, err := models.GetRepositoryByName(u.Id, repoName)
		if err != nil {
			if err == models.ErrRepoNotExist {
				ctx.Error(404)
				return
			}
			ctx.JSON(500, &base.ApiJsonErr{"GetRepositoryByName: " + err.Error(), base.DOC_URL})
			return
		} else if err = repo.GetOwner(); err != nil {
			ctx.JSON(500, &base.ApiJsonErr{"GetOwner: " + err.Error(), base.DOC_URL})
			return
		}

		// Collaborators who have write access can be seen as owners.
		if ctx.IsSigned {
			ctx.Repo.IsOwner, err = models.HasAccess(ctx.User.Id, repo.Id, models.WRITABLE)
			if err != nil {
				ctx.JSON(500, &base.ApiJsonErr{"HasAccess: " + err.Error(), base.DOC_URL})
				return
			}
		}

		// Check if the mirror repository owner(mirror repository doesn't have access).
		if ctx.IsSigned && !ctx.Repo.IsOwner {
			if repo.OwnerId == ctx.User.Id {
				ctx.Repo.IsOwner = true
			}
			// Check if current user has admin permission to repository.
			if u.IsOrganization() {
				auth, err := models.GetHighestAuthorize(u.Id, ctx.User.Id, repo.Id, 0)
				if err != nil {
					ctx.JSON(500, &base.ApiJsonErr{"GetHighestAuthorize: " + err.Error(), base.DOC_URL})
					return
				}
				if auth == models.ORG_ADMIN {
					ctx.Repo.IsOwner = true
					ctx.Repo.IsAdmin = true
				}
			}
		}

		// Check access.
		if repo.IsPrivate && !ctx.Repo.IsOwner {
			if ctx.User == nil {
				ctx.Error(404)
				return
			}

			hasAccess, err := models.HasAccess(ctx.User.Id, repo.Id, models.READABLE)
			if err != nil {
				ctx.JSON(500, &base.ApiJsonErr{"HasAccess: " + err.Error(), base.DOC_URL})
				return
			} else if !hasAccess {
				ctx.Error(404)
				return
			}
		}
		ctx.Repo.HasAccess = true

		ctx.Repo.Repository = repo
	}
}

// RepoAssignment resolves metadata and authority without opening repository storage.
func RepoAssignment(redirect bool, args ...bool) macaron.Handler {
	return func(ctx *Context, provider hosting.Provider) {
		owner, err := models.GetUserByName(ctx.Params(":username"))
		if err != nil {
			ctx.Handle(404, "Repository owner", err)
			return
		}
		repo, err := models.GetRepositoryByName(owner.Id, ctx.Params(":reponame"))
		if err != nil {
			ctx.Handle(404, "Repository", err)
			return
		}
		uid := int64(0)
		if ctx.User != nil {
			uid = ctx.User.Id
		}
		access, err := models.RepositoryAccessFor(uid, repo.Id)
		if err != nil {
			ctx.Handle(500, "Repository access", err)
			return
		}
		if !access.Read {
			ctx.Error(404)
			return
		}
		repo.Owner = owner
		ctx.Repo.Owner = owner
		ctx.Repo.Repository = repo
		ctx.Repo.HasAccess = true
		ctx.Repo.IsOwner = access.Write
		ctx.Repo.IsTrueOwner = access.Admin
		if ctx.IsSigned && owner.IsOrganization() {
			auth, err := models.GetHighestAuthorize(owner.Id, uid, repo.Id, 0)
			if err != nil {
				ctx.Handle(500, "Repository authority", err)
				return
			}
			ctx.Repo.IsAdmin = auth == models.ORG_ADMIN
		}
		repo.NumOpenIssues = repo.NumIssues - repo.NumClosedIssues
		repo.NumOpenMilestones = repo.NumMilestones - repo.NumClosedMilestones
		ctx.Repo.RepoLink, err = repo.RepoLink()
		if err != nil {
			ctx.Handle(500, "RepoLink", err)
			return
		}
		if s := provider.Service(); s != nil {
			ctx.Repo.CloneLink = s.CloneLinks(owner.Name, repo.Name)
		}
		ctx.Data["HasAccess"] = true
		ctx.Data["RepoLink"] = ctx.Repo.RepoLink
		ctx.Data["Repository"] = repo
		ctx.Data["Owner"] = owner
		ctx.Data["Title"] = owner.Name + "/" + repo.Name
		ctx.Data["IsRepositoryOwner"] = ctx.Repo.IsOwner
		ctx.Data["IsRepositoryTrueOwner"] = ctx.Repo.IsTrueOwner || ctx.Repo.IsAdmin
		ctx.Data["DisableSSH"] = setting.DisableSSH
		ctx.Data["CloneLink"] = ctx.Repo.CloneLink
		if ctx.IsSigned {
			ctx.Data["IsWatchingRepo"] = models.IsWatching(uid, repo.Id)
			ctx.Data["IsStaringRepo"] = models.IsStaring(uid, repo.Id)
		}
	}
}

func RequireTrueOwner() macaron.Handler {
	return func(ctx *Context) {
		if !ctx.Repo.IsTrueOwner && !ctx.Repo.IsAdmin {
			if !ctx.IsSigned {
				ctx.SetCookie("redirect_to", "/"+url.QueryEscape(setting.AppSubUrl+ctx.Req.RequestURI), 0, setting.AppSubUrl)
				ctx.Redirect(setting.AppSubUrl + "/user/login")
				return
			}
			ctx.Handle(404, ctx.Req.RequestURI, nil)
			return
		}
	}
}

// GitHookService checks if repository Git hooks service has been enabled.
func GitHookService() macaron.Handler {
	return func(ctx *Context) {
		if !ctx.User.AllowGitHook && !ctx.User.IsAdmin {
			ctx.Handle(404, "GitHookService", nil)
			return
		}
	}
}
