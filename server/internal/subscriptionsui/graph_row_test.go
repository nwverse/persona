package subscriptionsui

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The list's Source Relationships row (#5741): only for an app that has the view (the iPhone's), on the whole list, and
// its press answers the link the app pushes the view for.
func TestGraphRow(t *testing.T) {
	f, b := newFake(), newBuiltins()
	e := sourcesEnv(i18n.ZhHans, false, b)
	if item(draw(t, f, e, state{}), "x.graph") != nil {
		t.Fatal("an app without the view gets the row")
	}
	e.Graph = true
	r := item(draw(t, f, e, state{}), "x.graph")
	if r == nil || r.Title != "来源关系图" || r.Subtitle == "" || r.Action == nil || r.Action.ID != "graph" {
		t.Fatalf("the row: %+v", r)
	}
	if item(draw(t, f, e, state{C: "apps"}), "x.graph") != nil {
		t.Error("a list narrowed to one connector gets the row")
	}
	// It only shows something: a device that may only look keeps the row (drawn above) and may press it.
	res, err := Handle(context.Background(), f, sourcesEnv(i18n.En, false, b), Request{Action: "graph"})
	if err != nil || res.OpenURL != GraphURL {
		t.Errorf("the press from a device that may only look: %+v %v", res, err)
	}
}
