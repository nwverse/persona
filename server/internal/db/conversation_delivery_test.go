package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/conversationlog"
)

// Simulate the already-persisted epoch-3 lost-reply / epoch-4 recovery state.
// New code deliberately refuses to create this state through activation.
func historicalDeliveryFixture(t *testing.T) (*DB, string, ConversationInput) {
	t.Helper()
	d, url := historyDB(t)
	ctx := t.Context()
	if err := d.AdoptConversation(ctx, "root", "chat", "s1", "claude", "h1"); err != nil {
		t.Fatal(err)
	}
	for epoch := int64(1); epoch <= 3; epoch++ {
		op := fmt.Sprintf("op%d", epoch+1)
		if err := d.BeginConversationSwitch(ctx, "root", "chat", ConversationSwitch{OperationID: op, ExpectedEpoch: epoch, SourceSegment: fmt.Sprintf("s%d", epoch), Request: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := d.PutConversationHandoff(ctx, "root", "chat", op, json.RawMessage(`{"summary":"handoff for new target"}`)); err != nil {
			t.Fatal(err)
		}
		if err := d.PrepareConversationSegment(ctx, "root", "chat", op, conversationlog.Segment{ID: fmt.Sprintf("s%d", epoch+1), MachineID: fmt.Sprintf("machine%d", epoch+1), SessionID: fmt.Sprintf("native%d", epoch+1), Engine: "agy"}, 0); err != nil {
			t.Fatal(err)
		}
		if err := d.ActivateConversationSegment(ctx, "root", "chat", op, epoch); err != nil {
			t.Fatal(err)
		}
		if epoch < 3 {
			if ok, err := d.AcceptIdleConversationSwitch(ctx, "root", "chat", op, epoch); err != nil || !ok {
				t.Fatalf("idle accept: %v %v", ok, err)
			}
		}
	}
	// Old binary had no dispatch columns. Backfill must use op3, not active s4.
	payload := []byte(`{"method":"assistant.send","params":{"text":"original content","images":[{"data":"original attachment"}]}}`)
	if err := d.EnqueueConversationInput(ctx, "root", "chat", "old", 4, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx, `UPDATE conversation_inputs SET epoch=3,state='in_doubt' WHERE client_msg_id='old'`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"next", "last"} {
		if err := d.EnqueueConversationInput(ctx, "root", "chat", id, 4, json.RawMessage(`{"text":"next"}`)); err != nil {
			t.Fatal(err)
		}
	}
	input, err := d.ConversationUnresolvedInput(ctx, "root", "chat")
	if err != nil || input == nil || input.DispatchSegment != "s3" || input.DispatchEpoch != 3 || input.DispatchMachine != "machine3" || input.DispatchSession != "native3" {
		t.Fatalf("legacy original target: %+v %v", input, err)
	}
	return d, url, *input
}

func TestHistoricalDeliverySettlementPreservesCurrentSwitchAndFIFO(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected", "absent"} {
		t.Run(outcome, func(t *testing.T) {
			d, url, input := historicalDeliveryFixture(t)
			ctx := t.Context()
			restarted, err := Open(ctx, url, ManagerSet)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if row, err := restarted.ClaimConversationInput(ctx, "root", "chat", 4); err != nil || row != nil {
				t.Fatalf("unresolved was overtaken: %+v %v", row, err)
			}
			if outcome == "absent" {
				if err := restarted.NoteConversationInputBlocked(ctx, "root", "chat", 4, input, "original receipt absent"); err != nil {
					t.Fatal(err)
				}
				pending, _ := d.ConversationUnresolvedInput(ctx, "root", "chat")
				if pending == nil || pending.BlockedReason != "original receipt absent" || pending.LastReconciledAt == nil || string(pending.Payload) != string(input.Payload) {
					t.Fatalf("uncertain payload/diagnostic lost: %+v", pending)
				}
				if row, err := d.ClaimConversationInput(ctx, "root", "chat", 4); err != nil || row != nil {
					t.Fatalf("absent receipt replayed/overtaken: %+v %v", row, err)
				}
				return
			}
			if err := restarted.ResolveHistoricalConversationDelivery(ctx, "root", "chat", 5, input, outcome); !errors.Is(err, ErrConversationEpoch) {
				t.Fatalf("root fence missing: %v", err)
			}
			if err := restarted.ResolveHistoricalConversationDelivery(ctx, "root", "chat", 4, input, outcome); err != nil {
				t.Fatal(err)
			}
			c, _ := d.LogicalConversation(ctx, "root", "chat")
			if c.Switch == nil || c.Switch.OperationID != "op4" || c.Switch.Phase != "active" {
				t.Fatalf("old receipt accepted new target: %+v", c)
			}
			handoff, _, err := d.ConversationPreparation(ctx, "root", "chat", "op4")
			if err != nil || len(handoff) == 0 {
				t.Fatalf("historical receipt consumed current handoff: %s %v", handoff, err)
			}
			row, err := restarted.ClaimConversationInput(ctx, "root", "chat", 4)
			if err != nil || row == nil || row.ClientMsgID != "next" || row.DispatchSegment != "s4" || row.DispatchEpoch != 4 {
				t.Fatalf("next input not claimed in order: %+v %v", row, err)
			}
			if again, err := d.ClaimConversationInput(ctx, "root", "chat", 4); err != nil || again != nil {
				t.Fatalf("concurrent/restarted claim duplicated: %+v %v", again, err)
			}
			if err := restarted.ResolveClaimedConversationDelivery(ctx, "root", "chat", 4, *row, "op4", "accepted"); err != nil {
				t.Fatal(err)
			}
			c, _ = d.LogicalConversation(ctx, "root", "chat")
			if c.Switch != nil {
				t.Fatalf("new target receipt failed acceptance: %+v", c.Switch)
			}
		})
	}
}

func TestManualConversationInputResolutionCASAuditAndIdempotency(t *testing.T) {
	for _, action := range []string{"accepted", "rejected", "replay"} {
		t.Run(action, func(t *testing.T) {
			d, _, input := historicalDeliveryFixture(t)
			ctx := t.Context()
			p := ConversationInputResolution{RequestID: "manual-1", ExpectedEpoch: 4, Input: input, Action: action, Reason: "operator verified original delivery"}
			stale := p
			stale.Input.Version++
			if err := d.ResolveConversationInputManually(ctx, "root", "chat", "human", stale); !errors.Is(err, ErrConversationEpoch) {
				t.Fatalf("stale CAS accepted: %v", err)
			}
			for range 2 {
				if err := d.ResolveConversationInputManually(ctx, "root", "chat", "human", p); err != nil {
					t.Fatal(err)
				}
			}
			changed := p
			changed.Action = "accepted"
			changed.Reason = "different intent"
			if err := d.ResolveConversationInputManually(ctx, "root", "chat", "human", changed); !errors.Is(err, ErrConversationEpoch) {
				t.Fatalf("idempotency key reused: %v", err)
			}
			var payload []byte
			var count int
			if err := d.pool.QueryRow(ctx, `SELECT original_payload FROM conversation_input_resolutions WHERE request_id='manual-1'`).Scan(&payload); err != nil || string(payload) != string(input.Payload) {
				t.Fatalf("original payload lost: %q %v", payload, err)
			}
			if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_input_resolutions`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("audit duplicated: %d %v", count, err)
			}
			if err := d.ResolveHistoricalConversationDelivery(ctx, "root", "chat", 4, input, "accepted"); !errors.Is(err, ErrConversationEpoch) {
				t.Fatalf("old receipt crossed manual fence: %v", err)
			}
			row, err := d.ClaimConversationInput(ctx, "root", "chat", 4)
			want := "next"
			if action == "replay" {
				want = "old"
			}
			if err != nil || row == nil || row.ClientMsgID != want || row.DispatchEpoch != 4 {
				t.Fatalf("manual action changed FIFO: %+v %v", row, err)
			}
		})
	}
}

func TestUnresolvedDeliveryFencesNewSwitchAndClaimRace(t *testing.T) {
	d, url := historyDB(t)
	ctx := t.Context()
	if err := d.AdoptConversation(ctx, "root", "chat", "s1", "claude", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := d.EnqueueConversationInput(ctx, "root", "chat", "one", 1, json.RawMessage(`{"text":"one"}`)); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	claims := make(chan *ConversationInput, 2)
	for _, conn := range []*DB{d, other} {
		wg.Add(1)
		go func(conn *DB) {
			defer wg.Done()
			row, err := conn.ClaimConversationInput(ctx, "root", "chat", 1)
			if err != nil && !errors.Is(err, ErrConversationSwitchBusy) {
				t.Error(err)
			}
			claims <- row
		}(conn)
	}
	wg.Wait()
	close(claims)
	count := 0
	for row := range claims {
		if row != nil {
			count++
			if row.DispatchSegment != "s1" || row.DispatchEpoch != 1 {
				t.Fatalf("claim identity missing: %+v", row)
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate claim: %d", count)
	}
	if err := d.BeginConversationSwitch(ctx, "root", "chat", ConversationSwitch{OperationID: "blocked", ExpectedEpoch: 1, SourceSegment: "s1", Request: json.RawMessage(`{}`)}); !errors.Is(err, ErrConversationSwitchBusy) {
		t.Fatalf("unresolved input crossed activation: %v", err)
	}
}
