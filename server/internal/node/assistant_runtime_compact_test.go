package node

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

const runtimeCompactTestID = "agy-11111111-2222-3333-4444-555555555555"

func runtimeCompactFixture(t *testing.T) (*Node, *fakeRuntime, func(string, int64, string)) {
	t.Helper()
	n, _ := runtimeNode(t)
	hello := assistantRuntimeHello()
	hello["runtime"], hello["name"], hello["launcher"] = "agy", "Antigravity", "agy"
	host := dialRuntime(t, n, hello)
	cwd := filepath.Join(n.home, ".persona", "assistant")
	push := func(status string, rev int64, turnError string) {
		t.Helper()
		row := proto.Session{SessionID: runtimeCompactTestID, Cwd: cwd, Status: status, Rev: rev, TurnError: turnError}
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{row}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "runtime row updated", func() bool {
			got, _, ok := n.runtimes.rowOf(runtimeCompactTestID)
			return ok && got.Status == status && got.Rev == rev && got.TurnError == turnError
		})
	}
	push(proto.StatusIdle, 1, "")
	return n, host, push
}

func TestAssistantRuntimeCompaction(t *testing.T) {
	for _, test := range []struct {
		name, turnError, state string
		sawWorking             bool
	}{
		{"done", "", proto.CompactionDone, true},
		{"fast", "", proto.CompactionDone, false},
		{"failed", "Compaction failed", proto.CompactionFailed, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			n, host, push := runtimeCompactFixture(t)
			id := runtimeCompactTestID
			n.mu.Lock()
			n.ctxSent = map[string]map[string]string{id: {"snapshot": "previous context"}}
			n.mu.Unlock()
			params := map[string]any{"session_id": id}
			if _, err := nodeCall(t, n, "assistant.compact", params); err != nil {
				t.Fatal(err)
			}
			sends := host.got("session.send")
			want := map[string]any{"session_id": id, "text": "/compact", "assistant": true}
			var got map[string]any
			if len(sends) != 1 || json.Unmarshal(sends[0], &got) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("runtime sends: %s", sends)
			}
			if began := listed(t, n)[id].Compaction; began == nil || began.State != proto.CompactionRunning || began.StartedAt.IsZero() {
				t.Fatalf("running compaction: %+v", began)
			}
			if _, err := nodeCall(t, n, "assistant.compact", params); refusalCode(err) != "compacting" {
				t.Fatalf("duplicate compaction: %v", err)
			}
			n.sweepCompactions()
			if test.sawWorking {
				push(proto.StatusWorking, 2, "")
			}
			push(proto.StatusDone, 2, test.turnError)
			waitFor(t, "compaction settled", func() bool {
				got := listed(t, n)[id].Compaction
				return got != nil && got.State == test.state && got.FinishedAt != nil && got.Error == test.turnError
			})
			n.mu.Lock()
			_, sent := n.ctxSent[id]
			n.mu.Unlock()
			if sent {
				t.Fatal("the next message would omit the context snapshot")
			}
		})
	}
}

func TestAssistantRuntimeCompactionRefusals(t *testing.T) {
	n, host, push := runtimeCompactFixture(t)
	id := runtimeCompactTestID
	if err := n.assistantCompact(t.Context(), id, true); refusalCode(err) != "not_running" {
		t.Fatalf("only running: %v", err)
	}
	push(proto.StatusWorking, 2, "")
	if err := n.assistantCompact(t.Context(), id, false); refusalCode(err) != "busy" {
		t.Fatalf("busy: %v", err)
	}
	push(proto.StatusIdle, 3, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := n.assistantCompact(ctx, id, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if sends := host.got("session.send"); len(sends) != 0 {
		t.Fatalf("refused compaction sent input: %s", sends)
	}
	host.mu.Lock()
	host.answer = func(string, json.RawMessage) (any, error) {
		return nil, &rpc.Error{Code: "not_found", Message: "Conversation not found"}
	}
	host.mu.Unlock()
	if err := n.assistantCompact(t.Context(), id, false); refusalCode(err) != "not_found" {
		t.Fatalf("runtime refusal: %v", err)
	}
	if got := listed(t, n)[id].Compaction; got == nil || got.State != proto.CompactionFailed || got.FinishedAt == nil {
		t.Fatalf("failed compaction: %+v", got)
	}
}

func TestAssistantRuntimeCompactionCancelsPendingSend(t *testing.T) {
	n, host, _ := runtimeCompactFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	host.mu.Lock()
	host.answer = func(string, json.RawMessage) (any, error) {
		close(started)
		<-release
		return nil, nil
	}
	host.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- n.assistantCompact(ctx, runtimeCompactTestID, false) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("runtime did not receive the command")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send: %v", err)
	}
	if got := listed(t, n)[runtimeCompactTestID].Compaction; got == nil || got.State != proto.CompactionFailed || got.FinishedAt == nil {
		t.Fatalf("canceled compaction: %+v", got)
	}
}

func TestAssistantRuntimeHandoff(t *testing.T) {
	n, host, _ := runtimeCompactFixture(t)
	host.mu.Lock()
	host.answer = func(string, json.RawMessage) (any, error) {
		return map[string]any{"items": []proto.Item{
			{Seq: 1, Kind: proto.KindText, Role: "user", Text: "<persona-context kind=\"snapshot\">Snapshot</persona-context>\n\nQuestion"},
			{Seq: 2, Kind: proto.KindToolResult, Role: "assistant", Text: "Tool output"},
			{Seq: 3, Kind: proto.KindText, Role: "assistant", Text: "Answer"},
			{Seq: 4, Kind: proto.KindText, Role: "user", Text: "<persona-context>Internal context</persona-context>"},
		}}, nil
	}
	host.mu.Unlock()
	raw, err := nodeCall(t, n, "assistant.handoff", map[string]any{"session_id": runtimeCompactTestID})
	if err != nil {
		t.Fatal(err)
	}
	var got Handoff
	want := Handoff{Recent: []HandoffTurn{{Role: "user", Text: "Question"}, {Role: "assistant", Text: "Answer"}}}
	if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("handoff %s: %v", raw, err)
	}
	pages := host.got("session.items")
	var page struct {
		SessionID string `json:"session_id"`
		Limit     int    `json:"limit"`
	}
	if len(pages) != 1 || json.Unmarshal(pages[0], &page) != nil || page.SessionID != runtimeCompactTestID || page.Limit != 400 {
		t.Fatalf("runtime pages: %s", pages)
	}
}
