package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// These tests pin the way out of a batch switch that never activates its target (#15317): its retries are bounded
// from the switch's start, it ends refused with its real reason, and restore_batch_id can cancel it while its
// conversation is still on its saved settings.

// conversationSwitchTestSettle waits until no sweep goroutine is advancing a conversation, so a test can change
// logicalSwitchPrepareLimit without racing one.
func conversationSwitchTestSettle(t *testing.T, s *Service) {
	t.Helper()
	waitFor(t, "segment sweeps to finish", func() bool {
		s.logical.mu.Lock()
		defer s.logical.mu.Unlock()
		return len(s.logical.running) == 0
	})
}

func conversationSwitchTestLimit(t *testing.T, s *Service, limit time.Duration) {
	t.Helper()
	conversationSwitchTestSettle(t, s)
	old := logicalSwitchPrepareLimit
	logicalSwitchPrepareLimit = limit
	t.Cleanup(func() {
		conversationSwitchTestSettle(t, s)
		logicalSwitchPrepareLimit = old
	})
}

// conversationSwitchTestUnknownReceipt makes every preparation of the rig end without a receipt: the start is
// lost on the way back, and every later receipt check fails too.
func conversationSwitchTestUnknownReceipt(r *conversationSwitchTestRig) {
	r.f.answerNode("assistant.segment.prepare", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("fake link reset before the reply")
	})
	r.f.answerNode("assistant.segment.receipt", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("fake receipt unavailable")
	})
}

func conversationSwitchTestStart(t *testing.T, r *conversationSwitchTestRig) (conversationSwitchTestReport, conversationSwitchTestItem) {
	t.Helper()
	r.setTurn(proto.StatusIdle)
	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"conversation_ids":["fresh-chat"],"launcher":"fallback","model":"new-model","effort":"low"}`)
	started := conversationSwitchTestDecode(t, code, raw)
	item := conversationSwitchTestItemFor(t, started, "fresh-chat")
	if item.Outcome != "switching" || item.OperationID == "" {
		t.Fatalf("switch did not start: %+v", started)
	}
	return started, item
}

// conversationSwitchTestWaitSwitch sweeps until fresh-chat's operation reaches phase with an error containing want.
func conversationSwitchTestWaitSwitch(t *testing.T, r *conversationSwitchTestRig, item conversationSwitchTestItem, phase, want string) *db.ConversationSwitch {
	t.Helper()
	var op *db.ConversationSwitch
	waitFor(t, "operation "+phase+" with "+want, func() bool {
		r.s.logicalSweep(context.Background())
		var err error
		op, err = r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID)
		return err == nil && op != nil && op.Phase == phase && strings.Contains(op.Error, want)
	})
	return op
}

func conversationSwitchTestOnSource(t *testing.T, r *conversationSwitchTestRig, item conversationSwitchTestItem, segments int) {
	t.Helper()
	c, err := r.d.LogicalConversation(context.Background(), item.Machine, item.ConversationID)
	if err != nil || c == nil {
		t.Fatalf("logical conversation: %+v %v", c, err)
	}
	a, ok := c.Active()
	if !ok || c.Epoch != item.ExpectedEpoch || c.Switch != nil || a.MachineID != "server" || a.SessionID != "fresh-chat" || len(c.Segments) != segments {
		t.Fatalf("the conversation left its source: %+v", c)
	}
}

// A preparation whose receipt stays unknown keeps retrying within the limit, keeping its latest error; past the limit,
// counted from the switch's start, it ends refused with that error and the conversation stays on its source.
func TestConversationSwitchUnknownReceiptEndsRefusedWithItsReason(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)

	op := conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	created := op.CreatedAt
	for range 3 {
		r.s.logicalSweep(context.Background())
		conversationSwitchTestSettle(t, r.s)
	}
	if report := conversationSwitchTestStatus(t, r.s, started.BatchID); conversationSwitchTestItemFor(t, report, "fresh-chat").Outcome != "switching" {
		t.Fatalf("ended before its limit: %+v", report)
	}
	if op, _ = r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID); !op.CreatedAt.Equal(created) {
		t.Fatalf("retries moved the switch's start: %v then %v", created, op.CreatedAt)
	}

	conversationSwitchTestLimit(t, r.s, time.Nanosecond)
	report := conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "refused"})
	got := conversationSwitchTestItemFor(t, report, "fresh-chat")
	if !strings.Contains(got.Reason, "could not be confirmed") || !strings.Contains(got.Reason, "last error") || !strings.Contains(got.Reason, "fake receipt unavailable") {
		t.Fatalf("refusal does not name its real cause: %q", got.Reason)
	}
	conversationSwitchTestOnSource(t, r, item, 1)
	conversationSwitchTestNoSyntheticMessage(t, r)
}

// A prepared target that cannot take over (its source cannot be retired) ends the same way: the target is abandoned
// and closed, and the source is told it is not retired.
func TestConversationSwitchPreparedTargetEndsAndIsClosed(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	r.f.answerNode("assistant.segment.retire", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("fake retire failure")
	})
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "prepared", "fake retire failure")

	conversationSwitchTestLimit(t, r.s, time.Nanosecond)
	report := conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "refused"})
	if reason := conversationSwitchTestItemFor(t, report, "fresh-chat").Reason; !strings.Contains(reason, "could not take over") || !strings.Contains(reason, "fake retire failure") {
		t.Fatalf("refusal does not name its real cause: %q", reason)
	}
	conversationSwitchTestOnSource(t, r, item, 2)
	waitFor(t, "the abandoned target to close", func() bool {
		r.s.logicalSweep(context.Background())
		for _, call := range conversationSwitchTestAllCalls(r) {
			if call.Method == "session.close" && strings.Contains(string(call.Params), "prepared-chat-1") {
				return true
			}
		}
		return false
	})
	restored := false
	for _, call := range conversationSwitchTestAllCalls(r) {
		restored = restored || call.Method == "assistant.segment.restore" && strings.Contains(string(call.Params), `"fresh-chat"`)
	}
	if !restored {
		t.Fatal("the source was not told it is not retired")
	}
}

// restore_batch_id cancels an item whose switch has not activated, back to its saved settings; the original item ends
// refused, and nothing is prepared again.
func TestConversationSwitchRestoreCancelsAnUnfinishedItem(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	conversationSwitchTestSettle(t, r.s)

	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"restore_batch_id":"`+started.BatchID+`","preview":true}`)
	preview := conversationSwitchTestItemFor(t, conversationSwitchTestDecode(t, code, raw), "fresh-chat")
	if preview.Outcome != "ready" || preview.Target != item.Before {
		t.Fatalf("restore preview of an unfinished item: %+v", preview)
	}
	if op, _ := r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID); op.Phase != "in_doubt" {
		t.Fatalf("preview changed the switch: %+v", op)
	}

	code, raw = conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"restore_batch_id":"`+started.BatchID+`"}`)
	restoring := conversationSwitchTestDecode(t, code, raw)
	got := conversationSwitchTestItemFor(t, restoring, "fresh-chat")
	if restoring.BatchID == "" || restoring.RestoreOf != started.BatchID || got.Outcome != "switched" || got.Target != item.Before || !strings.Contains(got.Reason, "cancelled") {
		t.Fatalf("restore of an unfinished item: %+v", restoring)
	}
	original := conversationSwitchTestItemFor(t, conversationSwitchTestStatus(t, r.s, started.BatchID), "fresh-chat")
	if original.Outcome != "refused" || !strings.Contains(original.Reason, "cancelled by restore batch "+restoring.BatchID) {
		t.Fatalf("original item after the cancel: %+v", original)
	}
	conversationSwitchTestOnSource(t, r, item, 1)
	if again := conversationSwitchTestItemFor(t, conversationSwitchTestStatus(t, r.s, restoring.BatchID), "fresh-chat"); again.Outcome != "switched" {
		t.Fatalf("restore batch moved on: %+v", again)
	}
	if n := len(conversationSwitchTestPreparations(r)); n != 0 {
		t.Fatalf("restore prepared a conversation: %d", n)
	}
}

// An item whose switch activates while the restore waits for confirmation is not cancelled: the re-check after
// confirmation refuses it, and the switch finishes.
func TestConversationSwitchRestoreLeavesASwitchThatActivated(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	conversationSwitchTestSettle(t, r.s)

	confirm := func(string) (bool, string, error) {
		// The receipt is found while the person is deciding, and the switch activates.
		r.f.answerNode("assistant.segment.receipt", func(_ string, raw json.RawMessage) (any, error) {
			model := "new-model"
			r.f.known["late-chat"] = true
			r.f.sessions["server"] = append(r.f.sessions["server"], proto.Session{SessionID: "late-chat", MachineID: "server", Assistant: true, Agent: "codex", Launcher: "fallback", Model: model, RequestedModel: &model, Effort: "low", Status: proto.StatusIdle, State: proto.StateIdle, Rev: 100})
			return map[string]any{"session_id": "late-chat", "prepared": true}, nil
		})
		waitFor(t, "the switch to activate", func() bool {
			r.s.logicalSweep(context.Background())
			c, err := r.d.LogicalConversation(context.Background(), item.Machine, item.ConversationID)
			return err == nil && c != nil && c.Epoch == item.ExpectedEpoch+1
		})
		return true, "", nil
	}
	a := conversationsSwitchArgs{RestoreBatchID: started.BatchID}
	report, outcome, err := r.s.executeConversationSwitches(context.Background(), a, "", r.s.conversationSwitchResolver("server"), confirm)
	if err != nil || outcome != "" {
		t.Fatalf("restore: %v %q", err, outcome)
	}
	if got := conversationSwitchTestItemFor(t, conversationSwitchTestReportFrom(t, report), "fresh-chat"); got.Outcome != "refused" {
		t.Fatalf("an activated switch was cancelled: %+v", report)
	}
	conversationSwitchTestSettle(t, r.s)
	conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "switched"})
	c, err := r.d.LogicalConversation(context.Background(), item.Machine, item.ConversationID)
	if a, ok := c.Active(); err != nil || !ok || a.SessionID != "late-chat" {
		t.Fatalf("the activated switch did not finish: %+v %v", c, err)
	}
}

func conversationSwitchTestReportFrom(t *testing.T, report conversationSwitchReport) conversationSwitchTestReport {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return conversationSwitchTestDecode(t, http.StatusOK, raw)
}
