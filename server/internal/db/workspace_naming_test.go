package db

import (
	"slices"
	"testing"
)

func TestWorkspaceNamingUpgradeRepositoryMigrationCollision(t *testing.T) {
	for _, old := range []struct {
		version int
		name    string
		search  string
	}{
		{513, "0513_todo_repositories.sql", "0513_assistant_search_plain.sql"},
		{514, "0514_todo_repositories.sql", "0514_assistant_search_strong.sql"},
	} {
		for _, both := range []bool{false, true} {
			t.Run(old.name+map[bool]string{false: "/predecessor", true: "/both records"}[both], func(t *testing.T) {
				ctx := t.Context()
				url := freshDatabase(t)
				d, err := Open(ctx, url, ManagerSet)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				want := records(t, d)
				if _, err = d.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE name=$1`, old.search); err != nil {
					t.Fatal(err)
				}
				query := `UPDATE schema_migrations SET version=$1,name=$2 WHERE name='0516_todo_repositories.sql'`
				if both {
					query = `INSERT INTO schema_migrations(version,name,part) VALUES($1,$2,'manager')`
				}
				if _, err = d.pool.Exec(ctx, query, old.version, old.name); err != nil {
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
}
