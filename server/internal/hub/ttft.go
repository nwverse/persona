package hub

import (
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const (
	hubTTFTMaxSessions        = 256
	hubTTFTMaxItemsPerSession = 64
	hubTTFTTimestampSlack     = 5 * time.Second
)

// hubTTFTTracker is a bounded, zero-background-polling streaming TTFT observer on the Hub.
// It acts as a fallback when a node or runtime does not attach ttft_ms natively, and preserves
// any node-provided ttft_ms untouched. Historical replays without an observed live turn stay nil.
type hubTTFTTracker struct {
	mu       sync.Mutex
	sessions map[[2]string]*hubTTFTSession
}

type hubTTFTSession struct {
	armed        bool
	startedAt    time.Time
	pendingReady bool
	pendingStart time.Time
	pendingTTFT  int64
	touchedAt    time.Time
	bound        map[string]int64
	boundOrder   []string
}

func (t *hubTTFTTracker) entryLocked(key [2]string, now time.Time) *hubTTFTSession {
	if t.sessions == nil {
		t.sessions = map[[2]string]*hubTTFTSession{}
	}
	st := t.sessions[key]
	if st == nil {
		if len(t.sessions) >= hubTTFTMaxSessions {
			var oldestKey [2]string
			var oldestTime time.Time
			hasOldest := false
			for k, v := range t.sessions {
				if !hasOldest || v.touchedAt.Before(oldestTime) {
					oldestKey, oldestTime, hasOldest = k, v.touchedAt, true
				}
			}
			if hasOldest {
				delete(t.sessions, oldestKey)
			}
		}
		st = &hubTTFTSession{}
		t.sessions[key] = st
	}
	st.touchedAt = now
	return st
}

func (t *hubTTFTTracker) arm(machineID, sessionID string, startedAt time.Time) {
	if t == nil || machineID == "" || sessionID == "" || startedAt.IsZero() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.entryLocked([2]string{machineID, sessionID}, startedAt)
	st.armed = true
	st.startedAt = startedAt
	st.pendingReady = false
}

func (t *hubTTFTTracker) firstToken(machineID, sessionID string, now time.Time) (int64, bool) {
	if t == nil || machineID == "" || sessionID == "" || now.IsZero() {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sessions[[2]string{machineID, sessionID}]
	if st == nil || !st.armed {
		return 0, false
	}
	st.armed = false
	ms := now.Sub(st.startedAt).Milliseconds()
	if ms < 0 {
		return 0, false
	}
	st.pendingReady = true
	st.pendingStart = st.startedAt
	st.pendingTTFT = ms
	st.touchedAt = now
	return ms, true
}

func (st *hubTTFTSession) bindLocked(itemID string, ms int64) {
	if itemID == "" || ms < 0 {
		return
	}
	if st.bound == nil {
		st.bound = map[string]int64{}
	}
	if _, exists := st.bound[itemID]; exists {
		return
	}
	if len(st.boundOrder) >= hubTTFTMaxItemsPerSession {
		evict := st.boundOrder[0]
		st.boundOrder = st.boundOrder[1:]
		delete(st.bound, evict)
	}
	st.bound[itemID] = ms
	st.boundOrder = append(st.boundOrder, itemID)
}

func isHubTTFTAssistantItem(it proto.Item) bool {
	if it.Role != "assistant" {
		return false
	}
	switch it.Kind {
	case proto.KindText, proto.KindThinking, proto.KindToolUse, proto.KindFold:
		return true
	}
	return false
}

// annotate preserves node-provided Item.TTFTMS values and attaches Hub-observed streaming TTFT
// when absent. When allowBind is false (HTTP history reads), only previously bound item IDs are
// re-annotated so historical replays without live observation never receive fabricated timings.
func (t *hubTTFTTracker) annotate(machineID, sessionID string, items []proto.Item, allowBind bool) bool {
	if t == nil || machineID == "" || sessionID == "" || len(items) == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sessions[[2]string{machineID, sessionID}]
	if st == nil || (!st.pendingReady && len(st.bound) == 0) {
		return false
	}
	changed := false
	for i := range items {
		it := &items[i]
		if it.TTFTMS != nil {
			if *it.TTFTMS >= 0 {
				st.bindLocked(it.ID, *it.TTFTMS)
				if allowBind && st.pendingReady && isHubTTFTAssistantItem(*it) &&
					(it.TS.IsZero() || st.pendingStart.IsZero() || !it.TS.Before(st.pendingStart.Add(-hubTTFTTimestampSlack))) {
					st.pendingReady = false
				}
			}
			continue
		}
		if ms, ok := st.bound[it.ID]; ok {
			it.TTFTMS = proto.Int64(ms)
			changed = true
			continue
		}
		if allowBind && st.pendingReady && isHubTTFTAssistantItem(*it) {
			if !it.TS.IsZero() && !st.pendingStart.IsZero() && it.TS.Before(st.pendingStart.Add(-hubTTFTTimestampSlack)) {
				continue
			}
			it.TTFTMS = proto.Int64(st.pendingTTFT)
			st.bindLocked(it.ID, st.pendingTTFT)
			st.pendingReady = false
			changed = true
		}
	}
	return changed
}
