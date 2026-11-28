package extension

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// cloudManifest is the smallest manifest with these cloud_hosts items, for the hub's Parse and the SDK's schema alike.
func cloudManifest(t *testing.T, items ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"manifest_version": 1, "id": "example-hosts", "name": "Example Hosts", "version": "1.0.0",
		"engines": map[string]any{"extension_protocol": 1}, "runtime": map[string]any{"command": "bin/example-hosts"},
		"contributes": map[string]any{"cloud_hosts": items}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCloudHostsPoint(t *testing.T) {
	p := Points["cloud_hosts"]
	if p.Name != "cloud_hosts" || p.Namespace != "cloud" || p.Version != 1 || !p.NeedsRPC || p.NeedsHTTP {
		t.Fatalf("point %+v", p)
	}
	m, err := Parse(cloudManifest(t,
		map[string]any{"id": "main", "name": "Example Hosts", "vendor": "examplehosts", "custom_actions": []any{
			map[string]any{"id": "renew", "title": "Renew", "safety": "confirm", "confirm": "Renew this server?"},
			map[string]any{"id": "probe", "title": "Keep-alive check", "safety": "read"},
		}},
		map[string]any{"id": "spare", "name": "Spare", "vendor": "examplehosts"}))
	if err != nil {
		t.Fatal(err)
	}
	got := m.CloudHosts()
	if len(got) != 2 || got[0].ID != "main" || got[0].Vendor != "examplehosts" || len(got[0].CustomActions) != 2 || got[1].CustomActions != nil {
		t.Fatalf("%+v", got)
	}
	if items := m.Items("cloud_hosts"); !slices.Equal(items, []string{"main", "spare"}) {
		t.Fatal(items)
	}
	lines := Explain(m)
	if !slices.ContainsFunc(lines, func(l PermissionLine) bool { return l.Key == "cloud_hosts" && l.Title != "" && l.Detail != "" }) {
		t.Errorf("the install page does not say it: %+v", lines)
	}
	if (&Manifest{}).CloudHosts() != nil {
		t.Error("no contribution")
	}
}

// What an item may be. Where the schema cannot say it (an id twice), only the hub refuses.
func TestCloudHostsRefusals(t *testing.T) {
	item := func(change func(it map[string]any)) []map[string]any {
		it := map[string]any{"id": "main", "name": "Example Hosts", "vendor": "examplehosts"}
		change(it)
		return []map[string]any{it}
	}
	action := func(id, safety, confirm string) map[string]any {
		a := map[string]any{"id": id, "title": "Renew", "safety": safety}
		if confirm != "" {
			a["confirm"] = confirm
		}
		return a
	}
	actions := func(a ...map[string]any) func(it map[string]any) {
		return func(it map[string]any) { it["custom_actions"] = append([]map[string]any{}, a...) }
	}
	eight := make([]map[string]any, 9)
	for i := range eight {
		eight[i] = action("a"+string(rune('0'+i)), "read", "")
	}
	for _, c := range []struct {
		name       string
		items      []map[string]any
		ok         bool
		schemaToo  bool // the schema says the same
		schemaPass bool // when not schemaToo: the schema lets it through
	}{
		{"plain", item(func(map[string]any) {}), true, true, true},
		{"every safety", item(actions(action("renew", "confirm", "Renew?"), action("wipe", "destructive", "Wipe it?"), action("probe", "read", ""))), true, true, true},
		{"a read action with a sentence", item(actions(action("probe", "read", "Check it?"))), true, true, true},
		{"eight custom actions", item(actions(eight[:8]...)), true, true, true},
		{"a name of 40 characters", item(func(it map[string]any) { it["name"] = strings.Repeat("代", 40) }), true, true, true},
		{"a confirm of 200 characters", item(actions(action("renew", "confirm", strings.Repeat("a", 200)))), true, true, true},
		{"no name", item(func(it map[string]any) { delete(it, "name") }), false, true, false},
		{"a blank name", item(func(it map[string]any) { it["name"] = " " }), false, true, false},
		{"a name of 41 characters", item(func(it map[string]any) { it["name"] = strings.Repeat("a", 41) }), false, true, false},
		{"a control character", item(func(it map[string]any) { it["name"] = "a\nb" }), false, true, false},
		{"a bad id", item(func(it map[string]any) { it["id"] = "Main!" }), false, true, false},
		{"no vendor", item(func(it map[string]any) { delete(it, "vendor") }), false, true, false},
		{"a bad vendor", item(func(it map[string]any) { it["vendor"] = "Example Hosts" }), false, true, false},
		{"no custom actions named", item(actions()), false, true, false},
		{"nine custom actions", item(actions(eight...)), false, true, false},
		{"a bad custom id", item(actions(action("Renew", "confirm", "Renew?"))), false, true, false},
		{"a bad safety", item(actions(action("renew", "careful", "Renew?"))), false, true, false},
		{"no safety", item(actions(map[string]any{"id": "renew", "title": "Renew"})), false, true, false},
		{"no title", item(actions(map[string]any{"id": "renew", "safety": "read"})), false, true, false},
		{"a title of 41 characters", item(actions(map[string]any{"id": "renew", "title": strings.Repeat("a", 41), "safety": "read"})), false, true, false},
		{"a confirm of 201 characters", item(actions(action("renew", "confirm", strings.Repeat("a", 201)))), false, true, false},
		{"an unknown field in an action", item(actions(map[string]any{"id": "renew", "title": "Renew", "safety": "read", "cost": 1})), false, true, false},
		{"an unknown field", item(func(it map[string]any) { it["region"] = "x" }), false, true, false},
		{"no items", nil, false, true, false},
		{"a custom id twice", item(actions(action("renew", "read", ""), action("renew", "read", ""))), false, false, true},
		{"a confirm action without its sentence", item(actions(action("renew", "confirm", ""))), false, true, false},
		{"a destructive action without its sentence", item(actions(action("wipe", "destructive", ""))), false, true, false},
		{"a blank sentence", item(actions(action("renew", "confirm", "  "))), false, true, false},
		{"a sentence with a control character", item(actions(action("renew", "confirm", "Renew\nit?"))), false, true, false},
	} {
		raw := cloudManifest(t, c.items...)
		_, hubErr := Parse(raw)
		schemaErr := wire.ValidateJSON("Manifest", raw)
		if (hubErr == nil) != c.ok {
			t.Errorf("%s: hub: want valid=%v, got %v", c.name, c.ok, hubErr)
		}
		wantSchema := c.ok
		if !c.schemaToo {
			wantSchema = c.schemaPass
		}
		if (schemaErr == nil) != wantSchema {
			t.Errorf("%s: schema: want valid=%v, got %v", c.name, wantSchema, schemaErr)
		}
	}
	if _, err := Parse(with(t, func(m map[string]any) {
		delete(m, "runtime")
		delete(m, "activation_events")
		m["contributes"] = map[string]any{"cloud_hosts": []any{map[string]any{"id": "main", "name": "Example Hosts", "vendor": "examplehosts"}}}
	})); err == nil || !strings.Contains(err.Error(), "needs a runtime that speaks the protocol") {
		t.Errorf("a provider without a program: %v", err)
	}
}

// The hub's safety table is the doc's "Actions and safety levels", word for word; custom and unknown kinds have none.
func TestCloudActionSafetyTable(t *testing.T) {
	want := map[wire.CloudActionKind]wire.CloudSafety{
		"start": "confirm", "stop": "confirm", "reboot": "confirm", "snapshot_create": "confirm",
		"hard_reset": "destructive", "force_stop": "destructive",
		"sync_ip": "read", "snapshot_list": "read", "console": "read",
	}
	for action, safety := range want {
		if got, ok := CloudActionSafety(action); !ok || got != safety {
			t.Errorf("%s: %q %v, want %q", action, got, ok, safety)
		}
	}
	for _, action := range []wire.CloudActionKind{"custom", "renew", "", "START"} {
		if got, ok := CloudActionSafety(action); ok {
			t.Errorf("%q has a safety level of the hub's: %q", action, got)
		}
	}
	if !slices.Equal(CloudStandardActions, []wire.CloudActionKind{"start", "stop", "reboot", "hard_reset", "force_stop", "sync_ip", "snapshot_list", "snapshot_create", "console"}) {
		t.Errorf("standard actions %v", CloudStandardActions)
	}
	for _, k := range CloudStandardActions {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: a standard action without a safety level", k)
		}
	}
	if len(CloudStandardActions) != len(want) || len(wire.CloudActionKinds) != len(want)+1 {
		t.Errorf("the schema's kinds %v and the table drifted", wire.CloudActionKinds)
	}
}

// The registry lists every installed provider, on or off, and follows the switches.
func TestEachCloudHost(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newTestRegistry(t)
	pkg := t.TempDir()
	writePackage(t, pkg, func(m map[string]any) {
		m["contributes"] = map[string]any{"cloud_hosts": []any{
			map[string]any{"id": "main", "name": "Example Hosts", "vendor": "examplehosts", "custom_actions": []any{
				map[string]any{"id": "renew", "title": "Renew", "safety": "confirm", "confirm": "Renew this server?"}}},
			map[string]any{"id": "spare", "name": "Spare", "vendor": "examplepanel"},
		}}
	})
	pv, err := r.Preview(ctx, "dir:"+pkg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(pv.Permissions, func(l PermissionLine) bool { return l.Key == "cloud_hosts" }) {
		t.Errorf("the preview does not say it: %+v", pv.Permissions)
	}
	if _, err := r.Install(ctx, pv.ID, pv.Digest, "cli"); err != nil {
		t.Fatal(err)
	}
	id := pv.Manifest.ID
	if !r.Get(id).Record.Enabled {
		if err := r.SetEnabled(ctx, id, true, "cli"); err != nil {
			t.Fatal(err)
		}
	}
	type entry struct {
		extension string
		it        CloudHostItem
		on        bool
	}
	all := func() []entry {
		var out []entry
		r.EachCloudHost(func(extension string, it CloudHostItem, on bool) { out = append(out, entry{extension, it, on}) })
		return out
	}
	got := all()
	if len(got) != 2 || got[0].it.ID != "main" || got[0].extension != id || got[0].it.Vendor != "examplehosts" ||
		len(got[0].it.CustomActions) != 1 || got[0].it.CustomActions[0].Safety != wire.CloudSafetyConfirm || !got[0].on ||
		got[1].it.ID != "spare" || got[1].it.Name != "Spare" || !got[1].on {
		t.Fatalf("%+v", got)
	}
	if err := r.SetContribution(ctx, id, "cloud_hosts", "spare", false, "device:x"); err != nil {
		t.Fatal(err)
	}
	if got := all(); len(got) != 2 || !got[0].on || got[1].on {
		t.Fatalf("spare switched off: %+v", got)
	}
	if err := r.SetEnabled(ctx, id, false, "cli"); err != nil {
		t.Fatal(err)
	}
	for _, e := range all() {
		if e.on {
			t.Fatalf("the extension is off: %+v", e)
		}
	}
	if len(all()) != 2 {
		t.Fatal("an extension switched off still lists its providers")
	}
}
