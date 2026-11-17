package devices

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
)

// Only the hub's human-input submission from a device marks a message as the owner's own; a caller's own owner_input
// never survives, and everything else in the params is kept as it was.
func TestOwnerInputParams(t *testing.T) {
	human := &devicewire.Submission{ID: "s1", Kind: "human_input", OriginalText: "merge it"}
	device := &devicewire.Caller{Kind: "device", CredentialID: "d1"}
	for _, c := range []struct {
		name   string
		call   devicewire.NodeCall
		params string
		want   bool
	}{
		{"the owner's app", devicewire.NodeCall{Caller: device, Submission: human}, `{"session_id":"x","text":"merge it"}`, true},
		{"the owner's app, a claim of its own", devicewire.NodeCall{Caller: device, Submission: human}, `{"text":"merge it","owner_input":false}`, true},
		{"the assistant", devicewire.NodeCall{Caller: &devicewire.Caller{Kind: "system"}}, `{"text":"merge it","owner_input":true}`, false},
		{"no caller", devicewire.NodeCall{}, `{"text":"merge it","owner_input":true}`, false},
		{"a device, no submission", devicewire.NodeCall{Caller: device}, `{"text":"merge it"}`, false},
		{"another kind of submission", devicewire.NodeCall{Caller: device, Submission: &devicewire.Submission{ID: "s2", Kind: "relay"}}, `{"text":"merge it"}`, false},
		{"a submission without an id", devicewire.NodeCall{Caller: device, Submission: &devicewire.Submission{Kind: "human_input"}}, `{"text":"merge it"}`, false},
	} {
		out, err := ownerInputParams(c.call, json.RawMessage(c.params))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var fields map[string]any
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := fields["owner_input"] == true; got != c.want {
			t.Errorf("%s: owner_input = %v in %s", c.name, fields["owner_input"], out)
		}
		if _, there := fields["owner_input"]; there && !c.want {
			t.Errorf("%s: a caller's owner_input survived: %s", c.name, out)
		}
		if fields["text"] != "merge it" {
			t.Errorf("%s: text changed: %s", c.name, out)
		}
	}
	if _, err := ownerInputParams(devicewire.NodeCall{}, json.RawMessage(`[1]`)); err == nil {
		t.Error("params that are not an object were passed on")
	}
}

// A caller cannot claim the owner mark in another letter case either: a node decodes owner_input into a struct field,
// whose match ignores case (#9607). Every variant is dropped, and the mark is written back only for the owner's own
// typed message, once, as owner_input.
func TestOwnerInputParamsDropsEveryCase(t *testing.T) {
	human := devicewire.NodeCall{Caller: &devicewire.Caller{Kind: "device"}, Submission: &devicewire.Submission{ID: "h1", Kind: "human_input"}}
	for _, key := range []string{"owner_input", "OWNER_INPUT", "Owner_Input", "owner_Input"} {
		for name, p := range map[string]devicewire.NodeCall{
			"no caller":                 {},
			"system":                    {Caller: &devicewire.Caller{Kind: "system"}},
			"device, no human input":    {Caller: &devicewire.Caller{Kind: "device"}},
			"the owner's typed message": human,
		} {
			raw, _ := json.Marshal(map[string]any{"session_id": "x", "text": "hi", key: true, "OWNER_input": true})
			out, err := ownerInputParams(p, raw)
			if err != nil {
				t.Fatal(err)
			}
			var asNode struct {
				OwnerInput bool `json:"owner_input"`
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(out, &asNode) != nil || json.Unmarshal(out, &fields) != nil {
				t.Fatalf("%s %s: %s", name, key, out)
			}
			marks := 0
			for k := range fields {
				if strings.EqualFold(k, "owner_input") {
					marks++
				}
			}
			want := name == "the owner's typed message"
			if asNode.OwnerInput != want || (want && (marks != 1 || string(fields["owner_input"]) != "true")) || (!want && marks != 0) {
				t.Errorf("%s with %s: %s", name, key, out)
			}
		}
	}
}
