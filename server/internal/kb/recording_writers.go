package kb

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The recording module's write side while its writers are still in this package (docs/plans/plan-kb-recording-port.md,
// section 6: PRs 3 to 5 move them into internal/modules/recording, and this file goes with the last of them). The
// module gives it to a knowledge base in its hook; nothing in the Core names it.

// RecordingWriters is the writers of engine e that are still in this package, as a recordingwire.Provider: the title
// and summary rounds and the sweep are e's own, the audio touch points its store's. The minutes and day-recap
// writers are the module's (internal/modules/recording/writers), whose provider carries this one for the rest; on
// its own this one answers for them as a knowledge base without the module does (recordingwire.NoProvider).
func RecordingWriters(e *Engine) recordingwire.Provider { return recordingWriters{e: e} }

type recordingWriters struct {
	recordingwire.NoProvider
	e *Engine
}

func (w recordingWriters) TitleRecordings(ctx context.Context, max int) int {
	return w.e.TitleRecordings(ctx, max)
}
func (w recordingWriters) SummarizeRecordings(ctx context.Context, max int) int {
	return w.e.SummarizeRecordings(ctx, max)
}
func (w recordingWriters) AfterPrune(time.Time)              { w.e.sweepTranscriptions() }
func (w recordingWriters) AudioDropped(id string, why error) { w.e.Store.audioDroppedLocked(id, why) }
func (w recordingWriters) AudioClosed(id string)             { w.e.Store.audioFileClosed(id) }
