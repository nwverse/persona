package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func environmentMap(t *testing.T, environment []string) map[string]string {
	t.Helper()
	values := map[string]string{}
	for _, variable := range environment {
		name, value, ok := strings.Cut(variable, "=")
		if !ok || values[name] != "" {
			t.Fatalf("malformed or duplicate variable %q in %#v", variable, environment)
		}
		values[name] = value
	}
	return values
}

// isolateLibpqEnvironment keeps the developer's own PG* variables and passfile
// out of pgx's defaults.
func isolateLibpqEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PGHOST", "PGPORT", "PGUSER", "PGDATABASE", "PGPASSWORD", "PGSSLMODE", "PGSERVICE", "PGAPPNAME"} {
		t.Setenv(name, "")
	}
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "none"))
}

// libpq expands a URI only as an explicit dbname argument, never from
// PGDATABASE: every connection part must be its own variable.
func TestClientEnvironmentUsesPlainLibpqVariables(t *testing.T) {
	isolateLibpqEnvironment(t)
	target := Target{URL: "postgres://persona:process-secret@db.example:6543/persona?sslmode=verify-full&sslrootcert=/etc/persona/root.crt&password=query-secret&connect_timeout=7"}
	values := environmentMap(t, clientEnvironment(target, "another database", false))
	want := map[string]string{
		"PGHOST": "db.example", "PGPORT": "6543", "PGUSER": "persona", "PGDATABASE": "another database",
		"PGSSLMODE": "verify-full", "PGSSLROOTCERT": "/etc/persona/root.crt", "PGCONNECT_TIMEOUT": "7",
	}
	for name, value := range want {
		if values[name] != value {
			t.Errorf("%s = %q, want %q", name, values[name], value)
		}
	}
	if values["PGPASSWORD"] != "query-secret" && values["PGPASSWORD"] != "process-secret" {
		t.Errorf("PGPASSWORD = %q", values["PGPASSWORD"])
	}
	for name, value := range values {
		if name == "PGPASSWORD" {
			continue
		}
		if strings.Contains(value, "secret") || strings.Contains(value, "://") {
			t.Errorf("%s carries a password or URL: %q", name, value)
		}
	}
}

func TestClientEnvironmentKeepsPasswordOutOfDatabaseArgument(t *testing.T) {
	isolateLibpqEnvironment(t)
	target := Target{URL: "postgres://persona:process-secret@db.example:5432/persona?sslmode=require&sslrootcert=/host/only.crt"}
	for _, docker := range []bool{false, true} {
		values := environmentMap(t, clientEnvironment(target, "another database", docker))
		if values["PGPASSWORD"] != "process-secret" || values["PGDATABASE"] != "another database" || values["PGUSER"] != "persona" {
			t.Fatalf("docker=%v client environment = %#v", docker, values)
		}
		if docker {
			if values["PGHOST"] != "127.0.0.1" || values["PGPORT"] != "5432" || values["PGSSLMODE"] != "disable" || values["PGSSLROOTCERT"] != "" {
				t.Fatalf("Docker client does not use the container's loopback: %#v", values)
			}
		}
	}
}

func TestClientEnvironmentUnixSocketAndDefaultHost(t *testing.T) {
	isolateLibpqEnvironment(t)
	values := environmentMap(t, clientEnvironment(Target{URL: "postgres://persona@/persona?host=/run/persona-pg"}, "persona", false))
	if values["PGHOST"] != "/run/persona-pg" || values["PGPORT"] != "5432" || values["PGUSER"] != "persona" || values["PGPASSWORD"] != "" {
		t.Fatalf("unix-socket environment = %#v", values)
	}
	values = environmentMap(t, clientEnvironment(Target{URL: "postgres:///persona"}, "persona", false))
	if values["PGHOST"] == "" || values["PGDATABASE"] != "persona" {
		t.Fatalf("missing host should fall back to pgx's default host: %#v", values)
	}
}

func TestRunClientRedactsRealProcessErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	directory := t.TempDir()
	secret := "process-secret"
	rawURL := "postgres://persona:" + secret + "@db.example:5432/persona?sslmode=disable"
	script := "#!/bin/sh\nprintf '%s %s\\n' '" + rawURL + "' '" + secret + "' >&2\nexit 1\n"
	name := filepath.Join(directory, "pg_dump")
	if err := os.WriteFile(name, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	backend := newPostgresBackend()
	err := backend.runClient(context.Background(), Target{URL: rawURL}, "persona", nil, io.Discard, "pg_dump")
	if err == nil {
		t.Fatal("expected the fake client failure")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), rawURL) {
		t.Fatalf("process error leaked credentials: %q", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("process error was not redacted: %q", err)
	}
}

func TestRunClientDockerArgumentsContainNoPassword(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	directory := t.TempDir()
	argsFile := filepath.Join(directory, "args")
	databaseFile := filepath.Join(directory, "database")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$CAPTURE_ARGS\"\nprintf '%s' \"$PGDATABASE\" >\"$CAPTURE_DATABASE\"\nprintf 'dump'\n"
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("CAPTURE_ARGS", argsFile)
	t.Setenv("CAPTURE_DATABASE", databaseFile)
	backend := newPostgresBackend()
	target := Target{URL: "postgres://persona:process-secret@db.example:5432/persona?sslmode=disable", Container: "disposable-postgres"}
	var stdout bytes.Buffer
	if err := backend.runClient(context.Background(), target, "persona", nil, &stdout, "pg_dump", "--format=custom"); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsFile)
	database, _ := os.ReadFile(databaseFile)
	if strings.Contains(string(args), "process-secret") || strings.Contains(string(database), "process-secret") {
		t.Fatalf("Docker arguments leak a password: args %q database %q", args, database)
	}
	for _, wanted := range []string{"exec", "--interactive", "disposable-postgres", "pg_dump", "--format=custom"} {
		if !strings.Contains(string(args), wanted) {
			t.Errorf("Docker arguments lack %q: %q", wanted, args)
		}
	}
	if string(database) != "persona" {
		t.Errorf("PGDATABASE = %q, want the plain database name", database)
	}
	// Every variable set for the client must be forwarded into the container.
	for _, variable := range clientEnvironment(target, "persona", true) {
		name, _, _ := strings.Cut(variable, "=")
		if !strings.Contains(string(args), "--env\n"+name+"\n") {
			t.Errorf("Docker arguments do not forward %s: %q", name, args)
		}
	}
	if stdout.String() != "dump" {
		t.Fatalf("dump stdout = %q", stdout.String())
	}
}

func TestExternalDatabaseDoesNotGuessLocalDockerContainer(t *testing.T) {
	target, err := makeTarget("hub", "postgres://persona:secret@db.example:5432/persona", "", "persona-postgres")
	if err != nil {
		t.Fatal(err)
	}
	if target.DefaultContainer != "" {
		t.Fatalf("external URL guessed container %q", target.DefaultContainer)
	}
	local, err := makeTarget("hub", "postgres://persona:secret@127.0.0.1:5432/persona", "", "persona-postgres")
	if err != nil {
		t.Fatal(err)
	}
	if local.DefaultContainer != "persona-postgres" {
		t.Fatalf("native Docker default was not retained: %q", local.DefaultContainer)
	}
}

// pg_restore without -d prints a script instead of connecting (PostgreSQL 18
// refuses outright), so restore names the plain database explicitly.
func TestRestorePassesPlainDatabaseName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	directory := t.TempDir()
	argsFile := filepath.Join(directory, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$CAPTURE_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(directory, "pg_restore"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(directory, "dump")
	if err := os.WriteFile(dump, []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("CAPTURE_ARGS", argsFile)
	backend := newPostgresBackend()
	target := Target{URL: "postgres://persona:process-secret@db.example:5432/persona?sslmode=disable"}
	if err := backend.Restore(context.Background(), target, "persona_verify_m_0123", dump); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--dbname=persona_verify_m_0123\n") || strings.Contains(string(args), "secret") {
		t.Fatalf("pg_restore arguments = %q", args)
	}
	for _, unsafe := range []string{"", "host=elsewhere dbname=x", "postgres://elsewhere/x"} {
		if err := backend.Restore(context.Background(), target, unsafe, dump); err == nil {
			t.Errorf("restore accepted database name %q", unsafe)
		}
	}
}
