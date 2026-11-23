package recording

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// What the core asks of the module (internal/recordinghost.Layer).

// Routes registers the module's HTTP routes; engine is the knowledge base for a route.
func (s *Recordings) Routes(engine func(http.ResponseWriter, *http.Request) *kb.Engine) {
	s.recordingRoutes(engine)
}

// AudioSocket serves GET /api/v1/recordings/{id}/audio.
func (s *Recordings) AudioSocket(w http.ResponseWriter, r *http.Request) { s.audioSocket(w, r) }

// Hook gives knowledge base e the module's callbacks: the minutes' push, the live log's and meeting.json's changes,
// the day recordings' names and the day's push.
func (s *Recordings) Hook(e *kb.Engine) {
	e.MinutesWritten, e.LiveChanged, e.MeetingChanged, e.DayRecordingName = s.minutesWritten, s.meetingKick, s.meetingLiveChanged, s.dayRecordingName
	e.DayRecapWritten = func(ctx context.Context, r kb.Recording, d kb.DayRecap) ([]string, error) {
		return s.dayRecapWritten(ctx, e, r, d)
	}
}

func (s *Recordings) AttendResume(e *kb.Engine)                    { s.attendResume(e) }
func (s *Recordings) FinalPasses(ctx context.Context)              { s.finalPasses(ctx) }
func (s *Recordings) RoomsSweep(ctx context.Context, e *kb.Engine) { s.meetingRoomsSweep(ctx, e) }
func (s *Recordings) HubSweep(e *kb.Engine)                        { s.hubSweep(e) }

// TranscriptionChanged: the meeting transcription the owner chose, or whether it is ready, changed: the live jobs of
// a choice no longer standing are ended (transcriptionJobsCheck), never under the caller's locks.
func (s *Recordings) TranscriptionChanged() {
	s.hubTr.wg.Add(1)
	go func() {
		defer s.hubTr.wg.Done()
		s.transcriptionJobsCheck(s.runCtx())
	}()
}

func (s *Recordings) TranscriptionJobsCheck(ctx context.Context) { s.transcriptionJobsCheck(ctx) }

func (s *Recordings) JustMeNow(machine, session string) bool { return s.justMeNow(machine, session) }
func (s *Recordings) JustMeSent(machine, session string)     { s.justMeSent(machine, session) }
func (s *Recordings) JustMeStarted(machine, session string, newTurn, woken bool) {
	s.justMeStarted(machine, session, newTurn, woken)
}
func (s *Recordings) JustMeEnded(machine, session string, held bool) {
	s.justMeEnded(machine, session, held)
}
func (s *Recordings) MeetingTurnNow(machine, session string) bool {
	return s.meetingTurnNow(machine, session)
}
func (s *Recordings) MeetingTurnStarted(machine, session string, newTurn bool) bool {
	return s.meetingTurnStarted(machine, session, newTurn)
}
func (s *Recordings) MeetingTurnEnded(machine, session string) bool {
	return s.meetingTurnEnded(machine, session)
}
func (s *Recordings) MeetingTurnStopped(machine, session string, row proto.Session, restarted bool) bool {
	return s.meetingTurnStopped(machine, session, row, restarted)
}
func (s *Recordings) MeetingTurnPrompt(machine, session string, p map[string]any) {
	s.meetingTurnPrompt(machine, session, p)
}
func (s *Recordings) MeetingTurnRunning(ctx context.Context, machine, session string) (bool, error) {
	return s.meetingTurnRunning(ctx, machine, session)
}
func (s *Recordings) MeetingTurnAgentRunning(ctx context.Context, machine string) (bool, error) {
	return s.meetingTurnAgentRunning(ctx, machine)
}
func (s *Recordings) MeetingTurnUserSpoke(machine, session string) {
	s.meetingTurnUserSpoke(machine, session)
}
func (s *Recordings) MeetingRefuses(ctx context.Context, machine, session, name string, assistant bool) (proto.ToolResult, bool) {
	return s.meetingRefuses(ctx, machine, session, name, assistant)
}
func (s *Recordings) RoomRefusesSend(ctx context.Context, machine, session, lang string) error {
	return s.roomRefusesSend(ctx, machine, session, lang)
}
func (s *Recordings) RoomToolCall(ctx context.Context, recording, name string, args json.RawMessage) proto.ToolResult {
	return s.roomToolCall(ctx, recording, name, args)
}
func (s *Recordings) RoomTools() []proto.Tool { return meetingRoomTools() }
func (s *Recordings) RoomToolNames() []string { return meetingRoomToolNames() }
