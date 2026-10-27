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
