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
	if err := refuseKnowledgeBaseOverlap(home, kbDir); err != nil {
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
		return errors.New("the built-in manager database names a different secret.key than the one on disk, usually because its key rotation is unfinished (a secret.key.next-* file is pending). " +
			"If so, start the built-in manager once so it finishes the rotation (it resumes at startup), then stop it and retry; the rotation cannot be rolled back after its commit")
	}
	return nil
}

// runtimeTargets are the paths under home that restore replaces, other than
// the knowledge base.
func runtimeTargets(home string) []string {
	targets := []string{filepath.Join(home, "hub.json"), filepath.Join(home, "secret.key"), filepath.Join(home, "managers", "assistant")}
	for _, name := range append(append([]string(nil), optionalRootFiles...), optionalRuntimeDirectories...) {
		targets = append(targets, filepath.Join(home, filepath.FromSlash(name)))
	}
	return targets
}

// pathWithin reports whether child is parent or lies under it.
func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// refuseKnowledgeBaseOverlap refuses a knowledge base that would hold the
// runtime directory (home, its parent or any ancestor, /): replacing it would
// move the whole install, the previous-files directory with it, into the
// knowledge base's previous copy. It also refuses one that is, holds, or lies
// inside a runtime file or directory restore replaces as well (hub.json,
// managers/assistant, extensions...), which would push the item just restored
// into the previous files, and one inside a previous-files directory. Paths
// are compared as given and with their symbolic links resolved.
func refuseKnowledgeBaseOverlap(home, kb string) error {
	forms := func(name string) []string {
		return []string{filepath.Clean(name), resolveExisting(name)}
	}
	for _, kbForm := range forms(kb) {
		for _, homeForm := range forms(home) {
			if pathWithin(kbForm, homeForm) {
				return fmt.Errorf("refusing the knowledge base %s: it holds the runtime directory %s, so restoring it would move the whole install into its previous copy. "+
					"Nothing was changed; restore into a runtime directory outside it, or set kb_dir to a directory of its own", kb, home)
			}
			if rel, err := filepath.Rel(homeForm, kbForm); err == nil && strings.HasPrefix(strings.Split(filepath.ToSlash(rel), "/")[0], ".persona-backup-previous-") {
				return fmt.Errorf("refusing the knowledge base %s: it lies inside a previous-files directory of an earlier restore. Nothing was changed", kb)
			}
		}
		for _, target := range runtimeTargets(home) {
			for _, targetForm := range forms(target) {
				if pathWithin(targetForm, kbForm) || pathWithin(kbForm, targetForm) {
					return fmt.Errorf("refusing the knowledge base %s: it overlaps %s, which the backup holds and restore replaces as well. "+
						"Nothing was changed; set kb_dir to a directory of its own, outside managers/assistant, extensions, skills and hub.machines_kept (the default is data)", kb, target)
				}
			}
		}
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

// renameFile and removeAll are os.Rename and os.RemoveAll; tests replace them
// to make the file transaction's steps fail.
var (
	renameFile = os.Rename
	removeAll  = os.RemoveAll
)

type restoreItem struct {
	source  string
	target  string
	runtime bool
	// absent: the manifest records that the bundle's install had none of this
	// (a knowledge base with Present false). With no staged source, the
	// install's copy is moved aside into the previous files with nothing in
	// its place. Without the record, an item with no staged source is left
	// untouched: the bundle cannot tell an absent file from one never
	// collected.
	absent bool
	// empty: a runtime directory the manifest lists whose staged copy holds
	// nothing; it is installed as an empty directory.
	empty bool
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
	home         string
	backupRoot   string
	externalRoot string
	records      []*restoreRecord
}

// applyFiles installs what the bundle has, each copy staged beside its target
// before the target is renamed aside. What the manifest records as absent or
// empty is moved aside with nothing (or an empty directory) in its place, and
// listed in movedAside; what the bundle has no copy or record of stays as it
// is. The replaced files remain in mode-0700 transaction directories after
// success.
func applyFiles(ctx context.Context, home, staging string, manifest *Manifest, allowExternal bool, id string) (previous, previousExternal string, movedAside []string, rollback func() error, returnErr error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return "", "", nil, nil, err
	}
	items, err := restoreItems(home, staging, manifest, allowExternal)
	if err != nil {
		return "", "", nil, nil, err
	}
	transaction := &fileTransaction{id: id, home: home, backupRoot: filepath.Join(home, ".persona-backup-previous-"+id)}
	if err := os.MkdirAll(transaction.backupRoot, 0o700); err != nil {
		return "", "", nil, nil, err
	}
	committed := false
	defer func() {
		if !committed {
			returnErr = errors.Join(returnErr, transaction.rollback())
		}
	}()
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return "", "", nil, nil, err
		}
		if err := transaction.apply(item); err != nil {
			return "", "", nil, nil, err
		}
	}
	committed = true
	if emptyDirectory(transaction.backupRoot) {
		_ = os.Remove(transaction.backupRoot)
		transaction.backupRoot = ""
	}
	if transaction.externalRoot != "" && emptyDirectory(transaction.externalRoot) {
		_ = os.Remove(transaction.externalRoot)
		transaction.externalRoot = ""
	}
	return transaction.backupRoot, transaction.externalRoot, transaction.movedAside(), transaction.rollback, nil
}

// movedAside names what was moved into the previous files with nothing (or an
// empty directory) in its place.
func (t *fileTransaction) movedAside() []string {
	var names []string
	for _, record := range t.records {
		if !record.previousMoved || !(record.item.absent || record.item.empty) {
			continue
		}
		name := record.item.target
		if rel, err := filepath.Rel(t.home, name); err == nil && record.item.runtime {
			name = filepath.ToSlash(rel)
		}
		names = append(names, name)
	}
	return names
}

// dockerPasswordKept reports whether restore leaves the install's own
// docker/db-password in place although the bundle carries one.
func dockerPasswordKept(home string, manifest *Manifest) bool {
	for _, entry := range manifest.Files {
		if entry.Path == "runtime/docker/db-password" {
			_, err := os.Lstat(filepath.Join(home, "docker", "db-password"))
			return err == nil
		}
	}
	return false
}

func restoreItems(home, staging string, manifest *Manifest, allowExternal bool) ([]restoreItem, error) {
	var items []restoreItem
	for _, name := range []string{"hub.json", "secret.key"} {
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", name), target: filepath.Join(home, name), runtime: true})
	}
	for _, name := range optionalRootFiles {
		// A Docker install's database password stays: the role's password is
		// not part of the bundle, so the new or surviving cluster only accepts
		// the one its own file names, and a bundle's copy (from the cluster it
		// was taken on) would stop the containers from starting. Only an install
		// with none gets the bundle's.
		if name == "docker/db-password" {
			if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(name))); err == nil {
				continue
			}
		}
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", filepath.FromSlash(name)), target: filepath.Join(home, filepath.FromSlash(name)), runtime: true})
	}
	// A pending rotation key (secret.key.next-<fingerprint>) may be the only copy
	// of a key: one the install has is never moved, and the bundle's is installed
	// only under a name the install does not have (the name is the key's
	// fingerprint, so a file of the same name holds the same key).
	for _, entry := range manifest.Files {
		rel, ok := strings.CutPrefix(entry.Path, "runtime/")
		if !ok || strings.Contains(rel, "/") || !strings.HasPrefix(rel, "secret.key.next-") {
			continue
		}
		target := filepath.Join(home, rel)
		if _, err := os.Lstat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		items = append(items, restoreItem{source: filepath.Join(staging, "runtime", rel), target: target, runtime: true})
	}
	listed := map[string]bool{}
	for _, name := range manifest.RuntimeDirectories {
		listed[name] = true
	}
	for _, name := range append([]string{"managers/assistant"}, optionalRuntimeDirectories...) {
		item := restoreItem{source: filepath.Join(staging, "runtime", filepath.FromSlash(name)), target: filepath.Join(home, filepath.FromSlash(name)), runtime: true}
		// A directory the manifest lists existed in the bundle's install; with
		// no file staged it was empty there (a bundle holds files only), and is
		// restored empty rather than left as this install has it.
		// (restoreItems may run more than once on one staging directory, so an
		// empty staged directory counts as none.)
		if listed[name] {
			if _, err := os.Lstat(item.source); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			} else if err != nil || emptyDirectory(item.source) {
				item.empty = true
				if err := os.MkdirAll(item.source, 0o700); err != nil {
					return nil, err
				}
			}
		}
		items = append(items, item)
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
	if err := refuseKnowledgeBaseOverlap(home, kbTarget); err != nil {
		return nil, err
	}
	kbSource := filepath.Join(staging, "kb")
	if manifest.KnowledgeBase.Present {
		if err := os.MkdirAll(kbSource, 0o700); err != nil {
			return nil, err
		}
	}
	// Present false records that the bundle's install had no knowledge base.
	items = append(items, restoreItem{source: kbSource, target: kbTarget, runtime: manifest.KnowledgeBase.External == "", absent: !manifest.KnowledgeBase.Present})
	return items, nil
}

func (t *fileTransaction) apply(item restoreItem) error {
	staged := true
	if _, err := os.Lstat(item.source); errors.Is(err, os.ErrNotExist) {
		// What the bundle has no copy and no record of is not the bundle's to
		// replace: it stays in the live install, untouched.
		if !item.absent {
			return nil
		}
		staged = false
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(item.target); errors.Is(err, os.ErrNotExist) && !staged {
		return nil // recorded absent, and absent here too
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record := &restoreRecord{item: item}
	t.records = append(t.records, record)
	parent := filepath.Dir(item.target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if staged {
		record.temporary = filepath.Join(parent, fmt.Sprintf(".%s.persona-new-%s-%d", filepath.Base(item.target), t.id, len(t.records)))
		if err := copyRecursive(item.source, record.temporary); err != nil {
			return err
		}
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
		if err := renameFile(item.target, record.backup); err != nil {
			return fmt.Errorf("preserve %s before restore: %w", filepath.Base(item.target), err)
		}
		record.previousMoved = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if staged {
		if err := renameFile(record.temporary, item.target); err != nil {
			return fmt.Errorf("install staged %s: %w", filepath.Base(item.target), err)
		}
		record.installed = true
	}
	return secretkey.SyncDir(filepath.Dir(item.target))
}

// rollback puts every original back. The previous-files directories are
// removed only once empty: an original that could not be put back stays in
// them, and the error names it, its directory, and what to do.
func (t *fileTransaction) rollback() error {
	var errs []error
	for i := len(t.records) - 1; i >= 0; i-- {
		record := t.records[i]
		if record.installed {
			if err := removeAll(record.item.target); err != nil {
				if record.previousMoved {
					errs = append(errs, fmt.Errorf("remove the restored %s so that its original can go back: %w", record.item.target, err))
				} else {
					errs = append(errs, fmt.Errorf("remove %s, which the restore installed and this install did not have: %w; remove it by hand", record.item.target, err))
				}
			} else {
				record.installed = false
			}
		}
		if record.previousMoved {
			if err := renameFile(record.backup, record.item.target); err != nil {
				errs = append(errs, fmt.Errorf("put the original %s back from %s: %w", record.item.target, record.backup, err))
			} else {
				record.previousMoved = false
			}
		}
		if record.temporary != "" {
			_ = os.RemoveAll(record.temporary)
		}
	}
	var kept []string
	for _, root := range []string{t.backupRoot, t.externalRoot} {
		if root == "" {
			continue
		}
		if err := os.Remove(root); err != nil && !errors.Is(err, os.ErrNotExist) {
			kept = append(kept, root)
		}
	}
	if len(kept) > 0 {
		errs = append(errs, fmt.Errorf("the file rollback is incomplete; the originals it could not put back are preserved in %s, each as NNN-<name> (NNN is the order restore replaced them in). "+
			"Move each back to its place by hand (removing what the restore installed there first), then delete the emptied directory", strings.Join(kept, " and ")))
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
