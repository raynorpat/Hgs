package user

import (
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
)

// PrepareDiscovery composes per-render utility data after shell authority exists.
// Reduced/auth/install pages do not touch account or repository storage.
func PrepareDiscovery(ctx *middleware.Context) error {
	shell, ok := ctx.Data["Shell"].(ui.Shell)
	if !ok || shell.Kind == ui.Anonymous {
		return nil
	}
	if !ctx.IsSigned || ctx.User == nil {
		return nil
	}
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	repos, err := models.RecentRepositories(ctx.Req.Request.Context(), ctx.User.Id)
	if err != nil {
		return err
	}
	items := make([]ui.NavItem, 0, len(repos))
	for _, repo := range repos {
		link, err := repo.RepoLink()
		if err != nil {
			return err
		}
		items = append(items, ui.NavItem{Label: repo.Owner.Name + " / " + repo.Name, URL: link})
	}
	ctx.Data[ui.SearchURL] = setting.AppSubUrl + "/explore/search"
	ctx.Data[ui.RecentsURL] = setting.AppSubUrl + "/user/recent"
	ctx.Data["UIRecentRepositories"] = items
	return nil
}

func Recent(ctx *middleware.Context) {
	if !ctx.IsSigned || ctx.User == nil {
		ctx.Error(404)
		return
	}
	ctx.Data["Title"] = ctx.Tr("ui.recents")
	ui.BuildShell(ctx, ui.Workspace, "")
	ctx.HTML(200, "user/recent")
}

// ClearRecent is registered behind the existing sign-in and CSRF Toggle guard.
func ClearRecent(ctx *middleware.Context) {
	if !ctx.IsSigned || ctx.User == nil {
		ctx.Error(404)
		return
	}
	if err := models.ClearRepositoryVisits(ctx.Req.Request.Context(), ctx.User.Id); err != nil {
		ctx.Handle(500, "ClearRepositoryVisits", err)
		return
	}
	ctx.Redirect(setting.AppSubUrl+"/user/recent", 303)
}
