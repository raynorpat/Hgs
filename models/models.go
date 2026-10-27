// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/go-xorm/xorm"
	_ "github.com/lib/pq"
	"modernc.org/sqlite"

	// "github.com/gogits/gogs/models/migrations"
	"github.com/gogits/gogs/modules/setting"
)

// Engine represents a xorm engine or session.
type Engine interface {
	Delete(interface{}) (int64, error)
	Exec(string, ...interface{}) (sql.Result, error)
	Get(interface{}) (bool, error)
	Insert(...interface{}) (int64, error)
	Id(interface{}) *xorm.Session
	Where(string, ...interface{}) *xorm.Session
}

var (
	x         *xorm.Engine
	tables    []interface{}
	HasEngine bool
	xormLog   *os.File

	DbCfg struct {
		Type, Host, Name, User, Passwd, Path, SSLMode string
	}

	UseSQLite3 bool
)

func init() {
	// xorm opens SQLite by the driver name "sqlite3"; modernc registers itself as "sqlite".
	sql.Register("sqlite3", &sqlite.Driver{})

	tables = append(tables,
		new(User), new(PublicKey), new(Follow), new(Oauth2), new(AccessToken),
		new(Repository), new(Watch), new(Star), new(Action), new(Access),
		new(Issue), new(Comment), new(Attachment), new(IssueUser), new(Label), new(Milestone),
		new(LoginSource),
		new(Team), new(OrgUser), new(TeamUser),
		new(Notice), new(EmailAddress), new(HostingFormat), new(ReviewPublication), new(ReviewReservation))
}

func LoadModelsConfig() {
	sec := setting.Cfg.Section("database")
	DbCfg.Type = sec.Key("DB_TYPE").String()
	if DbCfg.Type == "sqlite3" {
		UseSQLite3 = true
	}
	DbCfg.Host = sec.Key("HOST").String()
	DbCfg.Name = sec.Key("NAME").String()
	DbCfg.User = sec.Key("USER").String()
	if len(DbCfg.Passwd) == 0 {
		DbCfg.Passwd = sec.Key("PASSWD").String()
	}
	DbCfg.SSLMode = sec.Key("SSL_MODE").String()
	DbCfg.Path = sec.Key("PATH").MustString("data/gogs.db")
}

func getEngine() (*xorm.Engine, error) {
	cnnstr := ""
	switch DbCfg.Type {
	case "mysql":
		cnnstr = fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8",
			DbCfg.User, DbCfg.Passwd, DbCfg.Host, DbCfg.Name)
	case "postgres":
		var host, port = "127.0.0.1", "5432"
		fields := strings.Split(DbCfg.Host, ":")
		if len(fields) > 0 && len(strings.TrimSpace(fields[0])) > 0 {
			host = fields[0]
		}
		if len(fields) > 1 && len(strings.TrimSpace(fields[1])) > 0 {
			port = fields[1]
		}
		cnnstr = fmt.Sprintf("user=%s password=%s host=%s port=%s dbname=%s sslmode=%s",
			DbCfg.User, DbCfg.Passwd, host, port, DbCfg.Name, DbCfg.SSLMode)
	case "sqlite3":
		os.MkdirAll(path.Dir(DbCfg.Path), os.ModePerm)
		cnnstr = "file:" + DbCfg.Path + "?cache=shared&mode=rwc"
	default:
		return nil, fmt.Errorf("Unknown database type: %s", DbCfg.Type)
	}
	return xorm.NewEngine(DbCfg.Type, cnnstr)
}

func SetEngine() (err error) {
	e, err := getEngine()
	if err != nil {
		return fmt.Errorf("connect to database: %v", err)
	}

	if err := setEngine(e); err != nil {
		e.Close()
		return err
	}
	return nil
}

func setEngine(e *xorm.Engine) error {
	// Keep database diagnostics in the configured log directory.
	logPath := path.Join(setting.LogRootPath, "xorm.log")
	os.MkdirAll(path.Dir(logPath), os.ModePerm)

	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("models.init(fail to create xorm.log): %v", err)
	}
	if xormLog != nil {
		xormLog.Close()
	}
	xormLog = f
	x = e
	x.Logger = xorm.NewSimpleLogger(f)

	x.ShowSQL = true
	x.ShowInfo = true
	x.ShowDebug = true
	x.ShowErr = true
	x.ShowWarn = true
	return nil
}

func NewEngine() (err error) {
	e, err := getEngine()
	if err != nil {
		return err
	}
	if err = checkHostingFormat(e); err != nil {
		e.Close()
		return err
	}
	if err = setEngine(e); err != nil {
		e.Close()
		return err
	}

	defer func() {
		if err != nil {
			CloseEngine()
		}
	}()

	// if err = migrations.Migrate(x); err != nil {
	// 	return err
	// }

	if err = x.StoreEngine("InnoDB").Sync2(tables...); err != nil {
		return fmt.Errorf("sync database struct error: %v\n", err)
	}
	marker := &HostingFormat{Id: 1, Version: 1}
	if has, getErr := x.Id(1).Get(new(HostingFormat)); getErr != nil {
		return getErr
	} else if !has {
		if _, err = x.Insert(marker); err != nil {
			return err
		}
	}
	return nil
}

type Statistic struct {
	Counter struct {
		User, Org, PublicKey,
		Repo, Watch, Star, Action, Access,
		Issue, Comment, Oauth, Follow,
		LoginSource,
		Milestone, Label,
		Team, Attachment int64
	}
}

func GetStatistic() (stats Statistic) {
	stats.Counter.User = CountUsers()
	stats.Counter.Org = CountOrganizations()
	stats.Counter.PublicKey, _ = x.Count(new(PublicKey))
	stats.Counter.Repo = CountRepositories()
	stats.Counter.Watch, _ = x.Count(new(Watch))
	stats.Counter.Star, _ = x.Count(new(Star))
	stats.Counter.Action, _ = x.Count(new(Action))
	stats.Counter.Access, _ = x.Count(new(Access))
	stats.Counter.Issue, _ = x.Count(new(Issue))
	stats.Counter.Comment, _ = x.Count(new(Comment))
	stats.Counter.Oauth, _ = x.Count(new(Oauth2))
	stats.Counter.Follow, _ = x.Count(new(Follow))
	stats.Counter.LoginSource, _ = x.Count(new(LoginSource))
	stats.Counter.Milestone, _ = x.Count(new(Milestone))
	stats.Counter.Label, _ = x.Count(new(Label))
	stats.Counter.Team, _ = x.Count(new(Team))
	stats.Counter.Attachment, _ = x.Count(new(Attachment))
	return
}

func Ping() error {
	return x.Ping()
}

// DumpDatabase dumps all data from database to file system.
func DumpDatabase(filePath string) error {
	return x.DumpAllToFile(filePath)
}

// CloseEngine releases the process-owned database and its log.
func CloseEngine() error {
	HasEngine = false
	if xormLog != nil {
		xormLog.Close()
		xormLog = nil
	}
	if x != nil {
		return x.Close()
	}
	return nil
}
