package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// ParseView reads and checks a View a plugin answered with.
func ParseView(raw json.RawMessage) (*View, error) {
	var v View
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if v.Sections == nil {
		v.Sections = []Section{}
	}
	for i := range v.Sections {
		if v.Sections[i].Items == nil {
			v.Sections[i].Items = []UIElem{}
		}
	}
	return &v, CheckView(&v)
}

// CheckView agrees with the schema's examples of views: each one holds, each one under invalid/ is refused.
func TestCheckViewAgreesWithTheSchema(t *testing.T) {
	for _, dir := range []string{"", "invalid"} {
		files, _ := filepath.Glob(filepath.Join("..", "..", "..", "schema", "extension", "examples", dir, "View.*.json"))
		if len(files) == 0 {
			t.Fatalf("no examples in %q", dir)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseView(raw)
			if dir == "" && err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
			if dir == "invalid" && err == nil {
				t.Errorf("%s: accepted", filepath.Base(f))
			}
		}
	}
}

// Fit leaves out what the asking app cannot draw (and a section left with nothing), raises refresh_after to its least,
// and holds a card to its size.
func TestFitAssistantOnlySections(t *testing.T) {
	v := &View{Sections: []Section{
		{Items: []UIElem{{Type: "html", Html: "<p>a</p>"}}},
		{Items: []UIElem{{Type: "html", Html: "<p>b</p>"}}},
		{Items: []UIElem{{Type: "html", Html: "<p>c</p>"}}},
	}}
	card, _ := Slot("overview_card")
	dropped, err := Fit(v, card, UIVersion)
	if err != nil || len(v.Sections) != 0 || !slices.Contains(dropped, "html") {
		t.Fatalf("sections=%+v dropped=%v err=%v", v.Sections, dropped, err)
	}
}

func TestRestoreNativePresenceByKeyAfterSiblingChanges(t *testing.T) {
	decode := func(raw string) any {
		t.Helper()
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	source := decode(`[{"key":"a","title":"A","disabled":false},{"key":"b","title":"B","disabled":false},{"key":"c","title":"C","disabled":false}]`)
	old := decode(`[{"key":"a","title":"A"},{"key":"b","title":"B"},{"key":"c","title":"C"}]`)
	next := decode(`[{"key":"c","title":"Changed"},{"key":"b","title":"B"},{"key":"new","title":"New"}]`)
	want := decode(`[{"key":"c","title":"Changed","disabled":false},{"key":"b","title":"B","disabled":false},{"key":"new","title":"New"}]`)
	if got := restorePresence(source, old, next); !reflect.DeepEqual(got, want) {
		t.Fatalf("shifted native presence: got %#v, want %#v", got, want)
	}
}

func TestFit(t *testing.T) {
	chart := UIElem{Type: "chart", Kind: "line", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1}}}}}
	view := func() *View {
		return &View{RefreshAfter: 1, Sections: []Section{
			{Title: "图", Items: []UIElem{chart}},
			{Title: "数", Items: []UIElem{{Type: "metric", Label: "a", Value: "1"}, {Type: "hologram"}}}}}
	}
	page, _ := Slot("nav_page")
	v := view()
	dropped, err := Fit(v, page, 1)
	slices.Sort(dropped) // unknown types are left out before the check, those newer than the app after it
	if err != nil || len(v.Sections) != 1 || v.Sections[0].Title != "数" || len(v.Sections[0].Items) != 1 || v.RefreshAfter != RefreshLeast ||
		!slices.Equal(dropped, []string{"chart", "hologram"}) {
		t.Fatalf("%v %+v %v", err, v, dropped)
	}
	v = view()
	if dropped, err := Fit(v, page, UIVersion); err != nil || len(v.Sections) != 2 || !slices.Equal(dropped, []string{"hologram"}) {
		t.Fatalf("%v %+v %v", err, v, dropped)
	}
	card, _ := Slot("overview_card")
	field := &View{Sections: []Section{{Items: []UIElem{{Type: "field", Name: "n", Label: "N", Kind: "text"}}}}}
	if _, err := Fit(field, card, UIVersion); err == nil {
		t.Fatal("a card has no fields")
	}
	if _, err := Fit(&View{Sections: make([]Section, CompactSections+1)}, card, UIVersion); err == nil {
		t.Fatal("a card has few sections")
	}
	if _, err := Fit(&View{RefreshAfter: 10 * RefreshMost}, page, UIVersion); err != nil {
		t.Fatal(err)
	}
}
