package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
)

// All-day recordings and their daily recap (#4741; kb/recording_day.go, kb/day_recap.go): the phone's appends
// (POST …/segments), the refusals of a PUT that only a day recording has, the recap through the minutes' routes
// (kb_minutes.go), the day's one push, and the names and the recap time the knowledge base asks the manager for. The
// evening routine's facts about the day are briefing_day.go's.

// Codes of the day recordings' refusals (the contract's).
const (
	codeNotDay      = "not_day"
	codeBaseChanged = "base_changed"
	codeSegmentsGap = "segments_gap"
	codeDayFull     = "day_full"
	codeKindChanged = "kind_changed"
)

// maxSegmentsBody is the most an append's body may be (the phone halves its batch on 413 too_large).
const maxSegmentsBody = 1 << 20

func (s *Service) dayRoutes() {
	// Appends to a day recording by position (kb.AppendRecording). Full credentials only, like …/live, and the
	// refusals in the contract's order: 503, 413 too_large, 410, 404, 403 kb_read_only, 400, then the append's own.
	s.handle("POST /api/v1/recordings/{id}/segments", func(w http.ResponseWriter, r *http.Request) {
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r) // a read-only one too: 410 and 404 come before kb_read_only
		if e == nil {
			return
		}
		raw, ok := meetingBody(w, r, maxSegmentsBody, "1 MiB")
		if !ok {
			return
		}
		id := r.PathValue("id")
		if _, _, ok := meetingRecording(w, e, id); !ok {
			return
		}
		if !s.meetingWritable(r.Context(), e) {
			apierr.WriteCode(w, http.StatusForbidden, assistantwire.CodeKBReadOnly, i18n.Err("assistant-kb_routes.read_only"))
			return
		}
		a, err := parseAppend(raw)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		out, err := e.AppendRecording(id, a)
		var changed kb.BaseChangedError
		var gap kb.SegmentsGapError
		var full kb.DayFullError
		switch {
		case errors.Is(err, kb.ErrBadRecording):
			apierr.Write(w, http.StatusBadRequest, err)
			return
		case errors.Is(err, kb.ErrNotDay):
			apierr.WriteCode(w, http.StatusConflict, codeNotDay, i18n.Err("assistant-day-recap.not_day"))
			return
		case errors.As(err, &changed):
			body := apierr.LocalizedBody(http.StatusConflict, codeBaseChanged, i18n.Err("assistant-day-recap.base_changed"), i18n.OfWriter(w))
			body["base"], body["next"] = changed.Base, changed.Next
			apierr.WriteBody(w, http.StatusConflict, body)
			return
		case errors.As(err, &gap):
			body := apierr.LocalizedBody(http.StatusConflict, codeSegmentsGap, i18n.Err("assistant-day-recap.segments_gap", i18n.Args{"next": gap.Next}), i18n.OfWriter(w))
			body["next"] = gap.Next
			apierr.WriteBody(w, http.StatusConflict, body)
			return
		case errors.As(err, &full):
			writeDayFull(w, full.Next)
			return
		case err != nil:
			writeMeetingError(w, err) // 410, 404 and 403 when it changed since the check above
			return
		}
		if out.Wake { // the recap lane has work it did not have (an hour finished, a late sentence, complete)
			s.kbWake()
		}
		apierr.WriteJSON(w, map[string]any{"id": out.ID, "revision": out.Revision, "next": out.Next, "status": out.Status})
	})
}

// writeDayFull answers 413 day_full with the hub's count: the phone goes on in the next part.
func writeDayFull(w http.ResponseWriter, next int) {
	body := apierr.LocalizedBody(http.StatusRequestEntityTooLarge, codeDayFull, i18n.Err("assistant-day-recap.day_full"), i18n.OfWriter(w))
	body["next"] = next
	apierr.WriteBody(w, http.StatusRequestEntityTooLarge, body)
}

// parseAppend reads a POST …/segments body; what is wrong with its values (no base, a revision not above it, too many
// segments) is the knowledge base's to refuse. from may be written 12.0, as …/live takes it.
func parseAppend(raw []byte) (kb.RecordingAppend, error) {
	var in struct {
		kb.RecordingAppend
		From json.RawMessage `json:"from"` // over the embedded one's
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return kb.RecordingAppend{}, i18n.Err("assistant-meeting.bad_json", i18n.Args{"error": err.Error()})
	}
	a := in.RecordingAppend
	if in.From != nil {
		from, ok := wholeNumber(in.From)
		if !ok || from < 0 || from > 1<<31 {
			return a, i18n.Err("assistant-meeting.bad_from")
		}
		n := int(from)
		a.From = &n
	}
	return a, nil
}

// putDayFields are what the 200 of a day recording's PUT adds: its base and the hub's count, and the stored revision
// in place of the upload's (a stale one is no success for a day recording: the phone sends again above it).
func putDayFields(out map[string]any, put kb.RecordingPut) {
	out["revision"], out["base"], out["next"] = put.Revision, put.Base, put.Next
}

// dayUpload: upload u says it is a day recording (as kb's clean reads its kind).
func dayUpload(u kb.RecordingUpload) bool {
	return strings.EqualFold(strings.TrimSpace(u.Kind), kb.RecordingDay)
}

// dayRecording is recording rawID when it is a day recording.
func dayRecording(e *kb.Engine, rawID string) (*kb.Recording, bool) {
	id, ok := kb.RecordingID(rawID)
	if !ok {
		return nil, false
	}
	r, ok := e.Store.Recording(id)
	if !ok || r.Kind != kb.RecordingDay {
		return nil, false
	}
	return r, true
}

// dayRecapWritten pushes the day's recap (kb.Engine.DayRecapWritten, told once a day across the day's parts): a
// stable id per day, so a restart that tells it again shows the phone one, a tap opening the main conversation, and
// the same text with the recording's web page to the Lark bot and the channels with pushes on (at least once).
func (s *Service) dayRecapWritten(ctx context.Context, r kb.Recording, d kb.DayRecap) {
	l := s.lang(ctx)
	meetings := 0
	for _, en := range d.Timeline {
		if en.Kind == kb.RecapMeeting {
			meetings++
		}
	}
	day := cmp.Or(r.Day, d.Day)
	title := i18n.T(l, "assistant-day-recap.ready_title")
	body := i18n.T(l, "assistant-day-recap.ready_body", i18n.Args{"day": day, "meetings": meetings, "actions": len(d.Actions)})
	set := s.settings(ctx)
	n := Notification{ID: "ntf_dayrecap_" + day, Kind: NotifyPush, Title: title, Body: body, DeviceID: set.MachineID, SessionID: set.Main}
	if err := s.hub.NotifyRequest(ctx, NotifyRequest{Notification: n}); err != nil {
		s.logf("assistant: day recap push: %v", err)
	}
	if link := recordingLink(s.webBase(ctx), r.ID); link != "" {
		body += "\n" + link
	}
	s.channelsPushed("", "", title, body) // as minutesWritten: not as the main conversation's
}

// dayRecordingName is a day recording's name in the owner's language (kb.Engine.DayRecordingName), unless the user
// titled it.
func (s *Service) dayRecordingName(day string) string {
	return i18n.T(s.lang(s.runCtx()), "assistant-day-recap.name", i18n.Args{"date": day})
}

// eveningClock is the evening routine's time of day, whether or not it is on (kb.Engine.DayRecapClock: the recap is
// due 30 minutes before it); -1 while the schedule can't be read or has no evening routine, which the knowledge base
// takes as the default routine's 21:30.
func (s *Service) eveningClock() (int, int) {
	ctx, cancel := context.WithTimeout(s.runCtx(), 5*time.Second)
	defer cancel()
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return -1, -1
	}
	defer sc.mu.Unlock()
	for _, t := range sc.tasks {
		if t.ID != "evening" {
			continue
		}
		if m := hhmm.FindStringSubmatch(t.Time); m != nil {
			hh, _ := strconv.Atoi(m[1])
			mm, _ := strconv.Atoi(m[2])
			return hh, mm
		}
	}
	return -1, -1
}

// dayRecapTodos makes a to-do of each action of version of day recording rec's recap, as minutesTodos does for
// minutes: an id from the recording, the version and the action (todo_day_…), so a retried request makes none twice,
// and notes that name the day and the clock time it was said in the recording's own zone.
func (s *Service) dayRecapTodos(ctx context.Context, e *kb.Engine, rec *kb.Recording, version int, actions []string) ([]Todo, *kb.DayRecap, error) {
	s.minutesMu.Lock()
	defer s.minutesMu.Unlock()
	rc, err := e.ReadDayRecap(rec.ID)
	if err != nil {
		return nil, nil, err
	}
	if rc.Version != version {
		return nil, nil, kb.ErrMinutesStale
	}
	note := s.dayRecapTodoNotes(ctx, e, rec, rc)
	out, made, byTask, err := s.actionTodos(ctx, rc.Actions, actions, func(aid string) string { return dayRecapTodoID(rec.ID, version, aid) }, note)
	if err != nil {
		if made != nil { // those made so far are recorded all the same, as minutesTodos records them
			if _, serr := e.SetDayRecapTodos(rec.ID, version, made, byTask); serr != nil {
				s.logf("assistant: recap to-dos of %s: %v", rec.ID, serr)
			}
		}
		return out, nil, err
	}
	rc, err = e.SetDayRecapTodos(rec.ID, version, made, byTask)
	return out, rc, err
}

// dayRecapTodoID is the to-do an action of a version of a day recording's recap becomes.
func dayRecapTodoID(recording string, version int, action string) string {
	return actionTodoID("todo_day_", recording, version, action)
}

// dayRecapTodoNotes is what a to-do made of an action of recap rc of day recording rec says: the day, the clock time
// it was said in the recording's zone, the deadline as said, the recording's page.
func (s *Service) dayRecapTodoNotes(ctx context.Context, e *kb.Engine, rec *kb.Recording, rc *kb.DayRecap) func(kb.MinutesAction) string {
	loc, l, link := e.Store.RecordingZone(rec), s.lang(ctx), recordingLink(s.webBase(ctx), rec.ID)
	day := cmp.Or(rc.Day, rec.Day)
	return func(a kb.MinutesAction) string {
		clock := rec.StartedAt.Add(time.Duration(a.At * float64(time.Second))).In(loc).Format("15:04")
		notes := i18n.T(l, "assistant-day-recap.todo_notes", i18n.Args{"day": day, "clock": clock})
		if a.By != "" {
			notes += "\n" + i18n.T(l, "assistant-minutes.todo_by", i18n.Args{"by": a.By})
		}
		if link != "" {
			notes += "\n" + link
		}
		return notes
	}
}

// recapActionLines is what kb_get adds to recap rc of day recording id: each suggested to-do with what became of it,
// as minutesActionLines says a meeting's.
func (s *Service) recapActionLines(ctx context.Context, id string, rc *kb.DayRecap) string {
	return s.actionLines(ctx, "\nSuggested to-dos as they stand (one becomes a to-do only when the user confirms it):\n", rc.Actions,
		func(aid string) string { return dayRecapTodoID(id, rc.Version, aid) })
}
