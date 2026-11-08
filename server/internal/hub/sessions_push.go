package hub

import (
	"bytes"
	"encoding/json"
	"github.com/nwverse/persona/server/internal/compat"
	"net/http"
	"slices"
	"strings"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/proto"
)

// The session lists the apps get (docs/protocol/protocol.md "Session deltas", docs/server/server.md "What gets pushed to the app"). An app that asks for
// sessions_delta (?features= on the WebSocket) gets each machine's list once when it connects, then only what
// changed: the sessions that are new or different, each whole, and the ids that went. An older app gets the whole
// list each time, as before, though never the same list twice. A machine's list is ~1 KB a session and up to 150
// sessions, and one session changes at a time: the phone downloaded and decoded ~2.6 MB a minute of lists.
const featureSessionsDelta = "sessions_delta"

// wantsFeature: the app listed feature in the WebSocket URL's ?features= (comma-separated).
func wantsFeature(r *http.Request, feature string) bool {
	return urlFeatures(r)[feature]
}

// urlFeatures is everything the app listed in the WebSocket URL's ?features=.
func urlFeatures(r *http.Request) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.Split(r.URL.Query().Get("features"), ",") {
		if f != "" {
			set[f] = true
		}
	}
	return set
}

// sentSession is one session of a list, as sent.
type sentSession struct {
	id string
	// history: a Codex session known only from its rollout (codex_history). Not pushed to a delta app unless it has the
	// session already (a live one that ended): the app fetches the history when it wants it (GET /api/v1/sessions).
	history bool
	raw     json.RawMessage
}

// encodeSessions encodes each session of list (the same bytes json.Marshal gives) into one buffer, each raw a slice
// of it: one allocation for the list rather than a copy of every session and then a clone of its bytes, what a push
// spent most of its memory on. The raws share the buffer and are only read from then on.
func encodeSessions(list []proto.Session) []sentSession { return encodeRows(list, false, "") }

// encodeStamped is encodeSessions of list as stamped with manager (stamped), without the copy of the list: a session
// that names another manager is copied alone, into one scratch session, to be encoded.
func encodeStamped(list []proto.Session, manager string) []sentSession {
	return encodeRows(list, true, manager)
}

func encodeRows(list []proto.Session, stamp bool, manager string) []sentSession {
	out := make([]sentSession, 0, len(list))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // escapes HTML as json.Marshal does
	type span struct{ start, end int }
	spans := make([]span, 0, len(list))
	var scratch *proto.Session
	for i := range list {
		start := buf.Len()
		s := &list[i]
		if stamp && s.Manager != manager {
			if scratch == nil {
				scratch = new(proto.Session)
			}
			*scratch = *s
			scratch.Manager, s = manager, scratch
		}
		if err := enc.Encode(s); err != nil {
			buf.Truncate(start)
			spans = append(spans, span{-1, -1})
			continue
		}
		spans = append(spans, span{start, buf.Len() - 1}) // without the '\n' Encode ends each value with
		if start == 0 && i < len(list)-1 {
			// Sized from the first session, room for the rest to differ a little, so the buffer is rarely regrown.
			buf.Grow(buf.Len() * (len(list) - i - 1) * 5 / 4)
		}
	}
	b := buf.Bytes()
	for i, sp := range spans {
		if sp.start < 0 {
			continue
		}
		s := &list[i]
		out = append(out, sentSession{id: s.SessionID, history: s.SourceKind == "codex_history", raw: b[sp.start:sp.end:sp.end]})
	}
	return out
}

// sessionsMessage is the whole-list message (the same bytes as marshalling the sessions themselves). Put together
// from the encoded sessions rather than marshalled: json.Marshal checks and copies every json.RawMessage again,
// half of what a list push cost, for sessions it has just encoded.
func sessionsMessage(machine string, list []sentSession) []byte {
	id, _ := json.Marshal(machine)
	size := len(`{"machine_id":,"sessions":[],"type":"sessions"}`) + len(id) + len(list)
	for _, s := range list {
		size += len(s.raw)
	}
	b := make([]byte, 0, size)
	b = append(b, `{"machine_id":`...)
	b = append(b, id...)
	b = append(b, `,"sessions":[`...)
	for i, s := range list {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, s.raw...)
	}
	return append(b, `],"type":"sessions"}`...)
}

// forDelta is what a delta app gets of list, given what it has.
func forDelta(list []sentSession, have map[string][]byte) []sentSession {
	out := make([]sentSession, 0, len(list))
	for _, s := range list {
		if _, had := have[s.id]; s.history && !had {
			continue
		}
		out = append(out, s)
	}
	return out
}

// sessionsDelta is the message taking an app from have to list (nil: nothing changed), and what it then has. Run for
// every delta app on every push, and most pushes change nothing for a given app's machine list, so that case is
// checked first without allocating and gives back have itself. A list naming a session twice, which no node sends,
// may then hide that one of have's sessions went; the next change removes it.
func sessionsDelta(machine string, list []sentSession, have map[string][]byte) ([]byte, map[string][]byte) {
	if unchanged(list, have) {
		return nil, have
	}
	next := make(map[string][]byte, len(list))
	var upsert []json.RawMessage
	for _, s := range list {
		old, had := have[s.id]
		if s.history && !had {
			continue
		}
		next[s.id] = s.raw
		if !had || !bytes.Equal(old, s.raw) {
			upsert = append(upsert, s.raw)
		}
	}
	var remove []string
	for id := range have {
		if _, ok := next[id]; !ok {
			remove = append(remove, id)
		}
	}
	if len(upsert) == 0 && len(remove) == 0 {
		return nil, next
	}
	slices.Sort(remove)
	return deltaMessage(machine, upsert, remove), next
}

// unchanged: an app with have needs nothing for list (what forDelta shows of it is have, byte for byte).
func unchanged(list []sentSession, have map[string][]byte) bool {
	n := 0
	for _, s := range list {
		old, had := have[s.id]
		if s.history && !had {
			continue
		}
		if !had || !bytes.Equal(old, s.raw) {
			return false
		}
		n++
	}
	return n == len(have)
}

// deltaMessage is the sessions_delta message, the same bytes as marshalling a map of it (its keys sorted, empty
// arrays as []) but put together from the encoded sessions, as sessionsMessage is.
func deltaMessage(machine string, upsert []json.RawMessage, remove []string) []byte {
	id, _ := json.Marshal(machine)
	size := len(`{"machine_id":,"remove":[],"type":"sessions_delta","upsert":[]}`) + len(id) + len(upsert)
	for _, raw := range upsert {
		size += len(raw)
	}
	for _, r := range remove {
		size += len(r) + 3 // quotes and comma; escapes grow the slice
	}
	b := make([]byte, 0, size)
	b = append(b, `{"machine_id":`...)
	b = append(b, id...)
	b = append(b, `,"remove":[`...)
	for i, r := range remove {
		if i > 0 {
			b = append(b, ',')
		}
		q, _ := json.Marshal(r)
		b = append(b, q...)
	}
	b = append(b, `],"type":"sessions_delta","upsert":[`...)
	for i, raw := range upsert {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, raw...)
	}
	return append(b, `]}`...)
}

// sameSessions: a and b are the same list, session for session in the same order (what the apps read of it).
func sameSessions(a, b []sentSession) bool {
	return slices.EqualFunc(a, b, func(x, y sentSession) bool { return x.id == y.id && bytes.Equal(x.raw, y.raw) })
}

// broadcastSessions sends every app a machine's session list: an older app the whole list unless it is the list
// they last got (an interaction shown, a session seen, a node pass that changed nothing the app reads), a delta app
// what changed for it. The whole-list message (~150 KB) is put together only when the list changed and an older app
// is connected, once for all of them. The lock is held until everything is queued, so what was recorded is what the
// apps got last. list is the machine's as kept (keptSessions) or as decorated: the apps get it stamped with its
// manager either way, and it is never changed.
func (h *Hub) broadcastSessions(m *Machine, list []proto.Session) {
	owner := h.machineOwner(m.cfg.ID)
	shown := h.shown(m.cfg.ID, list)
	enc := encodeStamped(shown, owner)
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	before := h.listCache.index.Revision()
	if h.listCache.machines != nil { // a paginated reader: its index reads the sessions themselves
		h.indexSessionsLocked(m.cfg.ID, stamped(shown, owner), enc)
	}
	revision := h.listCache.index.Revision()
	prev, sent := h.sessionsSent[m.cfg.ID]
	changed := !sent || !sameSessions(prev, enc)
	if changed {
		if h.sessionsSent == nil {
			h.sessionsSent = map[string][]sentSession{}
		}
		h.sessionsSent[m.cfg.ID] = enc
		h.live.changed() // what the Fleet activity is made of
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var full []byte
	for c := range h.clients {
		var b []byte
		if c.listMode {
			if before != revision {
				c.sendJSON(map[string]any{"type": "sessions_list", "revision": revision})
			}
			continue
		}
		if c.delta {
			b, c.have[m.cfg.ID] = sessionsDelta(m.cfg.ID, enc, c.have[m.cfg.ID])
		} else if changed {
			if full == nil {
				full = sessionsMessage(m.cfg.ID, enc)
			}
			b = full
		}
		if b != nil {
			c.queue(b)
		}
	}
}

// snapshotSessions sends a delta app that just connected a machine's whole list (without the Codex history), what
// later deltas build on. Under sessionsMu, so no delta for the machine can come between the list and the record.
func (h *Hub) snapshotSessions(c *client, m *Machine) {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	shown := forDelta(encodeStamped(h.shown(m.cfg.ID, m.keptSessions()), h.machineOwner(m.cfg.ID)), nil)
	have := make(map[string][]byte, len(shown))
	for _, s := range shown {
		have[s.id] = s.raw
	}
	c.have[m.cfg.ID] = have
	c.queue(sessionsMessage(m.cfg.ID, shown))
}

// queue puts b on the client's way out; a client 512 messages behind is dropped (it would miss this one for good
// now that nothing is sent twice) and resyncs when it reconnects.
func (c *client) queue(b []byte) {
	b = compat.ProjectJSON(b, c.canonicalContracts.Load() || c.urlFeatures[compat.Feature])
	select {
	case c.out <- b:
	default:
		c.dropSlow()
	}
}

// dropSlow closes a client too slow to take what it is sent, in the background and once: the close waits on its stuck
// connection (up to about 10 s), and until it ends the client is still sent to, each send finding the queue full.
func (c *client) dropSlow() {
	if c.dropped.CompareAndSwap(false, true) && c.conn != nil {
		go c.conn.Close(websocket.StatusPolicyViolation, "too slow")
	}
}
