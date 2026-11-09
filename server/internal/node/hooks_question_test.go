package node

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func runQuestionHook(t *testing.T, n *Node, event string, questions []map[string]any) map[string]any {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"questions": questions})
	raw, _ := json.Marshal(hookInput{HookEventName: event, ToolName: "AskUserQuestion",
		SessionID: "s1", Cwd: "/tmp/x", ToolInput: input})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := n.handleHook(ctx, sockRequest{Input: raw})
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)["hookSpecificOutput"].(map[string]any)
}

// The terminal must show Claude's own question while the same card waits for a phone answer (#2382).
func TestClaudeQuestionPreToolUseDoesNotWaitForPhone(t *testing.T) {
	fakeAgent(t)
	n := codexTestNode(t)
	withCancelHub(t, n)
	done := runHookAsync(t, n, map[string]any{
		"hook_event_name": "PreToolUse", "session_id": decidedSession, "tool_name": "AskUserQuestion",
		"tool_input": map[string]any{"questions": []any{map[string]any{"question": "Which?", "header": "Pick",
			"options": []any{map[string]any{"label": "A"}, map[string]any{"label": "B"}}}}},
	})
	select {
	case out := <-done:
		if out != nil {
			t.Fatalf("handing the question to the terminal must not answer it: %v", out)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("PreToolUse still waits for the phone, so Claude cannot draw its terminal question")
	}
}

// A phone answer arrives through PermissionRequest while the terminal shows the same question.
func TestClaudeQuestionPermissionRequestAnswersFromPhone(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.answer = func(proto.Interaction) proto.Resolution {
		return proto.Resolution{Answers: map[string]string{"Which?": "A"}}
	}
	raw, _ := json.Marshal(map[string]any{"questions": []map[string]any{{"question": "Which?", "header": "Pick",
		"options": []map[string]any{{"label": "A"}, {"label": "B"}}}}})
	out, _ := runPermissionHook(t, n, hookInput{HookEventName: "PermissionRequest", ToolName: "AskUserQuestion",
		SessionID: decidedSession, ToolInput: raw})
	if out == nil {
		t.Fatal("PermissionRequest dropped the phone's answer")
	}
	decision := out.(map[string]any)["hookSpecificOutput"].(map[string]any)["decision"].(map[string]any)
	updated, _ := decision["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]string)
	if decision["behavior"] != "allow" || answers["Which?"] != "A" || len(fh.interactions()) != 1 {
		t.Fatalf("phone answer was not delivered: %v (cards=%d)", out, len(fh.interactions()))
	}
}

// A terminal answer wins over the waiting phone card, and the managed hooks report that answer (#2382).
func TestClaudeQuestionTerminalAnswerWithdrawsPhoneCard(t *testing.T) {
	fakeAgent(t)
	n := codexTestNode(t)
	h := withCancelHub(t, n)
	questions := []any{map[string]any{"question": "Which?", "header": "Pick",
		"options": []any{map[string]any{"label": "A"}, map[string]any{"label": "B"}}}}
	done := runHookAsync(t, n, map[string]any{
		"hook_event_name": "PermissionRequest", "session_id": decidedSession, "tool_name": "AskUserQuestion",
		"tool_input": map[string]any{"questions": questions},
	})
	waitFor(t, "the phone card", func() bool { asked, _ := h.counts(); return asked == 1 })
	postInput, _ := json.Marshal(map[string]any{"questions": questions, "answers": map[string]string{"Which?": "A"}})
	post, err := json.Marshal(hookInput{HookEventName: "PostToolUse", SessionID: decidedSession, ToolName: "AskUserQuestion",
		ToolUseID: "toolu_q1", ToolInput: postInput})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.handleHook(context.Background(), sockRequest{Input: post, Attempt: "a2"}); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out != nil {
			t.Fatalf("a question already answered in the terminal was answered again: %v", out)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("the terminal answer did not release PermissionRequest")
	}
	waitFor(t, "the phone card to be withdrawn", func() bool { _, cancelled := h.counts(); return cancelled == 1 })
	hooks := hookSettings("personad", "", false)["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Fatal("Persona-started Claude does not report AskUserQuestion PostToolUse")
	}
}

// A fast terminal answer may reach node before PermissionRequest; its id still prevents a phone card.
func TestClaudeQuestionTerminalCompletionBeforePermissionRequest(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	questions := []map[string]any{{"question": "Which?", "options": []map[string]any{{"label": "A"}, {"label": "B"}}}}
	input, _ := json.Marshal(map[string]any{"questions": questions})
	pre, _ := json.Marshal(hookInput{HookEventName: "PreToolUse", SessionID: "s1", ToolName: "AskUserQuestion",
		ToolUseID: "toolu_early", ToolInput: input})
	if _, err := n.handleHook(context.Background(), sockRequest{Input: pre}); err != nil {
		t.Fatal(err)
	}
	postInput, _ := json.Marshal(map[string]any{"questions": questions, "answers": map[string]string{"Which?": "A"}})
	post, _ := json.Marshal(hookInput{HookEventName: "PostToolUse", SessionID: "s1", ToolName: "AskUserQuestion",
		ToolUseID: "toolu_early", ToolInput: postInput})
	if _, err := n.handleHook(context.Background(), sockRequest{Input: post}); err != nil {
		t.Fatal(err)
	}
	out, took := runPermissionHook(t, n, hookInput{HookEventName: "PermissionRequest", SessionID: "s1",
		ToolName: "AskUserQuestion", ToolInput: input})
	if out != nil || len(fh.interactions()) != 0 || took > time.Second {
		t.Fatalf("an already-answered question asked the phone again: %v after %v (cards=%d)", out, took, len(fh.interactions()))
	}
}

func TestClaudeQuestionLateReceiptDoesNotCancelNextIdenticalQuestion(t *testing.T) {
	fakeAgent(t)
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	release := make(chan struct{})
	defer close(release)
	calls := 0
	fh.answer = func(proto.Interaction) proto.Resolution {
		calls++
		if calls == 1 {
			return proto.Resolution{Answers: map[string]string{"Which?": "A"}}
		}
		<-release
		return proto.Resolution{}
	}
	questions := []map[string]any{{"question": "Which?", "options": []map[string]any{{"label": "A"}, {"label": "B"}}}}
	input, _ := json.Marshal(map[string]any{"questions": questions})
	for _, id := range []string{"toolu_first", "toolu_second"} {
		raw, _ := json.Marshal(hookInput{HookEventName: "PreToolUse", SessionID: "s1", ToolName: "AskUserQuestion",
			ToolUseID: id, ToolInput: input})
		if _, err := n.handleHook(context.Background(), sockRequest{Input: raw}); err != nil {
			t.Fatal(err)
		}
		if id == "toolu_first" {
			if out := runQuestionHook(t, n, "PermissionRequest", questions); out["decision"] == nil {
				t.Fatal("the first phone answer was not delivered")
			}
		}
	}
	done := runHookAsync(t, n, map[string]any{
		"hook_event_name": "PermissionRequest", "session_id": "s1", "tool_name": "AskUserQuestion",
		"tool_input": map[string]any{"questions": questions},
	})
	waitFor(t, "the second card", func() bool { return len(fh.interactions()) == 2 })
	postInput, _ := json.Marshal(map[string]any{"questions": questions, "answers": map[string]string{"Which?": "A"}})
	post, _ := json.Marshal(hookInput{HookEventName: "PostToolUse", SessionID: "s1", ToolName: "AskUserQuestion",
		ToolUseID: "toolu_first", ToolInput: postInput})
	if _, err := n.handleHook(context.Background(), sockRequest{Input: post}); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		t.Fatalf("the first question's late receipt cancelled the second: %v", out)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestClaudeQuestionPhoneCompletionDoesNotSuppressNextQuestion(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	questions := []map[string]any{{"question": "Which?", "options": []map[string]any{{"label": "A"}, {"label": "B"}}}}
	fh.answer = func(proto.Interaction) proto.Resolution {
		return proto.Resolution{Answers: map[string]string{"Which?": "A"}}
	}
	first := runQuestionHook(t, n, "PermissionRequest", questions)
	if first["decision"] == nil {
		t.Fatal("the phone's first answer was not delivered")
	}
	postInput, _ := json.Marshal(map[string]any{"questions": questions, "answers": map[string]string{"Which?": "A"}})
	post, _ := json.Marshal(hookInput{HookEventName: "PostToolUse", SessionID: "s1", ToolName: "AskUserQuestion",
		ToolUseID: "toolu_q1", ToolInput: postInput})
	if _, err := n.handleHook(context.Background(), sockRequest{Input: post}); err != nil {
		t.Fatal(err)
	}
	fh.answer = func(proto.Interaction) proto.Resolution {
		return proto.Resolution{Answers: map[string]string{"Which?": "B"}}
	}
	second := runQuestionHook(t, n, "PermissionRequest", questions)
	decision, _ := second["decision"].(map[string]any)
	updated, _ := decision["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]string)
	if decision["behavior"] != "allow" || answers["Which?"] != "B" || len(fh.interactions()) != 2 {
		t.Fatalf("the previous completion suppressed a new question: %v (cards=%d)", second, len(fh.interactions()))
	}
}

// Multi-select labels and Other text use the same phone-answer channel without reinterpretation.
func TestClaudeQuestionPermissionRequestKeepsMultiAndOtherAnswers(t *testing.T) {
	for _, tc := range []struct{ name, answer string }{{"multi", "A, B"}, {"other", "Something else"}} {
		t.Run(tc.name, func(t *testing.T) {
			n := codexTestNode(t)
			fh := withFakeHub(t, n)
			fh.answer = func(proto.Interaction) proto.Resolution {
				return proto.Resolution{Answers: map[string]string{"Which?": tc.answer}}
			}
			questions := []map[string]any{{"question": "Which?", "multiSelect": tc.name == "multi",
				"options": []map[string]any{{"label": "A"}, {"label": "B"}}}}
			out := runQuestionHook(t, n, "PermissionRequest", questions)
			decision, _ := out["decision"].(map[string]any)
			updated, _ := decision["updatedInput"].(map[string]any)
			answers, _ := updated["answers"].(map[string]string)
			if decision["behavior"] != "allow" || answers["Which?"] != tc.answer {
				t.Fatalf("answer changed in transit: %v", out)
			}
		})
	}
}

// Alike-worded questions are denied (answers are keyed by question text) with a reason for the model and no card;
// distinct questions behave as before (#4429). Trim/MultiSelect normalization is covered in proto's table test.
func TestHookAskUserQuestionDuplicateGuard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n2hR, n2hW := io.Pipe()
	h2nR, h2nW := io.Pipe()
	t.Cleanup(func() { cancel(); n2hW.Close(); h2nW.Close(); n2hR.Close(); h2nR.Close() })
	n, _ := testNode(t)
	n.peer = rpc.New("n", n2hW)
	hub := rpc.New("h", h2nW)
	created := 0
	reply := &proto.Resolution{}
	hub.OnRequest = func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		created++
		return *reply, nil
	}
	go hub.Serve(ctx, n2hR)
	go n.peer.Serve(ctx, h2nR)

	dup := runQuestionHook(t, n, "PreToolUse", []map[string]any{
		{"question": "确认部署？"}, {"question": " 　确认部署？　 ", "multiSelect": true},
	})
	reason, _ := dup["permissionDecisionReason"].(string)
	if dup["permissionDecision"] != "deny" || created != 0 || !strings.Contains(reason, "AskUserQuestion") {
		t.Fatalf("duplicate questions must be denied without a card, telling the model how to ask again: %v (%d)", dup, created)
	}
	*reply = proto.Resolution{Answers: map[string]string{"发给谁？": "张三", "哪些部分？": "进展"}}
	ok := runQuestionHook(t, n, "PermissionRequest", []map[string]any{
		{"question": "发给谁？"}, {"question": "哪些部分？", "multiSelect": true},
	})
	decision, _ := ok["decision"].(map[string]any)
	if decision["behavior"] != "allow" || created != 1 {
		t.Fatalf("distinct questions must be allowed, one card created: %v (created=%d)", ok, created)
	}
	updated, _ := decision["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]string)
	if len(answers) != 2 || answers["发给谁？"] != "张三" {
		t.Fatalf("answers lost: %v", updated)
	}
}
