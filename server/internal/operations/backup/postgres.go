package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
)

type postgresBackend struct {
	lookPath func(string) (string, error)
}

func newPostgresBackend() *postgresBackend {
	return &postgresBackend{lookPath: exec.LookPath}
}

func (b *postgresBackend) connect(ctx context.Context, target Target, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(target.URL)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	if database != "" {
		cfg.Database = database
	}
	cfg.RuntimeParams["application_name"] = "personad-backup"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, targetError(target, "connect to the database", err)
	}
	return conn, nil
}

func (b *postgresBackend) admin(ctx context.Context, target Target, avoid ...string) (*pgx.Conn, error) {
	var last error
	for _, database := range []string{"postgres", "template1"} {
		var skip bool
		for _, name := range avoid {
			if database == name {
				skip = true
			}
		}
		if skip {
			continue
		}
		conn, err := b.connect(ctx, target, database)
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("no maintenance database is available")
	}
	return nil, last
}

func closeConn(ctx context.Context, conn *pgx.Conn) {
	if conn != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
	}
}

func (b *postgresBackend) Identity(ctx context.Context, target Target, part, database string) (DatabaseIdentity, error) {
	if database == "" {
		database = target.Database
	}
	conn, err := b.connect(ctx, target, database)
	if err != nil {
		return DatabaseIdentity{}, fmt.Errorf("%s database is missing or unreachable: %w", part, err)
	}
	defer closeConn(ctx, conn)
	var identity DatabaseIdentity
	if err := conn.QueryRow(ctx, `SHOW server_version`).Scan(&identity.ServerVersion); err != nil {
		return DatabaseIdentity{}, targetError(target, "read the server version", err)
	}
	rows, err := conn.Query(ctx, `SELECT version, name, coalesce(part, '') FROM schema_migrations ORDER BY version, name, coalesce(part, '')`)
	if err != nil {
		return DatabaseIdentity{}, targetError(target, "read schema_migrations", err)
	}
	defer rows.Close()
	for rows.Next() {
		var migration Migration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.Part); err != nil {
			return DatabaseIdentity{}, targetError(target, "read schema_migrations", err)
		}
		identity.Schema = append(identity.Schema, migration)
	}
	if err := rows.Err(); err != nil {
		return DatabaseIdentity{}, targetError(target, "read schema_migrations", err)
	}
	if len(identity.Schema) == 0 {
		return DatabaseIdentity{}, fmt.Errorf("%s schema_migrations is empty; refusing an uninitialized database", part)
	}
	if part == "manager" {
		var hasTable bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass('public.sealing_key') IS NOT NULL`).Scan(&hasTable); err != nil {
			return DatabaseIdentity{}, targetError(target, "check the encryption-key identity", err)
		}
		if hasTable {
			if err := conn.QueryRow(ctx, `SELECT fingerprint FROM sealing_key`).Scan(&identity.SealingKey); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return DatabaseIdentity{}, targetError(target, "read the encryption-key identity", err)
			}
		}
		if identity.SealingKey == "" {
			if identity.SealedValues, err = sealedValues(ctx, conn); err != nil {
				return DatabaseIdentity{}, targetError(target, "read the sealed secrets", err)
			}
		}
	}
	return identity, nil
}

// sealedValues mirrors db.SealedValues over one connection (db.DB would open a
// pool of its own), skipping tables and columns an older schema lacks.
func sealedValues(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	var out []string
	collect := func(query string, args ...any) error {
		rows, err := conn.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		values, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, value := range values {
			if value != "" {
				out = append(out, value)
			}
		}
		return nil
	}
	hasColumn := func(table, column string) (bool, error) {
		var ok bool
		err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass($1) AND attname = $2 AND attnum > 0 AND NOT attisdropped)`, table, column).Scan(&ok)
		return ok, err
	}
	for _, sealed := range db.SealedColumns() {
		table, column, _ := strings.Cut(sealed, ".")
		if ok, err := hasColumn(table, column); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		if err := collect(`SELECT coalesce(` + pgx.Identifier{column}.Sanitize() + `::text, '') FROM ` + pgx.Identifier{table}.Sanitize()); err != nil {
			return nil, err
		}
	}
	if ok, err := hasColumn("settings", "value"); err != nil || !ok {
		return out, err
	}
	for _, setting := range db.SealedSettings() {
		for _, field := range setting.Fields {
			query := `SELECT coalesce(value->>$2, '') FROM settings WHERE key = $1 AND jsonb_typeof(value) = 'object'`
			if setting.List {
				query = `SELECT coalesce(e.elem->>$2, '') FROM settings s, jsonb_array_elements(s.value) AS e(elem)
					WHERE s.key = $1 AND jsonb_typeof(s.value) = 'array' AND jsonb_typeof(e.elem) = 'object'`
			}
			if err := collect(query, setting.Key, field); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (b *postgresBackend) Quiesced(ctx context.Context, target Target, database string, missingOK bool) error {
	if database == "" {
		database = target.Database
	}
	conn, err := b.connect(ctx, target, database)
	if err != nil {
		if missingOK {
			exists, existsErr := b.Exists(ctx, target, database)
			if existsErr == nil && !exists {
				return nil
			}
		}
		return fmt.Errorf("%s database is missing or unreachable: %w", target.Part, err)
	}
	defer closeConn(ctx, conn)
	var sessions int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`).Scan(&sessions); err != nil {
		return targetError(target, "check that writes are stopped", err)
	}
	if sessions != 0 {
		return fmt.Errorf("%s database still has %d other connection(s); stop the hub and built-in manager before using --writes-quiesced", target.Part, sessions)
	}
	return nil
}

func (b *postgresBackend) Create(ctx context.Context, target Target, name string) error {
	conn, err := b.admin(ctx, target, name)
	if err != nil {
		return err
	}
	defer closeConn(ctx, conn)
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		return targetError(target, "create a temporary database", err)
	}
	return nil
}

func (b *postgresBackend) Drop(ctx context.Context, target Target, name string) error {
	conn, err := b.admin(ctx, target, name)
	if err != nil {
		return err
	}
	defer closeConn(ctx, conn)
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+identifier+` WITH (FORCE)`); err != nil {
		if _, fallbackErr := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+identifier); fallbackErr != nil {
			return targetError(target, "drop a temporary database", errors.Join(err, fallbackErr))
		}
	}
	return nil
}

func (b *postgresBackend) Rename(ctx context.Context, target Target, from, to string) error {
	conn, err := b.admin(ctx, target, from)
	if err != nil {
		return err
	}
	defer closeConn(ctx, conn)
	query := `ALTER DATABASE ` + pgx.Identifier{from}.Sanitize() + ` RENAME TO ` + pgx.Identifier{to}.Sanitize()
	if _, err := conn.Exec(ctx, query); err != nil {
		return targetError(target, "rename a staged database", err)
	}
	return nil
}

func (b *postgresBackend) Exists(ctx context.Context, target Target, name string) (bool, error) {
	conn, err := b.admin(ctx, target, name)
	if err != nil {
		return false, err
	}
	defer closeConn(ctx, conn)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return false, targetError(target, "check a database name", err)
	}
	return exists, nil
}

func (b *postgresBackend) Dump(ctx context.Context, target Target, destination string) error {
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	runErr := b.runClient(ctx, target, target.Database, nil, out, "pg_dump", "--format=custom", "--no-owner", "--no-acl")
	if syncErr := out.Sync(); runErr == nil {
		runErr = syncErr
	}
	if closeErr := out.Close(); runErr == nil {
		runErr = closeErr
	}
	if runErr != nil {
		return runErr
	}
	info, err := os.Stat(destination)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return errors.New("pg_dump produced an empty dump")
	}
	return nil
}

func (b *postgresBackend) Restore(ctx context.Context, target Target, database, dumpPath string) error {
	// pg_restore connects only with -d (otherwise it prints a script). libpq
	// expands a dbname with "=" or a URI prefix as a connection string, so
	// only a plain name is passed; the rest stays in the environment.
	if database == "" || strings.Contains(database, "=") || strings.Contains(database, "://") {
		return errors.New("refusing to restore into a database name libpq would read as a connection string")
	}
	in, err := os.Open(dumpPath)
	if err != nil {
		return err
	}
	defer in.Close()
	return b.runClient(ctx, target, database, in, io.Discard, "pg_restore", "--format=custom", "--exit-on-error", "--no-owner", "--no-acl", "--dbname="+database)
}

var postgresURLPattern = lazyre.New(`postgres(?:ql)?://[^[:space:]"']+`)
var passwordPattern = lazyre.New(`(?i)(password|sslpassword)=[^[:space:]]+`)

func redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "<redacted>")
		}
	}
	text = postgresURLPattern.ReplaceAllString(text, "<redacted-database-url>")
	return passwordPattern.ReplaceAllString(text, "$1=<redacted>")
}

func targetSecrets(target Target) []string {
	secrets := []string{target.URL}
	if u, err := url.Parse(target.URL); err == nil {
		if password, ok := u.User.Password(); ok {
			secrets = append(secrets, password)
		}
	}
	if cfg, err := pgx.ParseConfig(target.URL); err == nil {
		secrets = append(secrets, cfg.Password)
	}
	return secrets
}

func targetError(target Target, action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", action, redact(err.Error(), targetSecrets(target)...))
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.remaining <= 0 {
		return len(p), nil
	}
	if len(p) > b.remaining {
		_, _ = b.Buffer.Write(p[:b.remaining])
		b.remaining = 0
		return len(p), nil
	}
	n, _ := b.Buffer.Write(p)
	b.remaining -= n
	return len(p), nil
}

// libpqQueryEnvironment maps connection-URL query parameters to the libpq
// environment variables the command-line clients read. Host, port, user,
// database and password are taken from pgx's parsed configuration instead.
var libpqQueryEnvironment = map[string]string{
	"sslmode":                  "PGSSLMODE",
	"sslrootcert":              "PGSSLROOTCERT",
	"sslcert":                  "PGSSLCERT",
	"sslkey":                   "PGSSLKEY",
	"sslcrl":                   "PGSSLCRL",
	"sslcrldir":                "PGSSLCRLDIR",
	"sslsni":                   "PGSSLSNI",
	"sslnegotiation":           "PGSSLNEGOTIATION",
	"sslcertmode":              "PGSSLCERTMODE",
	"ssl_min_protocol_version": "PGSSLMINPROTOCOLVERSION",
	"ssl_max_protocol_version": "PGSSLMAXPROTOCOLVERSION",
	"requirepeer":              "PGREQUIREPEER",
	"gssencmode":               "PGGSSENCMODE",
	"channel_binding":          "PGCHANNELBINDING",
	"connect_timeout":          "PGCONNECT_TIMEOUT",
	"target_session_attrs":     "PGTARGETSESSIONATTRS",
	"passfile":                 "PGPASSFILE",
}

// clientEnvironment is the libpq environment for pg_dump/pg_restore. libpq
// expands a URI only as an explicit dbname argument, never in PGDATABASE, so
// every part is passed as its own variable; the password only in PGPASSWORD.
// Inside a Docker container the server is the container's own loopback.
func clientEnvironment(target Target, database string, docker bool) []string {
	values := map[string]string{"PGDATABASE": database}
	u, err := url.Parse(target.URL)
	if err != nil {
		return []string{"PGDATABASE=" + database, "PGPASSWORD="}
	}
	// pgx loads TLS files while parsing; host, port, user and password must
	// not depend on whether those files are readable here.
	addressing := *u
	query := u.Query()
	for _, key := range []string{"sslrootcert", "sslcert", "sslkey", "sslpassword", "sslcrl", "sslcrldir", "sslmode", "sslsni", "sslnegotiation"} {
		query.Del(key)
	}
	query.Set("sslmode", "disable")
	addressing.RawQuery = query.Encode()
	if cfg, err := pgx.ParseConfig(addressing.String()); err == nil {
		values["PGHOST"], values["PGPORT"] = cfg.Host, strconv.Itoa(int(cfg.Port))
		values["PGUSER"], values["PGPASSWORD"] = cfg.User, cfg.Password
	}
	if docker {
		values["PGHOST"], values["PGPORT"], values["PGSSLMODE"] = "127.0.0.1", "5432", "disable"
	} else {
		for key, list := range u.Query() {
			if name, ok := libpqQueryEnvironment[key]; ok && len(list) > 0 {
				values[name] = list[len(list)-1]
			}
		}
	}
	environment := make([]string, 0, len(values))
	for name, value := range values {
		if value != "" || name == "PGPASSWORD" {
			environment = append(environment, name+"="+value)
		}
	}
	sort.Strings(environment)
	return environment
}

func (b *postgresBackend) runClient(ctx context.Context, target Target, database string, stdin io.Reader, stdout io.Writer, program string, args ...string) error {
	container := target.Container
	if container == "" {
		if _, err := b.lookPath(program); err != nil {
			container = target.DefaultContainer
		}
	}
	environment := clientEnvironment(target, database, container != "")
	var cmd *exec.Cmd
	if container != "" {
		dockerArgs := []string{"exec", "--interactive"}
		for _, variable := range environment {
			name, _, _ := strings.Cut(variable, "=")
			dockerArgs = append(dockerArgs, "--env", name)
		}
		dockerArgs = append(dockerArgs, container, program)
		cmd = exec.CommandContext(ctx, "docker", append(dockerArgs, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, program, args...)
	}
	cmd.Env = append(os.Environ(), environment...)
	cmd.Stdin, cmd.Stdout = stdin, stdout
	stderr := &limitedBuffer{remaining: 32 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			detail = ": " + detail
		}
		return fmt.Errorf("%s failed%s: %w", program, redact(detail, targetSecrets(target)...), err)
	}
	return nil
}

// sortedIdentities makes fake and real backends produce the same canonical
// comparison even if a database driver relaxes row ordering in the future.
func sortedIdentities(identity DatabaseIdentity) DatabaseIdentity {
	identity.Schema = append([]Migration(nil), identity.Schema...)
	sort.Slice(identity.Schema, func(i, j int) bool {
		if identity.Schema[i].Version != identity.Schema[j].Version {
			return identity.Schema[i].Version < identity.Schema[j].Version
		}
		if identity.Schema[i].Name != identity.Schema[j].Name {
			return identity.Schema[i].Name < identity.Schema[j].Name
		}
		return identity.Schema[i].Part < identity.Schema[j].Part
	})
	return identity
}

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
