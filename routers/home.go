// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package routers

import (
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
	"github.com/gogits/gogs/routers/user"
)

const (
	HOME          base.TplName = "home"
	EXPLORE_REPOS base.TplName = "explore/repos"
)

func Home(ctx *middleware.Context) {
	if ctx.IsSigned {
		if !ctx.User.IsActive && setting.Service.RegisterEmailConfirm {
			ctx.Data["Title"] = ctx.Tr("auth.active_your_account")
			ctx.HTML(200, user.ACTIVATE)
		} else {
			user.Dashboard(ctx)
		}
		return
	}

	// Check auto-login.
	uname := ctx.GetCookie(setting.CookieUserName)
	if len(uname) != 0 {
		ctx.Redirect(setting.AppSubUrl + "/user/login")
		return
	}

	if setting.OauthService != nil {
		ctx.Data["OauthEnabled"] = true
		ctx.Data["OauthService"] = setting.OauthService
	}

	ctx.Data["PageIsHome"] = true
	if setting.Service.DisableRegistration {
		ctx.Data["ShowRegistrationButton"] = false
	}
	ctx.Data["ShowPublicExplore"] = !setting.Service.RequireSignInView
	if !setting.Service.RequireSignInView {
		page, err := models.ListAccessibleRepositories(ctx.Req.Request.Context(), 0, models.CatalogQuery{Visibility: "public", PageSize: 3})
		if err != nil {
			ctx.Handle(500, "ListAccessibleRepositories", err)
			return
		}
		ctx.Data["LandingRepositories"] = page.Repositories
		if len(page.Repositories) > 0 {
			repo := page.Repositories[0]
			ctx.Data["LandingCloneCommand"] = "hg clone " + setting.AppUrl + "hg/" + url.PathEscape(repo.Owner.Name) + "/" + url.PathEscape(repo.Name)
		}
	}
	ui.BuildShell(ctx, ui.Anonymous, "")
	ctx.HTML(200, HOME)
}

// catalogData validates route input before the model normalizes its own callers.
func catalogData(ctx *middleware.Context) (models.CatalogQuery, bool) {
	q := models.CatalogQuery{Text: ctx.Query("q"), Visibility: ctx.Query("visibility"), Scope: ctx.Query("scope")}
	invalid := false
	if utf8.RuneCountInString(q.Text) > 200 {
		invalid = true
	}
	if q.Visibility != "" && q.Visibility != "all" && q.Visibility != "public" && q.Visibility != "private" {
		invalid = true
	}
	if q.Scope != "" && q.Scope != "accessible" && q.Scope != "mine" {
		invalid = true
	}
	for _, field := range []struct {
		key    string
		target *int
	}{{"page", &q.Page}, {"page_size", &q.PageSize}} {
		raw := ctx.Query(field.key)
		if raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || (field.key == "page" && (value < 1 || value > 1000000)) {
				invalid = true
			} else {
				*field.target = value
			}
		}
	}
	if raw := ctx.Query("owner_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			invalid = true
		} else {
			q.OwnerID = id
		}
	}
	q = models.NormalizeCatalogQuery(q)
	ctx.Data["CatalogQuery"] = q
	return q, !invalid
}

func explore(ctx *middleware.Context, partial bool) {
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	if !ctx.IsSigned && setting.Service.RequireSignInView {
		ctx.Error(404)
		return
	}
	ctx.Data["Title"] = ctx.Tr("explore")
	kind := ui.Anonymous
	if ctx.IsSigned {
		kind = ui.Workspace
	}
	ui.BuildShell(ctx, kind, "explore")
	q, valid := catalogData(ctx)
	template := EXPLORE_REPOS
	if partial {
		template = "explore/repo_results"
	}
	if !valid {
		ctx.Data["CatalogError"] = ctx.Tr("ui.catalog_invalid")
		ctx.HTML(400, template)
		return
	}
	var actorID int64
	if ctx.IsSigned && ctx.User != nil {
		actorID = ctx.User.Id
	}
	page, err := models.ListAccessibleRepositories(ctx.Req.Request.Context(), actorID, q)
	if err != nil {
		ctx.Handle(500, "ListAccessibleRepositories", err)
		return
	}
	ctx.Data["Repos"] = page.Repositories
	ctx.Data["CatalogPage"] = page
	pages := []ui.PageLink{}
	makeLink := func(pageNumber int) string {
		values := url.Values{"q": {q.Text}, "visibility": {q.Visibility}, "scope": {q.Scope}, "page": {strconv.Itoa(pageNumber)}, "page_size": {strconv.Itoa(q.PageSize)}}
		if q.OwnerID > 0 {
			values.Set("owner_id", strconv.FormatInt(q.OwnerID, 10))
		}
		return setting.AppSubUrl + "/explore?" + values.Encode()
	}
	if q.Page > 1 {
		pages = append(pages, ui.PageLink{Label: ctx.Tr("ui.previous"), URL: makeLink(q.Page - 1)})
	}
	if page.Total > q.Page*q.PageSize {
		pages = append(pages, ui.PageLink{Label: ctx.Tr("ui.next"), URL: makeLink(q.Page + 1)})
	}
	ctx.Data["CatalogPagination"] = struct {
		Label string
		Pages []ui.PageLink
	}{ctx.Tr("ui.pagination"), pages}
	ctx.HTML(200, template)
}
func Explore(ctx *middleware.Context)       { explore(ctx, false) }
func ExploreSearch(ctx *middleware.Context) { explore(ctx, true) }
func NotFound(ctx *middleware.Context) {
	ctx.Data["Title"] = "Page Not Found"
	ctx.Handle(404, "home.NotFound", nil)
}
