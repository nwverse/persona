package hub

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
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
