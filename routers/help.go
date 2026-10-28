package routers

import (
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

// Help describes the supported workflows without exposing instance configuration.
func Help(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("ui.help")
	ctx.Data["HelpSSHEnabled"] = !setting.DisableSSH && setting.SSHAddress != ""
	kind := ui.Anonymous
	if ctx.IsSigned {
		kind = ui.Workspace
	}
	ui.BuildShell(ctx, kind, "help")
	ctx.HTML(200, "help")
}
