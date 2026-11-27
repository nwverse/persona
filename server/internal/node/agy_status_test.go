package node

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdrcore"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestAgyStatusCrossVerification(t *testing.T) {
	oldSummary, oldScreen := agySummaryStatusFn, agyStatusScreenFn
	t.Cleanup(func() { agySummaryStatusFn, agyStatusScreenFn = oldSummary, oldScreen })
	now := time.Now()
	for _, tc := range []struct {
		name, event, summary, text, want              string
		settled, notIdle, recent, advanced, readError bool
	}{
		{name: "settled", event: "Stop", settled: true, summary: "IDLE", text: "❯", want: proto.StatusDone},
		{name: "native idle", event: "Stop", settled: true, summary: "CASCADE_RUN_STATUS_IDLE", text: agySwitchIdleScreen, want: proto.StatusDone},
		{name: "advanced invocation", event: "PreInvocation", advanced: true, summary: "IDLE", text: ">", want: proto.StatusIdle},
		{name: "allow", event: "Stop", settled: true, summary: "IDLE", text: "Allow command?\n❯", want: proto.StatusBlocked, recent: true},
		{name: "options", event: "Stop", settled: true, summary: "IDLE", text: "Approve?\n❯ 1. Yes\n  2. No", want: proto.StatusBlocked},
		{name: "cancel", event: "Stop", settled: true, summary: "IDLE", text: "Esc to cancel\n❯", want: proto.StatusBlocked},
		{name: "working hooks", event: "PreInvocation", summary: "IDLE", text: "❯", want: proto.StatusBlocked},
		{name: "unsettled stop", event: "Stop", summary: "IDLE", text: "❯", want: proto.StatusBlocked},
		{name: "running summary", event: "Stop", settled: true, summary: "CASCADE_RUN_STATUS_RUNNING", text: "❯", want: proto.StatusBlocked},
		{name: "not fully idle", event: "Stop", settled: true, summary: "IDLE", notIdle: true, text: "❯", want: proto.StatusBlocked},
		{name: "historical prompt", event: "Stop", settled: true, summary: "IDLE", text: "Antigravity CLI\n>\nStill running", want: proto.StatusBlocked},
		{name: "read error", event: "Stop", settled: true, summary: "IDLE", text: "❯", readError: true, want: proto.StatusBlocked},
		{name: "recent send", event: "Stop", settled: true, summary: "IDLE", text: "❯", recent: true, want: proto.StatusWorking},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := fromTerminal(herdrcore.Terminal{Address: "t1", Foreground: &herdrcore.Foreground{Name: "agy"},
				Status: &herdrcore.Status{App: "antigravity", State: "blocked"}})
			if a.Agent != agyAgent || a.AgentStatus != proto.StatusBlocked {
				t.Fatal("terminal mapping lost AGY blocked")
			}
			report := agyHookReport{conversation: agyTestID, event: tc.event, at: now, settled: tc.settled}
			n := &Node{home: t.TempDir(), agyReports: map[string]agyHookReport{a.PaneID: report}}
			if tc.advanced {
				n.noteBlockedAt("agy-test", true, now.Add(-time.Second))
				report.invocationSteps = 1
				n.agyReports[a.PaneID] = report
			}
			if tc.recent {
				n.workSent.mark(a.PaneID, now)
			}
			agySummaryStatusFn = func(_ context.Context, _, id string) (string, bool, error) {
				if id != agyTestID {
					t.Fatalf("summary conversation = %q", id)
				}
				return tc.summary, tc.notIdle, nil
			}
			agyStatusScreenFn = func(_ *Node, _ context.Context, pane string) (string, error) {
				if tc.readError {
					return "", errors.New("unreadable screen")
				}
				return tc.text, nil
			}
			if got := n.liveStatus("agy-test", a, now); got != tc.want {
				t.Fatalf("liveStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgyStatusSummaryDatabase(t *testing.T) {
	sqlite, err := codexSQLiteProgram()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := "CREATE TABLE conversation_summaries (conversation_id TEXT, status TEXT, not_fully_idle INTEGER);" +
		"INSERT INTO conversation_summaries VALUES ('" + agyTestID + "', 'IDLE', 0);"
	if out, err := exec.CommandContext(ctx, sqlite, filepath.Join(root, "conversation_summaries.db"), query).CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %v: %s", err, out)
	}
	if status, notIdle, err := readAgySummaryStatus(ctx, root, agyTestID); err != nil || status != "IDLE" || notIdle {
		t.Fatalf("idle summary = %q, %v, %v", status, notIdle, err)
	}
	if status, notIdle, _ := readAgySummaryStatus(ctx, root, agyOtherTestID); status != "" || !notIdle {
		t.Fatal("missing conversation must fail closed")
	}
}
