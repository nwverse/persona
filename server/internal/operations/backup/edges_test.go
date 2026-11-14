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
