package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The assistant's cloud tools against the provider program of TestCloudRoutes: hidden until a provider is on, the
// hosts with their computers' names, a read action at once, a confirm action only after the card was allowed (then
// asked and audited as the assistant), a declined or unanswered card that asks the program nothing, the destructive
// card's wording, and start-and-reconnect passed through.
func TestCloudTools(t *testing.T) {
	fastCloudRecovery(t)
	h := newTestManager(t)
	store := cloudStore{newExtensionStore()}
	h.store = store
	devs := &cloudDevices{online: map[string]bool{"mac-spot": false, "mac-run": true},
		comesBack: map[string]bool{"mac-spot": true}}
	h.opts.Devices = devs
	if h.cloudTools() != nil {
		t.Fatal("cloud tools offered with no provider")
	}
	if known, readOnly := h.ToolAccess("cloud_hosts"); !known || !readOnly {
		t.Errorf("cloud_hosts access %v %v", known, readOnly)
	}
	if known, readOnly := h.ToolAccess("cloud_host_action"); !known || readOnly {
		t.Errorf("cloud_host_action access %v %v", known, readOnly)
	}
	startTestExtensions(t, h)
	mux := h.routes()
	master := h.cfg.Token
	out := filepath.Join(t.TempDir(), "calls.jsonl")
	c, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+cloudPackage(t, out)+`"}`)
	if c != 200 {
		t.Fatalf("preview %d %v", c, pv)
	}
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	waitLong(t, "the provider answering", func() bool {
		c, body := call(t, mux, "GET", "/api/v1/extensions/-/cloud/instances", master, "")
		list, _ := body["hosts"].([]any)
		return c == 200 && len(list) == 3
	})
	tools := h.cloudTools()
	if len(tools) != 2 || tools[0].Name != "cloud_hosts" || tools[1].Name != "cloud_host_action" {
		t.Fatalf("tools %+v", tools)
	}
	for _, mid := range []string{"mac-run", "mac-spot"} {
		inst := strings.Replace(mid, "mac-", "i-", 1)
		if c, body := call(t, mux, "PUT", "/api/v1/machines/"+mid+"/cloud", master,
			fmt.Sprintf(`{"extension": "example-hosts", "provider": "main", "account": "acct", "instance": %q}`, inst)); c != 200 {
			t.Fatalf("bind %s %d %v", mid, c, body)
		}
	}
	ctx := i18n.With(context.Background(), i18n.En)
	tool := func(name, args string) proto.ToolResult {
		return h.callCloudTool(ctx, "conv1", name, json.RawMessage(args))
	}
	text := func(r proto.ToolResult) string { return r.Content[0].Text }

	// cloud_hosts: every host, the bound ones with their computer's name; one computer's by its name.
	res := tool("cloud_hosts", `{}`)
	var hosts struct {
		Hosts []struct {
			MachineID string `json:"machine_id"`
			Machine   string `json:"machine"`
			Instance  struct {
				ID string `json:"id"`
			} `json:"instance"`
		} `json:"hosts"`
		Problems []map[string]any `json:"problems"`
	}
	if res.IsError || json.Unmarshal([]byte(text(res)), &hosts) != nil || len(hosts.Hosts) != 3 || len(hosts.Problems) != 1 {
		t.Fatalf("cloud_hosts %+v", res)
	}
	named := map[string]string{}
	for _, host := range hosts.Hosts {
		named[host.Instance.ID] = host.MachineID + "/" + host.Machine
	}
	if named["i-run"] != "mac-run/Server mac-run" || named["i-spot"] != "mac-spot/Server mac-spot" || named["i-stuck"] != "/" {
		t.Fatalf("hosts' computers %v", named)
	}
	res = tool("cloud_hosts", `{"machine": "Server mac-run"}`)
	if res.IsError || !strings.Contains(text(res), `"machine_id":"mac-run"`) || !strings.Contains(text(res), `"bound":true`) ||
		!strings.Contains(text(res), `"i-run"`) {
		t.Fatalf("one computer %+v", res)
	}
	if res := tool("cloud_hosts", `{"machine": "nowhere"}`); !res.IsError {
		t.Fatalf("an unknown computer %+v", res)
	}

	// A read action runs at once, with no card; snapshot_list answers the snapshots.
	before := cloudToolActions(t, out)
	res = tool("cloud_host_action", `{"machine": "mac-run", "action": "sync_ip"}`)
	if res.IsError || !strings.Contains(text(res), "198.51.100.7") || cloudToolActions(t, out) != before+1 {
		t.Fatalf("sync_ip %+v", res)
	}
	if res := tool("cloud_host_action", `{"machine": "mac-spot", "action": "snapshot_list"}`); res.IsError || !strings.Contains(text(res), "snap-0") {
		t.Fatalf("snapshot_list %+v", res)
	}
	if res := tool("cloud_host_action", `{"machine": "mac-run", "action": "start", "id": "x"}`); !res.IsError || !strings.HasPrefix(text(res), "bad_request") {
		t.Fatalf("a bad request %+v", res)
	}
	// The model's arguments are held to CloudActionRequest as the routes' are: a name longer than 80 or on two lines is
	// refused before any card, so the name a card shows is the one the program would get.
	before = cloudToolActions(t, out)
	for _, name := range []string{strings.Repeat("n", 81), "nightly\\nrun"} {
		res := tool("cloud_host_action", `{"machine": "mac-run", "action": "snapshot_create", "name": "`+name+`"}`)
		if !res.IsError || !strings.HasPrefix(text(res), "bad_request") {
			t.Fatalf("snapshot named %q: %+v", name, res)
		}
	}
	select {
	case c := <-deskOf(h).ch:
		t.Fatalf("a card for a bad request: %+v", c)
	default:
	}
	if cloudToolActions(t, out) != before {
		t.Fatal("the program heard a bad request")
	}
	// An action the instance does not offer now (start on a running one) is refused before any card: nobody is asked
	// to approve what would be refused, and the program hears no action and nothing is audited.
	rows := len(store.audits())
	if res := tool("cloud_host_action", `{"machine": "mac-run", "action": "start"}`); !res.IsError || !strings.HasPrefix(text(res), "unsupported") {
		t.Fatalf("an action not offered %+v", res)
	}
	select {
	case c := <-deskOf(h).ch:
		t.Fatalf("a card for an action not offered: %+v", c)
	default:
	}
	if cloudToolActions(t, out) != before || len(store.audits()) != rows {
		t.Fatalf("an action not offered reached the program or the audit: %d calls, %d rows", cloudToolActions(t, out)-before, len(store.audits())-rows)
	}

	// A confirm action: the card names the action, the computer and the instance; allowed, the program is asked and
	// the write is audited as the assistant.
	done := make(chan proto.ToolResult, 1)
	before = cloudToolActions(t, out)
	go func() { done <- tool("cloud_host_action", `{"machine": "mac-run", "action": "stop"}`) }()
	card := answerCard(t, h, true, false)
	l := h.userLang()
	want := i18n.T(l, "hub-extension_cloud.card_confirm", i18n.Args{"instance": "i-run", "machine": "Server mac-run",
		"action": i18n.T(l, "hub-extension_cloud.action_stop")})
	if card.ToolName != "cloud_host_action" || card.Summary != want || !strings.Contains(string(card.ToolInput), `"safety":"confirm"`) {
		t.Fatalf("card %q %s", card.Summary, card.ToolInput)
	}
	if res := <-done; res.IsError || !strings.Contains(text(res), `"status":"accepted"`) || cloudToolActions(t, out) != before+1 {
		t.Fatalf("stop %+v", res)
	}
	audited := false
	for _, row := range store.audits() {
		if row.action == auditCloudAction && row.actor == actorAssistant && strings.Contains(string(row.detail), `"stop"`) &&
			strings.Contains(string(row.detail), `"accepted"`) {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("no assistant audit row in %+v", store.audits())
	}

	// Declined, or nobody answered: the program hears nothing and nothing more is audited.
	rows = len(store.audits())
	before = cloudToolActions(t, out)
	go func() { done <- tool("cloud_host_action", `{"machine": "mac-run", "action": "reboot"}`) }()
	answerCard(t, h, false, false)
	if res := <-done; !res.IsError || !strings.Contains(text(res), "declined") {
		t.Fatalf("declined %+v", res)
	}
	go func() { done <- tool("cloud_host_action", `{"machine": "mac-run", "action": "reboot"}`) }()
	select {
	case c := <-deskOf(h).ch:
		c.answer <- proto.Resolution{Dismiss: true, Reason: proto.DismissedExpired}
	case <-time.After(5 * time.Second):
		t.Fatal("no card")
	}
	if res := <-done; !res.IsError || strings.Contains(text(res), "declined") {
		t.Fatalf("unanswered %+v", res)
	}
	if cloudToolActions(t, out) != before || len(store.audits()) != rows {
		t.Fatalf("a refused card reached the program or the audit: %d calls, %d rows", cloudToolActions(t, out)-before, len(store.audits())-rows)
	}

	// A destructive action is worded as destructive and, allowed, acknowledged with the instance's id.
	go func() { done <- tool("cloud_host_action", `{"machine": "mac-run", "action": "force_stop"}`) }()
	card = answerCard(t, h, true, false)
	want = i18n.T(l, "hub-extension_cloud.card_destructive", i18n.Args{"instance": "i-run", "machine": "Server mac-run",
		"action": i18n.T(l, "hub-extension_cloud.action_force_stop")})
	if card.Summary != want || !strings.Contains(string(card.ToolInput), `"safety":"destructive"`) {
		t.Fatalf("destructive card %q", card.Summary)
	}
	if res := <-done; res.IsError || !strings.Contains(text(res), `"status":"done"`) {
		t.Fatalf("force_stop %+v", res)
	}

	// A custom action's card carries the manifest's sentence.
	go func() {
		done <- tool("cloud_host_action", `{"machine": "mac-spot", "action": "custom", "id": "renew"}`)
	}()
	card = answerCard(t, h, true, false)
	if !strings.Contains(card.Summary, "Renew") || !strings.HasSuffix(card.Summary, "\nRenew this server for another term?") ||
		!strings.Contains(card.Summary, "Spot box (i-spot)") {
		t.Fatalf("custom card %q", card.Summary)
	}
	if res := <-done; res.IsError {
		t.Fatalf("renew %+v", res)
	}

	// Start and reconnect: the card says so, and the recovery runs to the end.
	go func() {
		done <- tool("cloud_host_action", `{"machine": "mac-spot", "action": "start", "reconnect": true}`)
	}()
	card = answerCard(t, h, true, false)
	if !strings.HasSuffix(card.Summary, "\n"+i18n.T(l, "hub-extension_cloud.card_reconnect", i18n.Args{"machine": "Server mac-spot"})) {
		t.Fatalf("reconnect card %q", card.Summary)
	}
	if res := <-done; res.IsError || !strings.Contains(text(res), `"recovery"`) {
		t.Fatalf("start and reconnect %+v", res)
	}
	waitLong(t, "the recovery to end", func() bool {
		rec := h.cloudRecoveryOf("mac-spot")
		return rec != nil && rec.Phase == "done"
	})

	// The assistant cannot bind: there is no such action, and nothing changed.
	if res := tool("cloud_host_action", `{"machine": "mac-run", "action": "bind"}`); !res.IsError {
		t.Fatalf("bind %+v", res)
	}
	// Switched off, the tools go away.
	if c, body := call(t, mux, "PUT", "/api/v1/extensions/example-hosts/-/enabled", master, `{"enabled": false}`); c != 200 {
		t.Fatalf("disable %d %v", c, body)
	}
	if h.cloudTools() != nil {
		t.Fatal("cloud tools offered with the provider off")
	}
}

// cloudToolActions counts the cloud.action calls the program heard (its handler is told the method without the
// namespace).
func cloudToolActions(t *testing.T, out string) int {
	n := 0
	for _, c := range offloadCalls(t, out) {
		if c["method"] == "action" || c["method"] == "cloud.action" {
			n++
		}
	}
	return n
}
