package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// agent_interrupt from the health space waits for the phone, which can take minutes. A turn that ended meanwhile is
// not stopped once the user allows it: the status is read again after the phone, and an idle Claude gets no
// session.interrupt (its Esc on an idle box quits claude, docs/lessons/agents.md "Esc on an idle Claude input box exits
// claude"). The instruction in then still goes, as to any agent that is not working. A refreshed blocked or unknown row is
// not proof of rest: it still reaches the node, whose own guard decides whether Esc is the right stop.
func TestAgentInterruptReadsTheStatusAgainAfterThePhone(t *testing.T) {
	for _, tc := range []struct {
		name          string
		then          string
		background    bool
		refreshStatus string
		refreshState  string
		wantInterrupt bool
	}{
		{name: "stop"},
		{name: "then", then: "Chart my sleep"},
		{name: "stop with background work", background: true},
		{name: "then with background work", then: "Chart my sleep", background: true},
		{name: "blocked", refreshStatus: proto.StatusBlocked, refreshState: proto.StateNeedsYou, wantInterrupt: true},
		{name: "blocked then", then: "Chart my sleep", refreshStatus: proto.StatusBlocked, refreshState: proto.StateNeedsYou, wantInterrupt: true},
		{name: "unknown", refreshStatus: proto.StatusUnknown, refreshState: proto.StateUnknown, wantInterrupt: true},
		{name: "unknown then", then: "Chart my sleep", refreshStatus: proto.StatusUnknown, refreshState: proto.StateUnknown, wantInterrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthHub()
			base := slices.Clone(f.sessions["server"])
			code := proto.Session{SessionID: "code-0000-0000", MachineID: "server", Cwd: "/home/me/src/app", Agent: "claude",
				Status: proto.StatusWorking, State: proto.StateWorking, Title: "app", UpdatedAt: time.Now()}
			f.setSessions("server", append(slices.Clone(base), code)...)
			s := newTestService(t, f)
			setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "main-0000-0000" })

			// The phone card is up; the session's state changes before the user taps Allow.
			go func() {
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
					if qs := s.pendingQuestions(); len(qs) == 1 {
						now := code
						now.Status, now.State = proto.StatusIdle, proto.StateIdle
						if tc.refreshStatus != "" {
							now.Status, now.State = tc.refreshStatus, tc.refreshState
						}
						if tc.background {
							now.State, now.BackgroundWorking = proto.StateWorking, true
							now.Background = []proto.BackgroundTask{{}}
						}
						f.setSessions("server", append(slices.Clone(base), now)...)
						allow := true
						raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
						if _, err := s.Action(context.Background(), Action{Op: "answer", InteractionID: qs[0].ID, Answer: raw}); err != nil {
							t.Error(err)
						}
						return
					}
				}
				t.Error("no card on the phone")
			}()
			args, _ := json.Marshal(map[string]any{"session_id": "code-0000-0000", "then": tc.then})
			out, isErr := spaceTool(t, s, "sleep-000-0000", "agent_interrupt", string(args))
			if isErr {
				t.Fatalf("error: %s", out)
			}
			got := f.interrupted()
			if tc.wantInterrupt {
				if len(got) != 1 || strings.Contains(out, "was not working") {
					t.Fatalf("%s after the phone: interrupted %v, result %s; want the node's stop decision", tc.refreshStatus, got, out)
				}
			} else {
				if len(got) != 0 {
					t.Fatalf("an agent whose turn ended while the phone asked was still interrupted: %v (result %s)", got, out)
				}
				if tc.background {
					if !strings.Contains(out, "background work is still running") || strings.Contains(out, "was not working") {
						t.Fatalf("background result: %s", out)
					}
				} else if strings.Contains(out, "Stopped") || !strings.Contains(out, "was not working") {
					t.Fatalf("result: %s", out)
				}
			}
			if tc.then != "" && !slices.Equal(sentToAgent(f), []string{tc.then}) {
				t.Fatalf("sent %q", sentToAgent(f))
			}
		})
	}
}

// A node that found no turn running pressed nothing (not_running, #10827): agent_interrupt says the agent was not
// working, never "Stopped".
func TestAgentInterruptTakesTheNodesNotRunning(t *testing.T) {
	s, f := newQuietService(t)
	f.setSessions("box", proto.Session{SessionID: "s1abcdef", MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
	f.answerNode("session.interrupt", func(string, json.RawMessage) (any, error) {
		return map[string]any{"ok": true, "settled": true, "not_running": true}, nil
	})
	res, err := runJSON(t, s, "conv1", "agent_interrupt", map[string]any{"machine": "box", "session_id": "s1abcdef"})
	if err != nil || strings.Contains(res, "Stopped") || !strings.Contains(res, "was not working") {
		t.Fatalf("not running: %s %v", res, err)
	}
}
