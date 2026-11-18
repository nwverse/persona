package db

import (
	"context"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
)

// timestampedProbes are timestamped migrations as later lines of work add them, by path under migrations/. The
// manager's needs the table its last numbered migration (0561) makes, so it runs only after the numbered ones; neither
// can run twice.
var timestampedProbes = map[string]string{
	"manager/202611191230_probe.sql": `ALTER TABLE model_usage ADD COLUMN probe int`,
	"hub/202611191231_probe.sql":     `CREATE TABLE timestamp_probe (id int)`,
}

// withMigrations are the embedded migrations and extra ones (path under migrations/: SQL): a build that has them.
func withMigrations(t *testing.T, extra map[string]string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for part, names := range setFiles(t) {
		for _, name := range names {
			b, err := fs.ReadFile(migrations, "migrations/"+part+"/"+name)
			if err != nil {
				t.Fatal(err)
			}
			out["migrations/"+part+"/"+name] = &fstest.MapFile{Data: b}
		}
	}
	for path, sql := range extra {
		out["migrations/"+path] = &fstest.MapFile{Data: []byte(sql)}
	}
	return out
}

// versionType is schema_migrations.version's type.
func versionType(t *testing.T, d *DB) string {
	t.Helper()
	var typ string
	if err := d.pool.QueryRow(context.Background(), `SELECT format_type(atttypid, atttypmod) FROM pg_attribute
		WHERE attrelid = 'schema_migrations'::regclass AND attname = 'version'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	return typ
}

// stamped are the versions of schema_migrations' timestamped rows, in order.
func stamped(t *testing.T, d *DB) []int64 {
	t.Helper()
	var out []int64
	if err := d.pool.QueryRow(context.Background(), `SELECT coalesce(array_agg(version ORDER BY version), '{}')
		FROM schema_migrations WHERE version > $1`, lastNumbered).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A database of each kind (a hub's own, a manager's own, one they share) that the numbered migrations made, its
// version an int as every build before timestamps left it: the next open widens the column to bigint and keeps the
// rows; the timestamped migrations run after the numbered ones, each once, recorded under their minute; and opening
// again only reads. A set with two files of one version is refused, not half skipped.
func TestMigrateTimestampedAfterNumbered(t *testing.T) {
	ctx := context.Background()
	built := withMigrations(t, timestampedProbes)
	for _, c := range []struct {
		name    string
		sets    Sets
		records []string
		stamped []int64
	}{
		{"hub", HubSet, []string{"202611191231_probe.sql hub"}, []int64{202611191231}},
		{"manager", ManagerSet, []string{"202611191230_probe.sql manager"}, []int64{202611191230}},
		{"shared", AllSets, []string{"202611191230_probe.sql manager", "202611191231_probe.sql hub"},
			[]int64{202611191230, 202611191231}},
	} {
		t.Run(c.name, func(t *testing.T) {
			url := freshDatabase(t)
			older, err := Open(ctx, url, c.sets)
			if err != nil {
				t.Fatal(err)
			}
			if typ := versionType(t, older); typ != "bigint" {
				t.Fatalf("a new database's version is %s", typ)
			}
			want := append(records(t, older), c.records...)
			sort.Strings(want)
			_, err = older.pool.Exec(ctx, `ALTER TABLE schema_migrations ALTER COLUMN version TYPE int`)
			// Its connections keep reads of schema_migrations prepared, which Postgres refuses to run once the column
			// changes type: a pool that reads it before and after the widening is a test's only.
			older.Close()
			if err != nil {
				t.Fatal(err)
			}
			d, err := Attach(ctx, url, c.sets)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if err := d.migrateFrom(ctx, built); err != nil {
				t.Fatal(err)
			}
			if typ := versionType(t, d); typ != "bigint" {
				t.Fatalf("version is %s after the upgrade", typ)
			}
			if got := records(t, d); !slices.Equal(got, want) {
				t.Fatalf("records %v, want %v", got, want)
			}
			if got := stamped(t, d); !slices.Equal(got, c.stamped) {
				t.Fatalf("timestamped versions %v, want %v", got, c.stamped)
			}
			traced, log := tracedDB(t, url)
			traced.sets = c.sets
			for range 2 {
				if err := traced.migrateFrom(ctx, built); err != nil {
					t.Fatalf("again: %v", err)
				}
				if got := log.take(); len(got) > 4 {
					t.Fatalf("migrating up to date took %d statements: %v", len(got), got)
				}
			}
			if got := records(t, d); !slices.Equal(got, want) {
				t.Fatalf("records after opening again %v, want %v", got, want)
			}
			if c.sets != AllSets {
				return
			}
			twice := withMigrations(t, map[string]string{
				"manager/202611191230_probe.sql": timestampedProbes["manager/202611191230_probe.sql"],
				"manager/202611191230_other.sql": `CREATE TABLE timestamp_other (id int)`,
			})
			if err := d.migrateFrom(ctx, twice); err == nil || !strings.Contains(err.Error(), "same version 202611191230") {
				t.Fatalf("two migrations of one minute: %v", err)
			}
			if slices.Contains(tablesOf(t, d), "timestamp_other") {
				t.Fatal("the second migration of a minute ran")
			}
		})
	}
	// On a new database the timestamped migrations follow the numbered ones they build on.
	d, err := Attach(ctx, freshDatabase(t), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.migrateFrom(ctx, built); err != nil {
		t.Fatal(err)
	}
	if got := stamped(t, d); !slices.Equal(got, []int64{202611191230, 202611191231}) {
		t.Fatalf("timestamped versions %v", got)
	}
}

// previousSchemaMigrationsSplit is the catalog check of the builds from before timestamps (v0.8.2 and earlier back to
// the split): the same as schemaMigrationsSplit, without the version's type.
const previousSchemaMigrationsSplit = `SELECT coalesce((
	SELECT c.relkind = 'r' AND n.nspname = current_schema()
		AND EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'part' AND a.attnum > 0 AND NOT a.attisdropped)
		AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conrelid = c.oid AND k.conname = 'schema_migrations_pkey')
		AND EXISTS (SELECT 1 FROM pg_class i WHERE i.relnamespace = c.relnamespace AND i.relname = 'schema_migrations_version_part')
	FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = to_regclass('schema_migrations')), false)`

// A build from before timestamps, switched back to (a failed take-over, `personad hub switch --prev`, an update rolled
// back) on a database this one has migrated further: its catalog check finds the table as it left it, so it changes
// nothing there; it reads the bigint versions, the timestamped ones too, into a Go int as it always did; and every
// migration it has is done, so it applies and records nothing. Forward again, nothing runs twice.
func TestOlderBuildAfterTimestampedMigrations(t *testing.T) {
	ctx := context.Background()
	url := migratedDatabase(t, AllSets)
	d, err := Attach(ctx, url, AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	built := withMigrations(t, timestampedProbes)
	if err := d.migrateFrom(ctx, built); err != nil {
		t.Fatal(err)
	}
	want := records(t, d)
	var split bool
	if err := d.pool.QueryRow(ctx, previousSchemaMigrationsSplit).Scan(&split); err != nil || !split {
		t.Fatalf("an older build's catalog check: %v %v", split, err)
	}
	rows, err := d.pool.Query(ctx, `SELECT version, name, part FROM schema_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	var version int // what readSchemaRecords scanned into before timestamps
	var name string
	var part *string
	seen := 0
	if _, err := pgx.ForEachRow(rows, []any{&version, &name, &part}, func() error {
		if strings.HasPrefix(name, "2026") && int64(version) >= firstStamp {
			seen++
		}
		return nil
	}); err != nil || seen != len(timestampedProbes) {
		t.Fatalf("an older build's read: %d timestamped rows, %v", seen, err)
	}
	// The older build's own migrations: the embedded ones, without the timestamped files.
	traced, log := tracedDB(t, url)
	traced.sets = AllSets
	if err := traced.migrateFrom(ctx, migrations); err != nil {
		t.Fatal(err)
	}
	if got := log.take(); len(got) > 4 {
		t.Fatalf("the older build's open took %d statements: %v", len(got), got)
	}
	if err := traced.migrateFrom(ctx, built); err != nil {
		t.Fatal(err)
	}
	if got := records(t, d); !slices.Equal(got, want) {
		t.Fatalf("records %v, want %v", got, want)
	}
}
