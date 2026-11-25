package kb

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The recording module's write side while its writers are still in this package (docs/plans/plan-kb-recording-port.md,
// section 6: PRs 3 to 5 move them into internal/modules/recording, and this file goes with the last of them). The
// module gives it to a knowledge base in its hook; nothing in the Core names it.

// RecordingWriters is the writers of engine e that are still in this package, as the part of recordingwire.Provider
// the module's provider carries for them: the rounds are e's own, the audio touch points its store's. The minutes
// and day-recap writers are the module's already (internal/modules/recording/writers).
func RecordingWriters(e *Engine) recordingwire.WritersInCore { return recordingWriters{e} }

type recordingWriters struct{ e *Engine }

func (w recordingWriters) TitleRecordings(ctx context.Context, max int) int {
	return w.e.TitleRecordings(ctx, max)
}
func (w recordingWriters) SummarizeRecordings(ctx context.Context, max int) int {
	return w.e.SummarizeRecordings(ctx, max)
}
func (w recordingWriters) AfterPrune(time.Time)              { w.e.sweepTranscriptions() }
func (w recordingWriters) AudioDropped(id string, why error) { w.e.Store.audioDroppedLocked(id, why) }
func (w recordingWriters) AudioClosed(id string)             { w.e.Store.audioFileClosed(id) }
