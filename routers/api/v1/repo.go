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

	// Check visibility.
	if ctx.IsSigned && opt.Uid > 0 {
		if ctx.User.Id == opt.Uid {
			opt.Private = true
		} else {
			u, err := models.GetUserById(opt.Uid)
			if err != nil {
				ctx.JSON(500, map[string]interface{}{
					"ok":    false,
					"error": err.Error(),
				})
				return
			}
			if u.IsOrganization() && u.IsOwnedBy(ctx.User.Id) {
				opt.Private = true
			}
			// FIXME: how about collaborators?
		}
	}

	repos, err := models.SearchRepositoryByName(opt)
	if err != nil {
		ctx.JSON(500, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	results := make([]*Repository, len(repos))
	for i := range repos {
		if err = repos[i].GetOwner(); err != nil {
			ctx.JSON(500, map[string]interface{}{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
		results[i] = ToApiRepository(repos[i].Owner, repos[i], api.Permission{}, service)
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
	numOwnRepos := len(ownRepos)

	collaRepos, err := models.GetCollaborativeRepos(ctx.User.Id)
	if err != nil {
		ctx.JSON(500, &base.ApiJsonErr{Message: "GetCollaborativeRepos: " + err.Error(), DocUrl: base.DOC_URL})
		return
	}

	repos := make([]*Repository, numOwnRepos+len(collaRepos))
	for i := range ownRepos {
		repos[i] = ToApiRepository(ctx.User, ownRepos[i], api.Permission{Admin: true, Push: true, Pull: true}, service)
	}
	for i := range collaRepos {
		if err = collaRepos[i].GetOwner(); err != nil {
			ctx.JSON(500, &base.ApiJsonErr{Message: "GetOwner: " + err.Error(), DocUrl: base.DOC_URL})
			return
		}
		j := i + numOwnRepos
		repos[j] = ToApiRepository(collaRepos[i].Owner, collaRepos[i].Repository, api.Permission{Admin: false, Push: collaRepos[i].CanPush, Pull: true}, service)

		// FIXME: cache result to reduce DB query?
		if collaRepos[i].Owner.IsOrganization() && collaRepos[i].Owner.IsOwnedBy(ctx.User.Id) {
			repos[j].Permissions.Admin = true
		}
	}

	ctx.JSON(200, &repos)
}
