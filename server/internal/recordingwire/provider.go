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
	// The minutes and day-recap rounds, each at most max recordings a call, returning how many it wrote.
	WriteMinutes(ctx context.Context, max int) int
	WriteDayRecaps(ctx context.Context, max int) int
	WritersInCore
	MinutesFromCore
}

// WritersInCore is the part of Provider whose code is still in the knowledge base (kb.RecordingWriters): the title
// and summary rounds, the sweep after a prune and the audio writers' touch points. The module's provider carries it
// until PRs 4 and 5 of docs/plans/plan-kb-recording-port.md move that code.
type WritersInCore interface {
	// The title and summary rounds, each at most max recordings a call, returning how many it wrote.
	TitleRecordings(ctx context.Context, max int) int
	SummarizeRecordings(ctx context.Context, max int) int
	// AfterPrune is the module's part of the retention round, after the knowledge base's own: the sweep of hub
	// transcripts (it mends what a crash left of one, makes minutes again after a final pass, drops audio).
	AfterPrune(now time.Time)
	// AudioDropped notes that recording id's audio directory is being removed, why; the caller holds the store's
	// recording files lock for writing. AudioClosed closes the audio file id has open for writing; the caller holds no
	// lock of the store.
	AudioDropped(id string, why error)
	AudioClosed(id string)
}

// MinutesFromCore is what the writers still in the knowledge base (the upload, the photos; PRs 4 and 5 of
// docs/plans/plan-kb-recording-port.md move them) call of the minutes and day-recap writers, which are the module's.
// Transitional: each method goes when its caller moves. The upload and the photo routes are the module's own, so
// nothing reaches these without the module; NoProvider answers as a knowledge base with no minutes and no recap.
type MinutesFromCore interface {
	// PutMinutes writes minutes.json and minutes.md of recording id, unless it was deleted meanwhile (false then);
	// the caller holds the recordings lock. RenderMinutes is minutes.md of m.
	PutMinutes(id ID, m *Minutes) (bool, error)
	RenderMinutes(m *Minutes, start time.Time, zone *time.Location) string
	// RemakeMinutes has r's minutes made again at the next round; QuietMinutes: the ones being made now are made
	// again only because a photo changed.
	RemakeMinutes(r *Recording)
	QuietMinutes(r *Recording) bool
	// DropOverviewPhotos takes the bullets and conclusions citing a photo cites says goes out of m's overview.
	DropOverviewPhotos(m *Minutes, cites func(n int) bool)
	// RepairDayRecap is what an upload u that sets a new base does to day recording r's recap before it is committed
	// (recapped: r has a recap); RebaseRecap what it does to r's recap targets; PruneRecapNotes drops the notes of the
	// chunks whose lines the upload changed.
	RepairDayRecap(r *Recording, u *RecordingUpload) (recapped bool, err error)
	RebaseRecap(r *Recording, recapped bool, now time.Time)
	PruneRecapNotes(r *Recording, segs []kbwire.RecordingSegment) error
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

func (NoProvider) PutMinutes(ID, *Minutes) (bool, error)                          { return false, nil }
func (NoProvider) RenderMinutes(m *Minutes, _ time.Time, _ *time.Location) string { return m.Markdown }
func (NoProvider) RemakeMinutes(*Recording)                                       {}
func (NoProvider) QuietMinutes(*Recording) bool                                   { return false }
func (NoProvider) DropOverviewPhotos(*Minutes, func(int) bool)                    {}
func (NoProvider) RepairDayRecap(r *Recording, _ *RecordingUpload) (bool, error) {
	return r.RecapVersion > 0, nil
}
func (NoProvider) RebaseRecap(*Recording, bool, time.Time)                     {}
func (NoProvider) PruneRecapNotes(*Recording, []kbwire.RecordingSegment) error { return nil }
