package writers

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Writers is the recordings' write side of one knowledge base (recordingwire.Provider): the minutes and day-recap
// writers of this package over the storage port, and, until they move here too, the knowledge base's own remaining
// writers (recordingwire.WritersInCore). The module makes one in its hook and gives it to the knowledge base.
type Writers struct {
	core recordingwire.Core
	// rest is the knowledge base's writers that are still its own code.
	rest recordingwire.WritersInCore
	// minutesWritten is told when a recording's minutes for its current revision are written: the assistant pushes
	// them. Not for minutes made again only because a photo was added or deleted. Called from the round, outside the
	// store's locks. dayRecapWritten is told once a day (Recording.Day, across the day's parts) of the recap the day's
	// push goes with: it pushes to the destinations not in r.RecapPushTook and returns those that took it now; an
	// error leaves the push due (OnWritten sets both).
	minutesWritten  func(ctx context.Context, r Recording, m Minutes)
	dayRecapWritten func(ctx context.Context, r Recording, d DayRecap) (took []string, err error)

	// What tests set on the engine a call makes (engine.jobLeft, minutesCall, minutesPart).
	jobLeft                  func(ctx context.Context) time.Duration
	minutesCall, minutesPart int
}

// New is the write side of the knowledge base behind core; rest is its writers that are still its own code (nil:
// none, for a knowledge base that only answers requests).
func New(core recordingwire.Core, rest recordingwire.WritersInCore) *Writers {
	return &Writers{core: core, rest: rest}
}

// OnWritten sets what is told when minutes are written and when a day's recap is to be pushed, before the knowledge
// base is used.
func (w *Writers) OnWritten(minutes func(ctx context.Context, r Recording, m Minutes),
	dayRecap func(ctx context.Context, r Recording, d DayRecap) (took []string, err error)) {
	w.minutesWritten, w.dayRecapWritten = minutes, dayRecap
}

// Of is the Writers a knowledge base was given as its provider; nil when p is another provider (no module).
func Of(p recordingwire.Provider) *Writers {
	w, _ := p.(*Writers)
	return w
}

// engine is the knowledge base's engine as the writers see it now: its models and evening clock as they stand, the
// module's callbacks.
func (w *Writers) engine() *engine {
	e := &engine{Store: newStore(w.core), MinutesWritten: w.minutesWritten, DayRecapWritten: w.dayRecapWritten,
		jobLeft: w.jobLeft, minutesCall: w.minutesCall, minutesPart: w.minutesPart}
	if w.core.HasModel() {
		e.ModelFor = func(purpose string) Model { return w.core.Model(known(recordingwire.Purpose(purpose))) }
	}
	if _, ok := w.core.DayRecapClock(); ok {
		e.DayRecapClock = func() (hour, minute int) {
			c, _ := w.core.DayRecapClock()
			return c.Hour, c.Minute
		}
	}
	return e
}

// The rounds (recordingwire.Provider): the minutes' and the day recaps' are this package's, the others the knowledge
// base's own until they move here.

func (w *Writers) WriteMinutes(ctx context.Context, max int) int {
	return w.engine().WriteMinutes(ctx, max)
}
func (w *Writers) WriteDayRecaps(ctx context.Context, max int) int {
	return w.engine().WriteDayRecaps(ctx, max)
}

// TellDayRecap tells the module of recap d of day recording r as a round does for a push that is due
// (OnWritten): the push ids it took, and an error while some destination has not taken it.
func (w *Writers) TellDayRecap(ctx context.Context, r Recording, d DayRecap) ([]string, error) {
	return w.dayRecapWritten(ctx, r, d)
}

func (w *Writers) TitleRecordings(ctx context.Context, max int) int {
	return w.rest.TitleRecordings(ctx, max)
}
func (w *Writers) SummarizeRecordings(ctx context.Context, max int) int {
	return w.rest.SummarizeRecordings(ctx, max)
}
func (w *Writers) AfterPrune(now time.Time)          { w.rest.AfterPrune(now) }
func (w *Writers) AudioDropped(id string, why error) { w.rest.AudioDropped(id, why) }
func (w *Writers) AudioClosed(id string)             { w.rest.AudioClosed(id) }

// The requests of the module's routes.

// AskMinutes has recording id's minutes (a day recording's recap) made again at the next round.
func (w *Writers) AskMinutes(id string) error { return w.engine().AskMinutes(id) }

// SetMinutesTodos records the to-dos made of the actions of version of recording id's minutes.
func (w *Writers) SetMinutesTodos(id string, version int, todos, byTask map[string]string) (*Minutes, error) {
	return w.engine().SetMinutesTodos(id, version, todos, byTask)
}

// SetDayRecapTodos records the to-dos made of the actions of version of day recording id's recap.
func (w *Writers) SetDayRecapTodos(id string, version int, confirmed []MinutesAction) (*DayRecap, error) {
	return w.engine().SetDayRecapTodos(id, version, confirmed)
}

// What the writers still in the knowledge base call of these (recordingwire.MinutesFromCore).

func (w *Writers) PutMinutes(id recordingwire.ID, m *Minutes) (bool, error) {
	return newStore(w.core).putMinutes(string(id), m)
}
func (w *Writers) RenderMinutes(m *Minutes, start time.Time, zone *time.Location) string {
	return renderMinutes(m, start, zone)
}
func (w *Writers) RemakeMinutes(r *Recording)     { remakeMinutes(r) }
func (w *Writers) QuietMinutes(r *Recording) bool { return quietMinutes(r) }
func (w *Writers) DropOverviewPhotos(m *Minutes, cites func(n int) bool) {
	dropOverviewPhotos(m, cites)
}
func (w *Writers) RepairDayRecap(r *Recording, u *RecordingUpload) (bool, error) {
	return w.engine().repairDayRecap(r, u)
}
func (w *Writers) RebaseRecap(r *Recording, recapped bool, now time.Time) {
	rebaseRecap(r, recapped, now)
}
func (w *Writers) PruneRecapNotes(r *Recording, segs []RecordingSegment) error {
	return newStore(w.core).pruneRecapNotes(r, segs)
}

var _ recordingwire.Provider = (*Writers)(nil)
