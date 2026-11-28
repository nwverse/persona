package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// These tests reload the approval journal and interaction rows from a real
// Postgres database into a fresh manager Service. They skip without
// PERSONA_TEST_DATABASE_URL; scripts/ci/server.sh provisions Postgres 18.

func pgJournalEntries(t *testing.T, database *db.DB) []approvalJournalEntry {
	t.Helper()
	var list []approvalJournalEntry
	if _, err := database.Setting(context.Background(), approvalJournalKey, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

// restartedOnPostgres builds a fresh manager over the same database and fake
// hub/devices, so every journal and interaction read reloads from Postgres.
func restartedOnPostgres(t *testing.T, ft *fleetTest, database *db.DB) *Service {
	t.Helper()
	return newRestartedService(t, Options{
		Hub:     ft.f,
		Store:   func() Store { return database },
		Logf:    testLogf(t),
		Devices: ft.d,
	})
}

func testDetachedPostgresKeepPending(t *testing.T, order string) {
	t.Helper()
	database := todoTestDB(t) // truncates, so no prior subtest's pending rows restore
	ctx := context.Background()
	ft := newFleetTestOn(t, database)
	preparedTestConversation(ft)
	ix := detachedTestCard("ix_pg_keep_" + order)
	ft.s.record(ix)
	if err := ft.s.approvalJournalStarted(ctx, ix); err != nil {
		t.Fatal(err)
	}

	restarted := restartedOnPostgres(t, ft, database)
	if order == "restore_first" {
		restarted.restoreInteractions(ctx)
		restarted.recoverApprovalJournals(ctx)
	} else {
		restarted.recoverApprovalJournals(ctx)
		restarted.restoreInteractions(ctx)
	}

	if got := len(restarted.pendingInteractions()); got != 1 {
		t.Fatalf("pending cards after %s = %d, want 1", order, got)
	}
	status, _, err := database.InteractionOutcome(ctx, ix.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("interaction status after %s = %q, want pending", order, status)
	}
	for _, e := range pgJournalEntries(t, database) {
		if e.ID == ix.ID && e.Result != "" {
			t.Fatalf("journal terminalized after %s: %+v", order, e)
		}
	}
}

func TestDetachedApprovalPostgresRestartKeepsPendingRestoreFirst(t *testing.T) {
	testDetachedPostgresKeepPending(t, "restore_first")
}

func TestDetachedApprovalPostgresRestartKeepsPendingRecoverFirst(t *testing.T) {
	testDetachedPostgresKeepPending(t, "recover_first")
}

func TestDetachedApprovalPostgresRestartAllowOneShotAcrossAnotherRestart(t *testing.T) {
	database := todoTestDB(t)
	ctx := context.Background()
	ft := newFleetTestOn(t, database)
	preparedTestConversation(ft)
	ix := detachedTestCard("ix_pg_allow_oneshot")
	ft.s.record(ix)
	if err := ft.s.approvalJournalStarted(ctx, ix); err != nil {
		t.Fatal(err)
	}

	first := restartedOnPostgres(t, ft, database)
	first.restoreInteractions(ctx)
	first.recoverApprovalJournals(ctx)
	if got := len(first.pendingInteractions()); got != 1 {
		t.Fatalf("restored pending = %d, want 1", got)
	}

	allow := true
	if err := first.resolve(ix.ID, proto.Resolution{Allow: &allow}); err != nil {
		t.Fatalf("first owner Allow failed: %v", err)
	}
	// A duplicate click must not mint a second decision or grant. A repeated
	// claim returns the first answer; either way only one grant is consumable.
	_ = first.resolve(ix.ID, proto.Resolution{Allow: &allow})
	if result := waitFakeApprovalResult(t, ft.f, ix.ID); !strings.Contains(result, "approved") {
		t.Fatalf("Allow result = %q, want approved continuation", result)
	}

	// Another manager restart reloads the approved journal and its grant from
	// Postgres; the exact-input retry still consumes the grant exactly once.
	again := restartedOnPostgres(t, ft, database)
	retry := ix
	retry.ID = ix.ID + "_retry"
	got, ok, err := again.consumeDetachedGrant(ctx, retry)
	if err != nil || !ok || got.Allow == nil || !*got.Allow || got.ApprovalID != ix.ID {
		t.Fatalf("exact retry across restart = %+v ok=%v err=%v", got, ok, err)
	}
	second := ix
	second.ID = ix.ID + "_second_retry"
	if got, ok, err := again.consumeDetachedGrant(ctx, second); err != nil || ok || got.Allow != nil {
		t.Fatalf("second retry consumed the one-time grant: %+v ok=%v err=%v", got, ok, err)
	}
	changed := ix
	changed.ID = ix.ID + "_changed"
	changed.ToolInput = []byte(`{"command":"printf different"}`)
	if got, ok, err := again.consumeDetachedGrant(ctx, changed); err != nil || ok || got.Allow != nil {
		t.Fatalf("changed input consumed the approval: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestDetachedApprovalPostgresRestartDenyCreatesNoGrant(t *testing.T) {
	database := todoTestDB(t)
	ctx := context.Background()
	ft := newFleetTestOn(t, database)
	preparedTestConversation(ft)
	ix := detachedTestCard("ix_pg_deny")
	ft.s.record(ix)
	if err := ft.s.approvalJournalStarted(ctx, ix); err != nil {
		t.Fatal(err)
	}

	restarted := restartedOnPostgres(t, ft, database)
	restarted.restoreInteractions(ctx)
	restarted.recoverApprovalJournals(ctx)

	deny := false
	if err := restarted.resolve(ix.ID, proto.Resolution{Allow: &deny}); err != nil {
		t.Fatalf("owner Deny failed: %v", err)
	}
	if result := waitFakeApprovalResult(t, ft.f, ix.ID); !strings.Contains(result, "denied") {
		t.Fatalf("Deny result = %q, want denied", result)
	}
	retry := ix
	retry.ID = ix.ID + "_retry"
	if got, ok, err := restarted.consumeDetachedGrant(ctx, retry); err != nil || ok || got.Allow != nil {
		t.Fatalf("Deny created a continuation grant: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestDetachedApprovalPostgresRestartApplyingStaysUncertain(t *testing.T) {
	database := todoTestDB(t)
	ctx := context.Background()
	ft := newFleetTestOn(t, database)
	preparedTestConversation(ft)
	ix := detachedTestCard("ix_pg_applying")
	ft.s.record(ix)
	_, fingerprint, err := approvalJournalOccurrence(ix)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PutSetting(ctx, approvalJournalKey, []approvalJournalEntry{{
		ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID,
		CreatedAt: ix.CreatedAt, Fingerprint: fingerprint, Status: "applying",
		UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	restarted := restartedOnPostgres(t, ft, database)
	restarted.recoverApprovalJournals(ctx)
	if result := waitFakeApprovalResult(t, ft.f, ix.ID); !strings.Contains(result, "uncertain") {
		t.Fatalf("applying recovery = %q, want explicit uncertain outcome", result)
	}
	for _, e := range pgJournalEntries(t, database) {
		if e.ID == ix.ID && e.Status != "failed" {
			t.Fatalf("applying entry status = %q, want failed/uncertain", e.Status)
		}
	}
}

func TestDetachedApprovalPostgresRestartNonResumableCancels(t *testing.T) {
	database := todoTestDB(t)
	ctx := context.Background()
	ft := newFleetTestOn(t, database)
	preparedTestConversation(ft)
	ix := detachedTestCard("ix_pg_nonresumable")
	ix.Resumable = false
	ft.s.record(ix)
	if err := ft.s.approvalJournalStarted(ctx, ix); err != nil {
		t.Fatal(err)
	}

	restarted := restartedOnPostgres(t, ft, database)
	restarted.recoverApprovalJournals(ctx)
	if got := len(restarted.pendingInteractions()); got != 0 {
		t.Fatalf("non-resumable restored pending = %d, want 0", got)
	}
	if result := waitFakeApprovalResult(t, ft.f, ix.ID); !strings.Contains(result, "cancelled") {
		t.Fatalf("non-resumable recovery = %q, want cancellation", result)
	}
	status, _, err := database.InteractionOutcome(ctx, ix.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("non-resumable outcome = %q, want cancelled", status)
	}
	retry := ix
	retry.ID = ix.ID + "_retry"
	if got, ok, err := restarted.consumeDetachedGrant(ctx, retry); err != nil || ok || got.Allow != nil {
		t.Fatalf("cancelled non-resumable entry created a grant: %+v ok=%v err=%v", got, ok, err)
	}
}
