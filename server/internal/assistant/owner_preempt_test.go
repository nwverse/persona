package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ownerSend is what the hub does with the owner's message to conversation id: UserSpoke, then SendParams.
func ownerSend(t *testing.T, s *Service, id, text string) map[string]any {
	t.Helper()
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: id})
	params, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: id, Params: map[string]any{"text": text}})
	if err != nil {
		t.Fatal(err)
	}
	return params
}

// seeStatus is the node's list showing conversation id in status.
func seeStatus(s *Service, f *fakeHub, id, status string) {
	row := proto.Session{SessionID: id, Assistant: true, Status: status}
	f.setSessions("server", row)
	s.fleetKeepPrevious("server", []proto.Session{row})
}

func preemptService(t *testing.T) (*Service, *fakeHub, string) {
	t.Helper()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	sess, err := s.start(context.Background(), StartParams{Main: true, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	seeStatus(s, f, sess.SessionID, proto.StatusIdle)
	return s, f, sess.SessionID
}

// Which messages start a round the owner did not start (#4926).
func TestBackgroundRoundKind(t *testing.T) {
	notice := prompts.RenderTaskNotice(prompts.TaskNotice{Title: "Build", Machine: "server", State: "done", Text: "Built."})
	wake := prompts.RenderAgentWake(prompts.AgentWake{Attempt: 1, Wait: "the build"})
	limit := prompts.RenderAgentLimitContinue()
	for _, tt := range []struct {
		name, text string
		source     *proto.MessageSource
		want       string
	}{
		{"the owner's words", "what is the weather", nil, ""},
		{"supervision", "check", &proto.MessageSource{Kind: proto.SourceSupervision}, proto.SourceSupervision},
		{"another conversation", "hello", &proto.MessageSource{Kind: proto.SourceConversation, ConversationID: "c2"}, proto.SourceConversation},
		{"scheduled task", "run", &proto.MessageSource{Kind: proto.SourcePatrol}, proto.SourcePatrol},
		{"follow-up", "look again", &proto.MessageSource{Kind: proto.SourceFollowup}, proto.SourceFollowup},
		{"task notice", notice, nil, roundNotice},
		{"meeting batch", "words", &proto.MessageSource{Kind: proto.SourceMeeting}, ""},
		// Persona going on with the owner's own work is no background round.
		{"usage limit reset", limit, prompts.ItemSource(proto.Item{Role: "user", Kind: proto.KindText, Text: limit}), ""},
		{"wake", wake, prompts.ItemSource(proto.Item{Role: "user", Kind: proto.KindText, Text: wake}), ""},
	} {
		if got := backgroundRoundKind(tt.text, tt.source); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The owner's message to a conversation on a round they did not start carries interject_background with the setting
// off, once: the round is theirs from then on, and its pushes are no longer held (#4926).
func TestOwnerPreemptsBackgroundRound(t *testing.T) {
	ctx := context.Background()
	notice := prompts.RenderTaskNotice(prompts.TaskNotice{Title: "Build", Machine: "server", State: "done", Text: "Built."})
	for _, msg := range []relayMsg{
		{Tool: superviseTool, Text: "A supervision prompt."},
		{Tool: "conversation_send", From: "c2", FromTitle: "Side", Text: "Please look."},
		{Tool: noticeTool, From: "todo1", FromTitle: "Build", Text: notice, State: "done"},
	} {
		t.Run(msg.Tool, func(t *testing.T) {
			s, f, main := preemptService(t)
			if err := s.deliverRelay(ctx, "server", main, msg); err != nil {
				t.Fatal(err)
			}
			if _, set := f.lastSend(t)["interject_background"]; set {
				t.Fatal("the background message itself interjects")
			}
			if held := s.supervising(main); held != (msg.Tool == superviseTool) {
				t.Fatalf("pushes held: %v", held)
			}
			seeStatus(s, f, main, proto.StatusWorking)
			if got := ownerSend(t, s, main, "are you there?"); got["interject_background"] != true {
				t.Fatalf("the owner's message does not preempt: %v", got)
			}
			if s.supervising(main) {
				t.Fatal("the owner spoke and the conversation's pushes are still held")
			}
			if got := ownerSend(t, s, main, "and another thing"); got["interject_background"] != nil {
				t.Fatalf("the round is the owner's by now, the setting is off: %v", got)
			}
		})
	}
}

// A scheduled run into a conversation is a background round too.
func TestOwnerPreemptsScheduledRound(t *testing.T) {
	ctx := context.Background()
	s, f, main := preemptService(t)
	params := map[string]any{"session_id": main, "text": "Run the inspection.", "message_source": &proto.MessageSource{Kind: proto.SourcePatrol, Title: "Inspection"}}
	if err := s.call(ctx, "server", DeviceAssistantSend, params, nil); err != nil {
		t.Fatal(err)
	}
	seeStatus(s, f, main, proto.StatusWorking)
	if got := ownerSend(t, s, main, "stop"); got["interject_background"] != true {
		t.Fatalf("no preemption of a scheduled round: %v", got)
	}
}

// A conversation another one opened is on a round the owner did not start from its first message.
func TestOwnerPreemptsOpenedConversation(t *testing.T) {
	ctx := context.Background()
	s, f, _ := preemptService(t)
	sess, err := s.startConversation(ctx, startParams{Text: "Take this over.", Source: &proto.MessageSource{Kind: proto.SourceConversation, ConversationID: "c2", Title: "Side"}})
	if err != nil {
		t.Fatal(err)
	}
	seeStatus(s, f, sess.SessionID, proto.StatusWorking)
	if got := ownerSend(t, s, sess.SessionID, "what is this?"); got["interject_background"] != true {
		t.Fatalf("no preemption in a conversation another one opened: %v", got)
	}
}

// A round the owner started stays theirs when a background message joins it: interject_background decides.
func TestOwnerRoundKeepsSetting(t *testing.T) {
	ctx := context.Background()
	s, f, main := preemptService(t)
	ownerSend(t, s, main, "do the long thing")
	seeStatus(s, f, main, proto.StatusWorking)
	if err := s.deliverRelay(ctx, "server", main, relayMsg{Tool: superviseTool, Text: "A supervision prompt."}); err != nil {
		t.Fatal(err)
	}
	if got := ownerSend(t, s, main, "one more thing"); got["interject_background"] != nil {
		t.Fatalf("setting off, the owner's own round: %v", got)
	}
	on := true
	setSettings(t, s, func(v *Settings) { v.InterjectBackground = &on })
	if got := ownerSend(t, s, main, "and this"); got["interject_background"] != true {
		t.Fatalf("setting on: %v", got)
	}
}

// The mark is the last round's: a message of the manager's that is no background one starts a round and drops it,
// and the node asking what its own message runs on (no text) leaves it for the owner's.
func TestOwnerPreemptMark(t *testing.T) {
	ctx := context.Background()
	s, f, main := preemptService(t)
	supervise := relayMsg{Tool: superviseTool, Text: "A supervision prompt."}
	if err := s.deliverRelay(ctx, "server", main, supervise); err != nil {
		t.Fatal(err)
	}
	if params, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: main, Params: map[string]any{}}); err != nil || params["interject_background"] != nil {
		t.Fatalf("the node's own message: %v %v", params, err)
	}
	seeStatus(s, f, main, proto.StatusWorking)
	if got := ownerSend(t, s, main, "hello?"); got["interject_background"] != true {
		t.Fatalf("the node's question used the mark up: %v", got)
	}

	seeStatus(s, f, main, proto.StatusIdle)
	if err := s.deliverRelay(ctx, "server", main, supervise); err != nil {
		t.Fatal(err)
	}
	seeStatus(s, f, main, proto.StatusIdle) // that round ended
	if err := s.call(ctx, "server", DeviceAssistantSend, map[string]any{"session_id": main, "text": prompts.RenderAgentLimitContinue()}, nil); err != nil {
		t.Fatal(err)
	}
	seeStatus(s, f, main, proto.StatusWorking)
	if got := ownerSend(t, s, main, "still there?"); got["interject_background"] != nil {
		t.Fatalf("Persona going on with the owner's work is no background round: %v", got)
	}

	// A send the node refused starts no round.
	seeStatus(s, f, main, proto.StatusIdle)
	f.failAssistant = errors.New("synthetic dispatch failure")
	if err := s.deliverRelay(ctx, "server", main, supervise); err == nil {
		t.Fatal("the send did not fail")
	}
	if got := ownerSend(t, s, main, "hm"); got["interject_background"] != nil {
		t.Fatalf("a refused send left a mark: %v", got)
	}
}
