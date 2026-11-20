package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// An extension's token never sees the health space's conversations or a private card (extension_private.go), whatever
// it was granted: not in the lists, their totals and counts, and not read by id.
func TestExtensionTokensNeverSeeTheHealthSpace(t *testing.T) {
	h, url, _, b := reachHub(t)
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{
		{MachineID: "mb", SessionID: "b1", Title: "B-SESSION", Cwd: "/work/project"},
		{MachineID: "mb", SessionID: "bh", Title: "HEALTH-SESSION", Cwd: "/home/me/.persona/health", Assistant: true, Space: "health"},
		{MachineID: "mb", SessionID: "bd", Title: "HEALTH-DIR", Cwd: "/home/me/.persona/health/notes", Assistant: true},
	}})
	eventually(t, 5*time.Second, "mb's sessions", func() bool { _, ok := h.localMachine("mb").session("bd"); return ok })
	cards := []proto.Interaction{
		{ID: "ix_ok", MachineID: "mb", SessionID: "b1", Cwd: "/work/project", Kind: proto.IxQuestion, Summary: "OK-CARD"},
		{ID: "ix_private", MachineID: "mb", SessionID: "b1", Cwd: "/work/project", Kind: proto.IxQuestion, Private: true, Summary: "PRIVATE-CARD"},
		{ID: "ix_health", MachineID: "mb", SessionID: "bh", Kind: proto.IxQuestion, Summary: "HEALTH-CARD"},
		{ID: "ix_health_cwd", MachineID: "mb", SessionID: "b1", Cwd: "/home/me/.persona/health", Kind: proto.IxQuestion, Summary: "HEALTH-CWD-CARD"},
	}
	for _, ix := range cards {
		if err := b.peer.Notify(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: ix}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 5*time.Second, "the cards", func() bool { return h.interactionOwner("ix_health_cwd") != "" })

	for _, path := range []string{"/api/v1/sessions", "/api/v1/sessions?machine_id=mb", "/api/v1/sessions?limit=50",
		"/api/v1/sessions?limit=50&summary=1", "/api/v1/sessions?counts=1&machine_id=mb", "/api/v1/sessions?limit=50&with_counts=1&machine_id=mb"} {
		code, body := extDo(t, url, http.MethodGet, path, "", nil)
		if code != http.StatusOK || strings.Contains(body, "HEALTH") || strings.Contains(body, `"bh"`) || strings.Contains(body, `"bd"`) {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		if !strings.Contains(path, "counts=1&") && !strings.Contains(body, "B-SESSION") {
			t.Fatalf("%s: the session it may see is missing: %s", path, body)
		}
	}
	// Totals and counts leave them out too.
	_, body := extDo(t, url, http.MethodGet, "/api/v1/sessions?counts=1&machine_id=mb", "", nil)
	var counts struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &counts); err != nil || counts.Total != 1 {
		t.Fatalf("counts %s", body)
	}

	for _, sid := range []string{"bh", "bd", "never-listed"} {
		for _, path := range []string{"/items", "/items?fold_work=1", "/items/i1/full", "/items/i1/image", "/items/i1/visualization", "/info", "/screen"} {
			if code, body := extDo(t, url, http.MethodGet, "/api/v1/sessions/mb/"+sid+path, "", nil); code != http.StatusNotFound {
				t.Errorf("%s%s: %d %s", sid, path, code, body)
			}
		}
	}
	if code, body := extDo(t, url, http.MethodGet, "/api/v1/sessions/mb/b1/info", "", nil); code != http.StatusOK {
		t.Fatalf("b1 info: %d %s", code, body)
	}

	code, body := extDo(t, url, http.MethodGet, "/api/v1/interactions", "", nil)
	if code != http.StatusOK || !strings.Contains(body, "OK-CARD") || strings.Contains(body, "PRIVATE") || strings.Contains(body, "HEALTH") {
		t.Fatalf("pending cards: %d %s", code, body)
	}
}

// The re-check of #10941: the extensions' agent.needs_you and agent.finished, which need no scope, are not sent of a
// card or a session they are not shown (extension_private.go): a card of the health space by its directory or its
// conversation's, a private card, a card or a session the hub does not list, a session of the space.
func TestExtensionsAreNotToldWhatTheyAreNotShown(t *testing.T) {
	h, a, _ := pathsHub(t, quiet, quiet)
	heardA := heard(a, extwire.EventEmit)
	health := identity.HealthDir("/home/me")
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "ma", Sessions: []proto.Session{{MachineID: "ma", SessionID: "s3", Title: "A's work"},
		{MachineID: "ma", SessionID: "sh", Title: "HEALTH-CONVERSATION", Cwd: health, Assistant: true, Space: "health"}}})
	eventually(t, 5*time.Second, "ma's sessions", func() bool { _, ok := h.localMachine("ma").session("sh"); return ok })
	cards := []proto.Interaction{
		{ID: "ix_health_cwd", MachineID: "ma", SessionID: "s3", Cwd: health, Kind: proto.IxQuestion, Summary: "HEALTH-CWD-CARD"},
		{ID: "ix_health", MachineID: "ma", SessionID: "sh", Kind: proto.IxQuestion, Summary: "HEALTH-CARD"},
		{ID: "ix_private", MachineID: "ma", SessionID: "s3", Cwd: "/work", Kind: proto.IxQuestion, Private: true, Summary: "PRIVATE-CARD"},
		{ID: "ix_unknown", MachineID: "ma", SessionID: "never-listed", Kind: proto.IxQuestion, Summary: "UNKNOWN-CARD"},
		{ID: "ix_ok", MachineID: "ma", SessionID: "s3", Cwd: "/work", Kind: proto.IxQuestion, Summary: "OK-CARD"},
	}
	for _, ix := range cards {
		if err := a.peer.Notify(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: ix}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 5*time.Second, "the cards", func() bool { return h.interactionOwner("ix_ok") != "" })
	push := func(n assistantwire.Notification) {
		t.Helper()
		if err := a.call(t, assistantwire.MethodNotify, assistantwire.NotifyRequest{Notification: n}, nil); err != nil {
			t.Fatalf("%s: %v", n.ID, err)
		}
	}
	// What they are not told first: the bus keeps the order, so once the last one is heard the others were not sent.
	for i, ix := range cards {
		push(assistantwire.Notification{ID: "card" + strconv.Itoa(i), Kind: assistantwire.NotifyInput, Interaction: &proto.Interaction{ID: ix.ID}})
	}
	push(assistantwire.Notification{ID: "done_health", Kind: assistantwire.NotifyDone, DeviceID: "ma", SessionID: "sh", Title: "HEALTH-TITLE"})
	push(assistantwire.Notification{ID: "done_unknown", Kind: assistantwire.NotifyDone, DeviceID: "ma", SessionID: "never-listed", Title: "UNKNOWN-TITLE"})
	push(assistantwire.Notification{ID: "push_none", Kind: assistantwire.NotifyPush, DeviceID: "ma", Title: "NO-SESSION"})
	push(assistantwire.Notification{ID: "done_ok", Kind: assistantwire.NotifyDone, DeviceID: "ma", SessionID: "s3", Title: "OK-TITLE"})
	eventually(t, 5*time.Second, "the events they are shown", func() bool {
		got := heardA()
		return strings.Contains(got, "OK-CARD") && strings.Contains(got, "NO-SESSION") && strings.Contains(got, "OK-TITLE")
	})
	got := heardA()
	for _, w := range []string{"HEALTH", "PRIVATE", "UNKNOWN", "ix_health", "ix_private", "ix_unknown", `"sh"`, "never-listed"} {
		if strings.Contains(got, w) {
			t.Errorf("the extensions are told %q:\n%s", w, got)
		}
	}
}

// The regression check of #10941, a forged session: an extension's token resumed any id it named, and a computer with
// no transcript of it started a new conversation under that id in the folder the extension chose, which the manager
// took for the health chat the id names (its to-dos, feed and held outcome lines shown). On a computer whose node does
// not check such a resume (no resume_outside_health), it resumes only a conversation its computer lists outside the
// space, as a scoped token does; nothing of the other two reaches the computer.
func TestAnExtensionResumesOnlyAListedChatOutsideTheHealthSpace(t *testing.T) {
	h, url, _, b := reachHub(t)
	// A node from before resume_outside_health: an id the hub does not list never reaches it. (With no word of the node
	// at all the hub answers that id 503, retryable: TestAnExtensionsResumeBeforeTheComputersListIsRetryable.)
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"},
		Info: proto.NodeInfo{Version: "test", Protocol: nodeproto.Version, Capabilities: []string{nodeproto.Sessions}}})
	eventually(t, 5*time.Second, "mb's node", func() bool { return h.localMachine("mb").nodeReported() })
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{
		{MachineID: "mb", SessionID: "b1", Title: "B-SESSION", Cwd: "/work/project"},
		{MachineID: "mb", SessionID: "bh", Title: "HEALTH-SESSION", Cwd: "/home/me/.persona/health", Assistant: true, Space: "health"},
	}})
	eventually(t, 5*time.Second, "mb's sessions", func() bool { _, ok := h.localMachine("mb").session("bh"); return ok })
	starts := func(id string) int {
		n := 0
		for _, l := range reached(b) {
			if strings.Contains(l, `"method":"session.start"`) && strings.Contains(l, `"resume_session_id":"`+id+`"`) {
				n++
			}
		}
		return n
	}
	for _, id := range []string{"6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b", "bh"} {
		code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions",
			`{"machine_id":"mb","launcher":"claude","cwd":"/tmp","resume_session_id":"`+id+`","prompt":"hi"}`, nil)
		if code != http.StatusNotFound {
			t.Errorf("resume of %s: %d %s", id, code, body)
		}
		if n := starts(id); n != 0 {
			t.Errorf("resume of %s reached the computer %d times", id, n)
		}
	}
	if code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions",
		`{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"b1","prompt":"go on"}`, nil); code != http.StatusOK {
		t.Fatalf("resume of its own chat: %d %s", code, body)
	}
	if starts("b1") != 1 {
		t.Fatalf("the resume of b1 did not reach the computer: %v", reached(b))
	}
}

// extensionResumeHub is computer mb on manager m-0b0b0b alone, an extension's (bx) to drive, with no list of its
// sessions yet. Its node announces caps (nil: a node the hub has no word of); its devices.call answers session.start
// with start, given the call's params.
func extensionResumeHub(t *testing.T, caps []string, start func(params string) (any, error)) (*Hub, string, *guardManager) {
	t.Helper()
	h, srv, _ := newBusHub(t)
	h.listedWait = 10 * time.Millisecond
	h.cfg.Machines = []MachineConfig{{ID: "mb", Name: "mb", Kind: "local"}}
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", func(method string, params json.RawMessage) (any, error) {
		switch method {
		case extwire.MethodAuthorize:
			return extwire.Authorized{OK: true, Extension: "bx", Name: "BX"}, nil
		case devicewire.MethodCall:
			if strings.Contains(string(params), `"method":"session.start"`) {
				return start(string(params))
			}
		}
		return map[string]any{}, nil
	})
	if _, err := h.moveMachine(context.Background(), "mb", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	var info proto.NodeInfo
	if caps != nil {
		info = proto.NodeInfo{Version: "test", Protocol: nodeproto.Version, Capabilities: caps}
	}
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"}, Info: info})
	eventually(t, 5*time.Second, "mb as reported", func() bool {
		m := h.localMachine("mb")
		return m != nil && m.ownerID() == "m-0b0b0b" && (caps == nil || m.announces(nodeproto.Sessions))
	})
	return h, srv.URL, b
}

// sessionStarts are the session.start calls m got that resume id, as sent.
func sessionStarts(m *guardManager, id string) []string {
	var out []string
	for _, l := range reached(m) {
		if strings.Contains(l, `"method":"session.start"`) && strings.Contains(l, `"resume_session_id":"`+id+`"`) {
			out = append(out, l)
		}
	}
	return out
}

// ownerDo is extDo with the owner's token.
func ownerDo(t *testing.T, h *Hub, url, method, path, body string, header map[string]string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, url+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(out))
}

// The regression check of #10941's resume rule: a node lists a quiet Claude chat only for a week, among its 80 newest
// and with a transcript of 2 KB at least, and a stopped Agent takes no message, so an extension lost the way back to
// an Agent it started once its computer stopped listing it. On a node that announces resume_outside_health the hub
// passes such a resume on with that mark, set by the hub alone; the node refuses one of the space or one it has nothing
// of with resume_unknown, which the extension gets as the hub's own 404, the same for a chat listed in the space. A
// listed chat outside the space goes on unmarked, as before; the owner's resume is never marked.
func TestAnExtensionResumesAnUnlistedChatOnlyThroughItsComputersCheck(t *testing.T) {
	const own, forged = "7a1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a40", "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	h, url, b := extensionResumeHub(t, []string{nodeproto.Sessions, nodeproto.ResumeOutsideHealth}, func(params string) (any, error) {
		if strings.Contains(params, forged) && strings.Contains(params, `"resume_outside_health":true`) {
			return nil, &rpc.Error{Code: proto.CodeResumeUnknown, Message: "这台电脑上没有这个对话的记录，无法在这里接着它。这次没有启动。"} // i18n-ignore: the node's words
		}
		return map[string]any{"session": map[string]any{"session_id": "started"}}, nil
	})
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{
		{MachineID: "mb", SessionID: "b1", Title: "B-SESSION", Cwd: "/work/project", Status: proto.StatusOffline},
		{MachineID: "mb", SessionID: "bh", Title: "HEALTH-SESSION", Cwd: "/home/me/.persona/health", Assistant: true, Space: "health"},
	}})
	eventually(t, 5*time.Second, "mb's sessions", func() bool { _, ok := h.localMachine("mb").session("bh"); return ok })
	resume := func(id string) string {
		return `{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"` + id + `","prompt":"go on","resume_outside_health":false}`
	}

	if code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume(own), nil); code != http.StatusOK {
		t.Fatalf("the extension's own chat its computer no longer lists: %d %s", code, body)
	}
	if got := sessionStarts(b, own); len(got) != 1 || !strings.Contains(got[0], `"resume_outside_health":true`) {
		t.Fatalf("the resume of the unlisted chat reached the computer as %v, want once with resume_outside_health", got)
	}
	if code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume("b1"), nil); code != http.StatusOK {
		t.Fatalf("a listed chat: %d %s", code, body)
	}
	if got := sessionStarts(b, "b1"); len(got) != 1 || strings.Contains(got[0], "resume_outside_health") {
		t.Fatalf("the resume of a listed chat reached the computer as %v, want once and unmarked", got)
	}

	codeHealth, bodyHealth := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume("bh"), nil)
	if codeHealth != http.StatusNotFound || len(sessionStarts(b, "bh")) != 0 {
		t.Fatalf("a chat listed in the space: %d %s, reached the computer %d times", codeHealth, bodyHealth, len(sessionStarts(b, "bh")))
	}
	codeForged, bodyForged := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume(forged), nil)
	if codeForged != codeHealth || bodyForged != bodyHealth {
		t.Fatalf("refused by the computer: %d %s, want what a chat listed in the space gets, %d %s", codeForged, bodyForged, codeHealth, bodyHealth)
	}
	if !strings.Contains(bodyForged, `"code":"not_found"`) || !strings.Contains(bodyForged, `"retryable":false`) {
		t.Fatalf("refused by the computer: %s", bodyForged)
	}

	if code, body := ownerDo(t, h, url, http.MethodPost, "/api/v1/sessions", `{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"own-2"}`, nil); code != http.StatusOK {
		t.Fatalf("the owner's resume of an unlisted chat: %d %s", code, body)
	}
	if got := sessionStarts(b, "own-2"); len(got) != 1 || strings.Contains(got[0], "resume_outside_health") {
		t.Fatalf("the owner's resume reached the computer as %v, want once and unmarked", got)
	}
}

// Minor 2 of the last #10941 review: right after a hub start no list of a computer's sessions is in yet, and an
// extension's resume of a chat that list will have was told 404 not_found, retryable false, which reads as deleted.
// On a node without the check it is told 503 sessions_not_listed, retryable, until the list comes; then a chat the list
// does not have is 404 again. While the hub has no word of the node at all (a devices manager that restarted without
// its warm file reports a zero node until the node connects again) it cannot tell an older node from a newer one,
// so a chat the list does not have stays 503 too. A node with the check is asked at once: it knows its own chats.
func TestAnExtensionsResumeBeforeTheComputersListIsRetryable(t *testing.T) {
	answer := func(string) (any, error) {
		return map[string]any{"session": map[string]any{"session_id": "started"}}, nil
	}
	resume := `{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"b1","prompt":"go on"}`
	for _, caps := range [][]string{nil, {nodeproto.Sessions}} {
		h, url, b := extensionResumeHub(t, caps, answer)
		code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume, map[string]string{"Accept-Language": "en"})
		if code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"sessions_not_listed"`) ||
			!strings.Contains(body, `"retryable":true`) || !strings.Contains(body, "haven’t come in yet") {
			t.Fatalf("node %v, no list yet: %d %s", caps, code, body)
		}
		if n := len(sessionStarts(b, "b1")); n != 0 {
			t.Fatalf("node %v, no list yet: reached the computer %d times", caps, n)
		}
		h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{
			{MachineID: "mb", SessionID: "b1", Title: "B-SESSION", Cwd: "/work/project", Status: proto.StatusOffline},
		}})
		eventually(t, 5*time.Second, "mb's sessions", func() bool { _, ok := h.localMachine("mb").session("b1"); return ok })
		if code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume, nil); code != http.StatusOK {
			t.Fatalf("node %v, once listed: %d %s", caps, code, body)
		}
		code, body = extDo(t, url, http.MethodPost, "/api/v1/sessions", strings.Replace(resume, `"b1"`, `"b2"`, 1), nil)
		switch {
		case caps == nil && (code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"sessions_not_listed"`) || !strings.Contains(body, `"retryable":true`)):
			t.Fatalf("no word of the node, a chat the list does not have: %d %s", code, body)
		case caps != nil && (code != http.StatusNotFound || !strings.Contains(body, `"retryable":false`)):
			t.Fatalf("node %v, a chat the list does not have: %d %s", caps, code, body)
		}
		if n := len(sessionStarts(b, "b2")); n != 0 {
			t.Fatalf("node %v, a chat the list does not have: reached the computer %d times", caps, n)
		}
	}
	_, url, b := extensionResumeHub(t, []string{nodeproto.Sessions, nodeproto.ResumeOutsideHealth}, answer)
	if code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume, nil); code != http.StatusOK || len(sessionStarts(b, "b1")) != 1 {
		t.Fatalf("a node with the check, no list yet: %d %s, %v", code, body, sessionStarts(b, "b1"))
	}
}

// The gate attack on #10941's resume rule, its finding 2: the hub decided an extension's resume before the request's
// other checks, so a listed health chat was answered 404 there while an id it does not list went on to them, and a
// request one of them refuses (here an agent type that is no type) told the two apart. The resume is decided last
// now: such a request gets the same answer whichever conversation it names, and none reaches the computer.
func TestAnExtensionsResumeIsDecidedAfterTheRequestsOtherChecks(t *testing.T) {
	h, url, b := extensionResumeHub(t, []string{nodeproto.Sessions, nodeproto.ResumeOutsideHealth}, func(string) (any, error) {
		return nil, &rpc.Error{Code: proto.CodeResumeUnknown, Message: "这台电脑上没有这个对话的记录，无法在这里接着它。这次没有启动。"} // i18n-ignore: the node's words
	})
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{
		{MachineID: "mb", SessionID: "b1", Title: "B-SESSION", Cwd: "/work/project", Status: proto.StatusOffline},
		{MachineID: "mb", SessionID: "bh", Title: "HEALTH-SESSION", Cwd: "/home/me/.persona/health", Assistant: true, Space: "health"},
	}})
	eventually(t, 5*time.Second, "mb's sessions", func() bool { _, ok := h.localMachine("mb").session("bh"); return ok })
	const unlisted = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	answers := map[string]string{}
	for _, id := range []string{"bh", unlisted, "b1"} {
		code, body := extDo(t, url, http.MethodPost, "/api/v1/sessions",
			`{"machine_id":"mb","launcher":"claude","agent_type":"no such type!","cwd":"/work/project","resume_session_id":"`+id+`","prompt":"go on"}`, nil)
		answers[id] = fmt.Sprintf("%d %s", code, body)
		if code != http.StatusBadRequest {
			t.Errorf("a resume of %s with an agent type that is no type: %s", id, answers[id])
		}
		if n := len(sessionStarts(b, id)); n != 0 {
			t.Errorf("a resume of %s with an agent type that is no type reached the computer %d times", id, n)
		}
	}
	if answers["bh"] != answers[unlisted] || answers["bh"] != answers["b1"] {
		t.Fatalf("the answers tell the conversations apart: %v", answers)
	}
	// Well formed, the health chat and the id the computer refuses still get the same 404.
	codeHealth, bodyHealth := extDo(t, url, http.MethodPost, "/api/v1/sessions",
		`{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"bh","prompt":"go on"}`, nil)
	codeUnlisted, bodyUnlisted := extDo(t, url, http.MethodPost, "/api/v1/sessions",
		`{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"`+unlisted+`","prompt":"go on"}`, nil)
	if codeHealth != http.StatusNotFound || codeHealth != codeUnlisted || bodyHealth != bodyUnlisted {
		t.Fatalf("well formed: the health chat %d %s, the unlisted id %d %s", codeHealth, bodyHealth, codeUnlisted, bodyUnlisted)
	}
}

// While a computer's engine runtime is away (Paseo restarting its bridge), its node answers any resume engine_missing,
// the extension's marked one of an id it does not know too (node resumeHostRefusal, #10941): the hub passes that on
// as the owner gets it, 409 engine_missing, never as its own 404, which reads as a chat that does not exist.
func TestAnExtensionsResumeWhileTheEngineIsAwayIsTheOwnersAnswer(t *testing.T) {
	const away = "这台电脑的引擎是 Paseo，但 Paseo 现在没有连上 Persona（没在运行，或刚启动还没连上），不能在这里新开或接着会话，这次没有发送。" // i18n-ignore: the node's words
	h, url, b := extensionResumeHub(t, []string{nodeproto.Sessions, nodeproto.ResumeOutsideHealth}, func(string) (any, error) {
		return nil, &rpc.Error{Code: "engine_missing", Message: away}
	})
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{}})
	eventually(t, 5*time.Second, "mb's list", func() bool { return h.localMachine("mb").hadList() })
	resume := `{"machine_id":"mb","launcher":"paseo-claude","cwd":"/work/project","resume_session_id":"7a1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a40","prompt":"go on"}`
	extCode, extBody := extDo(t, url, http.MethodPost, "/api/v1/sessions", resume, nil)
	ownerCode, ownerBody := ownerDo(t, h, url, http.MethodPost, "/api/v1/sessions", resume, nil)
	if extCode != http.StatusConflict || !strings.Contains(extBody, `"code":"engine_missing"`) || extCode != ownerCode || extBody != ownerBody {
		t.Fatalf("the extension's resume while the engine is away: %d %s; the owner's: %d %s", extCode, extBody, ownerCode, ownerBody)
	}
	if starts := sessionStarts(b, "7a1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a40"); len(starts) != 2 || !strings.Contains(starts[0], `"resume_outside_health":true`) {
		t.Fatalf("starts sent: %v", starts)
	}
}

// Minors 3 and 4 of the last #10941 review: the owner's resume a computer refuses with resume_unknown is worded in the
// request's language (it reached English and Japanese owners in Chinese), and is not told to the manager as a failed
// start, which a manager from before the refusal counted against the launcher.
func TestResumeUnknownIsWordedForTheOwnerAndNotALauncherFailure(t *testing.T) {
	h, url, b := extensionResumeHub(t, []string{nodeproto.Sessions, nodeproto.ResumeOutsideHealth}, func(params string) (any, error) {
		if strings.Contains(params, `"resume_session_id":"gone"`) {
			return nil, &rpc.Error{Code: proto.CodeResumeUnknown, Message: "这台电脑上没有这个对话的记录，无法在这里接着它。这次没有启动。"} // i18n-ignore: the node's words
		}
		return nil, &rpc.Error{Code: "launcher_missing", Message: "no such launcher"}
	})
	var mu sync.Mutex
	var ended []string
	b.mu.Lock()
	b.onEvent = func(method string, params json.RawMessage) {
		if method == assistantwire.EventStartEnded {
			mu.Lock()
			ended = append(ended, string(params))
			mu.Unlock()
		}
	}
	b.mu.Unlock()
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "mb", Sessions: []proto.Session{}})
	for lang, want := range map[string]string{"en": "has no record of that conversation", "ja": "この会話の記録がない", "zh-Hans": "没有这个对话的记录"} {
		code, body := ownerDo(t, h, url, http.MethodPost, "/api/v1/sessions", `{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"gone"}`,
			map[string]string{"Accept-Language": lang})
		if code != http.StatusConflict || !strings.Contains(body, `"code":"resume_unknown"`) || !strings.Contains(body, want) {
			t.Errorf("%s: %d %s", lang, code, body)
		}
	}
	// A failed start that is the launcher's is still told, which also says the events above had time to come.
	_, _ = ownerDo(t, h, url, http.MethodPost, "/api/v1/sessions", `{"machine_id":"mb","launcher":"claude","cwd":"/work/project","resume_session_id":"other"}`, nil)
	eventually(t, 5*time.Second, "the launcher's failure told", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ended) > 0
	})
	mu.Lock()
	defer mu.Unlock()
	for _, e := range ended {
		if strings.Contains(e, proto.CodeResumeUnknown) {
			t.Fatalf("a resume_unknown refusal was told to the manager: %v", ended)
		}
	}
}
