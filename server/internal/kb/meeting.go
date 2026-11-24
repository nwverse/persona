package kb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The meeting conversation (docs/assistant/meeting-minutes.md "Meeting conversation (#3862)"): a meeting recording
// whose upload carries room is a room meeting, attended by a conversation of its own (the room) in the role room says.
// What the manager keeps of it (the room's role, state and ids, the private mark, the follow-up and the creations
// asked of a node that are not settled yet) is meeting.json beside meta.json, never meta.json: an older manager after a
// rollback rewrites meta.json without the fields it does not know and, when it starts, resumes feeding every recording
// whose meta.json has an attendance, so a room meeting's meta.json never carries one (fail closed). The trim keeps
// meeting.json (an older manager's trim removes it, which leaves the meeting unmarked: private); search skips it.

const meetingFile = "meeting.json"

// Re-exported from recordingwire.
const (
	RoomNone        = recordingwire.RoomNone
	RoomStarting    = recordingwire.RoomStarting
	RoomReady       = recordingwire.RoomReady
	RoomUnavailable = recordingwire.RoomUnavailable
	RoomEnded       = recordingwire.RoomEnded
	RoomDeleted     = recordingwire.RoomDeleted
)

// Re-exported from recordingwire.
const (
	RoomRuntimeUnsupported = recordingwire.RoomRuntimeUnsupported
	RoomNodeTooOld         = recordingwire.RoomNodeTooOld
	RoomMachineOffline     = recordingwire.RoomMachineOffline
	RoomStartFailed        = recordingwire.RoomStartFailed
)

// Re-exported from recordingwire.
const (
	PartRoom       = recordingwire.PartRoom
	PartAsks       = recordingwire.PartAsks
	PartPrivate    = recordingwire.PartPrivate
	PartFollowup   = recordingwire.PartFollowup
	PartPhotos     = recordingwire.PartPhotos
	PartLines      = recordingwire.PartLines
	PartTopics     = recordingwire.PartTopics
	PartHighlights = recordingwire.PartHighlights
)

// Meeting is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Meeting = recordingwire.Meeting

// MeetingHandoff is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingHandoff = recordingwire.MeetingHandoff

// Re-exported from recordingwire.
const (
	HandoffSent         = recordingwire.HandoffSent
	HandoffFailed       = recordingwire.HandoffFailed
	MeetingHandoffsKept = recordingwire.MeetingHandoffsKept
)

// MeetingRoom is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingRoom = recordingwire.MeetingRoom

// Re-exported from recordingwire.
const (
	RoomBatchingLean = recordingwire.RoomBatchingLean
)

// MeetingRoomLean is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingRoomLean = recordingwire.MeetingRoomLean

// MeetingFollowup is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingFollowup = recordingwire.MeetingFollowup

// Re-exported from recordingwire.
var (
	ErrNotMeeting = recordingwire.ErrNotMeeting
)

// readMeeting is recording id's meeting.json, read from disk (an upload's undo journal never holds it); false when
// there is none.
func (s *Store) readMeeting(id string) (*Meeting, error) {
	raw, err := os.ReadFile(s.path(KindRecording, id, meetingFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Meeting
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("meeting of %s: %w", id, err)
	}
	return &m, nil
}

// putMeeting writes recording id's meeting.json, not when the recording was deleted meanwhile (false then); a trimmed
// recording keeps it (its private mark and its room's end). The caller holds meetMu.
func (s *Store) putMeeting(id string, m *Meeting) (bool, error) {
	if s.readOnly {
		return false, ErrReadOnly
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if _, gone := s.tombstones()[id]; gone {
		return false, nil
	}
	if _, err := os.Stat(s.path(KindRecording, id)); err != nil {
		return false, nil
	}
	return true, writeRecordingFile(s.path(KindRecording, id, meetingFile), append(raw, '\n'))
}

// meetingParts are the parts of m that differ from before, in a fixed order.
func meetingParts(before, after Meeting) []string {
	var out []string
	same := func(a, b any) bool {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return bytes.Equal(x, y)
	}
	if !same(roomShown(before.Room), roomShown(after.Room)) {
		out = append(out, PartRoom)
	}
	if !same(before.Private, after.Private) {
		out = append(out, PartPrivate)
	}
	if !same(before.Followup, after.Followup) {
		out = append(out, PartFollowup)
	}
	if !same(transcriptionShown(before.Transcription), transcriptionShown(after.Transcription)) {
		out = append(out, PartTranscription)
	}
	return out
}

// roomShown is room without what only the manager reads (its batching and a lean room's bookkeeping, #8253), whose
// changes raise no revision: the apps see nothing new of it.
func roomShown(room *MeetingRoom) *MeetingRoom {
	if room == nil {
		return nil
	}
	r := *room
	r.Batching, r.Lean = 0, MeetingRoomLean{}
	return &r
}

// meetingSame: a and b are the same meeting.json (also where no part the apps read differs: the timeline, the final
// pass's bookkeeping, a lean room's bookkeeping).
func meetingSame(a, b Meeting) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// bumpRevs raises the revisions of parts in m.
func bumpRevs(m *Meeting, parts []string) {
	if len(parts) == 0 {
		return
	}
	if m.Revs == nil {
		m.Revs = map[string]int64{}
	}
	for _, p := range parts {
		m.Revs[p]++
	}
}

// meetingChanged tells the engine's MeetingChanged of recording id's changed parts.
func (e *Engine) meetingChanged(id string, parts []string) {
	if len(parts) > 0 && e.MeetingChanged != nil {
		e.MeetingChanged(id, parts)
	}
}

// MeetingRead is recording id's meeting.json (empty without one) and its stored upload, also on a read-only knowledge
// base: ErrRecordingDeleted after a delete, ErrNotFound with no upload of it.
func (e *Engine) MeetingRead(rawID string) (Meeting, Recording, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return Meeting{}, Recording{}, ErrNotFound
	}
	if _, gone := e.Store.tombstones()[id]; gone {
		return Meeting{}, Recording{}, ErrRecordingDeleted
	}
	r, ok := e.Store.Recording(id)
	if !ok {
		return Meeting{}, Recording{}, ErrNotFound
	}
	var m Meeting
	if r.Conv != nil {
		m = r.Conv.Clone()
	}
	return m, *r, nil
}

// MeetingUpdate changes recording id's meeting.json with fn (given it and the stored upload) and writes the result
// unless fn fails or changed nothing; the revisions of the parts that changed go up and MeetingChanged hears of them.
// ErrRecordingDeleted after a delete, ErrNotFound with no upload (or deleted meanwhile), ErrReadOnly on a read-only
// knowledge base. fn runs with meeting.json locked: it must be quick and call no Engine method of the recordings.
func (e *Engine) MeetingUpdate(rawID string, fn func(m *Meeting, r Recording) error) (Meeting, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return Meeting{}, ErrNotFound
	}
	st := e.Store
	st.meetMu.Lock()
	m, parts, err := e.meetingUpdateLocked(id, fn)
	st.meetMu.Unlock()
	if err == nil {
		e.meetingChanged(id, parts)
	}
	return m, err
}

func (e *Engine) meetingUpdateLocked(id string, fn func(m *Meeting, r Recording) error) (Meeting, []string, error) {
	st := e.Store
	if _, gone := st.tombstones()[id]; gone {
		return Meeting{}, nil, ErrRecordingDeleted
	}
	r, ok := st.Recording(id)
	if !ok {
		return Meeting{}, nil, ErrNotFound
	}
	if st.ReadOnly() {
		return Meeting{}, nil, ErrReadOnly
	}
	var before Meeting
	if r.Conv != nil {
		before = r.Conv.Clone()
	}
	m := before.Clone()
	if err := fn(&m, *r); err != nil {
		return Meeting{}, nil, err
	}
	if meetingSame(before, m) {
		return m, nil, nil
	}
	parts := meetingParts(before, m)
	bumpRevs(&m, parts)
	if ok, err := st.putMeeting(id, &m); err != nil {
		return Meeting{}, nil, err
	} else if !ok {
		return Meeting{}, nil, ErrNotFound
	}
	return m, parts, nil
}

// SetPrivate sets recording id's private mark (PUT /api/v1/recordings/{id}/private): any time, a trimmed recording's
// too. Errors in the contract's order: ErrRecordingDeleted, ErrNotFound, ErrReadOnly, ErrNotMeeting.
func (e *Engine) SetPrivate(rawID string, private bool) (Meeting, error) {
	return e.MeetingUpdate(rawID, func(m *Meeting, r Recording) error {
		if !r.IsMeeting() {
			return ErrNotMeeting
		}
		m.Private = &private
		return nil
	})
}

// uploadRoomRole is the role an upload's room asks for, normalized as #2981's attendance is (normalizeAttendance), with
// notes besides (#8851): a value that is not one of the four is off with AttendanceUnknownRole, a role on a recording
// that is not a meeting off with AttendanceNotMeeting. The upload is cleaned (its Kind set).
func uploadRoomRole(u *RecordingUpload) (role, problem string) {
	role = strings.ToLower(strings.TrimSpace(u.Room.Attendance))
	switch {
	case u.Room.Bad:
		return "", AttendanceUnknownRole
	case role == "" || role == AttendanceOff:
		return "", ""
	case role != AttendanceNotes && role != AttendanceListener && role != AttendanceParticipant:
		return "", AttendanceUnknownRole
	case u.Kind != RecordingMeeting:
		return "", AttendanceNotMeeting
	}
	return role, ""
}

// putMeetingFields stores what upload u says of recording id's meeting: its room (an upload with room makes it a room
// meeting for good; one without turns the role of a room meeting off and changes nothing else), its first private
// mark (only a meeting's, only while it has none) and its transcription (putTranscriptionFields: the first mode named,
// then in hub mode the timeline and the end). The caller holds recMu; meeting.json is written before meta.json, so a
// crash between leaves a role no older manager reads. The upload's top-level attendance fields are cleared for a room
// meeting (they are ignored beside room, and never written to its meta.json). The meeting after it, and the parts
// that changed.
func (e *Engine) putMeetingFields(id string, u *RecordingUpload, now time.Time) (*Meeting, []string, error) {
	st := e.Store
	st.meetMu.Lock()
	defer st.meetMu.Unlock()
	// From the file, under meetMu: r was read before it, and a MeetingUpdate since (the private mark, the room, the
	// follow-up) would be written back over.
	var before Meeting
	if m, err := st.readMeeting(id); err != nil {
		return nil, nil, err
	} else if m != nil {
		before = m.Clone()
	}
	m := before.Clone()
	switch {
	case u.Room != nil:
		role, problem := uploadRoomRole(u)
		u.RoomProblem = problem
		if m.Room == nil {
			m.Room = &MeetingRoom{State: RoomNone}
		}
		m.Room.Attendance = role
	case m.Room != nil:
		m.Room.Attendance = "" // once a room meeting, always one: without room it is off
	}
	if m.Room != nil {
		u.Attendance, u.AttendingSession = "", nil
	}
	if u.Private != nil && m.Private == nil && u.Kind == RecordingMeeting {
		p := *u.Private
		m.Private = &p
	}
	e.putTranscriptionFields(id, &m, u, now)
	if meetingSame(before, m) {
		if m.Room == nil && m.Private == nil && m.Followup == nil && m.Transcription == nil {
			return nil, nil, nil
		}
		return &m, nil, nil
	}
	parts := meetingParts(before, m)
	bumpRevs(&m, parts)
	raw, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(st.path(KindRecording, id), 0o700); err != nil {
		return nil, nil, err
	}
	st.recFilesMu.Lock()
	err = writeRecordingFile(st.path(KindRecording, id, meetingFile), append(raw, '\n'))
	st.recFilesMu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return &m, parts, nil
}

// RoomMeetings are the recordings that are room meetings, oldest first: what the room keeper looks at after a
// restart.
func (e *Engine) RoomMeetings() []Recording {
	var out []Recording
	for _, r := range e.Store.Recordings() {
		if r.RoomMeeting() {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b Recording) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// RoomView is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RoomView = recordingwire.RoomView

// ConvView is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type ConvView = recordingwire.ConvView

// metaWithMeeting is a recording's meta.json as detail.meta shows it: with its meeting.json's private mark, room,
// follow-up and transcription (its mode, and in hub mode its state and gaps); a room meeting's attendance is its
// room's role (absent when off), never an attending_session.
func metaWithMeeting(raw []byte, m *Meeting) []byte {
	if m == nil {
		return raw
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil || meta == nil {
		return raw
	}
	set := func(k string, v any) {
		if b, err := json.Marshal(v); err == nil {
			meta[k] = b
		}
	}
	if m.Private != nil {
		set("private", *m.Private)
	}
	if v := m.RoomView(); v != nil {
		set("room", v)
		delete(meta, "attending_session")
		delete(meta, "attendance")
		if m.Room.Attendance != "" {
			if trimmed := meta["trimmed_at"]; jsonNull(trimmed) {
				set("attendance", m.Room.Attendance)
			}
		}
	}
	if v := m.FollowupView(); v != nil {
		set("followup", v)
	}
	if v := m.TranscriptionView(nil); v != nil {
		set("transcription", v)
	}
	if len(m.Handoffs) > 0 {
		set("handoffs", m.Handoffs)
	}
	out, err := json.Marshal(meta)
	if err != nil {
		return raw
	}
	return out
}

// HasMinutes: recording id has minutes that can be read.
func (s *Store) HasMinutes(id string) bool { return s.hasMinutes(id) }
