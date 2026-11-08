package update

import (
	"encoding/json"
	"slices"
	"testing"
)

// A hub that runs as persona-hub writes Run beside Target. Both directions across builds hold: an older edge or
// watchdog, which knows only Target, still finds personad there and runs it as `personad hub run` (the same hub); a
// pending written by an older build, without Run, runs Target as before.
func TestPendingRunAcrossBuilds(t *testing.T) {
	written := Pending{Target: "/data/bin/personad", Prev: "/data/bin/personad.prev", Run: "/data/bin/persona-hub",
		Args: []string{"/data/bin/persona-hub", "run"}}
	b, err := json.Marshal(written)
	if err != nil {
		t.Fatal(err)
	}
	// What an older build reads of it (its Pending had no Run).
	var older struct {
		Target string   `json:"target"`
		Args   []string `json:"args"`
	}
	if err := json.Unmarshal(b, &older); err != nil {
		t.Fatal(err)
	}
	if older.Target != "/data/bin/personad" || !slices.Equal(TargetArgs(older.Args, older.Target), []string{"hub", "run"}) {
		t.Fatalf("an older build would run %q %v", older.Target, TargetArgs(older.Args, older.Target))
	}
	var now Pending
	if err := json.Unmarshal(b, &now); err != nil {
		t.Fatal(err)
	}
	if now.RunPath() != "/data/bin/persona-hub" || !slices.Equal(TargetArgs(now.Args, now.RunPath()), []string{"run"}) {
		t.Fatalf("this build runs %q %v", now.RunPath(), TargetArgs(now.Args, now.RunPath()))
	}
	// A pending an older build wrote.
	var old Pending
	if err := json.Unmarshal([]byte(`{"target":"/data/bin/personad","prev":"/data/bin/personad.prev","args":["/data/bin/personad","hub","run"]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.RunPath() != "/data/bin/personad" || !slices.Equal(TargetArgs(old.Args, old.RunPath()), []string{"hub", "run"}) {
		t.Fatalf("an older pending runs %q %v", old.RunPath(), TargetArgs(old.Args, old.RunPath()))
	}
	// A hub run as personad writes no Run.
	b, err = json.Marshal(Pending{Target: "/data/bin/personad"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["run"]; ok {
		t.Fatalf("an empty Run is written: %s", b)
	}
}
