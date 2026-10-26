// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"io"
	"mime"
	"path"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/git"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/middleware"
)

func ServeBlob(ctx *middleware.Context, blob *git.Blob) error {
	dataRc, err := blob.Data()
	if err != nil {
		return err
	}

	buf := make([]byte, 1024)
	n, _ := dataRc.Read(buf)
	if n > 0 {
		buf = buf[:n]
	}

	_, isTextFile := base.IsTextFile(buf)
	_, isImageFile := base.IsImageFile(buf)
	ctx.Resp.Header().Set("Content-Type", "text/plain")
	if !isTextFile && !isImageFile {
		ctx.Resp.Header().Set("Content-Disposition", "attachment; filename="+path.Base(ctx.Repo.TreeName))
		ctx.Resp.Header().Set("Content-Transfer-Encoding", "binary")
	}
	ctx.Resp.Write(buf)
	_, err = io.Copy(ctx.Resp, dataRc)
	return err
}

func SingleDownload(ctx *middleware.Context) {
	found := false
	entries, err := ctx.Repo.HgRunner.Manifest(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node)
	if err != nil {
		ctx.Handle(404, "Manifest", err)
		return
	}
	for _, entry := range entries {
		if entry.Path == ctx.Repo.TreeName {
			found = true
			break
		}
	}
	if !found {
		ctx.Error(404)
		return
	}
	ctx.Resp.Header().Set("Content-Type", "application/octet-stream")
	ctx.Resp.Header().Set("X-Content-Type-Options", "nosniff")
	ctx.Resp.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": displayPath(path.Base(ctx.Repo.TreeName))}))
	stopAbort := hgbridge.InterruptResponseOnCancel(ctx.Repo.HgContext, middleware.BrowserResponseController(ctx.Req.Request))
	defer stopAbort()
	// ReadFile joins the native process before the browse registration releases.
	err = ctx.Repo.HgRunner.ReadFile(ctx.Repo.HgContext, ctx.Repo.HgPath, ctx.Repo.Node, ctx.Repo.TreeName, ctx.Resp)
	if err != nil && !ctx.Written() {
		ctx.Error(404)
	}
}
