package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

type recordingIdleAuditStore struct {
	Store
	mu             sync.Mutex
	failAuditLoad  bool
	failAuditWrite bool
	rows           []struct {
		plugin, action, actor string
		detail                json.RawMessage
	}
}

func (r *recordingIdleAuditStore) Setting(ctx context.Context, key string, dst any) (bool, error) {
	r.mu.Lock()
	fail := r.failAuditLoad
	r.mu.Unlock()
	if key == idleArchiveAuditKey && fail {
		return false, errors.New("audit store read failed")
	}
	return r.Store.Setting(ctx, key, dst)
}

func (r *recordingIdleAuditStore) PutSetting(ctx context.Context, key string, value any) error {
	r.mu.Lock()
	fail := r.failAuditWrite
	r.mu.Unlock()
	if key == idleArchiveAuditKey && fail {
		return errors.New("audit store write failed")
	}
	return r.Store.PutSetting(ctx, key, value)
}

func (r *recordingIdleAuditStore) AddExtensionAudit(_ context.Context, id, action, actor string, detail json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, struct {
		plugin, action, actor string
		detail                json.RawMessage
	}{plugin: id, action: action, actor: actor, detail: append(json.RawMessage(nil), detail...)})
	return nil
}

func (r *recordingIdleAuditStore) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

func idleArchiveTestSetup(t *testing.T, now time.Time, edit func(*proto.Session)) (*fleetTest, *recordingIdleAuditStore, proto.Session) {
	t.Helper()
	ft, row := closeTestService(t, func(s *proto.Session) {
		s.UpdatedAt = now.Add(-45 * time.Minute)
		if edit != nil {
			edit(s)
		}
	})
	rec := &recordingIdleAuditStore{Store: ft.s.store()}
	ft.s.setStore(func() Store { return rec })
	ft.s.idleArchiveAudit.now = func() time.Time { return now }
	return ft, rec, row
}

func postIdleArchive(s *Service, mid, sid, role, ext, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+mid+"/"+sid+"/archive-idle", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	if ext != "" {
		r.Header.Set(ExtensionHeader, ext)
	}
	s.ServeHTTP(w, r)
	return w
}

func getIdleArchiveAudit(s *Service, query, role, ext string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/archive-idle/audit"+query, nil)
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	if ext != "" {
		r.Header.Set(ExtensionHeader, ext)
	}
	s.ServeHTTP(w, r)
	return w
}

func TestIdleArchiveClosesEligibleIdleSessionAndWritesAudit(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
	ft, rec, row := idleArchiveTestSetup(t, now, nil)

	w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext",
		`{"idle_minutes":30,"reason":"reclaim idle slot"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp assistantwire.IdleArchiveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || !resp.Archived || resp.AlreadyArchived || !resp.AuditPersisted || resp.AuditID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.ObservedIdleSeconds != 45*60 {
		t.Fatalf("observed_idle_seconds = %d, want %d", resp.ObservedIdleSeconds, 45*60)
	}
	if resp.Close == nil || !resp.Close.Closed || resp.Close.Outcome != archiveCloseClosed {
		t.Fatalf("expected closed terminal, got %+v", resp.Close)
	}
	if retired := retiredOf(ft); len(retired) != 1 || retired[0] != row.SessionID {
		t.Fatalf("retired = %v, want [%s]", retired, row.SessionID)
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("session was not archived")
	}
	if rec.count() != 1 {
		t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
	}

	// Extension reads its own audit entry; another extension cannot see it; full role sees all.
	ownAudit := getIdleArchiveAudit(ft.s, "?machine_id=mac&session_id="+row.SessionID, roleExtension, "guardrail-ext")
	if ownAudit.Code != http.StatusOK {
		t.Fatalf("own audit status = %d: %s", ownAudit.Code, ownAudit.Body.String())
	}
	var ownBody struct {
		Entries []assistantwire.IdleArchiveAuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(ownAudit.Body.Bytes(), &ownBody); err != nil {
		t.Fatal(err)
	}
	if len(ownBody.Entries) != 1 || ownBody.Entries[0].ID != resp.AuditID ||
		ownBody.Entries[0].Outcome != idleArchiveOutcomeArchivedAndClosed ||
		ownBody.Entries[0].Extension != "guardrail-ext" || ownBody.Entries[0].Reason != "reclaim idle slot" {
		t.Fatalf("unexpected own audit entries: %+v", ownBody.Entries)
	}

	otherAudit := getIdleArchiveAudit(ft.s, "", roleExtension, "other-ext")
	if otherAudit.Code != http.StatusOK {
		t.Fatalf("other audit status = %d", otherAudit.Code)
	}
	var otherBody struct {
		Entries []assistantwire.IdleArchiveAuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(otherAudit.Body.Bytes(), &otherBody); err != nil {
		t.Fatal(err)
	}
	if len(otherBody.Entries) != 0 {
		t.Fatalf("other extension leaked entries: %+v", otherBody.Entries)
	}

	fullAudit := getIdleArchiveAudit(ft.s, "?outcome="+idleArchiveOutcomeArchivedAndClosed, roleFull, "")
	if fullAudit.Code != http.StatusOK {
		t.Fatalf("full audit status = %d", fullAudit.Code)
	}
	var fullBody struct {
		Entries []assistantwire.IdleArchiveAuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(fullAudit.Body.Bytes(), &fullBody); err != nil {
		t.Fatal(err)
	}
	if len(fullBody.Entries) != 1 || fullBody.Entries[0].ID != resp.AuditID {
		t.Fatalf("unexpected full audit entries: %+v", fullBody.Entries)
	}
}

func TestIdleArchiveEnforcesStrictIdleDurationBoundary(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
	cases := []struct {
		name      string
		updatedAt time.Time
		wantCode  string
	}{
		{"exact N minutes rejected", now.Add(-30 * time.Minute), idleArchiveCodeNotIdleLongEnough},
		{"less than N minutes rejected", now.Add(-29*time.Minute - 59*time.Second), idleArchiveCodeNotIdleLongEnough},
		{"future timestamp rejected", now.Add(time.Minute), idleArchiveCodeNotIdleLongEnough},
		{"zero timestamp rejected", time.Time{}, idleArchiveCodeIdleUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft, rec, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
				s.UpdatedAt = tc.updatedAt
			})
			w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":30}`)
			if w.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != tc.wantCode || body["audit_id"] == "" {
				t.Fatalf("got body %+v, want code %s with audit_id", body, tc.wantCode)
			}
			if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
				t.Fatal("refused session must not be archived or retired")
			}
			if rec.count() != 1 {
				t.Fatalf("refusal must write plugin_audit row, got %d", rec.count())
			}
		})
	}
}

func TestIdleArchiveHardRefusesProtectedAndBusySessions(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)

	t.Run("main conversation", func(t *testing.T) {
		ft, rec, _ := idleArchiveTestSetup(t, now, nil)
		setSettings(t, ft.s, func(v *Settings) { v.Main = "main-conv" })
		ft.d.setSessions("mac", []proto.Session{{
			MachineID: "mac", SessionID: "main-conv", Assistant: true, Status: proto.StatusIdle,
			UpdatedAt: now.Add(-2 * time.Hour),
		}})
		w := postIdleArchive(ft.s, "mac", "main-conv", roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeMainConversation {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeMainConversation)
		}
		if len(retiredOf(ft)) != 0 || rec.count() != 1 {
			t.Fatalf("retired=%v auditCount=%d", retiredOf(ft), rec.count())
		}
	})

	t.Run("direction lead conversation", func(t *testing.T) {
		ft, rec, _ := idleArchiveTestSetup(t, now, nil)
		setSettings(t, ft.s, func(v *Settings) { v.Leaders = []string{"lead-conv"} })
		ft.d.setSessions("mac", []proto.Session{{
			MachineID: "mac", SessionID: "lead-conv", Assistant: true, Status: proto.StatusIdle,
			UpdatedAt: now.Add(-2 * time.Hour),
		}})
		w := postIdleArchive(ft.s, "mac", "lead-conv", roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeDirectionLead {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeDirectionLead)
		}
		if len(retiredOf(ft)) != 0 || rec.count() != 1 {
			t.Fatalf("retired=%v auditCount=%d", retiredOf(ft), rec.count())
		}
	})

	t.Run("other assistant conversation", func(t *testing.T) {
		ft, _, _ := idleArchiveTestSetup(t, now, nil)
		ft.d.setSessions("mac", []proto.Session{{
			MachineID: "mac", SessionID: "side-assistant", Assistant: true, Status: proto.StatusIdle,
			UpdatedAt: now.Add(-2 * time.Hour),
		}})
		w := postIdleArchive(ft.s, "mac", "side-assistant", roleFull, "", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeAssistantConversation {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeAssistantConversation)
		}
	})

	t.Run("live pending interaction even when row count stale", func(t *testing.T) {
		ft, _, row := idleArchiveTestSetup(t, now, nil)
		ft.s.fleet.mu.Lock()
		if ft.s.fleet.interactions == nil {
			ft.s.fleet.interactions = map[string]*pending{}
		}
		ft.s.fleet.interactions["ix_open"] = &pending{ix: proto.Interaction{
			ID: "ix_open", MachineID: "mac", SessionID: row.SessionID, Kind: proto.IxPermission,
		}}
		ft.s.fleet.mu.Unlock()
		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeUnresolvedInteraction {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeUnresolvedInteraction)
		}
	})

	t.Run("open todo session", func(t *testing.T) {
		ft, _, row := idleArchiveTestSetup(t, now, nil)
		ft.s.todos.mu.Lock()
		ft.s.todos.loaded = true
		ft.s.todos.todos = map[string]*Todo{
			"todo_open": {
				ID:     "todo_open",
				Title:  "active task",
				Agents: []TodoAgent{{MachineID: "mac", SessionID: row.SessionID}},
			},
		}
		ft.s.todos.activeBySession = map[[2]string]string{{"mac", row.SessionID}: "todo_open"}
		ft.s.todos.mu.Unlock()
		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeOpenTodo {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeOpenTodo)
		}
	})

	busyCases := []struct {
		name     string
		edit     func(*proto.Session)
		wantCode string
	}{
		{"working", func(s *proto.Session) { s.Status, s.State = proto.StatusWorking, proto.StateWorking }, idleArchiveCodeSessionBusy},
		{"blocked", func(s *proto.Session) { s.Status = proto.StatusBlocked }, idleArchiveCodeUnresolvedInteraction},
		{"needs you", func(s *proto.Session) { s.State = proto.StateNeedsYou }, idleArchiveCodeUnresolvedInteraction},
		{"pending interaction count", func(s *proto.Session) { s.PendingInteractions = 1 }, idleArchiveCodeUnresolvedInteraction},
		{"starting", func(s *proto.Session) { s.Starting = true }, idleArchiveCodeSessionBusy},
		{"unknown status", func(s *proto.Session) { s.Status = proto.StatusUnknown }, idleArchiveCodeSessionBusy},
		{"queued input", func(s *proto.Session) { s.Queued = []string{"next"} }, idleArchiveCodeSessionBusy},
		{"loop active", func(s *proto.Session) { s.Loop = &proto.LoopInfo{} }, idleArchiveCodeSessionBusy},
		{"running server port", func(s *proto.Session) { s.Ports = []proto.Port{{Port: 8080}} }, idleArchiveCodeSessionBusy},
		{"running subagent", func(s *proto.Session) { s.Subagents = []proto.Subagent{{Running: true}} }, idleArchiveCodeSessionBusy},
		{"running workflow", func(s *proto.Session) { s.Workflows = []proto.Workflow{{Running: true}} }, idleArchiveCodeSessionBusy},
		{"wake pending", func(s *proto.Session) { wake := now.Add(time.Minute); s.Work = &proto.WorkState{WakeAt: &wake} }, idleArchiveCodeSessionBusy},
		{"external tool", func(s *proto.Session) { s.SourceKind = "codex_app" }, idleArchiveCodeExternalSession},
		{"subagent row", func(s *proto.Session) { s.IsSubagent = true }, idleArchiveCodeSubagentSession},
	}
	for _, tc := range busyCases {
		t.Run(tc.name, func(t *testing.T) {
			ft, _, row := idleArchiveTestSetup(t, now, tc.edit)
			w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			if w.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], tc.wantCode)
			}
			if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
				t.Fatal("refused session was mutated")
			}
		})
	}
}

func TestIdleArchiveValidationRolesAndArchiveOnly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ft, _, row := idleArchiveTestSetup(t, now, nil)

	// Disallowed roles and missing extension header are rejected 403.
	for _, tc := range []struct{ role, ext string }{
		{"readonly", ""},
		{roleAnswer, ""},
		{roleExtension, ""},
	} {
		w := postIdleArchive(ft.s, "mac", row.SessionID, tc.role, tc.ext, `{"idle_minutes":10}`)
		if w.Code != http.StatusForbidden {
			t.Fatalf("role=%q ext=%q: got %d, want 403", tc.role, tc.ext, w.Code)
		}
		aw := getIdleArchiveAudit(ft.s, "", tc.role, tc.ext)
		if aw.Code != http.StatusForbidden {
			t.Fatalf("audit role=%q ext=%q: got %d, want 403", tc.role, tc.ext, aw.Code)
		}
	}

	// Invalid request payloads are rejected 400.
	for _, bad := range []string{
		`{}`,
		`{"idle_minutes":0}`,
		`{"idle_minutes":-5}`,
		`{"idle_minutes":10,"unknown":1}`,
		`{"idle_minutes":10,"reason":"bad\u001bctrl"}`,
	} {
		w := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", bad)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: got %d, want 400", bad, w.Code)
		}
	}

	// close_terminal: false archives without closing terminal.
	w := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", `{"idle_minutes":15,"close_terminal":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("archive-only got %d: %s", w.Code, w.Body.String())
	}
	if len(retiredOf(ft)) != 0 {
		t.Fatalf("close_terminal=false still retired: %v", retiredOf(ft))
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("session was not archived")
	}

	// Immediate retry 1s later (within idle_minutes) is idempotent for archive-only even while live.
	ft.s.idleArchiveAudit.now = func() time.Time { return now.Add(time.Second) }
	idemLive := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", `{"idle_minutes":15,"close_terminal":false}`)
	if idemLive.Code != http.StatusOK {
		t.Fatalf("immediate live archive-only retry got %d: %s", idemLive.Code, idemLive.Body.String())
	}
	var idemLiveResp assistantwire.IdleArchiveResponse
	if err := json.Unmarshal(idemLive.Body.Bytes(), &idemLiveResp); err != nil {
		t.Fatal(err)
	}
	if !idemLiveResp.OK || !idemLiveResp.Archived || !idemLiveResp.AlreadyArchived {
		t.Fatalf("unexpected live idempotent response: %+v", idemLiveResp)
	}

	// Already-archived offline session is idempotent within idle_minutes too.
	offline := row
	offline.Status, offline.State, offline.PaneID = proto.StatusOffline, proto.StateOffline, ""
	ft.d.setSessions("mac", []proto.Session{offline})
	idem := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", `{"idle_minutes":15}`)
	if idem.Code != http.StatusOK {
		t.Fatalf("idempotent archive got %d: %s", idem.Code, idem.Body.String())
	}
	var idemResp assistantwire.IdleArchiveResponse
	if err := json.Unmarshal(idem.Body.Bytes(), &idemResp); err != nil {
		t.Fatal(err)
	}
	if !idemResp.OK || !idemResp.Archived || !idemResp.AlreadyArchived || idemResp.Close == nil || idemResp.Close.Outcome != archiveCloseNotRunning {
		t.Fatalf("unexpected idempotent response: %+v", idemResp)
	}
}

func TestIdleArchiveAdversarialReviewRegressions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("P0-1 offline or warm-grace computer refuses unverified live session", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			setup func(*fleetTest)
		}{
			{"disconnected computer", func(ft *fleetTest) {
				ft.d.mu.Lock()
				ft.d.machines["mac"].away = true
				ft.d.mu.Unlock()
			}},
			{"warm-start grace active", func(ft *fleetTest) {
				ft.d.mu.Lock()
				ft.d.machines["mac"].shownUntil = now.Add(time.Minute)
				ft.d.mu.Unlock()
			}},
			{"unlisted connection", func(ft *fleetTest) {
				ft.d.mu.Lock()
				ft.d.machines["mac"].unlisted = true
				ft.d.mu.Unlock()
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ft, _, row := idleArchiveTestSetup(t, now, nil)
				tc.setup(ft)
				w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
				if w.Code != http.StatusConflict {
					t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
				}
				var body map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if body["code"] != idleArchiveCodeSessionUnverified {
					t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeSessionUnverified)
				}
				if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
					t.Fatal("unverified live session was archived or retired")
				}
			})
		}
	})

	t.Run("P0-2 refused or failed close rolls back provisional archive", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			retireErr  error
			wantStatus int
			wantCode   string
		}{
			{"node working refusal", &rpc.Error{Code: "not_retirable", Message: "the agent is working"}, http.StatusConflict, idleArchiveCodeCloseRefused},
			{"node generic failure", &rpc.Error{Message: "herdr: no such pane"}, http.StatusBadGateway, idleArchiveCodeCloseFailed},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ft, rec, row := idleArchiveTestSetup(t, now, nil)
				ft.f.mu.Lock()
				ft.f.retireErr = tc.retireErr
				ft.f.mu.Unlock()
				w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
				if w.Code != tc.wantStatus {
					t.Fatalf("got %d (%s), want %d", w.Code, w.Body.String(), tc.wantStatus)
				}
				var body map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if body["code"] != tc.wantCode || body["audit_persisted"] != true {
					t.Fatalf("unexpected body: %+v", body)
				}
				if archivedIDs(t, ft.s)[row.SessionID] {
					t.Fatal("provisional archive was not rolled back after close refusal/failure")
				}
				if rec.count() != 1 {
					t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
				}
			})
		}
	})

	t.Run("P1-1 unknown main/leader ID returns 404 without oracle and archived hidden space keeps Space", func(t *testing.T) {
		ft, rec, _ := idleArchiveTestSetup(t, now, nil)
		setSettings(t, ft.s, func(v *Settings) {
			v.Main = "secret-main-on-other-host"
			v.Leaders = []string{"secret-lead-on-other-host"}
		})
		for _, probe := range []string{"secret-main-on-other-host", "secret-lead-on-other-host"} {
			w := postIdleArchive(ft.s, "mac", probe, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			if w.Code != http.StatusNotFound {
				t.Fatalf("probe %q: got %d (%s), want 404", probe, w.Code, w.Body.String())
			}
		}
		if rec.count() != 0 {
			t.Fatalf("oracle probes wrote %d audit entries, want 0", rec.count())
		}

		// Hidden privacy space with Cwd outside space directory preserves Space in archiveSnapshot.
		ft2, rec2, row2 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Space = "health"
			s.Cwd = "/tmp/unrelated"
		})
		if _, err := ft2.s.setAgentArchive(context.Background(), "mac", row2.SessionID, true); err != nil {
			t.Fatal(err)
		}
		// Drop from live node list so only the archived snapshot remains.
		ft2.d.setSessions("mac", nil)
		w := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("archived hidden-space snapshot returned %d (%s), want 404", w.Code, w.Body.String())
		}
		if rec2.count() != 0 {
			t.Fatalf("hidden-space snapshot probe wrote %d audit entries, want 0", rec2.count())
		}
	})

	t.Run("P1-2 outside direction lead, open responsible todo, and offline desktop app refused", func(t *testing.T) {
		// Outside direction lead
		ft1, _, row1 := idleArchiveTestSetup(t, now, nil)
		ft1.s.directions.mu.Lock()
		ft1.s.directions.loaded = true
		ft1.s.directions.m = map[string]*direction{
			"dir-outside": {
				Outside: &directionOutside{MachineID: "mac", SessionID: row1.SessionID},
			},
		}
		ft1.s.directions.mu.Unlock()
		w1 := postIdleArchive(ft1.s, "mac", row1.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		var b1 map[string]any
		_ = json.Unmarshal(w1.Body.Bytes(), &b1)
		if w1.Code != http.StatusConflict || b1["code"] != idleArchiveCodeDirectionLead {
			t.Fatalf("outside direction lead: got %d %+v, want 409 %s", w1.Code, b1, idleArchiveCodeDirectionLead)
		}

		// Open-todo Responsible session
		ft2, _, row2 := idleArchiveTestSetup(t, now, nil)
		ft2.s.todos.mu.Lock()
		ft2.s.todos.loaded = true
		ft2.s.todos.todos = map[string]*Todo{
			"todo_resp": {
				ID:          "todo_resp",
				Title:       "external responsible",
				Responsible: &db.TodoResponsible{Kind: db.ResponsibleSession, Name: "worker", MachineID: "mac", SessionID: row2.SessionID},
			},
		}
		ft2.s.todos.mu.Unlock()
		w2 := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		var b2 map[string]any
		_ = json.Unmarshal(w2.Body.Bytes(), &b2)
		if w2.Code != http.StatusConflict || b2["code"] != idleArchiveCodeOpenTodo {
			t.Fatalf("open responsible todo: got %d %+v, want 409 %s", w2.Code, b2, idleArchiveCodeOpenTodo)
		}

		// Offline desktop-app session (AgentSurface == desktop, SourceKind empty)
		ft3, _, row3 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Status, s.State, s.PaneID = proto.StatusOffline, proto.StateOffline, ""
			s.SourceKind = ""
			s.AgentSurface = transcript.SurfaceDesktop
		})
		w3 := postIdleArchive(ft3.s, "mac", row3.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		var b3 map[string]any
		_ = json.Unmarshal(w3.Body.Bytes(), &b3)
		if w3.Code != http.StatusConflict || b3["code"] != idleArchiveCodeExternalSession {
			t.Fatalf("offline desktop session: got %d %+v, want 409 %s", w3.Code, b3, idleArchiveCodeExternalSession)
		}
	})

	t.Run("P1-3 empty or unrecognized live status fails closed", func(t *testing.T) {
		for _, badStatus := range []string{"", "future_busy_status"} {
			ft, _, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
				s.Status = badStatus
				s.State = proto.StateUnknown
			})
			w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != http.StatusConflict || body["code"] != idleArchiveCodeSessionUnverified {
				t.Fatalf("status=%q: got %d %+v, want 409 %s", badStatus, w.Code, body, idleArchiveCodeSessionUnverified)
			}
			if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
				t.Fatalf("status=%q mutated session", badStatus)
			}
		}
	})

	t.Run("P1-5 audit store failure fails closed and emits no dangling audit_id", func(t *testing.T) {
		// Unloadable audit store blocks before any mutation.
		ft1, rec1, row1 := idleArchiveTestSetup(t, now, nil)
		rec1.mu.Lock()
		rec1.failAuditLoad = true
		rec1.mu.Unlock()
		w1 := postIdleArchive(ft1.s, "mac", row1.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w1.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit load failure got %d (%s), want 503", w1.Code, w1.Body.String())
		}
		if len(retiredOf(ft1)) != 0 || archivedIDs(t, ft1.s)[row1.SessionID] {
			t.Fatal("mutation occurred despite unreadable audit store")
		}

		// Audit write failure on archive-only rolls back archive, emits no native archive wish or plugin_audit row, and returns 503.
		ft2, rec2, row2 := idleArchiveTestSetup(t, now, nil)
		rec2.mu.Lock()
		rec2.failAuditWrite = true
		rec2.mu.Unlock()
		w2 := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":false}`)
		if w2.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit write failure on archive-only got %d (%s), want 503", w2.Code, w2.Body.String())
		}
		if archivedIDs(t, ft2.s)[row2.SessionID] {
			t.Fatal("archive-only was not rolled back after audit write failure")
		}
		if rec2.count() != 0 {
			t.Fatalf("rolled-back archive-only wrote %d plugin_audit rows, want 0", rec2.count())
		}
		if got := ft2.s.nativeArchiveView("mac", row2.SessionID); got != nil {
			t.Fatalf("rolled-back archive-only leaked native archive intent: %+v", got)
		}

		// Audit write failure on offline default close_terminal=true also rolls back cleanly with no side effects.
		ftOff, recOff, rowOff := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Status, s.State, s.PaneID = proto.StatusOffline, proto.StateOffline, ""
		})
		recOff.mu.Lock()
		recOff.failAuditWrite = true
		recOff.mu.Unlock()
		wOff := postIdleArchive(ftOff.s, "mac", rowOff.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if wOff.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit write failure on offline archive got %d (%s), want 503", wOff.Code, wOff.Body.String())
		}
		if archivedIDs(t, ftOff.s)[rowOff.SessionID] {
			t.Fatal("offline archive was not rolled back after audit write failure")
		}
		if recOff.count() != 0 {
			t.Fatalf("rolled-back offline archive wrote %d plugin_audit rows, want 0", recOff.count())
		}
		if got := ftOff.s.nativeArchiveView("mac", rowOff.SessionID); got != nil {
			t.Fatalf("rolled-back offline archive leaked native archive intent: %+v", got)
		}

		// Audit write failure on post-close success omits audit_id from JSON and does not mirror to plugin_audit.
		ftLive, recLive, rowLive := idleArchiveTestSetup(t, now, nil)
		recLive.mu.Lock()
		recLive.failAuditWrite = true
		recLive.mu.Unlock()
		wLive := postIdleArchive(ftLive.s, "mac", rowLive.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if wLive.Code != http.StatusOK {
			t.Fatalf("live close with audit failure got %d (%s), want 200", wLive.Code, wLive.Body.String())
		}
		var bLive map[string]any
		_ = json.Unmarshal(wLive.Body.Bytes(), &bLive)
		if bLive["audit_persisted"] != false {
			t.Fatalf("expected audit_persisted=false, got %+v", bLive)
		}
		if _, hasID := bLive["audit_id"]; hasID {
			t.Fatalf("IdleArchiveResponse serialized audit_id when audit_persisted=false: %+v", bLive)
		}
		if recLive.count() != 0 {
			t.Fatalf("failed audit mirrored %d rows to plugin_audit, want 0", recLive.count())
		}

		// Audit write failure on refusal emits audit_persisted=false, omits audit_id, and skips plugin_audit.
		ft3, rec3, row3 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Status, s.State = proto.StatusWorking, proto.StateWorking
		})
		rec3.mu.Lock()
		rec3.failAuditWrite = true
		rec3.mu.Unlock()
		w3 := postIdleArchive(ft3.s, "mac", row3.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w3.Code != http.StatusConflict {
			t.Fatalf("refusal with failing audit got %d, want 409", w3.Code)
		}
		var b3 map[string]any
		_ = json.Unmarshal(w3.Body.Bytes(), &b3)
		if b3["audit_persisted"] != false {
			t.Fatalf("expected audit_persisted=false, got %+v", b3)
		}
		if _, hasID := b3["audit_id"]; hasID {
			t.Fatalf("refusal emitted dangling audit_id on persistence failure: %+v", b3)
		}
		if rec3.count() != 0 {
			t.Fatalf("failed refusal audit mirrored %d rows to plugin_audit, want 0", rec3.count())
		}
	})

	t.Run("R2-P1-2 closing already-archived live session fences exit transcript writes and rolls back provisional ClosedAt on refusal", func(t *testing.T) {
		// (a) Exit transcript write during retireArchived does NOT unarchive an already-archived live session.
		realNow := time.Now().UTC()
		ft, _, row := idleArchiveTestSetup(t, realNow, nil)
		// Pre-archive without closing (ClosedAt is zero).
		wArch := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":false}`)
		if wArch.Code != http.StatusOK {
			t.Fatalf("pre-archive failed: %d %s", wArch.Code, wArch.Body.String())
		}
		// During session.retire, simulate the agent writing an exit transcript line and the node pushing
		// an updated session list before retire returns.
		ft.f.answerNode(deviceSessionRetire, func(_ string, _ json.RawMessage) (any, error) {
			bumped := row
			bumped.UpdatedAt = time.Now().UTC()
			ft.d.setSessions("mac", []proto.Session{bumped})
			if _, changed, err := ft.s.archivedSessions(context.Background(), "mac", []proto.Session{bumped}, ft.s.archiveMark()); err != nil {
				t.Errorf("mid-close archivedSessions failed: %v", err)
			} else if changed {
				t.Errorf("mid-close exit transcript write unexpectedly changed archive state")
			}
			return map[string]any{"closed": true}, nil
		})
		wClose := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":true}`)
		if wClose.Code != http.StatusOK {
			t.Fatalf("close of already-archived live session got %d (%s), want 200", wClose.Code, wClose.Body.String())
		}
		var resp assistantwire.IdleArchiveResponse
		_ = json.Unmarshal(wClose.Body.Bytes(), &resp)
		if !resp.Archived || !resp.AlreadyArchived || resp.Close == nil || !resp.Close.Closed {
			t.Fatalf("unexpected close response: %+v", resp)
		}
		if !archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("already-archived live session was unarchived by exit transcript write during close")
		}

		// (b) When node refuses close on an already-archived live session, provisional ClosedAt is cleared
		// while Archived remains true.
		ft2, _, row2 := idleArchiveTestSetup(t, realNow, nil)
		wArch2 := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":false}`)
		if wArch2.Code != http.StatusOK {
			t.Fatalf("pre-archive 2 failed: %d %s", wArch2.Code, wArch2.Body.String())
		}
		ft2.f.mu.Lock()
		ft2.f.retireErr = &rpc.Error{Code: "not_retirable", Message: "cannot resume"}
		ft2.f.mu.Unlock()
		wRef := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":true}`)
		if wRef.Code != http.StatusConflict {
			t.Fatalf("refused close of already-archived live session got %d (%s), want 409", wRef.Code, wRef.Body.String())
		}
		if !archivedIDs(t, ft2.s)[row2.SessionID] {
			t.Fatal("refused close unexpectedly unarchived already-archived session")
		}
		key2 := runtimeListID("mac", archiveSessionOf(row2))
		ft2.s.agentArchives.lock(context.Background())
		rec2 := ft2.s.agentArchives.records[key2]
		ft2.s.agentArchives.unlock()
		if !rec2.Archived || !rec2.ClosedAt.IsZero() {
			t.Fatalf("expected Archived=true and zero ClosedAt after refused close rollback, got archived=%v closedAt=%v", rec2.Archived, rec2.ClosedAt)
		}
	})
}

func TestIdleArchiveConcurrentRaceAndHiddenSpace(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
	ft, rec, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
		s.Space = "health"
	})
	// Health space is hidden from extensions by default -> 404 and zero audit leakage.
	w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("hidden space extension call got %d: %s, want 404", w.Code, w.Body.String())
	}
	if rec.count() != 0 {
		t.Fatalf("hidden space probe must not record audit entry, got %d", rec.count())
	}

	// Concurrent requests against a visible busy session and audit reader stay race-free.
	ft2, _, row2 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
		s.Status, s.State = proto.StatusWorking, proto.StateWorking
	})
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			res := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			if res.Code != http.StatusConflict {
				t.Errorf("concurrent refusal got %d, want 409", res.Code)
			}
		}()
		go func() {
			defer wg.Done()
			res := getIdleArchiveAudit(ft2.s, "?limit=10", roleExtension, "guardrail-ext")
			if res.Code != http.StatusOK {
				t.Errorf("concurrent audit read got %d, want 200", res.Code)
			}
		}()
	}
	wg.Wait()
}
