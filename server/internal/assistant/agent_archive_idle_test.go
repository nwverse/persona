package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

type recordingIdleAuditStore struct {
	Store
	mu   sync.Mutex
	rows []struct {
		plugin, action, actor string
		detail                json.RawMessage
	}
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
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
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

	// Already-archived offline session is idempotent and audited.
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
