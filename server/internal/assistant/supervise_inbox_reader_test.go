package assistant

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

type recordingInboxReminders struct {
	enabled bool
	pending bool
	queries []string
}

func (r *recordingInboxReminders) remindersEnabled(context.Context) bool {
	r.queries = append(r.queries, "enabled")
	return r.enabled
}

func (r *recordingInboxReminders) taskReceivesReminder(task Todo, in *prompts.SuperviseInbox) bool {
	r.queries = append(r.queries, task.ID+"/"+in.Item)
	return true
}

func (r *recordingInboxReminders) eventUnhandled(_ context.Context, key string) bool {
	r.queries = append(r.queries, key)
	return r.pending
}

func TestQueuedSupervisionReadsInboxReminders(t *testing.T) {
	for _, tc := range []struct {
		name, kind       string
		enabled, pending bool
		notices          int
	}{
		{"pending", sigInbox, true, true, 1},
		{"handled before delivery", sigInbox, true, false, 0},
		{"disabled before delivery", sigInbox, false, true, 0},
		{"ordinary event", sigStarted, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, hub := newQuietService(t)
			s.stopSuperviseTimers()
			task := supervisedTodo(t, s, "Queued reminder")
			reader := &recordingInboxReminders{enabled: true, pending: true}
			s.inboxReminders = reader
			g := superviseSignal{Todo: task.ID, Kind: tc.kind, Key: "event-1", To: "conv1"}
			if tc.kind == sigInbox {
				g.Inbox = &prompts.SuperviseInbox{Item: "item-1", ToAgent: "unhandled", Count: 1}
			}
			hub.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking})
			s.queueSignal(g)
			s.sendDigest(t.Context())
			if tc.kind == sigInbox {
				waiting := pendingFor(s, task.ID)
				if len(waiting) != 1 || waiting[0].id() != g.id() || s.queuedFor("conv1") != 0 {
					t.Fatal("unhandled event did not wait for revalidation")
				}
			} else if s.queuedFor("conv1") != 1 || len(reader.queries) != 0 {
				t.Fatal("ordinary event should queue without consulting the Inbox")
			}

			reader.queries = nil
			reader.enabled, reader.pending = tc.enabled, tc.pending
			drainQueuedDigest(t, s, hub)
			s.sendDigest(t.Context())
			got := hub.notices()
			if pending := pendingFor(s, task.ID); len(pending) != 0 || len(got) != tc.notices || s.queuedFor("conv1") != 0 {
				t.Fatalf("notices=%q pending=%v queue=%d", got, pending, s.queuedFor("conv1"))
			}
			if len(got) == 1 && !strings.Contains(got[0], "signal="+tc.kind) {
				t.Fatalf("missing %s signal in %q", tc.kind, got[0])
			}
			var want []string
			if tc.kind == sigInbox {
				want = append(want, "enabled")
				if tc.enabled {
					want = append(want, task.ID+"/item-1", g.Key)
				}
			}
			if !slices.Equal(reader.queries, want) {
				t.Fatalf("Inbox queries=%q want=%q", reader.queries, want)
			}
		})
	}
}
