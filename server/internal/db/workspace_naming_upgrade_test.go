package db

import (
	"slices"
	"testing"
)

func TestWorkspaceNamingUpgradeRepositoryMigrationCollision(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(map[bool]string{false: "repository predecessor", true: "both records"}[both], func(t *testing.T) {
			ctx := t.Context()
			url := freshDatabase(t)
			d, err := Open(ctx, url, ManagerSet)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			want := records(t, d)
			if _, err = d.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE name='0513_assistant_search_plain.sql'`); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE schema_migrations SET version=513,name='0513_todo_repositories.sql' WHERE name='0516_todo_repositories.sql'`
			if both {
				query = `INSERT INTO schema_migrations(version,name,part) VALUES(513,'0513_todo_repositories.sql','manager')`
			}
			if _, err = d.pool.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
			if err = d.migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if got := records(t, d); !slices.Equal(got, want) {
				t.Fatal(got, want)
			}
		})
	}
}
