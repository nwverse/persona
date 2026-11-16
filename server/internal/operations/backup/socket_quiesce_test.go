package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Unix-socket and default-host URLs are valid libpq URLs that name no TCP host.
func TestSocketDatabaseURLsAreAccepted(t *testing.T) {
	for _, raw := range []string{
		"postgres:///persona",
		"postgresql:///persona",
		"postgres://persona@/persona?host=/run/persona-pg",
		"postgres://persona:secret@/persona?host=%2Frun%2Fpersona-pg&sslmode=disable",
	} {
		target, err := makeTarget("hub", raw, "", "persona-postgres")
		if err != nil {
			fatalf(t, "makeTarget(%q): %v", raw, err)
		}
		if target.Database != "persona" {
			errorf(t, "makeTarget(%q) database = %q", raw, target.Database)
		}
		if target.DefaultContainer != "" {
			errorf(t, "makeTarget(%q) guessed Docker container %q for a socket URL", raw, target.DefaultContainer)
		}
		derived, err := withDatabase(raw, "persona_manager")
		if err != nil {
			fatalf(t, "withDatabase(%q): %v", raw, err)
		}
		again, err := makeTarget("manager", derived, "", "")
		if err != nil || again.Database != "persona_manager" {
			fatalf(t, "derived manager URL %q: %v (database %q)", derived, err, again.Database)
		}
		if u, _ := url.Parse(raw); u.Query().Get("host") != "" {
			if got, _ := url.Parse(derived); got.Query().Get("host") != u.Query().Get("host") {
				errorf(t, "derived URL %q lost the socket host", derived)
			}
		}
	}
}

func TestNonPostgresDatabaseURLsAreStillRejected(t *testing.T) {
	isolateLibpqEnvironment(t) // PGDATABASE would otherwise name the database
	for _, raw := range []string{"", "mysql://u@h/db", "http://h/db", "/var/lib/db", "postgres://h:5432"} {
		if _, err := makeTarget("hub", raw, "", ""); err == nil {
			errorf(t, "makeTarget(%q) was accepted", raw)
		}
	}
	if _, err := withDatabase("mysql://u@h/db", "x"); err == nil {
		errorT(t, "withDatabase accepted a non-postgres URL")
	}
}

// Against a real server, through its unix socket: PERSONA_TEST_SOCKET_URL is a
// URL such as postgres://user@/postgres?host=/path/to/socket-dir.
func TestPostgresBackendOverUnixSocket(t *testing.T) {
	base := os.Getenv("PERSONA_TEST_SOCKET_URL")
	if base == "" {
		skip(t, "set PERSONA_TEST_SOCKET_URL to a disposable server's unix-socket URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name := "pb_it_sock_" + randomSuffix(t)
	raw, err := withDatabase(base, name)
	if err != nil {
		fatal(t, err)
	}
	target, err := makeTarget("hub", raw, "", "")
	if err != nil {
		fatal(t, err)
	}
	backend := newPostgresBackend()
	if err := backend.Create(ctx, target, name); err != nil {
		fatal(t, err)
	}
	t.Cleanup(func() { _ = backend.Drop(context.Background(), target, name) })
	if err := backend.Quiesced(ctx, target, name, false); err != nil {
		fatalf(t, "idle database over a unix socket: %v", err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		fatal(t, err)
	}
	return hex.EncodeToString(random)
}

// A subscription's apply worker is attached to the subscriber database but is
// not a client; only client sessions are writers that must be stopped.
func TestQuiescedIgnoresReplicationWorkers(t *testing.T) {
	base := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if base == "" {
		skip(t, "set PERSONA_TEST_DATABASE_URL to a disposable superuser connection (wal_level=logical)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id := randomSuffix(t)
	pubName, subName := "pb_it_pub_"+id, "pb_it_sub_"+id
	pubURL, err := withDatabase(base, pubName)
	if err != nil {
		fatal(t, err)
	}
	subURL, err := withDatabase(base, subName)
	if err != nil {
		fatal(t, err)
	}
	backend := newPostgresBackend()
	pub := Target{Part: "hub", URL: pubURL, Database: pubName}
	sub := Target{Part: "hub", URL: subURL, Database: subName}
	slot := "pb_it_slot_" + id
	subscription := "pb_it_subscription_" + id
	for _, target := range []Target{pub, sub} {
		if err := backend.Create(ctx, target, target.Database); err != nil {
			fatal(t, err)
		}
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if conn, err := backend.connect(cleanup, sub, subName); err == nil {
			_, _ = conn.Exec(cleanup, `ALTER SUBSCRIPTION `+subscription+` DISABLE`)
			_, _ = conn.Exec(cleanup, `ALTER SUBSCRIPTION `+subscription+` SET (slot_name = NONE)`)
			_, _ = conn.Exec(cleanup, `DROP SUBSCRIPTION IF EXISTS `+subscription)
			closeConn(cleanup, conn)
		}
		_ = backend.Drop(cleanup, sub, subName)
		if conn, err := backend.connect(cleanup, pub, "postgres"); err == nil {
			_, _ = conn.Exec(cleanup, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = $1 AND NOT active`, slot)
			closeConn(cleanup, conn)
		}
		_ = backend.Drop(cleanup, pub, pubName)
	})
	run := func(target Target, statements ...string) {
		t.Helper()
		conn, err := backend.connect(ctx, target, target.Database)
		if err != nil {
			fatal(t, err)
		}
		defer closeConn(ctx, conn)
		for _, statement := range statements {
			if _, err := conn.Exec(ctx, statement); err != nil {
				fatalf(t, "%s: %v", statement, err)
			}
		}
	}
	run(pub, `CREATE TABLE t (id int PRIMARY KEY)`, `CREATE PUBLICATION p FOR TABLE t`,
		`SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`)
	run(sub, `CREATE TABLE t (id int PRIMARY KEY)`)
	cfg, err := pgx.ParseConfig(pubURL)
	if err != nil {
		fatal(t, err)
	}
	connection := "host=" + cfg.Host + " port=" + strconv.Itoa(int(cfg.Port)) + " user=" + cfg.User + " dbname=" + pubName
	if cfg.Password != "" {
		connection += " password=" + strings.ReplaceAll(cfg.Password, " ", "")
	}
	run(sub, `CREATE SUBSCRIPTION `+subscription+` CONNECTION '`+connection+`' PUBLICATION p
		WITH (create_slot = false, slot_name = '`+slot+`', copy_data = false)`)

	monitor, err := backend.connect(ctx, sub, "postgres")
	if err != nil {
		fatal(t, err)
	}
	defer closeConn(ctx, monitor)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	timeout := time.After(30 * time.Second)
	for {
		var workers int
		if err := monitor.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND backend_type <> 'client backend'`, subName).Scan(&workers); err != nil {
			fatal(t, err)
		}
		if workers > 0 {
			break
		}
		select {
		case <-poll.C:
		case <-timeout:
			fatal(t, "the logical replication apply worker never started")
		}
	}

	if err := backend.Quiesced(ctx, sub, subName, false); err != nil {
		fatalf(t, "a replication worker was counted as a writer: %v", err)
	}

	// A real client session must still block the check.
	client, err := backend.connect(ctx, sub, subName)
	if err != nil {
		fatal(t, err)
	}
	defer closeConn(ctx, client)
	err = backend.Quiesced(ctx, sub, subName, false)
	if err == nil || !strings.Contains(err.Error(), "1 other connection") {
		fatalf(t, "an open client session must block the check, got %v", err)
	}
}
