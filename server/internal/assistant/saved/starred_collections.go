package saved

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
)

const (
	starCollectionsKey     = "assistant.starred_collections"
	starCollectionsPath    = starPath + "/collections"
	starCollectionsMax     = 50
	starCollectionNameMax  = 64
	starRevisionMax        = 1<<53 - 1 // JSON numbers must stay exact in browser clients.
	starCollectionBodyMax  = 64 << 10
	starCollectionIDPrefix = "sc"
)

type namedStarCollection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Keep this document independent of the legacy snapshot setting. An older
// manager may continue to write stars without dropping collection metadata.
type starCollectionsDocument struct {
	Revision    int64                 `json:"revision"`
	Collections []namedStarCollection `json:"collections"`
	Assignments map[string]string     `json:"assignments"`
}

type starCollectionsResponse struct {
	starCollectionsDocument
	CanWrite bool `json:"can_write"`
}

var (
	errCollectionInput     = errors.New("invalid starred collection request")
	errCollectionConflict  = errors.New("starred collections changed")
	errCollectionDuplicate = errors.New("duplicate starred collection")
	errCollectionLimit     = errors.New("starred collection limit reached")
	errCollectionMissing   = errors.New("starred collection not found")
	errCollectionStarGone  = errors.New("starred message not found")
)

func validCollectionID(id string) bool {
	digits, ok := strings.CutPrefix(id, starCollectionIDPrefix)
	return ok && len(digits) == 32 && strings.Trim(digits, "0123456789abcdef") == ""
}

func collectionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !savedSearchText(name) || utf8.RuneCountInString(name) > starCollectionNameMax {
		return "", errCollectionInput
	}
	return name, nil
}

func collectionRevision(raw string) (int64, error) {
	if raw == "" || strings.Trim(raw, "0123456789") != "" || len(raw) > 1 && raw[0] == '0' {
		return 0, errCollectionInput
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision >= starRevisionMax {
		return 0, errCollectionInput
	}
	return revision, nil
}

func loadStarCollections(ctx context.Context, st revisionedSettings) (starCollectionsDocument, error) {
	var doc starCollectionsDocument
	exists, err := st.Setting(ctx, starCollectionsKey, &doc)
	if err != nil {
		return doc, errStarStore
	}
	if !exists {
		return starCollectionsDocument{Collections: []namedStarCollection{}, Assignments: map[string]string{}}, nil
	}
	if doc.Revision < 1 || doc.Revision > starRevisionMax || doc.Collections == nil || doc.Assignments == nil {
		return doc, errStarStore
	}
	return doc, nil
}

func (doc starCollectionsDocument) collectionIndex(id string) int {
	return slices.IndexFunc(doc.Collections, func(collection namedStarCollection) bool { return collection.ID == id })
}

func (doc starCollectionsDocument) duplicateName(id, name string) bool {
	return slices.ContainsFunc(doc.Collections, func(collection namedStarCollection) bool {
		return collection.ID != id && strings.EqualFold(collection.Name, name)
	})
}

func activeStarIDs(stars []Star) map[string]bool {
	ids := make(map[string]bool, len(stars))
	for _, star := range stars {
		ids[star.ID] = true
	}
	return ids
}

// A legacy manager or a concurrent star removal can leave an old ID in the
// metadata. It never resolves to a different message, because IDs aren't
// reused. Reads hide those entries, and the next accepted write prunes them.
func (doc starCollectionsDocument) pruneAssignments(active map[string]bool) bool {
	pruned := false
	for id, target := range doc.Assignments {
		if !active[id] || doc.collectionIndex(target) < 0 {
			delete(doc.Assignments, id)
			pruned = true
		}
	}
	return pruned
}

func (b *starBook) readCollections(ctx context.Context, st revisionedSettings) (starCollectionsDocument, map[string]bool, error) {
	doc, err := loadStarCollections(ctx, st)
	if err != nil {
		return doc, nil, err
	}
	stars, err := loadStars(ctx, st)
	if err != nil {
		return doc, nil, errStarStore
	}
	return doc, activeStarIDs(stars.Stars), nil
}

type starCollectionChange func(*starCollectionsDocument, map[string]bool) (bool, error)

func (b *starBook) changeCollections(ctx context.Context, st revisionedSettings, expected int64, apply starCollectionChange) (starCollectionsDocument, error) {
	doc, active, err := b.readCollections(ctx, st)
	if err != nil {
		return doc, err
	}
	if expected != doc.Revision {
		return doc, errCollectionConflict
	}
	pruned := doc.pruneAssignments(active)
	changed, err := apply(&doc, active)
	if err != nil || !changed && !pruned {
		return doc, err
	}
	doc.Revision++
	saved, err := st.CompareSetting(ctx, starCollectionsKey, expected, doc)
	if err != nil {
		return doc, errStarStore
	}
	if !saved {
		return doc, errCollectionConflict
	}
	b.h.Event(ctx, map[string]any{"type": "assistant_starred"})
	return doc, nil
}

func createStarCollection(name string) starCollectionChange {
	return func(doc *starCollectionsDocument, _ map[string]bool) (bool, error) {
		if doc.duplicateName("", name) {
			return false, errCollectionDuplicate
		}
		if len(doc.Collections) >= starCollectionsMax {
			return false, errCollectionLimit
		}
		doc.Collections = append(doc.Collections, namedStarCollection{ID: starCollectionIDPrefix + randomHex(16), Name: name})
		return true, nil
	}
}

func renameStarCollection(id, name string) starCollectionChange {
	return func(doc *starCollectionsDocument, _ map[string]bool) (bool, error) {
		at := doc.collectionIndex(id)
		if at < 0 {
			return false, errCollectionMissing
		}
		if doc.duplicateName(id, name) {
			return false, errCollectionDuplicate
		}
		if doc.Collections[at].Name == name {
			return false, nil
		}
		doc.Collections[at].Name = name
		return true, nil
	}
}

func deleteStarCollection(id string) starCollectionChange {
	return func(doc *starCollectionsDocument, _ map[string]bool) (bool, error) {
		at := doc.collectionIndex(id)
		if at < 0 {
			return false, errCollectionMissing
		}
		doc.Collections = slices.Delete(doc.Collections, at, at+1)
		for star, target := range doc.Assignments {
			if target == id {
				delete(doc.Assignments, star)
			}
		}
		return true, nil
	}
}

func assignStarCollection(ids []string, target string) starCollectionChange {
	return func(doc *starCollectionsDocument, active map[string]bool) (bool, error) {
		if target != "" && doc.collectionIndex(target) < 0 {
			return false, errCollectionMissing
		}
		for _, id := range ids {
			if !active[id] {
				return false, errCollectionStarGone
			}
		}
		changed := false
		for _, id := range ids {
			if doc.Assignments[id] == target {
				continue
			}
			changed = true
			if target == "" {
				delete(doc.Assignments, id)
			} else {
				doc.Assignments[id] = target
			}
		}
		return changed, nil
	}
}

func writeStarCollectionsError(w http.ResponseWriter, err error) {
	status, code, key := http.StatusBadRequest, "invalid_request", "err_collection_input"
	switch err {
	case errStarStore:
		status, code, key = http.StatusServiceUnavailable, "storage_unavailable", "err_store"
	case errCollectionConflict:
		status, code, key = http.StatusConflict, "conflict", "err_collection_conflict"
	case errCollectionDuplicate:
		status, code, key = http.StatusConflict, "duplicate_name", "err_collection_duplicate"
	case errCollectionLimit:
		code, key = "limit_reached", "err_collection_limit"
	case errCollectionMissing:
		status, code, key = http.StatusNotFound, "collection_not_found", "err_collection_missing"
	case errCollectionStarGone:
		status, code, key = http.StatusNotFound, "star_not_found", "err_collection_star_gone"
	}
	apierr.WriteCode(w, status, code, i18n.Err("assistant-starred."+key))
}

func (b *starBook) collectionFields(w http.ResponseWriter, r *http.Request, allowed ...string) (map[string]json.RawMessage, int64, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, starCollectionBodyMax))
	if err != nil {
		b.h.BodyErr(w, err, i18n.Err("assistant-starred.err_collection_input"))
		return nil, 0, false
	}
	fields, err := savedSearchObject(body, append(allowed, "revision")...)
	if err != nil {
		writeStarCollectionsError(w, errCollectionInput)
		return nil, 0, false
	}
	revision, err := collectionRevision(string(fields["revision"]))
	if err != nil {
		writeStarCollectionsError(w, err)
		return nil, 0, false
	}
	return fields, revision, true
}

func (b *starBook) writeCollectionState(w http.ResponseWriter, r *http.Request, revision int64, apply starCollectionChange) {
	st, ok := b.h.Store().(revisionedSettings)
	if !ok {
		writeStarCollectionsError(w, errStarStore)
		return
	}
	doc, err := b.changeCollections(r.Context(), st, revision, apply)
	if err != nil {
		writeStarCollectionsError(w, err)
		return
	}
	apierr.WriteJSON(w, starCollectionsResponse{starCollectionsDocument: doc, CanWrite: true})
}

func (b *starBook) listCollections(w http.ResponseWriter, r *http.Request) {
	st, ok := b.h.Store().(revisionedSettings)
	if !ok {
		writeStarCollectionsError(w, errStarStore)
		return
	}
	doc, active, err := b.readCollections(r.Context(), st)
	if err != nil {
		writeStarCollectionsError(w, err)
		return
	}
	doc.pruneAssignments(active)
	apierr.WriteJSON(w, starCollectionsResponse{starCollectionsDocument: doc, CanWrite: b.h.Full(r)})
}

func (b *starBook) saveCollection(w http.ResponseWriter, r *http.Request) {
	fields, revision, ok := b.collectionFields(w, r, "name")
	if !ok {
		return
	}
	rawName, err := savedSearchString(fields, "name", true)
	if err != nil {
		writeStarCollectionsError(w, errCollectionInput)
		return
	}
	name, err := collectionName(rawName)
	if err != nil {
		writeStarCollectionsError(w, err)
		return
	}
	if r.Method == http.MethodPost {
		b.writeCollectionState(w, r, revision, createStarCollection(name))
		return
	}
	id := r.PathValue("id")
	if !validCollectionID(id) {
		writeStarCollectionsError(w, errCollectionInput)
		return
	}
	b.writeCollectionState(w, r, revision, renameStarCollection(id, name))
}

func (b *starBook) removeCollection(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id := r.PathValue("id")
	revision, err := collectionRevision(query.Get("revision"))
	// The hub can forward its manager selector in this query. Only the
	// revision is part of the operation handled here.
	if err != nil || len(query["revision"]) != 1 || !validCollectionID(id) {
		writeStarCollectionsError(w, errCollectionInput)
		return
	}
	b.writeCollectionState(w, r, revision, deleteStarCollection(id))
}

func (b *starBook) moveCollections(w http.ResponseWriter, r *http.Request) {
	fields, revision, ok := b.collectionFields(w, r, "star_ids", "collection_id")
	if !ok {
		return
	}
	var ids []string
	var target *string
	if json.Unmarshal(fields["star_ids"], &ids) != nil || len(ids) == 0 || len(ids) > StarredMax ||
		json.Unmarshal(fields["collection_id"], &target) != nil || target != nil && !validCollectionID(*target) {
		writeStarCollectionsError(w, errCollectionInput)
		return
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validStarID(id) || seen[id] {
			writeStarCollectionsError(w, errCollectionInput)
			return
		}
		seen[id] = true
	}
	collection := ""
	if target != nil {
		collection = *target
	}
	b.writeCollectionState(w, r, revision, assignStarCollection(ids, collection))
}

// Look up optional undo metadata even though the star was just removed. A
// missing sidecar or unavailable metadata must not fail a legacy unstar.
func (b *starBook) collectionForUnstar(ctx context.Context, id string) string {
	st, ok := b.h.Store().(revisionedSettings)
	if !ok {
		return ""
	}
	doc, err := loadStarCollections(ctx, st)
	if err != nil {
		return ""
	}
	target := doc.Assignments[id]
	if doc.collectionIndex(target) < 0 {
		return ""
	}
	return target
}

// Restore membership only for the occurrence just created by Undo. The saved
// words already exist, so a deleted collection or unavailable sidecar must not
// turn a successful snapshot restore into an error.
func (b *starBook) restoreStarCollection(ctx context.Context, star, target string) {
	st, ok := b.h.Store().(revisionedSettings)
	if !ok {
		return
	}
	for range 8 {
		current, err := loadStarCollections(ctx, st)
		if err != nil || current.collectionIndex(target) < 0 || current.Revision == starRevisionMax {
			return
		}
		_, err = b.changeCollections(ctx, st, current.Revision, func(doc *starCollectionsDocument, active map[string]bool) (bool, error) {
			if !active[star] || doc.Assignments[star] != "" || doc.collectionIndex(target) < 0 {
				return false, nil
			}
			doc.Assignments[star] = target
			return true, nil
		})
		if err != errCollectionConflict {
			return
		}
	}
}

func (b *starBook) collectionRoutes(write func(http.HandlerFunc) http.HandlerFunc) {
	b.h.Handle("GET "+starCollectionsPath, b.listCollections)
	b.h.Handle("POST "+starCollectionsPath, write(b.saveCollection))
	b.h.Handle("PATCH "+starCollectionsPath+"/{id}", write(b.saveCollection))
	b.h.Handle("DELETE "+starCollectionsPath+"/{id}", write(b.removeCollection))
	b.h.Handle("PATCH "+starCollectionsPath+"/assignments", write(b.moveCollections))
}
