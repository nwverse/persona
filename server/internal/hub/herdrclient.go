package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/rpc"
)

// The web's full herdr view (docs/integrations/terminal.md "Full herdr UI"). A browser asks for one with herdr_open; the node
// runs herdr's own client in a PTY and its bytes come back here (herdr.client.data) and go on to the browser as
// herdr_data. This is not the terminal relay (terminal.go): that one keeps frames to replay to apps that come back and
// shares one stream between its subscribers, while a full client is one process per browser view, a raw byte stream
// with nothing to replay (a browser that reconnects opens a new view), and its flow control is the browser's acks.
//
// Messages from the browser, all with the view's id (its own choice, per connection):
//
//	herdr_open   {machine_id, session_id?, cols, rows, focus?}   session_id: the session or pane address to show first
//	herdr_input  {data | b64}     keys and pastes
//	herdr_resize {cols, rows}
//	herdr_ack    {bytes}          everything the browser has parsed so far, in bytes (cumulative)
//	herdr_close
//	herdr_focus  {session_id}     the view shows this session (or "pane:" address): herdr's focus moves to its pane
//
// To the browser: herdr_state {state: "open"}, herdr_data {data: base64}, herdr_end {error?, code?, unsupported?},
// herdr_focus {machine_id, workspace_id, tab_id, pane_id, session_id?, source, mine}: herdr's focus moved. source:
// "client" (a full view was just typed into), "persona" (a herdr_focus asked for it), "other" (the computer's own
// window, anything else); mine: the view this goes to is the one that did it. A view follows only a change that is
// its own (docs/integrations/terminal.md "Focus synchronization").
//
// Only a full device may open one: the client can do anything on that computer's herdr (run in any pane, close them).
// An answer-only device is refused, and the extension tokens cannot reach the WebSocket at all.
type fullRelays struct {
	mu     sync.Mutex
	byNode map[string]*fullRelay // the id the node knows it by -> the view
}

type fullRelay struct {
	c         *client
	machineID string
	id        string // the browser's id
	nodeID    string
	sent      int64 // bytes given to the browser
	acked     int64 // bytes the browser said it parsed
}

const (
	// fullPerClient: views one browser connection may have open.
	fullPerClient = 4
	// fullHubWindow: bytes in flight to one browser (given, not acked) past which its view ends. The node stops at a
	// quarter of this, so only a node or browser that ignores the rules gets here; it bounds what the hub holds.
	fullHubWindow = 1 << 20
	// fullInputMax: the largest herdr_input the hub forwards.
	fullInputMax = 1 << 20
	// fullMaxCols and fullMaxRows bound the grid a browser may ask for (the node checks again).
	fullMaxCols, fullMaxRows = 500, 300
)

func fullEndMessage(id, why, code string, unsupported bool) map[string]any {
	msg := map[string]any{"type": "herdr_end", "id": id}
	if why != "" {
		msg["error"] = why
	}
	if code != "" {
		msg["code"] = code
	}
	if unsupported {
		msg["unsupported"] = true
	}
	return msg
}

// herdrOpen starts a view for an app. Anything wrong ends it with herdr_end.
func (h *Hub) herdrOpen(ctx context.Context, c *client, machineID, id, sessionID string, cols, rows int, focus bool) {
	end := func(why, code string) { c.sendJSON(fullEndMessage(id, why, code, false)) }
	if c.role != roleFull {
		end("forbidden: the full herdr view needs a full device", "forbidden")
		return
	}
	if id == "" || len(id) > 64 || cols < 1 || rows < 1 || cols > fullMaxCols || rows > fullMaxRows {
		end("bad request", "bad_request")
		return
	}
	m := h.localMachine(machineID)
	if m == nil {
		end("unknown machine", "")
		return
	}
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	// The machine it reached, not the id the app named: an absorbed id answers for that machine, and its node's events
	// come in under the machine's own id.
	r := &fullRelay{c: c, machineID: m.cfg.ID, id: id, nodeID: "hc_" + hex.EncodeToString(raw[:])}
	t := &h.fulls
	t.mu.Lock()
	own := 0
	for _, o := range t.byNode {
		if o.c == c {
			if o.id == id {
				t.mu.Unlock()
				end("this view id is in use", "bad_request")
				return
			}
			own++
		}
	}
	if own >= fullPerClient {
		t.mu.Unlock()
		end("too many full herdr views on this connection", "too_many")
		return
	}
	if t.byNode == nil {
		t.byNode = map[string]*fullRelay{}
	}
	t.byNode[r.nodeID] = r // before the call: the client's first bytes can come ahead of its answer
	t.mu.Unlock()

	go func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		params := map[string]any{"client_id": r.nodeID, "cols": cols, "rows": rows}
		if sessionID != "" {
			params["session_id"], params["focus"] = sessionID, focus
		}
		err := m.call(cctx, "herdr.client.open", params, nil)
		if err != nil {
			if h.fullRemove(r) {
				var rerr *rpc.Error
				code := ""
				if errors.As(err, &rerr) {
					code = rerr.Code
				}
				msg := err.Error()
				c.sendJSON(fullEndMessage(id, msg, code, strings.Contains(msg, "unknown method") || strings.Contains(msg, "unsupported")))
			}
			return
		}
		t.mu.Lock()
		live := t.byNode[r.nodeID] == r
		t.mu.Unlock()
		if live {
			c.sendJSON(map[string]any{"type": "herdr_state", "id": id, "state": "open", "cols": cols, "rows": rows})
		}
	}()
}

// fullRemove takes the view out; false when it was already gone.
func (h *Hub) fullRemove(r *fullRelay) bool {
	t := &h.fulls
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byNode[r.nodeID] != r {
		return false
	}
	delete(t.byNode, r.nodeID)
	return true
}

// fullOf is an app's own view by the id it chose: nothing else of another app's is reachable through it.
func (h *Hub) fullOf(c *client, id string) *fullRelay {
	t := &h.fulls
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.byNode {
		if r.c == c && r.id == id {
			return r
		}
	}
	return nil
}

// herdrInput forwards keys, in order with the app's other messages, so it writes from here.
func (h *Hub) herdrInput(c *client, id, data, b64 string) {
	r := h.fullOf(c, id)
	if r == nil || len(data)+len(b64) > fullInputMax || data == "" && b64 == "" {
		return
	}
	if m := h.localMachine(r.machineID); m != nil {
		p := map[string]any{"client_id": r.nodeID}
		if b64 != "" {
			p["b64"] = b64
		} else {
			p["data"] = data
		}
		_ = m.notify("herdr.client.input", p)
	}
}

func (h *Hub) herdrResize(c *client, id string, cols, rows int) {
	r := h.fullOf(c, id)
	if r == nil || cols < 1 || rows < 1 || cols > fullMaxCols || rows > fullMaxRows {
		return
	}
	if m := h.localMachine(r.machineID); m != nil {
		_ = m.notify("herdr.client.resize", map[string]any{"client_id": r.nodeID, "cols": cols, "rows": rows})
	}
}

// herdrAck passes the browser's acknowledgement on to the node, which reads the client's output only while the browser
// is less than a window behind.
func (h *Hub) herdrAck(c *client, id string, bytes int64) {
	t := &h.fulls
	r := h.fullOf(c, id)
	if r == nil {
		return
	}
	t.mu.Lock()
	if bytes > r.acked && bytes <= r.sent { // cumulative, and not more than was given
		r.acked = bytes
	} else {
		bytes = r.acked
	}
	t.mu.Unlock()
	if m := h.localMachine(r.machineID); m != nil {
		_ = m.notify("herdr.client.ack", map[string]any{"client_id": r.nodeID, "bytes": bytes})
	}
}

// herdrClose ends an app's view at its request.
func (h *Hub) herdrClose(c *client, id string) {
	if r := h.fullOf(c, id); r != nil && h.fullRemove(r) {
		h.fullStopNode(r)
	}
}

// fullStopNode tells the node to end a view's client.
func (h *Hub) fullStopNode(r *fullRelay) {
	if m := h.localMachine(r.machineID); m != nil {
		go func() { _ = m.notify("herdr.client.close", map[string]any{"client_id": r.nodeID}) }()
	}
}

// fullDrop ends every view of an app that went away.
func (h *Hub) fullDrop(c *client) {
	t := &h.fulls
	t.mu.Lock()
	var gone []*fullRelay
	for k, r := range t.byNode {
		if r.c == c {
			gone = append(gone, r)
			delete(t.byNode, k)
		}
	}
	t.mu.Unlock()
	for _, r := range gone {
		h.fullStopNode(r)
	}
}

// herdrDataPush is the herdr_data message to the app. Its fields are in sorted key order, so the bytes are what
// json.Marshal gave for the map this replaced, without the map build, key sort and boxing on every output chunk.
type herdrDataPush struct {
	Data string `json:"data"`
	ID   string `json:"id"`
	Type string `json:"type"`
}

// onHerdrClientData gives a node's client output to the view's app.
func (h *Hub) onHerdrClientData(machineID string, params json.RawMessage) {
	var p struct {
		ClientID string `json:"client_id"`
		Data     string `json:"data"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	t := &h.fulls
	t.mu.Lock()
	r := t.byNode[p.ClientID]
	if r == nil || r.machineID != machineID { // a node reaches only the views on its own machine
		t.mu.Unlock()
		return
	}
	r.sent += int64(base64.StdEncoding.DecodedLen(len(p.Data)) - strings.Count(p.Data[max(0, len(p.Data)-2):], "="))
	over := r.sent-r.acked > fullHubWindow
	t.mu.Unlock()
	if over {
		h.fullAbort(r, "the browser fell too far behind the terminal")
		return
	}
	b, err := json.Marshal(herdrDataPush{Data: p.Data, ID: r.id, Type: "herdr_data"})
	if err != nil {
		return
	}
	select {
	case r.c.out <- b:
	default: // the app's queue is full: its view ends rather than the whole connection
		h.fullAbort(r, "the browser is too slow")
	}
}

// fullAbort ends a view from here: the app is told, the node's client is stopped.
func (h *Hub) fullAbort(r *fullRelay, why string) {
	if h.fullRemove(r) {
		h.fullStopNode(r)
		r.c.sendJSON(fullEndMessage(r.id, why, "", false))
	}
}

// onHerdrClientEnd: the node's client ended on its own (it quit, its lease ran out, the browser stopped reading).
func (h *Hub) onHerdrClientEnd(machineID string, params json.RawMessage) {
	var p struct {
		ClientID string `json:"client_id"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	t := &h.fulls
	t.mu.Lock()
	r := t.byNode[p.ClientID]
	if r == nil || r.machineID != machineID {
		t.mu.Unlock()
		return
	}
	delete(t.byNode, p.ClientID)
	t.mu.Unlock()
	r.c.sendJSON(fullEndMessage(r.id, p.Error, "", false))
}

// fullsOffline ends every view on a machine whose node connection went away (its clients went with it).
func (h *Hub) fullsOffline(machineID string) {
	t := &h.fulls
	t.mu.Lock()
	var gone []*fullRelay
	for k, r := range t.byNode {
		if r.machineID == machineID {
			gone = append(gone, r)
			delete(t.byNode, k)
		}
	}
	t.mu.Unlock()
	for _, r := range gone {
		r.c.sendJSON(fullEndMessage(r.id, "电脑离线", "offline", false))
	}
}

// keepaliveEvery: how often the node hears that the hub still has its views (its lease is 75 s).
var keepaliveEvery = 20 * time.Second

// fullKeepalive renews the node's lease on the views it runs; a node that stops hearing this closes the clients.
func (h *Hub) fullKeepalive(ctx context.Context) {
	tick := time.NewTicker(keepaliveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		byMachine := map[string][]string{}
		t := &h.fulls
		t.mu.Lock()
		for _, r := range t.byNode {
			byMachine[r.machineID] = append(byMachine[r.machineID], r.nodeID)
		}
		t.mu.Unlock()
		for mid, ids := range byMachine {
			if m := h.localMachine(mid); m != nil {
				_ = m.notify("herdr.client.keepalive", map[string]any{"client_ids": ids})
			}
		}
	}
}

// herdrFocus moves herdr's focus to the pane of the session the view now shows. It is a state change on the computer
// (the focus is shared by every client of its herdr), so like the view itself it is for a full device and for the
// view's own connection only; the node checks that the pane is one the host lists.
func (h *Hub) herdrFocus(c *client, id, sessionID string) {
	if c.role != roleFull || sessionID == "" || len(sessionID) > 256 {
		return
	}
	r := h.fullOf(c, id)
	if r == nil {
		return
	}
	if m := h.localMachine(r.machineID); m != nil {
		_ = m.notify("herdr.client.focus", map[string]any{"client_id": r.nodeID, "session_id": sessionID})
	}
}

// onHerdrFocus tells the views on a machine that herdr's focus moved, each whether the change is its own.
func (h *Hub) onHerdrFocus(machineID string, params json.RawMessage) {
	var p struct {
		WorkspaceID string `json:"workspace_id"`
		TabID       string `json:"tab_id"`
		PaneID      string `json:"pane_id"`
		SessionID   string `json:"session_id"`
		Source      string `json:"source"`
		ClientID    string `json:"client_id"`
	}
	if json.Unmarshal(params, &p) != nil || p.PaneID == "" {
		return
	}
	switch p.Source {
	case "client", "persona":
	default:
		p.Source, p.ClientID = "other", ""
	}
	t := &h.fulls
	t.mu.Lock()
	var views []*fullRelay
	for _, r := range t.byNode {
		if r.machineID == machineID {
			views = append(views, r)
		}
	}
	t.mu.Unlock()
	for _, r := range views {
		msg := map[string]any{"type": "herdr_focus", "id": r.id, "machine_id": machineID, "workspace_id": p.WorkspaceID,
			"tab_id": p.TabID, "pane_id": p.PaneID, "source": p.Source, "mine": p.ClientID != "" && p.ClientID == r.nodeID}
		if p.SessionID != "" {
			msg["session_id"] = p.SessionID
		}
		r.c.sendJSON(msg)
	}
}
