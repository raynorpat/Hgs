package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

// HgPathURL encodes literal repository path bytes, never OS paths or refs.
func HgPathURL(repo, action string, node hg.NodeID, path string) string {
	u := repo + "/" + action
	if node != "" {
		u += "/" + string(node)
	}
	if path == "" {
		return u
	}
	if !utf8.ValidString(path) {
		return u + "?path=" + base64.RawURLEncoding.EncodeToString([]byte(path))
	}
	for _, seg := range strings.Split(path, "/") {
		u += "/" + url.PathEscape(seg)
	}
	return u
}

// ParseHgPath reads escaped URL segments once. Tokens preserve non-UTF-8 names.
func ParseHgPath(u *url.URL, prefix string) (string, error) {
	bad := hg.ErrNotFound
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", bad
	}
	escapedPrefix := (&url.URL{Path: prefix}).EscapedPath()
	rest, ok := strings.CutPrefix(u.EscapedPath(), escapedPrefix)
	if !ok {
		return "", bad
	}
	var p string
	if tokens, has := q["path"]; has {
		if rest != "" || len(tokens) != 1 || len(tokens[0]) > 8192 {
			return "", bad
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(tokens[0])
		if err != nil || len(b) == 0 {
			return "", bad
		}
		p = string(b)
	} else {
		if rest == "" {
			return "", nil
		}
		if !strings.HasPrefix(rest, "/") || len(rest) > 12288 {
			return "", bad
		}
		var parts []string
		for _, seg := range strings.Split(rest[1:], "/") {
			d, err := url.PathUnescape(seg)
			if err == nil && !utf8.ValidString(d) {
				return "", hg.ErrUnsupportedEncoding
			}
			if err != nil || strings.Contains(d, "/") {
				return "", bad
			}
			parts = append(parts, d)
		}
		p = strings.Join(parts, "/")
	}
	if len(p) > 4096 {
		return "", bad
	}
	if strings.Contains(p, `\`) {
		return "", hg.ErrUnsupportedEncoding
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, ".hg") || strings.ContainsRune(seg, 0) {
			return "", bad
		}
	}
	return p, nil
}

// BrowserResponseController preserves the net/http writer underneath Macaron.
// It is request-scoped and is used only to interrupt canceled raw transfers.
type browserControllerKey struct{}
type browserResponse struct {
	controller *http.ResponseController
	aborted    bool
}
type browserURLKey struct{}

func HgRequestURL(r *http.Request) *url.URL {
	if u, ok := r.Context().Value(browserURLKey{}).(*url.URL); ok {
		return u
	}
	return r.URL
}
func WithBrowserResponseController(r *http.Request, w http.ResponseWriter) *http.Request {
	u := *r.URL
	c := context.WithValue(r.Context(), browserURLKey{}, &u)
	return r.WithContext(context.WithValue(c, browserControllerKey{}, &browserResponse{controller: http.NewResponseController(w)}))
}
func BrowserResponseController(r *http.Request) *http.ResponseController {
	response, _ := r.Context().Value(browserControllerKey{}).(*browserResponse)
	if response == nil {
		return nil
	}
	return response.controller
}

// AbortBrowserResponse marks an incomplete stream for termination by the outer
// HTTP handler, beyond Macaron's panic recovery and response wrappers.
func AbortBrowserResponse(r *http.Request) {
	if response, ok := r.Context().Value(browserControllerKey{}).(*browserResponse); ok {
		response.aborted = true
	}
}
func BrowserResponseAborted(r *http.Request) bool {
	response, _ := r.Context().Value(browserControllerKey{}).(*browserResponse)
	return response != nil && response.aborted
}

// RepoBrowse tracks filesystem readers separately from metadata-only routes.
func RepoBrowse() macaron.Handler {
	return func(ctx *Context, provider hosting.Provider) {
		s := provider.Service()
		if s == nil {
			ctx.Error(503)
			return
		}
		readCtx, cancel := context.WithTimeout(ctx.Req.Context(), hg.BrowserCommandTimeout)
		uid := int64(0)
		if ctx.User != nil {
			uid = ctx.User.Id
		}
		release, err := s.RegisterBrowserRead(uid, ctx.Repo.Repository.Id, cancel)
		if err != nil {
			cancel()
			ctx.Error(404)
			return
		}
		finished := false
		ctx.Repo.FinishRead = func() {
			if !finished {
				finished = true
				release()
				cancel()
			}
		}
		defer ctx.Repo.FinishRead()
		ctx.Repo.HgContext = readCtx
		ctx.Repo.HgRunner = s.Runner()
		ctx.Repo.HgPath, err = models.RepositoryStoragePath(s.RepositoryRoot(), ctx.Repo.Repository.Id)
		if err != nil {
			ctx.Handle(500, "RepositoryStoragePath", err)
			return
		}
		ctx.Next()
	}
}

func RepoRef(action string) macaron.Handler {
	return func(ctx *Context) {
		q, err := url.ParseQuery(ctx.Req.URL.RawQuery)
		if err != nil {
			ctx.Error(400)
			return
		}
		param := ctx.Params(":node")
		prefix := ctx.Repo.RepoLink
		if param != "" {
			prefix += "/" + action + "/" + param
		}
		p, err := ParseHgPath(HgRequestURL(ctx.Req.Request), prefix)
		if err != nil {
			ctx.Error(400, err.Error())
			return
		}
		ctx.Repo.TreeName = p
		kinds, hasKind := q["ref_kind"]
		names, hasName := q["ref_name"]
		if hasKind || hasName {
			if len(kinds) != 1 || len(names) != 1 || len(names[0]) == 0 || len(names[0]) > 4096 {
				ctx.Error(400)
				return
			}
			kind := hg.RefKind(kinds[0])
			if kind != hg.RefBranch && kind != hg.RefBookmark && kind != hg.RefTag {
				ctx.Error(400)
				return
			}
			node, err := ctx.Repo.HgRunner.Resolve(ctx.Repo.HgContext, ctx.Repo.HgPath, hg.RefSelection{Kind: kind, Name: names[0]})
			if err != nil {
				ctx.Handle(404, "Reference", err)
				return
			}
			ctx.Repo.FinishRead()
			ctx.Redirect(HgPathURL(ctx.Repo.RepoLink, action, node, p))
			return
		}
		var node hg.NodeID
		if param != "" {
			node, err = hg.ParseNodeID(param)
			if err != nil {
				ctx.Error(400)
				return
			}
		} else {
			node, err = ctx.Repo.HgRunner.Landing(ctx.Repo.HgContext, ctx.Repo.HgPath)
		}
		if errors.Is(err, hg.ErrEmptyRepository) {
			ctx.Data["IsBareRepo"] = true
			ctx.Repo.FinishRead()
			ctx.HTML(200, "repo/bare")
			return
		}
		if err != nil {
			ctx.Handle(404, "Changeset", err)
			return
		}
		cs, err := ctx.Repo.HgRunner.Changeset(ctx.Repo.HgContext, ctx.Repo.HgPath, node)
		if err != nil {
			ctx.Handle(404, "Changeset", err)
			return
		}
		ctx.Repo.Node = node
		ctx.Repo.Changeset = cs
		ctx.Data["Changeset"] = cs
		ctx.Data["Node"] = string(node)
		ctx.Data["BranchName"] = cs.Branch
	}
}
