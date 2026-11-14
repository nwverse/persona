package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

type inputClipFixtureCase struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Expected struct {
		Check        string  `json:"check"`
		InputBytes   int     `json:"input_bytes"`
		OutputBytes  int     `json:"output_bytes"`
		Remaining    *int    `json:"remaining"`
		ArrayItems   *int    `json:"array_items"`
		ObjectFields *int    `json:"object_fields"`
		Marker       bool    `json:"marker"`
		MarkerKey    string  `json:"marker_key"`
		KeyPrefix    string  `json:"key_prefix"`
		Value        *string `json:"value"`
	} `json:"expected"`
}

func inputClipFixtureInt(t *testing.T, v any) int {
	t.Helper()
	n, ok := v.(float64)
	if !ok || n != float64(int(n)) {
		t.Fatalf("fixture count %v is not an integer", v)
	}
	return int(n)
}

func expandInputClipFixture(t *testing.T, v any, index int) any {
	t.Helper()
	switch v := v.(type) {
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = expandInputClipFixture(t, e, index)
		}
		return out
	case map[string]any:
		if target, ok := v["$pad_object_to"]; ok {
			value, ok := expandInputClipFixture(t, v["value"], index).(map[string]any)
			if !ok {
				t.Fatal("$pad_object_to value is not an object")
			}
			if _, exists := value["pad"]; exists {
				t.Fatal("$pad_object_to value already has pad")
			}
			value["pad"] = ""
			base := len(marshalPlain(value))
			padding := inputClipFixtureInt(t, target) - base
			if padding < 0 {
				t.Fatalf("padded object is already %d bytes", base)
			}
			value["pad"] = strings.Repeat("x", padding)
			if got := len(marshalPlain(value)); got != base+padding {
				t.Fatalf("padded object is %d bytes, want %d", got, base+padding)
			}
			return value
		}
		if count, ok := v["$repeat"]; ok {
			out := make([]any, inputClipFixtureInt(t, count))
			for i := range out {
				out[i] = expandInputClipFixture(t, v["value"], i)
			}
			return out
		}
		if key, ok := v["$single_field"]; ok {
			name, ok := expandInputClipFixture(t, key, index).(string)
			if !ok {
				t.Fatal("$single_field key is not a string")
			}
			return map[string]any{name: expandInputClipFixture(t, v["value"], index)}
		}
		if count, ok := v["$fields"]; ok {
			out := make(map[string]any, inputClipFixtureInt(t, count))
			prefix, _ := v["key"].(string)
			pad := inputClipFixtureInt(t, v["pad"])
			for i := 0; i < inputClipFixtureInt(t, count); i++ {
				key := fmt.Sprintf("%s%0*d", prefix, pad, i)
				out[key] = expandInputClipFixture(t, v["value"], i)
			}
			return out
		}
		if width, ok := v["$index"]; ok {
			w := inputClipFixtureInt(t, width)
			if w == 0 {
				return strconv.Itoa(index)
			}
			return fmt.Sprintf("%0*d", w, index)
		}
		if text, ok := v["$string"]; ok {
			return strings.Repeat(text.(string), inputClipFixtureInt(t, v["count"]))
		}
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = expandInputClipFixture(t, e, index)
		}
		return out
	default:
		return v
	}
}

func inputClipTestObject(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want an object", v)
	}
	return m
}

func inputClipTestArray(t *testing.T, v any) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("got %T, want an array", v)
	}
	return a
}

func inputClipMarkerCounts(t *testing.T, note string) (arrayItems, objectFields int) {
	t.Helper()
	first, _, _ := strings.Cut(note, "\n")
	if _, err := fmt.Sscanf(first, "Omitted %d array items and %d object fields.", &arrayItems, &objectFields); err != nil {
		t.Fatalf("bad marker %q: %v", first, err)
	}
	return arrayItems, objectFields
}

func TestClipInputSharedCases(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/fixtures/input-clip/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []inputClipFixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var directive any
			if err := json.Unmarshal(c.Input, &directive); err != nil {
				t.Fatal(err)
			}
			expanded := expandInputClipFixture(t, directive, 0)
			input := marshalPlain(expanded)
			if c.Expected.InputBytes > 0 && len(input) != c.Expected.InputBytes {
				t.Fatalf("input is %d bytes, want %d", len(input), c.Expected.InputBytes)
			}
			before := append([]byte(nil), input...)
			out := clipJSON(input)
			if !bytes.Equal(input, before) {
				t.Fatal("clipJSON changed its source bytes")
			}
			if len(out) > maxInputLen {
				t.Fatalf("output is %d bytes, want at most %d", len(out), maxInputLen)
			}
			if c.Expected.OutputBytes > 0 && len(out) != c.Expected.OutputBytes {
				t.Fatalf("output is %d bytes, want %d", len(out), c.Expected.OutputBytes)
			}
			if again := clipJSON(input); !bytes.Equal(out, again) {
				t.Fatal("clipJSON is not deterministic")
			}
			if again := clipJSON(out); !bytes.Equal(out, again) {
				t.Fatal("a bounded result changed when clipped again")
			}
			if !c.Expected.Marker {
				if !bytes.Equal(out, input) {
					t.Fatalf("unmarked output changed: %d bytes from %d", len(out), len(input))
				}
				return
			}
			var decoded map[string]any
			if err := json.Unmarshal(out, &decoded); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}
			markerKey := c.Expected.MarkerKey
			if markerKey == "" {
				markerKey = inputClipMarker
			}
			note, ok := decoded[markerKey].(string)
			if !ok {
				t.Fatalf("marker %q is %T, want a string", markerKey, decoded[markerKey])
			}
			if !strings.HasSuffix(note, fmt.Sprintf(clipNoteFormat, len(input))) {
				t.Fatalf("marker %q does not end with the original-size cut note", note)
			}
			if !clipped(decoded) {
				t.Fatal("the hub's recursive cut detector did not recognize the marker")
			}
			arrayItems, objectFields := inputClipMarkerCounts(t, note)
			if c.Expected.ArrayItems != nil && arrayItems != *c.Expected.ArrayItems {
				t.Fatalf("marker says %d array items, want %d", arrayItems, *c.Expected.ArrayItems)
			}
			if c.Expected.ObjectFields != nil && objectFields != *c.Expected.ObjectFields {
				t.Fatalf("marker says %d object fields, want %d", objectFields, *c.Expected.ObjectFields)
			}

			switch c.Expected.Check {
			case "short_values":
				values := inputClipTestArray(t, decoded["values"])
				if c.Expected.Remaining != nil && len(values) != *c.Expected.Remaining {
					t.Fatalf("kept %d rows, want %d", len(values), *c.Expected.Remaining)
				}
				for i, row := range values {
					fields := inputClipTestArray(t, row)
					if len(fields) != 3 || fields[0] != strconv.Itoa(i) || fields[1] != strings.Repeat("a", 17) || fields[2] != strings.Repeat("b", 18) {
						t.Fatalf("row %d changed: %#v", i, fields)
					}
				}
				if arrayItems != 4_000-len(values) || objectFields != 0 {
					t.Fatalf("marker counts (%d, %d) do not match %d retained rows", arrayItems, objectFields, len(values))
				}
			case "nested_arrays":
				outer := inputClipTestArray(t, decoded["outer"])
				if len(outer) == 0 || len(outer) == 200 {
					t.Fatalf("kept %d outer rows, want a nonempty proper prefix", len(outer))
				}
				shortened := false
				for i, row := range outer {
					inner := inputClipTestArray(t, row)
					if len(inner) == 0 || len(inner) > 100 {
						t.Fatalf("inner row %d has %d items", i, len(inner))
					}
					shortened = shortened || len(inner) < 100
					for _, value := range inner {
						if value != "x" {
							t.Fatalf("inner row %d changed a value: %#v", i, value)
						}
					}
				}
				if !shortened || objectFields != 0 {
					t.Fatalf("outer rows %d, inner shortened %v, object fields %d", len(outer), shortened, objectFields)
				}
			case "object_array":
				if decoded["file_path"] != "/home/user/project/a.go" {
					t.Fatalf("file_path changed: %#v", decoded["file_path"])
				}
				edits := inputClipTestArray(t, decoded["edits"])
				if len(edits) == 0 || len(edits) == 2000 {
					t.Fatalf("kept %d edits, want a nonempty proper prefix", len(edits))
				}
				for i, edit := range edits {
					fields := inputClipTestObject(t, edit)
					if len(fields) != 2 || fields["old_string"] != "a" || fields["new_string"] != "b" {
						t.Fatalf("edit %d changed: %#v", i, fields)
					}
				}
			case "todo_array":
				todos := inputClipTestArray(t, decoded["todos"])
				if len(todos) == 0 || len(todos) == 2000 {
					t.Fatalf("kept %d to-dos, want a nonempty proper prefix", len(todos))
				}
				for i, todo := range todos {
					fields := inputClipTestObject(t, todo)
					if len(fields) != 3 || fields["content"] != "Do the work" || fields["activeForm"] != "Doing the work" || fields["status"] != "pending" {
						t.Fatalf("to-do %d changed: %#v", i, fields)
					}
				}
			case "question_array":
				questions := inputClipTestArray(t, decoded["questions"])
				if len(questions) == 0 || len(questions) == 1000 {
					t.Fatalf("kept %d questions, want a nonempty proper prefix", len(questions))
				}
				for i, question := range questions {
					fields := inputClipTestObject(t, question)
					options := inputClipTestArray(t, fields["options"])
					if fields["question"] != "Choose one?" || fields["header"] != "Choice" || fields["multi_select"] != false || len(options) != 1 {
						t.Fatalf("question %d changed: %#v", i, fields)
					}
					option := inputClipTestObject(t, options[0])
					if option["label"] != "A" || option["description"] != "First" {
						t.Fatalf("question %d option changed: %#v", i, option)
					}
				}
			case "wide_object":
				if values := inputClipTestArray(t, decoded["values"]); len(values) != 0 || arrayItems != 100 {
					t.Fatalf("array has %d items and marker says %d, want 0 and 100", len(values), arrayItems)
				}
				wide := inputClipTestObject(t, decoded["wide"])
				if len(wide) == 0 || len(wide) == 2000 || objectFields != 2000-len(wide) {
					t.Fatalf("wide object has %d fields and marker says %d omitted", len(wide), objectFields)
				}
				for i := 0; i < len(wide); i++ {
					key := fmt.Sprintf("a_long_field_name_with_many_bytes_%04d", i)
					if wide[key] != strconv.Itoa(i) {
						t.Fatalf("field %q changed or is out of order: %#v", key, wide[key])
					}
				}
			case "root_wide":
				if arrayItems != 0 {
					t.Fatalf("root-wide object omitted %d array items, want 0", arrayItems)
				}
				retained := len(decoded) - 1 // the synthetic marker is the only non-fixture field
				if retained <= 0 || retained >= 2000 || objectFields != 2000-retained {
					t.Fatalf("root kept %d fields and marker says %d omitted", retained, objectFields)
				}
				for i := 0; i < retained; i++ {
					key := fmt.Sprintf("%s%04d", c.Expected.KeyPrefix, i)
					want := strconv.Itoa(i)
					if c.Expected.Value != nil {
						want = *c.Expected.Value
					}
					if decoded[key] != want {
						t.Fatalf("root field %q changed or is out of order: %#v, want %q", key, decoded[key], want)
					}
				}
			case "single_long_key":
				if len(decoded) != 1 || arrayItems != 0 || objectFields != 1 {
					t.Fatalf("single long key left %d fields and counts (%d, %d)", len(decoded), arrayItems, objectFields)
				}
			case "mixed_strings":
				long, ok := decoded["long"].(string)
				if !ok || !strings.HasPrefix(long, "abcdef") || !strings.HasSuffix(long, fmt.Sprintf(clipNoteFormat, 6000)) {
					t.Fatalf("long string did not go through the string passes: %q", long)
				}
				values := inputClipTestArray(t, decoded["values"])
				if len(values) == 0 || len(values) == 3000 {
					t.Fatalf("kept %d mixed rows, want a nonempty proper prefix", len(values))
				}
				for i, row := range values {
					fields := inputClipTestArray(t, row)
					if len(fields) != 4 || fields[0] != "short" || fields[1] != strconv.Itoa(i) || fields[2] != true || fields[3] != nil {
						t.Fatalf("mixed row %d changed: %#v", i, fields)
					}
				}
			case "one_byte_over":
				values := inputClipTestArray(t, decoded["values"])
				if c.Expected.Remaining == nil || len(values) != *c.Expected.Remaining || arrayItems != 2430-len(values) || objectFields != 0 {
					t.Fatalf("boundary kept %d items with counts (%d, %d)", len(values), arrayItems, objectFields)
				}
				for i, value := range values {
					if value != "1234567" {
						t.Fatalf("boundary item %d changed: %#v", i, value)
					}
				}
				if decoded["pad"] != inputClipTestObject(t, expanded)["pad"] {
					t.Fatal("boundary padding changed")
				}
				// One more retained item, with the correspondingly shorter count in the marker, must not fit.
				decoded["values"] = append(values, "1234567")
				decoded[markerKey] = inputClipNote(len(input), arrayItems-1, objectFields)
				if restored := marshalPlain(decoded); len(restored) <= maxInputLen {
					t.Fatalf("one more item would still fit in %d bytes; the cut is not minimal", len(restored))
				}
			case "marker_collision":
				if decoded[inputClipMarker] != "keep this user value" || decoded[inputClipMarker+"_2"] != "keep this second user value" {
					t.Fatalf("user marker-like fields changed: %#v / %#v", decoded[inputClipMarker], decoded[inputClipMarker+"_2"])
				}
				if values := inputClipTestArray(t, decoded["values"]); len(values) == 0 || len(values) == 4000 {
					t.Fatalf("kept %d collision rows, want a nonempty proper prefix", len(values))
				}
			default:
				t.Fatalf("unknown shared check %q", c.Expected.Check)
			}
		})
	}
}
