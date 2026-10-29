// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package org

import (
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/contributions"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

const (
	HOME   base.TplName = "org/home"
	CREATE base.TplName = "org/create"
)

func Home(ctx *middleware.Context, provider hosting.Provider, index contributions.Provider) {
	org := ctx.Org.Organization
	ctx.Data["Title"] = org.FullName

	repos, err := models.GetRepositories(org.Id, true)
	if err != nil {
		ctx.Handle(500, "GetRepositories", err)
		return
	}
	actorID := int64(0)
	if ctx.IsSigned {
		actorID = ctx.User.Id
	}
	visible, err := models.FilterReadableRepositories(actorID, repos)
	if err != nil {
		ctx.Handle(500, "FilterReadableRepositories", err)
		return
	}
	ctx.Data["Repos"] = visible
	ctx.Data["IsOrganizationMember"] = ctx.Org.IsMember

	if err = org.GetMembers(); err != nil {
		ctx.Handle(500, "GetMembers", err)
		return
	}
	people := make([]*models.User, 0, len(org.Members))
	for _, member := range org.Members {
		if ctx.Org.IsMember || member.IsPublicMember(org.Id) {
			people = append(people, member)
		}
	}
	ctx.Data["ProfilePeople"] = people
	ctx.Data["Owner"] = org
	status, err := ui.PrepareProfile(ctx, org, index, visible)
	if err != nil {
		ctx.Handle(500, "PrepareProfile", err)
		return
	}

	if err = org.GetTeams(); err != nil {
		ctx.Handle(500, "GetTeams", err)
		return
	}
	for _, team := range org.Teams {
		if err := prepareTeamRepositories(ctx, team); err != nil {
			ctx.Handle(500, "TeamRepositories", err)
			return
		}
	}
	ctx.Data["Teams"] = org.Teams

	readme, readmeErr := middleware.LoadProfileReadme(ctx, provider, org)
	if readmeErr != nil {
		log.Error(4, "Organization profile README: %v", readmeErr)
	}
	ctx.Data["ProfileReadme"] = readme
	ctx.HTML(status, HOME)
}

func Create(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("new_org")
	ctx.HTML(200, CREATE)
}

func CreatePost(ctx *middleware.Context, form auth.CreateOrgForm) {
	ctx.Data["Title"] = ctx.Tr("new_org")

	if ctx.HasError() {
		ctx.HTML(200, CREATE)
		return
	}

	org := &models.User{
		Name:     form.OrgName,
		Email:    form.Email,
		IsActive: true,
		Type:     models.ORGANIZATION,
	}

	var err error
	if org, err = models.CreateOrganization(org, ctx.User); err != nil {
		switch err {
		case models.ErrUserAlreadyExist:
			ctx.Data["Err_OrgName"] = true
			ctx.RenderWithErr(ctx.Tr("form.org_name_been_taken"), CREATE, &form)
		case models.ErrEmailAlreadyUsed:
			ctx.Data["Err_Email"] = true
			ctx.RenderWithErr(ctx.Tr("form.email_been_used"), CREATE, &form)
		case models.ErrUserNameIllegal:
			ctx.Data["Err_OrgName"] = true
			ctx.RenderWithErr(ctx.Tr("form.illegal_org_name"), CREATE, &form)
		default:
			ctx.Handle(500, "CreateUser", err)
		}
		return
	}
	log.Trace("Organization created: %s", org.Name)

	ctx.Redirect(setting.AppSubUrl + "/org/" + form.OrgName + "/dashboard")
}
