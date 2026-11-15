package assistant

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

func TestSupervisionPRConflictDedupPersistsAcrossHeadsAndRestart(t *testing.T) {
	st := &memStore{}
	ctx := context.Background()
	now := time.Now()
	url := "https://github.com/example/app/pull/12"
	for i, tc := range []struct {
		todo, url, head string
		want            int
	}{
		{"todo_example", url, "head-a", 1},
		{"todo_example", url, "head-a", 0},
		{"todo_example", url, "head-b", 1},
		{"todo_example", url, "head-a", 0},
		{"todo_other", url, "head-a", 1},
		{"todo_example", "https://github.com/example/app/pull/13", "head-a", 1},
	} {
		// Each pass is a manager restart, beyond the ordinary deduplication window.
		s, _ := newQuietServiceOn(t, st)
		e := prompts.SuperviseEntry{ID: tc.todo, Kind: sigPRConflict, AgentID: fmt.Sprintf("agent-%d", i),
			Last: fmt.Sprintf("Unrelated agent update %d.", i), Ask: "A new question."}
		g := superviseSignal{Todo: tc.todo, Kind: sigPRConflict, Key: tc.url + "@" + tc.head,
			Machine: fmt.Sprintf("machine-%d", i), Session: e.AgentID, To: fmt.Sprintf("conversation-%d", i)}
		out, _, _, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{g}, now.Add(time.Duration(i)*time.Hour))
		if err != nil || len(out) != tc.want {
			t.Fatalf("pass %d (%+v): %d entries, %v; want %d", i, tc, len(out), err, tc.want)
		}
	}
	var history map[string]superviseFingerprint
	if ok, err := st.Setting(ctx, superviseDedupKey, &history); err != nil || !ok || len(history) != 4 {
		t.Fatalf("per-head history = %+v, %v, %v", history, ok, err)
	}
	for key, seen := range history {
		if !seen.Permanent {
			t.Fatalf("conflict fingerprint expires: %s = %+v", key, seen)
		}
	}
}

func TestSupervisionPRConflictDedupKeepsPermanentEntriesDuringOrdinaryExpiry(t *testing.T) {
	st := &memStore{}
	s, _ := newQuietServiceOn(t, st)
	ctx, now := context.Background(), time.Now()
	e := prompts.SuperviseEntry{ID: "todo_example", Last: "No task delivery yet."}
	conflict := superviseSignal{Todo: e.ID, Kind: sigPRConflict, Key: "https://github.com/example/app/pull/12@head-a"}
	idle := superviseSignal{Todo: e.ID, Kind: sigIdle, Key: "turn-a"}
	for _, g := range []superviseSignal{conflict, idle} {
		if _, _, _, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{g}, now); err != nil {
			t.Fatal(err)
		}
	}
	out, _, _, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{idle}, now.Add(time.Hour))
	if err != nil || len(out) != 1 {
		t.Fatalf("ordinary fingerprint stopped expiring: %v, %v", out, err)
	}
	out, _, _, err = s.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{conflict}, now.Add(2*time.Hour))
	if err != nil || len(out) != 0 {
		t.Fatalf("ordinary cleanup expired a permanent conflict: %v, %v", out, err)
	}
}

func TestSupervisionPRConflictReservationRollbackAndFailure(t *testing.T) {
	st := &memStore{}
	s, _ := newQuietServiceOn(t, st)
	ctx, now := context.Background(), time.Now()
	e := prompts.SuperviseEntry{ID: "todo_example", Kind: sigPRConflict}
	g := superviseSignal{Todo: e.ID, Kind: sigPRConflict, Key: "https://github.com/example/app/pull/12@head-a"}
	out, _, rollback, err := s.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{g}, now)
	if err != nil || len(out) != 1 {
		t.Fatalf("first reservation = %v, %v", out, err)
	}
	rollback()
	restarted, _ := newQuietServiceOn(t, st)
	out, _, _, err = restarted.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{g}, now.Add(time.Hour))
	if err != nil || len(out) != 1 {
		t.Fatalf("rolled-back conflict was lost after restart: %v, %v", out, err)
	}
	st.mu.Lock()
	st.failPut = errors.New("synthetic dedup write failure")
	st.mu.Unlock()
	g.Key = "https://github.com/example/app/pull/12@head-b"
	_, kept, _, err := restarted.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{g}, now.Add(2*time.Hour))
	if err == nil || len(kept) != 1 {
		t.Fatalf("unpersisted reservation was accepted: %v, %v", kept, err)
	}
	st.mu.Lock()
	st.failPut = nil
	st.mu.Unlock()
	out, _, _, err = restarted.reserveSupervision(ctx, []prompts.SuperviseEntry{e}, []superviseSignal{g}, now.Add(3*time.Hour))
	if err != nil || len(out) != 1 {
		t.Fatalf("failed reservation poisoned the new head: %v, %v", out, err)
	}
}

func TestSupervisionPRConflictFailedDispatchReleasesReservation(t *testing.T) {
	st := &memStore{}
	st.failKey(relayKey, errors.New("synthetic relay queue read failure"))
	s, f, task := newPRConflictTask(t, st)
	ctx := context.Background()
	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-a", Conflicts: true, CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	g, ok := pendingPRConflict(s, task.ID)
	if !ok {
		t.Fatal("no conflict signal")
	}
	f.mu.Lock()
	f.failAssistant = errors.New("synthetic dispatch failure")
	f.mu.Unlock()
	if s.deliverDigest(ctx, s.settings(ctx), "conv1", []superviseSignal{g}, "", time.Now(), time.Time{}) {
		t.Fatal("failed dispatch and queue admission were accepted")
	}
	st.failKey(relayKey, nil)
	f.mu.Lock()
	f.failAssistant = nil
	f.mu.Unlock()
	if !s.deliverDigest(ctx, s.settings(ctx), "conv1", []superviseSignal{g}, "", time.Now(), time.Time{}) ||
		len(f.notices()) != 1 {
		t.Fatalf("failed dispatch permanently swallowed the conflict: %q", f.notices())
	}
}
