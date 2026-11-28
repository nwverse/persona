package transcript

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A Codex BotMux turn as Codex 0.170 writes it: the model's exec_command call (response_item), the CommandExecution the
// transcript reads (event_msg), the model's wrapped output (Chunk ID … Output:) and a final answer that is only the
// sentinel. The ids are synthetic; the shapes are a real rollout's.
const (
	codexBotmuxUserID = "01a1-user"
	codexBotmuxCallID = "call_example_card"
	codexBotmuxCard   = "om_example_card"
)

func codexBotmuxLines(t *testing.T, command, output string) []string {
	t.Helper()
	marshal := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	wrapper := "<botmux_reminder>reply with botmux send</botmux_reminder>\n\n<user_message>\nsummarize the cart fields\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_example_owner\" name=\"Alex Chen\" />"
	args := marshal(map[string]any{"cmd": command, "workdir": "/r/fake", "yield_time_ms": 30000})
	return []string{
		codexTestSession,
		codexTestEvent(`{"type":"task_started","turn_id":"turn-1"}`),
		codexTestItem(marshal(map[string]any{"type": "UserMessage", "id": codexBotmuxUserID, "content": []map[string]string{{"type": "text", "text": wrapper}}})),
		`{"timestamp":"` + codexTestTS + `","type":"response_item","payload":` + marshal(map[string]any{"type": "function_call", "name": "exec_command", "arguments": args, "call_id": codexBotmuxCallID}) + `}`,
		codexTestItem(marshal(map[string]any{
			"type": "CommandExecution", "id": codexBotmuxCallID, "command": []string{"/bin/bash", "-lc", command},
			"source": "unified_exec_startup", "status": "completed", "stdout": "", "aggregated_output": output, "exit_code": 0,
		})),
		`{"timestamp":"` + codexTestTS + `","type":"response_item","payload":` + marshal(map[string]any{
			"type": "function_call_output", "call_id": codexBotmuxCallID,
			"output": "Chunk ID: 0a1b2c\nWall time: 2.0739 seconds\nProcess exited with code 0\nOriginal token count: 80\nOutput:\n" + output,
		}) + `}`,
		codexTestItem(`{"type":"AgentMessage","id":"msg_example_final","content":[{"type":"Text","text":"BOTMUX_NOTHING_TO_SEND"}],"phase":"final_answer"}`),
		codexTestEvent(`{"type":"task_complete","turn_id":"turn-1"}`),
	}
}

func codexBotmuxReceipt(messageID string) string {
	return "✓ 已发送 " + messageID + " ｜ 未引用 ｜ @ou_example_owner\n" + // i18n-ignore: BotMux's output
		`{"success":true,"messageId":"` + messageID + `","sessionId":"bmx-session","quotedMessageId":null,"mentioned":[{"open_id":"ou_example_owner","name":""}]}` + "\n"
}

func codexBotmuxTurn(replies ...BotmuxTurnReply) []BotmuxTurn {
	return []BotmuxTurn{{
		TurnID: "t1", Seq: 1,
		Inbound: []BotmuxTurnInbound{{MessageID: "om_example_in", Text: "summarize the cart fields", Via: "lark",
			Sender: &BotmuxTurnSender{Type: "user", OpenID: "ou_example_owner", Name: "Alex Chen"}}},
		Replies: replies,
		Anchor:  &BotmuxTurnAnchor{CLIID: "codex", CLIEntryID: codexBotmuxUserID},
	}}
}

// codexBotmuxReply is the decoded call's Botmux, after checking that Codex's exec_command became the Bash call the
// decoders read and the final answer was only the sentinel.
func codexBotmuxReply(t *testing.T, items []proto.Item, command string) *proto.Botmux {
	t.Helper()
	var reply *proto.Botmux
	var found, sentinel bool
	for _, it := range items {
		switch {
		case it.Kind == proto.KindToolUse && it.ToolUseID == codexBotmuxCallID:
			found = true
			if got := botmuxCommand(it.ToolName, it.ToolInput); got != command {
				t.Fatalf("botmuxCommand(%q) = %q, want %q", it.ToolName, got, command)
			}
			if n := countBotmuxSendInvocations(command); n != 1 {
				t.Fatalf("countBotmuxSendInvocations = %d, want 1", n)
			}
			reply = it.Botmux
		case it.Role == "assistant" && it.Kind == proto.KindText:
			sentinel = WithoutBotmuxSentinel(it.Text) == "" && strings.TrimSpace(it.Text) == BotmuxSentinel
		}
	}
	if !found {
		t.Fatalf("no tool_use for %s in %+v", codexBotmuxCallID, items)
	}
	if !sentinel {
		t.Fatalf("final answer is not the bare sentinel: %+v", items)
	}
	return reply
}

func TestBotmuxCodexCardFileReply(t *testing.T) {
	const command = "botmux send --card-file /tmp/card.json --response-kind final --mention ou_example_owner"
	cases := []struct {
		name  string
		reply BotmuxTurnReply
		want  *proto.Botmux
	}{
		{"record without text", BotmuxTurnReply{MessageID: codexBotmuxCard, ResponseKind: "final"},
			&proto.Botmux{Kind: proto.BotmuxReply, Final: true}},
		{"record with text", BotmuxTurnReply{MessageID: codexBotmuxCard, ResponseKind: "final", Text: "Cart fields: extra_interactive_area"},
			&proto.Botmux{Kind: proto.BotmuxReply, Text: "Cart fields: extra_interactive_area", Final: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, _ := codexParseAll(t, codexBotmuxLines(t, command, codexBotmuxReceipt(codexBotmuxCard)))
			// without a structured record the card's words are unknown: the call stays a plain call
			if got := codexBotmuxReply(t, DecorateBotmux(items), command); got != nil {
				t.Fatalf("wrapper fallback decorated a card send: %+v", got)
			}
			got := codexBotmuxReply(t, ApplyBotmuxTurns(DecorateBotmux(items), codexBotmuxTurn(tc.reply), "01a1"), command)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("reply = %+v, want %+v", got, tc.want)
			}
			b, _ := json.Marshal(got)
			if containsAny(string(b), "ou_example_", "om_example_") {
				t.Fatalf("reply leaked a Lark id: %s", b)
			}
		})
	}
}

// A script that builds and checks the card before sending it prints more than a page holds: the receipt at its end
// is only in the outcome's tail of the page's copy.
func TestBotmuxCodexReceiptAfterLongOutput(t *testing.T) {
	const command = "cat >/tmp/spec.json <<'EOF'\n{\"title\":\"cart\"}\nEOF\npython3 build_card.py --spec /tmp/spec.json --output /tmp/card.json\n" +
		"python3 -I check_links.py /tmp/card.json\nbotmux send --card-file /tmp/card.json --response-kind final"
	output := strings.Repeat("ok\thttps://example.com/repo/blob/main/src/cart.ts#L1\n", 150) + "-- total=150 ok=150 bad=0\n" + codexBotmuxReceipt(codexBotmuxCard)
	if len(output) <= pageOutputLen {
		t.Fatalf("output %d bytes fits a page", len(output))
	}
	items, _ := codexParseAll(t, codexBotmuxLines(t, command, output))
	for i := range items {
		items[i] = LightItem(items[i])
	}
	var cut bool
	for _, it := range items {
		if it.Kind == proto.KindToolResult && it.ToolUseID == codexBotmuxCallID {
			cut = it.FullSize > 0 && !strings.Contains(it.Text, codexBotmuxCard) && strings.Contains(it.Outcome.Tail, codexBotmuxCard)
		}
	}
	if !cut {
		t.Fatal("the page's copy still has the receipt in its text: the case does not test the tail")
	}
	got := codexBotmuxReply(t, ApplyBotmuxTurns(DecorateBotmux(items), codexBotmuxTurn(BotmuxTurnReply{MessageID: codexBotmuxCard, ResponseKind: "final"}), "01a1"), command)
	if want := (&proto.Botmux{Kind: proto.BotmuxReply, Final: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("reply = %+v, want %+v", got, want)
	}
}

func TestBotmuxSendReceiptIDsReadsCodexOutputs(t *testing.T) {
	receipt := codexBotmuxReceipt(codexBotmuxCard)
	cases := map[string]*proto.Item{
		"command output":                  {Text: receipt},
		"model output (Chunk ID wrapper)": {Text: "Chunk ID: 0a1b2c\nWall time: 2.0 seconds\nProcess exited with code 0\nOriginal token count: 80\nOutput:\n" + receipt},
		"cut page, receipt in the tail":   {Text: "ok\n…", FullSize: 9000, Outcome: &proto.ToolOutcome{Tail: receipt}},
		"whole output, tail repeats it":   {Text: "ok\n" + receipt, Outcome: &proto.ToolOutcome{Tail: receipt}},
	}
	for name, res := range cases {
		if got := botmuxSendReceiptIDs(res); !reflect.DeepEqual(got, []string{codexBotmuxCard}) {
			t.Errorf("%s: receipt ids = %v", name, got)
		}
	}
	failed := &proto.Item{Text: "…", FullSize: 9000, Outcome: &proto.ToolOutcome{Status: proto.OutcomeOK, Tail: `{"success":false,"error":"card rejected"}`}}
	if botmuxToolResultSucceeded(failed) {
		t.Error("a failed receipt in the tail counted as a delivered send")
	}
}
