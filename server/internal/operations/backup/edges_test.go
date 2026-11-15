package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	result, err = operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(password)
	if string(raw) != "old-cluster-password" {
		t.Fatalf("an absent docker/db-password was not restored from the bundle: %q", raw)
	}
	if result.KeptDockerPassword {
		t.Error("the result reports a kept password file, but the bundle's copy was installed")
	}
}

// Neither the bundle nor the install has the file: nothing was kept.
func TestRestoreWithoutAnyDockerPasswordFileReportsNothingKept(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	result, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.KeptDockerPassword {
		t.Error("the result reports a kept password file")
	}
	if _, err := os.Stat(filepath.Join(fixture.home, "docker", "db-password")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a password file appeared: %v", err)
	}
}

func readNote(t *testing.T, fixture *backupFixture) string {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(fixture.home, "data", "facts", "note.md"))
	return string(raw)
}

func assertDatabases(t *testing.T, fixture *backupFixture, hub, manager string) {
	t.Helper()
	if got := fixture.backend.server(fixture.hub)[fixture.hub.Database]; got == nil || got.dump != hub {
		t.Errorf("hub database = %+v, want %s", got, hub)
	}
	if got := fixture.backend.server(fixture.manager)[fixture.manager.Database]; got == nil || got.dump != manager {
		t.Errorf("manager database = %+v, want %s", got, manager)
	}
}

// The server dies before the first rename took effect: no bundle database can
// be live, so the files go back, and nothing is claimed about a previous
// database that was never made.
func TestServerDownBeforeTheFirstRenameRollsFilesBack(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	fixture.mutate(t)
	fixture.backend.preCommit, fixture.backend.preCommitDown = "hub:previous", true
	_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if err == nil || !strings.Contains(err.Error(), "injected failure before") {
		t.Fatalf("restore error = %v", err)
	}
	if got := readNote(t, fixture); got != "changed knowledge" {
		t.Errorf("files were kept under databases that are the install's own: %q", got)
	}
	if strings.Contains(err.Error(), "left as restored") || strings.Contains(err.Error(), "by hand") {
		t.Errorf("the error claims bundle databases may be live: %v", err)
	}
	assertDatabases(t, fixture, "hub-two", "manager-two")
}

// One existence check fails briefly while the server is back for the undo.
func TestBriefExistenceFailureDoesNotKeepBundleFiles(t *testing.T) {
	for _, fault := range []string{"previous", "install"} {
		t.Run(fault, func(t *testing.T) {
			fixture := newBackupFixture(t, "persona_manager")
			fixture.backend.armExistsFailures = 1
			operator := fixture.operator()
			if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
				t.Fatal(err)
			}
			fixture.mutate(t)
			fixture.backend.dropped = nil
			if fault == "previous" {
				fixture.backend.preCommit = "hub:previous"
			} else {
				fixture.backend.failInstallPart = "manager"
			}
			_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
			if err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("restore error = %v", err)
			}
			if got := readNote(t, fixture); got != "changed knowledge" {
				t.Errorf("files were kept although no bundle database is live: %q", got)
			}
			if strings.Contains(err.Error(), "left as restored") {
				t.Errorf("the error claims bundle databases may be live: %v", err)
			}
			if fault == "previous" && strings.Contains(err.Error(), "persona_previous_") {
				t.Errorf("the error names a previous database that was never made: %v", err)
			}
			assertDatabases(t, fixture, "hub-two", "manager-two")
			assertNothingDropped(t, fixture, "persona_staged_")
			assertNoTemporaryDatabases(t, fixture.backend) // the stage that never got installed is discarded
		})
	}
}

// The undo of the manager works and the hub's does not: a bundle database is
// live (the hub's), so the files stay as restored, and only the previous
// database still parked is named.
func TestPartialUndoKeepsFilesAndNamesOnlyWhatIsStillParked(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.failUndoPart = "hub"
	_, err := restoreWithFault(t, fixture, "manager:install")
	if err == nil {
		t.Fatal("restore succeeded")
	}
	if got := readNote(t, fixture); got != "original knowledge" {
		t.Errorf("files were rolled back under a live bundle database: %q", got)
	}
	assertDatabases(t, fixture, "hub-one", "manager-two") // the hub is the bundle's; the manager was put back
	var hubPrevious, managerPrevious string
	for name := range fixture.backend.server(fixture.hub) {
		if strings.HasPrefix(name, "persona_previous_h_") {
			hubPrevious = name
		}
	}
	for name := range fixture.backend.server(fixture.manager) {
		if strings.HasPrefix(name, "persona_previous_m_") {
			managerPrevious = name
		}
	}
	if hubPrevious == "" || !strings.Contains(err.Error(), "Previous hub database: "+hubPrevious) {
		t.Errorf("the error does not name the hub's previous database %q: %v", hubPrevious, err)
	}
	if managerPrevious != "" {
		t.Errorf("the manager's previous database was not renamed back: %s", managerPrevious)
	}
	if !strings.Contains(err.Error(), "previous built-in manager database: (none)") {
		t.Errorf("the error names a manager database that was already put back: %v", err)
	}
}

// The destination's existence check failing is no evidence that the rename did
// not happen.
func TestDestinationCheckFailureIsNotTakenForNotCommitted(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.existsErrName = fixture.manager.Database
	_, err := restoreWithFault(t, fixture, "manager:install")
	if err == nil {
		t.Fatal("restore succeeded")
	}
	assertDatabases(t, fixture, "hub-two", "manager-two")
	if got := readNote(t, fixture); got != "changed knowledge" {
		t.Errorf("files = %q", got)
	}
}

// A restore onto an external knowledge base names the copy it keeps.
func TestRestoreReportsThePreviousExternalKnowledgeBase(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	external := filepath.Join(t.TempDir(), "knowledge")
	writeFixtureFile(t, filepath.Join(external, "fact.md"), []byte("external original"))
	writeFixtureFile(t, filepath.Join(fixture.home, "managers", "assistant", "manager.json"), []byte(`{"database_name":"persona_manager","kb_dir":`+strconvQuote(external)+`}`))
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(external, "fact.md"), []byte("external changed"))
	result, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true, AllowExternalKB: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousExternalKB == "" {
		t.Fatal("the result does not name the previous external knowledge base")
	}
	if info, err := os.Stat(result.PreviousExternalKB); err != nil || !info.IsDir() {
		t.Fatalf("the named directory does not exist: %v", err)
	}
}

// The refusal sees through symlinks: an output reached through a link into the
// knowledge base, and a knowledge base or collected directory that is itself a
// link, are inside the collected tree.
func TestOutputRefusalResolvesSymlinks(t *testing.T) {
	for _, shape := range []string{"link-to-kb", "kb-is-link", "collected-dir-is-link"} {
		t.Run(shape, func(t *testing.T) {
			fixture := newBackupFixture(t, "persona_manager")
			outside := t.TempDir()
			var output string
			switch shape {
			case "link-to-kb":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(filepath.Join(fixture.home, "data"), link); err != nil {
					t.Skip("symlinks unavailable: ", err)
				}
				output = filepath.Join(link, "x.backup")
			case "kb-is-link":
				if err := os.RemoveAll(filepath.Join(fixture.home, "data")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(fixture.home, "data")); err != nil {
					t.Skip("symlinks unavailable: ", err)
				}
				output = filepath.Join(outside, "x.backup")
			case "collected-dir-is-link":
				if err := os.Symlink(outside, filepath.Join(fixture.home, "skills")); err != nil {
					t.Skip("symlinks unavailable: ", err)
				}
				output = filepath.Join(outside, "x.backup")
			}
			opts := fixture.options()
			opts.Output = output
			_, err := fixture.operator().Create(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), "inside") {
				t.Fatalf("create = %v", err)
			}
			if entries, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".persona-backup-*")); len(entries) != 0 {
				t.Errorf("staging left behind: %v", entries)
			}
		})
	}
}

// The aside rename worked, the install rename failed without happening, the
// check of it failed and so did the re-check: the install is taken for
// committed. The previous database then renames back, which proves the target
// name was free, so no bundle database is live and the files must go back.
func TestSuccessfulPreviousUndoProvesNoBundleDatabaseIsLive(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	fixture.mutate(t)
	fixture.backend.failInstallPart = "manager"
	fixture.backend.armExistsFailures = 2 // the commit check and the re-check
	_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("restore error = %v", err)
	}
	if got := readNote(t, fixture); got != "changed knowledge" {
		t.Errorf("files were kept although both live databases are the install's own: %q", got)
	}
	if strings.Contains(err.Error(), "may be live") {
		t.Errorf("the error claims a bundle database may be live: %v", err)
	}
	assertDatabases(t, fixture, "hub-two", "manager-two")
	assertNoTemporaryDatabases(t, fixture.backend) // and the stage is not left behind
}

// A successfully undone install is no longer installed, even when another part
// of the undo failed. The hub has no previous database in the second case, so
// nothing but the undo of its install can clear it.
func TestUndoneInstallIsNotCountedAsLive(t *testing.T) {
	for _, hubExisted := range []bool{true, false} {
		t.Run(fmt.Sprintf("hub-existed=%v", hubExisted), func(t *testing.T) {
			fixture := newBackupFixture(t, "persona_manager")
			operator := fixture.operator()
			if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
				t.Fatal(err)
			}
			fixture.mutate(t)
			if !hubExisted {
				delete(fixture.backend.server(fixture.hub), fixture.hub.Database)
			}
			// The manager's aside rename fails without happening and both of its
			// checks fail, so its undo cannot tell; the hub's install is undone fine.
			fixture.backend.preCommit, fixture.backend.armExistsFailures = "manager:previous", 2
			_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true})
			if err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("restore error = %v", err)
			}
			if got := readNote(t, fixture); got != "changed knowledge" {
				t.Errorf("files were kept although no bundle database is live: %q", got)
			}
			if hubExisted {
				assertDatabases(t, fixture, "hub-two", "manager-two")
			} else if got := fixture.backend.server(fixture.manager)[fixture.manager.Database]; got == nil || got.dump != "manager-two" {
				t.Errorf("manager database = %+v", got)
			}
		})
	}
}

// A previous database whose existence cannot be checked is not taken for
// absent: it stays named, so the operator can find it.
func TestParkedDatabaseWhoseCheckFailsStaysNamed(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.failUndoPart = "manager"
	fixture.backend.existsErrPrefix = "persona_previous_m_"
	_, err := restoreWithFault(t, fixture, "manager:install")
	if err == nil {
		t.Fatal("restore succeeded")
	}
	parked := ""
	for name := range fixture.backend.server(fixture.manager) {
		if strings.HasPrefix(name, "persona_previous_m_") {
			parked = name
		}
	}
	if parked == "" {
		t.Fatal("no previous manager database is parked")
	}
	if !strings.Contains(err.Error(), "previous built-in manager database: "+parked) {
		t.Errorf("the parked database %s is not named: %v", parked, err)
	}
}

// plainlyAbsentBeside says yes only when it established that the name is gone
// and the other exists.
func TestPlainlyAbsentBesideNeedsBothFactsEstablished(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	backend := fixture.backend
	operator := fixture.operator()
	target := fixture.hub
	server := backend.server(target)
	check := func(label string, want bool) {
		t.Helper()
		if got := operator.plainlyAbsentBeside(context.Background(), target, "gone", "persona"); got != want {
			t.Errorf("%s: plainlyAbsentBeside = %v, want %v", label, got, want)
		}
	}
	check("name absent, other present", true)
	delete(server, "persona")
	check("both absent", false)
	server["persona"] = &fakeDatabase{}
	server["gone"] = &fakeDatabase{}
	check("name present", false)
	delete(server, "gone")
	backend.existsFailures = 1
	check("the check of the name fails", false)
	backend.existsFailures = 0
	backend.commitThenFired, backend.existsErrName = true, "persona"
	check("the check of the other fails", false)
}

// No external knowledge base existed before the restore: nothing was kept, so
// no empty directory is left and none is reported.
func TestRestoreLeavesNoEmptyPreviousExternalKnowledgeBase(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	parent := t.TempDir()
	external := filepath.Join(parent, "knowledge")
	writeFixtureFile(t, filepath.Join(external, "fact.md"), []byte("external original"))
	writeFixtureFile(t, filepath.Join(fixture.home, "managers", "assistant", "manager.json"), []byte(`{"database_name":"persona_manager","kb_dir":`+strconvQuote(external)+`}`))
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(external); err != nil {
		t.Fatal(err)
	}
	result, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output, WritesQuiesced: true, AllowExternalKB: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousExternalKB != "" {
		t.Errorf("an empty previous external knowledge base is reported: %s", result.PreviousExternalKB)
	}
	if left, _ := filepath.Glob(filepath.Join(parent, ".*persona-backup-previous-*")); len(left) != 0 {
		t.Errorf("an empty previous-copy directory is left: %v", left)
	}
	if raw, _ := os.ReadFile(filepath.Join(external, "fact.md")); string(raw) != "external original" {
		t.Errorf("the knowledge base was not restored: %q", raw)
	}
}
