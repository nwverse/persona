package assistant

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// secretary is an agent the main conversation did not hand out (a long-lived one the user opened), working on no todo.
func secretary(f *fakeHub) {
	f.setSessions("box", proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "秘书",
		Status: proto.StatusIdle, State: proto.StateIdle, UpdatedAt: time.Now()})
}

// agent_send with task hands new work to an agent already running: the message goes as written, and the agent becomes
// a supervised task of the main conversation (a todo with the instruction as its notes, the agent linked, done_when
// kept, the task card and what it was told in the feed).
func TestAgentSendTaskMakesATask(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	res, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据", "done_when": []string{"表格链接贴回来"}, "expect_minutes": 40}})
	if err != nil {
		t.Fatal(err)
	}
	if sent := sentToAgent(f); len(sent) != 1 || sent[0] != "把周报数据拉一下" {
		t.Fatalf("sent %q", sent)
	}
	todo := onlyTodo(s)
	if !strings.Contains(res, todo.ID) || todo.Title != "拉周报数据" || todo.Origin != "conv1" || todo.Notes != "把周报数据拉一下" ||
		len(todo.Agents) != 1 || todo.Agents[0].SessionID != "sec00001" {
		t.Fatalf("task: %s\n%+v", res, todo)
	}
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" || len(v.DoneWhen) != 1 || v.DoneWhen[0] != "表格链接贴回来" || v.ExpectMinutes != 40 {
		t.Fatalf("supervision: %+v", todo.Supervision)
	}
	if got := feedKinds(s, "conv1"); got != "task,routed" {
		t.Fatalf("feed: %s", got)
	}
}
