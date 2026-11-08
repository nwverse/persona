package proto

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestPreCutoverSessionSourceContract(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"session_id":"s","started_by_jarvis":true}`, true},
		{`{"session_id":"s","started_by_persona":true}`, true},
		{`{"session_id":"s","started_by_jarvis":true,"started_by_persona":false}`, false},
		{`{"session_id":"s","started_by_jarvis":false,"started_by_persona":true}`, true},
	} {
		var s Session
		if err := json.Unmarshal([]byte(tc.raw), &s); err != nil || s.StartedByPersona != tc.want || s.SessionID != "s" {
			t.Fatal(tc.raw, s, err)
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var old struct {
			ID      string `json:"session_id"`
			Started bool   `json:"started_by_jarvis"`
		}
		var current struct {
			Started bool `json:"started_by_persona"`
		}
		if json.Unmarshal(raw, &old) != nil || json.Unmarshal(raw, &current) != nil || old.Started != tc.want || current.Started != tc.want || old.ID != "s" {
			t.Fatal(string(raw))
		}
	}
}

// A session is sent as the bytes the struct-copying MarshalJSON gave before MarshalJSONTo: through MarshalJSON (a
// value in an interface), MarshalJSONTo (a pointer, a slice element) and an encoder that leaves HTML alone.
func TestSessionWireBytesUnchanged(t *testing.T) {
	at := time.Unix(1700000000, 5)
	s := Session{SessionID: "s1", Title: "<b>&</b> \u2028", LastText: "\xff", StartedByPersona: true, UpdatedAt: at,
		ActivityAt: &at, Queued: []string{}, Efforts: []string{"<hi>"}, Subagents: []Subagent{{}}}
	before, err := json.Marshal(struct {
		sessionJSON
		LegacyStarted bool `json:"started_by_jarvis"`
	}{sessionJSON(s), s.StartedByPersona})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(s)
	pointer, _ := json.Marshal(&s)
	var noHTML bytes.Buffer
	enc := json.NewEncoder(&noHTML)
	enc.SetEscapeHTML(false)
	_ = enc.Encode([]Session{s})
	for name, c := range map[string][2]string{"value": {string(value), string(before)}, "pointer": {string(pointer), string(before)},
		"no-HTML encoder": {noHTML.String(), "[" + string(before) + "]\n"}} {
		if c[0] != c[1] {
			t.Errorf("%s:\n got %s\nwant %s", name, c[0], c[1])
		}
	}
}
func TestSourceAdapterPreservesPartialAndNullDecode(t *testing.T) {
	s := Session{SessionID: "s", Title: "keep", StartedByPersona: true}
	if err := json.Unmarshal([]byte(`{"status":"idle"}`), &s); err != nil || s.Title != "keep" || !s.StartedByPersona {
		t.Fatal(s, err)
	}
	if err := json.Unmarshal([]byte(`null`), &s); err != nil || s.SessionID != "s" {
		t.Fatal(s, err)
	}
	if err := json.Unmarshal([]byte(`{"started_by_persona":null,"started_by_jarvis":false}`), &s); err != nil || !s.StartedByPersona {
		t.Fatal("present canonical null fell through", s, err)
	}
	if err := json.Unmarshal([]byte(`{"started_by_jarvis":"yes"}`), &s); err == nil {
		t.Fatal("wrong known field type accepted")
	}
}
func TestLauncherSourceAdapterIsTyped(t *testing.T) {
	raw, err := json.Marshal(LauncherEnvelope(Launcher{Name: "custom", Source: "persona"}))
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		Source string `json:"source"`
	}
	json.Unmarshal(raw, &baseline)
	if baseline.Source != "jarvis" {
		t.Fatal(string(raw))
	}
	var current LauncherEnvelope
	if err := json.Unmarshal(raw, &current); err != nil || current.Source != "persona" {
		t.Fatal(current, err)
	}
}
func TestLauncherEmbeddingRetainsSiblingFields(t *testing.T) {
	type configured struct {
		Launcher
		AgentType string `json:"agent_type"`
		Health    string `json:"health"`
	}
	original := configured{Launcher: Launcher{Name: "fixture", Source: "persona"}, AgentType: "codex", Health: "blocked"}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded configured
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Fatalf("embedding lost sibling fields: %s", raw)
	}
}
