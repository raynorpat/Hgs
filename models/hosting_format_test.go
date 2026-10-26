package models

import (
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/modules/setting"
)

func TestHostingSchemaRejectedBeforeSync(t *testing.T) {
	oldDb, oldX := DbCfg, x
	defer func() { DbCfg, x = oldDb, oldX }()
	DbCfg.Type = "sqlite3"
	DbCfg.Path = filepath.ToSlash(filepath.Join(t.TempDir(), "legacy.db"))
	e, err := getEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err = e.Exec("CREATE TABLE repository (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Exec("INSERT INTO repository VALUES (1, 'legacy')"); err != nil {
		t.Fatal(err)
	}
	oldLog := setting.LogRootPath
	setting.LogRootPath = t.TempDir()
	t.Cleanup(func() { setting.LogRootPath = oldLog })
	if err = NewEngine(); err == nil {
		t.Fatal("accepted legacy database")
	}
	e.Close()
	e, err = getEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	metas, err := e.DBMetas()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || len(metas[0].Columns()) != 2 {
		t.Fatal("schema changed before rejection")
	}
}
func TestHostingSchemaFreshAndRestart(t *testing.T) {
	oldDb, oldX, oldLog := DbCfg, x, setting.LogRootPath
	defer func() {
		if x != nil && x != oldX {
			CloseEngine()
		}
		DbCfg, x, setting.LogRootPath = oldDb, oldX, oldLog
	}()
	DbCfg.Type = "sqlite3"
	DbCfg.Path = filepath.ToSlash(filepath.Join(t.TempDir(), "fresh.db"))
	setting.LogRootPath = t.TempDir()
	if err := NewEngine(); err != nil {
		t.Fatal(err)
	}
	CloseEngine()
	if err := NewEngine(); err != nil {
		t.Fatal(err)
	}
}
