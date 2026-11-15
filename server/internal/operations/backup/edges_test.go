package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRefusesActiveDatabaseConnections(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.active["manager:"+fixture.manager.Database] = 1
	_, err := fixture.operator().Create(context.Background(), fixture.options())
	if err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("active-connection error = %v", err)
	}
	if _, statErr := os.Stat(fixture.output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bundle exists with active writers: %v", statErr)
	}
}

func TestInterruptedRestoreRollsBack(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	fixture.mutate(t)
	fixture.backend.failInstallPart = "manager"
	fixture.backend.installErr = context.Canceled
	_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("restore interruption = %v", err)
	}
	if got := fixture.backend.server(fixture.hub)[fixture.hub.Database].dump; got != "hub-two" {
		t.Fatalf("hub was not rolled back after interruption: %q", got)
	}
	if got := fixture.backend.server(fixture.manager)[fixture.manager.Database].dump; got != "manager-two" {
		t.Fatalf("manager was not rolled back after interruption: %q", got)
	}
	raw, _ := os.ReadFile(filepath.Join(fixture.home, "data", "facts", "note.md"))
	if string(raw) != "changed knowledge" {
		t.Fatalf("files were not rolled back after interruption: %q", raw)
	}
	assertNoTemporaryDatabases(t, fixture.backend)
}

func TestExternalKnowledgeBaseNeedsExplicitRestoreApproval(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	external := filepath.Join(t.TempDir(), "knowledge")
	writeFixtureFile(t, filepath.Join(external, "fact.md"), []byte("external original"))
	managerConfig := `{"database_name":"persona_manager","kb_dir":` + strconvQuote(external) + `}`
	writeFixtureFile(t, filepath.Join(fixture.home, "managers", "assistant", "manager.json"), []byte(managerConfig))
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(external, "fact.md"), []byte("external changed"))
	_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if err == nil || !strings.Contains(err.Error(), "--allow-external-kb") {
		t.Fatalf("external restore without approval = %v", err)
	}
	if got := fixture.backend.server(fixture.hub)[fixture.hub.Database].dump; got != "hub-one" {
		t.Fatalf("database changed without external approval: %q", got)
	}
	raw, _ := os.ReadFile(filepath.Join(external, "fact.md"))
	if string(raw) != "external changed" {
		t.Fatalf("external files changed without approval: %q", raw)
	}
	if _, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true, AllowExternalKB: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(external, "fact.md"))
	if string(raw) != "external original" {
		t.Fatalf("external files were not restored: %q", raw)
	}
	assertNoTemporaryDatabases(t, fixture.backend)
}

func TestExternallyHostedBuiltinIsOutOfScope(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	writeFixtureFile(t, filepath.Join(fixture.home, "hub.json"), []byte(`{"database_url":"`+fixture.hub.URL+`","assistant":{"external":true}}`))
	_, _, err := Discover(Options{Home: fixture.home})
	if err == nil || !strings.Contains(err.Error(), "outside this backup's scope") {
		t.Fatalf("external built-in discovery = %v", err)
	}
}

func TestVerifyAndRestoreCanDiscoverFromBundleAlone(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	emptyHome := filepath.Join(t.TempDir(), "new-runtime")
	if _, err := operator.Verify(context.Background(), Options{Home: emptyHome, Bundle: fixture.output}); err != nil {
		t.Fatal(err)
	}
	result, err := operator.Restore(context.Background(), Options{Home: emptyHome, Bundle: fixture.output, WritesQuiesced: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restored {
		t.Fatal("bare restore did not complete")
	}
	raw, err := os.ReadFile(filepath.Join(emptyHome, "hub.json"))
	if err != nil || !bytes.Contains(raw, []byte("hub-token-secret")) {
		t.Fatalf("bare restored hub.json = %q, %v", raw, err)
	}
	assertNoTemporaryDatabases(t, fixture.backend)
}

func TestManifestRejectsMismatchedBundledKey(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	if _, err := fixture.operator().Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readBundleManifest(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ManagerKeyFingerprint = strings.Repeat("0", 32)
	if err := manifest.validate(); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("mismatched manifest key = %v", err)
	}
}

func TestManifestContainsFingerprintsNotKeyMaterial(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	if _, err := fixture.operator().Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readBundleManifest(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifestJSON(t, fixture.output), fixture.managerKey) {
		t.Fatal("manifest contains manager key bytes")
	}
	if manifest.ManagerKeyFingerprint == string(fixture.managerKey) {
		t.Fatal("manifest stores the key rather than its fingerprint")
	}
}

func manifestJSON(t *testing.T, bundle string) []byte {
	t.Helper()
	manifest, _, err := readBundleManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	if err := extractBundle(bundle, staging, manifest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(staging, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func strconvQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

// A rename whose error arrives after the server committed it must not be taken
// for "nothing happened": rollback has to put the databases back from what
// actually exists, and must not leave files and databases from different
// installs.
func TestUncertainRenameResultRollsBackToTheRealState(t *testing.T) {
	for _, fault := range []string{"manager:install", "hub:install", "manager:previous", "hub:previous"} {
		t.Run(fault, func(t *testing.T) {
			fixture := newBackupFixture(t, "persona_manager")
			operator := fixture.operator()
			if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
				t.Fatal(err)
			}
			fixture.mutate(t)
			fixture.backend.commitThenFail = fault
			_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
			if err == nil || !strings.Contains(err.Error(), "injected connection loss") {
				t.Fatalf("restore error = %v", err)
			}
			if !fixture.backend.commitThenFired {
				t.Fatal("the fault was never injected")
			}
			if got := fixture.backend.server(fixture.hub)[fixture.hub.Database]; got == nil || got.dump != "hub-two" {
				t.Fatalf("hub database after rollback = %+v", got)
			}
			if got := fixture.backend.server(fixture.manager)[fixture.manager.Database]; got == nil || got.dump != "manager-two" {
				t.Fatalf("manager database after rollback = %+v", got)
			}
			raw, _ := os.ReadFile(filepath.Join(fixture.home, "data", "facts", "note.md"))
			if string(raw) != "changed knowledge" {
				t.Fatalf("files were not rolled back: %q", raw)
			}
			assertNoTemporaryDatabases(t, fixture.backend)
			for _, server := range fixture.backend.servers {
				for name := range server {
					if strings.HasPrefix(name, "persona_previous_") {
						t.Errorf("previous database %s was not renamed back", name)
					}
				}
			}
		})
	}
}

// Without usable pg_dump/pg_restore every command stops before it touches the
// bundle, the files or the databases.
func TestUnsupportedClientsFailBeforeAnyWork(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	fixture.mutate(t)
	fixture.backend.clientErr = errors.New("compose is not supported")
	fixture.backend.created, fixture.backend.renamed = nil, nil
	if _, err := operator.Verify(context.Background(), Options{Home: fixture.home, Bundle: fixture.output}); err == nil || !strings.Contains(err.Error(), "compose is not supported") {
		t.Errorf("verify = %v", err)
	}
	if _, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true}); err == nil || !strings.Contains(err.Error(), "compose is not supported") {
		t.Errorf("restore = %v", err)
	}
	other := fixture.options()
	other.Output = filepath.Join(filepath.Dir(fixture.output), "second.backup")
	if _, err := operator.Create(context.Background(), other); err == nil || !strings.Contains(err.Error(), "compose is not supported") {
		t.Errorf("create = %v", err)
	}
	if len(fixture.backend.created) != 0 || len(fixture.backend.renamed) != 0 {
		t.Errorf("databases were touched: created %v renamed %v", fixture.backend.created, fixture.backend.renamed)
	}
	if _, err := os.Stat(other.Output); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("bundle published: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(fixture.home, "data", "facts", "note.md"))
	if string(raw) != "changed knowledge" {
		t.Errorf("files touched: %q", raw)
	}
}

// A bundle from a non-Docker install has no docker/db-password. Restoring it on
// a Docker install must keep that file: the database role's password is not
// part of the bundle, and the file is how the containers authenticate.
func TestRestoreKeepsDockerPasswordFileAbsentFromBundle(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(fixture.home, "docker", "db-password")
	writeFixtureFile(t, password, []byte("compose-password"))
	if _, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(password)
	if err != nil || string(raw) != "compose-password" {
		t.Fatalf("docker/db-password after restore = %q, %v", raw, err)
	}
}

func restoreWithFault(t *testing.T, fixture *backupFixture, fault string) (Result, error) {
	t.Helper()
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	fixture.mutate(t)
	fixture.backend.dropped = nil // only what the restore drops counts
	fixture.backend.commitThenFail = fault
	result, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if !fixture.backend.commitThenFired {
		t.Fatal("the fault was never injected")
	}
	return result, err
}

func assertNothingDropped(t *testing.T, fixture *backupFixture, tolerated ...string) {
	t.Helper()
	for _, name := range fixture.backend.dropped {
		ok := false
		for _, prefix := range tolerated {
			ok = ok || strings.HasPrefix(name, prefix)
		}
		if !ok {
			t.Errorf("database %s was dropped after an uncertain rename", name)
		}
	}
}

// When the server cannot be asked whether the rename committed, it is taken to
// have: the undo runs, and nothing is dropped.
func TestRenameCheckFailureIsNotTakenForNotCommitted(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.existsErr = true
	_, err := restoreWithFault(t, fixture, "manager:install")
	if err == nil {
		t.Fatal("restore succeeded")
	}
	if got := fixture.backend.server(fixture.manager)[fixture.manager.Database]; got == nil || got.dump != "manager-two" {
		t.Fatalf("manager after rollback = %+v", got)
	}
	if got := fixture.backend.server(fixture.hub)[fixture.hub.Database]; got == nil || got.dump != "hub-two" {
		t.Fatalf("hub after rollback = %+v", got)
	}
	assertNothingDropped(t, fixture, "persona_staged_") // undone, so the staged copies are the ones to discard
}

// Both names existing, or neither, is not "the rename did not happen": nothing
// is dropped, the previous database stays, and the operator is told which
// staged database may remain.
func TestAmbiguousRenameStateDropsNothing(t *testing.T) {
	for _, shape := range []string{"both", "neither"} {
		t.Run(shape, func(t *testing.T) {
			fixture := newBackupFixture(t, "persona_manager")
			fixture.backend.faultShape = shape
			_, err := restoreWithFault(t, fixture, "manager:install")
			if err == nil {
				t.Fatal("restore succeeded")
			}
			assertNothingDropped(t, fixture, "persona_staged_h_")
			found := false
			for name, database := range fixture.backend.server(fixture.manager) {
				if strings.HasPrefix(name, "persona_previous_m_") && database.dump == "manager-two" {
					found = true
				}
				if database.dump == "manager-two" && !strings.HasPrefix(name, "persona_previous_m_") && name != fixture.manager.Database {
					t.Errorf("unexpected copy of the previous database under %s", name)
				}
			}
			if shape == "both" && !strings.Contains(err.Error(), "persona_staged_m_") {
				t.Errorf("the error does not name the staged database that may remain: %v", err)
			}
			if !found && fixture.backend.server(fixture.manager)[fixture.manager.Database] == nil {
				t.Errorf("the previous manager database is gone: %v", fixture.backend.server(fixture.manager))
			}
		})
	}
}

// The server stays unreachable after an install rename committed: nothing can
// be undone. Files must then stay consistent with the databases that are live,
// and the error must say where the previous databases and files are.
func TestUnreachableServerAfterCommitKeepsFilesAndNamesThePreviousState(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.down = true
	_, err := restoreWithFault(t, fixture, "manager:install")
	if err == nil {
		t.Fatal("restore succeeded")
	}
	raw, _ := os.ReadFile(filepath.Join(fixture.home, "data", "facts", "note.md"))
	if string(raw) != "original knowledge" {
		t.Errorf("files were rolled back under databases that stayed restored: %q", raw)
	}
	for _, part := range []Target{fixture.hub, fixture.manager} {
		for name, database := range fixture.backend.server(part) {
			if strings.HasPrefix(name, "persona_previous_") && !strings.Contains(err.Error(), name) {
				t.Errorf("the error does not name the previous database %s (%s): %v", name, database.dump, err)
			}
		}
	}
	for _, wanted := range []string{"persona_previous_h_", "persona_previous_m_", "previous runtime files", "restore"} {
		if !strings.Contains(err.Error(), wanted) {
			t.Errorf("the error lacks %q: %v", wanted, err)
		}
	}
	assertNothingDropped(t, fixture, "persona_staged_h_")
}

// An output inside a tree the backup collects (the knowledge base, runtime
// directories) would be copied into itself with its staging directory.
func TestCreateRefusesAnOutputInsideACollectedTree(t *testing.T) {
	for _, inside := range []string{"data/backups", "data", "managers/assistant/out", "extensions/out", "skills"} {
		t.Run(inside, func(t *testing.T) {
			fixture := newBackupFixture(t, "persona_manager")
			opts := fixture.options()
			opts.Output = filepath.Join(fixture.home, filepath.FromSlash(inside), "install.backup")
			_, err := fixture.operator().Create(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), "inside") {
				t.Fatalf("create = %v", err)
			}
			if _, statErr := os.Stat(filepath.Dir(opts.Output)); statErr == nil && inside != "data" && inside != "managers/assistant/out" && inside != "skills" && inside != "data/backups" {
				t.Errorf("the output directory was created: %v", statErr)
			}
			entries, _ := filepath.Glob(filepath.Join(fixture.home, filepath.FromSlash(inside), ".persona-backup-*"))
			if len(entries) != 0 {
				t.Errorf("staging left behind: %v", entries)
			}
		})
	}
}

// Outside the collected trees (including a sibling that merely shares a prefix)
// the output is fine, even inside the runtime directory.
func TestCreateAllowsAnOutputBesideTheCollectedTrees(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	for _, dir := range []string{"backups", "data-old"} {
		opts := fixture.options()
		opts.Output = filepath.Join(fixture.home, dir, "install.backup")
		if _, err := fixture.operator().Create(context.Background(), opts); err != nil {
			t.Errorf("create into %s: %v", dir, err)
		}
	}
}

// A restore of a bundle with docker/db-password onto an install that already
// has one keeps the install's: the role's password is not part of the bundle,
// and a Compose cluster only accepts the one its own file names.
func TestRestoreKeepsTheExistingDockerPasswordFile(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	password := filepath.Join(fixture.home, "docker", "db-password")
	writeFixtureFile(t, password, []byte("old-cluster-password"))
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, password, []byte("new-cluster-password"))
	result, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(password)
	if string(raw) != "new-cluster-password" {
		t.Fatalf("docker/db-password after restore = %q", raw)
	}
	if !result.KeptDockerPassword {
		t.Error("the result does not report the kept password file")
	}
	if err := os.Remove(password); err != nil {
		t.Fatal(err)
	}
	fixture.mutate(t)
	if _, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(password)
	if string(raw) != "old-cluster-password" {
		t.Fatalf("an absent docker/db-password was not restored from the bundle: %q", raw)
	}
}
