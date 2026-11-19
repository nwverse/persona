package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// One classification of a failed relay send (#11393): every code the issue names, the ones the node was found to give
// as well, the bus's shapes of the hub's codes, the manager's own errors and an unknown code.
func TestRelayFailureClassification(t *testing.T) {
	coded := func(code string) error { return &rpc.Error{Code: code, Message: "refused: " + code} }
	for _, tc := range []struct {
		name string
		err  error
		want relayFailure
	}{
		{"link dropped while it went", rpc.ErrDropped, relayFailedInDoubt},
		{"link closed", rpc.ErrClosed, relayFailedInDoubt},
		{"time ran out", context.DeadlineExceeded, relayFailedInDoubt},
		{"assistant_in_doubt", coded(assistantwire.CodeAssistantInDoubt), relayFailedInDoubt},
		{"node_closed from the bus", busError(coded(CodeNodeClosed)), relayFailedInDoubt},

		{"no code", &rpc.Error{Message: "the launcher did not start, the message was not delivered"}, relayFailedNotNow},
		{"internal", coded("internal"), relayFailedNotNow},
		{"machine_offline", coded(CodeMachineOffline), relayFailedNotNow},
		{"machine_offline from the bus", busError(coded(CodeMachineOffline)), relayFailedNotNow},
		{"node_closed", coded(CodeNodeClosed), relayFailedNotNow},

		{"busy", coded("busy"), relayFailedBusy},
		{"adapter_busy", coded("adapter_busy"), relayFailedBusy},
		{"compacting", coded("compacting"), relayFailedBusy},
		{"runtime_away", coded("runtime_away"), relayFailedBusy},
		{"assistant_control_failed", coded("assistant_control_failed"), relayFailedBusy},
		{"adapter_timeout", coded("adapter_timeout"), relayFailedBusy},
		{"adapter_unavailable", coded("adapter_unavailable"), relayFailedBusy},
		{"handover", coded("handover"), relayFailedBusy},
		{"node_restarting", coded("node_restarting"), relayFailedBusy},
		{"deadline", coded("deadline"), relayFailedBusy},

		{"claude_missing", coded(proto.CodeClaudeMissing), relayFailedSetup},
		{"launcher_missing", coded(proto.CodeLauncherMissing), relayFailedSetup},
		{"no_device from the bus", busError(coded(assistantwire.CodeNoDevice)), relayFailedSetup},

		{"the conversation is gone", &rpc.Error{Message: "conversation conv1 is gone"}, relayFailedGone},
		{"the manager's pick is gone", errors.New("the picked model is gone"), relayFailedGone},

		{"the manager's own refusal", errors.New("the model pick cannot be read"), relayFailedLocal},
		{"the manager's own localized refusal", i18n.Err("external-adapter.scope_required"), relayFailedLocal},

		{"assistant_supervision_runtime", coded(assistantwire.CodeSupervisionRuntime), relayFailedRefused},
		{"an unknown code", coded("unheard_of"), relayFailedRefused},
	} {
		if got := relayFailureOf(tc.err); got != tc.want {
			t.Errorf("%s (%v): %d, want %d", tc.name, tc.err, got, tc.want)
		}
		if got := refusedForGood(tc.err); got != (tc.want == relayFailedRefused) {
			t.Errorf("%s: refused for good %v", tc.name, got)
		}
	}
}

// refuseSendsTo has the fake node refuse every assistant.send into conversation id with err, and take the others.
func refuseSendsTo(f *fakeHub, id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev := f.onAssistantSend
	f.onAssistantSend = func(f *fakeHub, p map[string]any) {
		if prev != nil {
			prev(f, p)
		}
		f.failAssistant = nil
		if p["session_id"] == id {
			f.failAssistant = err
		}
	}
}

// sentTexts are the texts of the sends that went into conversation id.
func (f *fakeHub) sentTexts(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.sends {
		if a["session_id"] == id {
			text, _ := a["text"].(string)
			out = append(out, text)
		}
	}
	return out
}

func (f *fakeHub) pushList() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pushes)
}

// drainSettled drains conversation c and waits until no drain of it runs.
func drainSettled(t *testing.T, s *Service, c proto.Session) {
	t.Helper()
	s.drainRelays(c.MachineID, []proto.Session{c})
	waitFor(t, "the drain to settle", func() bool { return !drainingNow(s, c.SessionID) })
}

// A conversation that cannot take a message now, by a code (it is at work, compacting, away; its setup missing),
// keeps it queued: it is tried again on a later drain, never sooner than relayRetryGap after it failed, and goes in
// once the conversation takes it.
func TestQueuedNotNowRefusalWaitsForTheGap(t *testing.T) {
	codes := append(slices.Clone(relayBusyCodes), proto.CodeClaudeMissing, proto.CodeLauncherMissing)
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			s, f := newQuietService(t)
			gap := relayRetryGap
			relayRetryGap = 100 * time.Millisecond
			t.Cleanup(func() { relayRetryGap = gap })
			idle := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			s.queueRelay("conv1", relayMsg{From: "conv2", FromTitle: "Planning", Tool: "conversation_send", Text: "status?", At: time.Now().UTC()})
			var at []time.Time
			f.mu.Lock()
			f.failAssistant = &rpc.Error{Code: code, Message: "not now"}
			f.onAssistantSend = func(_ *fakeHub, p map[string]any) {
				if p["session_id"] == "conv1" {
					at = append(at, time.Now())
				}
			}
			f.mu.Unlock()
			tries := func() []time.Time {
				f.mu.Lock()
				defer f.mu.Unlock()
				return slices.Clone(at)
			}
			drainSettled(t, s, idle)
			if len(tries()) != 1 || s.queuedFor("conv1") != 1 {
				t.Fatalf("after the first try: %d tries, %d queued", len(tries()), s.queuedFor("conv1"))
			}
			s.drainRelays("server", []proto.Session{idle}) // at once: it waits for the gap, then its timer drains it
			waitFor(t, "the try after the gap", func() bool { return len(tries()) == 2 && !drainingNow(s, "conv1") })
			if got := tries(); got[1].Sub(got[0]) < relayRetryGap {
				t.Fatalf("tried again after %v, sooner than the gap", got[1].Sub(got[0]))
			}
			if s.queuedFor("conv1") != 1 || len(f.pushList()) != 0 || f.sentTo("conv2") != 0 {
				t.Fatal("a refusal that only means not now left the queue or was reported")
			}
			f.mu.Lock()
			f.failAssistant = nil
			f.mu.Unlock()
			s.drainRelays("server", []proto.Session{idle})
			waitFor(t, "the message to go in", func() bool { return s.queuedFor("conv1") == 0 && f.sentTo("conv1") == 1 })
		})
	}
}

// A queued message refused for good with a code nobody knows is tried once and ends as its kind says (#11393): a
// supervision message ends in the owner's push and is not moved (decision 2 is open), a card answer moves to the main
// conversation, Persona's own notices leave, a message from another conversation leaves with one line to its sender.
// Three more drains send nothing. An approval result stays queued and is tried again.
func TestQueuedRefusalForGoodEndsByKind(t *testing.T) {
	at := time.Now().UTC()
	for _, tc := range []struct {
		name string
		msg  relayMsg
	}{
		{"supervision message", relayMsg{From: superviseTool, FromTitle: "督办", Tool: superviseTool, Text: "📋 a task waits for review", At: at}},
		{"card answer", relayMsg{From: "todo_1", FromTitle: "待办「x」", Tool: askAnswerTool, Text: "the owner chose A", At: at, Owner: true}},
		{"approval result", relayMsg{From: approvalResultFrom + "ix1", FromTitle: "Approval result", Tool: askAnswerTool, Text: "Approval ix1 applied", At: at}},
		{"task notice", relayMsg{From: "todo_1", FromTitle: "任务「x」", Tool: noticeTool, State: "done", Text: "🔔 done", At: at}},
		{"task end", relayMsg{From: "todo_1", FromTitle: "任务「x」", Tool: taskEndTool, Text: "🔔 accepted", At: at}},
		{"spawn notice", relayMsg{From: "conv9", FromTitle: "Research", Tool: spawnNoticeTool, Text: "🔔 ended", At: at}},
		{"chat reply", relayMsg{From: "thread1", FromTitle: "群「x」", Tool: chatReplyTool, Text: "new messages", At: at}},
		{"conversation_send", relayMsg{From: "conv2", FromTitle: "Planning", Tool: "conversation_send", Text: "please check the build", At: at}},
		{"conversation_new", relayMsg{From: "conv2", FromTitle: "Planning", Tool: "conversation_new", Text: "please check the build", At: at}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, f := newQuietService(t)
			gap := relayRetryGap
			relayRetryGap = 0 // a message that stays is tried on every drain
			t.Cleanup(func() { relayRetryGap = gap })
			main := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			side := proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			sender := proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			f.mu.Lock()
			f.known["side1"], f.known["conv2"] = true, true
			f.mu.Unlock()
			f.setSessions("server", main, side, sender)
			refuseSendsTo(f, "side1", &rpc.Error{Code: "unheard_of", Message: "this conversation will not take it"})
			tries := relayTries(f, "side1")
			s.queueRelay("side1", tc.msg)

			drainSettled(t, s, side)
			switch tc.name { // what follows a refusal runs once the drain let go of it
			case "supervision message":
				waitFor(t, "the owner's push", func() bool { return len(f.pushList()) == 1 })
			case "card answer":
				waitFor(t, "the answer in the main conversation", func() bool { return f.sentTo("conv1") == 1 })
			case "conversation_send", "conversation_new":
				waitFor(t, "the line to the sender", func() bool { return f.sentTo("conv2") == 1 })
			}
			for range 3 {
				drainSettled(t, s, side)
			}
			got := len(triesOf(f, tries))
			if tc.name == "approval result" {
				if got != 4 || s.queuedFor("side1") != 1 || f.sentTo("conv1") != 0 {
					t.Fatalf("an approval result: %d tries, %d queued, %d in the main conversation", got, s.queuedFor("side1"), f.sentTo("conv1"))
				}
				return
			}
			if got != 1 || s.queuedFor("side1") != 0 {
				t.Fatalf("%d tries, %d still queued: want one try and none", got, s.queuedFor("side1"))
			}
			pushes := f.pushList()
			switch tc.name {
			case "supervision message":
				want := i18n.T(s.lang(ctx), "assistant-conversations.supervision_delivery_refused")
				if len(pushes) != 1 || pushes[0].SessionID != "side1" || pushes[0].Body != want {
					t.Fatalf("pushes %+v, want one about side1 saying %q", pushes, want)
				}
				if f.sentTo("conv1") != 0 || f.sentTo("conv2") != 0 {
					t.Fatal("the refused supervision message moved to another conversation")
				}
			case "card answer":
				if texts := f.sentTexts("conv1"); !strings.Contains(texts[0], "the owner chose A") {
					t.Fatalf("the main conversation got %q", texts[0])
				}
			case "conversation_send", "conversation_new":
				line := f.sentTexts("conv2")[0]
				for _, want := range []string{"side1", "unheard_of", tc.msg.Tool, "please check the build"} {
					if !strings.Contains(line, want) {
						t.Errorf("the sender's line lacks %q:\n%s", want, line)
					}
				}
			}
			if tc.name != "supervision message" && len(pushes) != 0 {
				t.Fatalf("pushed %+v", pushes)
			}
			if tc.name != "card answer" && f.sentTo("conv1") != 0 {
				t.Fatal("the main conversation got the refused message")
			}
			if tc.name != "conversation_send" && tc.name != "conversation_new" && f.sentTo("conv2") != 0 {
				t.Fatal("a line went to a conversation that sent nothing")
			}
			for range 3 { // the line, the moved answer and the push are told once
				drainSettled(t, s, side)
				drainSettled(t, s, sender)
				drainSettled(t, s, main)
			}
			if len(triesOf(f, tries)) != 1 || len(f.pushList()) != len(pushes) || f.sentTo("conv1") > 1 || f.sentTo("conv2") > 1 {
				t.Fatal("a later drain sent the refused message again or told it twice")
			}
		})
	}
}

// A card answer the main conversation refuses as well stays queued there; it is not moved again.
func TestCardAnswerRefusedByTheMainConversationStays(t *testing.T) {
	s, f := newQuietService(t)
	gap := relayRetryGap
	relayRetryGap = 0
	t.Cleanup(func() { relayRetryGap = gap })
	main := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	refuseSendsTo(f, "conv1", &rpc.Error{Code: "unheard_of", Message: "no"})
	tries := relayTries(f, "conv1")
	s.queueRelay("conv1", relayMsg{From: "todo_1", FromTitle: "待办「x」", Tool: askAnswerTool, Text: "the owner chose A", At: time.Now().UTC()})
	for range 3 {
		drainSettled(t, s, main)
	}
	if got := len(triesOf(f, tries)); got != 3 || s.queuedFor("conv1") != 1 {
		t.Fatalf("%d tries, %d queued: want it kept and tried on each drain", got, s.queuedFor("conv1"))
	}
}

// The direct tries refused for good end the same way: nothing is queued for a task's end, a card answer moves to the
// main conversation, an approval result is queued for its conversation, and a supervision message to a side
// conversation ends in the owner's push, neither queued nor moved.
func TestDirectRefusalForGoodEndsByKind(t *testing.T) {
	setup := func(t *testing.T) (*Service, *fakeHub, *[]string) {
		s, f := newQuietService(t)
		f.mu.Lock()
		f.known["side1"] = true
		f.mu.Unlock()
		f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
			proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
		refuseSendsTo(f, "side1", &rpc.Error{Code: "unheard_of", Message: "no"})
		return s, f, relayTries(f, "side1")
	}
	ctx := context.Background()
	t.Run("task end", func(t *testing.T) {
		s, f, tries := setup(t)
		s.relayOrQueue(ctx, "side1", relayMsg{From: "todo_1", FromTitle: "任务「x」", Tool: taskEndTool, Text: "🔔 accepted", At: time.Now().UTC()})
		if len(triesOf(f, tries)) != 1 || s.queuedFor("side1") != 0 || len(f.pushList()) != 0 {
			t.Fatal("a task's end refused for good was queued or pushed")
		}
	})
	t.Run("card answer", func(t *testing.T) {
		s, f, tries := setup(t)
		if err := s.relayToAsker(ctx, askTodo{TodoID: "todo_2", MachineID: "server", SessionID: "side1"}, "待办「y」", "the owner chose B", true); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the answer in the main conversation", func() bool { return f.sentTo("conv1") == 1 })
		if len(triesOf(f, tries)) != 1 || s.queuedFor("side1") != 0 || !strings.Contains(f.sentTexts("conv1")[0], "the owner chose B") {
			t.Fatal("the refused card answer did not move to the main conversation")
		}
	})
	t.Run("approval result", func(t *testing.T) {
		s, f, tries := setup(t)
		if err := s.deliverApprovalResult(approvalJournalEntry{ID: "ix1", MachineID: "server", SessionID: "side1", Status: "resolved",
			Result: "Approval ix1 applied", UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the queue's try", func() bool { return len(triesOf(f, tries)) == 2 && !drainingNow(s, "side1") })
		if s.queuedFor("side1") != 1 || f.sentTo("conv1") != 0 {
			t.Fatal("a refused approval result was not kept for its conversation")
		}
	})
	t.Run("supervision message", func(t *testing.T) {
		s, f, task := newSupervisedService(t)
		f.mu.Lock()
		f.known["side1"] = true
		f.mu.Unlock()
		f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
			proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
		refuseSendsTo(f, "side1", &rpc.Error{Code: "unheard_of", Message: "no"})
		tries, main := relayTries(f, "side1"), f.sentTo("conv1")
		if _, _, err := s.updateSupervision(ctx, task.ID, func(v *Supervision) bool {
			v.State = reviewPending
			return true
		}); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		task = todoNow(s, task.ID)
		if !s.deliverDigest(ctx, s.settings(ctx), "side1", watchSignals(task, nil, now), "", now, time.Time{}) {
			t.Fatal("the refused supervision message was not retired")
		}
		waitFor(t, "the owner's push", func() bool {
			return slices.ContainsFunc(f.pushList(), func(p Notification) bool {
				return p.SessionID == "side1" && p.Body == i18n.T(s.lang(ctx), "assistant-conversations.supervision_delivery_refused")
			})
		})
		if len(triesOf(f, tries)) != 1 || s.queuedFor("side1") != 0 || f.sentTo("conv1") != main {
			t.Fatal("the refused supervision message was queued or moved")
		}
		if got := todoNow(s, task.ID); got.DoneAt != nil || got.Supervision.State != reviewPending {
			t.Fatalf("the task must stay open for review: %+v", got)
		}
	})
}

// Until #11393's decision 1 is answered, conversation_send returns the coded "not now" refusals to the sender and
// queues nothing, as before; a computer away, also as the bus hands the hub's code back, still queues the message.
func TestConversationSendNotNowRefusals(t *testing.T) {
	try := func(t *testing.T, refusal error) (string, error, int) {
		s, f := newQuietService(t)
		f.mu.Lock()
		f.failAssistant = refusal
		f.known["conv2"] = true
		f.mu.Unlock()
		f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
			proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
		out, err := s.toolConversationSend(context.Background(), "conv2", []byte(`{"id":"conv1","text":"status?"}`))
		return out, err, s.queuedFor("conv1")
	}
	for _, code := range relayBusyCodes {
		if out, err, queued := try(t, &rpc.Error{Code: code, Message: "not now"}); err == nil || queued != 0 {
			t.Errorf("%s: reported %q, %d queued", code, out, queued)
		}
	}
	if out, err, queued := try(t, busError(&rpc.Error{Code: CodeMachineOffline, Message: "machine is offline"})); err != nil || queued != 1 ||
		!strings.Contains(out, "could not take the message now") {
		t.Fatalf("a computer away: %q, %v, %d queued", out, err, queued)
	}
}

// A quiet task's notice the conversation refuses for good sends the push it stood for: the card its agent waits on
// (needs), or the failed start (failed).
func TestQuietNoticeRefusedForGoodSendsItsPush(t *testing.T) {
	for _, state := range []string{"needs", "failed"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			s, f := newQuietService(t)
			f.mu.Lock()
			f.known["lark1"] = true
			f.mu.Unlock()
			main := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			lark := proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
			f.setSessions("server", main, lark)
			s.markReporter(ctx, "server", "lark1")
			if _, err := s.runTool(ctx, "server", "lark1", "agent_start",
				json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`)); err != nil {
				t.Fatal(err)
			}
			todo := onlyTodo(s)
			if !todo.Quiet || todo.Supervision != nil || todo.Origin != "lark1" {
				t.Fatalf("not a quiet task told in lark1: %+v", todo)
			}
			refuseSendsTo(f, "lark1", &rpc.Error{Code: "unheard_of", Message: "no"})
			tries := relayTries(f, "lark1")
			ix := proto.Interaction{ID: "ix1", MachineID: "box", SessionID: "s1", Kind: proto.IxQuestion,
				Questions: []proto.Question{{Question: "用哪个分支？"}}}
			if state == "needs" { // the notice is raised by the card, and queues behind lark1's turn
				agentTurn(s, f, proto.StatusBlocked, "", 1)
				s.fleet.mu.Lock()
				if s.fleet.interactions == nil {
					s.fleet.interactions = map[string]*pending{}
				}
				s.fleet.interactions[ix.ID] = &pending{ix: ix}
				s.fleet.mu.Unlock()
				s.InteractionRaised(ctx, InteractionRaised{Interaction: ix})
			} else {
				s.queueRelay("lark1", relayMsg{From: todo.ID, FromTitle: "任务「x」", Tool: noticeTool, State: state, Text: "🔔 没开起来", At: time.Now().UTC()})
			}
			waitFor(t, "the notice to queue", func() bool { return s.queuedFor("lark1") == 1 })
			before := len(f.pushList())
			lark.Status = proto.StatusIdle
			f.setSessions("server", main, lark)
			drainSettled(t, s, lark)
			waitFor(t, "the push", func() bool { return len(f.pushList()) == before+1 })
			p := f.pushList()[before]
			switch state {
			case "needs":
				if p.Kind != assistantwire.NotifyInput || p.Interaction == nil || p.Interaction.ID != ix.ID {
					t.Fatalf("push %+v, want the card's", p)
				}
			case "failed":
				if p.SessionID != "lark1" || !strings.Contains(p.Title, todo.Title) {
					t.Fatalf("push %+v, want the failed start's", p)
				}
			}
			drainSettled(t, s, lark)
			if len(triesOf(f, tries)) != 1 || s.queuedFor("lark1") != 0 || len(f.pushList()) != before+1 {
				t.Fatal("the refused notice was tried or told again")
			}
		})
	}
}

// The line that tells a sender its message was refused for good (prompts/assistant/relay_refused.md), pinned in the
// owner's three languages.
func TestGoldenRelayRefused(t *testing.T) {
	d := prompts.RelayRefused{Tool: "conversation_send", ID: "side1", Title: "Research", Code: "unheard_of", Excerpt: "please check the build"}
	golden(t, "relay_refused", prompts.RenderRelayRefused(d))
	d.Language = "English"
	golden(t, "relay_refused.en", prompts.RenderRelayRefused(d))
	d.Language, d.Excerpt = "日本語", ""
	golden(t, "relay_refused.ja", prompts.RenderRelayRefused(d))
}
