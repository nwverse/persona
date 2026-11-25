package recordingwire

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

// The rules of a recording's stored state that both the knowledge base (its readers, retention and the trim) and the
// recording module's writers apply: pure functions of the types of this package, moved here from internal/kb so that
// one definition serves both sides of the storage port (docs/plans/plan-kb-recording-port.md, section 3.5).

const (
	// MinutesTries: unusable answers for one revision before the minutes (or a day recap) are failed.
	MinutesTries = 3
	// MinutesTimeoutTries: tries of one revision that ran out of time before the minutes are failed.
	MinutesTimeoutTries = 4
	// MinutesMinCalendar, seconds: shorter recordings are not meetings just because a calendar item overlaps.
	MinutesMinCalendar = 5 * 60
	// MinutesLineRunes: a transcript line, or a photo's text, longer than this is cut where the model reads it.
	MinutesLineRunes = 4000
	// MinutesHubWait is how long a hub-mode meeting's minutes wait for its final transcript after the complete upload.
	MinutesHubWait = 45 * time.Minute
	// PhotoLabel marks a photo's line in photos.md and in the minutes' input.
	PhotoLabel = "photo:"
	// The kinds of knowledge-base entry the recordings' writers name.
	KindRecording = "recordings"
	KindMeeting   = "meetings"
	KindEvent     = "events"
	// The day recap's files next to a day recording's meta.json.
	RecapFile      = "recap.json"
	RecapMarkdown  = "recap.md"
	RecapNotesFile = "recap_notes.json"
	// AITitleMinutes is Recording.AITitleFrom of a title the minutes gave.
	AITitleMinutes = "minutes"
)

func ClockAt(start time.Time, sec float64, zone *time.Location) string {
	return start.Add(time.Duration(sec * float64(time.Second))).In(zone).Format("15:04:05")
}

// RecordingEnd is when r ended: ended_at, else its start plus what was recorded (at least a minute).
func RecordingEnd(r *Recording) time.Time {
	end := r.StartedAt.Add(time.Duration(r.Duration * float64(time.Second)))
	if r.EndedAt != nil && r.EndedAt.After(end) {
		end = *r.EndedAt
	}
	if !end.After(r.StartedAt.Add(time.Minute)) {
		end = r.StartedAt.Add(time.Minute)
	}
	return end
}

// MinutesSpan is the recording's start and end in the user's zone, to the second: the span minutes are about.
func MinutesSpan(r *Recording, zone *time.Location) (time.Time, time.Time) {
	return r.StartedAt.In(zone).Truncate(time.Second), RecordingEnd(r).In(zone).Truncate(time.Second)
}

// CoverageTo is how far r's text reaches, in seconds since started_at: its coverage's to, its duration without one
// (the recap reads up to its last segment then, recapLines).
func CoverageTo(r *Recording) float64 {
	if r.Coverage != nil {
		return r.Coverage.To
	}
	return r.Duration
}

// DayStart is the start of day (YYYY-MM-DD) in zone, and of the day after.
func DayStart(day string, zone *time.Location) (time.Time, time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, day, zone)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return d, time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, zone), true
}

// GetsMinutes: the recording gets minutes. A day recording never does (it gets a recap, day_recap.go).
func GetsMinutes(r *Recording) bool {
	if r.Kind == RecordingDay {
		return false
	}
	return r.Kind == RecordingMeeting || r.MinutesAsked || r.Meeting != nil && r.Duration >= MinutesMinCalendar
}

// PlanMinutes sets MinutesState from the rest: pending until minutes of the current revision are written, failed after
// MinutesTries unusable answers for it or MinutesTimeoutTries tries that ran out of time; minutes of an older revision
// keep showing meanwhile. A meeting that completed with no text gets none ("", docs/assistant/meeting-minutes.md
// "Attendance fields"): an attended one is uploaded before anything was said, and may end so.
func PlanMinutes(r *Recording) {
	switch {
	case !GetsMinutes(r):
		r.MinutesState = "" // minutes made while it was one stay readable; nothing more is made
	case r.Trimmed():
		r.MinutesState = MinutesReady // the transcript is gone: the minutes kept are all there will be
	case r.Complete && r.Chars == 0:
		r.MinutesState = ""
	case r.MinutesRev > 0 && r.MinutesRev >= r.Revision:
		r.MinutesState = MinutesReady
	case r.MinutesFailRev == r.Revision && (r.MinutesFails >= MinutesTries || r.MinutesTimeouts >= MinutesTimeoutTries):
		r.MinutesState = MinutesFailed
	default:
		r.MinutesState = MinutesPending
	}
}

// PlanRecap sets a day recording's RecapState from the rest: pending while a recap is due, failed once the model could
// not write it, ready once one was written and none is due, absent before any. A trimmed one is due no more: its
// recap kept is all there will be.
func PlanRecap(r *Recording) {
	if r.Kind != RecordingDay {
		return
	}
	if r.Trimmed() {
		r.RecapDue = nil
	}
	switch {
	case r.RecapDue != nil && RecapFailed(r.RecapDue):
		r.RecapState = MinutesFailed
	case r.RecapDue != nil:
		r.RecapState = MinutesPending
	case r.RecapVersion > 0:
		r.RecapState = MinutesReady
	default:
		r.RecapState = ""
	}
}

// RecapFailed: the model could not write it after the minutes' tries.
func RecapFailed(t *RecapTarget) bool {
	return t.Fails >= MinutesTries || t.Timeouts >= MinutesTimeoutTries
}

// PhotoLine is a photo as photos.md and the minutes' input have it: "[P3] 14:32:05 photo: caption · text · note".
func PhotoLine(n int, clock, words string) string {
	return strings.TrimRight("[P"+strconv.Itoa(n)+"] "+clock+" "+PhotoLabel+" "+words, " ")
}

// MinutesPhotos are the photos m cites (all four sections, and the overview's bullets and conclusions, which cite only
// photos an item cites too); empty without minutes.
func MinutesPhotos(m *Minutes) map[int]bool {
	out := map[int]bool{}
	if m == nil {
		return out
	}
	add := func(n int) {
		if n > 0 {
			out[n] = true
		}
	}
	for _, p := range m.Points {
		add(p.Photo)
	}
	for _, d := range m.Decisions {
		add(d.Photo)
	}
	for _, a := range m.Actions {
		add(a.Photo)
	}
	for _, o := range m.Open {
		add(o.Photo)
	}
	if o := m.Overview; o != nil {
		for _, c := range o.Conclusions {
			add(c.Photo)
		}
		for _, g := range o.Groups {
			for _, t := range g.Topics {
				for _, b := range t.Bullets {
					add(b.Photo)
				}
			}
		}
	}
	return out
}

// HubTranscription is r's hub-mode transcription, nil when r is not transcribed on the hub.
func HubTranscription(r *Recording) *MeetingTranscription {
	if !r.HubTranscribed() {
		return nil
	}
	return r.Conv.Transcription
}

// TranscriptionDone: r is transcribed on the hub and its final pass has run or failed.
func TranscriptionDone(r *Recording) bool {
	t := HubTranscription(r)
	return t != nil && t.Done()
}

// TranscriptionHolds: r's minutes wait for its final transcript: hub mode, the state not final nor failed, at most
// MinutesHubWait after the complete upload.
func TranscriptionHolds(r *Recording, now time.Time) bool {
	t := HubTranscription(r)
	return t != nil && !t.Done() && !t.CompleteAt.IsZero() && now.Before(t.CompleteAt.Add(MinutesHubWait))
}

// HourStart is the start of t's clock hour in zone, by the zone's offset at t, so every hour is an hour of absolute
// time: the hour a fall-back repeats is two of them, never one of two hours.
func HourStart(t time.Time, zone *time.Location) time.Time {
	_, off := t.In(zone).Zone()
	shift := time.Duration(off) * time.Second
	return t.Add(shift).Truncate(time.Hour).Add(-shift).In(zone)
}

// SortDaySegments orders a day recording's segments as segments.json keeps them: by start, then position.
func SortDaySegments(segs []kbwire.RecordingSegment) {
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].Start != segs[j].Start {
			return segs[i].Start < segs[j].Start
		}
		return segs[i].N < segs[j].N
	})
}

// MinutesTaskKey is what makes an action the same task in minutes made again: who and what.
func MinutesTaskKey(a MinutesAction) string { return a.Who + "\x00" + a.What }

// ErrTranscriptExpired: the recording was trimmed; only its minutes are kept.
var ErrTranscriptExpired = errors.New("the transcript expired; only the minutes are kept")

// The model purposes of the meeting recordings' calls (docs/assistant/model-config.md): the minutes job, a question
// asked during a meeting, an attended meeting's room conversation.
const (
	PurposeMeetingMinutes = "meeting.minutes"
	PurposeMeetingAnswer  = "meeting.answer"
	PurposeMeetingRoom    = "meeting.room"
)

// RecordingID is id as stored (lowercase), false when it is not a UUID (8-4-4-4-12 hexadecimal digits).
func RecordingID(id string) (string, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) != 36 {
		return id, false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return id, false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return id, false
			}
		}
	}
	return id, true
}

var dayZones sync.Map // IANA name → *time.Location, loaded once

// DayLocation is the IANA zone name ("Local" is not one).
func DayLocation(name string) (*time.Location, error) {
	if l, ok := dayZones.Load(name); ok {
		return l.(*time.Location), nil
	}
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	dayZones.Store(name, l)
	return l, nil
}

// RecordingZone is the zone r's clock times are in: a day recording's own (tz), else (and when it cannot be loaded)
// user, the knowledge base's zone.
func RecordingZone(r *Recording, user *time.Location) *time.Location {
	if r.Kind == RecordingDay && r.TZ != "" {
		if l, err := DayLocation(r.TZ); err == nil {
			return l
		}
	}
	return user
}

// DaySpan is the span day recording r covers as of now: from its start to the end of what it holds, or to its end
// once it has one; one still being recorded reaches now, at most the midnight that ends its day (in its zone; user is
// the knowledge base's zone, RecordingZone).
func DaySpan(r *Recording, now time.Time, user *time.Location) (time.Time, time.Time) {
	end := r.StartedAt.Add(time.Duration(CoverageTo(r) * float64(time.Second)))
	switch {
	case r.EndedAt != nil:
		end = later(end, *r.EndedAt)
	case !r.Complete:
		if _, midnight, ok := DayStart(r.Day, RecordingZone(r, user)); ok {
			end = later(end, earlier(now, midnight))
		}
	}
	return r.StartedAt, end
}

// RecordedWithin: day recording r's span as of now (DaySpan) overlaps from–to.
func RecordedWithin(r *Recording, now, from, to time.Time, user *time.Location) bool {
	start, end := DaySpan(r, now, user)
	return start.Before(to) && (end.After(from) || !start.Before(from))
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
