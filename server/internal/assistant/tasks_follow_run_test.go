package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Every receipt a turn's end writes names the run whose turn it was (#10465), so a to-do's history gives it to that
// run without guessing from the computer's name; an interrupted receipt keeps the run through the reads that work out
// its resolution. A named turn's receipt keeps the feed id it had before.
func TestReceiptNamesItsRun(t *testing.T) {
	prompt := proto.Item{Kind: proto.KindText, Role: "user", Text: "修登录崩溃"}
	reply := "修好了，提了 https://github.com/o/r/pull/901"
	for _, c := range []struct {
		name      string
		items     []proto.Item
		lastText  string
		turnError string
		state     string
		turnKey   string // the turn's end, when the transcript names it: the receipt's id is receiptID's
	}{
		{name: "done", items: turnPage("turn-1", reply, true), lastText: reply, state: "done", turnKey: "turn-1"},
		{name: "cut off by the model service", items: []proto.Item{prompt}, turnError: "API Error: 529 overloaded", state: "interrupted"},
		{name: "no output", items: []proto.Item{prompt, {Kind: proto.KindThinking, Role: "assistant", Text: "…"}}, state: "interrupted"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, f := newQuietService(t)
			f.judge = `{"verdict":"not_done","reason":"还没验"}`
			todo := startedTask(t, s, f)
			ctx := context.Background()
			f.setItems(c.items...)
			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude",
				Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
			s.taskWorking(ctx, "box", "s1")
			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude",
				Status: proto.StatusDone, State: proto.StateUnread, TurnError: c.turnError, LastText: c.lastText, UpdatedAt: time.Now()})
			s.taskTurnEnded(ctx, "box", "s1")

			items, _, err := s.feed(ctx, "conv1", time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			var receipt *FeedItem
			for i := range items {
				if items[i].Kind == feedReceipt && items[i].Ref == todo.ID {
					receipt = &items[i]
				}
			}
			if receipt == nil {
				t.Fatalf("no receipt: %s", feedKinds(s, "conv1"))
			}
			var rb receiptBody
			if err := json.Unmarshal(receipt.Body, &rb); err != nil {
				t.Fatal(err)
			}
			if rb.State != c.state || rb.MachineID != "box" || rb.SessionID != "s1" || rb.Machine == "" {
				t.Fatalf("receipt = %s", receipt.Body)
			}
			if c.turnKey != "" {
				if want := receiptID(todo.ID, taskTurn{Machine: "box", Session: "s1", Key: c.turnKey}); receipt.ID != want {
					t.Fatalf("receipt id = %q, want %q", receipt.ID, want)
				}
			}
		})
	}
}

// A turn's receipt id hashes what it hashed before receipts named their run: a manager upgraded between a turn's
// receipt and a second look at that turn finds the receipt under the same id and writes no second one.
func TestReceiptIDKeepsItsInputs(t *testing.T) {
	if got := receiptID("todo1", taskTurn{Machine: "box", Session: "s1", Key: "turn-7"}); got != "feed_turn_6ea0311ff3f9e31dc14e" {
		t.Fatalf("receiptID = %q", got)
	}
}

// What the owner tells a task, and what the hub words for them, names the run it reached (#10465): the route's answer
// says where it was delivered, and the to-do's history puts the line on that run. A message that started a new Agent
// names that one, beside the new_agent fields older apps read.
func TestRoutedItemNamesItsRun(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	ctx := context.Background()
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据"}}); err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	type routed struct {
		Text            string `json:"text"`
		Relay           string `json:"relay"`
		From            string `json:"from"`
		MachineID       string `json:"machine_id"`
		SessionID       string `json:"session_id"`
		NewAgent        string `json:"new_agent"`
		NewAgentMachine string `json:"new_agent_machine"`
	}
	bodyOf := func(it FeedItem) routed {
		t.Helper()
		var b routed
		if err := json.Unmarshal(it.Body, &b); err != nil {
			t.Fatalf("routed body %s: %v", it.Body, err)
		}
		return b
	}

	item, err := s.routeToTask(ctx, "conv1", todo.ID, "顺便加一列环比")
	if err != nil {
		t.Fatalf("the owner's message: %v", err)
	}
	if b := bodyOf(item); b.MachineID != "box" || b.SessionID != "sec00001" || b.Relay != "" || b.From != "" {
		t.Fatalf("the owner's message: %s", item.Body)
	}
	item, err = s.relayToTask(ctx, "conv1", todo.ID, "选方案 B", nil)
	if err != nil {
		t.Fatalf("a relayed answer: %v", err)
	}
	if b := bodyOf(item); b.MachineID != "box" || b.SessionID != "sec00001" || b.Relay != routedRelay {
		t.Fatalf("a relayed answer: %s", item.Body)
	}

	// Its agent stops (read-only, #4754): the owner's next message starts a new one, and the line names that one.
	f.setSessions("box", proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "秘书",
		Launcher: "work", LastText: "表格在 /code/desk/out.xlsx", Status: proto.StatusOffline, State: proto.StateOffline, UpdatedAt: time.Now(),
		Capabilities: &proto.SessionCapabilities{Read: true, Terminal: true}, ReadOnlyReason: "stopped"})
	item, err = s.routeToTask(ctx, "conv1", todo.ID, "接着把环比也算上")
	if err != nil {
		t.Fatalf("the message to a stopped task: %v", err)
	}
	if b := bodyOf(item); b.NewAgent == "" || b.NewAgent == "sec00001" || b.SessionID != b.NewAgent || b.MachineID != "box" ||
		b.NewAgentMachine != b.MachineID {
		t.Fatalf("the message that started a new Agent: %s", item.Body)
	}

	// The feed keeps what the route answered.
	items, _, err := s.feed(ctx, "conv1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, it := range items {
		if it.Kind != feedRouted {
			continue
		}
		if b := bodyOf(it); b.From == "" && b.MachineID != "" && b.SessionID != "" {
			named++
		}
	}
	if named != 3 {
		t.Fatalf("%d routed lines name their run, want 3: %s", named, feedKinds(s, "conv1"))
	}
}
