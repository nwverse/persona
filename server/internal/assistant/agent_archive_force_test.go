package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestForceArchiveConfirmationDatabase(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	ft := newFleetTestOn(t, database)
	ft.f.sessions["mac"] = nil
	row := proto.Session{MachineID: "mac", SessionID: "blocked-agent", Agent: "claude", Status: proto.StatusBlocked, State: proto.StateNeedsYou, UpdatedAt: time.Now().Add(-time.Hour)}
	ft.d.setSessions("mac", []proto.Session{row})
	ft.d.machines["mac"].call = func(method string, raw json.RawMessage) (any, error) {
		if method == "session.close" {
			var got struct {
				Force bool `json:"force"`
			}
			_ = json.Unmarshal(raw, &got)
			if !got.Force {
				t.Error("close did not request cancellation-only force")
			}
		}
		return map[string]any{}, nil
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		ix := proto.Interaction{ID: "force-" + kind, MachineID: "mac", SessionID: row.SessionID, Kind: kind, ExpiresAt: time.Now().Add(time.Hour)}
		ft.s.record(ix)
		ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix, local: func(r proto.Resolution) error {
			if r.Allow != nil && *r.Allow || r.Approve != nil && *r.Approve || r.Always || r.Mode != "" {
				t.Errorf("force archive granted approval: %+v", r)
			}
			if kind == proto.IxScreen && !r.Dismiss {
				t.Errorf("screen was not cancelled: %+v", r)
			}
			return nil
		}}
	}
	result, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
	if err != nil {
		t.Fatalf("force archive: %v", err)
	}
	if !strings.Contains(result, "closed") {
		t.Fatalf("close result missing: %s", result)
	}
	if got := ft.d.callsTo("mac", "session.close"); len(got) != 1 {
		t.Fatalf("close calls: %s", got)
	}
	if len(ft.s.pendingQuestions()) != 0 {
		t.Fatal("confirmations remain pending")
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("stale blocked inventory undid the force archive")
	}
	restarted := newFleetTestOn(t, database)
	restarted.d.setSessions("mac", nil)
	if !archivedIDs(t, restarted.s)[row.SessionID] {
		t.Fatal("force archive did not persist")
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		status, r, err := database.InteractionOutcome(ctx, "force-"+kind)
		wantStatus := "resolved"
		if kind == proto.IxScreen {
			wantStatus = "dismissed"
		}
		if err != nil || status != wantStatus || r == nil {
			t.Fatalf("stored confirmation %s: %s %+v %v", kind, status, r, err)
		}
		if kind == proto.IxPermission && (r.Allow == nil || *r.Allow) || kind == proto.IxPlan && (r.Approve == nil || *r.Approve) || kind == proto.IxScreen && !r.Dismiss || r.Always || r.Mode != "" {
			t.Fatalf("stored confirmation granted or lost its cancellation: %+v", r)
		}
		raw, _ := json.Marshal(r)
		t.Logf("%s persisted %s: %s", kind, status, raw)
	}
}

func TestForceArchiveHTTPAndQuestionBoundary(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou })
	ix := proto.Interaction{ID: "waiting-question", MachineID: "mac", SessionID: row.SessionID, Kind: proto.IxQuestion}
	ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix}
	_, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
	if err == nil || len(ft.d.callsTo("mac", "session.close")) != 0 {
		t.Fatal("force archive bypassed an ordinary question")
	}
	ft.s.finishInteraction(ix.ID)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/?force=1", nil)
	r.SetPathValue("sid", row.SessionID)
	r.Header.Set(RoleHeader, roleFull)
	ft.s.setAgentSessionArchive(w, r, "mac", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"closed":true`) {
		t.Fatalf("forced HTTP close: %d %s", w.Code, w.Body)
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("HTTP close did not retain archive")
	}
	row.Status, row.State = proto.StatusWorking, proto.StateWorking
	ft.d.setSessions("mac", []proto.Session{row})
	if archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("new work did not restore the archived session")
	}
}

func TestForceArchiveKeepsOtherBusyGuards(t *testing.T) {
	for name, edit := range map[string]func(*proto.Session){
		"working":            func(s *proto.Session) { s.Status = proto.StatusWorking; s.State = proto.StateWorking },
		"starting":           func(s *proto.Session) { s.Starting = true },
		"background":         func(s *proto.Session) { s.BackgroundWorking = true },
		"background waiting": func(s *proto.Session) { s.BackgroundWaiting = true },
		"background tasks":   func(s *proto.Session) { s.Background = []proto.BackgroundTask{{ID: "job"}} },
		"workflow":           func(s *proto.Session) { s.Workflows = []proto.Workflow{{Running: true}} },
		"subagent":           func(s *proto.Session) { s.Subagents = []proto.Subagent{{Running: true}} },
		"loop":               func(s *proto.Session) { s.Loop = &proto.LoopInfo{} },
		"queued":             func(s *proto.Session) { s.Queued = []string{"next"} },
		"server":             func(s *proto.Session) { s.Ports = []proto.Port{{Port: 5173}} },
		"assistant":          func(s *proto.Session) { s.Assistant = true },
	} {
		t.Run(name, func(t *testing.T) {
			ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou; edit(s) })
			_, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
			if err == nil {
				t.Fatal("force accepted unsafe state")
			}
			if len(ft.d.callsTo("mac", "session.close")) != 0 || len(archivedIDs(t, ft.s)) != 0 {
				t.Fatal("refusal changed the Agent")
			}
		})
	}
}

func TestForceArchiveCloseFailureAndHTTPRole(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou })
	ft.d.machines["mac"].call = func(string, json.RawMessage) (any, error) { return nil, errors.New("close failed") }
	for _, role := range []string{roleAnswer, roleFull} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/?force=1", nil)
		r.SetPathValue("sid", row.SessionID)
		r.Header.Set(RoleHeader, role)
		ft.s.setAgentSessionArchive(w, r, "mac", true)
		if role == roleAnswer && w.Code != http.StatusForbidden {
			t.Fatalf("answer-only: %d", w.Code)
		}
		if role == roleFull && w.Code == http.StatusOK {
			t.Fatal("failed close claimed success")
		}
	}
	if len(archivedIDs(t, ft.s)) != 0 {
		t.Fatal("failed close hid the blocked Agent")
	}
}
