package proto

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
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

// decodeTwice is how a session was decoded before UnmarshalJSONFrom: its bytes into a copy of it, then the started
// key used decoded again.
func decodeTwice(data []byte, s *Session) error {
	v := struct {
		sessionJSON
		LegacyStarted  json.RawMessage `json:"started_by_jarvis"`
		CurrentStarted json.RawMessage `json:"started_by_persona"`
	}{sessionJSON: sessionJSON(*s)}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	raw := v.LegacyStarted
	if len(v.CurrentStarted) > 0 {
		raw = v.CurrentStarted
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v.sessionJSON.StartedByPersona); err != nil {
			return err
		}
	}
	*s = Session(v.sessionJSON)
	return nil
}

// A session decodes in one pass to what the two-pass decoding gave, into a fresh or a filled-in session; one that
// fails is left as it was.
func TestSessionDecodeMatchesTwoPass(t *testing.T) {
	at := time.Unix(1700000000, 5).UTC()
	full, _ := json.Marshal(Session{SessionID: "s1", Title: "<b>&</b>", StartedByPersona: true, UpdatedAt: at, ActivityAt: &at,
		Efforts: []string{"low"}, Subagents: []Subagent{{}}, Capabilities: &SessionCapabilities{}})
	filled := Session{SessionID: "old", Title: "keep", StartedByPersona: true, Efforts: []string{"high", "max"}}
	for _, raw := range []string{
		string(full), `null`, `{}`, `{"status":"idle"}`, `{"started_by_jarvis":true}`, `{"started_by_jarvis":false}`,
		`{"started_by_persona":false,"started_by_jarvis":true}`, `{"started_by_persona":null,"started_by_jarvis":false}`,
		`{"started_by_persona":true,"started_by_persona":null}`, `{"Session_ID":"x","STARTED_BY_PERSONA":true}`,
		`{"started_by_jarvis":"yes","started_by_persona":true}`, `{"started_by_jarvis":"yes"}`, `{"started_by_persona":[1]}`,
		`{"efforts":["xhigh"],"unknown":{"a":[1,{}]},"title":"\u00e9\ud83d\ude00"}`, "{\"title\":\"\xff\"}", `{"title":"\q"}`,
		`{"session_id":"new","status":5,"title":"t"}`, `{"session_id":"new","started_by_persona":"no"}`,
	} {
		for _, start := range []Session{{}, filled} {
			want, got := start, start
			want.Efforts, got.Efforts = slices.Clone(start.Efforts), slices.Clone(start.Efforts)
			wantErr, gotErr := decodeTwice([]byte(raw), &want), json.Unmarshal([]byte(raw), &got)
			if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(want)
				t.Errorf("%s into %q:\n got %s, %v\nwant %s, %v", raw, start.SessionID, g, gotErr, w, wantErr)
			}
		}
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
