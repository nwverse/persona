package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func openPhoneWriteQueue(t *testing.T) *DB {
	t.Helper()
	d := open(t)
	clear := func() {
		if _, err := d.pool.Exec(context.Background(), `TRUNCATE phone_write_queue`); err != nil {
			t.Error(err)
		}
	}
	clear()
	t.Cleanup(clear)
	return d
}

func TestPhoneWriteQueuePersistence(t *testing.T) {
	d, ctx := openPhoneWriteQueue(t), context.Background()
	for _, id := range []string{"first", "second", "third"} {
		if _, err := d.EnqueuePhoneWrite(ctx, id, "phone_reminders_create", json.RawMessage(fmt.Sprintf(`{"title":%q}`, id))); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := d.ClaimPhoneWrite(ctx, "first")
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if write, err := d.CancelPhoneWrite(ctx, "third"); err != nil || write.State != "cancelled" {
		t.Fatalf("cancel: %+v %v", write, err)
	}
	// A fresh pool represents hub restart without restarting the database server.
	other, err := Open(ctx, os.Getenv("PERSONA_TEST_DATABASE_URL"), HubSet)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.RecoverPhoneWrites(ctx); err != nil {
		t.Fatal(err)
	}
	writes, err := other.PhoneWrites(ctx)
	if err != nil || len(writes) != 3 {
		t.Fatalf("restored: %+v %v", writes, err)
	}
	for i, want := range []struct{ id, state string }{{"first", "uncertain"}, {"second", "queued"}, {"third", "cancelled"}} {
		got := writes[i]
		if got.ID != want.id || got.State != want.state || got.Tool != "phone_reminders_create" || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Errorf("receipt %d: %+v, want %+v", i, got, want)
		}
	}
	if claimed, err := other.ClaimPhoneWrite(ctx, "first"); err != nil || claimed {
		t.Fatalf("uncertain write was reclaimed: %v %v", claimed, err)
	}
	if err := d.FinishPhoneWrite(ctx, "first", "succeeded", json.RawMessage(`{"content":[]}`)); err != nil {
		t.Fatal(err)
	}
	writes, err = other.PhoneWrites(ctx)
	if err != nil || writes[0].State != "uncertain" {
		t.Fatalf("late result replaced uncertainty: %+v %v", writes, err)
	}
}

func TestPhoneWriteQueueDeduplicatesConcurrentJSON(t *testing.T) {
	d, ctx := openPhoneWriteQueue(t), context.Background()
	other, err := Open(ctx, os.Getenv("PERSONA_TEST_DATABASE_URL"), HubSet)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	ids := make(chan string, 24)
	for i := range 24 {
		wg.Go(func() {
			pool, args := d, json.RawMessage(`{"value":1,"nested":{"a":true,"b":[2,3]}}`)
			if i%2 == 1 {
				pool, args = other, json.RawMessage(` { "nested": {"b":[2.0,3e0], "a":true}, "value":1.0 } `)
			}
			write, err := pool.EnqueuePhoneWrite(ctx, fmt.Sprintf("request-%d", i), "phone_health_write_sample", args)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- write.ID
		})
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("duplicates got different IDs: %s and %s", first, id)
		}
	}
	writes, err := d.PhoneWrites(ctx)
	if err != nil || len(writes) != 1 {
		t.Fatalf("deduplicated receipts: %+v %v", writes, err)
	}
	if write, err := other.FindQueuedPhoneWrite(ctx, "phone_health_write_sample", json.RawMessage(`{"nested":{"b":[2.0,3e0],"a":true},"value":1.00}`)); err != nil || write == nil || write.ID != first {
		t.Fatalf("lookup equivalent arguments: %+v %v", write, err)
	}
	for i, args := range []string{`{"value":9007199254740992}`, `{"value":9007199254740993}`} {
		if _, err := d.EnqueuePhoneWrite(ctx, fmt.Sprintf("precise-%d", i), "phone_health_write_sample", json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.EnqueuePhoneWrite(ctx, "other-tool", "phone_reminders_create", writes[0].Args); err != nil {
		t.Fatal(err)
	}
	if claimed, err := d.ClaimPhoneWrite(ctx, first); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if write, err := other.FindQueuedPhoneWrite(ctx, writes[0].Tool, writes[0].Args); err != nil || write != nil {
		t.Fatalf("lookup included delivering write: %+v %v", write, err)
	}
	if write, err := d.EnqueuePhoneWrite(ctx, "new-attempt", writes[0].Tool, writes[0].Args); err != nil || write.ID != "new-attempt" {
		t.Fatalf("delivering write must not deduplicate a new request: %+v %v", write, err)
	}
	writes, err = d.PhoneWrites(ctx)
	if err != nil || len(writes) != 5 {
		t.Fatalf("distinct receipts: %+v %v", writes, err)
	}
}

func TestPhoneWriteQueueTransitions(t *testing.T) {
	for _, state := range []string{"succeeded", "declined", "uncertain", "failed"} {
		t.Run(state, func(t *testing.T) {
			d, ctx := openPhoneWriteQueue(t), context.Background()
			write, err := d.EnqueuePhoneWrite(ctx, "receipt", "phone_reminders_create", json.RawMessage(`{"title":"Example"}`))
			if err != nil {
				t.Fatal(err)
			}
			result := json.RawMessage(`{"content":[{"type":"text","text":"Example result"}]}`)
			if err := d.FinishPhoneWrite(ctx, write.ID, state, result); err != nil {
				t.Fatal(err)
			}
			if claimed, err := d.ClaimPhoneWrite(ctx, write.ID); err != nil || !claimed {
				t.Fatalf("finish changed queued write: %v %v", claimed, err)
			}
			if claimed, err := d.ClaimPhoneWrite(ctx, write.ID); err != nil || claimed {
				t.Fatalf("second claim: %v %v", claimed, err)
			}
			if write, err := d.CancelPhoneWrite(ctx, write.ID); err != nil || write.State != "delivering" {
				t.Fatalf("cancel delivering: %+v %v", write, err)
			}
			if err := d.FinishPhoneWrite(ctx, write.ID, state, result); err != nil {
				t.Fatal(err)
			}
			writes, err := d.PhoneWrites(ctx)
			if err != nil || len(writes) != 1 || writes[0].State != state || !strings.Contains(string(writes[0].Result), "Example result") {
				t.Fatalf("finished receipt: %+v %v", writes, err)
			}
			if got, err := d.CancelPhoneWrite(ctx, write.ID); err != nil || got.State != state {
				t.Fatalf("cancel terminal: %+v %v", got, err)
			}
		})
	}
	t.Run("cancel races claim", func(t *testing.T) {
		d, ctx := openPhoneWriteQueue(t), context.Background()
		for i := range 20 {
			id := fmt.Sprintf("race-%d", i)
			if _, err := d.EnqueuePhoneWrite(ctx, id, "phone_reminders_create", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			start, claim := make(chan struct{}), make(chan bool, 1)
			go func() {
				<-start
				claimed, err := d.ClaimPhoneWrite(ctx, id)
				if err != nil {
					t.Error(err)
				}
				claim <- claimed
			}()
			close(start)
			write, err := d.CancelPhoneWrite(ctx, id)
			claimed := <-claim
			if err != nil || write == nil {
				t.Fatalf("cancel: %+v %v", write, err)
			}
			if (claimed && write.State != "delivering") || (!claimed && write.State != "cancelled") {
				t.Fatalf("claim and cancel disagree: claimed=%v receipt=%+v", claimed, write)
			}
		}
		if write, err := d.CancelPhoneWrite(ctx, "missing"); err != nil || write != nil {
			t.Fatalf("missing receipt: %+v %v", write, err)
		}
	})
}

func TestPhoneWriteQueueBoundsAndRetention(t *testing.T) {
	d, ctx := openPhoneWriteQueue(t), context.Background()
	for i := range PhoneWritePendingLimit {
		if _, err := d.EnqueuePhoneWrite(ctx, fmt.Sprintf("pending-%d", i), "phone_reminders_create", json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.EnqueuePhoneWrite(ctx, "overflow", "phone_reminders_create", json.RawMessage(`{"n":100}`)); !errors.Is(err, ErrPhoneWriteQueueFull) {
		t.Fatalf("overflow: %v", err)
	}
	if write, err := d.EnqueuePhoneWrite(ctx, "duplicate", "phone_reminders_create", json.RawMessage(`{"n":1.0}`)); err != nil || write.ID != "pending-1" {
		t.Fatalf("deduplication at limit: %+v %v", write, err)
	}
	if _, err := d.CancelPhoneWrite(ctx, "pending-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.EnqueuePhoneWrite(ctx, "replacement", "phone_reminders_create", json.RawMessage(`{"n":100}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx, `UPDATE phone_write_queue SET updated_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx, `INSERT INTO phone_write_queue (id, tool, args, state, updated_at)
		SELECT 'history-' || n, 'phone_reminders_create', '{}', 'succeeded', now() - n * interval '1 second'
		FROM generate_series(1, $1::int) n`, PhoneWriteHistoryLimit+5); err != nil {
		t.Fatal(err)
	}
	writes, err := d.PhoneWrites(ctx)
	if err != nil || len(writes) != PhoneWritePendingLimit+PhoneWriteHistoryLimit {
		t.Fatalf("retention size=%d err=%v", len(writes), err)
	}
	for _, write := range writes {
		if write.ID == "pending-0" || write.ID == fmt.Sprintf("history-%d", PhoneWriteHistoryLimit+1) {
			t.Fatalf("old terminal receipt survived: %+v", write)
		}
	}
	if writes[0].ID != "pending-1" || writes[0].State != "queued" {
		t.Fatalf("old pending receipt disappeared: %+v", writes[0])
	}
}

func TestPhoneWriteQueueRejectsInvalidInput(t *testing.T) {
	d, ctx := openPhoneWriteQueue(t), context.Background()
	for _, args := range []string{"null", "[]", "true", "{", `{"value":"` + strings.Repeat("x", PhoneWriteArgsLimit) + `"}`} {
		if _, err := d.EnqueuePhoneWrite(ctx, "invalid", "phone_reminders_create", json.RawMessage(args)); err == nil {
			t.Fatalf("accepted invalid arguments (length %d)", len(args))
		}
		if _, err := d.FindQueuedPhoneWrite(ctx, "phone_reminders_create", json.RawMessage(args)); err == nil {
			t.Fatalf("lookup accepted invalid arguments (length %d)", len(args))
		}
	}
	if err := d.FinishPhoneWrite(ctx, "missing", "queued", nil); err == nil {
		t.Fatal("accepted nonterminal finish state")
	}
	if err := d.FinishPhoneWrite(ctx, "missing", "succeeded", json.RawMessage(`"`+strings.Repeat("x", PhoneWriteResultLimit)+`"`)); err == nil {
		t.Fatal("accepted oversized result")
	}
}
