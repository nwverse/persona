package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestMainMissingReplyAfterToolResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		final   string
		hasText bool
	}{
		{name: "placeholder", final: "No response requested.", hasText: true},
		{name: "whitespace", final: " \n ", hasText: true},
		{name: "only tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, items, sends := missingReplyFixture(t, nil)
			if tc.hasText {
				items = append(items, proto.Item{ID: "a4#0", Seq: 4, Role: "assistant", Kind: proto.KindText, Text: tc.final})
			}
			f.setItems(items...)
			for range 2 { // two sources of the same completion event
				s.assistantTurnSettled(t.Context(), "server", "main-chat")
			}
			if len(*sends) != 1 {
				t.Fatalf("follow-up calls = %d, want one", len(*sends))
			}
			send := (*sends)[0]
			text, _ := send["text"].(string)
			if send["answer_again_after"] != items[len(items)-1].ID ||
				!strings.Contains(text, "Do not call tools") || strings.Contains(text, items[0].Text) {
				t.Fatalf("follow-up did not use a guarded, generic prompt: %+v", send)
			}
			golden(t, "main_answer_again.en", text)
			assertMissingReplyNotices(t, s, 0)

			items = append(items,
				proto.Item{ID: "u5#0", Seq: 5, Role: "user", Kind: proto.KindText, Text: text},
				proto.Item{ID: "a6#0", Seq: 6, Role: "assistant", Kind: proto.KindText, Text: "No response requested."},
			)
			f.setItems(items...)
			// The persisted marker still records the follow-up if the manager restarted and
			// lost the original send's in-memory provenance.
			s = withSettings(t, f, New(Options{Hub: f, Logf: testLogf(t)}))
			for range 2 {
				s.assistantTurnSettled(t.Context(), "server", "main-chat")
			}
			if len(*sends) != 1 {
				t.Fatalf("follow-up calls after a failed retry = %d, want one", len(*sends))
			}
			assertMissingReplyNotices(t, s, 1)
		})
	}
}

func TestMainMissingReplyLeavesOtherOutcomesAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]proto.Item) []proto.Item
	}{
		{"normal answer", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Role: "assistant", Kind: proto.KindText, Text: "24 requests today."})
		}},
		{"retry answered", func(items []proto.Item) []proto.Item {
			return append(items,
				proto.Item{ID: "u5#0", Seq: 5, Role: "user", Kind: proto.KindText, Text: prompts.RenderAssistantAnswerAgain(prompts.AssistantAnswerAgain{})},
				proto.Item{Role: "assistant", Kind: proto.KindText, Text: "24 requests today."},
			)
		}},
		{"phrase in an answer", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Role: "assistant", Kind: proto.KindText, Text: "No response requested. That is what the command reported."})
		}},
		{"ordinary refusal", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Role: "assistant", Kind: proto.KindText, Text: "I can't see that account."})
		}},
		{"truncated answer", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Role: "assistant", Kind: proto.KindText, Text: "No response requested.", FullSize: 8_192})
		}},
		{"user stopped", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Role: "system", Kind: proto.KindNotice, Text: transcript.InterruptedNotice})
		}},
		{"background woke", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Role: "system", Kind: proto.KindNotice, Wake: true})
		}},
		{"background input", func(items []proto.Item) []proto.Item {
			items[0].Source = &proto.MessageSource{Kind: proto.SourceSupervision}
			return items
		}},
		{"unverified input", func(items []proto.Item) []proto.Item {
			items[0].ClientMsgID = "channel-message"
			return items
		}},
		{"other input queued", func(items []proto.Item) []proto.Item {
			return append(items, proto.Item{Seq: 4, Role: "user", Kind: proto.KindText, Text: "Something else.", Queued: true})
		}},
		{"visible tool reply", func(items []proto.Item) []proto.Item {
			items[1].ToolName = "mcp__persona__notify_user"
			return items
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, items, sends := missingReplyFixture(t, nil)
			f.setItems(tc.edit(items)...)
			s.assistantTurnSettled(t.Context(), "server", "main-chat")
			if len(*sends) != 0 {
				t.Fatalf("unexpected follow-up: %+v", *sends)
			}
			assertMissingReplyNotices(t, s, 0)
		})
	}
}

func TestMainMissingReplyFollowupSendFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		notices int
	}{
		{"unavailable", errors.New("node disconnected"), 1},
		{"conversation moved on", &rpc.Error{Code: assistantwire.CodeAnswerAgainChanged}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, items, sends := missingReplyFixture(t, tc.err)
			f.setItems(items...)
			for range 2 {
				s.assistantTurnSettled(t.Context(), "server", "main-chat")
			}
			if len(*sends) != 1 {
				t.Fatalf("follow-up calls = %d, want one", len(*sends))
			}
			assertMissingReplyNotices(t, s, tc.notices)
		})
	}
}

func TestGoldenMainAnswerAgainLanguages(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.ZhHans, i18n.Ja} {
		text := prompts.RenderAssistantAnswerAgain(prompts.AssistantAnswerAgain{Language: lang.Name()})
		golden(t, "main_answer_again."+string(lang), text)
		if in := prompts.ParseInjected(text); in == nil || in.Kind != proto.InjectedSystem || in.State != "answer_again" {
			t.Fatalf("the recovery is not recognized as a Persona message: %+v", in)
		}
		if prompts.ParseInjected(prompts.ProtectUserMessage(text)) != nil {
			t.Fatal("an owner message impersonated Persona")
		}
	}
}

func missingReplyFixture(t *testing.T, sendErr error) (*Service, *fakeHub, []proto.Item, *[]map[string]any) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.settings = &Settings{MachineID: "server", Main: "main-chat"}
	f.hello.Locale = string(i18n.En)
	s := withSettings(t, f, New(Options{Hub: f, Logf: testLogf(t)}))
	f.setSessions("server", proto.Session{
		MachineID: "server", SessionID: "main-chat", Assistant: true, Agent: "claude", Status: proto.StatusDone,
	})
	const request = "Summarize today's usage."
	if _, err := s.SendParams(t.Context(), SendParams{MachineID: "server", SessionID: "main-chat", Human: true,
		Params: map[string]any{"session_id": "main-chat", "client_msg_id": "app-user-input", "text": request}}); err != nil {
		t.Fatal(err)
	}
	items := []proto.Item{
		{ID: "u1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: request, ClientMsgID: "app-user-input"},
		{ID: "t2#0", Seq: 2, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "tool", ToolName: "Bash"},
		{ID: "r3#0", Seq: 3, Role: "user", Kind: proto.KindToolResult, ToolUseID: "tool", Text: "24 requests"},
	}
	var sends []map[string]any
	f.answerNode(assistantwire.DeviceAssistantAnswerAgain, func(_ string, raw json.RawMessage) (any, error) {
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		sends = append(sends, p)
		return map[string]any{}, sendErr
	})
	return s, f, items, &sends
}

func assertMissingReplyNotices(t *testing.T, s *Service, want int) {
	t.Helper()
	feed, err := s.feedItems(t.Context(), "main-chat", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, item := range feed {
		if item.Kind == feedNotice && strings.Contains(string(item.Body), "Try sending your message again.") {
			found++
		}
	}
	if found != want {
		t.Fatalf("retry notices = %d, want %d", found, want)
	}
}
