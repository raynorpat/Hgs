// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/middleware"
	"mime"
	"path"
)

func SingleDownload(ctx *middleware.Context) {
	ctx.Resp.Header().Set("Content-Type", "application/octet-stream")
	ctx.Resp.Header().Set("X-Content-Type-Options", "nosniff")
	ctx.Resp.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": displayPath(path.Base(ctx.Repo.TreeName))}))
	stopAbort := hgbridge.InterruptResponseOnCancel(ctx.Repo.HgContext, middleware.BrowserResponseController(ctx.Req.Request))
	defer stopAbort()
	// ReadFile joins the native process before the browse registration releases.
	// ReadFile reports a missing file before writing, so no manifest scan is needed.
	err := ctx.Repo.HgRunner.ReadFile(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, ctx.Repo.TreeName, ctx.Resp)
	if err != nil {
		if !ctx.Written() {
			ctx.Error(404)
		} else {
			middleware.AbortBrowserResponse(ctx.Req.Request)
		}
	}
}
