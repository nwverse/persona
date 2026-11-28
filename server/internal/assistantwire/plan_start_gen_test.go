package assistantwire

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/jsonsubset"
)

// Every PlanStartView in spec/fixtures/plan-start reads into the generated Go type, writes back as the same JSON, and
// holds to schema/wire/plan-start.schema.json (#17853).
func TestPlanStartSamplesRoundTrip(t *testing.T) {
	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "wire", "plan-start.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsonsubset.Compile(schemaBytes)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "plan-start", "plan_start.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name     string `json:"name"`
			Expected struct {
				Start json.RawMessage `json:"start"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	seen := map[PlanStartReason]bool{}
	for _, c := range doc.Cases {
		var got PlanStartView
		d := json.NewDecoder(bytes.NewReader(c.Expected.Start))
		d.DisallowUnknownFields()
		if err := d.Decode(&got); err != nil {
			t.Fatalf("%s: decode: %v", c.Name, err)
		}
		if !got.Reason.Valid() {
			t.Errorf("%s: invalid reason %q", c.Name, got.Reason)
		}
		seen[got.Reason] = true
		out, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var wantVal, gotVal any
		if err := json.Unmarshal(c.Expected.Start, &wantVal); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &gotVal); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(wantVal, gotVal) {
			t.Errorf("%s: round-trip mismatch\n want %s\n got  %s", c.Name, c.Expected.Start, out)
		}
		if problem := checkTodoView(t, root, "PlanStartView", out); problem != "" {
			t.Errorf("%s: schema violation: %s", c.Name, problem)
		}
	}
	for _, reason := range PlanStartReasons {
		if !seen[reason] {
			t.Errorf("spec/fixtures/plan-start misses reason %q", reason)
		}
	}
}

func TestPlanStartUnknownReasonReportsDefectAndRoundTrips(t *testing.T) {
	var seen []string
	prev := PlanStartWireUnknownEnum
	PlanStartWireUnknownEnum = func(enum, raw string) { seen = append(seen, enum+":"+raw) }
	defer func() { PlanStartWireUnknownEnum = prev }()

	raw := `{"reason":"future_gate","remember":[0]}`
	var got PlanStartView
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Reason.Valid() || got.Reason != "future_gate" {
		t.Fatalf("reason = %q (valid=%v)", got.Reason, got.Reason.Valid())
	}
	if !reflect.DeepEqual(seen, []string{"PlanStartReason:future_gate"}) {
		t.Fatalf("defect hook saw %v", seen)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != raw {
		t.Fatalf("round-trip = %s, want %s", out, raw)
	}
}
