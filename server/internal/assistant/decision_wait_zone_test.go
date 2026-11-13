package assistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A store swapped in mid-test answers with the settings saved in it: the briefing's hour is read in the user's time
// zone, not the machine's. Swapped without them, the zone falls back to time.Local once the settings cache goes stale,
// and a held decision then becomes waitNoBriefingTime on machines whose own zone puts the routine's next run past its
// deadline: TestDecisionThatCannotBeKeptAsHeldIsPushed would pass only at some hours of the day.
func TestDecisionKeepsItsBriefingTimeZoneAfterAStoreSwap(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	// A zone that is not this machine's, whatever it runs in: the fallback would show up as this.
	zone := "Asia/Shanghai"
	if time.Local.String() == zone {
		zone = "Australia/Sydney"
	}
	setSettings(t, s, func(v *Settings) { v.Timezone = zone })
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	s.accept.mu.Lock()
	s.loadAcceptLocked(context.Background())
	s.accept.mu.Unlock()
	st := &memStore{}
	if err := st.PutSetting(context.Background(), settingsKey, s.settings(context.Background())); err != nil {
		t.Fatal(err)
	}
	s.prefs.mu.Lock()
	s.prefs.at = time.Time{} // settings read again, from the store that is there now
	s.prefs.mu.Unlock()
	s.store = func() Store { return st }
	if got := s.location(context.Background()).String(); got != zone {
		t.Fatalf("the briefing's time zone after the store was swapped: %q, want %q", got, zone)
	}
	if res := reviewTask(t, s, todo.ID, waitingDecision()); !strings.Contains(res, "can wait (can_wait)") {
		t.Fatalf("task_review's answer: %s", res)
	}
	if a := askOf(s, todo.ID); a == nil || !a.ForBriefing {
		t.Fatalf("ask %+v", a)
	}
}
