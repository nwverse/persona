package fleetcountadapt_test

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount/fleetcountadapt"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
)

// fixtures are stored records as the store writes them; each replays through Restore and Store unchanged. Add the
// records a real store holds, written as json.Marshal writes them.
var fixtures = []string{
	`{"id":"agent 1","state":"working","role":"worker","lineage":"root","muting":"heard","visibility":"listed","card_standing":"stands","telling":"told","holding":"held_by_persona","why":"needs_you","surface":"badge"}`,
}

func TestFixturesReplayUnchanged(t *testing.T) {
	for _, fixture := range fixtures {
		var r fleetcountadapt.Record
		if err := json.Unmarshal([]byte(fixture), &r); err != nil {
			t.Fatal(err)
		}
		x := domaintest.Ok(t, fleetcountadapt.Restore(r))
		got, err := json.Marshal(fleetcountadapt.Store(x))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != fixture {
			t.Errorf("replayed\n%s\nas\n%s", fixture, got)
		}
	}
}

func TestRestoreRefusesWhatTheStoreCannotHold(t *testing.T) {
	r := fleetcountadapt.Record{ID: "agent 1", State: "from a newer peer", Role: "worker", Lineage: "root", Muting: "heard", Visibility: "listed", CardStanding: "stands", Telling: "told", Holding: "held_by_persona", Why: "needs_you", Surface: "badge"}
	got := domaintest.Fails(t, fleetcountadapt.Restore(r))
	if want := "invalid fleetcount.agent.no_state state"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
