package extension

import (
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func navigateView() *View {
	return &View{Sections: []Section{{Items: []UIElem{{
		Type: wire.ComponentCode, Key: "code", Path: "main.go", Text: "package main\n", StartLine: 1,
		Navigate: &wire.Action{ID: "nav"},
	}}}}}
}

// An app of UI version 13 keeps a code's navigate; an older one, or a native stream, gets the same code without it.
func TestNavigateIsLeftOutBelow13(t *testing.T) {
	page, _ := Slot("nav_page")
	v := navigateView()
	if _, err := Fit(v, page, 13); err != nil {
		t.Fatal(err)
	}
	if e := items(v)[0]; e.Navigate == nil || e.Navigate.ID != "nav" {
		t.Fatalf("13 keeps navigate: %+v", e)
	}
	for _, version := range []int{4, 5, 12} {
		v := navigateView()
		if _, err := Fit(v, page, version); err != nil {
			t.Fatalf("%d: %v", version, err)
		}
		if e := items(v)[0]; e.Type != wire.ComponentCode || e.Navigate != nil || e.Text != "package main\n" {
			t.Fatalf("%d: %+v", version, e)
		}
	}
	v = navigateView()
	if _, err := fit(v, page, 13, nil, true); err != nil {
		t.Fatal(err)
	}
	if items(v)[0].Navigate != nil {
		t.Fatal("a native stream has no navigate")
	}
}
