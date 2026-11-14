package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// Low-distraction ("ln") mode: when its switch is on, the hub delivers pushes by urgency instead of pushing
// everything. P0 still interrupts, P1 is held for the "Needs you" list / briefings, P2 stays silent, and the same
// related key may interrupt at most once in 12 hours. Every push the switch holds back is recorded with its reason,
// in memory and in hub_settings, behind a read-only API for extensions and the nightly review. With the switch off
// (the default) every push goes out exactly as before; the filter is a no-op.

const (
	lnModeSettingKey       = "hub.ln_mode"
	lnInterceptsSettingKey = "hub.ln_intercepts"
	lnSentSettingKey       = "hub.push_sent"

	// lnDedupWindow: how long the same related key stays quiet after it pushed once.
	lnDedupWindow = 12 * time.Hour
	// lnConfigTTL bounds how often one Pusher asks the database for the switch while pushes keep coming.
	lnConfigTTL = 15 * time.Second
	// lnInterceptMax bounds how many held pushes are kept; the oldest go first.
	lnInterceptMax = 500
	// lnInterceptAge: held pushes older than this are not worth showing any more.
	lnInterceptAge      = 14 * 24 * time.Hour
	lnInterceptLimitMax = 200
	// lnSentMax bounds how many delivered pushes the daily counts remember; the oldest go first. Counts read this
	// full record, so they stay exact on a busy day where the 200-entry intercepts view would truncate.
	lnSentMax = 2000
	// lnStatsDaysMax: how far back the read-only daily stats may go, matching the records' age.
	lnStatsDaysMax = 14
)

// Why a push was held back.
const (
	interceptReasonP1    = "ln_p1_held"   // P1 while ln mode is on: kept for the list / briefing
	interceptReasonP2    = "ln_p2_silent" // P2 while ln mode is on: silent
	interceptReasonDedup = "ln_dedup_12h" // the same related key pushed within the last 12 hours
)

// pushIntercept is one push ln mode held back, as the read-only API returns it.
type pushIntercept struct {
	At         time.Time `json:"at"`
	Level      string    `json:"level"`
	Reason     string    `json:"reason"`
	Title      string    `json:"title,omitempty"`
	Body       string    `json:"body,omitempty"`
	RelatedKey string    `json:"related_key,omitempty"`
	Manager    string    `json:"manager,omitempty"`
	// Also holds the titles of P1 conclusion pushes that merged into this one: several conclusion cards arriving in
	// the same minute become one intercepted entry instead of a stack.
	Also []string `json:"also,omitempty"`
}

// pushSent is one alert push the filter let through to the devices: one entry per logical push, however many devices
// actually received it (the same rule a held push is one intercept by). It backs the read-only daily delivery counts
// and is kept with the switch off as well as on, so the counts describe every push that reached the phone.
type pushSent struct {
	At         time.Time `json:"at"`
	Level      string    `json:"level"`
	RelatedKey string    `json:"related_key,omitempty"`
	Manager    string    `json:"manager"`
}

// pushPolicy says how urgent one alert push is and what it belongs to. The zero value is P1 with no related key.
type pushPolicy struct {
	level      string
	relatedKey string
	title      string
	body       string
	manager    string
}

func (p pushPolicy) levelOr(level string) pushPolicy {
	if p.level == "" {
		p.level = level
	}
	return p
}

// levelFor derives the urgency of a manager notification that named no level itself, from its kind.
func levelFor(kind, given string) string {
	if given != "" {
		return given
	}
	switch kind {
	case assistantwire.NotifyInput, assistantwire.NotifyDecide, assistantwire.NotifyReply:
		// A card or a decision: something only the owner can answer now.
		return assistantwire.LevelP0
	case assistantwire.NotifyDone:
		// A turn that finished: background news.
		return assistantwire.LevelP2
	default:
		// notify_user and the assistant's own alerts: worth knowing, not urgent.
		return assistantwire.LevelP1
	}
}

// lnModeOn says whether the switch is on. It never waits on the database: the switch is read once at startup
// (loadLnState), written through setLnMode, and refreshed in the background when the cached answer is older than
// lnConfigTTL (another hub process may have changed it). A database that does not answer keeps the last known answer,
// off before the first successful read, so an outage never turns filtering on by itself.
func (p *Pusher) lnModeOn() bool {
	if p == nil {
		return false
	}
	p.lnMu.Lock()
	defer p.lnMu.Unlock()
	if p.store != nil && !p.lnRefreshing && (p.lnCached == nil || p.now().Sub(p.lnCachedAt) >= lnConfigTTL) {
		p.lnRefreshing = true
		go p.refreshLnMode(p.lnVersion)
	}
	return p.lnCached != nil && *p.lnCached
}

// refreshLnMode reads the switch from hub_settings into the cache (lnModeOn starts it, one at a time). version is
// lnVersion when it started: a setLnMode meanwhile wins over what this read saw.
func (p *Pusher) refreshLnMode(version uint64) {
	enabled, ok := p.readLnMode()
	p.lnMu.Lock()
	defer p.lnMu.Unlock()
	p.lnRefreshing = false
	if version != p.lnVersion {
		return
	}
	if ok {
		p.lnCached, p.lnCachedAt = &enabled, p.now()
	} else if p.lnCached != nil {
		p.lnCachedAt = p.now() // keep the last answer; try again after another TTL
	}
}

// readLnMode reads the switch; ok false when the database did not answer. No row is off.
func (p *Pusher) readLnMode() (enabled, ok bool) {
	if p.store == nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
	defer cancel()
	found, err := p.store.Setting(ctx, lnModeSettingKey, &enabled)
	if err != nil {
		return false, false
	}
	return found && enabled, true
}

// setLnMode writes the switch (hub_settings) and refreshes the cache immediately.
func (p *Pusher) setLnMode(ctx context.Context, enabled bool) error {
	if p == nil || p.store == nil {
		return errors.New("push settings are not available")
	}
	ctx, cancel := context.WithTimeout(ctx, p.storeTimeout)
	defer cancel()
	if err := p.store.PutSetting(ctx, lnModeSettingKey, enabled); err != nil {
		return err
	}
	p.lnMu.Lock()
	p.lnCached, p.lnCachedAt = &enabled, p.now()
	p.lnVersion++
	p.lnMu.Unlock()
	return nil
}

// applyLn decides whether one alert push may go out. With the switch off it allows everything and touches nothing.
// It runs once per push call, never per device: a held push is one intercepted entry however many devices would have
// received it. The 12-hour check and the note of a P0 that goes out happen under one hold of lnMu, so two pushes with
// the same key at the same moment cannot both pass.
func (p *Pusher) applyLn(policy pushPolicy) (allow bool, reason string) {
	level := policy.level
	if level == "" {
		level = assistantwire.LevelP1
	}
	now := p.now()
	if !p.lnModeOn() {
		// The switch is off: every push goes out as before, and the delivery counts still remember it.
		p.recordSent(policy, level, now)
		return true, ""
	}
	key := strings.TrimSpace(policy.relatedKey)
	in := pushIntercept{At: now.UTC(), Level: level, Title: policy.title, Body: policy.body, RelatedKey: key, Manager: policy.manager}
	p.lnMu.Lock()
	if key != "" {
		if last, seen := p.lnLastPush[key]; seen && now.Sub(last) < lnDedupWindow {
			in.Reason = interceptReasonDedup
			p.addInterceptLocked(in, false)
			p.lnMu.Unlock()
			return false, interceptReasonDedup
		}
	}
	switch level {
	case assistantwire.LevelP0:
		// P0 interrupts; its key then stays quiet for the window.
		if key != "" {
			p.noteKeyPushedLocked(key, now)
		}
		p.addSentLocked(pushSent{At: now.UTC(), Level: level, RelatedKey: key, Manager: policy.manager})
		p.lnMu.Unlock()
		return true, ""
	case assistantwire.LevelP2:
		in.Reason = interceptReasonP2
		p.addInterceptLocked(in, false)
		p.lnMu.Unlock()
		return false, interceptReasonP2
	default: // P1 and anything unknown: held for the list and the briefing rather than alerted.
		in.Level, in.Reason = assistantwire.LevelP1, interceptReasonP1
		p.addInterceptLocked(in, true)
		p.lnMu.Unlock()
		return false, interceptReasonP1
	}
}

// noteKeyPushedLocked remembers that key alerted at now, and forgets keys whose window is over (card keys are unique,
// so the map would otherwise only grow). lnMu is held.
func (p *Pusher) noteKeyPushedLocked(key string, now time.Time) {
	if p.lnLastPush == nil {
		p.lnLastPush = map[string]time.Time{}
	}
	for k, at := range p.lnLastPush {
		if now.Sub(at) >= lnDedupWindow {
			delete(p.lnLastPush, k)
		}
	}
	p.lnLastPush[key] = now
}

// addInterceptLocked keeps one held push and schedules its save. merge (P1): it folds into a recent P1 entry of the
// same manager opened in the same minute, or about the same related key within the 12-hour window, so several
// conclusion cards arriving together, or three cards about one PR, become one entry. Entries past the age and count
// limits go. lnMu is held.
func (p *Pusher) addInterceptLocked(in pushIntercept, merge bool) {
	if merge {
		minute := in.At.Truncate(time.Minute)
		for i := len(p.intercepts) - 1; i >= 0 && i >= len(p.intercepts)-64; i-- {
			e := &p.intercepts[i]
			if e.Reason != interceptReasonP1 || e.Manager != in.Manager {
				continue
			}
			sameMinute := e.At.Truncate(time.Minute).Equal(minute)
			sameKey := in.RelatedKey != "" && e.RelatedKey == in.RelatedKey && in.At.Sub(e.At) < lnDedupWindow
			if sameMinute || sameKey {
				if in.Title != "" {
					e.Also = append(slices.Clip(e.Also), in.Title)
				}
				p.scheduleSaveLocked()
				return
			}
		}
	}
	cutoff := p.now().Add(-lnInterceptAge)
	kept := p.intercepts[:0]
	for _, e := range p.intercepts {
		if e.At.After(cutoff) {
			kept = append(kept, e)
		}
	}
	p.intercepts = append(kept, in)
	if len(p.intercepts) > lnInterceptMax {
		p.intercepts = slices.Clone(p.intercepts[len(p.intercepts)-lnInterceptMax:])
	}
	p.scheduleSaveLocked()
}

// recordIntercept keeps one held push (tests and callers outside applyLn).
func (p *Pusher) recordIntercept(in pushIntercept) {
	p.lnMu.Lock()
	p.addInterceptLocked(in, false)
	p.lnMu.Unlock()
}

// addSentLocked remembers one delivered push for the daily counts, dropping entries past the age and count limits.
// lnMu is held.
func (p *Pusher) addSentLocked(in pushSent) {
	cutoff := p.now().Add(-lnInterceptAge)
	kept := p.sent[:0]
	for _, e := range p.sent {
		if e.At.After(cutoff) {
			kept = append(kept, e)
		}
	}
	p.sent = append(kept, in)
	if len(p.sent) > lnSentMax {
		p.sent = slices.Clone(p.sent[len(p.sent)-lnSentMax:])
	}
	if p.store == nil { // a pusher without a settings store keeps the counts in memory only
		return
	}
	p.sentDirty = true
	if !p.lnWriting {
		p.lnWriting = true
		go p.saveIntercepts()
	}
}

// recordSent remembers one delivered push for the daily counts. It is the entry for paths where applyLn does not run
// (the switch is off in sendFromPolicy); applyLn's own paths call addSentLocked under its lock.
func (p *Pusher) recordSent(policy pushPolicy, level string, now time.Time) {
	if p == nil {
		return
	}
	p.lnMu.Lock()
	defer p.lnMu.Unlock()
	p.addSentLocked(pushSent{At: now.UTC(), Level: level, RelatedKey: strings.TrimSpace(policy.relatedKey), Manager: policy.manager})
}

// scheduleSaveLocked marks the list changed and starts the one writer if it is not running. The writer saves the
// latest list, then again while it changed meanwhile, so saves never land out of order and a burst of held pushes is
// one or two writes, off the push path. lnMu is held.
func (p *Pusher) scheduleSaveLocked() {
	if p.store == nil {
		return
	}
	p.lnDirty = true
	if !p.lnWriting {
		p.lnWriting = true
		go p.saveIntercepts()
	}
}

func (p *Pusher) saveIntercepts() {
	for {
		p.lnMu.Lock()
		if !p.lnDirty && !p.sentDirty {
			p.lnWriting = false
			p.lnMu.Unlock()
			return
		}
		saveIntercepts := p.lnDirty
		p.lnDirty = false
		saveSent := p.sentDirty
		p.sentDirty = false
		list := cloneIntercepts(p.intercepts)
		sent := slices.Clone(p.sent)
		p.lnMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
		if saveIntercepts {
			if err := p.store.PutSetting(ctx, lnInterceptsSettingKey, list); err != nil {
				p.logf("ln mode: save held pushes: %v", err)
			}
		}
		if saveSent {
			if err := p.store.PutSetting(ctx, lnSentSettingKey, sent); err != nil {
				p.logf("ln mode: save delivered pushes: %v", err)
			}
		}
		cancel()
	}
}

// waitInterceptsSaved waits (up to d) for the writer to finish; true when nothing is left to save.
func (p *Pusher) waitInterceptsSaved(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		p.lnMu.Lock()
		idle := !p.lnWriting && !p.lnDirty && !p.sentDirty
		p.lnMu.Unlock()
		if idle {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func cloneIntercepts(in []pushIntercept) []pushIntercept {
	out := make([]pushIntercept, len(in))
	for i, e := range in {
		e.Also = slices.Clone(e.Also)
		out[i] = e
	}
	return out
}

// loadLnState reads the switch and the held-push list once at startup; an absent or unreadable row is off / empty.
func (p *Pusher) loadLnState() {
	if p == nil || p.store == nil {
		return
	}
	if enabled, ok := p.readLnMode(); ok {
		p.lnMu.Lock()
		p.lnCached, p.lnCachedAt = &enabled, p.now()
		p.lnMu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
	defer cancel()
	var stored []pushIntercept
	if ok, err := p.store.Setting(ctx, lnInterceptsSettingKey, &stored); err == nil && ok {
		cutoff := p.now().Add(-lnInterceptAge)
		p.lnMu.Lock()
		p.intercepts = p.intercepts[:0]
		for _, e := range stored {
			if e.At.After(cutoff) {
				p.intercepts = append(p.intercepts, e)
			}
		}
		p.lnMu.Unlock()
	}

	var delivered []pushSent
	if ok, err := p.store.Setting(ctx, lnSentSettingKey, &delivered); err == nil && ok {
		cutoff := p.now().Add(-lnInterceptAge)
		p.lnMu.Lock()
		p.sent = p.sent[:0]
		for _, e := range delivered {
			if e.At.After(cutoff) {
				p.sent = append(p.sent, e)
			}
		}
		p.lnMu.Unlock()
	}
}

// lnInterceptsView returns the held pushes newest first, at most limit (clamped to lnInterceptLimitMax). When manager
// is not empty, only that manager's entries count toward the limit: the filter runs before the truncation, so another
// manager's burst can never push an extension's own entries out of its 200.
func (p *Pusher) lnInterceptsView(manager string, limit int) []pushIntercept {
	if limit <= 0 || limit > lnInterceptLimitMax {
		limit = lnInterceptLimitMax
	}
	p.lnMu.Lock()
	defer p.lnMu.Unlock()
	out := make([]pushIntercept, 0, min(limit, len(p.intercepts)))
	for i := len(p.intercepts) - 1; i >= 0 && len(out) < limit; i-- {
		e := p.intercepts[i]
		if manager != "" && e.Manager != manager {
			continue
		}
		e.Also = slices.Clone(e.Also)
		out = append(out, e)
	}
	return out
}

// lnDayStats is one UTC calendar day's delivery counts for one manager, counted from the full in-memory records
// (up to 500 held / 2000 delivered entries) rather than the 200-entry view, so the numbers stay exact past 200.
type lnDayStats struct {
	Date        string         `json:"date"`
	Manager     string         `json:"manager"`
	Sent        int            `json:"sent"`
	Intercepted int            `json:"intercepted"`
	ByReason    map[string]int `json:"by_reason,omitempty"`
}

// lnDailyStats counts delivered and held pushes at or after since, per UTC day and manager. When manager is not empty
// only that manager's pushes are counted (an extension's view). Days with no pushes are omitted; days sort ascending.
func (p *Pusher) lnDailyStats(since time.Time, manager string) []lnDayStats {
	type agg struct {
		sent, intercepted int
		byReason          map[string]int
	}
	p.lnMu.Lock()
	days := map[string]map[string]*agg{}
	add := func(at time.Time, mgr string, reason string, delivered bool) {
		if at.Before(since) {
			return
		}
		if manager != "" && mgr != manager {
			return
		}
		day := at.UTC().Format("2006-01-02")
		if days[day] == nil {
			days[day] = map[string]*agg{}
		}
		a := days[day][mgr]
		if a == nil {
			a = &agg{byReason: map[string]int{}}
			days[day][mgr] = a
		}
		if delivered {
			a.sent++
			return
		}
		a.intercepted++
		a.byReason[reason]++
	}
	for _, e := range p.sent {
		add(e.At, e.Manager, "", true)
	}
	for _, e := range p.intercepts {
		add(e.At, e.Manager, e.Reason, false)
	}
	p.lnMu.Unlock()

	dates := make([]string, 0, len(days))
	for d := range days {
		dates = append(dates, d)
	}
	slices.Sort(dates)
	out := make([]lnDayStats, 0, len(dates))
	for _, d := range dates {
		managers := make([]string, 0, len(days[d]))
		for m := range days[d] {
			managers = append(managers, m)
		}
		slices.Sort(managers)
		for _, m := range managers {
			a := days[d][m]
			out = append(out, lnDayStats{Date: d, Manager: m, Sent: a.sent, Intercepted: a.intercepted, ByReason: a.byReason})
		}
	}
	return out
}

// lnRoutes serves the switch, the read-only list of held pushes and the read-only daily delivery counts. The routes
// sit behind the standard API gate; an extension token with the notify scope may read the switch, its own manager's
// held pushes and counts, but not change the switch.
func (h *Hub) lnRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/push/ln-mode", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"enabled": h.push.lnModeOn()})
	})
	api.HandleFunc("PUT /api/v1/push/ln-mode", func(w http.ResponseWriter, r *http.Request) {
		if extensionToken(r) {
			writeErr(w, http.StatusForbidden, errors.New("the ln mode switch is for the hub's own apps"))
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeJSONBody(w, r, &body, 1<<10); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if err := h.push.setLnMode(r.Context(), body.Enabled); err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		writeJSON(w, map[string]any{"enabled": body.Enabled})
	})
	api.HandleFunc("GET /api/v1/push/intercepts", func(w http.ResponseWriter, r *http.Request) {
		// The manager filter runs before the 200-entry limit: an extension token reads only its own manager's held
		// pushes; a full credential reads every manager's.
		view := h.push.lnInterceptsView(extensionManager(r), 0)
		writeJSON(w, map[string]any{"intercepts": view})
	})
	api.HandleFunc("GET /api/v1/push/stats/daily", func(w http.ResponseWriter, r *http.Request) {
		now := h.push.now()
		since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		q := r.URL.Query()
		if raw := q.Get("since"); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeErr(w, http.StatusBadRequest, errors.New("since must be an RFC3339 timestamp"))
				return
			}
			since = parsed
		} else if raw := q.Get("days"); raw != "" {
			days, err := strconv.Atoi(raw)
			if err != nil || days < 1 || days > lnStatsDaysMax {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("days must be a number from 1 to %d", lnStatsDaysMax))
				return
			}
			since = since.AddDate(0, 0, -(days - 1))
		}
		if oldest := now.Add(-lnInterceptAge); since.Before(oldest) {
			since = oldest
		}
		// An extension token counts only its own manager's pushes; a full credential counts every manager's.
		writeJSON(w, map[string]any{
			"since": since.UTC().Format(time.RFC3339),
			"days":  h.push.lnDailyStats(since, extensionManager(r)),
		})
	})
}
