package writers

import (
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// purgeCounting is the storage port counting how often the writers ask the search index to drop what was deleted.
type purgeCounting struct {
	recordingwire.Core
	purges int
}

func (c *purgeCounting) PurgeSearch() {
	c.purges++
	c.Core.PurgeSearch()
}

// Deleting a photo asks the search index for the purge, through the storage port: its line of photos.md and the
// minutes' items citing it leave the index at its next update (the index's side is the knowledge base's
// TestPhotoDeleteDropsItsWordsFromTheSearchIndex).
func TestPhotoDeleteAsksForTheSearchPurge(t *testing.T) {
	e := goldenEngine(t, nil)
	photoMeeting(t, e, recID, time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone))
	addPhoto(t, e, recID, photoUpload(1, 1, "第一张", ""), false)
	core := &purgeCounting{Core: kb.RecordingCore(e.Engine)}
	w := New(core, nil)
	if _, _, err := w.Photos(recID); err != nil || core.purges != 0 {
		t.Fatalf("reading the photos: %v, %d purges", err, core.purges)
	}
	if ok, err := w.PhotoDelete(recID, 1); !ok || err != nil || core.purges != 1 {
		t.Fatalf("PhotoDelete: %v, %v, %d purges", ok, err, core.purges)
	}
	if _, err := w.PhotoDelete(recID, 7); !errors.Is(err, ErrPhotoNotFound) || core.purges != 1 {
		t.Fatalf("a photo that is not there: %v, %d purges", err, core.purges)
	}
}

// The audio writers' state is one per store: a second engine over the same store (the manager makes one when its
// settings change) is hooked with writers of its own, and a delete through them reaches the socket the first ones
// opened. Without that an open socket would go on writing into a recording deleted through the new engine.
func TestAudioStateIsOnePerStore(t *testing.T) {
	e, _ := hubEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	hubMeeting(t, e, recID, now)
	w, err := e.AudioOpen(recID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(0, pcmAt(0, 100)); err != nil {
		t.Fatal(err)
	}
	second := newTestEngine(&kb.Engine{Store: e.Store, TranscriptionReady: e.TranscriptionReady, Logf: func(string, ...any) {}})
	if second.Writers == e.Writers || second.Writers.audio != e.Writers.audio {
		t.Fatalf("the second engine's writers: same %v, same audio state %v", second.Writers == e.Writers, second.Writers.audio == e.Writers.audio)
	}
	if held, err := second.AudioHeld(recID); err != nil || len(held) != 1 {
		t.Fatalf("held through the second engine: %v %v", held, err)
	}
	if ok, err := second.DeleteRecording(recID, now); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if err := w.Write(1, pcmAt(secs(1), 100)); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("a write after the delete through the other engine: %v", err)
	}
	_ = w.Close()
	// A store nobody hooked has no state to share: its writers start their own.
	fresh := goldenEngine(t, nil)
	if fresh.Writers.audio == e.Writers.audio || fresh.Writers.audio == nil {
		t.Fatal("writers of another store share the audio state")
	}
}

// An audio append goes through the port without a goroutine or an allocation of the port's: the files lock is the
// store's own, held for reading through an interface call, and the chunk file stays open between appends.
func TestAudioAppendAllocatesNothingForThePort(t *testing.T) {
	e, _ := hubEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	hubMeeting(t, e, recID, now)
	w, err := e.AudioOpen(recID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	pcm := pcmAt(0, 160)
	at := int64(0)
	if err := w.Write(0, pcm); err != nil { // the first append makes the chunk file
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		at += 160
		if err := w.Write(sampleAt(at), pcm); err != nil {
			t.Fatal(err)
		}
	})
	// What an append allocates is its own: the span it is missing (missingLocked). Nothing more with the port between.
	if allocs > 1 {
		t.Fatalf("an append allocates %v times", allocs)
	}
}
