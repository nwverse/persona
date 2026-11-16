package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A knowledge-base target that holds the runtime directory (home, its parent,
// a grandparent or higher, /) would move the whole install, the previous-files
// directory with it, into its own previous copy. One that is, holds, or lies
// inside a restored runtime item (hub.json, managers, managers/assistant,
// extensions...) would push the item just restored into the previous files.
// restoreItems refuses both before anything is changed.
func TestRestoreItemsRefuseAKnowledgeBaseOverlappingTheRuntime(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "srv", "persona", "runtime")
	if err := os.MkdirAll(home, 0o700); err != nil {
		fatal(t, err)
	}
	for _, external := range []string{
		filepath.Join(root, "srv"), // grandparent
		root,                       // higher
		filepath.Join(root, "srv", "persona"),
		home,
		string(filepath.Separator),
		filepath.Join(home, "hub.json"),
		filepath.Join(home, "managers"),
		filepath.Join(home, "managers", "assistant"),
		filepath.Join(home, "managers", "assistant", "kb"),
		filepath.Join(home, "extensions", "x"),
		filepath.Join(home, ".persona-backup-previous-x"),
	} {
		manifest := &Manifest{KnowledgeBase: KnowledgeBaseLocator{External: external, Present: true}}
		_, err := restoreItems(home, t.TempDir(), manifest, true)
		switch {
		case err == nil:
			errorf(t, "restoreItems accepted external knowledge base %s", external)
		case !strings.Contains(err.Error(), "knowledge base") || !strings.Contains(err.Error(), "Nothing was changed"):
			errorf(t, "unclear refusal for %s: %v", external, err)
		case pathWithin(external, home) && !strings.Contains(err.Error(), "holds the runtime directory"):
			errorf(t, "the refusal of %s does not say it holds the runtime directory: %v", external, err)
		}
	}
	for _, relative := range []string{"hub.json", "secret.key", "managers", "managers/assistant", "managers/assistant/kb", "skills", "skills/notes", "hub.machines_kept", ".persona-backup-previous-1/data"} {
		manifest := &Manifest{KnowledgeBase: KnowledgeBaseLocator{Relative: relative, Present: true}}
		if _, err := restoreItems(home, t.TempDir(), manifest, true); err == nil {
			errorf(t, "restoreItems accepted relative knowledge base %q", relative)
		}
	}
	// Through a symbolic link as well: the runtime is reached through a link,
	// the knowledge base is named by its real path, and only the resolved paths
	// show that one holds the other.
	link := filepath.Join(t.TempDir(), "link-to-srv")
	if err := os.Symlink(filepath.Join(root, "srv"), link); err == nil {
		for _, external := range []string{filepath.Join(root, "srv"), filepath.Join(root, "srv", "persona", "runtime", "managers")} {
			manifest := &Manifest{KnowledgeBase: KnowledgeBaseLocator{External: external, Present: true}}
			if _, err := restoreItems(filepath.Join(link, "persona", "runtime"), t.TempDir(), manifest, true); err == nil {
				errorf(t, "restoreItems accepted %s for a runtime reached through a symbolic link", external)
			}
		}
	}
	// The ordinary layouts stay accepted.
	for _, manifest := range []*Manifest{
		{KnowledgeBase: KnowledgeBaseLocator{Present: true}},
		{KnowledgeBase: KnowledgeBaseLocator{Relative: "data", Present: true}},
		{KnowledgeBase: KnowledgeBaseLocator{Relative: "kb/notes", Present: true}},
		{KnowledgeBase: KnowledgeBaseLocator{External: filepath.Join(root, "knowledge"), Present: true}},
		{KnowledgeBase: KnowledgeBaseLocator{External: filepath.Join(root, "srv", "persona", "knowledge"), Present: true}},
	} {
		if _, err := restoreItems(home, t.TempDir(), manifest, true); err != nil {
			errorf(t, "restoreItems refused knowledge base %+v: %v", manifest.KnowledgeBase, err)
		}
	}
}

// Through the operator: the refusal comes before any database is staged and
// before any file is moved, with a message that names the knowledge base.
func TestRestoreRefusesAKnowledgeBaseHoldingTheRuntimeUpFront(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	// home becomes <tmp>/srv/persona/runtime, the example of the review.
	deep := filepath.Join(t.TempDir(), "srv", "persona")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		fatal(t, err)
	}
	if err := os.Rename(fixture.home, filepath.Join(deep, "runtime")); err != nil {
		fatal(t, err)
	}
	fixture.home = filepath.Join(deep, "runtime")
	operator := fixture.operator()
	if _, err := operator.Create(context.Background(), fixture.options()); err != nil {
		fatal(t, err)
	}
	fixture.mutate(t)
	for _, external := range []string{filepath.Dir(deep), filepath.Join(fixture.home, "managers")} {
		tampered := rewriteManifest(t, fixture.output, func(m *Manifest) {
			m.KnowledgeBase = KnowledgeBaseLocator{External: external, Present: true}
		})
		created := len(fixture.backend.created)
		_, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: tampered, WritesQuiesced: true, AllowExternalKB: true})
		if err == nil || !strings.Contains(err.Error(), "knowledge base") || !strings.Contains(err.Error(), external) {
			fatalf(t, "restore with external knowledge base %s = %v", external, err)
		}
		if len(fixture.backend.created) != created {
			errorf(t, "databases were staged before the layout was refused: %v", fixture.backend.created[created:])
		}
		if got := readNote(t, fixture); got != "changed knowledge" {
			errorf(t, "the knowledge base changed: %q", got)
		}
		if left, _ := filepath.Glob(filepath.Join(fixture.home, ".persona-backup-previous-*")); len(left) != 0 {
			errorf(t, "a previous-files directory was made: %v", left)
		}
		if _, err := os.Stat(filepath.Join(fixture.home, "hub.json")); err != nil {
			errorf(t, "the runtime moved: %v", err)
		}
		assertDatabases(t, fixture, "hub-two", "manager-two")
	}
	// The normal external knowledge base beside the runtime is still restored.
	external := filepath.Join(deep, "knowledge")
	writeFixtureFile(t, filepath.Join(external, "fact.md"), []byte("external changed"))
	normal := rewriteManifest(t, fixture.output, func(m *Manifest) {
		m.KnowledgeBase = KnowledgeBaseLocator{External: external, Present: true}
	})
	result, err := operator.Restore(context.Background(), Options{Home: fixture.home, Bundle: normal, WritesQuiesced: true, AllowExternalKB: true})
	if err != nil {
		fatal(t, err)
	}
	assertFile(t, filepath.Join(external, "facts", "note.md"), []byte("original knowledge"))
	if result.PreviousExternalKB == "" || filepath.Dir(result.PreviousExternalKB) != deep {
		errorf(t, "previous external knowledge base %q is not beside it in %s", result.PreviousExternalKB, deep)
	}
	kept, _ := filepath.Glob(filepath.Join(result.PreviousExternalKB, "*-knowledge", "fact.md"))
	if len(kept) != 1 {
		fatalf(t, "the previous external knowledge base is not kept in %s: %v", result.PreviousExternalKB, kept)
	}
	assertFile(t, kept[0], []byte("external changed"))
}

// Create refuses a knowledge base inside a collected runtime directory: the
// bundle could not be restored (restore refuses the overlap).
func TestCreateRefusesAKnowledgeBaseInsideARuntimeDirectory(t *testing.T) {
	fixture := newBackupFixture(t, "persona_manager")
	writeFixtureFile(t, filepath.Join(fixture.home, "managers", "assistant", "manager.json"), []byte(`{"database_name":"persona_manager","kb_dir":"kb"}`))
	writeFixtureFile(t, filepath.Join(fixture.home, "managers", "assistant", "kb", "fact.md"), []byte("knowledge"))
	_, err := fixture.operator().Create(context.Background(), fixture.options())
	if err == nil || !strings.Contains(err.Error(), "knowledge base") {
		fatalf(t, "create with kb_dir inside managers/assistant = %v", err)
	}
	if _, statErr := os.Stat(fixture.output); !errors.Is(statErr, os.ErrNotExist) {
		errorf(t, "a bundle was written: %v", statErr)
	}
}
