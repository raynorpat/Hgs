package ui

import (
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"strings"
)

// PrepareAccountPage is installed by server composition, avoiding a middleware/UI
// import cycle. It only composes view data: reduced pages never discover storage.
func PrepareAccountPage(ctx *middleware.Context, name base.TplName) {
	if ctx.IsSigned {
		ctx.Data[SignOutURL] = setting.AppSubUrl + "/user/logout"
	}
	page := string(name)
	ctx.Data["UIAccountPage"] = strings.HasPrefix(page, "admin/") || strings.HasPrefix(page, "org/") || strings.HasPrefix(page, "user/settings/") || strings.HasPrefix(page, "user/auth/") || page == "user/profile" || page == "install"
	if ctx.Data["UIAccountPage"] == true && ctx.Flash != nil && ctx.Flash.ErrorMsg != "" {
		ctx.Data["HasError"] = true
	}
	switch {
	case page == "install" || strings.HasPrefix(page, "user/auth/"):
		BuildShell(ctx, Anonymous, "")
	case strings.HasPrefix(page, "admin/"):
		active := "overview"
		for key, flag := range map[string]string{"users": "PageIsAdminUsers", "organizations": "PageIsAdminOrganizations", "repositories": "PageIsAdminRepositories", "authentication": "PageIsAdminAuthentications", "configuration": "PageIsAdminConfig", "notices": "PageIsAdminNotices", "monitoring": "PageIsAdminMonitor"} {
			if ctx.Data[flag] == true {
				active = key
			}
		}
		BuildShell(ctx, Admin, active)
	case strings.HasPrefix(page, "user/settings/") || strings.HasPrefix(page, "org/") || page == "user/profile":
		active := ""
		if strings.HasPrefix(page, "org/") {
			active = "organizations"
		}
		BuildShell(ctx, Workspace, active)
	}
}
