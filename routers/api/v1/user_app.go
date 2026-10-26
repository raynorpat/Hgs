// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package v1

import (
	api "github.com/gogits/go-gogs-client"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
)

// GET /users/:username/tokens
func ListAccessTokens(ctx *middleware.Context) {
	tokens, err := models.ListAccessTokens(ctx.User.Id)
	if err != nil {
		ctx.JSON(500, &base.ApiJsonErr{Message: "ListAccessTokens: " + err.Error(), DocUrl: base.DOC_URL})
		return
	}

	apiTokens := make([]*api.AccessToken, len(tokens))
	for i := range tokens {
		apiTokens[i] = &api.AccessToken{Name: tokens[i].Name, Sha1: tokens[i].Sha1}
	}
	ctx.JSON(200, &apiTokens)
}

type CreateAccessTokenForm struct {
	Name string `json:"name" binding:"Required"`
}

// POST /users/:username/tokens
func CreateAccessToken(ctx *middleware.Context, form CreateAccessTokenForm) {
	t := &models.AccessToken{
		Uid:  ctx.User.Id,
		Name: form.Name,
	}
	if err := models.NewAccessToken(t); err != nil {
		ctx.JSON(500, &base.ApiJsonErr{Message: "NewAccessToken: " + err.Error(), DocUrl: base.DOC_URL})
		return
	}
	ctx.JSON(201, &api.AccessToken{Name: t.Name, Sha1: t.Sha1})
}
