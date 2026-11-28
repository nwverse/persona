package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// serveCloudExtensionForTest is example-hosts' program: two cloud_hosts providers, "main" and "broken" (whose list
// always fails with not_signed_in), writing each call's method and params as a line of JSON to CLOUD_OUT. Its
// instances live in its memory: a start turns one starting, and the next read finds it running at a new address.
func serveCloudExtensionForTest() {
	out := os.Getenv("CLOUD_OUT")
	var mu sync.Mutex
	note := func(method string, params json.RawMessage) {
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			b, _ := json.Marshal(map[string]any{"method": method, "params": params})
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
	insts := map[wire.CloudInstanceID]*wire.CloudInstance{
		"i-spot": {Account: "acct", ID: "i-spot", DisplayName: "Spot\nbox", State: wire.CloudInstanceStatePreempted,
			PublicIps:  []string{"203.0.113.10", "not-an-ip", "fe80::1%eth0", "203.0.113.10"},
			ConsoleURL: "https://user:secret@console.example.com/i-spot", Lifecycle: &wire.CloudLifecycle{Spot: true},
			Actions:       []wire.CloudActionKind{"start", "custom", "snapshot_list", "sync_ip", "console"},
			CustomActions: []wire.CloudCustomActionID{"renew", "probe", "undeclared"}},
		"i-run": {Account: "acct", ID: "i-run", State: wire.CloudInstanceStateRunning, PublicIps: []string{"198.51.100.7"},
			ConsoleURL: "https://console.example.com/i-run",
			Actions:    []wire.CloudActionKind{"stop", "reboot", "force_stop", "snapshot_create", "sync_ip", "console"}},
		"i-stuck": {Account: "acct", ID: "i-stuck", State: wire.CloudInstanceStateStopped, PublicIps: []string{"192.0.2.5"},
			Actions: []wire.CloudActionKind{"start"}},
	}
	ids := []wire.CloudInstanceID{"i-spot", "i-run", "i-stuck"}
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "example-hosts", Version: "1.0.0",
		Namespaces: map[string]pserve.Handler{
			"cloud": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				note(method, params)
				var p struct {
					Contribution string                   `json:"contribution"`
					Instance     wire.CloudInstanceRef    `json:"instance"`
					Action       wire.CloudActionKind     `json:"action"`
					Custom       wire.CloudCustomActionID `json:"custom"`
					Name         string                   `json:"name"`
				}
				_ = json.Unmarshal(params, &p)
				if p.Contribution == "broken" {
					return nil, &rpc.Error{Code: string(wire.CloudErrorCodeNotSignedIn), Message: "The API key was refused."}
				}
				switch p.Instance.ID {
				case "i-garbled":
					return map[string]any{"account": "acct", "id": "i-garbled", "state": "exploded"}, nil
				case "i-wrongid":
					return wire.CloudInstance{Account: "acct", ID: "i-other", State: wire.CloudInstanceStateRunning}, nil
				}
				inst := insts[p.Instance.ID]
				switch method {
				case "instances":
					var list wire.CloudInstanceList
					for _, id := range ids {
						list.Instances = append(list.Instances, *insts[id])
					}
					return list, nil
				case "instance":
					if inst == nil {
						return nil, &rpc.Error{Code: string(wire.CloudErrorCodeNotFound), Message: "No such instance."}
					}
					now := *inst
					if inst.State == wire.CloudInstanceStateStarting {
						inst.State = wire.CloudInstanceStateRunning
						if inst.ID == "i-spot" {
							inst.PublicIps = []string{"203.0.113.20"}
						}
					}
					return now, nil
				case "snapshots":
					return wire.CloudSnapshotList{Snapshots: []wire.CloudSnapshot{{ID: "snap-0", Name: "Before\tupgrade", State: wire.CloudSnapshotStateReady}}}, nil
				case "action":
					switch p.Action {
					case "start":
						inst.State = wire.CloudInstanceStateStarting
						return wire.CloudActionResult{Status: wire.CloudActionStatusAccepted}, nil
					case "stop":
						return wire.CloudActionResult{Status: wire.CloudActionStatusAccepted}, nil
					case "force_stop":
						return wire.CloudActionResult{Status: wire.CloudActionStatusDone}, nil
					case "sync_ip":
						return wire.CloudActionResult{Status: wire.CloudActionStatusDone, Instance: inst}, nil
					case "console":
						if inst.ID == "i-spot" {
							return wire.CloudActionResult{Status: wire.CloudActionStatusDone, URL: "http://console.example.com/plain"}, nil
						}
						return wire.CloudActionResult{Status: wire.CloudActionStatusDone, URL: "https://console.example.com/signed?t=1"}, nil
					case "custom":
						if p.Custom == "probe" && inst.ID == "i-spot" {
							inst.PublicIps = nil // the vendor took the address back while the instance is preempted
						}
						return wire.CloudActionResult{Status: wire.CloudActionStatusDone, Message: "Renewed\nuntil next year (" + string(p.Custom) + ")"}, nil
					case "snapshot_create":
						switch p.Name {
						case "refuse":
							return wire.CloudActionResult{Status: wire.CloudActionStatusRefused, Message: "Quota of snapshots reached."}, nil
						case string(wire.CloudErrorCodeCapacity), string(wire.CloudErrorCodeNotSignedIn), string(wire.CloudErrorCodeInvalidState),
							string(wire.CloudErrorCodeRateLimited):
							return nil, &rpc.Error{Code: p.Name, Message: "The vendor said " + p.Name}
						case "explode":
							return nil, &rpc.Error{Code: string(wire.CloudErrorCodeFailed), Message: "Internal\nerror"}
						}
						return wire.CloudActionResult{Status: wire.CloudActionStatusDone,
							Snapshot: &wire.CloudSnapshot{ID: "snap-1", Name: p.Name, State: wire.CloudSnapshotStateCreating}}, nil
					}
				}
				return nil, pserve.UnknownMethod(method)
			}}})
}

// cloudPackage is example-hosts' package, its calls written to out.
func cloudPackage(t *testing.T, out string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "hosts"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=cloud exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": "example-hosts", "name": "Example Hosts", "version": "1.0.0",
		"activation_events": ["on_startup"], "engines": {"plugin_protocol": 1},
		"runtime": {"command": {%q: "bin/hosts"}, "env": {"CLOUD_OUT": %q}},
		"contributes": {"cloud_hosts": [
			{"id": "main", "name": "Example Hosts", "vendor": "examplehosts", "custom_actions": [
				{"id": "renew", "title": "Renew", "safety": "confirm", "confirm": "Renew this server for another term?"},
				{"id": "probe", "title": "Keep-alive check", "safety": "read"}]},
			{"id": "broken", "name": "Broken Hosts", "vendor": "brokenhosts"}]}}`,
		runtime.GOOS+"-"+runtime.GOARCH, out)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

// cloudDevices are the computers as the devices list them; Reconnect brings one online when it may come back.
type cloudDevices struct {
	mu         sync.Mutex
	online     map[string]bool
	comesBack  map[string]bool
	reconnects []string
}

func (d *cloudDevices) Has(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.online[id]
	return ok
}

func (d *cloudDevices) CallNode(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("no node here")
}

func (d *cloudDevices) Views() []devices.View {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []devices.View
	for _, id := range slices.Sorted(func(yield func(string) bool) {
		for id := range d.online {
			if !yield(id) {
				return
			}
		}
	}) {
		out = append(out, devices.View{Summary: devices.Summary{ID: id, Name: "Server " + id, Online: d.online[id]}})
	}
	return out
}

func (d *cloudDevices) Reconnect(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reconnects = append(d.reconnects, id)
	if d.comesBack[id] {
		d.online[id] = true
	}
	return nil
}

func (d *cloudDevices) remove(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.online, id)
}

func (d *cloudDevices) reconnected() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.reconnects)
}

// cloudStore is the extensions' test store that also reads the audit back.
type cloudStore struct{ *extensionStore }

func (s cloudStore) ExtensionAudits(_ context.Context, plugin string, limit int) ([]db.ExtensionAudit, error) {
	var out []db.ExtensionAudit
	rows := s.audits()
	at := time.Now()
	for i := len(rows) - 1; i >= 0 && len(out) < limit; i-- {
		if plugin == "" || rows[i].plugin == plugin {
			out = append(out, db.ExtensionAudit{Plugin: rows[i].plugin, Action: rows[i].action, Actor: rows[i].actor, Detail: rows[i].detail,
				At: at.Add(time.Duration(i) * time.Millisecond)})
		}
	}
	return out, nil
}

// cloudActionCalls counts the cloud.action calls the program heard (its handler is told the method without the
// namespace).
func cloudActionCalls(t *testing.T, out string) int {
	n := 0
	for _, c := range offloadCalls(t, out) {
		if c["method"] == "action" || c["method"] == "cloud.action" {
			n++
		}
	}
	return n
}

// cloudFollowing counts the recoveries' goroutines running now (cloudFollow, named through a method expression so a
// rename fails to compile instead of counting nothing).
func cloudFollowing() int {
	name := runtime.FuncForPC(reflect.ValueOf((*Manager).cloudFollow).Pointer()).Name()
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, name+"(") {
			count++
		}
	}
	return count
}

func fastCloudRecovery(t *testing.T) {
	old := []time.Duration{cloudStartWait, cloudReconnectWait, cloudStartPoll, cloudOnlinePoll, cloudReconnectEvery}
	cloudStartWait, cloudReconnectWait, cloudStartPoll, cloudOnlinePoll, cloudReconnectEvery =
		3*time.Second, 300*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() {
		cloudStartWait, cloudReconnectWait, cloudStartPoll, cloudOnlinePoll, cloudReconnectEvery = old[0], old[1], old[2], old[3], old[4]
	})
}

// The cloud routes end to end against a provider program: listing, binding, the gates, the actions with every
// re-check, snapshots, the audit, start-and-reconnect, and a provider switched off.
func TestCloudRoutes(t *testing.T) {
	fastCloudRecovery(t)
	h := newTestManager(t)
	store := cloudStore{newExtensionStore()}
	h.store = store
	devs := &cloudDevices{online: map[string]bool{"mac-spot": false, "mac-run": true, "mac-other": true, "mac-stuck": false},
		comesBack: map[string]bool{"mac-spot": true}}
	h.opts.Devices = devs
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
	var hosts map[string]any
	waitLong(t, "the provider answering", func() bool {
		c, body := call(t, mux, "GET", "/api/v1/extensions/-/cloud/instances", master, "")
		hosts = body
		list, _ := body["hosts"].([]any)
		return c == 200 && len(list) == 3
	})

	// Providers: the enabled contributions, no program asked.
	before := len(offloadCalls(t, out))
	c, providers := call(t, mux, "GET", "/api/v1/extensions/-/cloud", master, "")
	if c != 200 || fmt.Sprint(providers["providers"]) != "[map[extension:example-hosts id:main name:Example Hosts vendor:examplehosts] "+
		"map[extension:example-hosts id:broken name:Broken Hosts vendor:brokenhosts]]" || len(offloadCalls(t, out)) != before {
		t.Fatalf("providers %d %v", c, providers)
	}
	// Instances: the failing provider is a problem, the other's instances are listed, re-checked.
	problems, _ := hosts["problems"].([]any)
	if len(problems) != 1 || problems[0].(map[string]any)["code"] != "not_signed_in" || problems[0].(map[string]any)["provider"] != "broken" ||
		problems[0].(map[string]any)["error"] == "" {
		t.Fatalf("problems %v", hosts["problems"])
	}
	spot := hosts["hosts"].([]any)[0].(map[string]any)
	inst := spot["instance"].(map[string]any)
	if fmt.Sprint(inst["public_ips"]) != "[203.0.113.10]" || inst["console_url"] != nil || inst["display_name"] != "Spot box" ||
		fmt.Sprint(inst["actions"]) != "[start snapshot_list sync_ip console]" || fmt.Sprint(inst["custom_actions"]) != "[renew probe]" {
		t.Fatalf("re-checked instance %v", inst)
	}
	if got := fmt.Sprint(spot["actions"]); got != "[map[action:start safety:confirm] map[action:snapshot_list safety:read] map[action:sync_ip safety:read] "+
		"map[action:console safety:read] map[action:custom confirm_text:Renew this server for another term? id:renew safety:confirm title:Renew] "+
		"map[action:custom id:probe safety:read title:Keep-alive check]]" {
		t.Fatalf("host actions %s", got)
	}
	if spot["machine_id"] != nil {
		t.Fatalf("an unbound host names a computer: %v", spot)
	}

	// Binding.
	bind := func(mid, instance string) (int, map[string]any) {
		return call(t, mux, "PUT", "/api/v1/machines/"+mid+"/cloud", master,
			fmt.Sprintf(`{"extension": "example-hosts", "provider": "main", "account": "acct", "instance": %q}`, instance))
	}
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac-run/cloud", master, ""); c != 200 || body["bound"] != false || len(body) != 1 {
		t.Fatalf("unbound %d %v", c, body)
	}
	if c, body := bind("mac-run", "i-run"); c != 200 || body["bound"] != true || body["host"].(map[string]any)["machine_id"] != "mac-run" ||
		body["binding"].(map[string]any)["public_ip"] != "198.51.100.7" || body["binding"].(map[string]any)["vendor"] != "examplehosts" {
		t.Fatalf("bind %d %v", c, body)
	}
	if c, body := bind("mac-spot", "i-spot"); c != 200 {
		t.Fatalf("bind spot %d %v", c, body)
	}
	if c, body := bind("mac-stuck", "i-stuck"); c != 200 {
		t.Fatalf("bind stuck %d %v", c, body)
	}
	if c, body := bind("mac-other", "i-run"); c != 409 || body["code"] != "already_bound" || body["machine_id"] != "mac-run" || body["error"] == "" {
		t.Fatalf("already bound %d %v", c, body)
	}
	for _, tc := range []struct {
		mid, instance string
		status        int
		code          string
	}{
		{"mac-other", "i-missing", 404, "not_found"},
		{"mac-other", "i-garbled", 502, "plugin_failed"},
		{"mac-other", "i-wrongid", 502, "plugin_failed"},
		{"nowhere", "i-run", 404, "not_found"},
	} {
		if c, body := bind(tc.mid, tc.instance); c != tc.status || body["code"] != tc.code || body["error"] == "" {
			t.Errorf("bind %s to %s: %d %v, want %d %s", tc.mid, tc.instance, c, body, tc.status, tc.code)
		}
	}
	for _, body := range []string{`{"extension": "example-hosts", "provider": "main", "account": "acct"}`,
		`{"extension": "example-hosts", "provider": "main", "account": "acct", "instance": "i-run", "extra": 1}`} {
		if c, got := call(t, mux, "PUT", "/api/v1/machines/mac-other/cloud", master, body); c != 400 || got["code"] != "bad_request" {
			t.Errorf("bind %s: %d %v", body, c, got)
		}
	}
	if c, body := call(t, mux, "PUT", "/api/v1/machines/mac-other/cloud", master, `{"extension": "example-hosts", "provider": "nobody", "account": "a", "instance": "b"}`); c != 404 || body["code"] != "not_found" {
		t.Errorf("an unknown provider: %d %v", c, body)
	}
	c, list := call(t, mux, "GET", "/api/v1/extensions/-/cloud/bindings", master, "")
	if bs, _ := list["bindings"].([]any); c != 200 || len(bs) != 3 {
		t.Fatalf("bindings %d %v", c, list)
	}
	if c, body := call(t, mux, "GET", "/api/v1/extensions/-/cloud/instances", master, ""); c != 200 || body["hosts"].([]any)[1].(map[string]any)["machine_id"] != "mac-run" {
		t.Fatalf("instances with bindings %d %v", c, body)
	}

	act := func(mid, body string) (int, map[string]any) {
		return call(t, mux, "POST", "/api/v1/machines/"+mid+"/cloud/actions", master, body)
	}
	// The gates: nothing reaches the program before they pass.
	n := cloudActionCalls(t, out)
	for _, tc := range []struct {
		mid, body    string
		status       int
		code, safety string
	}{
		{"mac-run", `{"action": "stop"}`, 400, "confirmation_required", "confirm"},
		{"mac-run", `{"action": "force_stop", "confirm": true}`, 400, "acknowledgement_required", "destructive"},
		{"mac-run", `{"action": "force_stop", "confirm": true, "acknowledge": "i-wrong"}`, 400, "acknowledgement_required", "destructive"},
		{"mac-spot", `{"action": "custom", "id": "renew"}`, 400, "confirmation_required", "confirm"},
		// Not offered now (a running instance offers no start): refused before the gate, so nobody confirms it.
		{"mac-run", `{"action": "start"}`, 422, "unsupported", ""},
		{"mac-run", `{"action": "snapshot_create"}`, 400, "confirmation_required", "confirm"},
		{"mac-run", `{"action": "start", "confirm": true}`, 422, "unsupported", ""},
		{"mac-spot", `{"action": "custom", "id": "undeclared", "confirm": true}`, 422, "unsupported", ""},
		{"mac-run", `{"action": "snapshot_list"}`, 400, "bad_request", ""},
		{"mac-run", `{"action": "teleport", "confirm": true}`, 400, "bad_request", ""},
		{"mac-run", `{"action": "stop", "confirm": true, "reconnect": true}`, 400, "bad_request", ""},
		{"mac-run", `{"action": "custom", "confirm": true}`, 400, "bad_request", ""},
		{"mac-other", `{"action": "stop", "confirm": true}`, 404, "not_bound", ""},
		{"mac-run", `{"action": "snapshot_create", "confirm": true, "name": "` + strings.Repeat("n", 81) + `"}`, 400, "bad_request", ""},
		{"mac-run", `{"action": "snapshot_create", "confirm": true, "name": "nightly\nrun"}`, 400, "bad_request", ""},
	} {
		c, body := act(tc.mid, tc.body)
		if c != tc.status || body["code"] != tc.code || body["error"] == "" || (tc.safety != "" && body["safety"] != tc.safety) {
			t.Errorf("%s %s: %d %v, want %d %s", tc.mid, tc.body, c, body, tc.status, tc.code)
		}
	}
	// An approval given for another binding than the computer has now acts on nothing.
	stale := wire.CloudBinding{Extension: "example-hosts", Provider: "main", Account: "acct", Instance: "i-spot"}
	if _, f := h.cloudAct(context.Background(), i18n.En, "test", h.machineByID("mac-run"),
		wire.CloudActionRequest{Action: wire.CloudActionKindStop, Confirm: true}, &stale); f == nil || f.code != codeCloudBadRequest {
		t.Errorf("an action approved for another binding: %+v", f)
	}
	if got := cloudActionCalls(t, out); got != n {
		t.Fatalf("the program heard %d actions before a gate passed", got-n)
	}

	// Actions that pass.
	if c, body := act("mac-run", `{"action": "stop", "confirm": true}`); c != 202 || body["status"] != "accepted" {
		t.Errorf("stop %d %v", c, body)
	}
	if got := cloudActionCalls(t, out); got != n+1 {
		t.Fatalf("the program heard %d actions for one that passed the gate", got-n)
	}
	if c, body := act("mac-run", `{"action": "force_stop", "confirm": true, "acknowledge": "i-run"}`); c != 200 || body["status"] != "done" {
		t.Errorf("force stop %d %v", c, body)
	}
	if c, body := act("mac-run", `{"action": "sync_ip"}`); c != 200 || body["host"] == nil {
		t.Errorf("sync ip %d %v", c, body)
	}
	if c, body := act("mac-run", `{"action": "console"}`); c != 200 || body["url"] != "https://console.example.com/signed?t=1" {
		t.Errorf("console %d %v", c, body)
	}
	if c, body := act("mac-spot", `{"action": "console"}`); c != 502 || body["code"] != "plugin_failed" {
		t.Errorf("a console link that is not https %d %v", c, body)
	}
	if c, body := act("mac-spot", `{"action": "custom", "id": "renew", "confirm": true}`); c != 200 || body["message"] != "Renewed until next year (renew)" {
		t.Errorf("custom renew %d %v", c, body)
	}
	if c, body := act("mac-spot", `{"action": "custom", "id": "probe"}`); c != 200 || body["status"] != "done" {
		t.Errorf("custom probe (read) %d %v", c, body)
	}
	// Read without an address (the probe made the vendor take it back), the binding keeps the last one it had.
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac-spot/cloud", master, ""); c != 200 ||
		body["binding"].(map[string]any)["public_ip"] != "203.0.113.10" || body["host"].(map[string]any)["instance"].(map[string]any)["public_ips"] != nil {
		t.Fatalf("the computer with no address now %d %v", c, body)
	}
	if c, body := act("mac-run", `{"action": "snapshot_create", "confirm": true, "name": "nightly"}`); c != 200 || body["snapshot"].(map[string]any)["id"] != "snap-1" {
		t.Errorf("snapshot %d %v", c, body)
	}
	// What a provider answers with a code.
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"capacity", 503, "capacity"},
		{"not_signed_in", 409, "not_signed_in"},
		{"invalid_state", 409, "invalid_state"},
		{"rate_limited", 429, "rate_limited"},
		{"refuse", 422, "refused"},
		{"explode", 502, "plugin_failed"},
	} {
		c, body := act("mac-run", fmt.Sprintf(`{"action": "snapshot_create", "confirm": true, "name": %q}`, tc.name))
		if c != tc.status || body["code"] != tc.code || body["error"] == "" || body["detail"] == "" || strings.ContainsAny(fmt.Sprint(body["detail"]), "\n") {
			t.Errorf("snapshot %s: %d %v, want %d %s", tc.name, c, body, tc.status, tc.code)
		}
	}

	// A client that went away after the gate passed neither cancels the write nor leaves it audited as failed.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	audited, heard := len(store.audits()), cloudActionCalls(t, out)
	if res, f := h.cloudAct(gone, i18n.En, "test", h.machineByID("mac-run"),
		wire.CloudActionRequest{Action: wire.CloudActionKindSnapshotCreate, Confirm: true, Name: "gone"}, nil); f != nil || res.Snapshot == nil {
		t.Fatalf("a write whose client went away: %+v %+v", res, f)
	}
	if got := store.audits(); cloudActionCalls(t, out) != heard+1 || len(got) != audited+1 || !strings.Contains(string(got[len(got)-1].detail), `"outcome":"done"`) {
		t.Fatalf("a write whose client went away: %d calls, audit %+v", cloudActionCalls(t, out)-heard, got[audited:])
	}

	// Snapshots.
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac-spot/cloud/snapshots", master, ""); c != 200 ||
		fmt.Sprint(body["snapshots"]) != "[map[id:snap-0 name:Before upgrade state:ready]]" {
		t.Errorf("snapshots %d %v", c, body)
	}
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac-run/cloud/snapshots", master, ""); c != 422 || body["code"] != "unsupported" {
		t.Errorf("snapshots of an instance that has none %d %v", c, body)
	}

	// Start and reconnect: the instance comes back at another address and the computer reconnects.
	c, started := act("mac-spot", `{"action": "start", "confirm": true, "reconnect": true}`)
	if c != 202 || started["recovery"].(map[string]any)["phase"] != "starting" {
		t.Fatalf("start and reconnect %d %v", c, started)
	}
	var view map[string]any
	waitLong(t, "the recovery ending", func() bool {
		_, view = call(t, mux, "GET", "/api/v1/machines/mac-spot/cloud", master, "")
		rec, _ := view["recovery"].(map[string]any)
		return rec != nil && (rec["phase"] == "done" || rec["phase"] == "failed")
	})
	if rec := view["recovery"].(map[string]any); rec["phase"] != "done" || rec["ip_changed"] != true || !slices.Contains(devs.reconnected(), "mac-spot") {
		t.Fatalf("recovery %v (reconnects %v)", rec, devs.reconnected())
	}
	if view["binding"].(map[string]any)["public_ip"] != "203.0.113.20" || view["host"].(map[string]any)["instance"].(map[string]any)["state"] != "running" {
		t.Fatalf("the computer after the start %v", view)
	}
	// One that never reconnects fails, saying why.
	if c, body := act("mac-stuck", `{"action": "start", "confirm": true, "reconnect": true}`); c != 202 {
		t.Fatalf("start stuck %d %v", c, body)
	}
	waitLong(t, "the stuck recovery ending", func() bool {
		_, view = call(t, mux, "GET", "/api/v1/machines/mac-stuck/cloud", master, "")
		rec, _ := view["recovery"].(map[string]any)
		return rec != nil && (rec["phase"] == "done" || rec["phase"] == "failed")
	})
	if rec := view["recovery"].(map[string]any); rec["phase"] != "failed" || rec["error"] == "" || rec["ip_changed"] == true {
		t.Fatalf("stuck recovery %v", rec)
	}

	// The audit: binds and every write, never an address or the provider's words.
	c, audit := call(t, mux, "GET", "/api/v1/machines/mac-run/cloud/audit", master, "")
	rows, _ := audit["audit"].([]any)
	if c != 200 || len(rows) == 0 {
		t.Fatalf("audit %d %v", c, audit)
	}
	events := map[string]int{}
	for _, r := range rows {
		row := r.(map[string]any)
		events[fmt.Sprint(row["event"], "/", row["action"], "/", row["outcome"])]++
		if row["machine_id"] != "mac-run" || row["extension"] != "example-hosts" || row["actor"] == "" {
			t.Errorf("audit row %v", row)
		}
	}
	for _, want := range []string{"cloud_bind/<nil>/done", "cloud_action/stop/accepted", "cloud_action/force_stop/done",
		"cloud_action/snapshot_create/done", "cloud_action/snapshot_create/refused", "cloud_action/snapshot_create/failed"} {
		if events[want] == 0 {
			t.Errorf("no audit row %s in %v", want, events)
		}
	}
	for k := range events {
		if strings.Contains(k, "sync_ip") || strings.Contains(k, "console") {
			t.Errorf("a read was audited: %s", k)
		}
	}
	for _, row := range store.audits() {
		if s := string(row.detail); strings.Contains(s, "203.0.113") || strings.Contains(s, "198.51.100") || strings.Contains(s, "vendor said") ||
			strings.Contains(s, "acct") {
			t.Errorf("audit detail says too much: %s", s)
		}
	}
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac-run/cloud/audit?limit=x", master, ""); c != 400 || body["code"] != "bad_request" {
		t.Errorf("bad limit %d %v", c, body)
	}

	// A recovery whose binding goes stops: it reconnects nothing more.
	cloudReconnectWait = 5 * time.Second
	asked := len(devs.reconnected())
	// Started by a client that went away at once: the recovery begins all the same.
	if res, f := h.cloudAct(gone, i18n.En, "test", h.machineByID("mac-stuck"),
		wire.CloudActionRequest{Action: wire.CloudActionKindStart, Confirm: true, Reconnect: true}, nil); f != nil || res.Recovery == nil {
		t.Fatalf("start stuck again by a client that went away: %+v %+v", res, f)
	}
	waitLong(t, "the second recovery reconnecting", func() bool { return len(devs.reconnected()) > asked })

	// Unbinding, audited.
	if c, body := call(t, mux, "DELETE", "/api/v1/machines/mac-stuck/cloud", master, ""); c != 200 || body["bound"] != false {
		t.Fatalf("unbind %d %v", c, body)
	}
	// Its goroutine ends well before the reconnect wait (5 s, a Reconnect every 50 ms) would end it: had the unbind not
	// stopped it, it would have asked again many times by then.
	asked = len(devs.reconnected())
	waitLong(t, "the forgotten recovery's goroutine ending", func() bool { return cloudFollowing() == 0 })
	if got := len(devs.reconnected()); got > asked+1 || h.cloudRecoveryOf("mac-stuck") != nil {
		t.Fatalf("a forgotten recovery reconnected %d more times (%v)", got-asked, h.cloudRecoveryOf("mac-stuck"))
	}
	_, audit = call(t, mux, "GET", "/api/v1/machines/mac-stuck/cloud/audit?limit=1", master, "")
	if rows, _ := audit["audit"].([]any); len(rows) != 1 || rows[0].(map[string]any)["event"] != "cloud_unbind" {
		t.Fatalf("unbind audit %v", audit)
	}
	// A computer the devices no longer list is dropped from the bindings.
	devs.remove("mac-spot")
	if _, list := call(t, mux, "GET", "/api/v1/extensions/-/cloud/bindings", master, ""); len(list["bindings"].([]any)) != 1 {
		t.Fatalf("bindings after a computer went: %v", list)
	}

	// Only a device with full credentials.
	for _, p := range []principal{{ID: "watch", Role: roleAnswer}, {Role: roleFull, Extension: "example-hosts"}} {
		for _, r := range []*http.Request{httptest.NewRequest("GET", "/api/v1/extensions/-/cloud", nil),
			httptest.NewRequest("GET", "/api/v1/extensions/-/cloud/bindings", nil),
			httptest.NewRequest("GET", "/api/v1/machines/mac-run/cloud", nil),
			httptest.NewRequest("POST", "/api/v1/machines/mac-run/cloud/actions", strings.NewReader(`{"action": "stop", "confirm": true}`))} {
			rec := httptest.NewRecorder()
			h.apiMux.ServeHTTP(rec, r.WithContext(withPrincipal(r.Context(), p)))
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"read_only"`) {
				t.Errorf("%+v %s: %d %s", p, r.URL, rec.Code, rec.Body)
			}
		}
	}

	// A provider switched off: the binding stands, the computer's page says why, actions are refused.
	if c, body := call(t, mux, "PUT", "/api/v1/extensions/example-hosts/-/enabled", master, `{"enabled": false}`); c != 200 {
		t.Fatalf("disable %d %v", c, body)
	}
	c, view = call(t, mux, "GET", "/api/v1/machines/mac-run/cloud", master, "")
	if pr, _ := view["problem"].(map[string]any); c != 200 || view["bound"] != true || pr == nil || pr["code"] != "provider_off" || pr["error"] == "" || view["host"] != nil {
		t.Fatalf("provider off %d %v", c, view)
	}
	if c, body := act("mac-run", `{"action": "stop", "confirm": true}`); c != 409 || body["code"] != "provider_off" {
		t.Errorf("an action with the provider off %d %v", c, body)
	}
	if _, list := call(t, mux, "GET", "/api/v1/extensions/-/cloud/bindings", master, ""); len(list["bindings"].([]any)) != 1 {
		t.Fatalf("bindings with the provider off: %v", list)
	}
	if _, body := call(t, mux, "GET", "/api/v1/extensions/-/cloud", master, ""); len(body["providers"].([]any)) != 0 {
		t.Fatalf("providers when off: %v", body)
	}
}

// Without a database the bindings are empty and cannot be written.
func TestCloudNoDatabase(t *testing.T) {
	h := newTestManager(t)
	h.opts.Devices = &cloudDevices{online: map[string]bool{"mac": true}}
	mux := h.routes()
	master := h.cfg.Token
	if c, body := call(t, mux, "GET", "/api/v1/extensions/-/cloud/bindings", master, ""); c != 200 || fmt.Sprint(body["bindings"]) != "[]" {
		t.Fatalf("bindings %d %v", c, body)
	}
	if c, body := call(t, mux, "GET", "/api/v1/extensions/-/cloud", master, ""); c != 200 || fmt.Sprint(body["providers"]) != "[]" {
		t.Fatalf("providers %d %v", c, body)
	}
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac/cloud", master, ""); c != 200 || body["bound"] != false {
		t.Fatalf("machine %d %v", c, body)
	}
	if c, body := call(t, mux, "PUT", "/api/v1/machines/mac/cloud", master, `{"extension": "x", "provider": "y", "account": "a", "instance": "b"}`); c != 503 || body["code"] != "no_database" {
		t.Fatalf("bind %d %v", c, body)
	}
	if c, body := call(t, mux, "DELETE", "/api/v1/machines/mac/cloud", master, ""); c != 503 || body["code"] != "no_database" {
		t.Fatalf("unbind %d %v", c, body)
	}
	if c, body := call(t, mux, "GET", "/api/v1/machines/mac/cloud/audit", master, ""); c != 200 || fmt.Sprint(body["audit"]) != "[]" {
		t.Fatalf("audit %d %v", c, body)
	}
}

// A binding made before its extension was renamed is still its instance's: the instance lists its computer and no
// second computer can be bound to it.
func TestCloudBindingRenamedExtension(t *testing.T) {
	h := newTestManager(t)
	h.extensions = newExtensions(h.cfg.dir, nil, h.logf)
	manifest := json.RawMessage(`{"manifest_version": 1, "id": "new-hosts", "name": "Example Hosts", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": "bin/hosts"}, "activation_events": ["on_startup"],
		"contributes": {"cloud_hosts": [{"id": "main", "name": "Example Hosts", "vendor": "examplehosts"}]}}`)
	store := newExtensionStore()
	store.rows["new-hosts"] = extension.Record{ID: "new-hosts", StorageID: "old-hosts", PreviousID: "old-hosts", Aliases: []string{"old-hosts"},
		Source: extension.SourceInstalled, Version: "1.0.0", Manifest: manifest, Enabled: true, InstalledAt: time.Now().UTC()}
	reg := extension.NewRegistry(h.extensions.root, store, nil, 0)
	if err := reg.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.extensions.reg = reg
	if got := reg.CanonicalID("old-hosts"); got != "new-hosts" {
		t.Fatalf("the old id names %q", got)
	}
	list := []wire.CloudBinding{{MachineID: "mac-a", Extension: "old-hosts", Provider: "main", Account: "acct", Instance: "i-1"}}
	p := cloudProvider{extension: "new-hosts", contribution: "main"}
	if b := h.cloudBindingFor(list, p, "acct", "i-1"); b == nil || b.MachineID != "mac-a" {
		t.Fatalf("the renamed extension's binding: %+v", b)
	}
	if b := h.cloudBindingFor(list, p, "acct", "i-2"); b != nil {
		t.Fatalf("another instance's binding: %+v", b)
	}
	other := cloudProvider{extension: "other-hosts", contribution: "main"}
	if b := h.cloudBindingFor(list, other, "acct", "i-1"); b != nil {
		t.Fatalf("another extension's binding: %+v", b)
	}
	// The registry's provider, found by the old id too; nothing for another contribution or extension.
	if got, ok := h.cloudProviderOf("old-hosts", "main"); !ok || got.extension != "new-hosts" || got.name != "Example Hosts" ||
		got.vendor != "examplehosts" {
		t.Fatalf("the provider by its old id: %+v %v", got, ok)
	}
	if _, ok := h.cloudProviderOf("new-hosts", "nope"); ok {
		t.Fatal("no such contribution")
	}
	if _, ok := h.cloudProviderOf("other-hosts", "main"); ok {
		t.Fatal("no such extension")
	}
}

// A refused answer is logged by its definition and path, never by the value the validator found there.
func TestCloudSchemaRuleSaysNoValue(t *testing.T) {
	raw := []byte(`{"account": "acct", "id": "i-1", "state": "rebooting 203.0.113.7", "secret 198.51.100.1": 1}`)
	err := wire.ValidateJSON("CloudInstance", raw)
	if err == nil {
		t.Fatal("the answer passed")
	}
	rule := cloudSchemaRule("CloudInstance", err)
	if !strings.HasPrefix(rule, "does not match CloudInstance") || strings.Contains(rule, "203.0.113") || strings.Contains(rule, "198.51.100") ||
		strings.Contains(rule, "rebooting") || strings.Contains(rule, "secret") {
		t.Fatalf("rule %q (from %v)", rule, err)
	}
	if rule := cloudSchemaRule("CloudInstance", errors.New("203.0.113.7")); rule != "does not match CloudInstance" {
		t.Fatalf("another error's rule %q", rule)
	}
}
