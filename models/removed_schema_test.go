package models

import "testing"

func TestFreshSchemaOmitsDeferredFeatures(t *testing.T) {
	newTestDatabase(t)
	tables, err := x.DBMetas()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		switch table.Name {
		case "mirror", "release", "update_task", "webhook", "hook_task":
			t.Errorf("fresh schema contains deferred table %s", table.Name)
		}
		for _, column := range table.Columns() {
			if table.Name == "repository" {
				switch column.Name {
				case "is_bare", "is_fork", "fork_id", "is_mirror", "is_goget", "num_forks", "num_pulls", "num_closed_pulls", "default_branch":
					t.Errorf("fresh repository schema contains deferred column %s", column.Name)
				}
			}
			if table.Name == "user" && column.Name == "allow_git_hook" {
				t.Error("fresh user schema permits editing Git hooks")
			}
			if table.Name == "comment" && (column.Name == "commit_id" || column.Name == "line") {
				t.Errorf("fresh comment schema contains Git column %s", column.Name)
			}
		}
	}
}
