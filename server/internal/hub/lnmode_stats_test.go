package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// statsRequest serves one GET against the ln routes with an optional principal (nil is a full credential).
func statsRequest(t *testing.T, h *Hub, p *principal, target string) (int, []byte) {
	t.Helper()
	mux := http.NewServeMux()
	h.lnRoutes(mux)
	r := httptest.NewRequest("GET", target, nil)
	if p != nil {
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, *p))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func extPrincipal(manager string) *principal {
	return &principal{Role: roleExtension, Manager: manager}
}

// An extension token with the notify scope may read the switch state; only a full credential may change it. The PUT
// guard itself is covered in TestLnInterceptsHeldToManager.
func TestLnModeSwitchReadableByNotifyExtension(t *testing.T) {
	h, _ := lnHub(t)
	if err := h.push.setLnMode(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	code, body := statsRequest(t, h, extPrincipal("manager-a"), "/api/v1/push/ln-mode")
	if code != http.StatusOK {
		t.Fatalf("an extension token read the switch: %d %s", code, body)
	}
	var got struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &got); err != nil || !got.Enabled {
		t.Fatalf("switch answer = %s, want enabled true", body)
	}
}

// The manager filter runs before the 200-entry limit: another manager's burst cannot push this extension's entries
// out of its view. manager-b's three entries are the oldest, so a truncate-then-filter implementation would hide all
// of them behind manager-a's 220 newer ones.
func TestLnInterceptsManagerFilterBeforeLimit(t *testing.T) {
	h, _ := lnHub(t)
	now := h.push.now()
	for i := range 3 {
		h.push.recordIntercept(pushIntercept{At: now.Add(time.Duration(i) * time.Second).UTC(), Level: assistantwire.LevelP2,
			Reason: interceptReasonP2, Manager: "manager-b"})
	}
	for i := range 220 {
		h.push.recordIntercept(pushIntercept{At: now.Add(time.Minute + time.Duration(i)*time.Second).UTC(), Level: assistantwire.LevelP2,
			Reason: interceptReasonP2, Manager: "manager-a"})
	}
	if all := h.push.lnInterceptsView("", 0); len(all) != lnInterceptLimitMax {
		t.Fatalf("full credential view = %d entries, want the 200 cap", len(all))
	}
	own := h.push.lnInterceptsView("manager-b", 0)
	if len(own) != 3 {
		t.Fatalf("manager-b view = %d entries, want all 3 of its own past the 200 cap", len(own))
	}
	for _, e := range own {
		if e.Manager != "manager-b" {
			t.Fatalf("manager-b view contains %q", e.Manager)
		}
	}
	code, body := statsRequest(t, h, extPrincipal("manager-b"), "/api/v1/push/intercepts")
	if code != http.StatusOK {
		t.Fatalf("intercepts over HTTP: %d %s", code, body)
	}
	var viaHTTP struct {
		Intercepts []pushIntercept `json:"intercepts"`
	}
	if err := json.Unmarshal(body, &viaHTTP); err != nil {
		t.Fatal(err)
	}
	if len(viaHTTP.Intercepts) != 3 {
		t.Fatalf("manager-b over HTTP read %d entries, want 3", len(viaHTTP.Intercepts))
	}
}

// Delivered pushes are counted with the switch off as well as on, held pushes carry their reason, and the counts read
// the full records so they stay exact past the 200-entry view limit.
func TestLnDailyStatsCounts(t *testing.T) {
	h, _ := lnHub(t)
	// Switch off: two pushes go out and still count as delivered.
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP1, "off-1", "one")); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP2, "off-2", "two")); err != nil {
		t.Fatal(err)
	}
	if err := h.push.setLnMode(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP0, "p0-key", "rings")); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP2, "p2-key", "silent")); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP1, "p1-key", "held")); err != nil {
		t.Fatal(err)
	}
	// A busy day: 220 more held pushes, beyond the 200-entry view.
	now := h.push.now()
	for i := range 220 {
		h.push.recordIntercept(pushIntercept{At: now.Add(time.Duration(i) * time.Millisecond).UTC(), Level: assistantwire.LevelP2,
			Reason: interceptReasonP2, Manager: "manager-a"})
	}
	stats := h.push.lnDailyStats(now.Add(-24*time.Hour), "")
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want one UTC day", stats)
	}
	got := stats[0]
	if got.Manager != "manager-a" || got.Sent != 3 {
		t.Fatalf("stats = %+v, want 3 delivered (two with the switch off, one P0)", got)
	}
	// 1 P1 + 1 P2 from the delivered pushes + 220 recorded P2 entries.
	if got.Intercepted != 222 || got.ByReason[interceptReasonP2] != 221 || got.ByReason[interceptReasonP1] != 1 {
		t.Fatalf("stats = %+v, want 222 held (221 P2, 1 P1), exact past 200", got)
	}
}

// The stats endpoint answers only the extension's own manager, understands since/days and rejects bad windows. Days
// bucket by UTC calendar day.
func TestLnDailyStatsHeldToManager(t *testing.T) {
	h, _ := lnHub(t)
	base := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	lnSetClock(h, base)
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP1, "a-today", "a")); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPushFrom("manager-b", lnNotification(assistantwire.LevelP1, "b-today", "b")); err != nil {
		t.Fatal(err)
	}
	lnMoveClock(h, base.AddDate(0, 0, -1))
	if err := h.deliverPushFrom("manager-b", lnNotification(assistantwire.LevelP1, "b-yesterday", "b old")); err != nil {
		t.Fatal(err)
	}
	lnMoveClock(h, base)

	read := func(target string) (int, struct {
		Since string       `json:"since"`
		Days  []lnDayStats `json:"days"`
	}) {
		code, body := statsRequest(t, h, extPrincipal("manager-b"), target)
		var out struct {
			Since string       `json:"since"`
			Days  []lnDayStats `json:"days"`
		}
		if code == http.StatusOK {
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
		}
		return code, out
	}
	code, out := read("/api/v1/push/stats/daily")
	if code != http.StatusOK {
		t.Fatalf("default window: %d", code)
	}
	if len(out.Days) != 1 || out.Days[0].Manager != "manager-b" || out.Days[0].Sent != 1 || out.Days[0].Date != "2026-11-14" {
		t.Fatalf("today for manager-b = %+v, want one delivered push on 2026-11-14", out)
	}
	code, out = read("/api/v1/push/stats/daily?days=2")
	if code != http.StatusOK || len(out.Days) != 2 {
		t.Fatalf("days=2: code %d days %+v", code, out.Days)
	}
	if out.Days[0].Date != "2026-11-13" || out.Days[1].Date != "2026-11-14" {
		t.Fatalf("day buckets = %+v, want ascending UTC days", out.Days)
	}
	for _, d := range out.Days {
		if d.Manager != "manager-b" {
			t.Fatalf("manager-b's stats contain %q", d.Manager)
		}
	}
	since := base.AddDate(0, 0, -1).Format(time.RFC3339)
	if code, out = read("/api/v1/push/stats/daily?since=" + since); code != http.StatusOK || len(out.Days) != 2 {
		t.Fatalf("since: code %d days %+v", code, out.Days)
	}
	for _, bad := range []string{"/api/v1/push/stats/daily?days=0", "/api/v1/push/stats/daily?days=99", "/api/v1/push/stats/daily?days=two",
		"/api/v1/push/stats/daily?since=not-a-time"} {
		if code, _ := read(bad); code != http.StatusBadRequest {
			t.Fatalf("%s answered %d, want 400", bad, code)
		}
	}
	// A full credential sees every manager.
	code, body := statsRequest(t, h, nil, "/api/v1/push/stats/daily?days=2")
	if code != http.StatusOK {
		t.Fatalf("full credential: %d %s", code, body)
	}
	var all struct {
		Days []lnDayStats `json:"days"`
	}
	if err := json.Unmarshal(body, &all); err != nil || len(all.Days) != 3 {
		t.Fatalf("full credential days = %+v (%s), want three manager-day rows", all.Days, body)
	}
}

// The delivered-push record lives in hub_settings, so a restart keeps the counts like the switch and the held list.
func TestLnSentPersists(t *testing.T) {
	h, _ := lnHub(t)
	if err := h.deliverPushFrom("manager-a", lnNotification(assistantwire.LevelP1, "persisted-sent", "x")); err != nil {
		t.Fatal(err)
	}
	if !h.push.waitInterceptsSaved(2 * time.Second) {
		t.Fatal("the delivered pushes were not saved")
	}
	restarted := newPusher(t.TempDir(), h.push.store, t.Logf)
	stats := restarted.lnDailyStats(h.push.now().Add(-24*time.Hour), "")
	if len(stats) != 1 || stats[0].Sent != 1 || stats[0].Manager != "manager-a" {
		t.Fatalf("restored stats = %+v, want one delivered push", stats)
	}
}
