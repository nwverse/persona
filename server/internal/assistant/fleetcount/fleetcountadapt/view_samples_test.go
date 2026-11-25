package fleetcountadapt

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// viewSamples are the counts views of schema/wire/counts.schema.json the clients' round trips read
// (docs/protocol/counts.md, #15512).
const viewSamples = "../../../../../spec/fixtures/counts/view_samples.json"

// viewCount writes the Agents list's tally as the view's agents_needs_you: the counted rows as keys, the rows the
// count leaves out as held, each with this adapter's spelling of its Why (whyWord). A quiet task's row is in neither:
// the round that makes the view never hands those agents in (fleetInputsInto leaves them out), and the view lists no
// quiet rows (docs/protocol/counts.md). Nothing in the manager writes a view before step 7a of
// docs/plans/plan-derived-counts.md, so this test is what holds the samples to fleetcount's rows until then.
type viewCount struct {
	f     *Fleet
	count assistantwire.CountsViewCount
	last  int // the place in keys of the row a further card goes under, -1 after a row the count leaves out
}

func (v *viewCount) row(key string, why fleetcount.Why, cards ...string) {
	v.last = -1
	if why.Counted() {
		v.count.Keys = append(v.count.Keys, assistantwire.CountsRow{ID: assistantwire.CountsRowKey(key)})
		v.last = len(v.count.Keys) - 1
		for _, c := range cards {
			v.card(c)
		}
		return
	}
	why.Accept(&v.f.why)
	if v.f.why.word != "quiet" {
		v.count.Held = append(v.count.Held, assistantwire.CountsHeldRow{ID: assistantwire.CountsRowKey(key),
			Why: assistantwire.CountsHeldReason(v.f.why.word)})
	}
}

func (v *viewCount) card(id string) {
	if v.last >= 0 {
		row := &v.count.Keys[v.last]
		row.Cards = append(row.Cards, assistantwire.CountsCardID(id))
	}
}

func (v *viewCount) agentKey(agent slot.Slot) string {
	s := v.f.seats[agent.Wire()].session
	return "agent:" + s.MachineID + "/" + s.SessionID
}

func (v *viewCount) VisitAgent(agent slot.Slot, why fleetcount.Why, _ instant.Instant) {
	v.row(v.agentKey(agent), why)
}

func (v *viewCount) VisitAgentByCard(agent, card slot.Slot, why fleetcount.Why) {
	v.row(v.agentKey(agent), why, v.f.ixs[card.Wire()].ix.ID)
}

func (v *viewCount) VisitCard(card slot.Slot, why fleetcount.Why) {
	id := v.f.ixs[card.Wire()].ix.ID
	v.row("card:"+id, why, id)
}

func (v *viewCount) VisitCardUnder(card slot.Slot) { v.card(v.f.ixs[card.Wire()].ix.ID) }

func (v *viewCount) VisitTotal(total fleetcount.RowCount) { v.count.N = total.Wire() }

// Each view sample that names a needs-you case holds, as its agents_needs_you, the rows and the number fleetcount
// decides for that case's sessions and cards on the Agents list, read through this adapter, with each held row's
// reason a word the wire knows; its totals repeat the number.
func TestViewSamplesAreFleetcountsRows(t *testing.T) {
	byName := map[string]needsYouCase{}
	for _, c := range needsYouCases(t) {
		byName[c.Name] = c
	}
	b, err := os.ReadFile(viewSamples)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				NeedsYou string `json:"needs_you"`
			} `json:"input"`
			Expected struct {
				View assistantwire.CountsView `json:"view"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatalf("%s: %v", viewSamples, err)
	}
	checked := 0
	for _, s := range file.Cases {
		if s.Input.NeedsYou == "" {
			continue // a sample of the shape alone (more, an absent count), which no fixture case gives
		}
		t.Run(s.Name, func(t *testing.T) {
			c, ok := byName[s.Input.NeedsYou]
			if !ok {
				t.Fatalf("no needs-you case %q", s.Input.NeedsYou)
			}
			want := s.Expected.View.Counts.AgentsNeedsYou
			if want == nil {
				t.Fatal("no agents_needs_you")
			}
			f := c.fleet(t)
			got := viewCount{f: f, last: -1}
			f.decide(fleetcount.SurfaceBadge()).Accept(&got)
			if !reflect.DeepEqual(got.count, *want) {
				g, _ := json.Marshal(got.count)
				w, _ := json.Marshal(want)
				t.Fatalf("fleetcount's rows\n %s\nthe sample's\n %s", g, w)
			}
			for _, h := range want.Held {
				if !h.Why.Valid() {
					t.Errorf("%s: held as %q, a word the wire does not know", h.ID, h.Why)
				}
			}
			if tot := s.Expected.View.Totals; tot != nil && (tot.AgentsNeedsYou == nil || *tot.AgentsNeedsYou != want.N) {
				t.Errorf("totals %v, want agents_needs_you %d", tot.AgentsNeedsYou, want.N)
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatalf("%s: no sample names a needs-you case", viewSamples)
	}
}
