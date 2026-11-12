package hub

import (
	"encoding/json"
	"testing"
	"time"
)

// The pace of the widgets' wakes (#5721): Needs You at once, other changes after widgetGap, a heartbeat when nothing
// changed, and the first time always.
func TestWidgetWakePace(t *testing.T) {
	var w widgetWaker
	t0 := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	c := widgetCounts{needsYou: 1, working: 2}
	if !w.due("phone", c, t0) {
		t.Fatal("first time: not due")
	}
	w.wrote("phone", c, t0)
	cases := []struct {
		name  string
		c     widgetCounts
		after time.Duration
		due   bool
	}{
		{"same counts, soon", c, time.Minute, false},
		{"needs you changed, at once", widgetCounts{needsYou: 2, working: 2}, time.Second, true},
		{"needs you back to none, at once", widgetCounts{needsYou: 0, working: 2}, time.Second, true},
		{"working changed, within the gap", widgetCounts{needsYou: 1, working: 3}, widgetGap - time.Second, false},
		{"working changed, after the gap", widgetCounts{needsYou: 1, working: 3}, widgetGap, true},
		{"finished changed, after the gap", widgetCounts{needsYou: 1, working: 2, unread: 1}, widgetGap, true},
		{"nothing changed, before the heartbeat", c, widgetHeartbeat - time.Second, false},
		{"nothing changed, the heartbeat", c, widgetHeartbeat, true},
	}
	for _, tc := range cases {
		if got := w.due("phone", tc.c, t0.Add(tc.after)); got != tc.due {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.due)
		}
	}
	w.forget("phone")
	if !w.due("phone", c, t0.Add(time.Second)) {
		t.Error("forgotten: not due again")
	}
}

// Through the hub: a phone that has widgets and is not open gets one silent "widgets" wake for a change, none for the
// same counts again, another at once when Needs You changes; one without widgets gets none.
func TestWidgetWakePushes(t *testing.T) {
	h, f := testLive(t)
	h.push.register(Device{DeviceID: "other", Name: "other", APNSToken: "othertok", Topic: "app.example"})
	prefs := devicePrefs(nil)
	prefs.Widgets = true
	if err := h.push.setPrefsBy("phone", "", prefs); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	h.live.now = func() time.Time { return clock }
	wakes := func() (phone, other int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, call := range f.got {
			var body map[string]any
			_ = json.Unmarshal(call.Body, &body)
			if call.Type != "background" || body["persona_wake"] != "widgets" {
				continue
			}
			switch call.Token {
			case "devtok":
				phone++
			case "othertok":
				other++
			}
		}
		return
	}
	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if got, _ := wakes(); got == want {
				return
			} else if got > want || time.Now().After(deadline) {
				t.Fatalf("phone wakes = %d, want %d", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	h.live.wakeWidgets(FleetState{Phase: "working", Working: 1})
	waitFor(1)
	clock = clock.Add(time.Minute)
	h.live.wakeWidgets(FleetState{Phase: "working", Working: 1})
	h.live.wakeWidgets(FleetState{Phase: "working", Working: 2}) // within widgetGap: held
	time.Sleep(100 * time.Millisecond)
	waitFor(1)
	h.live.wakeWidgets(FleetState{Phase: "needsYou", NeedsYou: 1, Working: 2})
	waitFor(2)
	if _, other := wakes(); other != 0 {
		t.Fatalf("a phone without widgets was woken %d times", other)
	}
	// The app went to the background with these counts: it wrote them, so no wake for them after it.
	h.live.phoneLeft("phone", true)
	clock = clock.Add(widgetGap)
	h.live.wakeWidgets(h.live.shownState)
	time.Sleep(100 * time.Millisecond)
	waitFor(2)
}
