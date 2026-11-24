package kb

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Recordings (docs/assistant/knowledge.md "Recordings", docs/protocol/protocol.md "Recording sync"): the phone uploads each recording's transcript,
// keyed by its UUID, the whole state every time (revision orders them), so replays, reordering and a transcript
// completed later all land on one entry. Only text: the audio stays on the phone, except a meeting transcribed on the
// hub, whose audio the hub keeps until the minutes from its final transcript are written (recording_audio.go). A
// directory per recording: meta.json, segments.json, transcript.md (what the searches read: the transcript of record,
// hub_segments.go), summary.md (the model's, once it is complete), and for an attended meeting live.json (live.go),
// for a meeting with photos photos.json, photos.md and attachments/ (recording_photos.go), for one transcribed on the
// hub hub_segments.json and attachments/audio/ (transcription.go). Uploads journal the accepted files until all
// replacements commit (recording_commit.go). Past recording_days a recording with minutes keeps only meta.json, its
// minutes and the photos they cite until minutes_days (recording_trim.go); any other goes.
// recordings/.deleted.json is the tombstones: a late upload of a deleted one is refused. Kept recordingDays.

// KindRecording is the recordings' kind and directory.
const KindRecording = "recordings"

// DefaultRecordingDays is how long recordings are kept unless the user says otherwise (assistant.recording_days).
const DefaultRecordingDays = 14

// DefaultMinutesDays is how long a meeting's minutes are kept unless the user says otherwise (assistant.minutes_days);
// 0 means forever; a finite window is never shorter than the recording itself (MinutesDaysFor).
const DefaultMinutesDays = 0

// Limits of one upload.
const (
	maxRecordingSegments = 20000
	maxRecordingText     = 4 << 20
)

// Defined in the contract package kbwire (docs/server/architecture.md "Contract packages"); the old names stay.
type RecordingSegment = kbwire.RecordingSegment

// RecordingBookmark is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingBookmark = recordingwire.RecordingBookmark

// RecordingCalendar is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingCalendar = recordingwire.RecordingCalendar

// Re-exported from recordingwire.
const (
	RecordingNote    = recordingwire.RecordingNote
	RecordingMeeting = recordingwire.RecordingMeeting
	RecordingDay     = recordingwire.RecordingDay
)

// Limits of an upload's calendar hint.
const (
	maxCalendarTitle     = 200
	maxCalendarAttendees = 100
	maxCalendarName      = 100
)

// RecordingUpload is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingUpload = recordingwire.RecordingUpload

// MeetingRef is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingRef = recordingwire.MeetingRef

// Re-exported from recordingwire.
const (
	MinutesPending = recordingwire.MinutesPending
	MinutesReady   = recordingwire.MinutesReady
	MinutesFailed  = recordingwire.MinutesFailed
)

// Recording is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Recording = recordingwire.Recording

// RecordingState is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingState = recordingwire.RecordingState

// Re-exported from recordingwire.
const (
	RecordingStored  = recordingwire.RecordingStored
	RecordingStale   = recordingwire.RecordingStale
	RecordingExpired = recordingwire.RecordingExpired
)

// RecordingPut is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingPut = recordingwire.RecordingPut

var (
	// ErrRecordingDeleted: the recording was deleted (a tombstone); the phone must not upload it again.
	ErrRecordingDeleted = errors.New("this recording was deleted")
	// ErrBadRecording wraps what is wrong with an upload.
	ErrBadRecording = errors.New("invalid recording")
	uuidRe          = lazyre.New(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// RecordingID is id as stored (lowercase), false when it is not a UUID.
func RecordingID(id string) (string, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	return id, uuidRe.MatchString(id)
}

func badRecording(f string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRecording, fmt.Sprintf(f, a...))
}

// recordingDays is days, or the default when unset.
func recordingDays(days int) int {
	if days <= 0 {
		return DefaultRecordingDays
	}
	return days
}

func (s *Store) Recording(id string) (*Recording, bool) {
	var r Recording
	raw, err := s.recordingRead(id, "meta.json")
	if err != nil || raw == nil || json.Unmarshal(raw, &r) != nil {
		return nil, false
	}
	if r.Kind == RecordingDay && r.Chars > 0 && r.TextRev == 0 {
		r.TextRev = 1 // a day recording's text stored without its count: what no recap or note read yet
	}
	r.Persisted = true
	s.recFilesMu.RLock()
	m, err := s.readMeeting(id)
	s.recFilesMu.RUnlock()
	if err != nil {
		// Unreadable: as a room meeting whose role is off (it feeds nothing, takes no question), and private.
		m = &Meeting{Room: &MeetingRoom{State: RoomNone}}
	}
	r.Conv = m
	return &r, true
}

// Recordings lists them all (the tombstones file is not one).
func (s *Store) Recordings() []*Recording {
	var out []*Recording
	for _, id := range s.IDs(KindRecording) {
		if strings.HasPrefix(id, ".") {
			continue
		}
		if r, ok := s.Recording(id); ok {
			out = append(out, r)
		}
	}
	return out
}

// RecordingSegments reads a recording's transcript of record (TranscriptOfRecord): its upload's segments, in hub mode
// with the hub's sentences.
func (s *Store) RecordingSegments(id string) []RecordingSegment {
	files, m, h, err := s.recordSnapshot(id)
	if err != nil {
		return nil
	}
	return recordOf(files, m, h)
}

func (s *Store) tombstonesPath() string { return filepath.Join(s.Dir, KindRecording, ".deleted.json") }

func (s *Store) tombstones() map[string]time.Time {
	m := map[string]time.Time{}
	_, _ = readJSON(s.tombstonesPath(), &m)
	return m
}

// recordingName is what a recording is called in lists: the user's title, the phone calendar's, the matched calendar
// item's, the model's, else when it started (NameSource says which).
func (s *Store) recordingName(r *Recording) string {
	switch r.NameSource() {
	case NamedByRename:
		return r.Renamed
	case NamedByTitle:
		return r.Title
	case NamedByCalendar:
		return calendarTitle(r)
	case NamedByModel:
		return r.AITitle
	case "":
		if r.Kind == RecordingDay {
			return "全天录音 " + r.Day // Engine.recordingName has it in the owner's language
		}
	}
	return "录音 " + r.StartedAt.In(s.Zone()).Format("2006-01-02 15:04")
}

// Re-exported from recordingwire.
const (
	NamedByRename   = recordingwire.NamedByRename
	NamedByTitle    = recordingwire.NamedByTitle
	NamedByCalendar = recordingwire.NamedByCalendar
	NamedByModel    = recordingwire.NamedByModel
)

// cleanUpload checks an upload and puts its segments in order (empty ones dropped).
func cleanUpload(u *RecordingUpload) error {
	if u.Revision <= 0 {
		return badRecording("revision must be > 0")
	}
	if u.StartedAt.IsZero() {
		return badRecording("started_at is required")
	}
	if u.Device != "phone" && u.Device != "watch" {
		return badRecording("device must be phone or watch")
	}
	// A day upload over the limits is day_full, not refused here: the hub's count goes with it (recording_day.go).
	day := strings.EqualFold(strings.TrimSpace(u.Kind), RecordingDay)
	if len(u.Segments) > maxRecordingSegments {
		if !day {
			return badRecording("at most %d segments", maxRecordingSegments)
		}
		u.DayFull = true
	}
	u.Next = len(u.Segments)
	// A fresh slice: the caller's upload (its Segments' backing array) is never changed.
	segs := make([]RecordingSegment, 0, len(u.Segments))
	total := 0
	for i, sg := range u.Segments {
		sg.Text, sg.N, sg.Source = strings.TrimSpace(sg.Text), 0, "" // only the hub sets a source
		if day {
			sg.N = i + 1    // its position, empty ones counted
			sg.Echo = false // Persona never speaks in an all-day recording
		}
		if sg.Text == "" {
			continue
		}
		total += len(sg.Text)
		segs = append(segs, sg)
	}
	if total > maxRecordingText {
		if !day {
			return badRecording("the transcript is over %d bytes", maxRecordingText)
		}
		u.DayFull = true
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	u.Segments = segs
	u.Bookmarks = slices.Clone(u.Bookmarks)
	if day {
		u.Spoken = nil
	}
	// Persona's echo: the phone's mark, or the same rule against the stretches it says Persona spoke in.
	markEcho(u.Segments, u.Spoken)
	sort.SliceStable(u.Bookmarks, func(i, j int) bool { return u.Bookmarks[i].At < u.Bookmarks[j].At })
	u.Title = oneLine(u.Title)
	u.Language = strings.TrimSpace(u.Language)
	switch u.Kind = strings.ToLower(strings.TrimSpace(u.Kind)); u.Kind {
	case "", RecordingNote:
		u.Kind = RecordingNote
	case RecordingMeeting:
	case RecordingDay:
		return cleanUploadDay(u)
	default:
		return badRecording("kind must be meeting, note or day")
	}
	u.Day, u.TZ, u.Part, u.Coverage = "", "", 0, nil // a day recording's only
	return cleanUploadCalendar(u)
}

// cleanUploadCalendar trims the calendar hint, dropping it when it says nothing.
func cleanUploadCalendar(u *RecordingUpload) error {
	c := u.Calendar
	if c == nil {
		return nil
	}
	c.Title, c.Location = strings.Join(strings.Fields(c.Title), " "), strings.Join(strings.Fields(c.Location), " ")
	if len([]rune(c.Title)) > maxCalendarTitle || len([]rune(c.Location)) > maxCalendarTitle {
		return badRecording("calendar title and location are at most %d characters", maxCalendarTitle)
	}
	if len(c.Attendees) > maxCalendarAttendees {
		return badRecording("at most %d calendar attendees", maxCalendarAttendees)
	}
	names := c.Attendees[:0]
	for _, n := range c.Attendees {
		n = strings.Join(strings.Fields(n), " ")
		if len([]rune(n)) > maxCalendarName {
			return badRecording("a calendar attendee is at most %d characters", maxCalendarName)
		}
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	c.Attendees = names
	if c.Title == "" && c.Location == "" && len(c.Attendees) == 0 {
		u.Calendar = nil
	}
	return nil
}

// PutRecording stores an upload (the whole state of recording id): RecordingStale when a newer revision is there,
// RecordingExpired when it started before the retention or was trimmed, ErrRecordingDeleted after a delete.
// Attendance fields that are wrong never fail it: attending is then off, and the result says why
// (normalizeAttendance). Engine.LiveChanged hears of a stored upload of a recording that attends or attended until now.
func (e *Engine) PutRecording(rawID string, u RecordingUpload, now time.Time) (RecordingPut, error) {
	put, live, err := e.putRecording(rawID, u, now)
	if err == nil && live && e.LiveChanged != nil {
		e.LiveChanged(put.id)
	}
	return put.RecordingPut, err
}

type recordingPut struct {
	RecordingPut
	id string
}

// putRecording is PutRecording; live reports a stored upload the attending feeder should look at.
func (e *Engine) putRecording(rawID string, u RecordingUpload, now time.Time) (put recordingPut, live bool, err error) {
	put.Attendance = AttendanceOff
	put.Room = u.Room != nil
	// Whatever the upload became (stored, stale, expired), the mode in effect after it.
	defer func() {
		put.Transcription, put.TranscriptionProblem = transcriptionOf(put.Meeting, u.Transcription, u.Kind, u.TranscriptionProblem)
	}()
	if e.Store.ReadOnly() {
		return put, false, ErrReadOnly
	}
	id, ok := RecordingID(rawID)
	if !ok {
		return put, false, badRecording("id must be a UUID")
	}
	put.id = id
	if err := cleanUpload(&u); err != nil {
		return put, false, err
	}
	put.Segments = u.Segments
	put.AttendanceProblem = normalizeAttendance(&u)
	day := u.Kind == RecordingDay
	if day {
		put.AttendanceProblem, put.Room = dropUploadRoom(&u, put.AttendanceProblem), false
	}
	put.Revision = u.Revision
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if _, gone := st.tombstones()[id]; gone {
		return put, false, ErrRecordingDeleted
	}
	r, exists := st.Recording(id)
	if exists {
		put.Revision, put.Base, put.Next = r.Revision, r.Base, r.Next
	}
	if u.StartedAt.Before(now.UTC().AddDate(0, 0, -recordingDays(e.RecordingDays()))) {
		put.Status = RecordingExpired
		return put, false, nil
	}
	if exists {
		put.Room, put.Meeting = put.Room || r.RoomMeeting(), r.Conv
	}
	if exists && r.Trimmed() {
		// Its transcript went with the retention (recording_days made longer since lets it back in the window): the
		// minutes stay what they are, and the hub takes no transcript back.
		put.Status = RecordingExpired
		return put, false, nil
	}
	if exists && (r.Kind == RecordingDay) != day {
		return put, false, ErrKindChanged
	}
	if exists && r.Revision > u.Revision {
		put.Status, put.Attendance = RecordingStale, r.AttendanceRole()
		putNamed(&put.RecordingPut, r)
		return put, false, nil
	}
	if u.DayFull {
		return put, false, DayFullError{Next: put.Next}
	}
	rebase, recapped := false, false
	if day && exists && r.Base != u.Revision {
		// A new base: a recap made from the old one loses at once what the new transcript no longer says.
		rebase = true
		if recapped, err = e.repairDayRecap(r, &u); err != nil {
			return put, false, err
		}
	}
	wasAttending := exists && r.Attending()
	meeting, parts, err := e.putMeetingFields(id, &u, now)
	if err != nil {
		return put, false, err
	}
	defer e.meetingChanged(id, parts)
	if meeting != nil && meeting.Room != nil {
		put.Room, put.AttendanceProblem = true, u.RoomProblem
	}
	put.Meeting = meeting
	// A newer transcript (not a retry of the accepted upload): its minutes are pushed as before.
	advanced := !exists || u.Revision > r.Revision
	if !exists {
		r = &Recording{ID: id}
	}
	r.Conv = meeting
	r.Revision, r.StartedAt, r.EndedAt, r.Duration = u.Revision, u.StartedAt.UTC(), u.EndedAt, u.Duration
	if r.EndedAt != nil {
		t := r.EndedAt.UTC()
		r.EndedAt = &t
	}
	// An ended hub-mode meeting is as long as the audio the hub holds when the upload says less (#9587): a phone killed
	// mid-segment loses the segment it was writing and ends the recording before it, while the hub got that audio.
	if r.EndedAt != nil && r.HubTranscribed() {
		if held := e.audioHeldEnd(id); held > r.Duration {
			r.Duration = held
			if end := r.StartedAt.Add(time.Duration(held * float64(time.Second))); end.After(*r.EndedAt) {
				r.EndedAt = &end
			}
		}
	}
	if r.Renamed != "" && u.Title != r.Title { // the phone renamed it since the web did: the newer name wins (#8877)
		r.Renamed, r.RenamedAt = "", nil
	}
	r.Language, r.Device, r.Title, r.Bookmarks, r.Complete = u.Language, u.Device, u.Title, u.Bookmarks, u.Complete
	r.Kind, r.Calendar = u.Kind, u.Calendar
	r.Attendance, r.AttendingSession = u.Attendance, u.AttendingSession // never a room meeting's (putMeetingFields)
	// chars counts the transcript of record without Persona's echo, which is not the meeting's text (a meeting of
	// only that has none, planMinutes): in hub mode the hub's sentences too (a phone that wrote nothing).
	r.Segments, r.Chars = len(u.Segments), recordChars(u.Segments)
	if r.HubTranscribed() {
		r.Chars = recordChars(TranscriptOfRecord(true, u.Segments, st.hubSegmentsOf(id)))
	}
	if day {
		putDay(r, &u)
		if rebase {
			rebaseRecap(r, recapped, now)
		}
	}
	if advanced {
		r.MinutesQuiet = 0 // a retry keeps a generation made again only for a photo quiet
	}
	planMinutes(r) // after Chars: a meeting that completed with no text gets none
	planRecap(r)
	r.Name = e.recordingName(r)
	r.Updated = now.UTC()
	if day && exists {
		// The notes of a chunk whose lines the upload changed (an erase above all) quote what it no longer says: they go
		// now, not with the next note written. Those of the others stay theirs.
		if err := st.pruneRecapNotes(r, u.Segments); err != nil {
			return put, false, err
		}
	}
	if err := st.commitRecording(r, u.Segments); err != nil {
		return put, false, err
	}
	put.Status, put.Attendance = RecordingStored, r.AttendanceRole()
	put.Revision, put.Base, put.Next = r.Revision, r.Base, r.Next
	putNamed(&put.RecordingPut, r)
	put.TitleDue = liveTitleDue(r, now)
	st.notify(KindRecording, id, "metadata")
	st.notify(KindRecording, id, "messages")
	e.index(KindRecording, id, r.Name, r.Tagging, r.OneLine, r.StartedAt)
	if r.EndedAt != nil {
		// The end batch is timed from when the hub first saw the end (live.go); the upload stands either way, and the
		// next one carrying ended_at tries again.
		if err := st.liveEnded(id, r.Attending() || r.RoomMeeting(), now); err != nil {
			e.logf("recording %s: live end: %v", id, err)
		}
	}
	return put, wasAttending || r.Attending() || r.RoomMeeting(), nil
}

func nonNilSegments(s []RecordingSegment) []RecordingSegment {
	if s == nil {
		return []RecordingSegment{}
	}
	return s
}

// renderTranscript is transcript.md: a line per segment (and bookmark) at its clock time in the user's zone; Persona's
// echo (#5905) keeps its line, marked.
func (s *Store) renderTranscript(r *Recording, segs []RecordingSegment) string {
	zone := s.RecordingZone(r)
	at := func(sec float64) string {
		return r.StartedAt.Add(time.Duration(sec * float64(time.Second))).In(zone).Format("15:04:05")
	}
	type line struct {
		sec  float64
		text string
	}
	var lines []line
	for _, sg := range segs {
		text := strings.Join(strings.Fields(sg.Text), " ")
		if sg.Echo {
			text = "[Persona 回声] " + text // i18n-ignore: knowledge-base data, like the bookmarks' mark
		}
		lines = append(lines, line{sg.Start, "- " + at(sg.Start) + " " + text})
	}
	for _, b := range r.Bookmarks {
		lines = append(lines, line{b.At, "- " + at(b.At) + " [书签] " + oneLine(b.Label)})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].sec < lines[j].sec })
	var b strings.Builder
	b.WriteString("# 录音 · ")
	if t := cmp.Or(r.Title, r.AITitle); t != "" {
		b.WriteString(t + " · ")
	}
	fmt.Fprintf(&b, "%s · %s\n\n", r.StartedAt.In(zone).Format("2006-01-02 15:04"), zone)
	for _, l := range lines {
		b.WriteString(strings.TrimRight(l.text, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// DeleteRecording removes recording id (files and index) and leaves a tombstone; false when there was none.
func (e *Engine) DeleteRecording(rawID string, now time.Time) (bool, error) {
	if e.Store.ReadOnly() {
		return false, ErrReadOnly
	}
	defer e.Store.SearchIndexPurge() // its summary, minutes and recap leave the search index at its next update
	id, ok := RecordingID(rawID)
	if !ok {
		return false, badRecording("id must be a UUID")
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	t := st.tombstones()
	if _, ok := t[id]; !ok {
		t[id] = now.UTC()
		if err := writeJSON(st.tombstonesPath(), t); err != nil {
			return false, err
		}
	}
	r, existed := st.Recording(id)
	if !existed {
		return false, nil
	}
	st.recFilesMu.Lock()
	st.audioDroppedLocked(id, ErrRecordingDeleted)
	err := os.RemoveAll(st.path(KindRecording, id))
	st.recFilesMu.Unlock()
	st.audioFileClosed(id)
	if err != nil {
		return false, err
	}
	e.unlinkRecording(id, r.Projects)
	e.unindex(KindRecording, []string{id})
	st.notify(KindRecording, id, "metadata")
	return true, nil
}

// unlinkRecording takes a gone recording off its projects.
func (e *Engine) unlinkRecording(id string, projects []string) {
	for _, slug := range projects {
		if p, ok := e.Store.Project(slug); ok {
			p.Items = removeStrings(p.Items, []string{KindRecording + "/" + id})
			_ = e.Store.putEntity(KindProject, p.Slug, p, &p.Tagging, false)
		}
	}
}

// RecordingStates is every recording kept (trimmed ones too) and every tombstone, for the phone to reconcile.
func (e *Engine) RecordingStates() (kept []RecordingState, deleted []string) {
	kept, deleted = []RecordingState{}, []string{}
	for _, r := range e.Store.Recordings() {
		s := RecordingState{ID: r.ID, Revision: r.Revision, Complete: r.Complete, StartedAt: r.StartedAt,
			MinutesState: r.MinutesState, TrimmedAt: r.TrimmedAt}
		if s.NameSource = r.NameSource(); s.NameSource != "" {
			s.Name = r.Name
		}
		if r.Kind == RecordingDay {
			next, base := r.Next, r.Base
			s.Kind, s.Day, s.Part, s.Next, s.Base = r.Kind, r.Day, r.Part, &next, &base
			s.RecapState, s.RecapFinal, s.RecapFinalAt = r.RecapState, r.RecapFinal, r.RecapFinalAt
		}
		kept = append(kept, s)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].StartedAt.After(kept[j].StartedAt) })
	for id := range e.Store.tombstones() {
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)
	return kept, deleted
}

// PruneRecordings applies retention to files and the index. Past recording_days, minutes are kept forever or within
// a finite minutes_days window, and the recording is trimmed to them (recording_trim.go); other recordings go.
// Interrupted trims finish whatever the window. Tombstones older than recording_days go too.
// It returns how many recordings were deleted, not counting trimmed ones. Then it deletes the hub-mode audio that
// should be gone and mends what a crash left of a hub transcript (sweepTranscriptions).
func (e *Engine) PruneRecordings(now time.Time) int {
	if e.Store.ReadOnly() {
		return 0
	}
	n := e.pruneRecordings(now)
	e.sweepTranscriptions()
	if n > 0 {
		e.Store.SearchIndexPurge() // their summaries, minutes and recaps leave the search index at its next update
	}
	return n
}

func (e *Engine) pruneRecordings(now time.Time) int {
	cut := now.UTC().AddDate(0, 0, -recordingDays(e.RecordingDays()))
	minutesDays := e.MinutesDays()
	minutesCut := now.UTC().AddDate(0, 0, -minutesDays)
	st := e.Store
	// The trims' meeting changes (photos, transcription) are told once recMu is let go (deferred calls run last first).
	told := map[string][]string{}
	var order []string
	tell := func(id string, parts []string) {
		if len(parts) > 0 {
			if told[id] == nil {
				order = append(order, id)
			}
			told[id] = append(told[id], parts...)
		}
	}
	defer func() {
		if len(order) > 0 {
			st.SearchIndexPurge() // a trimmed recording's files, too
		}
		for _, id := range order {
			e.meetingChanged(id, told[id])
		}
	}()
	st.recMu.Lock()
	defer st.recMu.Unlock()
	var gone []string
	for _, r := range st.Recordings() {
		if !r.StartedAt.Before(cut) {
			// Within recording_days only a trimmed one has anything to do: the files an interrupted trim left (a crash
			// after its meta.json, a failed removal) go even once recording_days made longer lets it back in the window.
			if r.Trimmed() {
				tell(r.ID, e.trimPruned(r, now))
			}
			continue
		}
		if (minutesDays == 0 || !r.StartedAt.Before(minutesCut)) && (r.Trimmed() || st.hasMinutes(r.ID) || st.hasRecap(r.ID)) {
			tell(r.ID, e.trimPruned(r, now))
			continue
		}
		st.recFilesMu.Lock()
		st.audioDroppedLocked(r.ID, ErrNotFound)
		err := os.RemoveAll(st.path(KindRecording, r.ID))
		st.recFilesMu.Unlock()
		st.audioFileClosed(r.ID)
		if err == nil {
			e.unlinkRecording(r.ID, r.Projects)
			st.notify(KindRecording, r.ID, "metadata")
			gone = append(gone, r.ID)
		}
	}
	e.unindex(KindRecording, gone)
	// Tombstones keep the recording_days window, not minutes_days. One only has to outlive the uploads it refuses, and
	// an upload of a recording that started before that window is expired anyway (putRecording), whether the recording
	// is gone, trimmed or deleted by the user. A tombstone is never older than its recording's start (it is written when
	// the recording is deleted), so one that falls out of the window stands for a recording past it: keeping it longer,
	// as long as a trimmed recording could have stayed, would refuse nothing more. Its other use, the phone dropping a
	// copy deleted elsewhere, ends with the window too: the phone drops every recording older than days itself.
	t := st.tombstones()
	n := len(t)
	for id, at := range t {
		if at.Before(cut) {
			delete(t, id)
		}
	}
	if len(t) != n {
		_ = writeJSON(st.tombstonesPath(), t)
	}
	return len(gone)
}

// trimPruned trims recording r for PruneRecordings (which holds recMu), or finishes its trim: idempotent, it says
// what changed and indexes the recording again only when something did. parts are the meeting parts that changed
// (PartPhotos, PartTranscription), which the caller tells MeetingChanged once it let recMu go.
func (e *Engine) trimPruned(r *Recording, now time.Time) (parts []string) {
	st := e.Store
	trimmed, photos, err := st.trimRecording(r, now)
	if err != nil {
		e.logf("recording %s: trim: %v", r.ID, err)
	} else if trimmed {
		st.notify(KindRecording, r.ID, "metadata")
		st.notify(KindRecording, r.ID, "messages")
		st.notify(KindRecording, r.ID, "summary")
		e.index(KindRecording, r.ID, r.Name, r.Tagging, r.OneLine, r.StartedAt)
	}
	if photos {
		parts = append(parts, PartPhotos)
	}
	if err == nil {
		parts = append(parts, e.trimTranscription(r.ID, now)...)
	}
	return parts
}

// RecordingDays is the retention in days (Engine.RecordingDaysFunc, the assistant's setting), the default without one.
func (e *Engine) RecordingDays() int {
	if e.RecordingDaysFunc != nil {
		return recordingDays(e.RecordingDaysFunc())
	}
	return DefaultRecordingDays
}

// MinutesDays is how many days a meeting's minutes are kept (Engine.MinutesDaysFunc, the assistant's setting, the
// default without one): 0 means forever; otherwise never fewer than RecordingDays.
func (e *Engine) MinutesDays() int {
	d := 0
	if e.MinutesDaysFunc != nil {
		d = e.MinutesDaysFunc()
	}
	return MinutesDaysFor(d, e.RecordingDays())
}

// MinutesDaysFor is the minutes' retention in effect for the setting minutes (<= 0: DefaultMinutesDays) and the
// recordings' days: 0 means forever; minutes never go before the recording they belong to.
func MinutesDaysFor(minutes, recording int) int {
	if minutes <= 0 {
		return DefaultMinutesDays
	}
	return max(minutes, recordingDays(recording))
}

func (e *Engine) unindex(kind string, ids []string) {
	if e.Unindex != nil && len(ids) > 0 {
		e.Unindex(kind, ids)
	}
}

// recordingSummary is the model's answer for a recording (kb/recording).
type recordingSummary struct {
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	OneLine    string     `json:"one_line"`
	Importance string     `json:"importance"`
	Relevance  modelScore `json:"relevance"`
	Tags       []string   `json:"tags"`
	Projects   []struct {
		Name   string `json:"name"`
		Update string `json:"update"`
	} `json:"projects"`
}

// recordingPromptChars bounds the transcript the model reads (the start and the end kept).
const recordingPromptChars = 24000

// SummarizeRecordings writes the summary and tags of complete recordings whose latest revision has none yet, newest
// first, at most max model calls per run (failed ones included; one that needed no call, changed meanwhile, costs none);
// how many it did. A recording whose summary failed waits like a project whose rewrite failed (retryWait) and the next
// one goes on, so one transcript the model keeps failing on neither costs a call every round nor holds back the others;
// a new revision of it is tried at once. A paused connection or no model set up stops the run without counting: the
// rest stay due.
func (e *Engine) SummarizeRecordings(ctx context.Context, max int) int {
	if e.Model == nil && e.ModelFor == nil {
		return 0
	}
	now := time.Now()
	var todo []*Recording
	due := map[string]bool{}
	for _, r := range e.Store.Recordings() {
		if summaryDue(r) {
			due[r.ID] = true
			if e.retries().recordings.waits(r.ID, recordingAbout(r), now) {
				continue
			}
			todo = append(todo, r)
		}
	}
	e.retries().recordings.keep(due) // summarized, trimmed or gone in the meantime
	sort.Slice(todo, func(i, j int) bool { return todo[i].StartedAt.After(todo[j].StartedAt) })
	n, calls := 0, 0
	for _, r := range todo {
		if calls >= max || ctx.Err() != nil {
			break
		}
		called, err := e.summarizeRecording(ctx, r)
		if called {
			calls++
		}
		if err != nil {
			if deferred(ctx, err) { // the connection is held, no model, or the run ended: they stay due
				break
			}
			f := e.retries().recordings.fail(r.ID, recordingAbout(r))
			e.logf("recording %s: %v (failed %d time(s) in a row, next try in %s)", r.ID, err, f.failures, retryWait(f.failures))
			continue
		}
		e.retries().recordings.clear(r.ID)
		if called {
			n++
		}
	}
	return n
}

// recordingAbout is what a recording's summary is asked about: its revision, and in hub mode whether its final
// transcript is in (a new upload, or the final pass's end, is tried at once).
func recordingAbout(r *Recording) string {
	about := strconv.FormatInt(r.Revision, 10)
	if hubFinalIn(r) {
		about += "/final"
	}
	return about
}

// summaryDue: recording r's short summary is to be written (again): a complete recording with text, not trimmed nor
// an all-day one, whose latest revision has none, or, in hub mode, whose summary was written before the final pass
// stored its transcript ("the short summary (made again after the final pass)"). r's Conv is read.
func summaryDue(r *Recording) bool {
	if !r.Complete || r.Chars == 0 || r.Trimmed() || r.Kind == RecordingDay {
		return false
	}
	return r.SummarizedRev < r.Revision || hubFinalIn(r) && !r.SummarizedFinal
}

// hubFinalIn: r is transcribed on the hub and its final pass ended (final or failed): its transcript of record is the
// final one.
func hubFinalIn(r *Recording) bool {
	t := hubTranscription(r)
	return t != nil && t.Done()
}

// summarizeRecording writes r's summary; called: the model was asked (not when r changed meanwhile so that it needs
// none, or could not be read).
func (e *Engine) summarizeRecording(ctx context.Context, r *Recording) (called bool, err error) {
	files, meeting, hub, err := e.Store.recordSnapshot(r.ID)
	if err != nil {
		return false, err
	}
	var snapshot Recording
	if files["meta.json"] == nil {
		return false, nil
	}
	if err := json.Unmarshal(files["meta.json"], &snapshot); err != nil {
		return false, err
	}
	snapshot.Conv = meeting
	r = &snapshot
	if !summaryDue(r) {
		return false, nil // a day recording's summary is its recap's (day_recap.go): it would read the whole day
	}
	var device []RecordingSegment
	if err := json.Unmarshal(files["segments.json"], &device); err != nil {
		return false, err
	}
	segs := TranscriptOfRecord(hubMode(meeting), device, hub.Segments) // in hub mode the hub's sentences too
	called = true
	zone := e.Store.Zone()
	in := prompts.KBRecordingInput{Pinned: r.Pinned, Title: r.Title, Device: r.Device, Language: r.Language,
		Start: r.StartedAt.In(zone), Minutes: int(r.Duration / 60), Projects: e.knownProjects(), Tags: e.TopTags(40),
		Transcript: clipMiddle(e.Store.renderTranscript(r, MeetingLines(segs)), recordingPromptChars)}
	var out recordingSummary
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	err = e.modelFor("kb.compress").JSONMax(cctx, prompts.RenderKBRecording(e.writingOwner(e.outputLang(r))), prompts.RenderKBRecordingInput(in), 4000, &out)
	cancel()
	if err != nil {
		return true, err
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	cur, ok := st.Recording(r.ID)
	if !ok || cur.Trimmed() {
		return true, nil // deleted or trimmed meanwhile: no summary of a transcript that is gone
	}
	now := time.Now().UTC()
	if s := strings.TrimSpace(out.Summary); s != "" && !taggingFieldLocked(&cur.Tagging, "summary") {
		if err := st.putSummary(KindRecording, cur.ID, s+"\n", false); err != nil {
			return true, err
		}
	}
	applyTags(&cur.Tagging, out.Importance, int(out.Relevance), out.Tags, now)
	cur.OneLine = oneLine(out.OneLine)
	if t := titleLine(out.Title); t != "" && cur.AITitleFrom != AITitleMinutes { // the minutes' title settles it
		cur.AITitle, cur.AITitleFrom = t, AITitleSummary
	}
	cur.Name = st.recordingName(cur)
	// It summarized r's revision, and r's final transcript when it was in: a newer upload meanwhile, or the final pass
	// ending meanwhile, has it summarized again.
	cur.SummarizedRev, cur.SummarizedFinal = r.Revision, hubFinalIn(r)
	for _, p := range out.Projects {
		pr := e.FindProject(p.Name)
		if pr == nil || !pr.Live() {
			continue
		}
		cur.Projects = addUnique(cur.Projects, pr.Slug)
		pr.Items = addUnique(pr.Items, KindRecording+"/"+cur.ID)
		if u := oneLine(p.Update); u != "" {
			if err := st.AppendLog(pr.Slug, now, "录音 · "+cur.Name, u); err != nil {
				e.logf("project %s: %v", pr.Slug, err)
			} else {
				pr.Dirty = true
			}
		}
		_ = st.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, false)
	}
	if err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false); err != nil {
		return true, err
	}
	e.index(KindRecording, cur.ID, cur.Name, cur.Tagging, cur.OneLine, cur.StartedAt)
	return true, nil
}

// clipMiddle keeps the first and last of s within n runes, marking the cut.
func clipMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := n * 2 / 3
	return string(r[:head]) + "\n…（中间省略）…\n" + string(r[len(r)-(n-head):])
}
