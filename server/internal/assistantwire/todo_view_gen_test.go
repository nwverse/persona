package assistantwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The views, counts and places the shared to-do cases expect (spec/fixtures/todo-view and todo-counts, #10226) read
// into the generated types (todo_view_gen.go) and write back as the same JSON: no field lost, renamed or added, no time
// written another way, and no required list or map written as null, which a nil slice or map would be.
func TestTodoViewSamplesRoundTrip(t *testing.T) {
	defs := todoViewSchemaDefs(t)
	samples := todoViewSamples(t)
	seen := map[string]bool{}
	for _, s := range samples {
		v := todoViewDefs[s.def]()
		d := json.NewDecoder(bytes.NewReader(s.raw))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			t.Fatalf("%s: %v", s.where, err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", s.where, err)
		}
		var want, got any
		if err := json.Unmarshal(s.raw, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: written back differently\n want %s\n got  %s", s.where, s.raw, out)
		}
		for _, at := range nullRequired(defs, defs[s.def], got, "$") {
			t.Errorf("%s: required %s is null", s.where, at)
		}
		seen[s.def] = true
	}
	for _, def := range []string{"TodoSummary", "TodoCounts", "TodoPlaces"} {
		if !seen[def] {
			t.Errorf("no %s sample", def)
		}
	}
}

// A zero TodoPlaces writes its places and sections as null, which the schema refuses: whoever makes one allocates them,
// and the check above would catch a sample that slipped.
func TestTodoViewNilListsAreCaught(t *testing.T) {
	out, err := json.Marshal(TodoPlaces{})
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	defs := todoViewSchemaDefs(t)
	at := nullRequired(defs, defs["TodoPlaces"], got, "$")
	slices.Sort(at)
	if want := []string{"$.counts.sections", "$.places"}; !slices.Equal(at, want) {
		t.Fatalf("null required fields of %s: %v, want %v", out, at, want)
	}
}

type todoViewSample struct {
	def, where string
	raw        json.RawMessage
}

// todoViewSamples are the expected values of every todo-view and todo-counts case, with the definition each holds to.
func todoViewSamples(t *testing.T) []todoViewSample {
	t.Helper()
	var out []todoViewSample
	for _, domain := range []string{"todo-view", "todo-counts"} {
		files, err := filepath.Glob(filepath.Join("..", "..", "..", "spec", "fixtures", domain, "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("spec/fixtures/%s: %v", domain, err)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Cases []struct {
					Name     string
					Expected struct {
						View   json.RawMessage `json:"view"`
						Counts json.RawMessage `json:"counts"`
						Places json.RawMessage `json:"places"`
					}
				}
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			for _, c := range doc.Cases {
				where := fmt.Sprintf("%s/%s %s", domain, filepath.Base(f), c.Name)
				for def, raw := range map[string]json.RawMessage{"TodoSummary": c.Expected.View,
					"TodoCounts": c.Expected.Counts, "TodoPlaces": c.Expected.Places} {
					if raw != nil {
						out = append(out, todoViewSample{def: def, where: where + " " + def, raw: raw})
					}
				}
			}
		}
	}
	return out
}

// todoViewSchemaDefs are schema/wire/todo-view.schema.json's definitions.
func todoViewSchemaDefs(t *testing.T) map[string]map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "wire", "todo-view.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Defs
}

// nullRequired lists where value, read by schema, holds a required array or object as null or not at all.
func nullRequired(defs map[string]map[string]any, schema map[string]any, value any, at string) []string {
	resolve := func(s map[string]any) map[string]any {
		for {
			ref, ok := s["$ref"].(string)
			if !ok {
				return s
			}
			s = defs[strings.TrimPrefix(ref, "#/$defs/")]
		}
	}
	schema = resolve(schema)
	var out []string
	switch v := value.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, r := range required {
			p, ok := props[r.(string)].(map[string]any)
			if !ok {
				continue
			}
			if typ := resolve(p)["type"]; (typ == "array" || typ == "object") && v[r.(string)] == nil {
				out = append(out, at+"."+r.(string))
			}
		}
		for k, sub := range v {
			if p, ok := props[k].(map[string]any); ok {
				out = append(out, nullRequired(defs, p, sub, at+"."+k)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, sub := range v {
				out = append(out, nullRequired(defs, items, sub, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return out
}
