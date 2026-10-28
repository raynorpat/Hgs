// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package middleware

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Unknwon/macaron"
	"github.com/macaron-contrib/cache"
	"github.com/macaron-contrib/csrf"
	"github.com/macaron-contrib/i18n"
	"github.com/macaron-contrib/session"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/setting"
)

// Context represents context of a request.
type Context struct {
	*macaron.Context
	Cache   cache.Cache
	csrf    csrf.CSRF
	Flash   *session.Flash
	Session session.Store

	User        *models.User
	IsSigned    bool
	IsBasicAuth bool

	// PrepareHTML composes optional page utilities only after request authority exists.
	PrepareHTML func(*Context) error
	// PreparePage supplies data-only presentation before optional discovery.
	PreparePage func(*Context, base.TplName)

	Repo struct {
		HgContext   context.Context
		HgRunner    *hg.Runner
		HgPath      string
		Node        hg.NodeID
		Changeset   hg.Changeset
		References  []hg.Reference // set when the landing page selection already listed them
		FinishRead  func()
		IsOwner     bool
		IsTrueOwner bool
		IsWatching  bool
		IsAdmin     bool // Current user is admin level.
		HasAccess   bool
		Repository  *models.Repository
		Owner       *models.User
		TreeName    string
		RepoLink    string
		CloneLink   models.CloneLink
	}

	Org struct {
		IsOwner      bool
		IsMember     bool
		IsAdminTeam  bool // In owner team or team that has admin permission level.
		Organization *models.User
		OrgLink      string

		Team *models.Team
	}
}

// HasError returns true if error occurs in form validation.
func (ctx *Context) HasApiError() bool {
	hasErr, ok := ctx.Data["HasError"]
	if !ok {
		return false
	}
	return hasErr.(bool)
}

func (ctx *Context) GetErrMsg() string {
	return ctx.Data["ErrorMsg"].(string)
}

// HasError returns true if error occurs in form validation.
func (ctx *Context) HasError() bool {
	hasErr, ok := ctx.Data["HasError"]
	if !ok {
		return false
	}
	ctx.Flash.ErrorMsg = ctx.Data["ErrorMsg"].(string)
	ctx.Data["Flash"] = ctx.Flash
	return hasErr.(bool)
}

// HTML calls Context.HTML and converts template name to string.
func (ctx *Context) HTML(status int, name base.TplName) {
	if ctx.PreparePage != nil {
		ctx.PreparePage(ctx, name)
	}
	// Native readers are reaped before transferring a buffered HTML response.
	if ctx.Repo.FinishRead != nil {
		ctx.Repo.FinishRead()
	}
	if status == 200 && ctx.PrepareHTML != nil {
		if err := ctx.PrepareHTML(ctx); err != nil {
			log.Error(4, "PrepareHTML: %v", err)
			ctx.Error(500)
			return
		}
	}
	ctx.Context.HTML(status, string(name))
	// Record only successful foreground HTML navigation, after the renderer has
	// succeeded. Assets, native traffic, polling, redirects and errors never count.
	if status == 200 && ctx.Resp.Status() == 200 && ctx.IsSigned && ctx.User != nil &&
		ctx.Repo.HasAccess && ctx.Repo.Repository != nil && ctx.Req.Method == "GET" &&
		strings.Contains(ctx.Req.Header.Get("Accept"), "text/html") &&
		ctx.Req.Header.Get("X-Requested-With") == "" &&
		(ctx.Req.Header.Get("Sec-Fetch-Mode") == "" || ctx.Req.Header.Get("Sec-Fetch-Mode") == "navigate") &&
		(ctx.Req.Header.Get("Sec-Fetch-Dest") == "" || ctx.Req.Header.Get("Sec-Fetch-Dest") == "document") {
		if err := models.RecordRepositoryVisit(ctx.Req.Request.Context(), ctx.User.Id, ctx.Repo.Repository.Id, time.Now()); err != nil {
			log.Error(4, "RecordRepositoryVisit: %v", err)
		}
	}
}

// RenderWithErr used for page has form validation but need to prompt error to users.
func (ctx *Context) RenderWithErr(msg string, tpl base.TplName, form interface{}) {
	if form != nil {
		auth.AssignForm(form, ctx.Data)
		// Submitted values belong to this response, independently of incoming flashes.
		ctx.Data["HasError"] = true
		ctx.Data["ErrorMsg"] = msg
	}
	ctx.Flash.ErrorMsg = msg
	ctx.Data["Flash"] = ctx.Flash
	ctx.HTML(200, tpl)
}

// Handle handles and logs error by given status.
func (ctx *Context) Handle(status int, title string, err error) {
	if err != nil {
		log.Error(4, "%s: %v", title, err)
		if macaron.Env != macaron.PROD {
			ctx.Data["ErrorMsg"] = err
		}
	}

	switch status {
	case 404:
		ctx.Data["Title"] = "Page Not Found"
	case 500:
		ctx.Data["Title"] = "Internal Server Error"
	}
	ctx.HTML(status, base.TplName(fmt.Sprintf("status/%d", status)))
}

func (ctx *Context) ServeContent(name string, r io.ReadSeeker, params ...interface{}) {
	modtime := time.Now()
	for _, p := range params {
		switch v := p.(type) {
		case time.Time:
			modtime = v
		}
	}
	ctx.Resp.Header().Set("Content-Description", "File Transfer")
	ctx.Resp.Header().Set("Content-Type", "application/octet-stream")
	ctx.Resp.Header().Set("Content-Disposition", "attachment; filename="+name)
	ctx.Resp.Header().Set("Content-Transfer-Encoding", "binary")
	ctx.Resp.Header().Set("Expires", "0")
	ctx.Resp.Header().Set("Cache-Control", "must-revalidate")
	ctx.Resp.Header().Set("Pragma", "public")
	http.ServeContent(ctx.Resp, ctx.Req.Request, name, modtime, r)
}

// Contexter initializes a classic context for a request.
func Contexter() macaron.Handler {
	return func(c *macaron.Context, l i18n.Locale, cache cache.Cache, sess session.Store, f *session.Flash, x csrf.CSRF) {
		ctx := &Context{
			Context: c,
			Cache:   cache,
			csrf:    x,
			Flash:   f,
			Session: sess,
		}
		// Compute current URL for real-time change language.
		link := setting.AppSubUrl + ctx.Req.RequestURI
		i := strings.Index(link, "?")
		if i > -1 {
			link = link[:i]
		}
		ctx.Data["Link"] = link

		ctx.Data["PageStartTime"] = time.Now()

		// Get user from session if logined.
		ctx.User, ctx.IsBasicAuth = auth.SignedInUser(ctx.Req.Request, ctx.Session)

		if ctx.User != nil {
			ctx.IsSigned = true
			ctx.Data["IsSigned"] = ctx.IsSigned
			ctx.Data["SignedUser"] = ctx.User
			ctx.Data["SignedUserName"] = ctx.User.Name
			ctx.Data["IsAdmin"] = ctx.User.IsAdmin
		} else {
			ctx.Data["SignedUserName"] = ""
		}

		// If request sends files, parse them here otherwise the Query() can't be parsed and the CsrfToken will be invalid.
		if ctx.Req.Method == "POST" && strings.Contains(ctx.Req.Header.Get("Content-Type"), "multipart/form-data") {
			if err := ctx.Req.ParseMultipartForm(setting.AttachmentMaxSize << 20); err != nil && !strings.Contains(err.Error(), "EOF") { // 32MB max size
				ctx.Handle(500, "ParseMultipartForm", err)
				return
			}
		}

		ctx.Data["CsrfToken"] = x.GetToken()
		ctx.Data["CsrfTokenHtml"] = template.HTML(`<input type="hidden" name="_csrf" value="` + x.GetToken() + `">`)

		ctx.Data["ShowRegistrationButton"] = setting.Service.ShowRegistrationButton

		c.Map(ctx)
	}
}
