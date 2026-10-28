package ui

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
)

// BuildShell does not query storage: repository authority is supplied by RepoAssignment.
// Anonymous is a genuinely reduced layout for authentication and installation.
func BuildShell(ctx *middleware.Context, kind ContextKind, active string) Shell {
	if _, set := ctx.Data[PullRequestsURL]; !set {
		ctx.Data[PullRequestsURL] = setting.AppSubUrl + "/pulls"
		if kind == Repository && ctx.Repo.HasAccess && ctx.Repo.Repository != nil {
			ctx.Data[PullRequestsURL] = ctx.Repo.RepoLink + "/pulls"
		}
	}
	s := Shell{Kind: kind, SubURL: setting.AppSubUrl}
	// Macaron strips AppSubUrl from URL.Path, but leaves RequestURI untouched.
	// Keep only navigation filters: password-reset/login tokens must not be copied.
	query := url.Values{}
	for _, key := range []string{"q", "page", "type", "state", "sort", "sortType", "repoid", "ref", "branch", "bookmark", "tag", "rev", "labels", "milestone", "assignee", "uid", "filter", "visibility", "scope", "owner_id", "page_size", "ref_kind", "ref_name", "parent", "cursor", "path"} {
		if values, ok := ctx.Req.URL.Query()[key]; ok {
			query[key] = values
		}
	}
	languageLink := url.URL{Path: s.SubURL + ctx.Req.URL.Path, RawQuery: query.Encode()}
	if ctx.Req.URL.RawPath != "" {
		languageLink.RawPath = s.SubURL + ctx.Req.URL.RawPath
	}
	ctx.Data["UILanguageLink"] = languageLink.String()
	if title, ok := ctx.Data["Title"].(string); ok {
		s.Title = title
	}
	s.SignedIn = ctx.IsSigned && ctx.User != nil
	s.CanAdmin = s.SignedIn && ctx.User.IsAdmin && kind != Anonymous
	if s.SignedIn {
		s.AvatarURL = ctx.User.AvatarLink()
		for _, r := range ctx.User.Name {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				s.Initials = string(unicode.ToUpper(r))
				break
			}
		}
	}
	if s.Initials == "" {
		s.Initials = "?"
	}
	tr := func(key string) string {
		if l, ok := ctx.Data["i18n"].(interface {
			Tr(string, ...interface{}) string
		}); ok {
			return l.Tr("ui." + key)
		}
		return key
	}
	add := func(key, path, icon string) {
		s.Navigation = append(s.Navigation, NavItem{Key: key, Label: tr(key), URL: s.SubURL + path, Icon: icon, Active: key == active})
	}
	if kind == Admin && !s.CanAdmin {
		s.Kind = Workspace
		kind = Workspace
	}
	switch kind {
	case Workspace:
		if s.SignedIn {
			add("overview", "/", "home")
			add("repositories", "/#repositories", "repository")
			add("organizations", "/#organizations", "organization")
			if u, ok := ctx.Data[PullRequestsURL].(string); ok && u != "" {
				s.Navigation = append(s.Navigation, NavItem{Key: "pull_requests", Label: tr("pull_requests"), URL: u, Icon: "pull-request", Active: active == "pull_requests"})
			}
			add("issues", "/issues", "issue")
		}
	case Repository:
		add("workspace", "/", "back")
		if ctx.Repo.HasAccess && ctx.Repo.Repository != nil {
			root := strings.TrimPrefix(ctx.Repo.RepoLink, s.SubURL)
			add("source", root, "repository")
			add("changesets", root+"/commits", "history")
			add("branches", root+"/branches", "branch")
			if u, ok := ctx.Data[PullRequestsURL].(string); ok && u != "" {
				s.Navigation = append(s.Navigation, NavItem{Key: "pull_requests", Label: tr("pull_requests"), URL: u, Icon: "pull-request", Active: active == "pull_requests"})
			}
			add("issues", root+"/issues", "issue")
			if s.SignedIn && (ctx.Repo.IsTrueOwner || ctx.Repo.IsAdmin) {
				add("settings", root+"/settings", "settings")
			}
			if ctx.Repo.Owner != nil {
				ctx.Data["UIRepositoryName"] = fmt.Sprintf("%s / %s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)
			}
		}
	case Admin:
		add("workspace", "/", "back")
		for _, n := range []struct{ k, p, i string }{{"overview", "", "home"}, {"users", "/users", "account"}, {"organizations", "/orgs", "organization"}, {"repositories", "/repos", "repository"}, {"authentication", "/auths", "lock"}, {"configuration", "/config", "settings"}, {"notices", "/notices", "issue"}, {"monitoring", "/monitor", "history"}} {
			add(n.k, "/admin"+n.p, n.i)
		}
	}
	ctx.Data["Shell"] = s
	return s
}
