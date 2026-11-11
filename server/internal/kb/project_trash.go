package kb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// A deleted project is soft-deleted (#4412): its directory moves to the data dir's .trash/<id>/project, and the chats,
// people and items that named it stop naming it. trash.json next to it records which ones, so a restore puts the
// project and exactly those links back.

// ErrConflict: a restore whose slug a newer project took meanwhile.
var ErrConflict = errors.New("conflict")

const trashDirName = ".trash"

// TrashEntry is one deleted project, as kb-trash lists it.
type TrashEntry struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	DeletedAt time.Time `json:"deleted_at"`
	// Unlinked are the "<kind>/<id>" entities that named the project when it was deleted.
	Unlinked []string `json:"unlinked,omitempty"`
}

func (s *Store) trashPath(id string, parts ...string) string {
	return filepath.Join(append([]string{s.Dir, trashDirName, SafeID(id)}, parts...)...)
}

// DeleteProject moves project slug to the trash and unlinks it. Merged and unknown projects are ErrNotFound.
func (e *Engine) DeleteProject(slug string, now time.Time) (TrashEntry, error) {
	if e.Store.ReadOnly() {
		return TrashEntry{}, ErrReadOnly
	}
	e.Store.applyMu.Lock()
	defer e.Store.applyMu.Unlock()
	p, ok := e.Store.Project(slug)
	if !ok || !p.Live() {
		return TrashEntry{}, fmt.Errorf("%s: %w", slug, ErrNotFound)
	}
	entry := TrashEntry{ID: fmt.Sprintf("%s-%d", p.Slug, now.UnixMilli()), Kind: KindProject, Slug: p.Slug, Name: p.Name, DeletedAt: now.UTC()}
	if err := os.MkdirAll(e.Store.trashPath(entry.ID), 0o755); err != nil {
		return TrashEntry{}, err
	}
	if err := os.Rename(e.Store.path(KindProject, p.Slug), e.Store.trashPath(entry.ID, "project")); err != nil {
		_ = os.Remove(e.Store.trashPath(entry.ID))
		return TrashEntry{}, err
	}
	// Moved first, so the pipeline can't write the project back while its links go.
	err := e.relinkRefs(p, true, func(ref string, list []string) ([]string, bool) {
		if !slices.Contains(list, p.Slug) {
			return list, false
		}
		entry.Unlinked = append(entry.Unlinked, ref)
		return removeStrings(list, []string{p.Slug}), true
	})
	if werr := writeJSON(e.Store.trashPath(entry.ID, "trash.json"), entry); err == nil {
		err = werr
	}
	if err != nil {
		return TrashEntry{}, err
	}
	e.unindex(KindProject, []string{p.Slug})
	e.Store.notify(KindProject, p.Slug, "metadata")
	return entry, nil
}

// Trash lists deleted projects, newest first.
func (e *Engine) Trash() []TrashEntry {
	dirs, _ := os.ReadDir(filepath.Join(e.Store.Dir, trashDirName))
	out := []TrashEntry{}
	for _, d := range dirs {
		var t TrashEntry
		if ok, err := readJSON(e.Store.trashPath(d.Name(), "trash.json"), &t); ok && err == nil && t.ID == d.Name() {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out
}

// RestoreProject puts a deleted project and its links back. ErrNotFound: no such trash entry; ErrConflict: a project
// with its slug exists again.
func (e *Engine) RestoreProject(id string) (TrashEntry, error) {
	if e.Store.ReadOnly() {
		return TrashEntry{}, ErrReadOnly
	}
	e.Store.applyMu.Lock()
	defer e.Store.applyMu.Unlock()
	var entry TrashEntry
	if ok, err := readJSON(e.Store.trashPath(id, "trash.json"), &entry); err != nil {
		return TrashEntry{}, err
	} else if !ok || entry.ID != id || entry.Kind != KindProject {
		return TrashEntry{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if _, err := os.Stat(e.Store.path(KindProject, entry.Slug)); err == nil {
		return TrashEntry{}, fmt.Errorf("%s: %w", entry.Slug, ErrConflict)
	}
	if err := os.MkdirAll(filepath.Join(e.Store.Dir, KindProject), 0o755); err != nil {
		return TrashEntry{}, err
	}
	if err := os.Rename(e.Store.trashPath(id, "project"), e.Store.path(KindProject, entry.Slug)); err != nil {
		return TrashEntry{}, err
	}
	p, ok := e.Store.Project(entry.Slug)
	if !ok {
		return TrashEntry{}, fmt.Errorf("%s: %w", entry.Slug, ErrNotFound)
	}
	// Only the recorded links come back, onto the entities that still exist.
	err := e.relinkRefs(p, true, func(ref string, list []string) ([]string, bool) {
		if !slices.Contains(entry.Unlinked, ref) || slices.Contains(list, p.Slug) {
			return list, false
		}
		return append(list, p.Slug), true
	})
	if err != nil {
		return TrashEntry{}, err
	}
	if err := os.RemoveAll(e.Store.trashPath(id)); err != nil {
		return TrashEntry{}, err
	}
	e.index(KindProject, p.Slug, p.Name, p.Tagging, p.OneLine, p.Updated)
	e.Store.notify(KindProject, p.Slug, "metadata")
	return entry, nil
}
