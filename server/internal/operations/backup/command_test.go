package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandErrorsRedactDiscoveredSecrets(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	fixture.backend.dumpErr = errors.New("dump " + fixture.hub.URL + " used hub-secret with hub-token-secret and usage-secret")
	fixture.backend.dumpPart = "hub"
	var stdout, stderr bytes.Buffer
	err := runWithOperator(context.Background(), fixture.operator(), []string{"create", "--home", fixture.home, "--output", fixture.output, "--writes-quiesced"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected the injected dump failure")
	}
	output := err.Error() + stdout.String() + stderr.String()
	for _, secret := range []string{fixture.hub.URL, "hub-secret", "hub-token-secret", "usage-secret"} {
		if strings.Contains(output, secret) {
			t.Errorf("output contains %q: %s", secret, output)
		}
	}
	if !strings.Contains(output, "<redacted>") {
		t.Fatalf("error does not show redaction: %q", output)
	}
}

func TestCreateAndRestoreRequireExplicitQuiescence(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	if _, err := fixture.operator().Create(context.Background(), Options{Home: fixture.home, Output: fixture.output}); err == nil || !strings.Contains(err.Error(), "--writes-quiesced") {
		t.Fatalf("create without quiescence = %v", err)
	}
	if _, err := fixture.operator().Restore(context.Background(), Options{Home: fixture.home, Bundle: fixture.output}); err == nil || !strings.Contains(err.Error(), "--writes-quiesced") {
		t.Fatalf("restore without quiescence = %v", err)
	}
}

func TestCommandUsageIsTopLevel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runWithOperator(context.Background(), fixturelessOperator{}, nil, &stdout, &stderr)
	var usage string
	if err == nil || !strings.Contains(err.Error(), "personad backup create") {
		t.Fatalf("missing usage = %v", err)
	}
	usage = Usage
	for _, command := range []string{"create", "verify", "restore"} {
		if !strings.Contains(usage, "personad backup "+command) {
			t.Errorf("usage lacks %s", command)
		}
	}
}

type fixturelessOperator struct{}

func (fixturelessOperator) Create(context.Context, Options) (Result, error)  { return Result{}, nil }
func (fixturelessOperator) Verify(context.Context, Options) (Result, error)  { return Result{}, nil }
func (fixturelessOperator) Restore(context.Context, Options) (Result, error) { return Result{}, nil }

func TestBundleRoundTripAndSHAMismatch(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	if _, err := fixture.operator().Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readBundleManifest(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	if err := extractBundle(fixture.output, staging, manifest); err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Files {
		info, err := os.Stat(filepath.Join(staging, filepath.FromSlash(entry.Path)))
		if err != nil || info.Mode().Perm() != os.FileMode(entry.Mode) {
			t.Errorf("extracted %s mode = %v, %v", entry.Path, info.Mode(), err)
		}
	}
	if err := os.WriteFile(filepath.Join(staging, hubDumpPath), []byte("tamper!"), 0o600); err != nil {
		t.Fatal(err)
	}
	tampered := filepath.Join(t.TempDir(), "tampered.backup")
	if err := publishBundle(context.Background(), staging, tampered); err != nil {
		t.Fatal(err)
	}
	tamperedManifest, _, err := readBundleManifest(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := extractBundle(tampered, t.TempDir(), tamperedManifest); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered bundle verification = %v", err)
	}
}

func TestBundleRejectsTraversalIndependentManagersAndWrongMode(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	if _, err := fixture.operator().Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readBundleManifest(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	bad := *manifest
	bad.Files = append([]FileEntry(nil), manifest.Files...)
	bad.Files[0].Path = "../escape"
	if err := bad.validate(); err == nil {
		t.Fatal("path traversal was accepted")
	}
	independent := *manifest
	independent.Files = append(append([]FileEntry(nil), manifest.Files...), FileEntry{Path: "runtime/managers/m-abcdef/secret.key", Size: 1, SHA256: strings.Repeat("0", 64), Mode: 0o600})
	if err := independent.validate(); err == nil {
		t.Fatal("an independently hosted manager was accepted")
	}
	if err := os.Chmod(fixture.output, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readBundleManifest(fixture.output); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("group-readable bundle was accepted: %v", err)
	}
}

func TestCanceledPublicationLeavesNoBundle(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readBundleManifest(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	if err := extractBundle(fixture.output, staging, manifest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output := filepath.Join(t.TempDir(), "interrupted.backup")
	if err := publishBundle(ctx, staging, output); !errors.Is(err, context.Canceled) {
		t.Fatalf("publication interruption = %v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted publication left an output: %v", err)
	}
}

func TestArchiveContainsOnlyRegularOwnerOnlyFiles(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	if _, err := fixture.operator().Create(context.Background(), fixture.options()); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || header.Mode&0o077 != 0 {
			t.Errorf("member %s type %d mode %#o", header.Name, header.Typeflag, header.Mode&0o777)
		}
		if header.Name == "runtime/hub.json" && header.Mode&0o777 != 0o600 {
			t.Errorf("secret configuration mode = %#o", header.Mode&0o777)
		}
		if strings.HasPrefix(header.Name, "runtime/managers/m-") {
			t.Errorf("independent manager entered bundle: %s", header.Name)
		}
	}
}
