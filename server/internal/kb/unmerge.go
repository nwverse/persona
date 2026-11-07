package kb

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Undoing a project merge (#34): dedupe merged projects that were not the same work, and the chats, people and
// items of the one merged away then named the wrong project.

// Unmerge undoes, as the user, the merge that folded the project id names (its slug, or its name) into another
// (unmergeProject). Returns its slug.
func (e *Engine) Unmerge(id string) (string, error) {
	e.Store.applyMu.Lock()
	defer e.Store.applyMu.Unlock()
	p := e.mergedProject(id)
	if p == nil {
		return "", fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return p.Slug, e.unmergeProject(p)
}

// mergedProject is the project id names for an unmerge: the one with that slug, else the most recently merged one
// with that name, else the live one FindProject gives (which unmergeProject refuses as not merged).
func (e *Engine) mergedProject(id string) *Project {
	if p, ok := e.Store.Project(id); ok {
		return p
	}
	n := normName(id)
	var merged *Project
	for _, p := range e.Store.Projects() {
		if !p.Live() && n != "" && (normName(p.Name) == n || p.Slug == Slug(id)) {
			merged = preferProject(merged, p)
		}
	}
	if merged != nil {
		return merged
	}
	return e.FindProject(id)
}

// unmergeProject undoes the merge that folded drop into another project (MergeProjects, merge_into): drop lists again
// with its status from before, and its chats, people and items name it again.
//
// A recorded merge (drop.Merge) is undone exactly: the aliases, people, chats, items, code directories, keywords and
// Moments posts it gave the kept project leave it, and the chats, people and items it moved from drop name drop
// alone again. A merge made before the record leaves no way to tell which of drop's people, chats and items the kept
// project had already, and it usually had many (dedupe pairs projects by the people and chats they share): they stay
// on the kept project too (remove_people / remove_chats take them off), and only drop's name and aliases leave it,
// since a live project's alias must not be another live project's name or alias.
//
// Either way drop's lines leave the kept project's timeline (drop's own still has them), both projects are marked
// dirty so their profiles (goal, kind, status, tags) are rewritten from what each now has, and dedupe never merges
// the pair again.
func (e *Engine) unmergeProject(drop *Project) error {
	if drop.Live() {
		return fmt.Errorf("%s is not merged", drop.Slug)
	}
	keep, found := e.Store.Project(drop.MergedInto)
	if found && !keep.Live() {
		return fmt.Errorf("%s went into %s, which was merged into %s since: unmerge %s first", drop.Slug, keep.Slug, keep.MergedInto, keep.Slug)
	}
	rec, now := drop.Merge, time.Now().UTC()
	if found {
		if rec != nil {
			keep.Aliases = removeStrings(keep.Aliases, rec.Aliases)
			keep.People = removeStrings(keep.People, rec.People)
			keep.Chats = removeStrings(keep.Chats, rec.Chats)
			keep.Items = removeStrings(keep.Items, rec.Items)
			keep.Dirs = removeStrings(keep.Dirs, rec.Dirs)
			keep.Keywords = removeStrings(keep.Keywords, rec.Keywords)
			keep.Moments = slices.DeleteFunc(slices.Clone(keep.Moments), func(m ProjectMoment) bool { return slices.Contains(rec.Moments, m.ID) })
			// Pinned facts edited since the merge are the user's newer word and stay.
			if rec.Pinned != nil && keep.Pinned == joinPinned(*rec.Pinned, drop.Pinned) {
				keep.Pinned = *rec.Pinned
			}
		} else {
			names := map[string]bool{normName(drop.Name): true}
			for _, a := range drop.Aliases {
				names[normName(a)] = true
			}
			keep.Aliases = slices.DeleteFunc(slices.Clone(keep.Aliases), func(a string) bool { return names[normName(a)] })
		}
		if err := e.unmergeLogs(keep.Slug, drop.Slug); err != nil {
			return err
		}
		_ = e.Store.AppendLog(keep.Slug, now, "Persona", fmt.Sprintf("拆开了项目「%s」", drop.Name))
		keep.Dirty, keep.Updated = true, now
		if err := e.Store.putEntity(KindProject, keep.Slug, keep, &keep.Tagging, true); err != nil {
			return err
		}
		e.index(KindProject, keep.Slug, keep.Name, keep.Tagging, keep.OneLine, keep.Updated)
	}
	// drop's chats, people and items name it again; the ones the merge moved stop naming the kept project.
	moved := map[string]bool{}
	if rec != nil && found {
		for _, ref := range rec.Moved {
			moved[ref] = true
		}
	}
	relink := func(ref string, list []string) ([]string, bool) {
		out := addUnique(slices.Clone(list), drop.Slug)
		if moved[ref] {
			out = removeStrings(out, []string{keep.Slug})
		}
		return out, !slices.Equal(out, list)
	}
	for _, id := range drop.Chats {
		if c, ok := e.Store.Chat(id); ok {
			if l, ch := relink(KindChat+"/"+id, c.Projects); ch {
				c.Projects = l
				if err := e.Store.putEntity(KindChat, c.ID, c, &c.Tagging, true); err != nil {
					return err
				}
			}
		}
	}
	for _, id := range drop.People {
		if p, ok := e.Store.Person(id); ok {
			if l, ch := relink(KindPerson+"/"+id, p.Projects); ch {
				p.Projects = l
				if err := e.Store.putEntity(KindPerson, p.ID, p, &p.Tagging, true); err != nil {
					return err
				}
			}
		}
	}
	for _, ref := range drop.Items {
		kind, id, ok := strings.Cut(ref, "/")
		if !ok {
			continue
		}
		if it, ok := e.Store.Item(kind, id); ok {
			if l, ch := relink(ref, it.Projects); ch {
				it.Projects = l
				if err := e.Store.putEntity(it.Kind, it.ID, it, &it.Tagging, true); err != nil {
					return err
				}
			}
		}
	}
	drop.Status = "active"
	if rec != nil && rec.Status != "" && rec.Status != "merged" {
		drop.Status = rec.Status
	}
	drop.MergedInto, drop.Merge, drop.Dirty, drop.Updated = "", nil, true, now
	if err := e.Store.putEntity(KindProject, drop.Slug, drop, &drop.Tagging, true); err != nil {
		return err
	}
	e.index(KindProject, drop.Slug, drop.Name, drop.Tagging, drop.OneLine, drop.Updated)
	if found {
		if err := e.Store.putDedupeVerdict(pairKey(keep.Slug, drop.Slug), dedupeVerdict{Split: true, Reason: "拆开了", At: now}); err != nil {
			return err
		}
	}
	return nil
}

// unmergeLogs takes drop's lines out of keep's timeline, where mergeLogs put them. A line both had before the merge
// (same minute, source and text) goes too; drop's timeline keeps it.
func (e *Engine) unmergeLogs(keepSlug, dropSlug string) error {
	theirs := map[string]bool{}
	for _, l := range e.Store.Log(dropSlug, math.MaxInt) {
		theirs[l] = true
	}
	if len(theirs) == 0 {
		return nil
	}
	lines := e.Store.Log(keepSlug, math.MaxInt)
	kept := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return theirs[l] })
	if len(kept) == len(lines) {
		return nil
	}
	return e.Store.WriteLog(keepSlug, kept)
}
