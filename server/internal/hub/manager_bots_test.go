package hub

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestManagerBotConflicts(t *testing.T) {
	feishu := extwire.BotIdentity{Kind: "feishu", ID: "cli_example"}
	wechat := extwire.BotIdentity{Kind: "wechat", ID: "example-account"}
	h := newTestHub(t)
	h.keepExtReport("assistant", &extwire.Report{BotIdentities: []extwire.BotIdentity{feishu, feishu, wechat}})
	roster := []managerInfo{{ID: "assistant", Online: true}}
	if got := h.managerBotConflicts(roster); len(got) != 0 {
		t.Fatalf("one manager, even with repeated identities: %+v", got)
	}
	roster = append(roster, managerInfo{ID: "m-work"}) // offline: use its last report
	h.keepExtReport("m-work", &extwire.Report{BotIdentities: []extwire.BotIdentity{feishu, wechat}})
	want := []managerBotConflict{{Platform: "feishu", Managers: []string{"assistant", "m-work"}}, {Platform: "wechat", Managers: []string{"assistant", "m-work"}}}
	if got := h.managerBotConflicts(roster); !reflect.DeepEqual(got, want) {
		t.Fatalf("shared identities: %+v, want %+v", got, want)
	}
	h.keepExtReport("m-removed", h.extReportOf("assistant"))
	h.keepExtReport("m-installing", h.extReportOf("assistant"))
	now := time.Now()
	roster = append(roster, managerInfo{ID: "m-installing", Installing: &now}, managerInfo{ID: "m-old"})
	if got := h.managerBotConflicts(roster); !reflect.DeepEqual(got, want) {
		t.Fatalf("removed, installing and old managers must not contribute: %+v", got)
	}
	h.keepExtReport("m-work", &extwire.Report{BotIdentities: []extwire.BotIdentity{
		{Kind: "feishu", ID: "different-app"}, {Kind: "wechat", ID: feishu.ID},
		{Kind: "feishu", ID: " "}, {Kind: "other", ID: feishu.ID},
	}})
	if got := h.managerBotConflicts(roster); len(got) != 0 {
		t.Fatalf("different identities and platforms must not match: %+v", got)
	}
	h.keepExtReport("m-work", &extwire.Report{BotIdentities: []extwire.BotIdentity{feishu}})
	h.forgetManagerState("m-work")
	if got := h.managerBotConflicts(roster); len(got) != 0 {
		t.Fatalf("revoked manager's report remains: %+v", got)
	}
}

func TestManagerBotReportsRefreshRoster(t *testing.T) {
	h := newTestHub(t)
	asked := &client{device: "example-device", out: make(chan []byte, 64)}
	addTestClient(h, asked)
	h.onClientMessage(context.Background(), asked, []byte(`{"type":"client_hello","features":["managers"]}`))
	<-asked.out
	c := &busConn{ident: db.BusIdentity{ID: "m-work"}}
	report := func(body string) bool {
		t.Helper()
		busEvents[extwire.EventReport](h, c, json.RawMessage(body))
		heard := false
		for len(asked.out) > 0 {
			if raw := <-asked.out; string(raw) == `{"type":"managers"}` {
				heard = true
			}
		}
		return heard
	}
	if report(`{}`) {
		t.Fatal("legacy report with no identities changed the roster")
	}
	const bound = `{"bot_identities":[{"kind":"wechat","id":"example-account"}]}`
	if !report(bound) {
		t.Fatal("binding did not refresh the roster")
	}
	if report(bound) {
		t.Fatal("unchanged report refreshed the roster")
	}
	if !report(`{}`) {
		t.Fatal("unbinding did not clear the warning")
	}
}

func TestManagersAPIReportsBotConflicts(t *testing.T) {
	h, srv, _ := managersHub(t)
	busToken(t, h, "assistant", "Assistant")
	busToken(t, h, "m-work", "Work")
	report := &extwire.Report{BotIdentities: []extwire.BotIdentity{{Kind: "feishu", ID: "cli_example_private_identity"}}}
	h.keepExtReport("assistant", report)
	h.keepExtReport("m-work", report)
	read := func() map[string]json.RawMessage {
		t.Helper()
		var out map[string]json.RawMessage
		if code := managersDo(t, h, srv, "GET", "/api/v1/managers", nil, &out); code != 200 {
			t.Fatalf("list: %d", code)
		}
		return out
	}
	out := read()
	var conflicts []managerBotConflict
	if err := json.Unmarshal(out["bot_conflicts"], &conflicts); err != nil || len(conflicts) != 1 || !reflect.DeepEqual(conflicts[0].Managers, []string{"assistant", "m-work"}) {
		t.Fatalf("conflicts: %+v (%v)", out, err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "cli_example_private_identity") {
		t.Fatal("roster exposes a raw bot identity")
	}
	h.keepExtReport("m-work", &extwire.Report{})
	if _, exists := read()["bot_conflicts"]; exists {
		t.Fatal("no conflicts should preserve the original response shape")
	}
}
