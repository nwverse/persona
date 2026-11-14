package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/secretkey"
)

var optionalRootFiles = []string{
	"secret.key.prev", "tailcat-hub.json", "handles.key", "apns.json", "apns_key.p8",
	"tailcat.json", "link.json", "daemons.json", "docker/db-password",
}

var optionalRuntimeDirectories = []string{"extensions", "skills", "hub.machines_kept"}

// userContentDirectories hold the owner's files (installed extensions and
// skills), copied in full like the knowledge base: a name such as yarn.lock or
// meeting.log there is content, not a runtime artifact.
var userContentDirectories = map[string]bool{"extensions": true, "skills": true}

func collectPayload(home, staging string, manifest *Manifest) error {
	home, err := filepath.Abs(home)
	if err != nil {
		return err
	}
	managerDir := filepath.Join(home, "managers", "assistant")
	if own := managerSecretFile(home); !samePath(own, managerconf.OwnFile(home, managerconf.SecretKey)) {
		return errors.New("the built-in manager still reads the hub's secret.key; finish its key-file migration before backup")
	}
	if err := copyTree(managerDir, filepath.Join(staging, "runtime", "managers", "assistant"), true); err != nil {
		return fmt.Errorf("collect the built-in manager files: %w", err)
	}
	manifest.RuntimeDirectories = append(manifest.RuntimeDirectories, "managers/assistant")
	if err := copyRequired(filepath.Join(home, "hub.json"), filepath.Join(staging, "runtime", "hub.json")); err != nil {
		return err
	}
	if err := copyRequired(filepath.Join(home, "secret.key"), filepath.Join(staging, "runtime", "secret.key")); err != nil {
		return err
	}
	for _, name := range optionalRootFiles {
		if err := copyOptional(filepath.Join(home, filepath.FromSlash(name)), filepath.Join(staging, "runtime", filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	nextKeys, err := filepath.Glob(filepath.Join(home, "secret.key.next-*"))
	if err != nil {
		return err
	}
	for _, name := range nextKeys {
		if err := copyOptional(name, filepath.Join(staging, "runtime", filepath.Base(name))); err != nil {
			return err
		}
	}
	for _, name := range optionalRuntimeDirectories {
		source := filepath.Join(home, name)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("runtime path %s is not a directory", name)
		}
		if err := copyTree(source, filepath.Join(staging, "runtime", name), !userContentDirectories[name]); err != nil {
			return fmt.Errorf("collect %s: %w", name, err)
		}
		manifest.RuntimeDirectories = append(manifest.RuntimeDirectories, name)
	}
	locator, kbDir, err := knowledgeBaseLocator(home)
	if err != nil {
		return err
	}
	manifest.KnowledgeBase = locator
	if info, err := os.Lstat(kbDir); errors.Is(err, os.ErrNotExist) {
		manifest.KnowledgeBase.Present = false
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return errors.New("the knowledge-base path is not a directory")
	} else {
		manifest.KnowledgeBase.Present = true
		if err := copyTree(kbDir, filepath.Join(staging, "kb"), false); err != nil {
			return fmt.Errorf("collect the knowledge base: %w", err)
		}
	}
	for _, required := range []string{"runtime/managers/assistant/manager.json", managerKeyPath} {
		name := filepath.Join(staging, filepath.FromSlash(required))
		if info, err := os.Lstat(name); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("required runtime file %s is missing or not regular", required)
		}
		if err := os.Chmod(name, 0o600); err != nil {
			return err
		}
	}
	hubKey, err := secretkey.ReadKey(filepath.Join(home, "secret.key"))
	if err != nil {
		return fmt.Errorf("read the hub encryption key: %w", err)
	}
	managerKey, err := secretkey.ReadKey(managerSecretFile(home))
	if err != nil {
		return fmt.Errorf("read the built-in manager encryption key: %w", err)
	}
	manifest.HubKeyFingerprint = secretkey.Fingerprint(hubKey)
	manifest.ManagerKeyFingerprint = secretkey.Fingerprint(managerKey)
	if manifest.Manager.SealingKey != "" && manifest.Manager.SealingKey != manifest.ManagerKeyFingerprint {
		return errors.New("the built-in manager database names a different secret.key; finish or roll back its key rotation before backup")
	}
	return nil
}

func managerSecretFile(home string) string {
	return filepath.Join(home, "managers", "assistant", "secret.key")
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

func copyRequired(source, destination string) error {
	if err := copyFile(source, destination); err != nil {
		return fmt.Errorf("collect required file %s: %w", filepath.Base(source), err)
	}
	return os.Chmod(destination, 0o600)
}

func copyOptional(source, destination string) error {
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return copyFile(source, destination)
}

func ephemeralRuntimeFile(name string) bool {
	base := filepath.Base(name)
	return base == ".lock" || strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, ".pid") ||
		strings.HasSuffix(base, ".sock") || strings.HasSuffix(base, ".log") || strings.Contains(base, ".tmp-")
}

// copyTree copies the regular files under source. Sockets are skipped
// everywhere (live endpoints, not data); skipRuntime also skips lock, pid, log
// and temporary files, and is only for directories the product itself writes,
// never for the owner's files.
func copyTree(source, destination string, skipRuntime bool) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real directory", source)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == source {
			return nil
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symbolic-link directory %s is not supported", name)
			}
			return os.MkdirAll(filepath.Join(destination, rel), 0o700)
		}
		if entry.Type()&os.ModeSocket != 0 || (skipRuntime && ephemeralRuntimeFile(name)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular runtime file %s is not supported", name)
		}
		return copyFile(name, filepath.Join(destination, rel))
	})
}

func copyFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", source)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 != 0 {
		if err := out.Chmod(0o700); err != nil {
			out.Close()
			return err
		}
	}
	_, copyErr := io.Copy(out, in)
	if syncErr := out.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

type restoreItem struct {
	source  string
	target  string
	runtime bool
}

type restoreRecord struct {
	item          restoreItem
	backup        string
	temporary     string
	previousMoved bool
	installed     bool
}

type fileTransaction struct {
	id           string
	backupRoot   string
	externalRoot string
	records      []*restoreRecord
}

// applyFiles stages every target beside itself before renaming anything. The
// previous files remain in mode-0700 transaction directories after success.
func applyFiles(ctx context.Context, home, staging string, manifest *Manifest, allowExternal bool, id string) (previous string, rollback func() error, returnErr error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return "", nil, err
	}
	items, err := restoreItems(home, staging, manifest, allowExternal)
	if err != nil {
		return "", nil, err
	}
	transaction := &fileTransaction{id: id, backupRoot: filepath.Join(home, ".persona-backup-previous-"+id)}
	if err := os.MkdirAll(transaction.backupRoot, 0o700); err != nil {
		return "", nil, err
	}
	committed := false
	defer func() {
		if !committed {
			returnErr = errors.Join(returnErr, transaction.rollback())
		}
	}()
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		if err := transaction.apply(item); err != nil {
			return "", nil, err
		}
	}
	committed = true
	if emptyDirectory(transaction.backupRoot) {
		_ = os.Remove(transaction.backupRoot)
		transaction.backupRoot = ""
	}
	return transaction.backupRoot, transaction.rollback, nil
}

func restoreItems(home, staging string, manifest *Manifest, allowExternal bool) ([]restoreItem, error) {
	var items []restoreItem
	for _, name := range []string{"hub.json", "secret.key"} {
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", name), target: filepath.Join(home, name), runtime: true})
	}
	for _, name := range optionalRootFiles {
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", filepath.FromSlash(name)), target: filepath.Join(home, filepath.FromSlash(name)), runtime: true})
	}
	nextKeys, _ := filepath.Glob(filepath.Join(home, "secret.key.next-*"))
	for _, name := range nextKeys {
		base := filepath.Base(name)
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", base), target: name, runtime: true})
	}
	for _, entry := range manifest.Files {
		if !strings.HasPrefix(entry.Path, "runtime/") {
			continue
		}
		rel := strings.TrimPrefix(entry.Path, "runtime/")
		if strings.Contains(rel, "/") || rel == "hub.json" || rel == "secret.key" || rel == "secret.key.prev" {
			continue
		}
		if strings.HasPrefix(rel, "secret.key.next-") {
			found := false
			for _, item := range items {
				if item.target == filepath.Join(home, rel) {
					found = true
				}
			}
			if !found {
				items = append(items, restoreItem{source: filepath.Join(staging, "runtime", rel), target: filepath.Join(home, rel), runtime: true})
			}
		}
	}
	for _, name := range append([]string{"managers/assistant"}, optionalRuntimeDirectories...) {
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", filepath.FromSlash(name)), target: filepath.Join(home, filepath.FromSlash(name)), runtime: true})
	}
	kbTarget := ""
	if manifest.KnowledgeBase.External != "" {
		if !allowExternal {
			return nil, errors.New("this bundle has an external knowledge base; rerun with --allow-external-kb to replace that directory")
		}
		kbTarget = manifest.KnowledgeBase.External
	} else {
		rel := manifest.KnowledgeBase.Relative
		if rel == "" {
			rel = "data"
		}
		kbTarget = filepath.Join(home, filepath.FromSlash(rel))
		// Only an approved external knowledge base may lie outside the runtime directory.
		if inside, err := filepath.Rel(home, kbTarget); err != nil || inside == "." || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return nil, errors.New("the knowledge-base relative path leaves the runtime directory")
		}
	}
	if samePath(kbTarget, home) || samePath(kbTarget, filepath.Dir(home)) || filepath.Clean(kbTarget) == string(filepath.Separator) {
		return nil, errors.New("refusing an unsafe knowledge-base restore target")
	}
	kbSource := filepath.Join(staging, "kb")
	if manifest.KnowledgeBase.Present {
		if err := os.MkdirAll(kbSource, 0o700); err != nil {
			return nil, err
		}
	}
	items = append(items, restoreItem{source: kbSource, target: kbTarget, runtime: manifest.KnowledgeBase.External == ""})
	return items, nil
}

func (t *fileTransaction) apply(item restoreItem) error {
	record := &restoreRecord{item: item}
	t.records = append(t.records, record)
	if _, err := os.Lstat(item.source); err == nil {
		parent := filepath.Dir(item.target)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
		record.temporary = filepath.Join(parent, fmt.Sprintf(".%s.persona-new-%s-%d", filepath.Base(item.target), t.id, len(t.records)))
		if err := copyRecursive(item.source, record.temporary); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	backupRoot := t.backupRoot
	if !item.runtime {
		if t.externalRoot == "" {
			t.externalRoot = filepath.Join(filepath.Dir(item.target), fmt.Sprintf(".%s.persona-backup-previous-%s", filepath.Base(item.target), t.id))
			if err := os.MkdirAll(t.externalRoot, 0o700); err != nil {
				return err
			}
		}
		backupRoot = t.externalRoot
	}
	record.backup = filepath.Join(backupRoot, fmt.Sprintf("%03d-%s", len(t.records), filepath.Base(item.target)))
	if _, err := os.Lstat(item.target); err == nil {
		if err := os.MkdirAll(filepath.Dir(record.backup), 0o700); err != nil {
			return err
		}
		if err := os.Rename(item.target, record.backup); err != nil {
			return fmt.Errorf("preserve %s before restore: %w", filepath.Base(item.target), err)
		}
		record.previousMoved = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if record.temporary != "" {
		if err := os.Rename(record.temporary, item.target); err != nil {
			return fmt.Errorf("install staged %s: %w", filepath.Base(item.target), err)
		}
		record.installed = true
	}
	if !record.previousMoved && !record.installed {
		return nil
	}
	return secretkey.SyncDir(filepath.Dir(item.target))
}

func (t *fileTransaction) rollback() error {
	var errs []error
	for i := len(t.records) - 1; i >= 0; i-- {
		record := t.records[i]
		if record.installed {
			if err := os.RemoveAll(record.item.target); err != nil {
				errs = append(errs, err)
			}
		}
		if record.previousMoved {
			if err := os.Rename(record.backup, record.item.target); err != nil {
				errs = append(errs, err)
			}
		}
		if record.temporary != "" {
			_ = os.RemoveAll(record.temporary)
		}
	}
	if len(errs) == 0 {
		_ = os.RemoveAll(t.backupRoot)
		if t.externalRoot != "" {
			_ = os.RemoveAll(t.externalRoot)
		}
	}
	return errors.Join(errs...)
}

func copyRecursive(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		return copyFile(source, destination)
	}
	if !info.IsDir() {
		return errors.New("staged restore input is not a regular file or directory")
	}
	return copyTree(source, destination, false)
}

func emptyDirectory(name string) bool {
	entries, err := os.ReadDir(name)
	return err == nil && len(entries) == 0
}
