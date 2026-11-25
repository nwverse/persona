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

// Every view spec/fixtures/counts/view_samples.json expects (#15512) reads into the generated CountsView
// (counts_gen.go) and writes back as the same JSON: no field lost, renamed or added, a total of 0 still written and an
// absent count still absent. What Go writes also holds to the schema, as the manager's tests of the views it makes
// should check, and each count's rows add up: the keys it lists plus more are n, and totals repeat n.
func TestCountsViewSamplesRoundTrip(t *testing.T) {
	root := countsSchema(t)
	samples := countsViewSamples(t)
	if len(samples) == 0 {
		t.Fatal("no samples")
	}
	for name, raw := range samples {
		var v CountsView
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var want, got any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: written back differently\n want %s\n got  %s", name, raw, out)
		}
		if p := checkCounts(t, root, out); p != "" {
			t.Errorf("%s: %s", name, p)
		}
		if c := v.Counts.AgentsNeedsYou; c != nil && len(c.Keys)+c.More != c.N {
			t.Errorf("%s: %d keys and %d more for n %d", name, len(c.Keys), c.More, c.N)
		}
		if c, tot := v.Counts.AgentsNeedsYou, v.Totals; tot != nil && (c == nil) != (tot.AgentsNeedsYou == nil) ||
			c != nil && tot != nil && *tot.AgentsNeedsYou != c.N {
			t.Errorf("%s: totals do not repeat n", name)
		}
	}
}

// A view made without its epoch, or with a count below zero, does not hold to the schema: the check the manager's
// tests make on what it writes catches both.
func TestCountsViewSchemaCatchesBadViews(t *testing.T) {
	root := countsSchema(t)
	for _, c := range []struct {
		v    CountsView
		want string
	}{
		{CountsView{Epoch: "e1", Counts: CountsViewCounts{AgentsNeedsYou: &CountsViewCount{N: 0}}}, ""},
		{CountsView{}, "$.epoch: should be at least 1 characters"},
		{CountsView{Epoch: "e1", Counts: CountsViewCounts{AgentsNeedsYou: &CountsViewCount{N: -1}}},
			"$.counts.agents_needs_you.n: should be at least 0"},
	} {
		out, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if p := checkCounts(t, root, out); p != c.want {
			t.Errorf("%s: %q, want %q", out, p, c.want)
		}
	}
}

// checkCounts says what in raw does not hold to the schema's CountsView, "" when it all does.
func checkCounts(t *testing.T, root map[string]any, raw []byte) string {
	t.Helper()
	v, err := jsonsubset.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := root["$defs"].(map[string]any)["CountsView"].(map[string]any)
	if schema == nil {
		t.Fatal("no definition CountsView")
	}
	return jsonsubset.ValidateAt(root, schema, v, "$", 0)
}

// countsViewSamples are the expected views of spec/fixtures/counts/view_samples.json, by case name.
func countsViewSamples(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "counts", "view_samples.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name     string `json:"name"`
			Expected struct {
				View json.RawMessage `json:"view"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]json.RawMessage{}
	for _, c := range doc.Cases {
		if c.Expected.View == nil {
			t.Fatalf("%s: no expected.view", c.Name)
		}
		out[c.Name] = c.Expected.View
	}
	return out
}

// countsSchema is schema/wire/counts.schema.json, compiled for jsonsubset.
func countsSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "wire", "counts.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsonsubset.Compile(data)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
