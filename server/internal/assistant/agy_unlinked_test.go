package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// agyRuntimeRow is an agy conversation the Antigravity runtime lists on a computer.
func agyRuntimeRow(id, title, cwd string, at time.Time) proto.Session {
	return proto.Session{SessionID: id, Agent: "agy", SourceKind: proto.SourceRuntime, SourceInstanceID: "agy", Title: title,
		Cwd: cwd, Status: proto.StatusDone, UpdatedAt: at}
}

// An agy pane whose conversation herdr never reported has only the placeholder notice to read. Its task's judgment
// says so (agy_unlinked) and names the agy conversations that may hold its reply: on the same computer, in the same
// directory, active since the pane was linked, on no other open to-do, newest first. It links none of them; the
// to-do's check says it in the owner's language and the outcome log names the same. A pane whose conversation reads,
// and a Claude agent with no reply, keep the generic "no reply" judgment (#13456).
func TestJudgeTaskDoneNamesAnUnlinkedAgyPane(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, nil)
	var logs logBuffer
	s.logf = logs.logf
	ctx := context.Background()
	linked := time.Now().Add(-10 * time.Minute).UTC()
	f.setSessions("box",
		proto.Session{SessionID: "agy-w1-p1", Agent: "agy", PaneID: "w1:p1", Cwd: "/code/app", Status: proto.StatusIdle, UpdatedAt: linked},
		agyRuntimeRow("agy-older", "Earlier work", "/code/app", linked.Add(-time.Minute)),
		agyRuntimeRow("agy-first", "Check the login page", "/code/app", linked.Add(2*time.Minute)),
		agyRuntimeRow("agy-second", "Login fix", "/code/app", linked.Add(5*time.Minute)),
		agyRuntimeRow("agy-elsewhere", "Other repository", "/code/other", linked.Add(5*time.Minute)),
		agyRuntimeRow("agy-taken", "Another task", "/code/app", linked.Add(6*time.Minute)),
		proto.Session{SessionID: "agy-w2-p1", Agent: "agy", PaneID: "w2:p1", Cwd: "/code/app", UpdatedAt: linked.Add(7 * time.Minute)},
		proto.Session{SessionID: "claude-1", Agent: "claude", Cwd: "/code/app", Status: proto.StatusIdle, UpdatedAt: linked.Add(8 * time.Minute)},
	)
	f.setItems(proto.Item{ID: agyPaneNoticeID, Seq: 1, Role: "system", Kind: proto.KindNotice, Text: "placeholder"})
	putTodo(s, Todo{ID: "other", Title: "Another task", Origin: "conv1", CreatedAt: linked,
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agy-taken", LinkedAt: linked, Agent: "agy"}}})
	task := Todo{ID: "task", Title: "Fix the login page", Origin: "conv1", CreatedAt: linked,
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agy-w1-p1", LinkedAt: linked, Cwd: "/code/app", Agent: "agy"}}}
	putTodo(s, task)

	c, err := s.judgeTaskDone(ctx, task)
	if err != nil || c.Verdict != verdictUnsure || c.Reason != reasonAgyUnlinked {
		t.Fatalf("judged %+v, %v", c, err)
	}
	second, first := strings.Index(c.Detail, "box/agy-second"), strings.Index(c.Detail, "box/agy-first")
	if !strings.Contains(c.Detail, "box/agy-w1-p1") || second < 0 || first < 0 || second > first {
		t.Fatalf("detail does not name the pane and its candidates newest first: %q", c.Detail)
	}
	for _, not := range []string{"agy-older", "agy-elsewhere", "agy-taken", "agy-w2-p1", "claude-1"} {
		if strings.Contains(c.Detail, not) {
			t.Fatalf("detail names %s: %q", not, c.Detail)
		}
	}
	if check := checkOf(i18n.Lang("en"), c, linked, time.Now()); check.Reason != i18n.T(i18n.Lang("en"), checkReasonAgyUnlinked) {
		t.Fatalf("check: %+v", check)
	}
	if n := len(todoNow(s, task.ID).Agents); n != 1 {
		t.Fatalf("a candidate was linked: %d agents", n)
	}

	// The outcome log of the same task names the pane and its candidates.
	done := todoNow(s, task.ID)
	now := time.Now().UTC()
	done.DoneAt = &now
	s.recordTaskOutcome(done, now)
	if !strings.Contains(logs.String(), "no agent reply to read: agy_unlinked box/agy-w1-p1 (candidates: box/agy-second, box/agy-first)") {
		t.Fatalf("outcome log:\n%s", logs.String())
	}

	// Its conversation reads (a prompt, no reply yet): the generic judgment, with no detail.
	f.setItems(proto.Item{ID: "1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "Fix the login page"})
	if c, err := s.judgeTaskDone(ctx, task); err != nil || c.Reason == reasonAgyUnlinked || c.Detail != "" || c.reasonKey != checkReasonNoReply {
		t.Fatalf("a readable pane: %+v, %v", c, err)
	}

	// A Claude agent with nothing to read: unchanged.
	f.setItems()
	claude := Todo{ID: "claude-task", Title: "Fix the login page", Origin: "conv1", CreatedAt: linked,
		Agents: []TodoAgent{{MachineID: "box", SessionID: "claude-1", LinkedAt: linked, Cwd: "/code/app", Agent: "claude"}}}
	putTodo(s, claude)
	if c, err := s.judgeTaskDone(ctx, claude); err != nil || c.Detail != "" || c.reasonKey != checkReasonNoReply {
		t.Fatalf("a Claude agent: %+v, %v", c, err)
	}
}

// With no candidate the pane is still named, and the detail says to look at the pane itself.
func TestJudgeTaskDoneUnlinkedAgyPaneWithoutCandidates(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, nil)
	linked := time.Now().Add(-time.Minute).UTC()
	f.setSessions("box", proto.Session{SessionID: "agy-w1-p1", Agent: "agy", PaneID: "w1:p1", Cwd: "/code/app", UpdatedAt: linked})
	f.setItems(proto.Item{ID: agyPaneNoticeID, Seq: 1, Role: "system", Kind: proto.KindNotice, Text: "placeholder"})
	task := Todo{ID: "task", Title: "Fix the login page", Origin: "conv1", CreatedAt: linked,
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agy-w1-p1", LinkedAt: linked, Cwd: "/code/app", Agent: "agy"}}}
	putTodo(s, task)
	c, err := s.judgeTaskDone(context.Background(), task)
	want := strings.TrimSpace(prompts.RenderAgyUnlinked(prompts.AgyUnlinked{Language: answerLanguage(s.hello(context.Background()).Locale),
		Panes: []prompts.AgyUnlinkedPane{{Machine: "box", Session: "agy-w1-p1"}}}))
	if err != nil || c.Reason != reasonAgyUnlinked || c.Detail != want {
		t.Fatalf("judged %+v, %v; want detail %q", c, err, want)
	}
}

// The supervisor is told the judgment's detail in place of the generic words of an unsure verdict.
func TestUnjudgedSignalCarriesTheAgyUnlinkedDetail(t *testing.T) {
	s, _, todo := newSupervisedService(t)
	ctx := context.Background()
	s.superviseJudged(ctx, todoNow(s, todo.ID), time.Now(), taskDoneCheck{Verdict: verdictUnsure, Reason: reasonAgyUnlinked,
		Detail: "the pane box/agy-w1-p1 is not linked; read box/agy-second"}, nil)
	s.supervise.mu.Lock()
	g, ok := s.supervise.pending[todo.ID+"|"+sigUnjudged]
	s.supervise.mu.Unlock()
	if !ok || g.Detail != "the pane box/agy-w1-p1 is not linked; read box/agy-second" {
		t.Fatalf("signal %+v (%t), pending %v", g, ok, pendingKinds(s, todo.ID))
	}
}

func TestGoldenAgyUnlinked(t *testing.T) {
	panes := []prompts.AgyUnlinkedPane{
		{Machine: "box", Session: "agy-w1-p1", Candidates: []prompts.AgyUnlinkedCandidate{
			{Machine: "box", Session: "agy-0f4e2d1c", Title: "Fix the login page"}, {Machine: "box", Session: "agy-7a9b3c5d"}}},
		{Machine: "laptop", Session: "agy-w2-p3"},
	}
	for name, lang := range map[string]string{"agy_unlinked": "", "agy_unlinked.en": "English", "agy_unlinked.ja": "日本語"} {
		golden(t, name, prompts.RenderAgyUnlinked(prompts.AgyUnlinked{Language: lang, Panes: panes}))
	}
}
