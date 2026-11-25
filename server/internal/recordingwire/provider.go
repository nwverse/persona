package recordingwire

import (
	"context"
	"github.com/nwverse/persona/server/internal/kbwire"
	"time"
)

// Provider is the write side of recordings as the knowledge base reaches it (docs/plans/plan-kb-recording-port.md,
// section 3.1): the background rounds and the audio writers' two touch points with retention and delete. It is the
// recording module's; the knowledge base holds NoProvider when the module is left out of the build or switched off.
// Reading never goes through it: listing, opening and searching what is stored are the knowledge base's own, module
// or not (docs/architecture/core-and-modules.md "A module that is off keeps what the user has").
type Provider interface {
	// The minutes, day-recap and title rounds, each at most max recordings a call, returning how many it wrote.
	WriteMinutes(ctx context.Context, max int) int
	WriteDayRecaps(ctx context.Context, max int) int
	TitleRecordings(ctx context.Context, max int) int
	// AfterPrune is the module's part of the retention round, after the knowledge base's own: the sweep of hub
	// transcripts (it mends what a crash left of one, makes minutes again after a final pass, drops audio).
	AfterPrune(now time.Time)
	// AudioDropped notes that recording id's audio directory is being removed, why; the caller holds the store's
	// recording files lock for writing. AudioClosed closes the audio file id has open for writing; the caller holds no
	// lock of the store.
	AudioDropped(id string, why error)
	AudioClosed(id string)
	WritersInCore
	UploadFromCore
}

// WritersInCore is the part of Provider whose code is still in the knowledge base (kb.RecordingWriters): the summary
// round. The module's provider carries it until PR 5 of docs/plans/plan-kb-recording-port.md moves that code.
type WritersInCore interface {
	// The summary round, at most max recordings a call, returning how many it wrote.
	SummarizeRecordings(ctx context.Context, max int) int
}

// UploadFromCore is what the upload, which is still in the knowledge base (PR 5 of
// docs/plans/plan-kb-recording-port.md moves it), calls of the writers that are the module's. Transitional: each
// method goes when the upload moves. The upload's routes are the module's own, so nothing reaches these without the
// module; NoProvider answers as a knowledge base with no recap, no audio and no title writer.
type UploadFromCore interface {
	// RepairDayRecap is what an upload u that sets a new base does to day recording r's recap before it is committed
	// (recapped: r has a recap); RebaseRecap what it does to r's recap targets; PruneRecapNotes drops the notes of the
	// chunks whose lines the upload changed.
	RepairDayRecap(r *Recording, u *RecordingUpload) (recapped bool, err error)
	RebaseRecap(r *Recording, recapped bool, now time.Time)
	PruneRecapNotes(r *Recording, segs []kbwire.RecordingSegment) error
	// AudioGaps are recording id's gaps (spans of seconds) for a recording that ends at second end (0: not known
	// yet), from the audio held now; AudioHeldEnd the second the held audio ends at, 0 with none. The caller may
	// hold the store's meeting lock.
	AudioGaps(id ID, end float64) [][2]float64
	AudioHeldEnd(id ID) float64
	// TitleDue: r has enough new text for its next live title (the upload's answer says so).
	TitleDue(r *Recording, now time.Time) bool
}

// NoProvider is the Provider of a knowledge base without the recording module: every round writes nothing and
// answers 0 (no model call, no file touched), and there is no audio writer to tell.
type NoProvider struct{}

func (NoProvider) WriteMinutes(context.Context, int) int        { return 0 }
func (NoProvider) TitleRecordings(context.Context, int) int     { return 0 }
func (NoProvider) WriteDayRecaps(context.Context, int) int      { return 0 }
func (NoProvider) SummarizeRecordings(context.Context, int) int { return 0 }
func (NoProvider) AfterPrune(time.Time)                         {}
func (NoProvider) AudioDropped(string, error)                   {}
func (NoProvider) AudioClosed(string)                           {}

func (NoProvider) RepairDayRecap(r *Recording, _ *RecordingUpload) (bool, error) {
	return r.RecapVersion > 0, nil
}
func (NoProvider) RebaseRecap(*Recording, bool, time.Time)                     {}
func (NoProvider) PruneRecapNotes(*Recording, []kbwire.RecordingSegment) error { return nil }
func (NoProvider) AudioGaps(ID, float64) [][2]float64                          { return nil }
func (NoProvider) AudioHeldEnd(ID) float64                                     { return 0 }
func (NoProvider) TitleDue(*Recording, time.Time) bool                         { return false }
