// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package routers

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Unknwon/com"
	"github.com/Unknwon/macaron"
	"gopkg.in/ini.v1"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/social"
)

const (
	INSTALL base.TplName = "install"
)

func checkRunMode() {
	switch setting.Cfg.Section("").Key("RUN_MODE").String() {
	case "prod":
		macaron.Env = macaron.PROD
		setting.ProdMode = true
	case "test":
		macaron.Env = macaron.TEST
	}
	log.Info("Run Mode: %s", strings.Title(macaron.Env))
}

func NewServices() {
	setting.NewServices()
	social.NewOauthService()
}

// GlobalInit is for global configuration reload-able.
func GlobalInit() error {
	setting.NewConfigContext()
	return InitServices()
}

// InitServices follows the configuration-only startup maintenance decision.
func InitServices() error {
	log.Trace("Custom path: %s", setting.CustomPath)
	log.Trace("Log path: %s", setting.LogRootPath)
	mailer.NewMailerContext()
	models.LoadModelsConfig()
	NewServices()

	if setting.InstallLock {

		if err := models.NewEngine(); err != nil {
			return err
		}

		models.HasEngine = true

	}
	checkRunMode()
	return nil
}

func InstallInit(ctx *middleware.Context) {
	if setting.InstallLock {
		ctx.Handle(404, "Install", errors.New("Installation is prohibited"))
		return
	}

	ctx.Data["Title"] = ctx.Tr("install.install")
	ctx.Data["PageIsInstall"] = true

	ctx.Data["DbOptions"] = []string{"MySQL", "PostgreSQL", "SQLite3"}
}

func Install(ctx *middleware.Context) {
	form := auth.InstallForm{}

	form.DbHost = models.DbCfg.Host
	form.DbUser = models.DbCfg.User
	form.DbName = models.DbCfg.Name
	form.DbPath = models.DbCfg.Path

	form.RepoRootPath = setting.RepoRootPath

	// Note(unknwon): it's hard for Windows users change a running user,
	// 	so just use current one if config says default.
	if setting.IsWindows && setting.RunUser == "git" {
		form.RunUser = os.Getenv("USER")
		if len(form.RunUser) == 0 {
			form.RunUser = os.Getenv("USERNAME")
		}
	} else {
		form.RunUser = setting.RunUser
	}

	form.Domain = setting.Domain
	form.HTTPPort = setting.HttpPort
	form.AppUrl = setting.AppUrl

	ctx.Data["CurDbOption"] = map[string]string{"mysql": "MySQL", "postgres": "PostgreSQL", "sqlite3": "SQLite3"}[models.DbCfg.Type]
	if ctx.Data["CurDbOption"] == "" {
		ctx.Data["CurDbOption"] = "SQLite3"
	}
	form.SSLMode = models.DbCfg.SSLMode

	auth.AssignForm(form, ctx.Data)
	ctx.HTML(200, INSTALL)
}

type InitializeHosting func() error

func InstallPost(ctx *middleware.Context, form auth.InstallForm, initialize InitializeHosting) {
	ctx.Data["CurDbOption"] = form.DbType

	if ctx.HasError() {
		ctx.HTML(200, INSTALL)
		return
	}

	// Validate all local settings before opening a database or persisting setup.
	invalid := func(field, message string) {
		ctx.Data["Err_"+field] = true
		ctx.RenderWithErr(ctx.Tr(message), INSTALL, &form)
	}
	if form.DbType != "MySQL" && form.DbType != "PostgreSQL" && form.DbType != "SQLite3" {
		invalid("DbType", "ui.invalid_database")
		return
	}
	if form.DbType == "PostgreSQL" && form.SSLMode != "disable" && form.SSLMode != "require" && form.SSLMode != "verify-full" {
		invalid("SSLMode", "ui.invalid_database")
		return
	}
	if form.DbType == "SQLite3" && strings.TrimSpace(form.DbPath) == "" {
		invalid("DbPath", "ui.invalid_database")
		return
	}
	if !models.IsLegalName(form.AdminName) {
		invalid("AdminName", "form.illegal_username")
		return
	}
	if form.AdminPasswd != form.AdminConfirmPasswd {
		invalid("AdminPasswd", "form.password_not_match")
		return
	}
	parsedURL, parseErr := url.Parse(form.AppUrl)
	if parseErr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		invalid("AppUrl", "ui.invalid_service_url")
		return
	}
	port, portErr := strconv.Atoi(form.HTTPPort)
	if portErr != nil || port < 1 || port > 65535 {
		invalid("HTTPPort", "ui.invalid_http_port")
		return
	}
	// Pass basic check, now test configuration.
	// Test database setting.
	dbTypes := map[string]string{"MySQL": "mysql", "PostgreSQL": "postgres", "SQLite3": "sqlite3"}
	models.DbCfg.Type = dbTypes[form.DbType]
	models.DbCfg.Host = form.DbHost
	models.DbCfg.User = form.DbUser
	models.DbCfg.Passwd = form.DbPasswd
	models.DbCfg.Name = form.DbName
	models.DbCfg.SSLMode = form.SSLMode
	models.DbCfg.Path = form.DbPath

	// Test the database without changing it; there is no migration from an
	// existing installation.
	if err := models.CheckFreshInstall(form.RepoRootPath); errors.Is(err, models.ErrNotFreshInstall) {
		ctx.RenderWithErr(err.Error(), INSTALL, &form)
		return
	} else if err != nil {
		ctx.RenderWithErr(ctx.Tr("ui.invalid_database"), INSTALL, &form)
		return
	}

	// Test repository root path.
	err := models.ValidateRepositoryRoot(form.RepoRootPath)
	if err == nil {
		err = os.MkdirAll(form.RepoRootPath, os.ModePerm)
	}
	if err != nil {
		ctx.Data["Err_RepoRootPath"] = true
		ctx.RenderWithErr(ctx.Tr("install.invalid_repo_path", err), INSTALL, &form)
		return
	}

	// Check run user.
	curUser := os.Getenv("USER")
	if len(curUser) == 0 {
		curUser = os.Getenv("USERNAME")
	}
	if form.RunUser != curUser {
		ctx.Data["Err_RunUser"] = true
		ctx.RenderWithErr(ctx.Tr("install.run_user_not_match", form.RunUser, curUser), INSTALL, &form)
		return
	}

	// Check admin password.
	if form.AdminPasswd != form.AdminConfirmPasswd {
		ctx.Data["Err_AdminPasswd"] = true
		ctx.RenderWithErr(ctx.Tr("form.password_not_match"), INSTALL, form)
		return
	}

	if form.AppUrl[len(form.AppUrl)-1] != '/' {
		form.AppUrl += "/"
	}

	// Save settings.
	cfg := ini.Empty()
	if com.IsFile(setting.CustomConf) {
		// Keeps custom settings if there is already something.
		if err := cfg.Append(setting.CustomConf); err != nil {
			log.Error(4, "Fail to load custom conf '%s': %v", setting.CustomConf, err)
		}
	}
	cfg.Section("database").Key("DB_TYPE").SetValue(models.DbCfg.Type)
	cfg.Section("database").Key("HOST").SetValue(models.DbCfg.Host)
	cfg.Section("database").Key("NAME").SetValue(models.DbCfg.Name)
	cfg.Section("database").Key("USER").SetValue(models.DbCfg.User)
	cfg.Section("database").Key("PASSWD").SetValue(models.DbCfg.Passwd)
	cfg.Section("database").Key("SSL_MODE").SetValue(models.DbCfg.SSLMode)
	cfg.Section("database").Key("PATH").SetValue(models.DbCfg.Path)

	cfg.Section("repository").Key("ROOT").SetValue(form.RepoRootPath)
	cfg.Section("").Key("RUN_USER").SetValue(form.RunUser)
	cfg.Section("server").Key("DOMAIN").SetValue(form.Domain)
	cfg.Section("server").Key("HTTP_PORT").SetValue(form.HTTPPort)
	cfg.Section("server").Key("ROOT_URL").SetValue(form.AppUrl)

	if len(strings.TrimSpace(form.SMTPHost)) > 0 {
		cfg.Section("mailer").Key("ENABLED").SetValue("true")
		cfg.Section("mailer").Key("HOST").SetValue(form.SMTPHost)
		cfg.Section("mailer").Key("USER").SetValue(form.SMTPEmail)
		cfg.Section("mailer").Key("PASSWD").SetValue(form.SMTPPasswd)

		cfg.Section("service").Key("REGISTER_EMAIL_CONFIRM").SetValue(com.ToStr(form.RegisterConfirm == "on"))
		cfg.Section("service").Key("ENABLE_NOTIFY_MAIL").SetValue(com.ToStr(form.MailNotify == "on"))
	}

	cfg.Section("").Key("RUN_MODE").SetValue("prod")

	cfg.Section("session").Key("PROVIDER").SetValue("file")
	if setting.SessionCookieSecure(form.AppUrl, false) {
		cfg.Section("session").Key("COOKIE_SECURE").SetValue("true")
	}

	cfg.Section("log").Key("MODE").SetValue("file")

	cfg.Section("security").Key("INSTALL_LOCK").SetValue("true")
	cfg.Section("security").Key("SECRET_KEY").SetValue(base.GetRandomString(15))

	os.MkdirAll(filepath.Dir(setting.CustomConf), os.ModePerm)
	if err := cfg.SaveTo(setting.CustomConf); err != nil {
		ctx.RenderWithErr(ctx.Tr("install.save_config_failed", err), INSTALL, &form)
		return
	}

	if err := GlobalInit(); err != nil {
		ctx.Handle(500, "Initialize database", err)
		return
	}

	// Create admin account.
	if err := models.CreateUser(&models.User{Name: form.AdminName, Email: form.AdminEmail, Passwd: form.AdminPasswd,
		IsAdmin: true, IsActive: true}); err != nil {
		if err != models.ErrUserAlreadyExist {
			setting.InstallLock = false
			ctx.Data["Err_AdminName"] = true
			ctx.Data["Err_AdminEmail"] = true
			ctx.RenderWithErr(ctx.Tr("install.invalid_admin_setting", err), INSTALL, &form)
			return
		}
		log.Info("Admin account already exist")
	}

	if err := initialize(); err != nil {
		ctx.Handle(500, "Initialize Mercurial hosting", err)
		return
	}

	log.Info("First-time run install finished!")
	ctx.Flash.Success(ctx.Tr("install.install_success"))
	ctx.Redirect(form.AppUrl + "user/login")
}
