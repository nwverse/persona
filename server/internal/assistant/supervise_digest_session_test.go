package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// sessionGetsFor counts the session.get calls the node was asked for conversation to.
func sessionGetsFor(f *fakeHub, to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method != DeviceSessionGet {
			continue
		}
		var a struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(c.Params, &a) == nil && a.SessionID == to {
			n++
		}
	}
	return n
}

// A digest with no signal that needs delivery-time revalidation goes straight to the direct send, which looks the
// conversation up itself (tryDirectRelay's check, then sendRelay's runtime policy and supervision instructions: 3
// session.get calls). The up-front lookup for #16637's partition only matters when a PR, draft, decision-reply or
// unhandled-inbox signal rides along, so an ordinary digest must not add a fourth.
func TestOrdinaryDigestSkipsThePartitionSessionLookup(t *testing.T) {
	s, f, _ := newPRConflictTask(t, &memStore{})
	t.Cleanup(func() { stopRelayTimer(s, "conv1") })
	ctx := context.Background()
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	done, err := s.createTodo(ctx, "Completed task", "ship it", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, done.ID, "conv1", []string{"Merged"}, 0)
	sig := superviseSignal{Todo: done.ID, Kind: sigReport, Report: upReportDone, Key: "done", Detail: "Task done", At: time.Now().UTC()}
	s.updateSupervisePending(func(pending map[string]superviseSignal) { pending[sig.id()] = sig })

	before := sessionGetsFor(f, "conv1")
	if !s.deliverDigest(ctx, s.settings(ctx), "conv1", []superviseSignal{sig}, "", time.Now(), time.Time{}) {
		t.Fatal("the digest was not delivered")
	}
	if n := f.noticesTo("conv1"); len(n) != 1 {
		t.Fatalf("want the digest sent directly, got %q", n)
	}
	if got := sessionGetsFor(f, "conv1") - before; got > 3 {
		t.Errorf("deliverDigest asked the node for conv1 %d times, want at most the direct send's 3", got)
	}
}
