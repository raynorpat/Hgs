package models

import (
	"database/sql"
	"errors"

	"github.com/go-xorm/xorm"
)

// HostingFormat is the explicit fresh Mercurial database generation marker.
// A populated unmarked database is never migrated by startup.
type HostingFormat struct {
	Id      int64
	Version int
}

func checkHostingFormat(e *xorm.Engine) error {
	metas, err := e.DBMetas()
	if err != nil {
		return err
	}
	populated := false
	columns := map[string]map[string]bool{}
	for _, table := range metas {
		columns[table.Name] = map[string]bool{}
		for _, c := range table.Columns() {
			columns[table.Name][c.Name] = true
		}
		var one int
		err = e.DB().QueryRow("SELECT 1 FROM " + e.Quote(table.Name) + " LIMIT 1").Scan(&one)
		if err == nil {
			populated = true
		} else if err != sql.ErrNoRows {
			return err
		}
	}
	if !populated {
		return nil
	}
	reject := errors.New("hosting: populated legacy or ambiguous database; a fresh Mercurial installation is required")
	for table, required := range map[string][]string{"hosting_format": {"id", "version"}, "repository": {"id", "owner_id", "state", "storage_nonce"}, "access": {"user_id", "repo_id", "mode"}, "public_key": {"owner_id", "fingerprint", "content"}} {
		for _, column := range required {
			if !columns[table][column] {
				return reject
			}
		}
	}
	var marker HostingFormat
	has, err := e.Id(1).Get(&marker)
	if err != nil {
		return err
	}
	if !has || marker.Version != 1 {
		return reject
	}
	count, err := e.Count(new(HostingFormat))
	if err != nil {
		return err
	}
	if count != 1 {
		return reject
	}
	count, err = e.Where("state NOT IN (?, ?, ?)", RepositoryReady, RepositoryProvisioning, RepositoryDeleting).Count(new(Repository))
	if err != nil {
		return err
	}
	if count != 0 {
		return reject
	}
	return nil
}
