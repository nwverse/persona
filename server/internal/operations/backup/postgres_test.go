package backup

import (
	"bytes"
	"context"
	"errors"
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

// The decision to fall back to the local container must use the host the
// connection really uses (pgx's), not the URL authority: here the authority
// says localhost but the query moves the connection to another server.
func TestContainerFallbackFollowsTheConnectionHost(t *testing.T) {
	isolateLibpqEnvironment(t)
	for raw, want := range map[string]string{
		"postgres://u@localhost/db?host=remote.example":           "",
		"postgres://u@127.0.0.1/db?host=remote.example":           "",
		"postgres://u@localhost/db?host=127.0.0.1,remote.example": "",
		"postgres://u@/db?host=remote.example":                    "",
		"postgres://u@remote.example/db?host=127.0.0.1":           "persona-postgres",
		"postgres://u@127.0.0.1/db":                               "persona-postgres",
		"postgres://u@localhost:5433/db":                          "persona-postgres",
	} {
		target, err := makeTarget("hub", raw, "", "persona-postgres")
		if err != nil {
			t.Fatalf("makeTarget(%q): %v", raw, err)
		}
		if target.DefaultContainer != want {
			t.Errorf("makeTarget(%q).DefaultContainer = %q, want %q", raw, target.DefaultContainer, want)
		}
	}
}

// pgx reads sslrootcert/sslcert while parsing; a file that is not readable
// where personad runs must not turn into "must name a database".
func TestMakeTargetIgnoresUnreadableTLSFiles(t *testing.T) {
	isolateLibpqEnvironment(t)
	raw := "postgres://u@127.0.0.1/db?sslmode=verify-full&sslrootcert=/nonexistent/ca.pem&sslcert=/nonexistent/c.pem&sslkey=/nonexistent/k.pem"
	target, err := makeTarget("hub", raw, "", "persona-postgres")
	if err != nil {
		t.Fatalf("makeTarget: %v", err)
	}
	if target.Database != "db" || target.DefaultContainer != "persona-postgres" {
		t.Fatalf("target = %+v", target)
	}
	if _, err := makeTarget("hub", "postgres://u@127.0.0.1/?sslrootcert=/nonexistent/ca.pem", "", ""); err == nil || !strings.Contains(err.Error(), "must name a database") {
		t.Fatalf("a URL without a database = %v", err)
	}
}

// A non-loopback host (the shipped Compose form: host "postgres") with no
// pg_dump/pg_restore here and no usable container fails before anything is
// done, with a message that says so.
func TestClientPreflightFailsEarlyWithoutUsableClients(t *testing.T) {
	isolateLibpqEnvironment(t)
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	present := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	compose, err := makeTarget("hub", "postgres://persona@postgres:5432/persona", "", "persona-postgres")
	if err != nil {
		t.Fatal(err)
	}
	backend := &postgresBackend{lookPath: missing}
	err = backend.CheckClients(compose, "pg_dump", "pg_restore")
	if err == nil {
		t.Fatal("a non-loopback host without clients was accepted")
	}
	for _, wanted := range []string{"pg_dump", "postgres", "Compose", "not supported"} {
		if !strings.Contains(err.Error(), wanted) {
			t.Errorf("error lacks %q: %v", wanted, err)
		}
	}
	if strings.Contains(err.Error(), "persona@") {
		t.Errorf("error leaks the URL: %v", err)
	}
	only := func(name string) (string, error) {
		if name == "pg_dump" {
			return "/usr/bin/pg_dump", nil
		}
		return "", os.ErrNotExist
	}
	if err := (&postgresBackend{lookPath: only}).CheckClients(compose, "pg_dump", "pg_restore"); err == nil || !strings.Contains(err.Error(), "pg_restore") {
		t.Errorf("pg_restore missing = %v", err)
	}
	if err := (&postgresBackend{lookPath: only}).CheckClients(compose, "pg_dump"); err != nil {
		t.Errorf("only pg_dump needed and present: %v", err)
	}
	if err := (&postgresBackend{lookPath: present}).CheckClients(compose, "pg_dump", "pg_restore"); err != nil {
		t.Errorf("clients present: %v", err)
	}
	// The loopback default container needs docker itself.
	local, err := makeTarget("hub", "postgres://persona@127.0.0.1:5432/persona", "", "persona-postgres")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&postgresBackend{lookPath: missing}).CheckClients(local, "pg_dump"); err == nil || !strings.Contains(err.Error(), "docker") {
		t.Errorf("no clients and no docker = %v", err)
	}
	dockerOnly := func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", os.ErrNotExist
	}
	if err := (&postgresBackend{lookPath: dockerOnly}).CheckClients(local, "pg_dump", "pg_restore"); err != nil {
		t.Errorf("container fallback available: %v", err)
	}
}

// The redaction list must hold the query-string password even when TLS files
// named in the URL cannot be read here.
func TestTargetSecretsIncludeQueryPasswordWithUnreadableTLSFiles(t *testing.T) {
	isolateLibpqEnvironment(t)
	target := Target{URL: "postgres://u@db.example/db?sslmode=verify-full&sslrootcert=/nonexistent/ca.pem&password=query-secret"}
	found := false
	for _, secret := range targetSecrets(target) {
		found = found || secret == "query-secret"
	}
	if !found {
		t.Fatalf("targetSecrets = %q", targetSecrets(target))
	}
}

// With an explicit container the programs are not looked up here, so a missing
// docker command must not be reported as missing client tools.
func TestPreflightDockerMessageIsAccurate(t *testing.T) {
	isolateLibpqEnvironment(t)
	target, err := makeTarget("hub", "postgres://u@db.example/db", "named-container", "")
	if err != nil {
		t.Fatal(err)
	}
	err = (&postgresBackend{lookPath: func(string) (string, error) { return "", os.ErrNotExist }}).CheckClients(target, "pg_dump")
	if err == nil || !strings.Contains(err.Error(), "docker") || !strings.Contains(err.Error(), "named-container") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "not installed here") {
		t.Errorf("the message claims the clients are missing without checking: %v", err)
	}
	// Clients on PATH while docker is missing: the explicit container still needs docker.
	only := func(name string) (string, error) {
		if name == "pg_dump" {
			return "/bin/pg_dump", nil
		}
		return "", os.ErrNotExist
	}
	err = (&postgresBackend{lookPath: only}).CheckClients(target, "pg_dump")
	if err == nil || strings.Contains(err.Error(), "not installed here") {
		t.Fatalf("clients present, docker missing = %v", err)
	}
}

// The default container needs docker too; without it the message names what is
// missing and the container.
func TestPreflightMissingDockerForTheDefaultContainerNamesBoth(t *testing.T) {
	isolateLibpqEnvironment(t)
	local, err := makeTarget("hub", "postgres://persona@127.0.0.1:5432/persona", "", "persona-postgres")
	if err != nil {
		t.Fatal(err)
	}
	only := func(name string) (string, error) {
		if name == "pg_dump" {
			return "/bin/pg_dump", nil
		}
		return "", os.ErrNotExist
	}
	err = (&postgresBackend{lookPath: only}).CheckClients(local, "pg_dump", "pg_restore")
	if err == nil {
		t.Fatal("missing pg_restore and docker was accepted")
	}
	for _, wanted := range []string{"pg_restore", "docker", "persona-postgres"} {
		if !strings.Contains(err.Error(), wanted) {
			t.Errorf("error lacks %q: %v", wanted, err)
		}
	}
	if strings.Contains(err.Error(), "pg_dump and") || strings.Contains(err.Error(), "and pg_restore") {
		t.Errorf("error names a program that is present: %v", err)
	}
}

// pgx tries every host of a multi-host URL in order (cfg.Fallbacks); the
// command-line clients must be given the same list, each host with its own
// port, or a backup fails whenever the first host is down or is not the one
// target_session_attrs wants.
func TestClientEnvironmentKeepsEveryHostOfAMultiHostURL(t *testing.T) {
	isolateLibpqEnvironment(t)
	target := Target{URL: "postgres://persona:pw@db-a.example:5433,db-b.example:6544,db-c.example:7655/persona?target_session_attrs=read-write&sslmode=disable"}
	values := environmentMap(t, clientEnvironment(target, "persona", false))
	if values["PGHOST"] != "db-a.example,db-b.example,db-c.example" || values["PGPORT"] != "5433,6544,7655" || values["PGTARGETSESSIONATTRS"] != "read-write" {
		t.Fatalf("multi-host environment = %#v", values)
	}
	values = environmentMap(t, clientEnvironment(Target{URL: "postgres://persona@/persona?host=/run/pg-a,/run/pg-b&port=5433,5434"}, "persona", false))
	if values["PGHOST"] != "/run/pg-a,/run/pg-b" || values["PGPORT"] != "5433,5434" {
		t.Fatalf("multi-socket environment = %#v", values)
	}
	// Inside the container the server is the container's own loopback.
	values = environmentMap(t, clientEnvironment(target, "persona", true))
	if values["PGHOST"] != "127.0.0.1" || values["PGPORT"] != "5432" {
		t.Fatalf("docker environment = %#v", values)
	}
}

// pgx decrypts an encrypted TLS client key with sslpassword. libpq has no
// environment variable for it (its PQconninfoOptions entry names none), so the
// clients get it in a private service file: never in their arguments or
// environment, the file gone once they exit, and the value kept out of every
// error.
func TestRunClientPassesTheTLSKeyPasswordPrivately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	isolateLibpqEnvironment(t)
	directory := t.TempDir()
	report := filepath.Join(directory, "report")
	secret := "key-passphrase-secret"
	rawURL := "postgres://persona@db.example:5432/persona?sslmode=verify-full&sslkey=/etc/persona/client.key&sslcert=/etc/persona/client.crt&sslpassword=" + secret
	script := "#!/bin/sh\n{ echo \"ARGS $*\"; env | grep '^PG'; echo SERVICE; cat \"$PGSERVICEFILE\"; echo \"FILE $PGSERVICEFILE\"; } > '" + report + "'\n" +
		"echo \"could not decrypt with sslpassword=" + secret + " (" + secret + ")\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(directory, "pg_dump"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+"/usr/bin:/bin")
	err := newPostgresBackend().runClient(context.Background(), Target{URL: rawURL}, "persona", nil, io.Discard, "pg_dump", "--format=custom")
	if err == nil {
		t.Fatal("expected the fake client failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error leaks the key password: %q", err)
	}
	raw, readErr := os.ReadFile(report)
	if readErr != nil {
		t.Fatal(readErr)
	}
	before, service, _ := strings.Cut(string(raw), "\nSERVICE\n")
	if strings.Contains(before, secret) {
		t.Errorf("the key password is in the client's arguments or environment:\n%s", before)
	}
	if !strings.Contains(before, "PGSERVICE=persona-backup") || !strings.Contains(service, "[persona-backup]\nsslpassword="+secret+"\n") {
		t.Fatalf("the client was not given the key password through its service file:\n%s", raw)
	}
	_, file, _ := strings.Cut(service, "FILE ")
	if _, statErr := os.Stat(strings.TrimSpace(file)); !os.IsNotExist(statErr) {
		t.Errorf("the service file %s remains: %v", strings.TrimSpace(file), statErr)
	}
	if got := targetError(Target{URL: rawURL}, "dump", errors.New("connect with "+secret+" failed")).Error(); strings.Contains(got, secret) {
		t.Errorf("a target error leaks the key password: %q", got)
	}
	// Without sslpassword no service file is made.
	if environment, cleanup, err := clientServiceFile(Target{URL: "postgres://persona@db.example/persona"}); err != nil || len(environment) != 0 {
		t.Errorf("service file for a URL without sslpassword: %v, %v", environment, err)
	} else {
		cleanup()
	}
	// A value a service file cannot hold is refused, without echoing it.
	if _, _, err := clientServiceFile(Target{URL: "postgres://persona@db.example/persona?sslpassword=abc%0Axyz"}); err == nil || strings.Contains(err.Error(), "xyz") {
		t.Errorf("a line break in sslpassword: %v", err)
	}
}
