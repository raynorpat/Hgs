// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package user

import (
	"bytes"
	"github.com/gogits/gogs/modules/setting"
	"strings"

	"github.com/Unknwon/com"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/ui"
)

const (
	DASHBOARD base.TplName = "user/dashboard/dashboard"
	ISSUES    base.TplName = "user/issues"
	STARS     base.TplName = "user/stars"
	PROFILE   base.TplName = "user/profile"
)

func Dashboard(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("dashboard")
	ctx.Data["PageIsDashboard"] = true
	ctx.Data["PageIsNews"] = true
	ui.BuildShell(ctx, ui.Workspace, "overview")

	var ctxUser *models.User
	// Check context type.
	orgName := ctx.Params(":org")
	if len(orgName) > 0 {
		// Organization.
		org, err := models.GetUserByName(orgName)
		if err != nil {
			if err == models.ErrUserNotExist {
				ctx.Handle(404, "GetUserByName", err)
			} else {
				ctx.Handle(500, "GetUserByName", err)
			}
			return
		}
		ctxUser = org
	} else {
		// Normal user.
		ctxUser = ctx.User
		collaborates, err := models.GetCollaborativeRepos(ctxUser.Id)
		if err != nil {
			ctx.Handle(500, "GetCollaborativeRepos", err)
			return
		}
		readable := collaborates[:0]
		for _, r := range collaborates {
			access, err := models.RepositoryAccessFor(ctx.User.Id, r.Id)
			if err == models.ErrRepoNotExist {
				continue
			}
			if err != nil {
				ctx.Handle(500, "Repository access", err)
				return
			}
			if access.Read {
				readable = append(readable, r)
			}
		}
		collaborates = readable
		ctx.Data["CollaborateCount"] = len(collaborates)
		ctx.Data["CollaborativeRepos"] = collaborates
	}
	ctx.Data["ContextUser"] = ctxUser

	if err := ctx.User.GetOrganizations(); err != nil {
		ctx.Handle(500, "GetOrganizations", err)
		return
	}
	ctx.Data["Orgs"] = ctx.User.Orgs

	repos, err := models.GetRepositories(ctxUser.Id, true)
	if err != nil {
		ctx.Handle(500, "GetRepositories", err)
		return
	}
	readable := repos[:0]
	for _, r := range repos {
		access, err := models.RepositoryAccessFor(ctx.User.Id, r.Id)
		if err == models.ErrRepoNotExist {
			continue
		}
		if err != nil {
			ctx.Handle(500, "Repository access", err)
			return
		}
		if access.Read {
			readable = append(readable, r)
		}
	}
	ctx.Data["Repos"] = readable

	// Get feeds.
	actions, err := models.GetFeeds(ctxUser.Id, 0, false)
	if err != nil {
		ctx.Handle(500, "GetFeeds", err)
		return
	}

	// Check access of private repositories.
	feeds := make([]*models.Action, 0, len(actions))
	for _, act := range actions {
		// Historical privacy flags cannot authorize current repository activity.
		access, err := models.RepositoryAccessFor(ctx.User.Id, act.RepoId)
		if err == models.ErrRepoNotExist {
			continue
		}
		if err != nil {
			ctx.Handle(500, "Activity repository access", err)
			return
		}
		if !access.Read {
			continue
		}
		switch act.OpType {
		case models.CREATE_REPO, models.CREATE_ISSUE, models.TRANSFER_REPO, models.COMMENT_ISSUE:
		default:
			continue
		}
		if (act.OpType == models.CREATE_ISSUE || act.OpType == models.COMMENT_ISSUE) && len(act.GetIssueInfos()) < 2 {
			continue
		}

		// FIXME: cache results?
		u, err := models.GetUserByName(act.ActUserName)
		if err != nil {
			if err == models.ErrUserNotExist {
				continue
			}
			ctx.Handle(500, "GetUserByName", err)
			return
		}
		act.ActAvatar = u.AvatarLink()
		feeds = append(feeds, act)
	}
	ctx.Data["Feeds"] = feeds
	ctx.HTML(200, DASHBOARD)
}

func ShowSSHKeys(ctx *middleware.Context, uid int64) {
	keys, err := models.ListPublicKeys(uid)
	if err != nil {
		ctx.Handle(500, "ListPublicKeys", err)
		return
	}

	var buf bytes.Buffer
	for i := range keys {
		buf.WriteString(keys[i].OmitEmail())
	}
	ctx.RenderData(200, buf.Bytes())
}

func Profile(ctx *middleware.Context) {
	ctx.Data["Title"] = "Profile"
	ctx.Data["PageIsUserProfile"] = true

	uname := ctx.Params(":username")
	// Special handle for FireFox requests favicon.ico.
	if uname == "favicon.ico" {
		ctx.Redirect(setting.AppSubUrl + "/img/favicon.png")
		return
	}

	isShowKeys := false
	if strings.HasSuffix(uname, ".keys") {
		isShowKeys = true
		uname = strings.TrimSuffix(uname, ".keys")
	}

	u, err := models.GetUserByName(uname)
	if err != nil {
		if err == models.ErrUserNotExist {
			ctx.Handle(404, "GetUserByName", err)
		} else {
			ctx.Handle(500, "GetUserByName", err)
		}
		return
	}

	// Show SSH keys.
	if isShowKeys {
		ShowSSHKeys(ctx, u.Id)
		return
	}

	if u.IsOrganization() {
		ctx.Redirect(setting.AppSubUrl + "/org/" + u.Name)
		return
	}

	// For security reason, hide e-mail address for anonymous visitors.
	if !ctx.IsSigned {
		u.Email = ""
	}
	ctx.Data["Owner"] = u

	tab := ctx.Query("tab")
	ctx.Data["TabName"] = tab
	switch tab {
	case "activity":
		actions, err := models.GetFeeds(u.Id, 0, false)
		if err != nil {
			ctx.Handle(500, "GetFeeds", err)
			return
		}
		feeds := make([]*models.Action, 0, len(actions))
		for _, act := range actions {
			if act.IsPrivate {
				if !ctx.IsSigned {
					continue
				}
				if has, _ := models.HasAccess(ctx.User.Id, act.RepoId,
					models.READABLE); !has {
					continue
				}
			}
			// FIXME: cache results?
			u, err := models.GetUserByName(act.ActUserName)
			if err != nil {
				if err == models.ErrUserNotExist {
					continue
				}
				ctx.Handle(500, "GetUserByName", err)
				return
			}
			act.ActAvatar = u.AvatarLink()
			feeds = append(feeds, act)
		}
		ctx.Data["Feeds"] = feeds
	default:
		ctx.Data["Repos"], err = models.GetRepositories(u.Id, ctx.IsSigned && ctx.User.Id == u.Id)
		if err != nil {
			ctx.Handle(500, "GetRepositories", err)
			return
		}
	}

	ctx.HTML(200, PROFILE)
}

func Email2User(ctx *middleware.Context) {
	u, err := models.GetUserByEmail(ctx.Query("email"))
	if err != nil {
		if err == models.ErrUserNotExist {
			ctx.Handle(404, "user.Email2User(GetUserByEmail)", err)
		} else {
			ctx.Handle(500, "user.Email2User(GetUserByEmail)", err)
		}
		return
	}
	ctx.Redirect(setting.AppSubUrl + "/user/" + u.Name)
}

func Issues(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("ui.issues")
	ui.BuildShell(ctx, ui.Workspace, "issues")
	view := ctx.Query("type")
	if view != "assigned" && view != "created_by" {
		view = "all"
	}
	rid := com.StrTo(ctx.Query("repoid")).MustInt64()
	page := com.StrTo(ctx.Query("page")).MustInt()
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		page = 1000000
	}
	result, err := models.ListUIIssues(ctx.Req.Request.Context(), models.UIIssueQuery{ActorID: ctx.User.Id, RepositoryID: rid, Global: true, Closed: ctx.Query("state") == "closed", View: view, Page: page})
	if err == models.ErrRepoNotExist {
		ctx.Error(404)
		return
	}
	if err != nil {
		ctx.Handle(500, "ListUIIssues", err)
		return
	}
	ctx.Data["RepoId"] = rid
	ctx.Data["Repos"] = result.Repositories
	ctx.Data["Issues"] = result.Issues
	ctx.Data["ViewType"] = view
	ctx.Data["IssueStats"] = &result.Stats
	ctx.Data["IsShowClosed"] = ctx.Query("state") == "closed"
	ctx.Data["State"] = ctx.Query("state")
	ui.IssuePagination(ctx, page, result.Total)
	ui.IssueEmpty(ctx, result.HasIssues, "")
	ctx.HTML(200, ISSUES)
}
