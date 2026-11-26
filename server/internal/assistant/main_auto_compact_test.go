package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

func newMainAutoCompactTestFixture(t *testing.T, mainID string) (*Service, *fakeHub, *time.Time, func(time.Duration)) {
	t.Helper()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	s.mainAutoCompact.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}
	setSettings(t, s, func(v *Settings) {
		v.MachineID = "server"
		v.Main = mainID
		v.MainMachineID = "server"
	})
	return s, f, &now, advance
}

func makeMainAutoCompactStepItems(fromSeq, toSeq int, ts time.Time) []proto.Item {
	if toSeq < fromSeq {
		return nil
	}
	items := make([]proto.Item, 0, toSeq-fromSeq+1)
	for seq := fromSeq; seq <= toSeq; seq++ {
		role := "assistant"
		if seq%2 == 1 {
			role = "user"
		}
		items = append(items, proto.Item{
			Seq:  seq,
			TS:   ts,
			Role: role,
			Kind: proto.KindText,
			Text: "step",
		})
	}
	return items
}

func TestMainAutoCompactTokenThresholdMath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    Settings
		window int
		want   int
	}{
		{"default 400k caps 1M Gemini window", Settings{}, 1_048_576, 300_000},
		{"default 400k respects smaller 200k window", Settings{}, 200_000, 150_000},
		{"default 400k respects 131k window", Settings{}, 131_072, 98_304},
		{"default 400k applies when model window unknown", Settings{}, 0, 300_000},
		{"main_compact_window=0 follows 1M model window", Settings{MainCompactWindow: ptr(0)}, 1_048_576, 786_432},
		{"main_compact_window=0 follows 200k model window", Settings{MainCompactWindow: ptr(0)}, 200_000, 150_000},
		{"main_compact_window=0 with unknown model window", Settings{MainCompactWindow: ptr(0)}, 0, 0},
		{"custom main_compact_window=240k on 1M model", Settings{MainCompactWindow: ptr(240_000)}, 1_048_576, 180_000},
		{"custom main_compact_window=240k on 200k model", Settings{MainCompactWindow: ptr(240_000)}, 200_000, 150_000},
		{"saved idle_auto_compact_tokens overrides window ratio", Settings{IdleAutoCompactTokens: ptr(210_000)}, 1_048_576, 210_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mainAutoCompactTokenThreshold(tc.cfg, tc.window); got != tc.want {
				t.Fatalf("mainAutoCompactTokenThreshold(%+v, %d) = %d, want %d", tc.cfg, tc.window, got, tc.want)
			}
		})
	}
	if !plausibleMainContext(1_020_000, 1_048_576) {
		t.Fatal("1.02M tokens on a 1,048,576 Gemini window must be plausible")
	}
	if plausibleMainContext(1_700_000, 1_048_576) || plausibleMainContext(1_200_000, 0) {
		t.Fatal("impossible gateway token spike must be rejected")
	}
}

func TestMainAutoCompactTriggersOnIdleTurnEndForAgyAndHonorsSettings(t *testing.T) {
	ctx := context.Background()
	mainID := "agy-11111111-2222-3333-4444-555555555555"
	s, f, _, advance := newMainAutoCompactTestFixture(t, mainID)

	var compactCalls []map[string]any
	var callsMu sync.Mutex
	f.answerNode(DeviceAssistantCompact, func(_ string, raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		callsMu.Lock()
		compactCalls = append(compactCalls, p)
		callsMu.Unlock()
		return map[string]any{"ok": true}, nil
	})
	countCompacts := func() int {
		callsMu.Lock()
		defer callsMu.Unlock()
		return len(compactCalls)
	}

	// Below default 300k threshold (75% of 400k): no compaction.
	f.setSessions("server", proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 20, ContextTokens: 250_000, ContextWindow: 1_048_576,
	})
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if countCompacts() != 0 {
		t.Fatalf("compacted below threshold: %d", countCompacts())
	}

	// Side chat above threshold: never auto-compacted.
	sideID := "agy-99999999-2222-3333-4444-555555555555"
	f.setSessions("server",
		proto.Session{SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy", Status: proto.StatusDone, Rev: 20, ContextTokens: 250_000, ContextWindow: 1_048_576},
		proto.Session{SessionID: sideID, Assistant: true, Agent: "agy", Launcher: "agy", Status: proto.StatusDone, Rev: 40, ContextTokens: 350_000, ContextWindow: 1_048_576},
	)
	s.mainAutoCompactTurnSettled(ctx, "server", sideID)
	if countCompacts() != 0 {
		t.Fatalf("side conversation was compacted: %d", countCompacts())
	}

	// Explicit opt-out via assistant.idle_auto_compact = false skips compaction even over threshold.
	f.setSessions("server", proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 25, ContextTokens: 320_000, ContextWindow: 1_048_576,
	})
	setSettings(t, s, func(v *Settings) { v.IdleAutoCompact = ptr(false) })
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if countCompacts() != 0 {
		t.Fatalf("compacted while idle_auto_compact=false: %d", countCompacts())
	}

	// Re-enable and cross 300k threshold through the real TurnEnded -> afterSettle path.
	setSettings(t, s, func(v *Settings) { v.IdleAutoCompact = nil })
	s.TurnEnded(ctx, SessionRef{MachineID: "server", SessionID: mainID})
	waitFor(t, "main agy session auto-compacted", func() bool { return countCompacts() == 1 })
	callsMu.Lock()
	gotParams := compactCalls[0]
	callsMu.Unlock()
	if gotParams["session_id"] != mainID {
		t.Fatalf("compact session_id = %v, want %s", gotParams["session_id"], mainID)
	}
	if _, hasOnlyRunning := gotParams["only_running"]; hasOnlyRunning {
		t.Fatalf("runtime-compatible auto-compact must not pass only_running: %v", gotParams)
	}

	// Complete the compaction and advance past cooldown.
	started := s.mainAutoCompact.clock()
	s.SessionsChanged(ctx, DeviceSessions{
		MachineID: "server",
		Sessions: []proto.Session{{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 26, ContextTokens: 0, ContextWindow: 1_048_576,
			Compaction: &proto.Compaction{State: proto.CompactionDone, StartedAt: started},
		}},
	})
	advance(defaultMainAutoCompactCooldown + time.Minute)

	// With main_compact_window = 0 (follow 1M model window -> 786,432 threshold), 350k does not trigger; 800k does.
	setSettings(t, s, func(v *Settings) { v.MainCompactWindow = ptr(0) })
	f.setSessions("server", proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 30, ContextTokens: 350_000, ContextWindow: 1_048_576,
	})
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if countCompacts() != 1 {
		t.Fatalf("compacted at 350k with main_compact_window=0 on 1M model: %d", countCompacts())
	}
	f.setSessions("server", proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 35, ContextTokens: 800_000, ContextWindow: 1_048_576,
	})
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if countCompacts() != 2 {
		t.Fatalf("did not compact at 800k on 1M model: %d", countCompacts())
	}
}

func TestMainAutoCompactFallbackStepsBytesAndCompactionBoundary(t *testing.T) {
	ctx := context.Background()

	t.Run("unreported tokens triggers on incremental item volume bytes", func(t *testing.T) {
		mainID := "agy-22222222-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		f.setItems(
			proto.Item{Seq: 1, Role: "user", Kind: proto.KindText, Text: "Analyze logs"},
			proto.Item{Seq: 10, Role: "user", Kind: proto.KindToolResult, Text: "clipped", FullSize: 300 * 1024},
			proto.Item{Seq: 24, Role: "user", Kind: proto.KindToolResult, Text: "clipped", FullSize: 280 * 1024},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 25, ContextTokens: 0, ContextWindow: 1_048_576,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 via fallback byte volume", compacts.Load())
		}
	})

	t.Run("recent compaction notice in items resets baseline and prevents false trigger", func(t *testing.T) {
		mainID := "agy-33333333-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		// High historical Rev and OutputTokens after restart, but CHECKPOINT notice landed at seq 390.
		f.setItems(
			proto.Item{Seq: 300, Role: "user", Kind: proto.KindToolResult, Text: "old huge output", FullSize: 900 * 1024},
			proto.Item{Seq: 390, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 395, Role: "user", Kind: proto.KindText, Text: "Short follow-up"},
			proto.Item{Seq: 400, Role: "assistant", Kind: proto.KindText, Text: "Short reply"},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 400, ContextTokens: 0, OutputTokens: 120_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("compacts = %d, want 0 because recent compaction notice was at seq 390", compacts.Load())
		}
	})

	t.Run("unreported tokens triggers on incremental steps", func(t *testing.T) {
		mainID := "agy-44444444-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		f.setItems(makeMainAutoCompactStepItems(1, 170, time.Time{})...)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 170, ContextTokens: 0,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 via fallback step count", compacts.Load())
		}
	})

	t.Run("stale frozen tokens fall back to step count while fresh tokens stay authoritative", func(t *testing.T) {
		mainID := "agy-55555555-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		// Fresh token update at Rev 180 (120k < 300k): must NOT falsely trigger fallback.
		row := proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 10, ContextTokens: 100_000, ContextWindow: 1_048_576,
		}
		s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{row}})
		row.Rev, row.ContextTokens = 180, 120_000
		f.setSessions("server", row)
		s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{row}})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("fresh below-threshold tokens falsely triggered fallback: %d", compacts.Load())
		}

		// Now tokens stay frozen at 120k while 170 real transcript steps accumulate -> stale fallback triggers.
		row.Rev = 350
		f.setSessions("server", row)
		f.setItems(makeMainAutoCompactStepItems(181, 350, time.Time{})...)
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("stale frozen tokens failed to trigger fallback: %d", compacts.Load())
		}
	})
}

func TestMainAutoCompactDelayedExportAfterTurnEnd(t *testing.T) {
	ctx := context.Background()
	mainID := "agy-66666666-2222-3333-4444-555555555555"
	s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
	var compacts atomic.Int32
	f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
		compacts.Add(1)
		return map[string]any{"ok": true}, nil
	})

	// Turn ends before transcript_full.jsonl flushes: pre-turn tokens are still below threshold.
	stale := proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 40, ContextTokens: 260_000, ContextWindow: 1_048_576,
	}
	f.setSessions("server", stale)
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 0 {
		t.Fatalf("compacted before delayed usage arrived: %d", compacts.Load())
	}

	// Delayed export arrives while still idle with updated context_tokens over threshold.
	flushed := stale
	flushed.Rev = 44
	flushed.ContextTokens = 325_000
	f.setSessions("server", flushed)
	s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{flushed}})
	waitFor(t, "delayed export triggered auto-compaction", func() bool { return compacts.Load() == 1 })
}

func TestMainAutoCompactBusyAndActiveGuards(t *testing.T) {
	ctx := context.Background()
	mainID := "agy-77777777-2222-3333-4444-555555555555"

	base := proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 40, ContextTokens: 360_000, ContextWindow: 1_048_576,
	}
	undelivered := []proto.UndeliveredMessage{{ID: "u1", Text: "pending"}}

	cases := []struct {
		name   string
		mutate func(*Service, *proto.Session)
	}{
		{"working", func(_ *Service, s *proto.Session) { s.Status = proto.StatusWorking }},
		{"blocked", func(_ *Service, s *proto.Session) { s.Status = proto.StatusBlocked }},
		{"offline", func(_ *Service, s *proto.Session) { s.Status = proto.StatusOffline }},
		{"starting", func(_ *Service, s *proto.Session) { s.Starting = true }},
		{"background working", func(_ *Service, s *proto.Session) { s.BackgroundWorking = true }},
		{"background waiting", func(_ *Service, s *proto.Session) { s.BackgroundWaiting = true }},
		{"background task", func(_ *Service, s *proto.Session) { s.Background = []proto.BackgroundTask{{Kind: "shell"}} }},
		{"queued on node", func(_ *Service, s *proto.Session) { s.Queued = []string{"next"} }},
		{"undelivered owner message", func(_ *Service, s *proto.Session) { s.Undelivered = &undelivered }},
		{"already compacting", func(_ *Service, s *proto.Session) {
			s.Compaction = &proto.Compaction{State: proto.CompactionRunning, StartedAt: time.Now().UTC()}
		}},
		{"needs you state", func(_ *Service, s *proto.Session) { s.State = proto.StateNeedsYou }},
		{"pending interaction count", func(_ *Service, s *proto.Session) { s.PendingInteractions = 1 }},
		{"usage limit error", func(_ *Service, s *proto.Session) { s.TurnErrorKind = proto.TurnErrorUsageLimit }},
		{"terminal pane session", func(_ *Service, s *proto.Session) { s.PaneID = "pane-1" }},
		{"queued hub relay", func(svc *Service, _ *proto.Session) {
			svc.queueRelay(mainID, relayMsg{From: "conv2", Tool: "conversation_send", Text: "waiting", At: time.Now().UTC()})
		}},
		{"recent user input after turn end", func(svc *Service, _ *proto.Session) {
			svc.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
			svc.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: mainID})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
			var compacts atomic.Int32
			f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
				compacts.Add(1)
				return map[string]any{"ok": true}, nil
			})
			row := base
			tc.mutate(s, &row)
			f.setSessions("server", row)
			s.mainAutoCompactTurnSettled(ctx, "server", mainID)
			if compacts.Load() != 0 {
				t.Fatalf("case %q triggered compaction %d times, want 0", tc.name, compacts.Load())
			}
		})
	}
}

type compactHookHub struct {
	*fakeHub
	mu            sync.Mutex
	beforeCompact func()
}

func (h *compactHookHub) setBeforeCompact(fn func()) {
	h.mu.Lock()
	h.beforeCompact = fn
	h.mu.Unlock()
}

func (h *compactHookHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceAssistantCompact {
		h.mu.Lock()
		fn := h.beforeCompact
		h.mu.Unlock()
		if fn != nil {
			fn()
		}
	}
	return h.fakeHub.DeviceCall(ctx, p)
}

func TestMainAutoCompactReentrancyCooldownAndRecovery(t *testing.T) {
	ctx := context.Background()
	mainID := "agy-88888888-2222-3333-4444-555555555555"
	f := newFakeHub().withMachine()
	hub := &compactHookHub{fakeHub: f}
	st := &memStore{}
	s := withSettings(t, f, New(Options{Hub: hub, Store: func() Store { return st }, Logf: testLogf(t)}))
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	s.mainAutoCompact.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}
	setSettings(t, s, func(v *Settings) {
		v.MachineID = "server"
		v.Main = mainID
		v.MainMachineID = "server"
	})

	startedRPC := make(chan struct{}, 1)
	releaseRPC := make(chan struct{})
	var blockOnce sync.Once
	hub.setBeforeCompact(func() {
		blockOnce.Do(func() {
			startedRPC <- struct{}{}
			<-releaseRPC
		})
	})

	var rpcErr error
	var rpcMu sync.Mutex
	var compacts atomic.Int32
	f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
		compacts.Add(1)
		rpcMu.Lock()
		err := rpcErr
		rpcMu.Unlock()
		return map[string]any{"ok": true}, err
	})

	row := proto.Session{
		SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
		Status: proto.StatusDone, Rev: 50, ContextTokens: 350_000, ContextWindow: 1_048_576,
	}
	f.setSessions("server", row)

	// 1. Re-entrancy guard: a second concurrent check while the first RPC is in flight is ignored.
	doneFirst := make(chan struct{})
	go func() {
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		close(doneFirst)
	}()
	<-startedRPC
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	close(releaseRPC)
	<-doneFirst
	if compacts.Load() != 1 {
		t.Fatalf("concurrent checks sent %d compacts, want 1", compacts.Load())
	}

	// 2. While waiting for node CompactionDone (still inFlight), another settled check does not re-trigger.
	row.Rev = 52
	row.ContextTokens = 360_000
	f.setSessions("server", row)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 1 {
		t.Fatalf("in-flight compaction re-triggered: %d", compacts.Load())
	}

	// 3. Compaction finishes -> cooldown prevents immediate re-trigger even if tokens exceed threshold again.
	doneStart := s.mainAutoCompact.clock()
	s.SessionsChanged(ctx, DeviceSessions{
		MachineID: "server",
		Sessions: []proto.Session{{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 53, ContextTokens: 0, ContextWindow: 1_048_576,
			Compaction: &proto.Compaction{State: proto.CompactionDone, StartedAt: doneStart},
		}},
	})
	row.Rev = 60
	row.ContextTokens = 340_000
	f.setSessions("server", row)
	advance(5 * time.Minute)
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 1 {
		t.Fatalf("re-triggered inside cooldown window: %d", compacts.Load())
	}

	// 4. After cooldown expires -> busy refusal backs off briefly and allows retry on next turn end.
	advance(defaultMainAutoCompactCooldown)
	rpcMu.Lock()
	rpcErr = &rpc.Error{Code: "busy", Message: "busy"}
	rpcMu.Unlock()
	s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 2 {
		t.Fatalf("expected busy attempt after cooldown: %d", compacts.Load())
	}
	// Inside busy cooldown: skipped.
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 2 {
		t.Fatalf("retried during busy cooldown: %d", compacts.Load())
	}
	// After busy cooldown with error cleared: succeeds even on same Rev watermark because busy cleared it.
	advance(defaultMainAutoCompactBusyCooldown + time.Second)
	rpcMu.Lock()
	rpcErr = nil
	rpcMu.Unlock()
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 3 {
		t.Fatalf("did not retry after busy cooldown expired: %d", compacts.Load())
	}

	// 5. Unknown-method refusal permanently disables auto-compact for this unsupported node/session.
	failStart := s.mainAutoCompact.clock()
	s.SessionsChanged(ctx, DeviceSessions{
		MachineID: "server",
		Sessions: []proto.Session{{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 61, ContextTokens: 0, ContextWindow: 1_048_576,
			Compaction: &proto.Compaction{State: proto.CompactionDone, StartedAt: failStart},
		}},
	})
	advance(defaultMainAutoCompactCooldown + time.Minute)
	rpcMu.Lock()
	rpcErr = errors.New("unknown method assistant.compact")
	rpcMu.Unlock()
	row.Rev = 70
	row.ContextTokens = 350_000
	f.setSessions("server", row)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 4 {
		t.Fatalf("expected unsupported probe attempt: %d", compacts.Load())
	}
	advance(time.Hour)
	row.Rev = 80
	row.ContextTokens = 380_000
	f.setSessions("server", row)
	s.mainAutoCompactTurnSettled(ctx, "server", mainID)
	if compacts.Load() != 4 {
		t.Fatalf("retried after permanent unknown-method refusal: %d", compacts.Load())
	}
}

func TestMainAutoCompactOmittedTokensPreserveBaseline(t *testing.T) {
	ctx := context.Background()

	t.Run("omitted context_tokens keeps lastTokens/peakTokens/baselineSeq intact for primary threshold", func(t *testing.T) {
		mainID := "agy-91111111-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})

		// First plausible reading below threshold.
		row := proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 20, ContextTokens: 220_000, ContextWindow: 1_048_576, OutputTokens: 10_000,
		}
		f.setSessions("server", row)
		s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{row}})

		// Ordinary usage-stripped idle snapshot omits ContextTokens (0): must NOT reset baseline or clear lastTokens/peakTokens.
		stripped := row
		stripped.Rev = 22
		stripped.ContextTokens = 0
		stripped.ContextWindow = 0
		f.setSessions("server", stripped)
		s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{stripped}})

		ref := SessionRef{MachineID: "server", SessionID: mainID}
		s.mainAutoCompact.mu.Lock()
		st := s.mainAutoCompact.bySession[ref]
		gotBase, gotLast, gotPeak, gotChange := st.baselineSeq, st.lastTokens, st.peakTokens, st.lastTokenChangeRev
		s.mainAutoCompact.mu.Unlock()
		if gotBase != 0 || gotLast != 220_000 || gotPeak != 220_000 || gotChange != 20 {
			t.Fatalf("omitted snapshot clobbered state: baselineSeq=%d lastTokens=%d peakTokens=%d lastTokenChangeRev=%d",
				gotBase, gotLast, gotPeak, gotChange)
		}

		// Subsequent plausible reading crossing 75% (300k) triggers primary threshold.
		row.Rev = 25
		row.ContextTokens = 320_000
		f.setSessions("server", row)
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 after omitted snapshot followed by growth", compacts.Load())
		}
	})

	t.Run("over-threshold omitted snapshot still triggers without compaction notice, and suppresses with explicit notice", func(t *testing.T) {
		// Case A: no compaction notice in items -> triggers via retained lastTokens >= 300k.
		mainID := "agy-92222222-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		high := proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusWorking, Rev: 40, ContextTokens: 320_000, ContextWindow: 1_048_576,
		}
		s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{high}})
		f.setItems(proto.Item{Seq: 41, Role: "assistant", Kind: proto.KindText, Text: "Done without usage"})
		omitted := proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 41, ContextTokens: 0, ContextWindow: 0,
		}
		f.setSessions("server", omitted)
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("omitted snapshot without compaction notice triggered %d times, want 1", compacts.Load())
		}

		// Case B: explicit compaction notice at seq 41 clears retained tokens and prevents duplicate trigger.
		mainID2 := "agy-93333333-2222-3333-4444-555555555555"
		s2, f2, _, _ := newMainAutoCompactTestFixture(t, mainID2)
		var compacts2 atomic.Int32
		f2.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts2.Add(1)
			return map[string]any{"ok": true}, nil
		})
		high2 := high
		high2.SessionID = mainID2
		s2.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{high2}})
		f2.setItems(proto.Item{Seq: 41, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice})
		omitted2 := omitted
		omitted2.SessionID = mainID2
		f2.setSessions("server", omitted2)
		s2.noteMainAutoCompactTurnEnded(ctx, "server", mainID2)
		s2.mainAutoCompactTurnSettled(ctx, "server", mainID2)
		if compacts2.Load() != 0 {
			t.Fatalf("omitted snapshot with explicit compaction notice triggered %d times, want 0", compacts2.Load())
		}
	})

	t.Run("repeated omitted snapshots never advance baselineSeq to mask fallback step growth", func(t *testing.T) {
		mainID := "agy-94444444-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		row := proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 20, ContextTokens: 180_000, ContextWindow: 1_048_576,
		}
		s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{row}})
		for _, rev := range []int64{50, 100, 150} {
			stripped := row
			stripped.Rev = rev
			stripped.ContextTokens = 0
			s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{stripped}})
		}
		final := row
		final.Rev = 170
		final.ContextTokens = 0
		f.setSessions("server", final)
		f.setItems(makeMainAutoCompactStepItems(1, 170, time.Time{})...)
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("repeated omitted snapshots masked fallback growth: compacts = %d, want 1", compacts.Load())
		}
	})
}

func TestMainAutoCompactCompactionNoticePreservesPostBoundaryOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("reconstructs output baseline from post-boundary item usage and deduplicates usage IDs", func(t *testing.T) {
		mainID := "agy-95555555-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})

		// Session has cumulative 150k output; compaction notice at seq 60 is followed by 50k post-boundary output
		// (30k repeated under the same usage ID + 20k inside a folded run).
		f.setItems(
			proto.Item{Seq: 50, Role: "assistant", Kind: proto.KindText, Text: "Old reply", Usage: []proto.TokenUsage{{Model: "gemini-2.5-pro", Output: 90_000}}},
			proto.Item{Seq: 60, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 80, Role: "assistant", Kind: proto.KindText, Text: "Post 1", Usage: []proto.TokenUsage{{ID: "req-1", Model: "gemini-2.5-pro", Output: 30_000}}},
			proto.Item{Seq: 81, Role: "assistant", Kind: proto.KindText, Text: "Post 1 dup", Usage: []proto.TokenUsage{{ID: "req-1", Model: "gemini-2.5-pro", Output: 30_000}}},
			proto.Item{Seq: 120, Role: "assistant", Kind: proto.KindFold, Fold: &proto.Fold{
				Items: 3, Usage: []proto.TokenUsage{{Model: "gemini-2.5-pro", Output: 20_000}},
			}},
		)
		row := proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 120, ContextTokens: 0, OutputTokens: 150_000,
		}
		f.setSessions("server", row)
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("compacted early at 50k post-boundary output: %d", compacts.Load())
		}

		// Baseline must be 150k - 50k = 100k, NOT 150k, so adding 35k more output (total 85k post-compaction) triggers.
		ref := SessionRef{MachineID: "server", SessionID: mainID}
		s.mainAutoCompact.mu.Lock()
		gotBaseOut := s.mainAutoCompact.bySession[ref].baselineOutputTokens
		s.mainAutoCompact.mu.Unlock()
		if gotBaseOut != 100_000 {
			t.Fatalf("baselineOutputTokens = %d, want 100000", gotBaseOut)
		}

		row.Rev = 130
		row.OutputTokens = 185_000
		f.setSessions("server", row)
		f.setItems(
			proto.Item{Seq: 60, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 80, Role: "assistant", Kind: proto.KindText, Text: "Post 1", Usage: []proto.TokenUsage{{ID: "req-1", Model: "gemini-2.5-pro", Output: 30_000}}},
			proto.Item{Seq: 120, Role: "assistant", Kind: proto.KindFold, Fold: &proto.Fold{
				Items: 3, Usage: []proto.TokenUsage{{Model: "gemini-2.5-pro", Output: 20_000}},
			}},
			proto.Item{Seq: 130, Role: "assistant", Kind: proto.KindText, Text: "Post 2", Usage: []proto.TokenUsage{{Model: "gemini-2.5-pro", Output: 35_000}}},
		)
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 once post-boundary output reached 85k", compacts.Load())
		}
	})

	t.Run("triggers immediately when probed items after compaction notice already exceed 80k output", func(t *testing.T) {
		mainID := "agy-96666666-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		f.setItems(
			proto.Item{Seq: 60, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 90, Role: "assistant", Kind: proto.KindText, Text: "Big post-compact reply 1", Usage: []proto.TokenUsage{{Model: "gemini-2.5-pro", Output: 45_000}}},
			proto.Item{Seq: 120, Role: "assistant", Kind: proto.KindText, Text: "Big post-compact reply 2", Usage: []proto.TokenUsage{{Model: "gemini-2.5-pro", Output: 40_000}}},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 120, ContextTokens: 0, OutputTokens: 150_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 immediately on 85k post-boundary output", compacts.Load())
		}
	})

	t.Run("without per-item usage, boundary discovery subtracts only output observed before the boundary", func(t *testing.T) {
		// Case A: timestamp attribution works even when Session.Rev is byte-sized (4096 -> 9000 -> 140000).
		mainID := "claude-97777777-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		for _, snap := range []proto.Session{
			{SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude", Status: proto.StatusDone, Rev: 4096, UpdatedAt: t0, OutputTokens: 20_000},
			{SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude", Status: proto.StatusDone, Rev: 9000, UpdatedAt: t0.Add(5 * time.Minute), OutputTokens: 30_000},
		} {
			s.SessionsChanged(ctx, DeviceSessions{MachineID: "server", Sessions: []proto.Session{snap}})
		}

		// Compaction notice lands at seq 26 (t0+6m); 75k post-boundary output (105k total) stays below 80k threshold.
		f.setItems(
			proto.Item{Seq: 26, TS: t0.Add(6 * time.Minute), Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 27, TS: t0.Add(10 * time.Minute), Role: "assistant", Kind: proto.KindText, Text: "Reply A"},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
			Status: proto.StatusDone, Rev: 120_000, UpdatedAt: t0.Add(10 * time.Minute), ContextTokens: 0, OutputTokens: 105_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("compacts = %d, want 0 at 75k post-boundary output", compacts.Load())
		}
		ref := SessionRef{MachineID: "server", SessionID: mainID}
		s.mainAutoCompact.mu.Lock()
		gotBaseOut := s.mainAutoCompact.bySession[ref].baselineOutputTokens
		s.mainAutoCompact.mu.Unlock()
		if gotBaseOut != 30_000 {
			t.Fatalf("baselineOutputTokens = %d, want 30000 from pre-boundary timestamp sample", gotBaseOut)
		}

		// Adding 10k more output (85k post-boundary) crosses 80k and triggers.
		f.setItems(
			proto.Item{Seq: 26, TS: t0.Add(6 * time.Minute), Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 27, TS: t0.Add(10 * time.Minute), Role: "assistant", Kind: proto.KindText, Text: "Reply A"},
			proto.Item{Seq: 28, TS: t0.Add(15 * time.Minute), Role: "assistant", Kind: proto.KindText, Text: "Reply B"},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
			Status: proto.StatusDone, Rev: 140_000, UpdatedAt: t0.Add(15 * time.Minute), ContextTokens: 0, OutputTokens: 115_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 once post-boundary output reached 85k", compacts.Load())
		}

		// Case B: probe-recorded Item.Seq output attribution works even when Item.TS is zero.
		mainID2 := "agy-98888888-2222-3333-4444-555555555555"
		s2, f2, _, _ := newMainAutoCompactTestFixture(t, mainID2)
		var compacts2 atomic.Int32
		f2.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts2.Add(1)
			return map[string]any{"ok": true}, nil
		})
		// Initial probe records (seq=25, output=30_000) in seqOutputHistory without needing timestamps.
		f2.setItems(proto.Item{Seq: 25, Role: "assistant", Kind: proto.KindText, Text: "Before compact"})
		f2.setSessions("server", proto.Session{
			SessionID: mainID2, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 9000, ContextTokens: 0, OutputTokens: 30_000,
		})
		s2.noteMainAutoCompactTurnEnded(ctx, "server", mainID2)
		s2.mainAutoCompactTurnSettled(ctx, "server", mainID2)
		if compacts2.Load() != 0 {
			t.Fatalf("pre-compact probe compacted unexpectedly: %d", compacts2.Load())
		}
		// Compaction notice lands at seq 26 (zero TS), followed by 85k more output.
		f2.setItems(
			proto.Item{Seq: 26, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 27, Role: "assistant", Kind: proto.KindText, Text: "After compact"},
		)
		f2.setSessions("server", proto.Session{
			SessionID: mainID2, Assistant: true, Agent: "agy", Launcher: "agy",
			Status: proto.StatusDone, Rev: 140_000, ContextTokens: 0, OutputTokens: 115_000,
		})
		s2.noteMainAutoCompactTurnEnded(ctx, "server", mainID2)
		s2.mainAutoCompactTurnSettled(ctx, "server", mainID2)
		if compacts2.Load() != 1 {
			t.Fatalf("compacts2 = %d, want 1 via seq-attributed 85k post-boundary output", compacts2.Load())
		}
	})
}

func TestMainAutoCompactByteSizedRevDoesNotFalseTrigger(t *testing.T) {
	ctx := context.Background()

	t.Run("native Claude byte-sized Rev growth with missing context_tokens does not false-trigger on small turns", func(t *testing.T) {
		mainID := "claude-10000001-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})

		// Initial transcript is 4096 bytes; ordinary small turn appends 2404 bytes (Rev 4096 -> 6500) with only 2 items.
		f.setItems(
			proto.Item{Seq: 1, Role: "user", Kind: proto.KindText, Text: "What is 2+2?"},
			proto.Item{Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "4"},
		)
		s.SessionsChanged(ctx, DeviceSessions{
			MachineID: "server",
			Sessions: []proto.Session{{
				SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
				Status: proto.StatusDone, Rev: 4096, ContextTokens: 0,
			}},
		})
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
			Status: proto.StatusDone, Rev: 6500, ContextTokens: 0,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("byte-sized Rev delta (4096->6500) false-triggered compaction: %d", compacts.Load())
		}

		// Another small append (Rev 6500 -> 9000, 4 total items): still must not trigger.
		f.setItems(
			proto.Item{Seq: 1, Role: "user", Kind: proto.KindText, Text: "What is 2+2?"},
			proto.Item{Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "4"},
			proto.Item{Seq: 3, Role: "user", Kind: proto.KindText, Text: "And 3+3?"},
			proto.Item{Seq: 4, Role: "assistant", Kind: proto.KindText, Text: "6"},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
			Status: proto.StatusDone, Rev: 9000, ContextTokens: 0,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("second byte-sized Rev delta (6500->9000) false-triggered compaction: %d", compacts.Load())
		}
	})

	t.Run("native Codex byte-sized Rev with frozen below-threshold context_tokens does not false-trigger", func(t *testing.T) {
		mainID := "codex-10000002-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

		// Fresh below-threshold reading at Rev 4096 (90k < 300k), then small append to Rev 6500 with identical 90k tokens.
		s.SessionsChanged(ctx, DeviceSessions{
			MachineID: "server",
			Sessions: []proto.Session{{
				SessionID: mainID, Assistant: true, Agent: "codex", Launcher: "codex",
				Status: proto.StatusDone, Rev: 4096, UpdatedAt: t0, ContextTokens: 90_000, ContextWindow: 400_000,
			}},
		})
		// Even with 170 pre-freeze items (at t0), only 2 items arrived after t0 -> must NOT trigger.
		items := makeMainAutoCompactStepItems(1, 170, t0)
		items = append(items,
			proto.Item{Seq: 171, TS: t0.Add(2 * time.Minute), Role: "user", Kind: proto.KindText, Text: "Quick ping"},
			proto.Item{Seq: 172, TS: t0.Add(2 * time.Minute), Role: "assistant", Kind: proto.KindText, Text: "Pong"},
		)
		f.setItems(items...)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "codex", Launcher: "codex",
			Status: proto.StatusDone, Rev: 6500, UpdatedAt: t0.Add(2 * time.Minute), ContextTokens: 90_000, ContextWindow: 400_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("frozen tokens with 2 new items and byte-sized Rev (4096->6500) false-triggered: %d", compacts.Load())
		}

		// Once 165 genuine post-freeze items accumulate while tokens stay frozen at 90k -> triggers.
		postFreeze := makeMainAutoCompactStepItems(171, 335, t0.Add(5*time.Minute))
		f.setItems(postFreeze...)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "codex", Launcher: "codex",
			Status: proto.StatusDone, Rev: 95_000, UpdatedAt: t0.Add(10 * time.Minute), ContextTokens: 90_000, ContextWindow: 400_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 after 165 genuine post-freeze steps", compacts.Load())
		}
	})

	t.Run("byte-sized Rev compaction notice clears retained over-threshold tokens without comparing Seq to Rev", func(t *testing.T) {
		mainID := "claude-10000003-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		// High pre-compaction context at large byte Rev 40000, then compaction notice at small Item.Seq 20.
		s.SessionsChanged(ctx, DeviceSessions{
			MachineID: "server",
			Sessions: []proto.Session{{
				SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
				Status: proto.StatusWorking, Rev: 40_000, ContextTokens: 320_000, ContextWindow: 400_000,
			}},
		})
		f.setItems(
			proto.Item{Seq: 20, Role: "system", Kind: proto.KindNotice, Text: transcript.CompactedNotice},
			proto.Item{Seq: 21, Role: "user", Kind: proto.KindText, Text: "Hi"},
			proto.Item{Seq: 22, Role: "assistant", Kind: proto.KindText, Text: "Hello"},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
			Status: proto.StatusDone, Rev: 42_000, ContextTokens: 0, ContextWindow: 400_000,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 0 {
			t.Fatalf("compaction notice at Seq 20 under Rev 40000 failed to suppress duplicate compact: %d", compacts.Load())
		}
	})

	t.Run("byte-sized Rev still triggers on real 160+ steps (including folded runs) and 512 KiB item bytes", func(t *testing.T) {
		// Case A: real 160+ steps including a KindFold run.
		mainID := "claude-10000004-2222-3333-4444-555555555555"
		s, f, _, _ := newMainAutoCompactTestFixture(t, mainID)
		var compacts atomic.Int32
		f.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts.Add(1)
			return map[string]any{"ok": true}, nil
		})
		f.setItems(
			proto.Item{Seq: 1, Role: "user", Kind: proto.KindText, Text: "Run build"},
			proto.Item{Seq: 2, Role: "assistant", Kind: proto.KindFold, Fold: &proto.Fold{Items: 160, ThroughSeq: 161}},
			proto.Item{Seq: 162, Role: "assistant", Kind: proto.KindText, Text: "Done"},
		)
		f.setSessions("server", proto.Session{
			SessionID: mainID, Assistant: true, Agent: "claude", Launcher: "claude",
			Status: proto.StatusDone, Rev: 85_000, ContextTokens: 0,
		})
		s.noteMainAutoCompactTurnEnded(ctx, "server", mainID)
		s.mainAutoCompactTurnSettled(ctx, "server", mainID)
		if compacts.Load() != 1 {
			t.Fatalf("compacts = %d, want 1 via 162 real post-compact steps (including fold)", compacts.Load())
		}

		// Case B: real 512 KiB uncompacted item bytes with small step count.
		mainID2 := "codex-10000005-2222-3333-4444-555555555555"
		s2, f2, _, _ := newMainAutoCompactTestFixture(t, mainID2)
		var compacts2 atomic.Int32
		f2.answerNode(DeviceAssistantCompact, func(string, json.RawMessage) (any, error) {
			compacts2.Add(1)
			return map[string]any{"ok": true}, nil
		})
		f2.setItems(
			proto.Item{Seq: 1, Role: "user", Kind: proto.KindText, Text: "Inspect dumps"},
			proto.Item{Seq: 2, Role: "user", Kind: proto.KindToolResult, Text: "clipped", FullSize: 300 * 1024},
			proto.Item{Seq: 3, Role: "user", Kind: proto.KindToolResult, Text: "clipped", FullSize: 280 * 1024},
		)
		f2.setSessions("server", proto.Session{
			SessionID: mainID2, Assistant: true, Agent: "codex", Launcher: "codex",
			Status: proto.StatusDone, Rev: 620_000, ContextTokens: 0,
		})
		s2.noteMainAutoCompactTurnEnded(ctx, "server", mainID2)
		s2.mainAutoCompactTurnSettled(ctx, "server", mainID2)
		if compacts2.Load() != 1 {
			t.Fatalf("compacts2 = %d, want 1 via 580 KiB item volume", compacts2.Load())
		}
	})
}
