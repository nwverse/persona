package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Meeting turns (docs/assistant/meeting-minutes.md "Feeding the attending session"): the turn a meeting batch starts
// in the attending session is a side exchange until it ends or the user sends a message of their own. Nothing that
// looks at turns takes it for one of the user's: no finished push and no change to whether the other turns are pushed
// (fleet_sessions.go keeps it out of the turn tracker), no Agents triage or work state, never the progress, report or
// completion of the session's task, no project-timeline line, nothing mirrored to the Lark bot or the channels. While
// it runs the hub serves the session only reading tools (meetingRefuses).
//
// A mark per session says so, kept in the assistant's store (assistant.meeting_turns) so it survives a restart:
// set when the batch is sent, started when the session is next seen working, ended when it stops again, and dropped
// when the user speaks during the turn or the next turn starts. The post-turn hooks run after the end, some of them a
// minute later, and still find the ended mark. A mark whose turn was never seen starting is the batch's for
// meetingTurnStartWithin; a turn that starts on a mark the hub could not follow (it restarted meanwhile) is taken for
// the meeting turn, as the contract asks of a turn the hub cannot tell.

const meetingTurnsKey = "assistant.meeting_turns"

const (
	// meetingTurnStartWithin is how long after its batch was sent a turn that starts is still the batch's.
	meetingTurnStartWithin = 10 * time.Minute
	// meetingTurnKeep is how long an ended mark stays when no turn follows it: the post-turn hooks ran long before.
	meetingTurnKeep = 24 * time.Hour
)

// meetingTurnTools are the hub's tools a meeting turn may call: they only read.
var meetingTurnTools = []string{"kb_search", "kb_list", "kb_get", "todo_list", "todo_get", "agents_list", "agent_read",
	"conversations_list", "conversation_read", "machines_list", "list_followups"}

// meetingTurn is one session's mark.
type meetingTurn struct {
	MachineID string    `json:"machine_id"`
	SessionID string    `json:"session_id"`
	Recording string    `json:"recording"`
	SentAt    time.Time `json:"sent_at"`
	Started   bool      `json:"started,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
}

type meetingTurnBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]*meetingTurn // by meetingTurnKey
	now    func() time.Time        // nil: time.Now (tests set it)
}

var errMeetingTurnsUnreadable = errors.New("meeting turns unreadable")

// meetingTurnKey is machine's session's key in the book: the machine as the devices name it.
func (s *Service) meetingTurnKey(machine, session string) string {
	if s.devs != nil {
		machine = s.devs.Canonical(machine)
	}
	return machine + "/" + session
}

func (b *meetingTurnBook) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// meetingTurnsLocked reads the book once and drops what has run out; b.mu is held.
func (s *Service) meetingTurnsLocked(ctx context.Context) error {
	b := &s.attend.turns
	if !b.loaded {
		m := map[string]*meetingTurn{}
		if st := s.store(); st != nil {
			if _, err := st.Setting(ctx, meetingTurnsKey, &m); err != nil {
				return fmt.Errorf("%w: %v", errMeetingTurnsUnreadable, err)
			}
		}
		if m == nil {
			m = map[string]*meetingTurn{}
		}
		b.m, b.loaded = m, true
	}
	now := b.clock()
	for k, t := range b.m {
		if !t.Started && now.Sub(t.SentAt) > meetingTurnStartWithin || !t.EndedAt.IsZero() && now.Sub(t.EndedAt) > meetingTurnKeep {
			delete(b.m, k)
		}
	}
	return nil
}

// saveMeetingTurnsLocked writes the book; b.mu is held. A failed write keeps the marks in memory.
func (s *Service) saveMeetingTurnsLocked() {
	st := s.store()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := st.PutSetting(ctx, meetingTurnsKey, s.attend.turns.m); err != nil {
		s.logf("save meeting turns: %v", err)
	}
}

// meetingTurnSent marks the turn a batch of recording is about to start in machine's session (before the send: a
// fast tool call can come before it returns); the time it is marked with. An error while the book cannot be read: the
// batch waits, since its turn could not be told from the user's.
func (s *Service) meetingTurnSent(machine, session, recording string) (time.Time, error) {
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.meetingTurnsLocked(context.Background()); err != nil {
		return time.Time{}, err
	}
	now := b.clock()
	b.m[s.meetingTurnKey(machine, session)] = &meetingTurn{MachineID: machine, SessionID: session, Recording: recording, SentAt: now}
	s.saveMeetingTurnsLocked()
	return now, nil
}

// meetingTurnUnsent takes back the mark sent at at: the send was refused, no turn comes of it.
func (s *Service) meetingTurnUnsent(machine, session string, at time.Time) {
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	k := s.meetingTurnKey(machine, session)
	if t := b.m[k]; t != nil && !t.Started && t.SentAt.Equal(at) {
		delete(b.m, k)
		s.saveMeetingTurnsLocked()
	}
}

// meetingTurnStarted: machine's session is seen working, a new turn when newTurn (it was seen stopped before);
// whether this turn is a meeting turn. Called once per start, where starts are observed (setSessions, TurnStarted).
func (s *Service) meetingTurnStarted(machine, session string, newTurn bool) bool {
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.meetingTurnsLocked(context.Background()) != nil {
		return false
	}
	k := s.meetingTurnKey(machine, session)
	t := b.m[k]
	switch {
	case t == nil:
		return false
	case !t.Started:
		t.Started = true
	case t.EndedAt.IsZero() && !newTurn:
		return true // the same turn, seen again (a reconnect)
	default: // a turn after the meeting turn: the mark has done its work
		delete(b.m, k)
		s.saveMeetingTurnsLocked()
		return false
	}
	s.saveMeetingTurnsLocked()
	return true
}

// meetingTurnEnded: machine's session stopped; whether the turn that ended is a meeting turn. A mark whose turn was
// not seen starting is left as it is: the end is an earlier turn's. Called again for the same end, it says the same.
func (s *Service) meetingTurnEnded(machine, session string) bool {
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.meetingTurnsLocked(context.Background()) != nil {
		return false
	}
	t := b.m[s.meetingTurnKey(machine, session)]
	if t == nil || !t.Started {
		return false
	}
	if t.EndedAt.IsZero() {
		t.EndedAt = b.clock()
		s.saveMeetingTurnsLocked()
	}
	return true
}

// meetingTurnUserSpoke: the user sent machine's session a message of their own: a meeting turn under way is theirs
// from here on. An ended one stays for the hooks of its end, until the user's turn starts.
func (s *Service) meetingTurnUserSpoke(machine, session string) {
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.meetingTurnsLocked(context.Background()) != nil {
		return
	}
	k := s.meetingTurnKey(machine, session)
	if t := b.m[k]; t != nil && t.EndedAt.IsZero() {
		delete(b.m, k)
		s.saveMeetingTurnsLocked()
	}
}

// meetingTurnNow: the turn of machine's session that runs or just ended is a meeting turn, for what looks at a turn
// (pushes, triage, tasks, mirrors). A batch sent whose turn has not been seen starting is not: the hooks of the turn
// before it may still be running.
func (s *Service) meetingTurnNow(machine, session string) bool {
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.meetingTurnsLocked(context.Background()) != nil {
		return false
	}
	t := b.m[s.meetingTurnKey(machine, session)]
	return t != nil && t.Started
}

// meetingTurnRunning: a meeting turn may be running in machine's session now (sent and not seen ending); an error
// when the book cannot be read.
func (s *Service) meetingTurnRunning(ctx context.Context, machine, session string) (bool, error) {
	if session == "" {
		return false, nil
	}
	b := &s.attend.turns
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.meetingTurnsLocked(ctx); err != nil {
		return false, err
	}
	t := b.m[s.meetingTurnKey(machine, session)]
	return t != nil && t.EndedAt.IsZero(), nil
}

// meetingRefuses refuses a hub tool other than the reading ones during a meeting turn of the calling session (and any
// call while the hub cannot tell, as guestRefuses does): other people's words started the turn, and only the user's
// own message may have Persona act.
func (s *Service) meetingRefuses(ctx context.Context, machine, session, name string) (proto.ToolResult, bool) {
	running, err := s.meetingTurnRunning(ctx, machine, session)
	switch {
	case err != nil:
		s.logf("mcp call %s: %v", name, err)
		return proto.TextResult("tool unavailable for the moment: "+name, true), true
	case !running || slices.Contains(meetingTurnTools, compat.Tool(name)):
		return proto.ToolResult{}, false
	}
	return proto.TextResult(i18n.T(s.lang(ctx), "assistant-meeting_feed.tool_needs_user", i18n.Args{"tool": name}), true), true
}

// meetingSource: a send's params carry a meeting's batch (message source meeting).
func meetingSource(p map[string]any) bool {
	src, _ := p["message_source"].(*proto.MessageSource)
	return src != nil && src.Kind == proto.SourceMeeting
}
