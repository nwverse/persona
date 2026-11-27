package db

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
)

// Each snapshot represents a different manager. Even two writes in the same
// microsecond must not let a handoff restore consent or settings from a stale one.
func TestExtensionBuiltinHandoffCAS(t *testing.T) {
	d, ctx := open(t), t.Context()
	const id = "builtin-cas-test"
	cleanupStorageFixture(t, d, id)
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	builtin := extension.Record{
		ID: id, Source: extension.SourceBuiltin, Version: "1.0.0", Manifest: json.RawMessage("{}"),
		Config: json.RawMessage("{}"), Enabled: true, InstalledAt: now, UpdatedAt: now,
	}
	read := func() extension.Record {
		t.Helper()
		rows, err := d.Extensions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(rows, func(r extension.Record) bool { return r.StorageKey() == id })
		if i < 0 {
			t.Fatal("missing built-in storage slot")
		}
		return rows[i]
	}
	write := func(r extension.Record, want error) {
		t.Helper()
		if err := d.PutExtension(ctx, r); !errors.Is(err, want) {
			t.Fatalf("write %s: expected %v, got %v", r.Source, want, err)
		}
	}
	cutover := func(before extension.Record, source string) extension.Record {
		r := before
		r.Source = source
		r.ExpectedSource, r.ExpectedID, r.ExpectedVersion = before.Source, before.ID, before.Version
		r.ExpectedUpdatedAt = before.UpdatedAt
		r.BuiltinPredecessor = source == extension.SourceInstalled
		return r
	}

	write(builtin, nil)
	first := read()
	if first.BuiltinPredecessor {
		t.Fatal("fresh built-in incorrectly marked as a replacement")
	}
	if err := d.PutExtensionSecret(ctx, id, "key", "sealed-fixture"); err != nil {
		t.Fatal(err)
	}
	// A built-in owns business data in the same stable slot as its credentials.
	if _, err := d.pool.Exec(ctx, `INSERT INTO extension_storage_spaces(id, plugin, extension_key, installed_at, state)
		VALUES ('builtin-cas-test-space', $1, $1, $2, '{"notes":"retained"}')`, id, first.InstalledAt); err != nil {
		t.Fatal(err)
	}
	edited := first
	edited.ExpectedSource = extension.SourceBuiltin
	edited.Config = json.RawMessage(`{"city":"new"}`)
	edited.Enabled = false
	edited.DisabledContributions = []string{"ui/summary"}
	write(edited, nil)

	write(cutover(first, extension.SourceInstalled), extension.ErrSettingsMoved)
	current := read()
	if current.Source != extension.SourceBuiltin || !current.UpdatedAt.After(first.UpdatedAt) {
		t.Fatal("first cutover overwrote a concurrently edited built-in")
	}
	pv := cutover(current, extension.SourceInstalled)
	pv.GrantedDigest = "new-permission-digest"
	write(pv, nil)
	write(pv, extension.ErrIdentity)
	write(edited, extension.ErrIdentity)
	installed := read()
	if !installed.BuiltinPredecessor || installed.Enabled || !slices.Equal(installed.DisabledContributions, edited.DisabledContributions) || installed.GrantedDigest != pv.GrantedDigest {
		t.Fatalf("replacement lost the marker or owner choices: %+v", installed)
	}
	fresh := pv
	fresh.ExpectedID, fresh.ExpectedSource, fresh.NewInstallation = "", "", true
	write(fresh, extension.ErrIdentity)

	rollback := cutover(installed, extension.SourceBuiltin)
	revised := installed
	revised.ExpectedSource = extension.SourceInstalled
	revised.GrantedDigest = "revised-grant"
	revised.Config = json.RawMessage(`{"city":"latest"}`)
	write(revised, nil)
	write(rollback, extension.ErrSettingsMoved)
	latest := read()
	if latest.Source != extension.SourceInstalled || latest.GrantedDigest != revised.GrantedDigest {
		t.Fatal("stale rollback changed the installed grant")
	}
	write(cutover(latest, extension.SourceBuiltin), nil)
	write(rollback, extension.ErrIdentity)
	write(revised, extension.ErrIdentity)
	if err := d.DeleteExtensionRecord(ctx, installed); !errors.Is(err, extension.ErrIdentity) {
		t.Fatalf("stale uninstall reached a restored built-in of the same version: %v", err)
	}
	for _, keepData := range []bool{true, false} {
		if err := d.DeleteExtensionRecordRetainingData(ctx, installed, keepData); !errors.Is(err, extension.ErrIdentity) {
			t.Fatalf("stale uninstall with keepData=%v: %v", keepData, err)
		}
	}
	var notes string
	if err := d.pool.QueryRow(ctx, `SELECT state->>'notes' FROM extension_storage_spaces
		WHERE plugin=$1 AND installed_at=$2 AND NOT retained`, id, first.InstalledAt).Scan(&notes); err != nil || notes != "retained" {
		t.Fatalf("rollback lost its active storage space: %v", err)
	}
	restored := read()
	var config map[string]string
	if err := json.Unmarshal(restored.Config, &config); err != nil || config["city"] != "latest" ||
		restored.BuiltinPredecessor || restored.Source != extension.SourceBuiltin || restored.Enabled {
		t.Fatalf("rollback did not retain current settings and switches: %+v", restored)
	}
	secrets, err := d.ExtensionSecrets(ctx, id)
	if err != nil || secrets["key"] != "sealed-fixture" {
		t.Fatalf("secret did not survive cutover and rollback: %v", err)
	}
}
