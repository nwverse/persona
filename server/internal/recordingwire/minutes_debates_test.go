package recordingwire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDebateStatusValid(t *testing.T) {
	if !DebateOpen.Valid() || !DebateSettled.Valid() {
		t.Fatal("open and settled must be valid")
	}
	for _, s := range []DebateStatus{"", "unknown", "OPEN", "resolved"} {
		if s.Valid() {
			t.Errorf("%q should not be valid", s)
		}
	}
}

func TestMinutesDebatesMarshalAndLenientUnmarshal(t *testing.T) {
	// No debates: omitempty keeps "debates" out of minutes.json.
	empty := Minutes{Title: "Sync", Points: []MinutesPoint{{Text: "One", At: 1}}}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"debates"`) {
		t.Errorf("empty debates serialized: %s", raw)
	}

	// Decision index 0 serializes explicitly; nil decision is omitted.
	zero := 0
	with := Minutes{
		Title:     "Sync",
		Decisions: []MinutesItem{{Text: "Per item", At: 1504.5}},
		Debates: []MinutesDebate{
			{
				Question: "Per item or per recording?",
				Topic:    "Export",
				Status:   DebateSettled,
				At:       312.0,
				Positions: []MinutesDebatePosition{
					{Stance: "Per item", At: 312.0, Who: "Ada"},
					{Stance: "Per recording", At: 340.5},
				},
				Outcome: &MinutesDebateOutcome{Text: "Per item for this release", At: 1504.5, Decision: &zero},
			},
			{
				Question: "Weekly or daily?",
				Status:   DebateOpen,
				At:       400.0,
				Positions: []MinutesDebatePosition{
					{Stance: "Weekly", At: 400.0},
					{Stance: "Daily", At: 420.0},
				},
			},
		},
	}
	raw, err = json.Marshal(with)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"decision":0`) {
		t.Errorf("decision index 0 missing: %s", raw)
	}
	var decoded Minutes
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Debates) != 2 || decoded.Debates[0].Outcome == nil || decoded.Debates[0].Outcome.Decision == nil || *decoded.Debates[0].Outcome.Decision != 0 {
		t.Fatalf("round-trip failed: %+v", decoded.Debates)
	}
	if decoded.Debates[1].Outcome != nil || decoded.Debates[1].Status != DebateOpen {
		t.Fatalf("open debate mismatch: %+v", decoded.Debates[1])
	}

	// Malformed debates never fail decoding the rest of minutes.json.
	for _, bad := range []string{
		`{"title":"Sync","points":[{"text":"One","at":1}],"debates":"bad"}`,
		`{"title":"Sync","points":[{"text":"One","at":1}],"debates":{}}`,
		`{"title":"Sync","points":[{"text":"One","at":1}],"debates":null}`,
		`{"title":"Sync","points":[{"text":"One","at":1}],"debates":[]}`,
		`{"title":"Sync","points":[{"text":"One","at":1}],"debates":["x",null,{"question":"","positions":[{"stance":"a","at":1},{"stance":"b","at":2}]},{"question":"Only one","positions":[{"stance":"a","at":1}]}]}`,
	} {
		var m Minutes
		if err := json.Unmarshal([]byte(bad), &m); err != nil {
			t.Errorf("%s: unmarshal error %v", bad, err)
		}
		if m.Title != "Sync" || len(m.Points) != 1 || m.Debates != nil {
			t.Errorf("%s: got title=%q points=%d debates=%+v", bad, m.Title, len(m.Points), m.Debates)
		}
	}

	// Unknown status is preserved (not Valid), unknown keys ignored, out-of-bounds decision dropped.
	var extra Minutes
	payload := `{"title":"Sync","decisions":[{"text":"D0","at":10}],"debates":[
		{"question":"Q1","status":"escalated","at":1,"extra":true,"positions":[
			{"stance":"A","at":1,"extra":1},{"stance":"B","at":2}
		],"outcome":{"text":"Ignored","at":10,"decision":0}},
		{"question":"Q2","status":"settled","at":3,"positions":[
			{"stance":"C","at":3},{"stance":"D","at":4}
		],"outcome":{"text":"Picked","at":10,"decision":5,"extra":"x"}}
	]}`
	if err := json.Unmarshal([]byte(payload), &extra); err != nil {
		t.Fatal(err)
	}
	if len(extra.Debates) != 2 {
		t.Fatalf("got %d debates", len(extra.Debates))
	}
	if extra.Debates[0].Status != "escalated" || extra.Debates[0].Status.Valid() || extra.Debates[0].Outcome != nil {
		t.Errorf("unknown status debate: %+v", extra.Debates[0])
	}
	if extra.Debates[1].Outcome == nil || extra.Debates[1].Outcome.Decision != nil {
		t.Errorf("out-of-bounds decision should be nil: %+v", extra.Debates[1].Outcome)
	}
}
