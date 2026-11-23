package node

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// Exercise the real process startup, mode persistence, and acknowledged Claude control protocol.
func TestAssistantResumeUsesCurrentDefaultUnlessModeExplicit(t *testing.T) {
	f := cliTestBash(t)
	raw, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"text": "first", "cwd": t.TempDir(), "launcher": "claude",
		"permission_mode": "auto", "default_permission_mode": "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.SessionID == "" {
		t.Fatalf("start result=%s err=%v", raw, err)
	}
	f.idle(t, started.SessionID)
	f.n.stopAssistantProcess(started.SessionID)

	// An older manager supplies no default: the legacy mode still wins on this path.
	if _, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"session_id": started.SessionID, "text": "resume from an older manager",
	}); err != nil {
		t.Fatal(err)
	}
	f.idle(t, started.SessionID)
	if st, _ := f.n.state.get(started.SessionID); st.AppMode != "auto" {
		t.Fatalf("legacy manager lost its previous mode; state=%+v", st)
	}
	f.n.stopAssistantProcess(started.SessionID)

	if _, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"session_id": started.SessionID, "text": "resume with the current default", "default_permission_mode": proto.ModeBypass,
	}); err != nil {
		t.Fatal(err)
	}
	f.idle(t, started.SessionID)
	if st, _ := f.n.state.get(started.SessionID); st.AppMode != proto.ModeBypass || st.AppModeExplicit {
		t.Fatalf("legacy mode %q overrode the current default; state=%+v", st.AppMode, st)
	}

	// A different default does not interrupt a live process; it applies on the next stopped-process resume.
	if _, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"session_id": started.SessionID, "text": "keep the live process", "default_permission_mode": "default",
	}); err != nil {
		t.Fatal(err)
	}
	f.idle(t, started.SessionID)
	if st, _ := f.n.state.get(started.SessionID); st.AppMode != proto.ModeBypass {
		t.Fatalf("live process changed with the default; state=%+v", st)
	}

	if err := f.n.setModes(context.Background(), started.SessionID, "plan", ""); err != nil {
		t.Fatal(err)
	}
	f.n.stopAssistantProcess(started.SessionID)
	if _, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"session_id": started.SessionID, "text": "resume with an explicit choice", "default_permission_mode": proto.ModeBypass,
	}); err != nil {
		t.Fatal(err)
	}
	f.idle(t, started.SessionID)
	if st, _ := f.n.state.get(started.SessionID); st.AppMode != "plan" || !st.AppModeExplicit {
		t.Fatalf("current default overrode the explicit conversation mode; state=%+v", st)
	}
}

func TestSessionSendDoesNotMarkFallbackModeExplicit(t *testing.T) {
	f := cliTestBash(t)
	raw, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"text": "legacy start", "cwd": t.TempDir(), "launcher": "claude",
		"permission_mode": "auto", "default_permission_mode": "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.SessionID == "" {
		t.Fatalf("start result=%s err=%v", raw, err)
	}
	f.idle(t, started.SessionID)

	if _, err := nodeCall(t, f.n, "session.send", map[string]any{
		"session_id": started.SessionID, "text": "session.send drops its generic mode",
		"permission_mode": "plan", "permission_mode_explicit": true,
	}); err != nil {
		t.Fatal(err)
	}
	f.idle(t, started.SessionID)
	if st, _ := f.n.state.get(started.SessionID); st.AppModeExplicit {
		t.Fatalf("fallback mode %q was pinned as an explicit choice; state=%+v", st.AppMode, st)
	}
}

func TestAssistantExplicitStartModeSurvivesDefaultResume(t *testing.T) {
	f := cliTestBash(t)
	raw, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"text": "explicit start", "cwd": t.TempDir(), "launcher": "claude",
		"permission_mode": "plan", "permission_mode_explicit": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.SessionID == "" {
		t.Fatalf("start result=%s err=%v", raw, err)
	}
	f.idle(t, started.SessionID)
	f.n.stopAssistantProcess(started.SessionID)

	if _, err := nodeCall(t, f.n, "assistant.send", map[string]any{
		"session_id": started.SessionID, "text": "resume the explicit start", "default_permission_mode": proto.ModeBypass,
	}); err != nil {
		t.Fatal(err)
	}
	f.idle(t, started.SessionID)
	if st, _ := f.n.state.get(started.SessionID); st.AppMode != "plan" || !st.AppModeExplicit {
		t.Fatalf("explicit start did not survive resume; state=%+v", st)
	}
}

func TestClaudeNewConversationPermissionModes(t *testing.T) {
	for _, mode := range proto.PermissionModes {
		t.Run(mode, func(t *testing.T) {
			f := cliTest(t)
			id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, mode, "", cliExec())
			if err != nil {
				t.Fatal(err)
			}
			f.idle(t, id)
			st, ok := f.n.state.get(id)
			if !ok || st.AppMode != mode {
				t.Fatalf("persisted mode: %+v", st)
			}
			p := f.proc(id)
			p.mu.Lock()
			applied := p.mode
			p.mu.Unlock()
			if applied != mode {
				t.Fatalf("applied mode: %q, want %q", applied, mode)
			}
			if mode == proto.ModeBypass {
				if argv := f.file("cli-argv"); !strings.Contains(argv, "--permission-mode "+mode) {
					t.Fatalf("start args: %q", argv)
				}
			} else if !strings.Contains(f.file("stdin.jsonl"), `"mode":"`+mode+`"`) {
				t.Fatalf("Claude did not receive mode %s: %s", mode, f.file("stdin.jsonl"))
			}
		})
	}
}
