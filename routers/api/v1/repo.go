// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package v1

import (
	"path"

	"github.com/Unknwon/com"

	api "github.com/gogits/go-gogs-client"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
)

// Repository is the retained Mercurial repository API representation.
type Repository struct {
	Id          int64          `json:"id"`
	Owner       api.User       `json:"owner"`
	FullName    string         `json:"full_name"`
	Private     bool           `json:"private"`
	HtmlUrl     string         `json:"html_url"`
	CloneUrl    string         `json:"clone_url"`
	SshUrl      string         `json:"ssh_url"`
	Permissions api.Permission `json:"permissions"`
}

func ToApiRepository(owner *models.User, repo *models.Repository, permission api.Permission, service *hosting.Service) *Repository {
	cl := service.CloneLinks(owner.Name, repo.Name)
	return &Repository{
		Id: repo.Id, Owner: *ToApiUser(owner), FullName: owner.Name + "/" + repo.Name,
		Private: repo.IsPrivate, HtmlUrl: setting.AppUrl + owner.Name + "/" + repo.Name,
		CloneUrl: cl.HTTPS, SshUrl: cl.SSH, Permissions: permission,
	}
}

func SearchRepos(ctx *middleware.Context, provider hosting.Provider) {
	service := provider.Service()
	if service == nil {
		ctx.Error(503)
		return
	}
	opt := models.SearchOption{
		Keyword: path.Base(ctx.Query("q")),
		Uid:     com.StrTo(ctx.Query("uid")).MustInt64(),
		Limit:   com.StrTo(ctx.Query("limit")).MustInt(),
	}
	if opt.Limit == 0 {
		opt.Limit = 10
	}

	actorID := int64(0)
	if ctx.User != nil {
		actorID = ctx.User.Id
	}
	page, err := models.ListAccessibleRepositories(ctx.Req.Context(), actorID, models.CatalogQuery{Text: opt.Keyword, OwnerID: opt.Uid, PageSize: opt.Limit})
	if err != nil {
		ctx.JSON(500, &base.ApiJsonErr{Message: err.Error(), DocUrl: base.DOC_URL})
		return
	}
	results := make([]*Repository, 0, len(page.Repositories))
	for _, repo := range page.Repositories {
		access, err := models.RepositoryAccessFor(actorID, repo.Id)
		if err != nil || !access.Read {
			continue
		}
		results = append(results, ToApiRepository(repo.Owner, repo, api.Permission{Admin: access.Admin, Push: access.Write, Pull: access.Read}, service))
	}

	ctx.JSON(200, map[string]interface{}{
		"ok":   true,
		"data": results,
	})
}

// GET /user/repos
func ListMyRepos(ctx *middleware.Context, provider hosting.Provider) {
	service := provider.Service()
	if service == nil {
		ctx.Error(503)
		return
	}
	ownRepos, err := models.GetRepositories(ctx.User.Id, true)
	if err != nil {
		ctx.JSON(500, &base.ApiJsonErr{Message: "GetRepositories: " + err.Error(), DocUrl: base.DOC_URL})
		return
	}
	collaRepos, err := models.GetCollaborativeRepos(ctx.User.Id)
	if err != nil {
		ctx.JSON(500, &base.ApiJsonErr{Message: err.Error(), DocUrl: base.DOC_URL})
		return
	}
	candidates := append([]*models.Repository{}, ownRepos...)
	for _, repo := range collaRepos {
		candidates = append(candidates, repo.Repository)
	}
	repos := make([]*Repository, 0, len(candidates))
	seen := map[int64]bool{}
	for _, repo := range candidates {
		if seen[repo.Id] {
			continue
		}
		seen[repo.Id] = true
		access, err := models.RepositoryAccessFor(ctx.User.Id, repo.Id)
		if err == models.ErrRepoNotExist {
			continue
		}
		if err != nil {
			ctx.JSON(500, &base.ApiJsonErr{Message: err.Error(), DocUrl: base.DOC_URL})
			return
		}
		if !access.Read {
			continue
		}
		if err = repo.GetOwner(); err != nil {
			ctx.JSON(500, &base.ApiJsonErr{Message: err.Error(), DocUrl: base.DOC_URL})
			return
		}
		repos = append(repos, ToApiRepository(repo.Owner, repo, api.Permission{Admin: access.Admin, Push: access.Write, Pull: access.Read}, service))
	}
	ctx.JSON(200, &repos)
}
