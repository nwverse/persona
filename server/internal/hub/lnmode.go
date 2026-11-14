package hub

import (
	"context"
	"errors"
	"net/http"
	"slices"
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

	// lnDedupWindow: how long the same related key stays quiet after it pushed once.
	lnDedupWindow = 12 * time.Hour
	// lnConfigTTL bounds how often one Pusher asks the database for the switch while pushes keep coming.
	lnConfigTTL = 15 * time.Second
	// lnInterceptMax bounds how many held pushes are kept; the oldest go first.
	lnInterceptMax = 500
	// lnInterceptAge: held pushes older than this are not worth showing any more.
	lnInterceptAge      = 14 * 24 * time.Hour
	lnInterceptLimitMax = 200
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

// lnModeOn says whether the switch is on, reading hub.settings through a short cache. A database that does not
// answer keeps the last known answer (off before the first successful read), so an outage never turns filtering on
// by itself.
func (p *Pusher) lnModeOn() bool {
	if p == nil {
		return false
	}
	p.lnMu.Lock()
	got, at := p.lnCached, p.lnCachedAt
	p.lnMu.Unlock()
	if got != nil && p.now().Sub(at) < lnConfigTTL {
		return *got
	}
	enabled := false
	if p.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
		var stored bool
		if ok, err := p.store.Setting(ctx, lnModeSettingKey, &stored); err == nil && ok {
			enabled = stored
		}
		cancel()
	}
	p.lnMu.Lock()
	p.lnCached, p.lnCachedAt = &enabled, p.now()
	p.lnMu.Unlock()
	return enabled
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
	p.lnMu.Unlock()
	return nil
}

// applyLn decides whether one alert push may go out while ln mode is on. It runs once per push call, never per
// device: a held push is one intercepted entry however many devices would have received it. The dedup bookkeeping of
// a push it allows is updated here, before device selection.
func (p *Pusher) applyLn(policy pushPolicy) (allow bool, reason string) {
	if !p.lnModeOn() {
		return true, ""
	}
	level := policy.level
	if level == "" {
		level = assistantwire.LevelP1
	}
	now := p.now()
	if key := strings.TrimSpace(policy.relatedKey); key != "" {
		p.lnMu.Lock()
		last, seen := p.lnLastPush[key]
		p.lnMu.Unlock()
		if seen && now.Sub(last) < lnDedupWindow {
			p.recordIntercept(pushIntercept{At: now.UTC(), Level: level, Reason: interceptReasonDedup,
				Title: policy.title, Body: policy.body, RelatedKey: key, Manager: policy.manager})
			return false, interceptReasonDedup
		}
	}
	switch level {
	case assistantwire.LevelP0:
		// P0 interrupts; remember the key so the same subject cannot interrupt again within the window.
		if key := strings.TrimSpace(policy.relatedKey); key != "" {
			p.lnMu.Lock()
			if p.lnLastPush == nil {
				p.lnLastPush = map[string]time.Time{}
			}
			p.lnLastPush[key] = now
			p.lnMu.Unlock()
		}
		return true, ""
	case assistantwire.LevelP2:
		p.recordIntercept(pushIntercept{At: now.UTC(), Level: level, Reason: interceptReasonP2,
			Title: policy.title, Body: policy.body, RelatedKey: policy.relatedKey, Manager: policy.manager})
		return false, interceptReasonP2
	default: // P1 and anything unknown: held rather than alerted.
		p.recordInterceptMerged(pushIntercept{At: now.UTC(), Level: assistantwire.LevelP1, Reason: interceptReasonP1,
			Title: policy.title, Body: policy.body, RelatedKey: policy.relatedKey, Manager: policy.manager})
		return false, interceptReasonP1
	}
}

// recordInterceptMerged folds a P1 conclusion push into the entry opened within the same minute when one exists, so
// several cards arriving together become one entry; otherwise it opens one.
func (p *Pusher) recordInterceptMerged(in pushIntercept) {
	p.lnMu.Lock()
	minute := in.At.Truncate(time.Minute)
	for i := len(p.intercepts) - 1; i >= 0 && i >= len(p.intercepts)-16; i-- {
		e := p.intercepts[i]
		if e.Reason == interceptReasonP1 && e.At.Truncate(time.Minute).Equal(minute) {
			if in.Title != "" {
				p.intercepts[i].Also = append(p.intercepts[i].Also, in.Title)
			}
			persist := append([]pushIntercept(nil), p.intercepts...)
			p.lnMu.Unlock()
			p.persistIntercepts(persist)
			return
		}
	}
	p.lnMu.Unlock()
	p.recordIntercept(in)
}

// recordIntercept keeps one held push, drops entries past the age and count limits, and best-effort persists the list.
func (p *Pusher) recordIntercept(in pushIntercept) {
	p.lnMu.Lock()
	cutoff := p.now().Add(-lnInterceptAge)
	kept := p.intercepts[:0]
	for _, e := range p.intercepts {
		if e.At.After(cutoff) {
			kept = append(kept, e)
		}
	}
	p.intercepts = append(kept, in)
	if len(p.intercepts) > lnInterceptMax {
		p.intercepts = p.intercepts[len(p.intercepts)-lnInterceptMax:]
	}
	persist := append([]pushIntercept(nil), p.intercepts...)
	p.lnMu.Unlock()
	p.persistIntercepts(persist)
}

// persistIntercepts writes the list to hub_settings; failures only log, the in-memory list stays authoritative until
// the next write.
func (p *Pusher) persistIntercepts(list []pushIntercept) {
	if p == nil || p.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
	defer cancel()
	if err := p.store.PutSetting(ctx, lnInterceptsSettingKey, list); err != nil {
		p.logf("ln mode: persist intercepts: %v", err)
	}
}

// loadIntercepts reads the persisted list once at startup; an absent or unreadable row means an empty list.
func (p *Pusher) loadIntercepts() {
	if p == nil || p.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
	defer cancel()
	var stored []pushIntercept
	if ok, err := p.store.Setting(ctx, lnInterceptsSettingKey, &stored); err != nil || !ok {
		return
	}
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

// lnInterceptsView returns the held pushes newest first, at most limit (clamped to lnInterceptLimitMax).
func (p *Pusher) lnInterceptsView(limit int) []pushIntercept {
	if limit <= 0 || limit > lnInterceptLimitMax {
		limit = lnInterceptLimitMax
	}
	p.lnMu.Lock()
	defer p.lnMu.Unlock()
	out := make([]pushIntercept, 0, min(limit, len(p.intercepts)))
	for i := len(p.intercepts) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, p.intercepts[i])
	}
	return out
}

// lnRoutes serves the switch and the read-only list of held pushes. The routes sit behind the standard API gate; an
// extension token may read the intercepts (the same content its pushes carry) but not change the switch.
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
		view := h.push.lnInterceptsView(0)
		// An extension token reads only its own manager's held pushes; a full credential reads every manager's.
		if em := extensionManager(r); em != "" {
			view = slices.DeleteFunc(slices.Clone(view), func(e pushIntercept) bool { return e.Manager != em })
		}
		writeJSON(w, map[string]any{"intercepts": view})
	})
}
