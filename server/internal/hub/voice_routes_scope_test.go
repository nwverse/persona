package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// The voice program on an extension's token (#17851, #16264): the scopes it runs on, every hub route it calls and the
// scope each needs, and the routes that stay closed to every extension. A package asks for voiceScopes in
// permissions.hub instead of the hub's full token; what follows from that is pinned below.

// voiceScopes are the scopes the voice program runs on, each one covered by the old full grant too
// (extension.InEveryScope), so a built-in handing over to a package asks for nothing more.
var voiceScopes = []string{extwire.ScopeKBRead, extwire.ScopeAgentsRead, extwire.ScopeAgentsControl,
	extwire.ScopeMachinesRead, extwire.ScopeAssistantAsk}

// voiceRoutes are the hub routes the voice program calls (server/internal/modules/voice/hub.go), a request of each as
// it sends it, and the scope the route needs.
var voiceRoutes = []struct {
	pattern, scope string
	method, path   string
	body           string
}{
	{"GET /api/v1/kb-search", extwire.ScopeKBRead, "GET", "/api/v1/kb-search?q=release", ""},
	{"GET /api/v1/sessions", extwire.ScopeAgentsRead, "GET", "/api/v1/sessions?machine_id=mb", ""},
	{"GET /api/v1/sessions/{mid}/{sid}/items", extwire.ScopeAgentsRead, "GET", "/api/v1/sessions/mb/b1/items?turns=1&lazy_images=1", ""},
	{"GET /api/v1/sessions/{mid}/{sid}/items/{iid}/full", extwire.ScopeAgentsRead, "GET", "/api/v1/sessions/mb/b1/items/i1/full", ""},
	{"GET /api/v1/interactions", extwire.ScopeAgentsRead, "GET", "/api/v1/interactions", ""},
	{"GET /api/v1/agents/triage", extwire.ScopeAgentsRead, "GET", "/api/v1/agents/triage", ""},
	{"POST /api/v1/interactions/{id}/resolve", extwire.ScopeAgentsControl, "POST", "/api/v1/interactions/ix_ok/resolve", `{"answers":{}}`},
	{"POST /api/v1/sessions/{mid}/{sid}/interrupt", extwire.ScopeAgentsControl, "POST", "/api/v1/sessions/mb/b1/interrupt", `{}`},
	{"POST /api/v1/sessions/{mid}/{sid}/messages", extwire.ScopeAgentsControl, "POST", "/api/v1/sessions/mb/b1/messages", `{"text":"hello","client_msg_id":"voice-0011223344556677"}`},
	{"GET /api/v1/machines", extwire.ScopeMachinesRead, "GET", "/api/v1/machines", ""},
	{"GET /api/v1/assistant", extwire.ScopeAssistantAsk, "GET", "/api/v1/assistant", ""},
	{"POST /api/v1/assistant/conversations", extwire.ScopeAssistantAsk, "POST", "/api/v1/assistant/conversations", `{"prompt":"hello","main":true}`},
}

// closedToExtensions are routes no scope may ever open, and why: giving one a scope hands an extension more than the
// route's name says.
var closedToExtensions = []struct{ pattern, why string }{
	{"GET /api/v1/ws", "the apps' socket: a connection that is not a scoped or read-only device's is served as the owner's (the " +
		"event log, every manager's sessions, terminals); extensions are told of changes through contributes.events"},
	{"POST /api/v1/machines/{mid}/files", "a file put anywhere the phone may write"},
	{"POST /api/v1/machines/{mid}/files/write", "a file written anywhere in the home (a shell's startup file is one)"},
	{"POST /api/v1/machines/{mid}/files/mkdir", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/rename", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/move", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/trash", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/restore", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/zip", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/copy", "the Files browser's changes share one handler"},
	{"POST /api/v1/machines/{mid}/files/chmod", "the Files browser's changes share one handler"},
	{"GET /api/v1/recordings/{id}/live", "a meeting's live lines, questions, photos and highlights; its room is on the session row (origin, origin_ref)"},
	{"POST /api/v1/recordings/{id}/live", "the phone's live sentences of a meeting"},
	{"PUT /api/v1/agents/triage", "changing the triage's switches; reading them is agents.read's"},
}

// Each route the voice program calls is registered and needs one of its five scopes, nothing else; the five are all
// it needs, and each is in the old full grant.
func TestVoiceRoutesNeedOnlyTheVoiceScopes(t *testing.T) {
	patterns := routePatterns(t)
	used := map[string]bool{}
	for _, r := range voiceRoutes {
		if !slices.Contains(patterns, r.pattern) {
			t.Errorf("%s is not a route of the hub any more: the voice program calls it", r.pattern)
		}
		if got := extwire.RouteScopes[r.pattern]; got != r.scope {
			t.Errorf("%s needs %q, want %s", r.pattern, got, r.scope)
		}
		if !slices.Contains(voiceScopes, r.scope) {
			t.Errorf("%s needs %s, which is not one of the voice program's scopes %v", r.pattern, r.scope, voiceScopes)
		}
		used[r.scope] = true
	}
	for _, s := range voiceScopes {
		if !used[s] {
			t.Errorf("%s opens no route the voice program calls: it should not ask for it", s)
		}
		if !extension.InEveryScope(s) {
			t.Errorf("%s is granted by name only: a built-in handing over to a package would have to ask for it", s)
		}
	}
}

// No scope opens the apps' socket, a computer's file changes, a meeting's live route or the triage's PUT.
func TestRoutesClosedToExtensionTokens(t *testing.T) {
	patterns := routePatterns(t)
	for _, c := range closedToExtensions {
		if !slices.Contains(patterns, c.pattern) {
			t.Errorf("%s is not a route of the hub any more: take it off closedToExtensions", c.pattern)
		}
		if scope, ok := extwire.RouteScopes[c.pattern]; ok {
			t.Errorf("%s is opened by %s; it stays for full credentials only: %s", c.pattern, scope, c.why)
		}
	}
}

// voiceHub is a hub whose manager B owns mb and knows the token of an extension granted voiceScopes: it answers
// extensions.authorize as the manager does (extmgr scopeOpens), by the route's scope in extwire.RouteScopes.
func voiceHub(t *testing.T) (*Hub, string, *guardManager) {
	t.Helper()
	h, srv, _ := newBusHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "mb", Name: "mb", Kind: "local"}}
	answer := func(knows bool) func(string, json.RawMessage) (any, error) {
		return func(method string, params json.RawMessage) (any, error) {
			switch method {
			case extwire.MethodAuthorize:
				if !knows {
					return extwire.Authorized{}, nil
				}
				var a extwire.Authorize
				_ = json.Unmarshal(params, &a)
				scope := extwire.RouteScopes[a.Pattern]
				return extwire.Authorized{OK: scope != "" && slices.Contains(voiceScopes, scope), Scope: scope,
					Extension: "voice", Name: "Voice", Scopes: voiceScopes}, nil
			case assistantwire.MethodHTTP:
				return map[string]any{"status": 200, "body": []byte(`{"ok":true}`)}, nil
			case devicewire.MethodCall:
				return map[string]any{"ok": true}, nil
			}
			return map[string]any{"ok": true}, nil
		}
	}
	connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, answer(false))
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", answer(true))
	if _, err := h.moveMachine(context.Background(), "mb", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"}})
	eventually(t, 5*time.Second, "mb", func() bool { return h.localMachine("mb") != nil })
	return h, srv.URL, b
}

// The voice program's requests with a token of voiceScopes: every route it calls lets it in, the routes closed to
// extensions and the ones it no longer calls refuse it with permission_denied, and what it sees and does is an
// extension's, not the owner's: no conversation of a space, no private card, a meeting's room by its session row, and
// a message that is not the owner's own input.
func TestVoiceProgramOnAnExtensionsToken(t *testing.T) {
	h, url, b := voiceHub(t)
	const room = "0a1b2c3d-0000-4000-8000-000000000001"
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{
		{MachineID: "mb", SessionID: "b1", Title: "B-SESSION", Cwd: "/work/project"},
		{MachineID: "mb", SessionID: "bh", Title: "SPACE-SESSION", Cwd: "/home/me/.persona/health", Assistant: true, Space: "health"},
		{MachineID: "mb", SessionID: "br", Title: "ROOM-SESSION", Cwd: "/home/me/.persona/guest-meeting/" + room, Origin: "meeting", OriginRef: room},
	}})
	eventually(t, 5*time.Second, "mb's sessions", func() bool { _, ok := h.localMachine("mb").session("br"); return ok })
	for _, ix := range []proto.Interaction{
		{ID: "ix_ok", MachineID: "mb", SessionID: "b1", Cwd: "/work/project", Kind: proto.IxQuestion, Summary: "OK-CARD"},
		{ID: "ix_private", MachineID: "mb", SessionID: "b1", Cwd: "/work/project", Kind: proto.IxQuestion, Private: true, Summary: "PRIVATE-CARD"},
		{ID: "ix_space", MachineID: "mb", SessionID: "bh", Kind: proto.IxQuestion, Summary: "SPACE-CARD"},
	} {
		if err := b.peer.Notify(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: ix}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 5*time.Second, "the cards", func() bool { return h.interactionOwner("ix_space") != "" })

	denied := func(code int, body string) bool {
		return code == http.StatusForbidden && strings.Contains(body, "permission_denied")
	}
	t.Run("the routes it calls let it in", func(t *testing.T) {
		for _, r := range voiceRoutes {
			if code, body := extDoAs(t, url, "voice-token", r.method, r.path, r.body); code != http.StatusOK {
				t.Errorf("%s %s: %d %s", r.method, r.path, code, clip(body, 200))
			}
		}
	})
	t.Run("the closed routes refuse it", func(t *testing.T) {
		for _, c := range []struct{ method, path, body string }{
			{"GET", "/api/v1/ws", ""},
			{"POST", "/api/v1/machines/mb/files/mkdir", `{"dir":"~","name":"PersonaCalls"}`},
			{"POST", "/api/v1/machines/mb/files/write", `{"path":"~/PersonaCalls/call.md","text":"x","create":true}`},
			{"GET", "/api/v1/recordings/" + room + "/live?parts=room", ""},
			{"PUT", "/api/v1/agents/triage", `{"enabled":false}`},
			{"GET", "/api/v1/channels", ""}, // no scope opens it: the program names the built-in channels itself
		} {
			before := len(reached(b))
			if code, body := extDoAs(t, url, "voice-token", c.method, c.path, c.body); !denied(code, body) {
				t.Errorf("%s %s: %d %s, want 403 permission_denied", c.method, c.path, code, clip(body, 200))
			}
			if got := reached(b); len(got) != before {
				t.Errorf("%s %s reached the manager: %v", c.method, c.path, got[before:])
			}
		}
	})
	t.Run("a space's conversation and a private card are not its to see or act on", func(t *testing.T) {
		code, body := extDoAs(t, url, "voice-token", "GET", "/api/v1/sessions?machine_id=mb", "")
		if code != http.StatusOK || !strings.Contains(body, "B-SESSION") || strings.Contains(body, "SPACE-SESSION") {
			t.Errorf("sessions: %d %s", code, clip(body, 300))
		}
		for _, c := range []struct{ method, path, body string }{
			{"GET", "/api/v1/sessions/mb/bh/items?turns=1", ""},
			{"POST", "/api/v1/sessions/mb/bh/messages", `{"text":"hello","client_msg_id":"voice-0011223344556688"}`},
			{"POST", "/api/v1/sessions/mb/bh/interrupt", `{}`},
			{"POST", "/api/v1/interactions/ix_private/resolve", `{"answers":{}}`},
			{"POST", "/api/v1/interactions/ix_space/resolve", `{"answers":{}}`},
		} {
			if code, body := extDoAs(t, url, "voice-token", c.method, c.path, c.body); code != http.StatusNotFound {
				t.Errorf("%s %s: %d %s, want 404", c.method, c.path, code, clip(body, 200))
			}
		}
		code, body = extDoAs(t, url, "voice-token", "GET", "/api/v1/interactions", "")
		if code != http.StatusOK || !strings.Contains(body, "OK-CARD") || strings.Contains(body, "PRIVATE-CARD") || strings.Contains(body, "SPACE-CARD") {
			t.Errorf("cards: %d %s", code, clip(body, 300))
		}
	})
	t.Run("a meeting's room is told by its session row", func(t *testing.T) {
		code, body := extDoAs(t, url, "voice-token", "GET", "/api/v1/sessions?machine_id=mb", "")
		var out struct {
			Sessions []proto.Session `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(body), &out); code != http.StatusOK || err != nil {
			t.Fatalf("sessions: %d %v %s", code, err, clip(body, 300))
		}
		i := slices.IndexFunc(out.Sessions, func(s proto.Session) bool { return s.SessionID == "br" })
		if i < 0 || out.Sessions[i].Origin != "meeting" || out.Sessions[i].OriginRef != room {
			t.Fatalf("the room's row: %+v", out.Sessions)
		}
		if code, body := extDoAs(t, url, "voice-token", "GET", "/api/v1/sessions/mb/br/items?turns=1", ""); code == http.StatusNotFound || denied(code, body) {
			t.Errorf("the room's items: %d %s", code, clip(body, 200))
		}
	})
	t.Run("what it sends is not the owner's own input", func(t *testing.T) {
		start := func(token string) string {
			before := len(reached(b))
			if code, body := extDoAs(t, url, token, "POST", "/api/v1/assistant/conversations", `{"prompt":"hello","main":true}`); code != http.StatusOK {
				t.Fatalf("start: %d %s", code, clip(body, 200))
			}
			return strings.Join(reached(b)[before:], "\n")
		}
		if got := start("voice-token"); !strings.Contains(got, assistantwire.MethodHTTP) || strings.Contains(got, `"reply_input":true`) || strings.Contains(got, `"human":true`) {
			t.Errorf("an extension's start was relayed as the owner's own input:\n%s", clip(got, 400))
		}
	})
}

// extDoAs is a request with the extension token name of voiceHub's manager B.
func extDoAs(t *testing.T, url, name, method, path, body string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, url+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+extTokenPrefix+name)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var out json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, string(out)
}
