package models

import (
	"path/filepath"
	"testing"
)

func TestSQLiteEngineRoundTrip(t *testing.T) {
	DbCfg.Type = "sqlite3"
	DbCfg.Path = filepath.ToSlash(filepath.Join(t.TempDir(), "gogs.db"))

	e, err := getEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.Sync2(tables...); err != nil {
		t.Fatal(err)
	}

	if _, err = e.Insert(&User{Name: "alice", LowerName: "alice", Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	u := &User{LowerName: "alice"}
	if has, err := e.Get(u); err != nil || !has || u.Id == 0 {
		t.Fatalf("reload user: has=%v id=%d err=%v", has, u.Id, err)
	}
}
