package kb

import (
	"context"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The recording module's write side while its writers are still in this package (docs/plans/plan-kb-recording-port.md,
// section 6: PRs 3 to 5 move them into internal/modules/recording, and this file goes with the last of them). The
// module gives it to a knowledge base in its hook; nothing in the Core names it.

// RecordingWriters is the writers of engine e that are still in this package (recordingwire.WritersInCore): the
// summary round. The others are the module's (internal/modules/recording/writers), whose provider carries this one
// for the rest.
func RecordingWriters(e *Engine) recordingwire.WritersInCore { return recordingWriters{e: e} }

type recordingWriters struct{ e *Engine }

func (w recordingWriters) SummarizeRecordings(ctx context.Context, max int) int {
	return w.e.SummarizeRecordings(ctx, max)
}
