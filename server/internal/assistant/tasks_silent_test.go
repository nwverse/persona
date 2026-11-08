package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// A task turn's end is told by what that turn did (#2349): a reply is done (judged, maybe closed); a model API error
// is interrupted (#1516); no reply and no tool call, with no error either (the model service answered 200 with
// nothing), is interrupted too: no "done", no done notice, nothing judged or closed.
func TestTaskTurnEndWithoutOutputIsInterrupted(t *testing.T) {
	prompt := proto.Item{Kind: proto.KindText, Role: "user", Text: "修登录崩溃"}
	for _, c := range []struct {
		name      string
		agent     string
		items     []proto.Item
		turnError string
		lastText  string // the session's last words, as the node lists them
		endState  string // the session's state when its turn ended (unread when "")
		state     string // the receipt's
		summary   string
		errText   string
		notice    []string // in the main conversation's supervision message
		judged    bool
	}{
		{name: "no output", items: []proto.Item{prompt, {Kind: proto.KindThinking, Role: "assistant", Text: "…"}},
			state: "interrupted", summary: interruptedSummary, errText: silentTurnErrorIn(i18n.Default),
			notice: []string{"被模型服务出错打断了", "这一轮没有任何输出"}},
		{name: "codex, no output", agent: "codex", items: []proto.Item{prompt},
			state: "interrupted", summary: interruptedSummary, errText: silentTurnErrorIn(i18n.Default),
			notice: []string{"这一轮没有任何输出"}},
		{name: "a reply", items: []proto.Item{prompt, {Kind: proto.KindToolUse, Role: "assistant", ToolName: "Edit"},
			{Kind: proto.KindText, Role: "assistant", Text: "修好了，提了 https://github.com/o/r/pull/901"}},
			state: "done", summary: "修好了，提了 https://github.com/o/r/pull/901",
			notice: []string{"做完了，等你验收", "修好了"}, judged: true},
		{name: "a model API error", items: []proto.Item{prompt}, turnError: "API Error: 529 overloaded",
			state: "interrupted", summary: interruptedSummary, errText: "API Error: 529 overloaded",
			notice: []string{"被模型服务出错打断了", "529 overloaded"}},
		// The node's word counts whatever the state now: an agent gone offline after the error was still cut off.
		{name: "a model API error, now unknown", items: []proto.Item{prompt}, turnError: "API Error: 529 overloaded", endState: proto.StateUnknown,
			state: "interrupted", summary: interruptedSummary, errText: "API Error: 529 overloaded",
			notice: []string{"被模型服务出错打断了", "529 overloaded"}},
		// Claude Code without credentials answers with a message of its own: the turn never ran (docs/assistant/onboarding.md 4.4).
		{name: "not logged in", items: []proto.Item{prompt, {Kind: proto.KindText, Role: "assistant", Text: "Not logged in · Please run /login"}},
			lastText: "Not logged in · Please run /login",
			state:    "interrupted", summary: interruptedSummary, errText: "Not logged in · Please run /login",
			notice: []string{"被模型服务出错打断了", "Not logged in"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, f := newQuietService(t)
			f.judge = `{"verdict":"done","reason":"做完了"}`
			todo := startedTask(t, s, f)
			ctx := context.Background()
			f.setItems(c.items...)
			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: cmp.Or(c.agent, "claude"),
				Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
			s.taskWorking(ctx, "box", "s1")
			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: cmp.Or(c.agent, "claude"),
				Status: proto.StatusDone, State: cmp.Or(c.endState, proto.StateUnread), TurnError: c.turnError, LastText: c.lastText, UpdatedAt: time.Now()})
			s.taskTurnEnded(ctx, "box", "s1")

			items, _, _ := s.feed(ctx, "conv1", time.Time{})
			last := items[len(items)-1]
			var rb receiptBody
			_ = json.Unmarshal(last.Body, &rb)
			if last.Kind != feedReceipt || rb.State != c.state || rb.Summary != c.summary || rb.Error != c.errText {
				t.Fatalf("receipt = %s %+v", last.Kind, rb)
			}
			waitFor(t, "the notice", func() bool { return len(f.notices()) == 1 })
			for _, want := range c.notice {
				if n := f.notices()[0]; !strings.Contains(n, want) {
					t.Errorf("notice lacks %q:\n%s", want, n)
				}
			}
			f.mu.Lock()
			judged := f.judged
			f.mu.Unlock()
			// Supervised (from the main conversation): never closed by the judge, only waiting for review.
			if closed := todoNow(s, todo.ID).DoneAt != nil; closed || (judged > 0) != c.judged {
				t.Fatalf("closed = %v after %d judgments, want judged %v", closed, judged, c.judged)
			}
		})
	}
}

func TestSilentTurn(t *testing.T) {
	prompt := proto.Item{Kind: proto.KindText, Role: "user", Text: "做吧"}
	for _, c := range []struct {
		name  string
		items []proto.Item
		want  bool
	}{
		{"only the prompt", []proto.Item{prompt}, true},
		{"thinking and a blank reply", []proto.Item{prompt, {Kind: proto.KindThinking, Role: "assistant", Text: "嗯"},
			{Kind: proto.KindText, Role: "assistant", Text: "  \n"}}, true},
		{"a reply", []proto.Item{prompt, {Kind: proto.KindText, Role: "assistant", Text: "好了"}}, false},
		{"tools only", []proto.Item{prompt, {Kind: proto.KindToolUse, Role: "assistant", ToolName: "Bash"},
			{Kind: proto.KindToolResult, Role: "user", Text: "ok"}}, false},
		{"stopped by the user", []proto.Item{prompt, {Kind: proto.KindNotice, Role: "system", Text: userStoppedNotice}}, false},
		{"nothing read", nil, false},
	} {
		if got := silentTurn(c.items); got != c.want {
			t.Errorf("%s: silentTurn = %v", c.name, got)
		}
	}
}
