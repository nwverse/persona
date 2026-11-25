package writers

import (
	"context"
	"encoding/json"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
)

// testEngine is a knowledge base with this package's writers hooked into it, as the recording module's hook does:
// the knowledge base's own members (PutRecording, Get, Store, Model, ...) and the writers' (WriteMinutes, AskMinutes,
// MinutesWritten, ...) under one value, as the tests had them while the writers were methods of kb.Engine.
type testEngine struct {
	*kb.Engine
	*Writers
	clock func() time.Time
}

// SetClock sets the knowledge base's clock, and keeps it for a test that makes a second engine on the same store.
func (e *testEngine) SetClock(now func() time.Time) {
	e.clock = now
	e.Engine.SetClock(now)
}

var (
	testStoresMu sync.Mutex
	testStores   = map[*kb.Store]*Writers{}
)

// newTestEngine hooks e: its provider is this package's writers over its storage port, with the knowledge base's
// remaining writers behind them (recording.Recordings.Hook).
func newTestEngine(e *kb.Engine) *testEngine {
	w := New(kb.RecordingCore(e), kb.RecordingWriters(e))
	e.SetRecordingProvider(w)
	testStoresMu.Lock()
	testStores[e.Store] = w
	testStoresMu.Unlock()
	return &testEngine{Engine: e, Writers: w}
}

// eng is the engine as the writers see it now (Writers.engine), for a test that calls a writer below the rounds.
func (e *testEngine) eng() *engine { return e.Writers.engine() }

// ws is knowledge-base store s as the writers see it.
func ws(s *kb.Store) *store {
	testStoresMu.Lock()
	defer testStoresMu.Unlock()
	return newStore(testStores[s].core)
}

// What the knowledge base keeps to itself and these tests name: its files' names and a few limits, as they are on
// disk and in internal/kb, and small helpers of its own tests.
const (
	meetingFile          = "meeting.json"
	photosFile           = "photos.json"
	photosDir            = "attachments"
	minutesHubWait       = recordingwire.MinutesHubWait
	maxCalendarTitle     = 200
	maxCalendarAttendees = 100
	maxCalendarName      = 100
	liveTitleMax         = 4
)

// entryOf is the kind and id of the entry a path under the data directory belongs to (recordings/<id>/...).
func entryOf(rel string) (kind, id string, ok bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// dropPhotos is the knowledge base's own (internal/kb/recording_photos.go, which moves here with the photo writers):
// it takes out of m the items citing a photo gone says goes, the overview's with them, and numbers the actions again.
func dropPhotos(m *Minutes, gone func(n int) bool) {
	cites := func(n int) bool { return n > 0 && gone(n) }
	m.Points = slices.DeleteFunc(m.Points, func(x MinutesPoint) bool { return cites(x.Photo) })
	m.Decisions = slices.DeleteFunc(m.Decisions, func(x MinutesItem) bool { return cites(x.Photo) })
	m.Actions = slices.DeleteFunc(m.Actions, func(x MinutesAction) bool { return cites(x.Photo) })
	m.Open = slices.DeleteFunc(m.Open, func(x MinutesItem) bool { return cites(x.Photo) })
	dropOverviewPhotos(m, cites)
	for i := range m.Actions {
		m.Actions[i].ID = "a" + strconv.Itoa(i+1)
	}
}

// summaryDue is the knowledge base's own rule (internal/kb/recordings.go): r's summary is to be made (again).
func summaryDue(r *Recording) bool {
	if !r.Complete || r.Chars == 0 || r.Trimmed() || r.Kind == RecordingDay {
		return false
	}
	t := recordingwire.HubTranscription(r)
	return r.SummarizedRev < r.Revision || t != nil && t.Done() && !r.SummarizedFinal
}

// forgetOnlyPhoto rewrites the photo book at path as a delete of photo n cut short leaves it: the only listed photo
// becomes a deleted one, its file and the minutes citing it untouched. The caller holds the store's files lock.
func forgetOnlyPhoto(path string, n int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var b map[string]json.RawMessage
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	var photos []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b["photos"], &photos); err != nil {
		return err
	}
	var deleted []kb.PhotoRef
	if b["deleted"] != nil {
		if err := json.Unmarshal(b["deleted"], &deleted); err != nil {
			return err
		}
	}
	deleted = append(deleted, kb.PhotoRef{N: n, ID: photos[0].ID})
	b["deleted"], _ = json.Marshal(deleted)
	delete(b, "photos")
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

// The title and summary rounds are the knowledge base's own code still; a test calls them there (the provider's
// methods of the same names carry them to the same functions).
func (e *testEngine) TitleRecordings(ctx context.Context, max int) int {
	return e.Engine.TitleRecordings(ctx, max)
}
func (e *testEngine) SummarizeRecordings(ctx context.Context, max int) int {
	return e.Engine.SummarizeRecordings(ctx, max)
}
