package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// turnPage is what the node pages as an agent's latest turn: the prompt, what the agent did since and, once the
// runtime closed the turn, its end as the transcript reader maps it (Codex's task_complete, transcript/codex.go).
// An open turn ends on a tool call still running.
func turnPage(turn, reply string, closed bool) []proto.Item {
	at := time.Now().UTC()
	items := []proto.Item{{ID: turn + "-ask#0", Kind: proto.KindText, Role: "user", Text: "iPad 横屏时登录页崩溃，修好并补测试", TS: at}}
	if reply != "" {
		items = append(items, proto.Item{ID: turn + "-reply#0", Kind: proto.KindText, Role: "assistant", Text: reply, TS: at})
	}
	if !closed {
		return append(items, proto.Item{ID: turn + "-tool#0", Kind: proto.KindToolUse, Role: "assistant", ToolName: "shell", ToolUseID: turn + "-call", TS: at})
	}
	return append(items, proto.Item{ID: turn + "-end#0", Kind: "turn_duration", Role: "system", TS: at,
		TurnDuration: &proto.TurnDuration{DurationMS: 1200, Source: "codex_task_complete", TurnID: turn}})
}

// turnRow is the task's agent s1 on box as its node lists it: status, the transcript's size rev and its last words.
func turnRow(status string, rev int64, text string) proto.Session {
	state := proto.StateUnread
	switch status {
	case proto.StatusWorking:
		state = proto.StateWorking
	case proto.StatusIdle:
		state = proto.StateIdle
	case proto.StatusUnknown:
		state = proto.StateUnknown
	}
	return proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "codex", Status: status, State: state,
		LastText: text, Rev: rev, UpdatedAt: time.Now()}
}

// nodeListed has box's node list row, and the manager hear that list (setSessions): all the manager is told of a session
// whose runtime sends no hook.
func nodeListed(s *Service, f *fakeHub, row proto.Session) {
	f.setSessions("box", row)
	s.setSessions("box", []proto.Session{row})
}

// turnCounts are what completion handling did for a task: the receipts in its conversation, the done judge's calls
// to the model, and the supervision messages put into the main conversation.
type turnCounts struct{ receipts, judgments, messages int }

func countTurns(t *testing.T, s *Service, todo string, hubs ...*fakeHub) (n turnCounts) {
	t.Helper()
	n.receipts = len(recoveryReceipts(t, s, todo))
	for _, f := range hubs {
		f.mu.Lock()
		n.judgments += f.judged
		f.mu.Unlock()
		n.messages += len(f.notices())
	}
	return n
}

// wantTurns waits until completion handling did want, and fails with what it did when it does not get there.
func wantTurns(t *testing.T, what string, want turnCounts, s *Service, todo string, hubs ...*fakeHub) {
	t.Helper()
	var got turnCounts
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got = countTurns(t, s, todo, hubs...); got == want {
			return
		}
	}
	t.Fatalf("%s: receipts=%d judgments=%d supervision messages=%d, want %d, %d, %d", what, got.receipts, got.judgments, got.messages,
		want.receipts, want.judgments, want.messages)
}

// stillTurns gives late duplicates the time to show (several supervision messages' spacing), then checks the counts.
func stillTurns(t *testing.T, what string, want turnCounts, s *Service, todo string, hubs ...*fakeHub) {
	t.Helper()
	time.Sleep(4 * shortSuperviseTimings.every)
	if got := countTurns(t, s, todo, hubs...); got != want {
		t.Fatalf("%s: receipts=%d judgments=%d supervision messages=%d, want %d, %d, %d", what, got.receipts, got.judgments, got.messages,
			want.receipts, want.judgments, want.messages)
	}
}

var oneEach = turnCounts{1, 1, 1}

// restartedOn is the manager started again on database st: newQuietServiceOn without setting the package's timings a
// second time. The test's first manager is not stopped the way a process is, and what it still runs reads them.
func restartedOn(t *testing.T, st Store) (*Service, *fakeHub) {
	t.Helper()
	s, f := newTaskServiceOn(t, st)
	shortSupervise(t, s)
	f.hello.Assistant.MachineID = "server"
	setSettings(t, s, func(v *Settings) { v.Main = "conv1" })
	f.known["conv1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	return s, f
}

// turnsQuiet waits until manager s is in the middle of nothing for task todo: no turn end being settled or judged, no
// sweep's look going. A restart is taken from there, so that the two managers of a test never work at once.
func turnsQuiet(t *testing.T, s *Service, todo string) {
	t.Helper()
	waitFor(t, "the manager to be done with the turn", func() bool {
		b := &s.tasks.turns
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.judging[todo] == 0 && !b.recovering[todo]
	})
}

// A supervised task whose agent's turn opens and completes while its node never lists it as working (a Codex
// inventory that says idle, done or unknown throughout, #5718) still gets its receipt, its judgment and the
// supervision message, once each: the sweep finds the turn the runtime closed.
func TestCompletionWithoutWorking(t *testing.T) {
	for _, status := range []string{proto.StatusIdle, proto.StatusDone, proto.StatusUnknown} {
		t.Run(status, func(t *testing.T) {
			s, f, todo := newSupervisedService(t)
			ctx := context.Background()
			nodeListed(s, f, turnRow(status, 1, ""))
			// The turn opens: the transcript grows, the status does not say so.
			f.setItems(turnPage("turn-1", "", false)...)
			nodeListed(s, f, turnRow(status, 2, ""))
			s.superviseWatch(ctx, time.Now())
			stillTurns(t, "while the turn is open", turnCounts{}, s, todo.ID, f)
			// The runtime closes it.
			f.setItems(turnPage("turn-1", "修好了，PR https://github.com/o/r/pull/901 已合并", true)...)
			row := turnRow(status, 3, "修好了，PR https://github.com/o/r/pull/901 已合并")
			nodeListed(s, f, row)
			s.superviseWatch(ctx, time.Now())
			wantTurns(t, "the turn completed with no working seen", oneEach, s, todo.ID, f)
			if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Supervision.State != reviewPending {
				t.Fatalf("not waiting for review: %+v %+v", now, now.Supervision)
			}
			// The same list and later sweeps are no second completion.
			for range 3 {
				nodeListed(s, f, row)
				s.superviseWatch(ctx, time.Now())
			}
			stillTurns(t, "after later sweeps", oneEach, s, todo.ID, f)
		})
	}
}

// The usual path (working, then stopped) and the sweep both see the same completed turn: whichever comes first
// handles it, and the other adds nothing.
func TestCompletionSeenByBothTriggers(t *testing.T) {
	for _, first := range []string{"the list's edge", "the sweep"} {
		t.Run(first, func(t *testing.T) {
			s, f, todo := newSupervisedService(t)
			ctx := context.Background()
			nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
			s.superviseWatch(ctx, time.Now()) // the manager's sweep has run: the turns' ledger is kept from here on
			f.setItems(turnPage("turn-1", "", false)...)
			nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
			f.setItems(turnPage("turn-1", "修好了", true)...)
			done := turnRow(proto.StatusDone, 3, "修好了")
			if first == "the sweep" {
				f.setSessions("box", done) // the node lists it stopped; that list has not reached the manager yet
				s.superviseWatch(ctx, time.Now())
				wantTurns(t, "the sweep", oneEach, s, todo.ID, f)
				s.setSessions("box", []proto.Session{done}) // and now it has: working → done
			} else {
				nodeListed(s, f, done)
				wantTurns(t, "the list's edge", oneEach, s, todo.ID, f)
				s.superviseWatch(ctx, time.Now())
			}
			s.superviseWatch(ctx, time.Now())
			stillTurns(t, "after both", oneEach, s, todo.ID, f)
			if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Supervision.State != reviewPending || now.Check == nil || now.Check.Verdict != verdictDone {
				t.Fatalf("not waiting for review with its check: %+v %+v %+v", now, now.Supervision, now.Check)
			}
		})
	}
}

// A completion told again changes nothing: the hub's turn end twice, the settled look again, the same list again, and
// a stale working row that comes after the turn's end and goes back to done (reordered lists). The task still waits
// for the one review, with the judgment on it.
func TestCompletionDuplicateEvents(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	ref := SessionRef{MachineID: "box", SessionID: "s1"}
	nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
	nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
	f.setItems(turnPage("turn-1", "修好了", true)...)
	done := turnRow(proto.StatusDone, 3, "修好了")
	nodeListed(s, f, done)
	wantTurns(t, "the turn", oneEach, s, todo.ID, f)

	s.TurnEnded(ctx, ref)
	s.TurnEnded(ctx, ref)
	s.taskTurnEnded(ctx, "box", "s1")
	nodeListed(s, f, done)
	s.superviseWatch(ctx, time.Now())
	stillTurns(t, "after the same end again", oneEach, s, todo.ID, f)

	// A list from before the end arrives late, then the current one again: the transcript has no turn past turn-1.
	stale := turnRow(proto.StatusWorking, 3, "")
	nodeListed(s, f, stale)
	nodeListed(s, f, done)
	s.superviseWatch(ctx, time.Now())
	stillTurns(t, "after a stale working row", oneEach, s, todo.ID, f)
	waitFor(t, "the review to wait again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Check == nil || now.Check.Verdict != verdictDone {
		t.Fatalf("the judgment was lost: %+v %+v", now, now.Check)
	}
	stillTurns(t, "after the review waits again", oneEach, s, todo.ID, f)
}

// A second turn in the same session is its own completion: here it ran while the manager heard nothing from the
// node (disconnected), and the list it hears afterwards says done, as the one before did.
func TestCompletionWhileDisconnected(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"not_done","reason":"测试还没补"}`
	nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
	nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
	f.setItems(turnPage("turn-1", "修了崩溃，测试还没补", true)...)
	nodeListed(s, f, turnRow(proto.StatusDone, 3, "修了崩溃，测试还没补"))
	wantTurns(t, "the first turn", oneEach, s, todo.ID, f)

	// Disconnected: the agent was sent more, worked and stopped. The manager heard none of it.
	f.judge = `{"verdict":"done","reason":"测试补上了"}`
	f.setItems(turnPage("turn-2", "测试补上了，PR https://github.com/o/r/pull/901 已合并", true)...)
	nodeListed(s, f, turnRow(proto.StatusDone, 5, "测试补上了，PR https://github.com/o/r/pull/901 已合并")) // connected again: done, as before
	s.superviseWatch(ctx, time.Now())
	wantTurns(t, "the turn that ran unheard", turnCounts{2, 2, 2}, s, todo.ID, f)
	if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Supervision.State != reviewPending {
		t.Fatalf("not waiting for review: %+v %+v", now, now.Supervision)
	}
	s.superviseWatch(ctx, time.Now())
	stillTurns(t, "after later sweeps", turnCounts{2, 2, 2}, s, todo.ID, f)
}

// The manager is started again on the same database at each point of a completion: before it heard of it at all,
// after the receipt, after the judgment, and after the supervision message. Each time the turn ends up with one
// receipt, one judgment applied and one message, whichever manager wrote them.
func TestCompletionAcrossRestart(t *testing.T) {
	for _, at := range []string{"before the receipt", "before the judgment", "before the message", "after the message"} {
		t.Run(at, func(t *testing.T) {
			database := taskRecoveryDatabase(t)
			s, f, todo := newSupervisedServiceOn(t, database)
			ctx := context.Background()
			nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
			s.superviseWatch(ctx, time.Now()) // the first manager's sweep has run: the turns' ledger is kept from here on
			done := turnRow(proto.StatusDone, 3, "修好了")
			page := turnPage("turn-1", "修好了", true)
			switch at {
			case "before the receipt":
				nodeListed(s, f, turnRow(proto.StatusWorking, 2, "")) // heard to start; it stops while the manager is down
			case "before the judgment":
				f.hookReads(receiptHooks{receipt: func(FeedItem) {
					f.mu.Lock()
					f.sessionsFail["box"] = 1000 // the receipt is out; this manager never gets to judge (its link is gone, then it is)
					f.mu.Unlock()
				}})
				nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
				f.setItems(page...)
				nodeListed(s, f, done)
				wantTurns(t, "the first manager's receipt", turnCounts{receipts: 1}, s, todo.ID, f)
			case "before the message":
				s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour} // judged; the message never leaves
				nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
				f.setItems(page...)
				nodeListed(s, f, done)
				wantTurns(t, "the first manager's judgment", turnCounts{receipts: 1, judgments: 1}, s, todo.ID, f)
				waitFor(t, "the review to wait", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
			default:
				nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
				f.setItems(page...)
				nodeListed(s, f, done)
				wantTurns(t, "the first manager", oneEach, s, todo.ID, f)
			}

			turnsQuiet(t, s, todo.ID)
			again, f2 := restartedOn(t, database) // the manager after a restart: memory gone, the database kept
			f2.judge = `{"verdict":"done","reason":"PR 已合并"}`
			f2.setItems(page...)
			if err := again.ensureTodosLoaded(ctx); err != nil {
				t.Fatal(err)
			}
			nodeListed(again, f2, done) // its first list: nothing to compare it with
			again.superviseWatch(ctx, time.Now())
			wantTurns(t, "after the restart", oneEach, again, todo.ID, f, f2)
			// What a restarted manager may hear again: the hub's turn end, the list, the sweep.
			again.TurnEnded(ctx, SessionRef{MachineID: "box", SessionID: "s1"})
			nodeListed(again, f2, done)
			again.superviseWatch(ctx, time.Now())
			stillTurns(t, "after the restart, told again", oneEach, again, todo.ID, f, f2)
			if now := todoNow(again, todo.ID); now.DoneAt != nil || now.Supervision.State != reviewPending {
				t.Fatalf("not waiting for review: %+v %+v", now, now.Supervision)
			}
		})
	}
}

// Nothing is recovered for a task nobody supervises, nor for a closed one: a turn that completes unseen there gets no
// receipt, no judgment and no message, and a closed task stays closed.
func TestCompletionUnseenNotSupervisedOrClosed(t *testing.T) {
	t.Run("not supervised", func(t *testing.T) {
		s, f := newTaskService(t) // no main conversation: the task it starts has no ledger
		shortSupervise(t, s)
		f.judge = `{"verdict":"done","reason":"修好了"}`
		todo := startedTask(t, s, f)
		if todo.Supervision != nil {
			t.Fatalf("supervised: %+v", todo.Supervision)
		}
		ctx := context.Background()
		nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
		f.setItems(turnPage("turn-1", "修好了", true)...)
		nodeListed(s, f, turnRow(proto.StatusIdle, 3, "修好了"))
		s.superviseWatch(ctx, time.Now())
		stillTurns(t, "a task nobody supervises", turnCounts{}, s, todo.ID, f)
		if now := todoNow(s, todo.ID); now.DoneAt != nil {
			t.Fatalf("closed: %+v", now)
		}
	})
	t.Run("closed", func(t *testing.T) {
		s, f, todo := newSupervisedService(t)
		ctx := context.Background()
		nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
		nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
		f.setItems(turnPage("turn-1", "修好了", true)...)
		nodeListed(s, f, turnRow(proto.StatusDone, 3, "修好了"))
		wantTurns(t, "the turn", oneEach, s, todo.ID, f)
		raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "看过了"})
		if _, err := s.runTool(ctx, "server", "conv1", "task_review", raw); err != nil {
			t.Fatal(err)
		}
		closed := todoNow(s, todo.ID)
		if closed.DoneAt == nil {
			t.Fatalf("not closed: %+v", closed)
		}
		// Its agent is sent something by hand afterwards and answers; no list says it worked.
		f.setItems(turnPage("turn-2", "不客气", true)...)
		nodeListed(s, f, turnRow(proto.StatusDone, 5, "不客气"))
		s.superviseWatch(ctx, time.Now())
		stillTurns(t, "a closed task", oneEach, s, todo.ID, f)
		if now := todoNow(s, todo.ID); now.DoneAt == nil || !now.DoneAt.Equal(*closed.DoneAt) {
			t.Fatalf("reopened: %+v", now)
		}
		// Opened again by hand: it is judged by what its agent does after that, not by the turn that ended before.
		if err := s.reopenTodo(ctx, todo.ID); err != nil {
			t.Fatal(err)
		}
		before := todoNow(s, todo.ID).Supervision.State
		s.superviseWatch(ctx, time.Now())
		s.superviseWatch(ctx, time.Now())
		stillTurns(t, "a task opened again", oneEach, s, todo.ID, f)
		if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Supervision.State != before {
			t.Fatalf("after the reopen: %+v %+v (was %q)", now, now.Supervision, before)
		}
	})
}

// What the sweep's look costs: a transcript read only for a supervised task whose agents look stopped, at most
// turnRecoverMost going at a time, one for each task until its row moves again, and none for a task whose agent
// works. So many stopped sessions cost nothing by themselves: only open supervised tasks are looked at, once each.
func TestTurnSweepReadsAreBounded(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	tasks := 2*turnRecoverMost + 1
	var rows []proto.Session
	for i := range tasks + 1 {
		sid := fmt.Sprintf("agent%02d", i)
		todo, err := s.createTodo(ctx, "任务 "+sid, "做 "+sid, "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		s.superviseTodo(ctx, todo.ID, "conv1", nil, 0)
		if err := s.linkTodoAgent(ctx, todo.ID, "box", sid, "任务", "/code/app", "codex"); err != nil {
			t.Fatal(err)
		}
		row := proto.Session{SessionID: sid, MachineID: "box", Cwd: "/code/app", Agent: "codex", Status: proto.StatusIdle,
			State: proto.StateIdle, Rev: 7, UpdatedAt: time.Now()}
		if i == tasks { // one more whose agent is listed as working: never read
			row.Status, row.State = proto.StatusWorking, proto.StateWorking
		}
		rows = append(rows, row)
	}
	for i := range 20 { // and stopped sessions of no task at all
		rows = append(rows, proto.Session{SessionID: fmt.Sprintf("other%02d", i), MachineID: "box", Cwd: "/code/app", Agent: "codex",
			Status: proto.StatusDone, State: proto.StateUnread, Rev: 3, UpdatedAt: time.Now()})
	}
	f.setSessions("box", rows...)
	f.setItems(turnPage("turn-1", "", false)...) // every transcript: a turn still open
	var reads, going, most atomic.Int32
	release := make(chan struct{})
	f.hookReads(receiptHooks{before: func(int) {
		reads.Add(1)
		if n := going.Add(1); n > most.Load() {
			most.Store(n)
		}
		<-release
	}, after: func(int) { going.Add(-1) }})
	s.superviseWatch(ctx, time.Now())
	waitFor(t, "the first reads", func() bool { return int(going.Load()) == turnRecoverMost })
	s.superviseWatch(ctx, time.Now()) // a tick while they are still going starts no more
	time.Sleep(50 * time.Millisecond)
	if got := int(reads.Load()); got != turnRecoverMost {
		t.Fatalf("%d reads going at a time, want at most %d", got, turnRecoverMost)
	}
	close(release)
	waitFor(t, "a read for each task", func() bool {
		s.superviseWatch(ctx, time.Now())
		return int(reads.Load()) == tasks && going.Load() == 0
	})
	for range 3 {
		s.superviseWatch(ctx, time.Now())
	}
	time.Sleep(50 * time.Millisecond)
	if got := int(reads.Load()); got != tasks || int(most.Load()) > turnRecoverMost {
		t.Fatalf("%d reads for %d tasks whose rows did not move (at most %d at a time, want %d)", got, tasks, most.Load(), turnRecoverMost)
	}
	rows[0].Rev, rows[0].UpdatedAt = 8, time.Now() // one transcript grew
	f.setSessions("box", rows...)
	waitFor(t, "the read of the row that moved", func() bool {
		s.superviseWatch(ctx, time.Now())
		return int(reads.Load()) == tasks+1
	})
	s.superviseWatch(ctx, time.Now())
	time.Sleep(50 * time.Millisecond)
	if got := int(reads.Load()); got != tasks+1 {
		t.Fatalf("%d reads after one row moved, want %d", got, tasks+1)
	}
}

// The stretch's receipt is reserved before the agents are asked once more whether they stopped. A node that does not
// answer that one time (a dropped link) must not leave the reservation behind: the turn still gets its receipt and
// its judgment once the node answers again, when its end is told again and, when nothing tells of it again, by the
// look the manager takes by itself a little later.
func TestTaskReceiptReservationIsReleased(t *testing.T) {
	for _, how := range []string{"told again", "by itself"} {
		t.Run(how, func(t *testing.T) {
			reachA, reachB, reachM, retry := agentReachAttempts, agentReachBackoff, agentReachMax, receiptRetryAfter
			agentReachAttempts, agentReachBackoff, agentReachMax, receiptRetryAfter = 2, time.Millisecond, 200*time.Millisecond, time.Hour
			if how == "by itself" {
				receiptRetryAfter = 30 * time.Millisecond
			}
			t.Cleanup(func() {
				agentReachAttempts, agentReachBackoff, agentReachMax, receiptRetryAfter = reachA, reachB, reachM, retry
			})
			s, f := newTaskService(t) // no main conversation: the task is not supervised, so no sweep looks at it
			f.judge = `{"verdict":"done","reason":"修好了"}`
			todo := startedTask(t, s, f)
			if todo.Supervision != nil {
				t.Fatalf("supervised: %+v", todo.Supervision)
			}
			ctx := context.Background()
			agentTurn(s, f, proto.StatusWorking, "", 0)
			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusDone,
				State: proto.StateUnread, LastText: "修好了", UpdatedAt: time.Now()})
			f.mu.Lock()
			f.onDevicesList = func() { // the node stops answering between the first look and the second
				f.mu.Lock()
				f.sessionsFail["box"] = agentReachAttempts
				f.mu.Unlock()
			}
			f.mu.Unlock()
			s.taskTurnEnded(ctx, "box", "s1")
			f.mu.Lock()
			fails, hook := f.sessionsFail["box"], f.onDevicesList
			f.mu.Unlock()
			if hook != nil || fails != 0 {
				t.Fatalf("the second look did not meet the dropped link: hook left %v, %d failures left", hook != nil, fails)
			}
			if how == "told again" {
				if got := len(recoveryReceipts(t, s, todo.ID)); got != 0 {
					t.Fatalf("%d receipts while the node did not answer", got)
				}
				s.taskTurnEnded(ctx, "box", "s1") // the node answers again, and the same turn end is told again
			}
			waitFor(t, "the receipt", func() bool { return len(recoveryReceipts(t, s, todo.ID)) == 1 })
			waitFor(t, "the task to close", func() bool { return todoNow(s, todo.ID).DoneAt != nil })
			time.Sleep(100 * time.Millisecond) // a second receipt or judgment would show by now
			f.mu.Lock()
			judged := f.judged
			f.mu.Unlock()
			if got := len(recoveryReceipts(t, s, todo.ID)); got != 1 || judged != 1 {
				t.Fatalf("%d receipts, judged %d times", got, judged)
			}
		})
	}
}

// A turn is closed where the runtime says so, and is named by that end; whatever starts or continues a turn after it
// leaves no closed turn to go by.
func TestTurnEndOf(t *testing.T) {
	ask := proto.Item{ID: "u1#0", Kind: proto.KindText, Role: "user", Text: "修登录崩溃"}
	reply := proto.Item{ID: "a1#0", Kind: proto.KindText, Role: "assistant", Text: "修好了"}
	codexEnd := proto.Item{ID: "line9#0", Kind: "turn_duration", Role: "system", TurnDuration: &proto.TurnDuration{DurationMS: 5, Source: "codex_task_complete", TurnID: "turn-7"}}
	claudeEnd := proto.Item{ID: "d1#0", Kind: "turn_duration", Role: "system", TurnDuration: &proto.TurnDuration{DurationMS: 5, Source: "claude", ParentUUID: "a1"}}
	stopped := proto.Item{ID: "n1#0", Kind: proto.KindNotice, Role: "system", Text: userStoppedNotice}
	for _, c := range []struct {
		name  string
		items []proto.Item
		key   string
	}{
		{"nothing read", nil, ""},
		{"open: the reply is last", []proto.Item{ask, reply}, ""},
		{"open: a tool runs", []proto.Item{ask, reply, {ID: "t1#0", Kind: proto.KindToolUse, Role: "assistant", ToolUseID: "c"}}, ""},
		{"Codex task_complete: its turn id", []proto.Item{ask, reply, codexEnd}, "turn-7"},
		{"Claude Code turn_duration: its item", []proto.Item{ask, reply, claudeEnd}, "d1#0"},
		{"stopped by the user", []proto.Item{ask, stopped}, "n1#0"},
		{"an end with nothing to name it", []proto.Item{ask, reply, {Kind: "turn_duration", Role: "system"}}, ""},
		{"a compaction notice after the end", []proto.Item{ask, reply, codexEnd, {ID: "n2#0", Kind: proto.KindNotice, Role: "system", Text: "compacted", Compact: &proto.Compact{}}}, "turn-7"},
		{"a queued message started the next turn", []proto.Item{ask, reply, codexEnd, {ID: "u2#0", Kind: proto.KindText, Role: "user", Text: "再看看深色", Queued: true}}, ""},
		{"a wake started the next turn", []proto.Item{ask, reply, claudeEnd, {ID: "w1#0", Kind: proto.KindNotice, Role: "system", Wake: true}}, ""},
		{"a background task's notice started the next turn", []proto.Item{ask, reply, claudeEnd, {ID: "k1#0", Kind: proto.KindNotice, Role: "system", Task: &proto.TaskNote{Kind: "shell"}}}, ""},
		{"the next turn ended too", []proto.Item{ask, reply, codexEnd, {ID: "u2#0", Kind: proto.KindText, Role: "user", Text: "再看看深色", Queued: true},
			{ID: "line20#0", Kind: "turn_duration", Role: "system", TurnDuration: &proto.TurnDuration{DurationMS: 5, Source: "codex_task_complete", TurnID: "turn-8"}}}, "turn-8"},
	} {
		end, closed := turnEndOf(c.items)
		if closed != (c.key != "") || end.Key != c.key {
			t.Errorf("%s: closed %v with key %q, want %q", c.name, closed, end.Key, c.key)
		}
	}
	if a, b := receiptID("todo1", taskTurn{Machine: "box", Session: "s1", Key: "turn-7"}), receiptID("todo1", taskTurn{Machine: "box", Session: "s1", Key: "turn-8"}); a == b ||
		a != receiptID("todo1", taskTurn{Machine: "box", Session: "s1", Key: "turn-7", Rev: 9}) || receiptID("todo1", taskTurn{Machine: "box", Session: "s1"}) != "" {
		t.Errorf("receipt ids: %q %q", a, b)
	}
}
