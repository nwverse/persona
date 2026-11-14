package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/nwverse/persona/server/internal/secretkey"
)

// Operator coordinates bundle, database, and file transactions.
type Operator struct {
	backend Backend
	now     func() time.Time
	random  io.Reader
}

func NewOperator() *Operator {
	return &Operator{backend: newPostgresBackend(), now: time.Now, random: rand.Reader}
}

func (o *Operator) Create(ctx context.Context, opts Options) (_ Result, err error) {
	if !opts.WritesQuiesced {
		return Result{}, errors.New("create requires --writes-quiesced after stopping the hub and built-in manager")
	}
	if opts.Output == "" {
		return Result{}, errors.New("create requires --output")
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return Result{}, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(output), ".persona-backup-stage-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)
	hub, manager, err := Discover(opts)
	if err != nil {
		return Result{}, err
	}
	for _, target := range []Target{hub, manager} {
		if err := o.backend.Quiesced(ctx, target, target.Database, false); err != nil {
			return Result{}, err
		}
	}
	hubIdentity, err := o.backend.Identity(ctx, hub, "hub", hub.Database)
	if err != nil {
		return Result{}, err
	}
	managerIdentity, err := o.backend.Identity(ctx, manager, "manager", manager.Database)
	if err != nil {
		return Result{}, err
	}
	// Sealed values are only for verification, not part of the identity kept.
	managerIdentity.SealedValues = nil
	manifest := &Manifest{
		Format: formatVersion, CreatedAt: o.now().UTC(), Hub: sortedIdentities(hubIdentity), Manager: sortedIdentities(managerIdentity),
		HubDatabase: hub.Database, ManagerDatabase: manager.Database,
	}
	if err := os.MkdirAll(filepath.Join(staging, "db"), 0o700); err != nil {
		return Result{}, err
	}
	if err := o.backend.Dump(ctx, hub, filepath.Join(staging, hubDumpPath)); err != nil {
		return Result{}, err
	}
	if err := o.backend.Dump(ctx, manager, filepath.Join(staging, managerDumpPath)); err != nil {
		return Result{}, err
	}
	for _, target := range []Target{hub, manager} {
		if err := o.backend.Quiesced(ctx, target, target.Database, false); err != nil {
			return Result{}, fmt.Errorf("writes resumed during backup: %w", err)
		}
	}
	if err := collectPayload(opts.Home, staging, manifest); err != nil {
		return Result{}, err
	}
	if err := writeManifest(staging, manifest); err != nil {
		return Result{}, err
	}
	if err := o.verifyPayload(ctx, hub, manager, staging, manifest); err != nil {
		return Result{}, fmt.Errorf("temporary-database verification failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := publishBundle(ctx, staging, output); err != nil {
		return Result{}, err
	}
	return Result{Bundle: output, Verified: true}, nil
}

func (o *Operator) Verify(ctx context.Context, opts Options) (Result, error) {
	if opts.Bundle == "" {
		return Result{}, errors.New("verify requires --bundle")
	}
	bundle, err := filepath.Abs(opts.Bundle)
	if err != nil {
		return Result{}, err
	}
	manifest, _, err := readBundleManifest(bundle)
	if err != nil {
		return Result{}, err
	}
	opts.Manifest = manifest
	staging, err := os.MkdirTemp("", "persona-backup-verify-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)
	if err := extractBundle(bundle, staging, manifest); err != nil {
		return Result{}, err
	}
	opts.BundleHome = staging
	hub, manager, err := Discover(opts)
	if err != nil {
		return Result{}, err
	}
	if err := o.verifyPayload(ctx, hub, manager, staging, manifest); err != nil {
		return Result{}, fmt.Errorf("temporary-database verification failed: %w", err)
	}
	return Result{Bundle: bundle, Verified: true}, nil
}

func (o *Operator) Restore(ctx context.Context, opts Options) (Result, error) {
	if !opts.WritesQuiesced {
		return Result{}, errors.New("restore requires --writes-quiesced after stopping the hub and built-in manager")
	}
	if opts.Bundle == "" {
		return Result{}, errors.New("restore requires --bundle")
	}
	bundle, err := filepath.Abs(opts.Bundle)
	if err != nil {
		return Result{}, err
	}
	manifest, _, err := readBundleManifest(bundle)
	if err != nil {
		return Result{}, err
	}
	opts.Manifest = manifest
	staging, err := os.MkdirTemp("", "persona-backup-restore-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)
	if err := extractBundle(bundle, staging, manifest); err != nil {
		return Result{}, err
	}
	opts.BundleHome = staging
	hub, manager, err := Discover(opts)
	if err != nil {
		return Result{}, err
	}
	if hub.Database != manifest.HubDatabase || manager.Database != manifest.ManagerDatabase {
		return Result{}, errors.New("the target database names differ from the bundle; use the original names so the restored configuration and databases agree")
	}
	if err := verifyPayloadKeyFiles(staging, manifest); err != nil {
		return Result{}, err
	}
	managerKeys, err := bundledManagerKeys(staging)
	if err != nil {
		return Result{}, err
	}
	for _, target := range []Target{hub, manager} {
		if err := o.backend.Quiesced(ctx, target, target.Database, true); err != nil {
			return Result{}, err
		}
	}
	hubStage, err := o.stageDatabase(ctx, hub, filepath.Join(staging, hubDumpPath), manifest.Hub, manifest.ManagerKeyFingerprint, nil, "h")
	if err != nil {
		return Result{}, err
	}
	defer o.discardStage(hub, hubStage)
	managerStage, err := o.stageDatabase(ctx, manager, filepath.Join(staging, managerDumpPath), manifest.Manager, manifest.ManagerKeyFingerprint, managerKeys, "m")
	if err != nil {
		return Result{}, err
	}
	defer o.discardStage(manager, managerStage)
	id, err := o.identifier()
	if err != nil {
		return Result{}, err
	}
	previousFiles, rollbackFiles, err := applyFiles(ctx, opts.Home, staging, manifest, opts.AllowExternalKB, id)
	if err != nil {
		return Result{}, err
	}
	for _, target := range []Target{hub, manager} {
		if err := o.backend.Quiesced(ctx, target, target.Database, true); err != nil {
			return Result{}, errors.Join(err, rollbackFiles())
		}
	}
	previous, err := o.swapDatabases(ctx, hub, manager, hubStage, managerStage)
	if err != nil {
		return Result{}, errors.Join(err, rollbackFiles())
	}
	return Result{Bundle: bundle, Verified: true, Restored: true, PreviousHubDB: previous[0], PreviousManagerDB: previous[1], PreviousFiles: previousFiles}, nil
}

type stagedDatabase struct {
	name   string
	active bool
}

func (o *Operator) discardStage(target Target, staged *stagedDatabase) {
	if staged == nil || !staged.active {
		return
	}
	ctx, cancel := cleanupContext()
	defer cancel()
	_ = o.backend.Drop(ctx, target, staged.name)
}

func (o *Operator) stageDatabase(ctx context.Context, target Target, dump string, expected DatabaseIdentity, keyFingerprint string, keys [][]byte, role string) (*stagedDatabase, error) {
	name, err := o.uniqueName(ctx, target, "persona_staged_"+role+"_")
	if err != nil {
		return nil, err
	}
	staged := &stagedDatabase{name: name, active: true}
	if err := o.backend.Create(ctx, target, name); err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			o.discardStage(target, staged)
		}
	}()
	if err := o.backend.Restore(ctx, target, name, dump); err != nil {
		return nil, err
	}
	actual, err := o.backend.Identity(ctx, target, target.Part, name)
	if err != nil {
		return nil, err
	}
	if err := compareIdentity(expected, actual, target.Part, keyFingerprint, keys); err != nil {
		return nil, err
	}
	keep = true
	return staged, nil
}

func verifyPayloadKeyFiles(staging string, manifest *Manifest) error {
	hubKey, err := secretkey.ReadKey(filepath.Join(staging, hubKeyPath))
	if err != nil {
		return errors.New("the bundled hub secret.key is invalid")
	}
	managerKey, err := secretkey.ReadKey(filepath.Join(staging, managerKeyPath))
	if err != nil {
		return errors.New("the bundled built-in manager secret.key is invalid")
	}
	if secretkey.Fingerprint(hubKey) != manifest.HubKeyFingerprint || secretkey.Fingerprint(managerKey) != manifest.ManagerKeyFingerprint {
		return errors.New("the bundled encryption keys do not match their manifest fingerprints")
	}
	return nil
}

// bundledManagerKeys are the keys the built-in manager opens its secrets with:
// its secret.key and, while a rotation keeps them, the ones in secret.key.prev
// (secretkey.NewWithFallback).
func bundledManagerKeys(staging string) ([][]byte, error) {
	current, err := secretkey.ReadKey(filepath.Join(staging, managerKeyPath))
	if err != nil {
		return nil, errors.New("the bundled built-in manager secret.key is invalid")
	}
	previous, err := secretkey.ReadKeys(secretkey.PrevPath(filepath.Join(staging, managerKeyPath)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("the bundled built-in manager secret.key.prev is invalid")
	}
	return append([][]byte{current}, previous...), nil
}

func (o *Operator) verifyPayload(ctx context.Context, hub, manager Target, staging string, manifest *Manifest) error {
	if err := verifyPayloadKeyFiles(staging, manifest); err != nil {
		return err
	}
	managerKeys, err := bundledManagerKeys(staging)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		target   Target
		dump     string
		expected DatabaseIdentity
		key      string
		keys     [][]byte
		role     string
	}{
		{hub, hubDumpPath, manifest.Hub, manifest.HubKeyFingerprint, nil, "h"},
		{manager, managerDumpPath, manifest.Manager, manifest.ManagerKeyFingerprint, managerKeys, "m"},
	} {
		if err := o.verifyOne(ctx, item.target, filepath.Join(staging, item.dump), item.expected, item.key, item.keys, item.role); err != nil {
			return err
		}
	}
	return nil
}

func (o *Operator) verifyOne(ctx context.Context, target Target, dump string, expected DatabaseIdentity, key string, keys [][]byte, role string) (returnErr error) {
	name, err := o.uniqueName(ctx, target, "persona_verify_"+role+"_")
	if err != nil {
		return err
	}
	if err := o.backend.Create(ctx, target, name); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := cleanupContext()
		defer cancel()
		returnErr = errors.Join(returnErr, o.backend.Drop(cleanupCtx, target, name))
	}()
	if err := o.backend.Restore(ctx, target, name, dump); err != nil {
		return err
	}
	actual, err := o.backend.Identity(ctx, target, target.Part, name)
	if err != nil {
		return err
	}
	return compareIdentity(expected, actual, target.Part, key, keys)
}

// compareIdentity checks the restored schema, and for the built-in manager that
// the bundled key is the one its secrets are sealed with: by the sealing_key
// record when there is one, otherwise (before its first rotation) by opening the
// sealed values themselves. Values that open with no key are already unreadable
// on the source install, as a rotation leaves them, so one that opens suffices;
// none opening means the bundle carries the wrong key.
func compareIdentity(expected, actual DatabaseIdentity, part, keyFingerprint string, keys [][]byte) error {
	expected, actual = sortedIdentities(expected), sortedIdentities(actual)
	if !reflect.DeepEqual(expected.Schema, actual.Schema) {
		return fmt.Errorf("%s schema identity differs after restore", part)
	}
	if part == "manager" {
		if expected.SealingKey != actual.SealingKey {
			return errors.New("the built-in manager encryption-key identity differs after restore")
		}
		if actual.SealingKey != "" && actual.SealingKey != keyFingerprint {
			return errors.New("the restored built-in manager database does not match the bundled current secret.key")
		}
		if actual.SealingKey == "" && !anySealedValueOpens(actual.SealedValues, keys) {
			return fmt.Errorf("the built-in manager database records no encryption key, and none of its %d sealed secret(s) opens with the bundled secret.key", len(actual.SealedValues))
		}
	}
	return nil
}

func anySealedValueOpens(values []string, keys [][]byte) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		for _, key := range keys {
			if _, err := secretkey.OpenWith(key, value); err == nil {
				return true
			}
		}
	}
	return false
}

func (o *Operator) swapDatabases(ctx context.Context, hub, manager Target, hubStage, managerStage *stagedDatabase) ([2]string, error) {
	var previous [2]string
	type swapRecord struct {
		target        Target
		staged        *stagedDatabase
		previous      string
		previousMoved bool
		installed     bool
	}
	var records []*swapRecord
	rollback := func() error {
		ctx, cancel := cleanupContext()
		defer cancel()
		var errs []error
		for i := len(records) - 1; i >= 0; i-- {
			record := records[i]
			if record.installed {
				if err := o.backend.Rename(ctx, record.target, record.target.Database, record.staged.name); err != nil {
					errs = append(errs, err)
				} else {
					record.staged.active = true
				}
			}
			if record.previousMoved {
				if err := o.backend.Rename(ctx, record.target, record.previous, record.target.Database); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return errors.Join(errs...)
	}
	for index, item := range []struct {
		target Target
		staged *stagedDatabase
		role   string
	}{{hub, hubStage, "h"}, {manager, managerStage, "m"}} {
		if err := ctx.Err(); err != nil {
			return previous, errors.Join(err, rollback())
		}
		exists, err := o.backend.Exists(ctx, item.target, item.target.Database)
		if err != nil {
			return previous, errors.Join(err, rollback())
		}
		record := &swapRecord{target: item.target, staged: item.staged}
		records = append(records, record)
		if exists {
			record.previous, err = o.uniqueName(ctx, item.target, "persona_previous_"+item.role+"_")
			if err != nil {
				return previous, errors.Join(err, rollback())
			}
			if err := o.backend.Rename(ctx, item.target, item.target.Database, record.previous); err != nil {
				return previous, errors.Join(err, rollback())
			}
			record.previousMoved = true
			previous[index] = record.previous
		}
		if err := o.backend.Rename(ctx, item.target, item.staged.name, item.target.Database); err != nil {
			return previous, errors.Join(err, rollback())
		}
		item.staged.active = false
		record.installed = true
	}
	return previous, nil
}

func (o *Operator) uniqueName(ctx context.Context, target Target, prefix string) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := o.identifier()
		if err != nil {
			return "", err
		}
		name := prefix + id
		if len(name) > 63 {
			return "", errors.New("internal temporary database name exceeds PostgreSQL's identifier limit")
		}
		exists, err := o.backend.Exists(ctx, target, name)
		if err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
	}
	return "", errors.New("could not allocate a unique temporary database name")
}

func (o *Operator) identifier() (string, error) {
	bytes := make([]byte, 8)
	if _, err := io.ReadFull(o.random, bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
