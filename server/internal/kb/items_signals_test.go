package kb

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

type signalItems struct{ items []kbwire.Item }

func (s *signalItems) Items(context.Context) ([]kbwire.Item, error) { return s.items, nil }
func (s *signalItems) DocTitle(context.Context, string) string      { return "" }

// A document's signals are kept as the source last reported them; a change of signals alone does not tag it again.
func TestCollectItemsKeepsTheLatestSignals(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: st}
	at := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	src := &signalItems{items: []kbwire.Item{{Kind: KindDoc, ID: "d1", Title: "Example doc", At: at, Signals: []string{kbwire.SignalViewed}}}}
	if err := e.CollectItems(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	it, ok := st.Item(KindDoc, "d1")
	if !ok || !slices.Equal(it.Signals, []string{kbwire.SignalViewed}) {
		t.Fatalf("first collect: %+v", it)
	}
	tagged := at.Add(time.Hour)
	it.TaggedAt, it.Note = tagged, "a note"
	if err := st.PutItem(it); err != nil {
		t.Fatal(err)
	}

	src.items[0].Signals = []string{kbwire.SignalAuthored, kbwire.SignalViewed}
	if err := e.CollectItems(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	it, _ = st.Item(KindDoc, "d1")
	if !slices.Equal(it.Signals, []string{kbwire.SignalAuthored, kbwire.SignalViewed}) {
		t.Fatalf("signals after the second collect: %v", it.Signals)
	}
	if !it.TaggedAt.Equal(tagged) || it.Note != "a note" {
		t.Fatalf("a change of signals alone tagged it again or lost its note: %+v", it)
	}

	src.items[0].Signals = nil
	if err := e.CollectItems(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if it, _ = st.Item(KindDoc, "d1"); len(it.Signals) != 0 || it.Note != "a note" {
		t.Fatalf("a source that no longer reports signals: %+v", it)
	}
}
