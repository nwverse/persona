package assistant

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The owner's message preempts a background round (docs/assistant/assistant.md "Interjecting while it answers",
// #4926). A round the owner did not start (a supervision message, a scheduled task or follow-up, another
// conversation's message, a task's notice) can sit behind a long foreground command for minutes. When the owner's own
// message reaches a conversation on such a round, it carries interject_background whatever the setting says, so the
// node moves the running command to the background and the model reads the owner at once. For a round the owner
// started, the setting decides as before.
//
// A mark per conversation says which message started its last round: set when the manager sends a background
// message into a conversation not seen working (call), dropped when another message of the manager's starts a round
// there, and used up by the owner's next message (SendParams): absorbed, it makes the round the owner's. A message
// sent into a working conversation joins the round in flight and changes nothing. A round that background work wakes
// after a marked one keeps the mark: the owner did not start that either. The marks are in memory only: after a
// restart the setting decides until the next background round, which is what best effort already means here.

// backgroundRoundKeep is how long a background round's mark is kept when the owner never speaks after it.
const backgroundRoundKeep = 24 * time.Hour

// roundNotice is the mark's kind for a task's notice, which carries no message source of its own.
const roundNotice = "notice"

// backgroundRound is one conversation's mark: the kind of message that started its round, and when it was sent.
type backgroundRound struct {
	kind string
	at   time.Time
}

type preemptBook struct {
	mu     sync.Mutex
	rounds map[string]backgroundRound // by conversation id
}

// backgroundRoundKind is the kind of round text with source starts when it is one the owner did not start, else "".
// Persona continuing the owner's own work (a usage limit that reset, a wake for what it waits on) is not one, as it
// is no background round in the apps ("Background rounds").
func backgroundRoundKind(text string, source *proto.MessageSource) string {
	if source == nil {
		if in := prompts.ParseInjected(text); in != nil && in.Kind == proto.InjectedTaskNotice {
			return roundNotice
		}
		return ""
	}
	switch source.Kind {
	case proto.SourceSupervision, proto.SourceConversation, proto.SourcePatrol:
		return source.Kind
	case proto.SourceFollowup:
		if in := prompts.ParseInjected(text); in != nil && (in.State == "limit_continue" || in.State == "wake") {
			return ""
		}
		return source.Kind
	}
	return ""
}

// roundStart is an assistant.send about to start a round: of kind ("" when it is no background message) in
// conversation id on machine ("" for a new conversation, named by the node's answer).
type roundStart struct {
	machine, id, kind string
}

// roundStarting is the round the assistant.send p to machine starts, read before it is sent; nil when it joins the
// round its conversation is already on, whose starter stands.
func (s *Service) roundStarting(machine string, p map[string]any, kind string) *roundStart {
	id, _ := p["session_id"].(string)
	if id != "" && s.seenWorking(machine, id) {
		return nil
	}
	return &roundStart{machine: machine, id: id, kind: kind}
}

// sent: the send was accepted with res; the conversation's mark is the round's.
func (r *roundStart) sent(s *Service, res json.RawMessage) {
	if r == nil {
		return
	}
	id := r.id
	if id == "" {
		var out struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(res, &out) != nil || out.SessionID == "" {
			return
		}
		id = out.SessionID
	}
	b := &s.preempt
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for k, m := range b.rounds {
		if now.Sub(m.at) > backgroundRoundKeep {
			delete(b.rounds, k)
		}
	}
	if r.kind == "" {
		delete(b.rounds, id)
		return
	}
	if b.rounds == nil {
		b.rounds = map[string]backgroundRound{}
	}
	b.rounds[id] = backgroundRound{kind: r.kind, at: now}
}

// ownerPreempts: the owner's message is about to go to conversation id; whether the last round there is one the
// owner did not start, so the message carries interject_background. The mark is used up either way: the round is the
// owner's from here on, or the message starts one of their own. Whether the round still runs is the node's to say
// (assistantProc.interjects: an idle conversation is unaffected), not the manager's list, which can be seconds old.
func (s *Service) ownerPreempts(id string) bool {
	b := &s.preempt
	b.mu.Lock()
	m, ok := b.rounds[id]
	delete(b.rounds, id)
	b.mu.Unlock()
	if !ok || time.Since(m.at) > backgroundRoundKeep {
		return false
	}
	s.logf("assistant: the owner's message reaches %s after a %s round started there: it moves foreground work to the background if that round still runs", short(id), m.kind)
	return true
}

// seenWorking: machine's last session list showed conversation id working.
func (s *Service) seenWorking(machine, id string) bool {
	if s.devs != nil {
		machine = s.devs.Canonical(machine)
	}
	return s.fleetPrevious(machine)[id].Status == proto.StatusWorking
}
