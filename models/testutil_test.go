package models

import (
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/modules/setting"
)

// newTestDatabase points package x at a fresh temporary SQLite database and
// restores the globals on cleanup. The returned function closes and reopens
// the database file, simulating a restart. Tests using it must not be parallel.
func newTestDatabase(t *testing.T) (reload func()) {
	// Panics if the test called t.Parallel, since x is shared.
	t.Setenv("GOGS_TEST_DATABASE", "1")

	oldX, oldDb, oldRoot, oldHas := x, DbCfg, setting.RepoRootPath, HasEngine
	DbCfg.Type = "sqlite3"
	DbCfg.Path = filepath.ToSlash(filepath.Join(t.TempDir(), "gogs.db"))
	setting.RepoRootPath = filepath.Join(t.TempDir(), "repos")

	open := func() {
		e, err := getEngine()
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Sync2(tables...); err != nil {
			e.Close()
			t.Fatal(err)
		}
		x = e
	}
	open()
	HasEngine = true

	t.Cleanup(func() {
		x.Close()
		x, DbCfg, setting.RepoRootPath, HasEngine = oldX, oldDb, oldRoot, oldHas
	})
	return func() {
		if err := x.Close(); err != nil {
			t.Fatal(err)
		}
		open()
	}
}
