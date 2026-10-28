package middleware

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/modules/setting"
	"github.com/macaron-contrib/csrf"
)

// FreshDraftCSRF uses the configured native factory once. Only the explicit
// session check suppresses its incoming cookie, forcing fresh native issuance.
func FreshDraftCSRF(options csrf.Options) macaron.Handler {
	factory := csrf.Csrfer(options)
	return func(ctx *macaron.Context) {
		original := append([]string(nil), ctx.Req.Header.Values("Cookie")...)
		if ctx.Req.Method == "GET" && strings.TrimPrefix(ctx.Req.URL.Path, setting.AppSubUrl) == "/user/drafts/session" {
			cookies := ctx.Req.Cookies()
			ctx.Req.Header.Del("Cookie")
			for _, c := range cookies {
				if c.Name != options.Cookie {
					ctx.Req.AddCookie(c)
				}
			}
			defer func() {
				ctx.Req.Header.Del("Cookie")
				for _, v := range original {
					ctx.Req.Header.Add("Cookie", v)
				}
			}()
		}
		if _, err := ctx.Invoke(factory); err != nil {
			panic(err)
		}
	}
}

var editorSubmitPath = regexp.MustCompile(`^/[^/]+/[^/]+/(?:issues/(?:new|[1-9][0-9]*)|comment/new|pulls/(?:new|[1-9][0-9]*/(?:edit|comments|review)))$`)

func EditorSubmissionPolicy(ctx *Context, token csrf.CSRF) {
	if ctx.Req.Method != "POST" || ctx.Req.Header.Get("X-Hgs-Editor-Submit") != "1" || !editorSubmitPath.MatchString(strings.TrimPrefix(ctx.Req.URL.Path, setting.AppSubUrl)) {
		return
	}
	ctx.EditorSubmission = true
	if !DraftRequestPolicy(ctx, token, true) {
		return
	}
}
func DraftRequestPolicy(ctx *Context, token csrf.CSRF, mutation bool) bool {
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	if !ctx.IsSigned || ctx.User == nil {
		ctx.JSON(401, map[string]string{"code": "unauthenticated"})
		return false
	}
	if !ctx.User.IsActive || ctx.User.IsOrganization() {
		ctx.JSON(403, map[string]string{"code": "forbidden"})
		return false
	}
	expected := ctx.Req.Header.Get("X-Hgs-Editor-Account")
	if (ctx.EditorSubmission && expected == "") || (expected != "" && expected != strconv.FormatInt(ctx.User.Id, 10)) {
		ctx.JSON(403, map[string]string{"code": "account_changed"})
		return false
	}
	if mutation && !token.ValidToken(ctx.Req.Header.Get("X-Csrf-Token")) {
		ctx.JSON(400, map[string]string{"code": "csrf_invalid"})
		return false
	}
	return true
}
func EstablishedDraftSession(ctx *Context) bool {
	c, err := ctx.Req.Cookie(setting.SessionConfig.CookieName)
	return err == nil && c.Value != "" && c.Value == ctx.Session.ID()
}
func (ctx *Context) EditorResult(code, destination string, committed, unsaved bool) {
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	if destination != "" {
		u, err := url.Parse(destination)
		if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, setting.AppSubUrl+"/") || strings.HasPrefix(u.Path, "//") || strings.Contains(destination, "\\") {
			code, destination, committed, unsaved = "outcome_unknown", "", false, false
		}
	}
	ctx.JSON(200, map[string]interface{}{"code": code, "destination": destination, "committed": committed, "unsaved": unsaved})
}
func (ctx *Context) editorFailure(status int) {
	code := "outcome_unknown"
	switch status {
	case 400, 409, 413, 422:
		code = "validation"
	case 401:
		code = "unauthenticated"
	case 403, 404:
		code = "forbidden"
	}
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	ctx.JSON(status, map[string]string{"code": code})
}
func (ctx *Context) Redirect(location string, status ...int) {
	if ctx.EditorSubmission {
		if len(status) > 0 && status[0] == 303 {
			ctx.EditorResult("saved", location, true, false)
		} else {
			ctx.EditorResult("outcome_unknown", "", false, false)
		}
		return
	}
	ctx.Context.Redirect(location, status...)
}
func (ctx *Context) Error(status int, message ...string) {
	if ctx.EditorSubmission {
		ctx.editorFailure(status)
		return
	}
	ctx.Context.Error(status, message...)
}
