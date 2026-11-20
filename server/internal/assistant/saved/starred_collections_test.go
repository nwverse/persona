package saved

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type collectionTestStore struct {
	mu   sync.Mutex
	docs map[string]json.RawMessage
}

func (s *collectionTestStore) Setting(_ context.Context, key string, out any) (bool, error) {
	s.mu.Lock()
	raw, ok := s.docs[key]
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

func (s *collectionTestStore) CompareSetting(_ context.Context, key string, revision int64, next any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current struct{ Revision int64 }
	if s.docs[key] != nil {
		if err := json.Unmarshal(s.docs[key], &current); err != nil {
			return false, err
		}
	}
	if current.Revision != revision {
		return false, nil
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	if s.docs == nil {
		s.docs = make(map[string]json.RawMessage)
	}
	s.docs[key] = raw
	return true, nil
}

func collectionRequest(t *testing.T, mux *http.ServeMux, method, suffix string, body any, status int, code string) starCollectionsResponse {
	t.Helper()
	var raw string
	if body != nil {
		switch body := body.(type) {
		case string:
			raw = body
		default:
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(data)
		}
	}
	request := httptest.NewRequest(method, starCollectionsPath+suffix, strings.NewReader(raw))
	request.Header.Set("Role", "full")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: %d, want %d: %s", method, suffix, response.Code, status, response.Body)
	}
	if status != http.StatusOK {
		var failure struct{ Code string }
		if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure.Code != code {
			t.Fatalf("failure code: got %s, want %s: %v", response.Body, code, err)
		}
		return starCollectionsResponse{}
	}
	var result starCollectionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.CanWrite || result.Collections == nil || result.Assignments == nil {
		t.Fatalf("invalid collection response: %+v", result)
	}
	return result
}

func addCollectionTestStar(t *testing.T, book *starBook, item string) Star {
	t.Helper()
	star, _, err := book.add(t.Context(), testStar(item))
	if err != nil {
		t.Fatal(err)
	}
	return star
}

func collectionMove(revision int64, target any, ids ...string) map[string]any {
	return map[string]any{"revision": revision, "collection_id": target, "star_ids": ids}
}

func TestStarCollectionsKeepSnapshotsIndependent(t *testing.T) {
	store := &collectionTestStore{}
	book, mux, events := newStarTestBook(store)
	first := addCollectionTestStar(t, book, "first")
	second := addCollectionTestStar(t, book, "second")
	starsBefore, err := loadStars(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	list := collectionRequest(t, mux, "GET", "", nil, 200, "")
	if list.Revision != 0 || len(list.Collections) != 0 || len(list.Assignments) != 0 {
		t.Fatalf("legacy snapshots created implicit collections: %+v", list)
	}

	list = collectionRequest(t, mux, "POST", "", `{"revision":0,"name":"  Release notes  "}`, 200, "")
	target := list.Collections[0].ID
	if list.Revision != 1 || !validCollectionID(target) || list.Collections[0].Name != "Release notes" {
		t.Fatalf("created collection: %+v", list)
	}
	list = collectionRequest(t, mux, "POST", "", `{"revision":1,"name":"设计讨论"}`, 200, "")
	other := list.Collections[1].ID
	list = collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(list.Revision, target, first.ID, second.ID), 200, "")
	if list.Revision != 3 || !reflect.DeepEqual(list.Assignments, map[string]string{first.ID: target, second.ID: target}) {
		t.Fatalf("bulk move: %+v", list)
	}
	list = collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(list.Revision, nil, second.ID), 200, "")
	if list.Revision != 4 || !reflect.DeepEqual(list.Assignments, map[string]string{first.ID: target}) {
		t.Fatalf("unfile: %+v", list)
	}
	list = collectionRequest(t, mux, "PATCH", "/"+target, `{"revision":4,"name":"Decisions"}`, 200, "")
	if list.Revision != 5 || list.Collections[0] != (namedStarCollection{ID: target, Name: "Decisions"}) || list.Assignments[first.ID] != target {
		t.Fatalf("rename lost the collection's identity: %+v", list)
	}

	_, fresh, _ := newStarTestBook(store)
	read := collectionRequest(t, fresh, "GET", "", nil, 200, "")
	if !reflect.DeepEqual(read, list) {
		t.Fatalf("fresh manager saw %+v, want %+v", read, list)
	}
	deleted := collectionRequest(t, fresh, "DELETE", "/"+target+"?revision=5&manager=m-example", nil, 200, "")
	if deleted.Revision != 6 || len(deleted.Assignments) != 0 || len(deleted.Collections) != 1 || deleted.Collections[0].ID != other {
		t.Fatalf("delete did not unfile the assigned snapshots: %+v", deleted)
	}
	starsAfter, err := loadStars(t.Context(), store)
	if err != nil || !reflect.DeepEqual(starsAfter, starsBefore) {
		t.Fatalf("folder edits changed the legacy snapshot setting: %+v, %v", starsAfter, err)
	}
	if len(*events) != 7 { // two star creates and five collection writes
		t.Fatalf("invalidation events: %v", *events)
	}
}

func TestStarCollectionsRejectInvalidAndStaleWritesWithoutEffects(t *testing.T) {
	store := &collectionTestStore{}
	book, mux, events := newStarTestBook(store)
	first := addCollectionTestStar(t, book, "first")
	second := addCollectionTestStar(t, book, "second")
	list := collectionRequest(t, mux, "POST", "", `{"revision":0,"name":"Release notes"}`, 200, "")
	target := list.Collections[0].ID

	collectionRequest(t, mux, "POST", "", `{"revision":0,"name":"Too late"}`, 409, "conflict")
	collectionRequest(t, mux, "POST", "", `{"revision":1,"name":" release NOTES "}`, 409, "duplicate_name")
	for _, bad := range []string{
		`{"name":"Missing revision"}`, `{"revision":null,"name":"Null revision"}`,
		`{"revision":"1","name":"Wrong type"}`, `{"revision":1,"revision":1,"name":"Ambiguous"}`,
		`{"revision":1,"name":" "}`, `{"revision":1,"name":"Line\nbreak"}`, `{"revision":1,"name":"Name","extra":true}`,
		`{"revision":1,"name":"Name"}{}`,
	} {
		collectionRequest(t, mux, "POST", "", bad, 400, "invalid_request")
	}
	collectionRequest(t, mux, "POST", "", map[string]any{"revision": 1, "name": strings.Repeat("案", starCollectionNameMax+1)}, 400, "invalid_request")
	collectionRequest(t, mux, "PATCH", "/assignments", `{"revision":1,"star_ids":["some-id"]}`, 400, "invalid_request")
	collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(1, target, first.ID, first.ID), 400, "invalid_request")
	collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(1, target, first.ID, "missing-star", second.ID), 404, "star_not_found")
	collectionRequest(t, mux, "DELETE", "/"+target+"?revision=1&revision=0", nil, 400, "invalid_request")
	collectionRequest(t, mux, "DELETE", "/sc"+strings.Repeat("a", 32)+"?revision=1", nil, 404, "collection_not_found")
	doStar(t, mux, "POST", "/collections", "read", `{"revision":1,"name":"Forbidden"}`, 403, nil)
	doStar(t, mux, "PATCH", "/collections/"+target, "read", `{"revision":1,"name":"Forbidden"}`, 403, nil)
	doStar(t, mux, "PATCH", "/collections/assignments", "read", "", 403, nil)
	doStar(t, mux, "DELETE", "/collections/"+target+"?revision=1", "read", "", 403, nil)

	unchanged := collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(1, nil, first.ID, second.ID), 200, "")
	if !reflect.DeepEqual(unchanged, list) || len(*events) != 3 {
		t.Fatalf("invalid or no-op writes had effects: %+v, events: %v", unchanged, *events)
	}
}

func TestStarCollectionsIgnoreLegacyOrphansAndReportUndoTarget(t *testing.T) {
	store := &collectionTestStore{}
	book, mux, _ := newStarTestBook(store)
	star := addCollectionTestStar(t, book, "with-undo")
	list := collectionRequest(t, mux, "POST", "", `{"revision":0,"name":"Platform"}`, 200, "")
	target := list.Collections[0].ID
	list = collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(list.Revision, target, star.ID), 200, "")
	var deletion struct{ Removed *removedStar }
	doStar(t, mux, "DELETE", "/"+star.ID, "full", "", 200, &deletion)
	if deletion.Removed == nil || deletion.Removed.ID != star.ID || deletion.Removed.CollectionID != target {
		t.Fatalf("undo omitted collection metadata: %+v", deletion)
	}
	visible := collectionRequest(t, mux, "GET", "", nil, 200, "")
	persisted, err := loadStarCollections(t.Context(), store)
	if err != nil || visible.Revision != list.Revision || len(visible.Assignments) != 0 || persisted.Assignments[star.ID] != target {
		t.Fatalf("read should hide, not write, stale assignments: %+v, %+v, %v", visible, persisted, err)
	}

	raw, err := json.Marshal(deletion.Removed)
	if err != nil {
		t.Fatal(err)
	}
	var restored struct{ Star Star }
	doStar(t, mux, "POST", "", "full", string(raw), 200, &restored)
	if restored.Star.ID == star.ID || restored.Star.Text != star.Text {
		t.Fatalf("Undo must restore the saved words in a new occurrence: %+v", restored)
	}
	visible = collectionRequest(t, mux, "GET", "", nil, 200, "")
	persisted, err = loadStarCollections(t.Context(), store)
	if err != nil || !reflect.DeepEqual(visible.Assignments, map[string]string{restored.Star.ID: target}) ||
		!reflect.DeepEqual(visible.Assignments, persisted.Assignments) {
		t.Fatalf("restored assignment or stale entry prune failed: %+v, %+v, %v", visible, persisted, err)
	}
	visible = collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(visible.Revision, nil, restored.Star.ID), 200, "")
	doStar(t, mux, "POST", "", "full", string(raw), 200, &restored)
	retried := collectionRequest(t, mux, "GET", "", nil, 200, "")
	if !reflect.DeepEqual(retried, visible) {
		t.Fatalf("duplicate undo overwrote another client's assignment: %+v", retried)
	}

	doStar(t, mux, "DELETE", "/"+restored.Star.ID, "full", "", 200, nil)
	visible = collectionRequest(t, mux, "DELETE", fmt.Sprintf("/%s?revision=%d", target, visible.Revision), nil, 200, "")
	doStar(t, mux, "POST", "", "full", string(raw), 200, &restored)
	afterDelete := collectionRequest(t, mux, "GET", "", nil, 200, "")
	if restored.Star.Text != star.Text || !reflect.DeepEqual(afterDelete, visible) {
		t.Fatalf("undo through a deleted collection lost its words or refiled: %+v, %+v", restored, afterDelete)
	}
	var snapshots map[string]json.RawMessage
	doStar(t, mux, "GET", "", "full", "", 200, &snapshots)
	if strings.Contains(string(snapshots["stars"]), "collection_id") {
		t.Fatalf("legacy snapshots acquired membership data: %+v", snapshots)
	}
}

type blockedCollectionStore struct {
	*collectionTestStore
	queued chan struct{}
	write  chan struct{}
}

func (s blockedCollectionStore) CompareSetting(ctx context.Context, key string, revision int64, next any) (bool, error) {
	if key == starCollectionsKey {
		s.queued <- struct{}{}
		<-s.write
	}
	return s.collectionTestStore.CompareSetting(ctx, key, revision, next)
}

func TestStarCollectionsConcurrentManagersUseCompareAndSwap(t *testing.T) {
	store := blockedCollectionStore{
		collectionTestStore: &collectionTestStore{},
		queued:              make(chan struct{}, 2),
		write:               make(chan struct{}),
	}
	unblock := sync.OnceFunc(func() { close(store.write) })
	defer unblock()
	_, one, oneEvents := newStarTestBook(store)
	_, two, twoEvents := newStarTestBook(store)
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for index, mux := range []*http.ServeMux{one, two} {
		wg.Go(func() {
			request := httptest.NewRequest("POST", starCollectionsPath, strings.NewReader(fmt.Sprintf(`{"revision":0,"name":"Collection %d"}`, index)))
			request.Header.Set("Role", "full")
			response := httptest.NewRecorder()
			<-start
			mux.ServeHTTP(response, request)
			statuses <- response.Code
		})
	}
	close(start)
	// Both managers must hit CAS with revision zero before either can write.
	for range 2 {
		select {
		case <-store.queued:
		case <-time.After(3 * time.Second):
			t.Fatal("two managers did not reach concurrent collection comparison")
		}
	}
	unblock()
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	current := collectionRequest(t, one, "GET", "", nil, 200, "")
	if counts[200] != 1 || counts[409] != 1 || current.Revision != 1 || len(current.Collections) != 1 ||
		len(*oneEvents)+len(*twoEvents) != 1 {
		t.Fatalf("concurrent creates: statuses %v, state %+v, events %v and %v", counts, current, *oneEvents, *twoEvents)
	}
}

func TestStarCollectionsCapacityAndUnavailableStorage(t *testing.T) {
	_, noStore, _ := newStarTestBook(nil)
	collectionRequest(t, noStore, "GET", "", nil, 503, "storage_unavailable")
	collectionRequest(t, noStore, "POST", "", `{"revision":0,"name":"Unavailable"}`, 503, "storage_unavailable")

	store := &collectionTestStore{}
	_, mux, events := newStarTestBook(store)
	var latest starCollectionsResponse
	for i := range starCollectionsMax {
		latest = collectionRequest(t, mux, "POST", "", map[string]any{"revision": i, "name": fmt.Sprintf("Project %d", i)}, 200, "")
	}
	collectionRequest(t, mux, "POST", "", map[string]any{"revision": latest.Revision, "name": "Over the limit"}, 400, "limit_reached")
	first := latest.Collections[0]
	renamed := collectionRequest(t, mux, "PATCH", "/"+first.ID, map[string]any{"revision": latest.Revision, "name": "Renamed"}, 200, "")
	if len(renamed.Collections) != starCollectionsMax || renamed.Collections[0].Name != "Renamed" || len(*events) != starCollectionsMax+1 {
		t.Fatalf("capacity affected rename or invalidation: %+v, %d events", renamed, len(*events))
	}
}

type unavailableCollectionStore struct{ *collectionTestStore }

func (s unavailableCollectionStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == starCollectionsKey {
		return false, errors.New("collection sidecar unavailable")
	}
	return s.collectionTestStore.Setting(ctx, key, out)
}

func TestStarCollectionsSidecarFailureDoesNotLoseUndoSnapshot(t *testing.T) {
	store := unavailableCollectionStore{&collectionTestStore{}}
	_, mux, _ := newStarTestBook(store)
	hint := &removedStar{
		Star:         Star{StarInput: testStar("with-sidecar-failure")},
		CollectionID: "sc" + strings.Repeat("a", 32),
	}
	raw, err := json.Marshal(hint)
	if err != nil {
		t.Fatal(err)
	}
	var added struct{ Star Star }
	doStar(t, mux, "POST", "", "full", string(raw), 200, &added)
	if added.Star.ID == "" || !reflect.DeepEqual(added.Star.StarInput, hint.StarInput) {
		t.Fatalf("undo snapshot was lost: %+v", added)
	}
	var deleted struct{ Removed *removedStar }
	doStar(t, mux, "DELETE", "/"+added.Star.ID, "full", "", 200, &deleted)
	if deleted.Removed == nil || deleted.Removed.ID != added.Star.ID || deleted.Removed.CollectionID != "" {
		t.Fatalf("unstar should tolerate unavailable metadata: %+v", deleted)
	}
}

func TestStarCollectionsUndoDoesNotOverwriteAnotherAssignment(t *testing.T) {
	store := &collectionTestStore{}
	book, mux, events := newStarTestBook(store)
	first := collectionRequest(t, mux, "POST", "", `{"revision":0,"name":"Original"}`, 200, "")
	second := collectionRequest(t, mux, "POST", "", `{"revision":1,"name":"New destination"}`, 200, "")
	star := addCollectionTestStar(t, book, "concurrently-restored")
	moved := collectionRequest(t, mux, "PATCH", "/assignments", collectionMove(second.Revision, second.Collections[1].ID, star.ID), 200, "")
	eventsBefore := len(*events)
	book.restoreStarCollection(t.Context(), star.ID, first.Collections[0].ID)
	got := collectionRequest(t, mux, "GET", "", nil, 200, "")
	if !reflect.DeepEqual(got, moved) || len(*events) != eventsBefore {
		t.Fatalf("undo overwrote a concurrently assigned collection: %+v, events %v", got, *events)
	}
}
