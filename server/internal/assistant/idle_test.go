package assistant

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The manager is idle unless its own work is going on: one of its conversations on a turn (or their machine not
// answering), a request of the hub's being answered, a hub event not handled, a scheduled task's message or a
// channel's answer going out. Another agent's turn on a computer is not its own work.
func TestManagerIdle(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	b := &BusHub{}
	ext := &fakeServing{}
	ctx := context.Background()
	sessions := func(list ...proto.Session) {
		f.mu.Lock()
		f.sessions = map[string][]proto.Session{"server": list}
		f.mu.Unlock()
	}
	busy := func(want string) {
		t.Helper()
		ok, why := managerIdle(ctx, b, s, ext)
		if want == "" && !ok {
			t.Fatalf("not idle: %s", why)
		}
		if want != "" && (ok || !strings.Contains(why, want)) {
			t.Fatalf("idle %v (%q), want busy with %q", ok, why, want)
		}
	}
	sessions(proto.Session{SessionID: "a1", Assistant: true}, proto.Session{SessionID: "x1", Status: proto.StatusWorking})
	busy("")

	sessions(proto.Session{SessionID: "a1", Assistant: true, Status: proto.StatusWorking})
	busy("a1 is on a turn")
	sessions(proto.Session{SessionID: "a1", Assistant: true, Starting: true})
	busy("a1 is on a turn")
	sessions(proto.Session{SessionID: "a1", Assistant: true, Compaction: &proto.Compaction{State: proto.CompactionRunning}})
	busy("a1 is on a turn")
	sessions(proto.Session{SessionID: "a1", Assistant: true})

	f.mu.Lock()
	f.sessionsFail["server"] = 1
	f.mu.Unlock()
	busy("does not answer")
	busy("")

	b.answering.Add(1)
	busy("request(s) of the hub's")
	b.answering.Add(-1)
	s.nodeAnswering.Add(1)
	busy("request(s) of the computers'")
	s.nodeAnswering.Add(-1)
	ext.n.Store(1)
	busy("stream(s) of the extensions'")
	ext.n.Store(0)
	b.pending.Add(2)
	busy("2 event(s)")
	b.pending.Add(-2)

	s.sched.mu.Lock()
	s.sched.sending = map[string]bool{"t1": true}
	s.sched.mu.Unlock()
	busy("scheduled task")
	s.sched.mu.Lock()
	delete(s.sched.sending, "t1")
	s.sched.mu.Unlock()

	s.chans.mu.Lock()
	s.chans.answering = map[string]bool{"r1": true}
	s.chans.mu.Unlock()
	busy("channel's answer")
	s.chans.mu.Lock()
	delete(s.chans.answering, "r1")
	s.chans.mu.Unlock()
	s.work.mu.Lock()
	s.work.init()
	s.work.triaging[[2]string{"server", "x1"}] = true
	s.work.mu.Unlock()
	busy("finished turn is being looked at")
	s.work.mu.Lock()
	delete(s.work.triaging, [2]string{"server", "x1"})
	s.work.mu.Unlock()
	busy("")
	if managerIdle(ctx, b, s, nil); !waitsOnPerson("interaction.create") || !waitsOnPerson("computer.elicit") || waitsOnPerson("mcp.call") {
		t.Fatal("waitsOnPerson")
	}

	// No machine for its conversations: none of them can be on a turn.
	if ok, why := newTestService(t, newFakeHub()).idle(ctx); !ok {
		t.Fatalf("no machine: %s", why)
	}
}

type fakeServing struct{ n atomic.Int64 }

func (f *fakeServing) Serving() int64 { return f.n.Load() }
