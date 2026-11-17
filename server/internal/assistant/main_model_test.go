package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type mainModelHub struct {
	Hub
	beforeStart func() error
	mu          sync.Mutex
	starts      int
	closed      []string
}

func (h *mainModelHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	switch p.Method {
	case DeviceAssistantHandoff:
		return json.RawMessage(`{"summary":"Keep the acceptance criteria","recent":[{"role":"user","text":"Ship the fix"}]}`), nil
	case assistantwire.DeviceAssistantStartPrepared:
		h.mu.Lock()
		h.starts++
		h.mu.Unlock()
		if h.beforeStart != nil {
			if err := h.beforeStart(); err != nil {
				return nil, err
			}
		}
		p.Method = DeviceAssistantSend // reuse the fake's new session IDs, never an actual turn
	case "session.close":
		var in struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(p.Params, &in)
		h.mu.Lock()
		h.closed = append(h.closed, in.SessionID)
		h.mu.Unlock()
		return json.RawMessage(`{}`), nil
	}
	return h.Hub.DeviceCall(ctx, p)
}

func mainModelFixture(t *testing.T, agent, launcher string) (*Service, *fakeHub, *mainModelHub) {
	t.Helper()
	s, f := pickFixture(t)
	f.setSessions("server", proto.Session{SessionID: "main", MachineID: "server", Assistant: true, Agent: agent, Launcher: launcher, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.BackgroundSupervisor = "background"; v.BackgroundSupervisorMachineID = "server" })
	h := &mainModelHub{Hub: f}
	setHub(s, h)
	return s, f, h
}

func TestMainModelHandoffCrossesRuntimesWithoutASummaryTurn(t *testing.T) {
	for _, tc := range []struct{ from, launcher, target string }{
		{"claude", "gw", "cx"}, {"codex", "cx", "claude"}, {"agy", "agy-rt", "claude"}, {"claude", "gw", "agy-rt"},
	} {
		t.Run(tc.from+"-"+tc.target, func(t *testing.T) {
			ctx := context.Background()
			s, f, h := mainModelFixture(t, tc.from, tc.launcher)
			h.beforeStart = func() error {
				if s.settings(ctx).Main != "main" {
					t.Fatal("source changed before target startup")
				}
				return nil
			}
			out, err := s.switchMainModel(ctx, "main", proto.ModelPick{Launcher: tc.target, Effort: "high"})
			if err != nil {
				t.Fatal(err)
			}
			if out.Replaced != "main" || !out.Carried || out.Session.SessionID == "main" || h.starts != 1 {
				t.Fatalf("handoff: %+v", out)
			}
			cfg := s.settings(ctx)
			if cfg.Main != out.Session.SessionID || cfg.BackgroundSupervisor != "background" || len(cfg.Former) != 1 {
				t.Fatalf("settings: %+v", cfg)
			}
			if s.isArchived("main") {
				t.Fatal("old transcript disappeared from history")
			}
			if tc.from == "agy" {
				if len(h.closed) != 0 {
					t.Fatal("runtime close can archive the old history")
				}
			} else if len(h.closed) != 1 || h.closed[0] != "main" {
				t.Fatal("the idle native source process stayed running")
			}
			p := f.lastSend(t)
			if p["text"] != "" || p["launcher"] != tc.target {
				t.Fatalf("a turn was sent while switching: %v", p)
			}
			if pick, ok := s.modelPickOf(ctx, "server", cfg.Main); !ok || pick.Launcher != tc.target || pick.Effort != "high" {
				t.Fatalf("pick: %+v %v", pick, ok)
			}
			// Read persisted state with empty caches, as after a manager restart.
			restarted := New(Options{Hub: f, Store: s.store})
			if restarted.settings(ctx).Main != cfg.Main || restarted.loadHandoff(ctx).For != cfg.Main {
				t.Fatal("handoff did not survive restart")
			}
			if first := s.handoffFor(ctx, cfg.Main, "Continue"); !strings.Contains(first, "Keep the acceptance criteria") || !strings.Contains(first, "Ship the fix") {
				t.Fatalf("context: %q", first)
			}
			if again := s.handoffFor(ctx, cfg.Main, "Next"); again != "Next" {
				t.Fatal("context replayed twice")
			}
		})
	}
}

func TestMainModelSameRuntimeKeepsTheConversation(t *testing.T) {
	s, _, h := mainModelFixture(t, "claude", "gw")
	out, err := s.switchMainModel(context.Background(), "main", proto.ModelPick{Launcher: "claude", Model: "opus"})
	if err != nil || out.Session.SessionID != "main" || out.Replaced != "" || h.starts != 0 {
		t.Fatalf("same engine: %+v %v", out, err)
	}
}

func TestMainModelHandoffPostgresCommitSurvivesManagerRestart(t *testing.T) {
	database := todoTestDB(t)
	s, f, _ := mainModelFixture(t, "claude", "gw")
	ctx := context.Background()
	memory := s.store().(*memStore)
	for key, raw := range memory.vals {
		if err := database.PutSetting(ctx, key, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	s.store = func() Store { return database }
	out, err := s.switchMainModel(ctx, "main", proto.ModelPick{Launcher: "cx", Model: "gpt-5", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Hub: f, Store: s.store})
	pick, ok := restarted.modelPickOf(ctx, "server", out.Session.SessionID)
	if restarted.settings(ctx).Main != out.Session.SessionID || restarted.loadHandoff(ctx).For != out.Session.SessionID || !ok || pick.Model != "gpt-5" {
		t.Fatal("Main, pick and handoff did not commit together")
	}
}

func TestMainModelHandoffTransfersOpenTasksAndKeepsWorkerConfiguration(t *testing.T) {
	s, _, _ := mainModelFixture(t, "claude", "gw")
	ctx := context.Background()
	open, err := s.newTodo(ctx, "Finish the change", "", "main", "", true)
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, open.ID, "main", []string{"Verified"}, 30)
	closed, err := s.newTodo(ctx, "Already complete", "", "main", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.editTodo(ctx, closed.ID, db.TodoEdit{Done: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.modelRecord(ctx, "agent.new")
	out, err := s.switchMainModel(ctx, "main", proto.ModelPick{Launcher: "cx"})
	if err != nil {
		t.Fatal(err)
	}
	got := todoNow(s, open.ID)
	if got.Origin != out.Session.SessionID || got.Supervision.Supervisor != out.Session.SessionID || got.Supervision.DoneWhen[0] != "Verified" {
		t.Fatalf("task lost its ownership or criteria: %+v", got)
	}
	if todoNow(s, closed.ID).Origin != "main" {
		t.Fatal("closed history was rewritten")
	}
	after, _ := s.modelRecord(ctx, "agent.new")
	if !reflect.DeepEqual(before, after) || s.settings(ctx).BackgroundSupervisor != "background" {
		t.Fatal("worker or background configuration changed")
	}
}

func TestMainModelHandoffRefusesBusyAndStaleSourcesBeforeStartup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*proto.Session)
		expect string
	}{
		{"stale", func(*proto.Session) {}, "previous"},
		{"working", func(c *proto.Session) { c.Status = proto.StatusWorking }, "main"},
		{"blocked", func(c *proto.Session) { c.Status = proto.StatusBlocked }, "main"},
		{"approval", func(c *proto.Session) { c.PendingInteractions = 1 }, "main"},
		{"queue", func(c *proto.Session) { c.Queued = []string{"not sent"} }, "main"},
		{"compaction", func(c *proto.Session) { c.Compaction = &proto.Compaction{State: proto.CompactionRunning} }, "main"},
		{"background", func(c *proto.Session) { c.BackgroundWorking = true }, "main"},
		{"unknown", func(c *proto.Session) { c.Status = proto.StatusUnknown }, "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, h := mainModelFixture(t, "claude", "gw")
			c := proto.Session{SessionID: "main", Assistant: true, Agent: "claude", Launcher: "gw", Status: proto.StatusIdle}
			tc.change(&c)
			f.setSessions("server", c)
			_, err := s.switchMainModel(context.Background(), tc.expect, proto.ModelPick{Launcher: "cx"})
			if err == nil || h.starts != 0 || s.settings(context.Background()).Main != "main" {
				t.Fatalf("unsafe source: %v, %d starts", err, h.starts)
			}
		})
	}
}

func TestMainModelHandoffFailurePreservesTheSource(t *testing.T) {
	for _, kind := range []string{"startup", "main_changed", "transcript_changed", "pick_changed", "save"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s, f, h := mainModelFixture(t, "claude", "gw")
			h.beforeStart = func() error {
				switch kind {
				case "startup":
					return errors.New("target unavailable")
				case "main_changed":
					_, err := s.updateSettings(ctx, func(v *Settings) error { v.Main = "elsewhere"; return nil })
					return err
				case "transcript_changed":
					f.setSessions("server", proto.Session{SessionID: "main", Assistant: true, Agent: "claude", Launcher: "gw", Status: proto.StatusIdle, Rev: 99})
				case "pick_changed":
					return s.setModelPick(ctx, "main", &modelPick{MachineID: "server", Launcher: "claude"})
				case "save":
					s.store().(*memStore).mu.Lock()
					s.store().(*memStore).failPut = errors.New("database unavailable")
					s.store().(*memStore).mu.Unlock()
				}
				return nil
			}
			_, err := s.switchMainModel(ctx, "main", proto.ModelPick{Launcher: "cx"})
			if err == nil || h.starts != 1 {
				t.Fatalf("failure not preserved: %v, starts %d", err, h.starts)
			}
			want := "main"
			if kind == "main_changed" {
				want = "elsewhere"
			}
			if s.settings(ctx).Main != want || len(s.settings(ctx).Former) != 0 || s.isArchived("main") {
				t.Fatal("failure changed the old main")
			}
			if kind != "startup" && len(h.closed) != 1 {
				t.Fatalf("unused target not closed: %v", h.closed)
			}
		})
	}
}

func TestMainModelHandoffConcurrentRequestsStartOnlyOneTarget(t *testing.T) {
	s, _, h := mainModelFixture(t, "claude", "gw")
	entered, release := make(chan struct{}), make(chan struct{})
	h.beforeStart = func() error { close(entered); <-release; return nil }
	done := make(chan error, 2)
	go func() {
		_, err := s.switchMainModel(context.Background(), "main", proto.ModelPick{Launcher: "cx"})
		done <- err
	}()
	<-entered
	go func() {
		_, err := s.switchMainModel(context.Background(), "main", proto.ModelPick{Launcher: "agy-rt"})
		done <- err
	}()
	close(release)
	a, b := <-done, <-done
	if (a == nil) == (b == nil) || h.starts != 1 {
		t.Fatalf("duplicate handoff: %v %v, starts %d", a, b, h.starts)
	}
}

func TestMainModelHandoffKeepsUnconsumedContextAcrossAnotherSwitch(t *testing.T) {
	s, f, _ := mainModelFixture(t, "claude", "gw")
	ctx := context.Background()
	first, err := s.switchMainModel(ctx, "main", proto.ModelPick{Launcher: "cx"})
	if err != nil {
		t.Fatal(err)
	}
	c := first.Session
	c.Status = proto.StatusIdle
	f.setSessions("server", c)
	second, err := s.switchMainModel(ctx, c.SessionID, proto.ModelPick{Launcher: "agy-rt"})
	if err != nil || !second.Carried {
		t.Fatalf("second: %+v %v", second, err)
	}
	if got := s.handoffFor(ctx, second.Session.SessionID, "Continue"); !strings.Contains(got, "Ship the fix") {
		t.Fatalf("context lost: %q", got)
	}
}

func TestMainModelRouteRequiresAnExpectedSourceAndFullRole(t *testing.T) {
	s, _, h := mainModelFixture(t, "claude", "gw")
	for _, body := range []string{`{}`, `{"expect":"previous","launcher":"cx"}`, `{"expect":"main","launcher":"missing"}`, `{"expect":"main","launcher":"cx","effort":"invalid"}`} {
		if r := appRequest(t, s, "POST", "/api/v1/assistant/main/model", body, "en"); r.Code == 200 {
			t.Fatalf("accepted %s", body)
		}
	}
	if h.starts != 0 {
		t.Fatal("invalid request started a target")
	}
	w, _ := permissionSettingsRequest(t, s, "POST", "/api/v1/assistant/main/model", "", "limited", `{"expect":"main","launcher":"cx"}`)
	if w.Code != 403 {
		t.Fatalf("role: %d %s", w.Code, w.Body.String())
	}
}
