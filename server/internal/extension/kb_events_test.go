package extension

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestKBEventTopicsAreDeclaredAndRequireConsent(t *testing.T) {
	old := with(t, func(m map[string]any) {
		m["activation_events"] = []any{"on_startup"}
		m["contributes"] = map[string]any{"events": []any{map[string]any{"id": "kb", "subscribe": []any{"agent.finished"}}}}
	})
	next := with(t, func(m map[string]any) {
		m["activation_events"] = []any{"on_startup"}
		m["contributes"] = map[string]any{"events": []any{map[string]any{"id": "kb", "subscribe": []any{"kb.changed", "kb.forgotten"}}}}
	})
	before, err := Parse(old)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Topics()) != 2 {
		t.Fatalf("topics: %v", after.Topics())
	}
	b, _ := json.Marshal(Added(before, after))
	if !strings.Contains(string(b), permissionText(i18n.Default, "event_kb_changed")) || !strings.Contains(string(b), permissionText(i18n.Default, "event_kb_forgotten")) {
		t.Fatalf("new subscriptions need consent: %s", b)
	}
}
