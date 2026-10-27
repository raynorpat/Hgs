package ui

import (
	"fmt"

	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
)

func IssuePagination(ctx *middleware.Context, page, total int) {
	pages := []PageLink{}
	for _, item := range []struct {
		n     int
		label string
	}{{page - 1, ctx.Tr("ui.previous")}, {page, fmt.Sprint(page)}, {page + 1, ctx.Tr("ui.next")}} {
		q := ctx.Req.URL.Query()
		q.Set("page", fmt.Sprint(item.n))
		pages = append(pages, PageLink{Label: item.label, URL: setting.AppSubUrl + ctx.Req.URL.Path + "?" + q.Encode(), Current: item.n == page, Disabled: item.n < 1 || (item.n > page && page*20 >= total)})
	}
	ctx.Data["IssuePagination"] = struct {
		Label string
		Pages []PageLink
	}{ctx.Tr("ui.issue_pages"), pages}
}

// IssueEmpty prepares a contextual, authorized action without revealing a denied
// target. Explicit unavailable targets are handled by the caller's safe404 path.
func IssueEmpty(ctx *middleware.Context, hasIssues bool, repoLink string) {
	if hasIssues {
		ctx.Data["IssueEmptyKind"] = "filtered"
		ctx.Data["IssueEmptyMessage"] = ctx.Tr("ui.no_issues")
		target := setting.AppSubUrl + "/issues"
		if repoLink != "" {
			target = repoLink + "/issues"
		}
		ctx.Data["IssueEmptyURL"] = target
		ctx.Data["IssueEmptyAction"] = ctx.Tr("ui.clear_filters")
		return
	}
	ctx.Data["IssueEmptyKind"] = "empty"
	if repoLink != "" {
		ctx.Data["IssueEmptyMessage"] = ctx.Tr("ui.no_repository_issues")
		if ctx.IsSigned {
			ctx.Data["IssueEmptyURL"] = repoLink + "/issues/new"
			ctx.Data["IssueEmptyAction"] = ctx.Tr("ui.new_issue")
		} else {
			ctx.Data["IssueEmptyURL"] = setting.AppSubUrl + "/user/login"
			ctx.Data["IssueEmptyAction"] = ctx.Tr("ui.sign_in")
		}
	} else {
		ctx.Data["IssueEmptyMessage"] = ctx.Tr("ui.no_workspace_issues")
		ctx.Data["IssueEmptyURL"] = setting.AppSubUrl + "/#repositories"
		ctx.Data["IssueEmptyAction"] = ctx.Tr("ui.choose_repository")
	}
}
