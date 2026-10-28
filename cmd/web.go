// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"html/template"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"

	"github.com/Unknwon/macaron"
	"github.com/codegangsta/cli"
	"github.com/macaron-contrib/binding"
	"github.com/macaron-contrib/cache"
	"github.com/macaron-contrib/captcha"
	"github.com/macaron-contrib/csrf"
	"github.com/macaron-contrib/i18n"
	"github.com/macaron-contrib/oauth2"
	"github.com/macaron-contrib/session"
	"github.com/macaron-contrib/toolbox"
	"gopkg.in/ini.v1"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/auth/apiv1"
	"github.com/gogits/gogs/modules/avatar"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/ui"
	"github.com/gogits/gogs/modules/version"
	"github.com/gogits/gogs/routers"
	"github.com/gogits/gogs/routers/admin"
	"github.com/gogits/gogs/routers/api/v1"
	"github.com/gogits/gogs/routers/org"
	"github.com/gogits/gogs/routers/repo"
	"github.com/gogits/gogs/routers/user"
)

var CmdWeb = cli.Command{
	Name:  "web",
	Usage: "Start Gogs web server",
	Description: `Gogs web server is the only thing you need to run,
and it takes care of all the other things for you`,
	Action: runWeb,
	Flags: []cli.Flag{
		cli.StringFlag{Name: "port, p", Value: "3000", Usage: "Temporary port number to prevent conflict"},
		cli.StringFlag{Name: "config, c", Value: "custom/conf/app.ini", Usage: "Custom configuration file path"},
	},
}

type VerChecker struct {
	ImportPath string
	Version    func() string
	Expected   string
}

// checkVersion checks if binary matches the version of templates files.
func checkVersion() {
	// Templates.
	data, err := ioutil.ReadFile(path.Join(setting.StaticRootPath, "templates/.VERSION"))
	if err != nil {
		log.Fatal(4, "Fail to read 'templates/.VERSION': %v", err)
	}
	if string(data) != setting.AppVer {
		log.Fatal(4, "Binary and template file version does not match, did you forget to recompile?")
	}

	// Check dependency version.
	checkers := []VerChecker{
		{"github.com/Unknwon/macaron", macaron.Version, "0.5.1"},
		{"github.com/macaron-contrib/binding", binding.Version, "0.0.4"},
		{"github.com/macaron-contrib/cache", cache.Version, "0.0.7"},
		{"github.com/macaron-contrib/csrf", csrf.Version, "0.0.3"},
		{"github.com/macaron-contrib/i18n", i18n.Version, "0.0.5"},
		{"github.com/macaron-contrib/session", session.Version, "0.1.6"},
		{"gopkg.in/ini.v1", ini.Version, "1.2.0"},
	}
	for _, c := range checkers {
		ver := c.Version()
		if !version.AtLeast(ver, c.Expected) {
			log.Fatal(4, "Package '%s' version is too old(%s -> %s), did you forget to update?", c.ImportPath, ver, c.Expected)
		}
	}
}

// newMacaron initializes Macaron instance.
func newMacaron() *macaron.Macaron {
	m := macaron.New()
	m.Use(macaron.Logger())
	m.Use(macaron.Recovery())
	if setting.EnableGzip {
		m.Use(macaron.Gziper())
	}
	m.SetURLPrefix(setting.AppSubUrl)
	m.Use(macaron.Static(
		path.Join(setting.StaticRootPath, "public"),
		macaron.StaticOptions{
			SkipLogging: !setting.DisableRouterLog,
		},
	))
	m.Use(macaron.Static(
		setting.AvatarUploadPath,
		macaron.StaticOptions{
			Prefix:      "avatars",
			SkipLogging: !setting.DisableRouterLog,
		},
	))
	m.Use(macaron.Renderer(macaron.RenderOptions{
		Directory:  path.Join(setting.StaticRootPath, "templates"),
		Funcs:      []template.FuncMap{base.TemplateFuncs},
		IndentJSON: macaron.Env != macaron.PROD,
	}))
	m.Use(middleware.Localization(i18n.Options{
		SubURL:          setting.AppSubUrl,
		Directory:       path.Join(setting.ConfRootPath, "locale"),
		CustomDirectory: path.Join(setting.CustomPath, "conf/locale"),
		Langs:           setting.Langs,
		Names:           setting.Names,
		// Retain safe navigation queries when selecting a language. The native
		// selector still owns locale validation and the language cookie.
		Redirect: false,
	}))
	m.Use(cache.Cacher(cache.Options{
		Adapter:       setting.CacheAdapter,
		AdapterConfig: setting.CacheConn,
		Interval:      setting.CacheInternal,
	}))
	captchaHandler := captcha.Captchaer(captcha.Options{SubURL: setting.AppSubUrl})
	m.Use(func(ctx *macaron.Context) {
		// The pinned image handler matches its route-relative URL prefix.
		original := ctx.Req.RequestURI
		ctx.Req.RequestURI = ctx.Req.URL.RequestURI()
		defer func() { ctx.Req.RequestURI = original }()
		if _, err := ctx.Invoke(captchaHandler); err != nil {
			panic(err)
		}
	})
	m.Use(session.Sessioner(setting.SessionConfig))
	m.Use(middleware.FreshDraftCSRF(csrf.Options{
		Secret:     setting.SecretKey,
		Cookie:     "_csrf",
		SetCookie:  true,
		Header:     "X-Csrf-Token",
		CookiePath: setting.AppSubUrl,
	}))
	m.Use(toolbox.Toolboxer(m, toolbox.Options{
		HealthCheckFuncs: []*toolbox.HealthCheckFuncDesc{
			&toolbox.HealthCheckFuncDesc{
				Desc: "Database connection",
				Func: models.Ping,
			},
		},
	}))

	// OAuth 2.
	if setting.OauthService != nil {
		for _, info := range setting.OauthService.OauthInfos {
			m.Use(oauth2.NewOAuth2Provider(info.Options, info.AuthUrl, info.TokenUrl))
		}
	}
	m.Use(middleware.Contexter())
	m.Use(middleware.EditorSubmissionPolicy)
	m.Use(func(ctx *middleware.Context) {
		ctx.PrepareHTML = user.PrepareDiscovery
		ctx.PreparePage = ui.PrepareAccountPage
	})
	return m
}

func runWeb(ctx *cli.Context) {
	if err := runWebServer(ctx); err != nil {
		log.Fatal(4, "Web server failed: %v", err)
	}
}

// All resources are released before the CLI reports a fatal startup error.
func runWebServer(ctx *cli.Context) error {
	if ctx.IsSet("config") {
		setting.CustomConf = ctx.String("config")
	}
	if err := routers.GlobalInit(); err != nil {
		return err
	}
	// Template paths come from the selected configuration, not the caller's
	// working directory. Validate them only after that configuration is loaded.
	checkVersion()
	holder := &hostingHolder{errors: make(chan error, 1)}
	stopDrafts := func() {}
	defer func() {
		stopDrafts()
		ctx, cancel := context.WithTimeout(context.Background(), hg.ShutdownTimeout)
		defer cancel()
		holder.Shutdown(ctx)
		models.CloseEngine()
	}()

	handler := newWebHandler(holder)

	// Flag for port number in case first time run conflict.
	if ctx.IsSet("port") {
		setting.AppUrl = strings.Replace(setting.AppUrl, setting.HttpPort, ctx.String("port"), 1)
		setting.HttpPort = ctx.String("port")
	}

	listenAddr := net.JoinHostPort(setting.HttpAddr, setting.HttpPort)
	log.Info("Listen: %v://%s%s", setting.Protocol, listenAddr, setting.AppSubUrl)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	server := &http.Server{Handler: handler, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if setting.Protocol != setting.HTTP && setting.Protocol != setting.HTTPS && setting.Protocol != setting.FCGI {
		return errors.New("invalid server protocol: " + string(setting.Protocol))
	}
	if setting.Protocol == setting.HTTPS {
		certificate, err := tls.LoadX509KeyPair(setting.CertFile, setting.KeyFile)
		if err != nil {
			return err
		}
		server.TLSConfig.Certificates = []tls.Certificate{certificate}
	}
	if setting.InstallLock {
		if err := holder.Initialize(); err != nil {
			return err
		}
	}
	stopDrafts = startDraftPurger(user.NewDraftService().PurgeExpired, func() bool { return holder.Service() != nil })
	served := make(chan error, 1)
	switch setting.Protocol {
	case setting.HTTP:
		go func() { served <- server.Serve(ln) }()
	case setting.HTTPS:
		go func() { served <- server.ServeTLS(ln, "", "") }()
	case setting.FCGI:
		go func() { served <- fcgi.Serve(ln, server.Handler) }()
	default:
		return errors.New("invalid server protocol: " + string(setting.Protocol))
	}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-signals.Done():
	case err = <-served:
	case err = <-holder.errors:
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error(4, "Server stopped: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), hg.ShutdownTimeout)
	defer cancel()
	// The raw holder rejects new admissions while HTTP and SSH drain together.
	done := make(chan error, 1)
	go func() { done <- holder.Shutdown(shutdownCtx) }()
	ln.Close()
	if setting.Protocol != setting.FCGI {
		server.Shutdown(shutdownCtx)
	}
	if shutdownErr := <-done; shutdownErr != nil {
		log.Warn("Hosting shutdown: %v", shutdownErr)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newWebHandler is the production registration path used by startup and route tests.
func newWebHandler(holder *hostingHolder) http.Handler {
	m := newMacaron()
	m.MapTo(holder, (*hosting.Provider)(nil))
	m.Map(collaboration.New(holder))
	m.Map(user.NewDraftService())
	m.Map(routers.InitializeHosting(holder.Initialize))

	reqSignIn := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	ignSignIn := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: setting.Service.RequireSignInView})
	reqSignOut := middleware.Toggle(&middleware.ToggleOptions{SignOutRequire: true})

	bind := binding.Bind
	bindIgnErr := binding.BindIgnErr
	protectedPOST := func(ctx *macaron.Context, token csrf.CSRF) { csrf.Validate(ctx, token) }

	// Routers.
	m.Get("/", middleware.Toggle(&middleware.ToggleOptions{}), routers.Home)
	m.Get("/explore", middleware.Toggle(&middleware.ToggleOptions{}), routers.Explore)
	m.Get("/explore/search", middleware.Toggle(&middleware.ToggleOptions{}), routers.ExploreSearch)
	m.Get("/help", ignSignIn, routers.Help)
	m.Get("/user/recent", reqSignIn, user.Recent)
	m.Post("/user/recent/clear", reqSignIn, user.ClearRecent)
	m.Get("/user/drafts/session", user.DraftSession)
	m.Post("/user/drafts/check", user.DraftCheck)
	m.Post("/user/drafts/open", user.DraftOpen)
	m.Post("/user/drafts/:page/save", user.DraftSave)
	m.Post("/user/drafts/:page/restore", user.DraftRestore)
	m.Post("/user/drafts/:page/discard", user.DraftDiscard)
	m.Combo("/install", routers.InstallInit).
		Get(routers.Install).
		Post(protectedPOST, bindIgnErr(auth.InstallForm{}), routers.InstallPost)
	m.Group("", func() {
		m.Get("/issues", user.Issues)
		m.Get("/pulls", repo.Pulls)
	}, reqSignIn)

	// API.
	// FIXME: custom form error response.
	m.Group("/api", func() {
		m.Group("/v1", func() {
			// Miscellaneous.
			m.Post("/markdown", bindIgnErr(apiv1.MarkdownForm{}), v1.Markdown)
			m.Post("/markdown/raw", v1.MarkdownRaw)

			// Users.
			m.Group("/users", func() {
				m.Get("/search", v1.SearchUsers)

				m.Group("/:username", func() {
					m.Get("", v1.GetUserInfo)

					m.Group("/tokens", func() {
						m.Combo("").Get(v1.ListAccessTokens).Post(bind(v1.CreateAccessTokenForm{}), v1.CreateAccessToken)
					}, middleware.ApiReqBasicAuth())
				})
			})

			// Repositories.

			m.Get("/user/repos", middleware.ApiReqToken(), v1.ListMyRepos)
			m.Get("/repos/search", v1.SearchRepos)

			m.Any("/*", func(ctx *middleware.Context) {
				ctx.JSON(404, &base.ApiJsonErr{Message: "Not Found", DocUrl: base.DOC_URL})
			})
		})
	})

	// User.
	m.Group("/user", func() {
		m.Get("/login", user.SignIn)
		m.Post("/login", protectedPOST, bindIgnErr(auth.SignInForm{}), user.SignInPost)
		m.Get("/info/:name", user.SocialSignIn)
		m.Get("/sign_up", user.SignUp)
		m.Post("/sign_up", protectedPOST, bindIgnErr(auth.RegisterForm{}), user.SignUpPost)
		m.Get("/reset_password", user.ResetPasswd)
		m.Post("/reset_password", protectedPOST, user.ResetPasswdPost)
	}, reqSignOut)
	m.Group("/user/settings", func() {
		m.Get("", user.Settings)
		m.Post("", bindIgnErr(auth.UpdateProfileForm{}), user.SettingsPost)
		m.Post("/avatar", binding.MultipartForm(auth.UploadAvatarForm{}), user.SettingsAvatar)
		m.Get("/email", user.SettingsEmails)
		m.Post("/email", bindIgnErr(auth.AddEmailForm{}), user.SettingsEmailPost)
		m.Get("/password", user.SettingsPassword)
		m.Post("/password", bindIgnErr(auth.ChangePasswordForm{}), user.SettingsPasswordPost)
		m.Get("/ssh", user.SettingsSSHKeys)
		m.Post("/ssh", bindIgnErr(auth.AddSSHKeyForm{}), user.SettingsSSHKeysPost)
		m.Get("/social", user.SettingsSocial)
		m.Post("/social", user.SettingsSocial)
		m.Combo("/applications").Get(user.SettingsApplications).Post(bindIgnErr(auth.NewAccessTokenForm{}), user.SettingsApplicationsPost)
		m.Route("/delete", "GET,POST", user.SettingsDelete)
	}, reqSignIn)
	m.Group("/user", func() {
		// r.Get("/feeds", binding.Bind(auth.FeedsForm{}), user.Feeds)
		m.Get("/activate", user.Activate)
		m.Post("/activate", protectedPOST, user.Activate)
		m.Get("/activate_email", user.ActivateEmail)
		m.Get("/email2user", user.Email2User)
		m.Get("/forget_password", user.ForgotPasswd)
		m.Post("/forget_password", protectedPOST, user.ForgotPasswdPost)
		m.Post("/logout", protectedPOST, user.SignOut)
	})

	// Gravatar service.
	avatarCache := path.Join(setting.StaticRootPath, "public/img/avatar")
	avt := avatar.CacheServer(avatarCache, path.Join(setting.StaticRootPath, "public/img/avatar_default.jpg"))
	os.MkdirAll(avatarCache, os.ModePerm)
	m.Get("/avatar/:hash", avt.ServeHTTP)

	adminReq := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true, AdminRequire: true})

	m.Group("/admin", func() {
		m.Get("", admin.Dashboard)
		m.Post("", admin.Dashboard)
		m.Get("/config", admin.Config)
		m.Get("/monitor", admin.Monitor)

		m.Group("/users", func() {
			m.Get("", admin.Users)
			m.Get("/new", admin.NewUser)
			m.Post("/new", bindIgnErr(auth.RegisterForm{}), admin.NewUserPost)
			m.Get("/:userid", admin.EditUser)
			m.Post("/:userid", bindIgnErr(auth.AdminEditUserForm{}), admin.EditUserPost)
			m.Post("/:userid/delete", admin.DeleteUser)
		})

		m.Group("/orgs", func() {
			m.Get("", admin.Organizations)
		})

		m.Group("/repos", func() {
			m.Get("", admin.Repositories)
		})

		m.Group("/auths", func() {
			m.Get("", admin.Authentications)
			m.Get("/new", admin.NewAuthSource)
			m.Post("/new", bindIgnErr(auth.AuthenticationForm{}), admin.NewAuthSourcePost)
			m.Get("/:authid", admin.EditAuthSource)
			m.Post("/:authid", bindIgnErr(auth.AuthenticationForm{}), admin.EditAuthSourcePost)
			m.Post("/:authid/delete", admin.DeleteAuthSource)
		})

		m.Group("/notices", func() {
			m.Get("", admin.Notices)
			m.Post("/:id:int/delete", admin.DeleteNotice)
		})
	}, adminReq)

	m.Get("/:username", ignSignIn, user.Profile)

	reqTrueOwner := middleware.RequireTrueOwner()

	// Organization.
	m.Group("/org", func() {
		m.Get("/create", org.Create)
		m.Post("/create", bindIgnErr(auth.CreateOrgForm{}), org.CreatePost)

		m.Group("/:org", func() {
			m.Get("/dashboard", user.Dashboard)
			m.Get("/members", org.Members)
			m.Post("/members/action/:action", org.MembersAction)

			m.Get("/teams", org.Teams)
			m.Get("/teams/:team", org.TeamMembers)
			m.Get("/teams/:team/repositories", org.TeamRepositories)
			m.Post("/teams/:team/action/:action", org.TeamsAction)
			m.Post("/teams/:team/action/repo/:action", org.TeamsRepoAction)
		}, middleware.OrgAssignment(true, true))

		m.Group("/:org", func() {
			m.Get("/teams/new", org.NewTeam)
			m.Post("/teams/new", bindIgnErr(auth.CreateTeamForm{}), org.NewTeamPost)
			m.Get("/teams/:team/edit", org.EditTeam)
			m.Post("/teams/:team/edit", bindIgnErr(auth.CreateTeamForm{}), org.EditTeamPost)
			m.Post("/teams/:team/delete", org.DeleteTeam)

			m.Group("/settings", func() {
				m.Get("", org.Settings)
				m.Post("", bindIgnErr(auth.UpdateOrgSettingForm{}), org.SettingsPost)
				m.Route("/delete", "GET,POST", org.SettingsDelete)
			})

			m.Route("/invitations/new", "GET,POST", org.Invitation)
		}, middleware.OrgAssignment(true, true, true))
	}, reqSignIn)
	m.Group("/org", func() {
		m.Get("/:org", org.Home)
	}, middleware.OrgAssignment(true))

	// Repository.
	m.Group("/repo", func() {
		m.Get("/forks/:id", repo.ForkProgress)
		m.Get("/forks/:id/status", repo.ForkStatus)
		m.Get("/create", repo.Create)
		m.Post("/create", bindIgnErr(auth.CreateRepoForm{}), repo.CreatePost)

	}, reqSignIn)

	m.Group("/:username/:reponame", func() {
		m.Get("/settings", repo.Settings)
		m.Post("/settings", bindIgnErr(auth.RepoSettingForm{}), repo.SettingsPost)
		m.Group("/settings", func() {
			m.Route("/collaboration", "GET,POST", repo.SettingsCollaboration)
		})
	}, reqSignIn, middleware.RepoAssignment(true), reqTrueOwner)

	m.Group("/:username/:reponame/pulls", func() {
		m.Get("/new", reqSignIn, repo.PullNew)
		m.Post("/new", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullNewPost)
		m.Get("/:number/operations/:operation", repo.PullOperation)
		m.Get("/:number/comparisons/:comparison/files/*", repo.PullDownload)
		m.Get("/:number/comparisons/:comparison/files/", repo.PullDownload)
		m.Post("/:number/edit", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullEdit)
		m.Post("/:number/state", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullState)
		m.Post("/:number/refresh", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullRefresh)
		m.Post("/:number/reviewers", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullReviewers)
		m.Post("/:number/review", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullReview)
		m.Post("/:number/review/dismiss", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullDismiss)
		m.Post("/:number/comments", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullComment)
		m.Post("/:number/threads/:thread/resolve", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullResolve)
		m.Post("/:number/merge", reqSignIn, protectedPOST, bindIgnErr(repo.PullForm{}), repo.PullMerge)
		m.Get("/:number", repo.PullView)
		m.Get("", repo.Pulls)
	}, ignSignIn, middleware.RepoAssignment(true), repo.PullRequestLimits)

	m.Group("/:username/:reponame", func() {
		m.Get("/fork", repo.Fork)
		m.Post("/fork", bindIgnErr(auth.CreateRepoForm{}), repo.ForkPost)
		m.Post("/action/:action", repo.Action)

		m.Group("/issues", func() {
			m.Get("/new", repo.CreateIssue)
			m.Post("/new", bindIgnErr(auth.CreateIssueForm{}), repo.CreateIssuePost)
			m.Post("/:index", bindIgnErr(auth.CreateIssueForm{}), repo.UpdateIssue)
			m.Post("/:index/label", repo.UpdateIssueLabel)
			m.Post("/:index/milestone", repo.UpdateIssueMilestone)
			m.Post("/:index/assignee", repo.UpdateAssignee)
			m.Get("/:index/attachment/:id", repo.IssueGetAttachment)
			m.Post("/labels/new", bindIgnErr(auth.CreateLabelForm{}), repo.NewLabel)
			m.Post("/labels/edit", bindIgnErr(auth.CreateLabelForm{}), repo.UpdateLabel)
			m.Post("/labels/delete", repo.DeleteLabel)
			m.Get("/milestones/new", repo.NewMilestone)
			m.Post("/milestones/new", bindIgnErr(auth.CreateMilestoneForm{}), repo.NewMilestonePost)
			m.Get("/milestones/:index/edit", repo.UpdateMilestone)
			m.Post("/milestones/:index/edit", bindIgnErr(auth.CreateMilestoneForm{}), repo.UpdateMilestonePost)
			m.Post("/milestones/:index/:action", repo.UpdateMilestone)
		})

		m.Post("/comment/:action", repo.Comment)

	}, reqSignIn, middleware.RepoAssignment(true))

	m.Group("/:username/:reponame", func() {

		m.Get("/issues", repo.Issues)
		m.Get("/issues/:index", repo.ViewIssue)
		m.Get("/issues/milestones", repo.Milestones)

		m.Group("", func() {
			m.Get("/branches", repo.Branches)
			m.Get("/commits", repo.Commits)
			m.Get("/commits/*", repo.Commits)
			m.Get("/src/:node", repo.PrepareRepositoryUI("source"), middleware.RepoRef("src"), repo.Home)
			m.Get("/src/:node/*", repo.PrepareRepositoryUI("source"), middleware.RepoRef("src"), repo.Home)
			m.Get("/raw/:node/*", middleware.RepoRef("raw"), repo.SingleDownload)
			m.Get("/raw/:node", middleware.RepoRef("raw"), repo.SingleDownload)
			m.Get("/commit/:node", repo.PrepareRepositoryUI("changesets"), middleware.RepoRef("commit"), repo.Diff)
		}, middleware.RepoBrowse())

	}, ignSignIn, middleware.RepoAssignment(true))

	m.Group("/:username", func() {
		m.Get("/:reponame", ignSignIn, middleware.RepoAssignment(true), middleware.RepoBrowse(), repo.PrepareRepositoryUI("source"), middleware.RepoRef("src"), repo.Home)
	})

	// robots.txt
	m.Get("/robots.txt", func(ctx *middleware.Context) {
		if setting.HasRobotsTxt {
			ctx.ServeFile(path.Join(setting.CustomPath, "robots.txt"))
		} else {
			ctx.Error(404)
		}
	})

	// Not found handler.
	m.NotFound(routers.NotFound)

	return holder.Handler(m)
}
