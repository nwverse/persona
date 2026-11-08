package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// A database a manager was given (`personad manager join --database-url`) is made when that server has none by
// its name yet (docs/assistant/managers.md, "Database"); the container install makes comes with its own.

// databaseName is what EnsureDatabase creates: plain lower-case identifiers only, so it needs no quoting rules.
var databaseName = lazyre.New(`^[a-z_][a-z0-9_]{0,62}$`)

// DatabaseName is the name of a URL's database ("" when it names none).
func DatabaseName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// WithDatabase is raw (a postgres:// URL) naming database name instead, everything else (user, password, host,
// options) as it was.
func WithDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return "", errors.New("not a postgres:// URL")
	}
	u.Path, u.RawPath = "/"+name, ""
	return u.String(), nil
}

// EnsureDatabase makes the database raw names when the server has none by that name yet (created: it did), by way
// of the server's maintenance database (postgres, else template1) with the same role. The name must be a plain
// identifier (lower case, digits, _). A role that may not create databases gets an error that says what to do.
func EnsureDatabase(ctx context.Context, raw string) (created bool, err error) {
	name := DatabaseName(raw)
	if !databaseName.MatchString(name) {
		return false, fmt.Errorf("database name %q: only lower-case letters, digits and _ are made automatically", name)
	}
	var conn *pgx.Conn
	for _, maint := range []string{"postgres", "template1"} {
		u, err := WithDatabase(raw, maint)
		if err != nil {
			return false, err
		}
		if conn, err = pgx.Connect(ctx, u); err == nil {
			break
		}
		if maint == "template1" {
			// Neither: the role may only reach its own database, which may already be there.
			if c, err2 := pgx.Connect(ctx, raw); err2 == nil {
				_ = c.Close(ctx)
				return false, nil
			}
			return false, fmt.Errorf("connect to the database server: %w", err)
		}
	}
	defer conn.Close(context.WithoutCancel(ctx))
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	_, err = conn.Exec(ctx, `CREATE DATABASE "`+name+`"`)
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &pe) && pe.Code == "42P04": // duplicate_database: made meanwhile
		return false, nil
	case errors.As(err, &pe) && pe.Code == "42501": // insufficient_privilege
		return false, fmt.Errorf("the database role may not create databases: create %q yourself (CREATE DATABASE %s OWNER <role>) or give a database_url", name, name)
	case err != nil:
		return false, fmt.Errorf("create database %s: %w", name, err)
	}
	return true, nil
}
