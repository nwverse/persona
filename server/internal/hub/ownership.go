package hub

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Ownership (docs/assistant/managers.md, "Ownership"): everything belongs to one manager, and the hub sends what concerns a thing to
// the manager it belongs to. This file is the one place that says who that is, so moving a machine or a source to
// another manager (step 3) changes ownership here and nowhere else:
//
//   - a machine: one of hub.json's, the manager it was moved to (moveMachine), else the one that carries hub.json's
//     machines (devicesOwner); any other (a mesh member's), devicesOwner. Only the roster and the moves decide it,
//     never what a manager reports;
//   - a session: its machine's manager (a manager's own conversations run on its own machines; moving a machine
//     moves its sessions with it);
//   - an interaction: the manager that reported it, taken only on a machine of its own (cardAllowed);
//   - what names no thing the hub can place (the assistant's pages, the inbox, the knowledge base, the model
//     configuration, the todos, the extensions and sources…): the manager the request names (ManagerHeader, or the
//     query parameter ManagerQuery), else the default manager. An item of such a list (an inbox item, an extension)
//     is reached by naming the manager its list stamped on it.
//
// A request naming a manager the roster does not have is answered 404 no_manager; one whose manager is not connected
// 503 assistant_offline, as for the one assistant before.

const (
	// ManagerHeader names the manager a request is for (an id from GET /api/v1/managers).
	ManagerHeader = "X-Persona-Manager"
	// ManagerQuery is the same as a query parameter (a WebSocket, a link opened in a browser).
	ManagerQuery = "manager"
)

// errUnknownManager: the request names a manager the roster does not have.
var errUnknownManager = errors.New("no such manager")

// errOtherManager: an extension's token is for a machine, session or card of another manager's than its own
// (extensionReach); answered 403 codeOtherManager.
var errOtherManager = errors.New("this extension's manager does not own it")

const codeOtherManager = "other_manager"

// namedManager is the manager r names itself, "" when it names none.
func namedManager(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get(ManagerHeader)); id != "" {
		return id
	}
	return strings.TrimSpace(r.URL.Query().Get(ManagerQuery))
}

// impliedManager is the manager r is for, "" for none: an extension's token acts as its manager (the one that knew
// the token: authorizeExtension) and only as it, so naming another (header or query) is errOtherManager; any other
// credential, the manager it names.
func impliedManager(r *http.Request) (string, error) {
	named := namedManager(r)
	if p := principalOf(r.Context()); p.Role == roleExtension && p.Manager != "" {
		if named != "" && named != p.Manager {
			return "", errOtherManager
		}
		return p.Manager, nil
	}
	return named, nil
}

// requestManager is the manager a request that names no thing goes to: the one it is for (impliedManager: the one it
// names, errUnknownManager when the roster has no such manager; an extension's, its own), else the default.
func (h *Hub) requestManager(r *http.Request) (string, error) {
	id, err := impliedManager(r)
	if err != nil {
		return "", err
	}
	if id == "" {
		return h.defaultOwner(r.Context()), nil
	}
	if !h.knownManager(r.Context(), id) {
		return "", fmt.Errorf("%w: %q", errUnknownManager, clip(id, 64))
	}
	return id, nil
}

// knownManager: id is a manager of the roster (connected or not). Without a database, the ones connected and the
// assistant the hub starts.
func (h *Hub) knownManager(ctx context.Context, id string) bool {
	if id == assistantwire.AssistantID || h.busConnOf(id) != nil {
		return true
	}
	s := h.managerStore()
	if s == nil {
		return false
	}
	b, err := s.BusIdentityByID(ctx, id)
	return err == nil && b != nil && b.Role == bus.RoleManager
}

// rosterHas: id is a manager of the roster (has), and whether the roster could tell (sure: a failed lookup is not
// "no").
func (h *Hub) rosterHas(ctx context.Context, id string) (has, sure bool) {
	if id == assistantwire.AssistantID || h.busConnOf(id) != nil {
		return true, true
	}
	s := h.managerStore()
	if s == nil {
		return false, true
	}
	b, err := s.BusIdentityByID(ctx, id)
	if err != nil {
		return false, false
	}
	return b != nil && b.Role == bus.RoleManager, true
}

// ownerCache keeps the default manager for a moment: it is asked on every forwarded request.
type ownerCache struct {
	mu    sync.Mutex
	def   string
	until time.Time
	gen   uint64 // bumped by ownersChanged: a read that began before it does not keep what it read
}

const defaultOwnerTTL = 2 * time.Second

// defaultOwner is defaultManager, kept for a couple of seconds (managersChanged drops it).
func (h *Hub) defaultOwner(ctx context.Context) string {
	c := &h.owners
	c.mu.Lock()
	if c.def != "" && time.Now().Before(c.until) {
		def := c.def
		c.mu.Unlock()
		return def
	}
	gen := c.gen
	c.mu.Unlock()
	def, err := h.defaultManagerOrErr(ctx)
	if err != nil {
		return def // the assistant, because the store did not answer: not kept
	}
	c.mu.Lock()
	if c.gen == gen { // the roster did not change while it was read
		c.def, c.until = def, time.Now().Add(defaultOwnerTTL)
	}
	c.mu.Unlock()
	return def
}

// ownersChanged forgets the kept default (the roster or the default changed).
func (h *Hub) ownersChanged() {
	c := &h.owners
	c.mu.Lock()
	c.def = ""
	c.gen++
	c.mu.Unlock()
}

// machineOwner is the manager machine mid belongs to, the only one that may act on it or its sessions.
//
// It comes from the roster and the moves alone: a machine of hub.json's is its entry's (moved, else the carrier's);
// any other (a mesh member's: only the carrier is handed the mesh) the carrier's. Nothing a manager reports about
// itself (a derived report, its snapshot's sessions, its main conversation) makes a machine its own.
func (h *Hub) machineOwner(mid string) string {
	return h.entryOwner(mid)
}

// entryOwner is the manager that carries machine mid: for one its manager keeps out of hub.json, that one
// (machines_kept.go); for hub.json's, the one it was moved to (moveMachine), else the carrier. Any other machine is the
// carrier's, and so is one whose entry was removed while its move is still kept (forgetMachineOwner drops it).
func (h *Hub) entryOwner(mid string) string {
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	entries := d.entries // as cfgEntries reads them
	if !d.started && h.cfg != nil {
		entries = h.cfg.Machines
	}
	if !slices.ContainsFunc(entries, func(mc MachineConfig) bool { return mc.ID == mid }) {
		if k := d.kept[mid]; k != "" { // its manager keeps it out of hub.json (machines_kept.go)
			return k
		}
		return devicesOwner
	}
	required := false
	for _, mc := range entries {
		if mc.ID == mid {
			required = mc.OwnerRequired
			break
		}
	}
	if required && d.assignFailed {
		return unknownMachineOwner
	}
	if o := d.assign[mid]; o != "" {
		return o
	}
	if required {
		return unknownMachineOwner
	}
	return devicesOwner
}

// sessionOwner is the manager session sid on machine mid belongs to: its machine's (a manager's own conversation runs
// on a machine of its own).
func (h *Hub) sessionOwner(mid, _ string) string {
	return h.machineOwner(mid)
}

// conversationOwner is the manager whose snapshot lists conversation sid; "" for none.
func (h *Hub) conversationOwner(sid string) string {
	id, _ := h.conversation(sid)
	return id
}

// interactionOwner is the manager that reported pending interaction id; "" when it is not pending.
func (h *Hub) interactionOwner(id string) string {
	if ix, ok := h.pendingInteraction(id); ok {
		return ix.Manager
	}
	return ""
}

// ---- forwarding by owner ----

// ownerOf resolves the manager a request goes to; an error is errUnknownManager.
type ownerOf func(r *http.Request) (string, error)

// byRequest: the manager the request names, else the default.
func (h *Hub) byRequest(r *http.Request) (string, error) { return h.requestManager(r) }

// byMachine: the request's machine ({mid}) decides; a request that also names a manager still goes to the
// machine's (the machine is the thing it is about). An extension's token goes only to its own manager's machine.
func (h *Hub) byMachine(r *http.Request) (string, error) {
	mid := r.PathValue("mid")
	if mid == "" {
		mid = r.PathValue("id")
	}
	return h.extensionsOwn(r, h.machineOwner(mid))
}

// bySession: the request's session ({mid}/{sid}); an extension's token, only its own manager's.
func (h *Hub) bySession(r *http.Request) (string, error) {
	return h.extensionsOwn(r, h.sessionOwner(r.PathValue("mid"), r.PathValue("sid")))
}

// byInteraction: the pending interaction ({id}) decides (an extension's token: only its own manager's); one that is
// no longer pending goes by the request.
func (h *Hub) byInteraction(r *http.Request) (string, error) {
	if id := h.interactionOwner(r.PathValue("id")); id != "" {
		return h.extensionsOwn(r, id)
	}
	return h.requestManager(r)
}

// extensionsOwn is owner, the manager of the thing r is about; errOtherManager when r carries an extension's token
// of another manager (an extension acts on its own manager's things only, as its manager does).
func (h *Hub) extensionsOwn(r *http.Request, owner string) (string, error) {
	if p := principalOf(r.Context()); p.Role == roleExtension && p.Manager != "" && owner != p.Manager {
		return "", errOtherManager
	}
	return owner, nil
}

// extensionReach checks, before the request is served, that an extension's token p is about its own manager's
// things only: a route of a machine ({mid}, machines/{id}), a session ({mid}/{sid}), a pending card
// (interactions/{id}) or a conversation (assistant/conversations/{id}) of another manager's is errOtherManager. What a
// route takes in its query or body is the route's to check (TestEveryExtensionRouteIsBounded). This covers the routes the hub serves itself as much
// as the ones it forwards by owner (byMachine, bySession, byInteraction check the same).
func (h *Hub) extensionReach(r *http.Request, p principal) error {
	if p.Role != roleExtension || p.Manager == "" || h.apiMux == nil {
		return nil
	}
	_, pattern := h.apiMux.Handler(r)
	route, values := routeValues(pattern, r)
	mid := values["mid"]
	if mid == "" && strings.HasPrefix(route, "/api/v1/machines/{id}") {
		mid = values["id"]
	}
	if mid != "" && h.machineOwner(mid) != p.Manager {
		return errOtherManager
	}
	if strings.HasPrefix(route, "/api/v1/interactions/{id}") {
		if o := h.interactionOwner(values["id"]); o != "" && o != p.Manager {
			return errOtherManager
		}
	}
	if strings.HasPrefix(route, "/api/v1/assistant/conversations/{id}") {
		if o := h.conversationOwner(values["id"]); o != "" && o != p.Manager {
			return errOtherManager
		}
	}
	return nil
}

// extensionManager is the manager whose extension's token r carries; "" for any other credential (then nothing is
// limited by it).
func extensionManager(r *http.Request) string {
	if p := principalOf(r.Context()); p.Role == roleExtension {
		return p.Manager
	}
	return ""
}

// routeValues are the path of a ServeMux pattern (its method left out) and the path values the mux sets for r on
// it, read before the mux serves r. Literal segments are taken as they are, {name} is one segment unescaped,
// {name...} the rest.
func routeValues(pattern string, r *http.Request) (string, map[string]string) {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = pattern[i+1:]
	}
	values := map[string]string{}
	if !strings.HasPrefix(pattern, "/") {
		return pattern, values
	}
	pats := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	segs := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	for i, p := range pats {
		name, ok := strings.CutPrefix(p, "{")
		if !ok || !strings.HasSuffix(name, "}") || i >= len(segs) {
			continue
		}
		name = strings.TrimSuffix(name, "}")
		if rest, wild := strings.CutSuffix(name, "..."); wild {
			if v, err := url.PathUnescape(strings.Join(segs[i:], "/")); err == nil {
				values[rest] = v
			}
			break
		}
		if v, err := url.PathUnescape(segs[i]); err == nil && name != "$" {
			values[name] = v
		}
	}
	return pattern, values
}

// byConversation: the manager whose conversation {id} is; one no snapshot lists goes by the request.
// An extension's token reaches only its own manager's conversations.
func (h *Hub) byConversation(r *http.Request) (string, error) {
	if p := principalOf(r.Context()); p.Role == roleExtension && p.Manager != "" {
		if id := h.conversationOwner(r.PathValue("id")); id != "" {
			if _, err := h.extensionsOwn(r, id); err != nil {
				return "", err
			}
		}
		return h.requestManager(r)
	}
	if named := namedManager(r); named == "" {
		if id := h.conversationOwner(r.PathValue("id")); id != "" {
			return id, nil
		}
	}
	return h.requestManager(r)
}

// owner resolves r's manager with by, answering 404 no_manager itself (403 codeOtherManager for an extension's token
// of another manager); false once it answered.
func (h *Hub) owner(w http.ResponseWriter, r *http.Request, by ownerOf) (string, bool) {
	id, err := by(r)
	if errors.Is(err, errOtherManager) {
		writeErrCode(w, http.StatusForbidden, codeOtherManager, err)
		return "", false
	}
	if err != nil {
		writeErrCode(w, http.StatusNotFound, "no_manager", err)
		return "", false
	}
	return id, true
}

// forwardOwned is forwardHTTP to the manager by resolves.
func (h *Hub) forwardOwned(w http.ResponseWriter, r *http.Request, by ownerOf) {
	if id, ok := h.owner(w, r, by); ok {
		h.forwardHTTP(w, r, id)
	}
}

// ---- merged lists ----

// mergedLists are the list routes whose answer the hub merges over every manager (route → the array that is the
// list); each item gets "manager". The rest of the answer is the default manager's.
var mergedLists = map[string]string{
	"GET /api/v1/inbox":      "items",
	"GET /api/v1/todos":      "todos",
	"GET /api/v1/extensions": "extensions",
	"GET /api/v1/plugins":    "plugins",
	"GET /api/v1/connectors": "connectors",
}

// listAnswer is one manager's answer to a list request.
type listAnswer struct {
	id     string
	status int
	header http.Header
	body   []byte
}

func (a listAnswer) ok() bool { return a.status >= 200 && a.status < 300 }

// bufferedWriter keeps what a handler writes (a manager's answer to a list request).
type bufferedWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedWriter() *bufferedWriter { return &bufferedWriter{header: http.Header{}} }

func (b *bufferedWriter) Header() http.Header { return b.header }
func (b *bufferedWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
func (b *bufferedWriter) Flush() {} // a relayed answer flushes as it streams; this one is written whole

func (b *bufferedWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

// listManagers are the managers a merged list asks: the default first, then the others connected now, by id.
func (h *Hub) listManagers(ctx context.Context) []string {
	def := h.defaultOwner(ctx)
	out := []string{def}
	var rest []string
	for _, p := range h.busRoster() {
		if p.Role == bus.RoleManager && p.ID != def {
			rest = append(rest, p.ID)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// mergeList answers a list request: a request for a manager (one it names, an extension's: its own, impliedManager) gets
// that manager's answer, else every manager is asked (ask, one after the other: the managers are few) and the lists under key merged, the default's first. Every
// item is stamped with its manager. A manager that does not answer is left out; when none does, the default's
// answer is given as it is.
func (h *Hub) mergeList(w http.ResponseWriter, r *http.Request, key string, ask func(w http.ResponseWriter, r *http.Request, id string)) {
	ids := []string{}
	named, err := impliedManager(r)
	if err != nil {
		writeErrCode(w, http.StatusForbidden, codeOtherManager, err)
		return
	}
	if named != "" {
		id, ok := h.owner(w, r, h.byRequest)
		if !ok {
			return
		}
		ids = append(ids, id)
	} else {
		ids = h.listManagers(r.Context())
	}
	body, err := readBody(w, r)
	if err != nil {
		return
	}
	var answers []listAnswer
	for _, id := range ids {
		bw := newBufferedWriter()
		ask(bw, withBody(r, body), id)
		answers = append(answers, listAnswer{id: id, status: cmp.Or(bw.status, http.StatusOK), header: bw.header, body: bw.body.Bytes()})
	}
	writeMerged(w, key, answers)
}

// writeMerged writes answers merged (see mergeList).
func writeMerged(w http.ResponseWriter, key string, answers []listAnswer) {
	var base *listAnswer
	var items []json.RawMessage
	var top map[string]json.RawMessage
	for i := range answers {
		a := &answers[i]
		if !a.ok() {
			continue
		}
		var t map[string]json.RawMessage
		if json.Unmarshal(a.body, &t) != nil {
			continue
		}
		raw, has := t[key]
		var list []json.RawMessage
		if has && json.Unmarshal(raw, &list) != nil {
			continue
		}
		for _, it := range list {
			items = append(items, stampManager(it, a.id))
		}
		if base == nil {
			base, top = a, t
		}
	}
	if base == nil { // nobody answered: the default's answer as it is
		a := answers[0]
		copyHeader(w.Header(), a.header)
		w.WriteHeader(a.status)
		_, _ = w.Write(a.body)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	list, _ := json.Marshal(items)
	top[key] = list
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(top)
	copyHeader(w.Header(), base.header)
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(base.status)
	_, _ = w.Write(buf.Bytes())
}

func copyHeader(dst, src http.Header) {
	for k, v := range src {
		dst[k] = v
	}
}

// stampManager is item (a JSON object) with "manager": id; anything else as it is.
func stampManager(item json.RawMessage, id string) json.RawMessage {
	t := bytes.TrimSpace(item)
	if len(t) < 2 || t[0] != '{' {
		return item
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(t, &fields) != nil {
		return item
	}
	idJSON, _ := json.Marshal(id)
	if _, has := fields[ManagerQuery]; has {
		fields[ManagerQuery] = idJSON
		out, _ := json.Marshal(fields)
		return out
	}
	var b bytes.Buffer
	b.WriteString(`{"manager":`)
	b.Write(idJSON)
	if len(fields) > 0 {
		b.WriteByte(',')
		b.Write(bytes.TrimSpace(t[1:]))
	} else {
		b.WriteByte('}')
	}
	return b.Bytes()
}

// readBody reads r's body (a list request has none, or a small one) so every manager can be asked with it; it
// answered when it fails.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(http.MaxBytesReader(w, r.Body, busHTTPLimit)); err != nil {
		writeBodyErr(w, err, err)
		return nil, err
	}
	return b.Bytes(), nil
}

// withBody is r with body to read again.
func withBody(r *http.Request, body []byte) *http.Request {
	r2 := r.Clone(r.Context())
	if body == nil {
		r2.Body = http.NoBody
	} else {
		r2.Body = readCloser{bytes.NewReader(body)}
	}
	return r2
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

// managerKey carries a resolved manager in a request's context (a handler wrapped around the forwarding, such as a
// reply's receipt, resolves it first).
type managerKey struct{}

func withManager(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), managerKey{}, id))
}

// managerOfContext is the manager withManager put on r; the assistant the hub starts when none.
func managerOfContext(r *http.Request) string {
	if id, ok := r.Context().Value(managerKey{}).(string); ok && id != "" {
		return id
	}
	return assistantwire.AssistantID
}

// notifyManagers sends every connected manager a one-way event (what the hub saw that is no one manager's: the apps
// it has, a mute, a source with something new); errBusOffline when none is connected.
func (h *Hub) notifyManagers(event string, params any) error {
	sent := false
	for _, p := range h.busRoster() {
		if p.Role == bus.RoleManager && h.notifyBus(p.ID, event, params) == nil {
			sent = true
		}
	}
	if !sent {
		return errBusOffline
	}
	return nil
}

// managerID is the manager c is; the assistant the hub starts for none (a test that plays the bus).
func (c *busConn) managerID() string {
	if c == nil || c.ident.ID == "" {
		return assistantwire.AssistantID
	}
	return c.ident.ID
}

// ---- moving a machine or a source to another manager ----

// machineOwnersKey keeps which of hub.json's machines another manager than the carrier carries: {machine: manager}.
const machineOwnersKey = "managers.machines"

// Not a valid roster ID: failed ownership reads must never select a manager.
const unknownMachineOwner = "!ownership-unavailable"

var (
	errNotMovable  = errors.New("only a computer of this hub's own list can move to another manager")
	errMoveOffline = errors.New("the manager is not connected")
	// errOwnersUnknown: the hub could not read which manager carries which machine; a move now would overwrite it.
	errOwnersUnknown = errors.New("the hub could not read which manager carries which machine; try again")
	// errSourceInDoubt: the import did not answer; whether the new manager has the source could not be told either.
	errSourceInDoubt = errors.New("the new manager did not say whether it took the source")
)

// moves orders a manager's removal against the moves to or from it: moves (machines and sources) hold managers
// shared, a removal exclusive, so it waits for them and the ones after it find the manager gone. A machine's moves
// are one at a time on top of that (d.moving); a source's too (sources).
type moves struct {
	managers sync.RWMutex
	mu       sync.Mutex             // guards sources
	sources  map[string]*sourceLock // a source being moved, while one is
}

// sourceLock is one source's move lock, kept while a move of it runs or waits.
type sourceLock struct {
	mu    sync.Mutex
	users int
}

// lockSource holds source's move lock (one move of a source at a time) and returns its unlock, which forgets the lock
// once no move of it waits.
func (mv *moves) lockSource(source string) func() {
	mv.mu.Lock()
	if mv.sources == nil {
		mv.sources = map[string]*sourceLock{}
	}
	l := mv.sources[source]
	if l == nil {
		l = &sourceLock{}
		mv.sources[source] = l
	}
	l.users++
	mv.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		mv.mu.Lock()
		if l.users--; l.users == 0 {
			delete(mv.sources, source)
		}
		mv.mu.Unlock()
	}
}

// removingManager runs remove (a manager's removal) once no move to or from a manager is under way, and keeps new
// ones waiting until it is done.
func (h *Hub) removingManager(remove func() error) error {
	h.moving.managers.Lock()
	defer h.moving.managers.Unlock()
	return remove()
}

// loadMachineOwners reads machineOwnersKey (a hub that starts, before any manager connects). An error leaves assign
// unread: dedicated local machines are withheld until a successful retry; legacy
// machines keep their existing assignments and carrier fallback.
func (h *Hub) loadMachineOwners(ctx context.Context) error {
	var m map[string]string
	if s := h.managerStore(); s != nil {
		if _, err := s.Setting(ctx, machineOwnersKey, &m); err != nil {
			h.logf("managers: which manager carries which machine: %v", err)
			h.devices.mu.Lock()
			h.devices.assignRead, h.devices.assignFailed = false, true
			h.devices.mu.Unlock()
			return err
		}
	}
	// A machine hub.json no longer has, or an owner the roster no longer has: back to the carrier.
	kept := map[string]string{}
	for mid, o := range m {
		if h.cfgEntry(mid) == nil || o == "" || o == devicesOwner {
			continue
		}
		if has, sure := h.rosterHas(ctx, o); sure && !has {
			continue // its manager was removed (a failed lookup keeps it: it is asked again at the next read)
		}
		kept[mid] = o
	}
	if s := h.managerStore(); s != nil && len(kept) != len(m) {
		if err := s.PutSetting(ctx, machineOwnersKey, kept); err != nil {
			h.logf("managers: which manager carries which machine: %v", err)
		}
	}
	d := &h.devices
	d.mu.Lock()
	d.assign, d.assignRead, d.assignFailed = kept, true, false
	d.mu.Unlock()
	return nil
}

// moveMachine hands hub.json's machine mid to manager to (devicesOwner: back to the carrier). The hub keeps the
// owner (machineOwnersKey) and from now on sends what concerns the machine to the new one; each manager is handed
// its machines again (devices.config): the old one lets it go, the new one connects it. Until the new one reports it
// the machine shows offline. The credentials the hub keeps for it (a service machine's, a Persona Link's) stay with
// the hub and go only to its owner (devices.connector_cred, the link relay).
// Moves go one at a time, each from the owners as the last one left them: two at once each wrote the map without
// the other's machine, and one move was lost on the next start. Owners the hub could not read when it started are
// read first, so a move does not write over them.
func (h *Hub) moveMachine(ctx context.Context, mid, to string) (from string, err error) {
	if h.knownEntry(mid) == nil {
		return "", errNotMovable
	}
	h.moving.managers.RLock() // not while its manager is being removed (removingManager)
	defer h.moving.managers.RUnlock()
	d := &h.devices
	d.moving.Lock()
	defer d.moving.Unlock()
	return h.moveMachineMoving(ctx, mid, to)
}

// moveMachineMoving is moveMachine with d.moving held.
func (h *Hub) moveMachineMoving(ctx context.Context, mid, to string) (from string, err error) {
	d := &h.devices
	// hub.json's entry, or a kept one's as its manager last reported it (owner_required is kept: machines_kept.go).
	if mc := h.knownEntry(mid); mc != nil && mc.OwnerRequired && to == devicesOwner {
		return h.entryOwner(mid), errors.New("a dedicated local machine requires an explicit non-carrier manager")
	}
	if err := h.readOwnersMoving(ctx); err != nil {
		return h.machineOwner(mid), err
	}
	// Still in the roster (a removal does not run while this does: removingManager).
	if to != devicesOwner && !h.knownManager(ctx, to) {
		return h.entryOwner(mid), fmt.Errorf("%w: %q", errUnknownManager, clip(to, 64))
	}
	from = h.entryOwner(mid)
	if from == to {
		return from, nil
	}
	if h.keptBy(mid) != "" && h.cfgEntry(mid) == nil { // its manager keeps it (machines_kept.go)
		if err := h.moveKept(ctx, mid, from, to); err != nil {
			return from, err
		}
		h.handMachineEntry(mid, from, to)
		h.machineHandedOver(mid, to)
		h.logf("managers: [%s] moved from %s to %s", mid, from, to)
		return from, nil
	}
	d.mu.Lock()
	next := map[string]string{}
	for k, v := range d.assign {
		next[k] = v
	}
	if to == devicesOwner {
		delete(next, mid)
	} else {
		next[mid] = to
	}
	d.mu.Unlock()
	if s := h.managerStore(); s != nil {
		if err := s.PutSetting(ctx, machineOwnersKey, next); err != nil {
			return from, err
		}
	}
	d.mu.Lock()
	d.assign = next
	delete(d.putTried, mid) // to is put it afresh (settleOwnList)
	d.mu.Unlock()
	h.handMachineEntry(mid, from, to) // shown as last reported until to reports it (machine_entries.go)
	h.machineHandedOver(mid, to)
	h.logf("managers: [%s] moved from %s to %s", mid, from, to)
	// A manager that keeps its own list does not go by the hand-over: from lets it go here, and to is put it from
	// hub.json once it reports (settleOwnList, after this move).
	if h.keepsList(from) {
		var out devicewire.MachineRemoved
		if err := h.keeperCall(ctx, from, devicewire.MethodMachinesRemove, devicewire.MachineRef{MachineID: mid}, &out); err != nil {
			h.logf("managers: [%s] moved away from %s, which could not take it out of its own list: %v", mid, from, err)
		}
	}
	if h.keepsList(to) {
		go h.settleOwnList(to)
	}
	return from, nil
}

// readOwnersMoving reads the owners if the hub could not when it started, and hands the machines another manager
// carries to it now. d.moving is held.
func (h *Hub) readOwnersMoving(ctx context.Context) error {
	d := &h.devices
	d.mu.Lock()
	read := d.assignRead
	d.mu.Unlock()
	if read {
		return nil
	}
	if err := h.loadMachineOwners(ctx); err != nil {
		return fmt.Errorf("%w: %v", errOwnersUnknown, err)
	}
	for _, mc := range h.knownEntries() {
		if m := h.localMachine(mc.ID); m != nil && m.ownerOrCarrier() != h.entryOwner(mc.ID) {
			h.machineHandedOver(mc.ID, h.entryOwner(mc.ID))
		}
	}
	return nil
}

// machineHandedOver makes machine mid manager to's in what the hub shows and routes, offline until it reports it,
// withdraws the other managers' cards on it, cuts the relays another manager has for it (Persona Link comes back to
// to), tells the managers their mutes, and hands every manager its machines again.
func (h *Hub) machineHandedOver(mid, to string) {
	h.withdrawCardsOn(mid, to)
	h.cutRelays("", mid)
	defer h.mutesChanged()
	if m := h.localMachine(mid); m != nil {
		m.mu.Lock()
		m.owner = to
		m.mu.Unlock()
		why := i18n.Localize(errAssistantOffline, h.userLang())
		h.offlineLocal(m, why)
		h.connectionEnded(m, why)
		h.broadcastMachines()
		h.sessionsChanged(m)
	}
	h.devicesConfigChanged()
}

// releaseMachines gives the machines removed manager id carried back to the carrier. Called under removingManager.
func (h *Hub) releaseMachines(ctx context.Context, id string) {
	d := &h.devices
	d.moving.Lock() // the whole release: a move meanwhile is not undone by it, nor one of its machines missed
	defer d.moving.Unlock()
	if err := h.readOwnersMoving(ctx); err != nil { // owners not read yet would keep the removed one's machines
		h.logf("managers: %s's machines back to %s: %v", id, devicesOwner, err)
		return
	}
	d.mu.Lock()
	var mine, keptMine []string
	for mid, o := range d.assign {
		if o == id {
			mine = append(mine, mid)
		}
	}
	for mid, o := range d.kept {
		if o == id {
			keptMine = append(keptMine, mid)
		}
	}
	d.mu.Unlock()
	// The machines it kept in its own list went with it: their entries (their secrets) were in its manager.json only.
	for _, mid := range keptMine {
		if err := h.setKept(ctx, mid, ""); err != nil {
			h.logf("managers: [%s] kept by %s, which was removed: %v", mid, id, err)
			continue
		}
		d.mu.Lock()
		reserved := d.assign[mid] != ""
		d.mu.Unlock()
		if reserved { // a dedicated machine's reservation goes with it (machines_owned_local.go)
			if err := h.assignOwner(ctx, mid, devicesOwner); err != nil {
				h.logf("managers: [%s] kept by %s, which was removed: its owner record: %v", mid, id, err)
			}
		}
		h.forgetMachineEntry(mid)
		h.machineGone(mid)
		h.logf("managers: [%s] kept by %s in its own list: gone with it", mid, id)
	}
	for _, mid := range mine {
		if k := h.keptBy(mid); k != "" && k != id && h.cfgEntry(mid) == nil {
			// Kept by another manager now: a stale reservation of id's, dropped, never a move (machines_kept.go).
			if err := h.assignOwner(ctx, mid, devicesOwner); err != nil {
				h.logf("managers: [%s] kept by %s: %s's old reservation: %v", mid, k, id, err)
			}
			continue
		}
		if mc := h.knownEntry(mid); mc != nil && mc.OwnerRequired {
			if err := h.releaseDedicatedMachineMoving(ctx, mid); err != nil {
				h.logf("managers: [%s] release dedicated owner: %v", mid, err)
			}
			continue
		}
		if _, err := h.moveMachineMoving(ctx, mid, devicesOwner); err != nil {
			h.logf("managers: [%s] back to %s: %v", mid, devicesOwner, err)
		}
	}
}

// moveSource moves source (extwire.SourceRef: "mail:<account>") from manager from to manager to: the old one
// exports it (its secrets in plaintext, over the bus only: the hub neither keeps nor logs the bundle), the new one
// imports it (sealing them with its own key), and the old one lets it go. Both have to be connected; one move of a
// source at a time. When the new one says it has the source already (a move whose release failed, done again), the
// hub checks it has that source and goes on to the release. When the import does not answer, the hub asks the new
// one whether it has the source: yes, it goes on; no, the old one keeps it; no answer, errSourceInDoubt. When the
// release fails both have it, and the error says so: moving it again releases it.
func (h *Hub) moveSource(ctx context.Context, source, from, to string) (string, error) {
	h.moving.managers.RLock()
	defer h.moving.managers.RUnlock()
	defer h.moving.lockSource(source)()
	if !h.knownManager(ctx, from) || !h.knownManager(ctx, to) {
		return "", errUnknownManager
	}
	if h.busConnOf(from) == nil || h.busConnOf(to) == nil {
		return "", errMoveOffline
	}
	var bundle json.RawMessage
	if err := h.callBus(ctx, from, extwire.MethodSourceExport, extwire.SourceRef{Source: source}, &bundle); err != nil {
		return "", fmt.Errorf("%s: export: %w", from, err)
	}
	var ref extwire.SourceRef
	err := h.callBus(ctx, to, extwire.MethodSourceImport, bundle, &ref)
	clear(bundle)
	var coded *rpc.Error
	switch {
	case err == nil:
	case errors.As(err, &coded) && coded.Code == extwire.CodeSourceExists:
		// A move whose release failed, done again: the new one has it if it can export that very source.
		if !h.sourceAt(ctx, to, source) {
			return "", fmt.Errorf("%s: import: %w", to, err)
		}
		ref.Source = source
	case errors.As(err, &coded):
		return "", fmt.Errorf("%s: import: %w", to, err) // refused: the old one keeps it
	default:
		// No answer (dropped, timed out): whether it took it decides.
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		has, known := h.sourceKnownAt(qctx, to, source)
		cancel()
		switch {
		case !known:
			return "", fmt.Errorf("%w: %v", errSourceInDoubt, err)
		case !has:
			return "", fmt.Errorf("%s: import: %w", to, err)
		}
		ref.Source = source
	}
	// The new one has it: the release is asked for even when the caller stopped waiting.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := h.callBus(rctx, from, extwire.MethodSourceRelease, extwire.SourceRef{Source: source}, nil); err != nil {
		return cmp.Or(ref.Source, source), fmt.Errorf("%s has it now, but %s did not let it go: %w", to, from, err)
	}
	h.logf("managers: source %s moved from %s to %s", clip(source, 80), from, to)
	return cmp.Or(ref.Source, source), nil
}

// sourceAt: manager id has source (it exports it; what it exports is dropped at once).
func (h *Hub) sourceAt(ctx context.Context, id, source string) bool {
	has, known := h.sourceKnownAt(ctx, id, source)
	return known && has
}

// sourceKnownAt asks manager id whether it has source: has, and whether it answered.
func (h *Hub) sourceKnownAt(ctx context.Context, id, source string) (has, known bool) {
	var b extwire.SourceBundle
	err := h.callBus(ctx, id, extwire.MethodSourceExport, extwire.SourceRef{Source: source}, &b)
	clear(b.Data)
	var coded *rpc.Error
	switch {
	case err == nil && b.Source == source:
		return true, true
	case err == nil: // an answer naming no source, or another: it did not say it has this one
		return false, false
	case errors.As(err, &coded) && coded.Code == extwire.CodeNoSource:
		return false, true
	}
	return false, false
}

// ownsMachine: manager c carries machine mid (what only its manager may do or ask for: report it, answer for its
// node, its credential); else a coded refusal.
func (h *Hub) ownsMachine(c *busConn, mid string) error {
	if h.machineOwner(mid) != c.managerID() {
		return &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("machine %q is another manager's", clip(mid, 64))}
	}
	return nil
}

// ownsRef: what manager owner says may name machine mid and session sid (either may be ""): only a machine of its
// own. A session named without its machine goes by where it is (the machines that list it, the manager whose
// conversation it is): none of another manager's. What names neither is its own.
func (h *Hub) ownsRef(owner, mid, sid string) bool {
	if mid != "" {
		return h.machineOwner(mid) == owner
	}
	if sid == "" || owner == devicesOwner && !h.anyMoved() { // nothing moved: every machine is the carrier's
		return true
	}
	if id, _ := h.conversation(sid); id != "" && id != owner {
		return false
	}
	for _, m := range h.machinesNow() {
		if _, ok := m.session(sid); ok && h.machineOwner(m.cfg.ID) != owner {
			return false
		}
	}
	return true
}

// anyMoved: some machine is carried by another manager than the carrier.
func (h *Hub) anyMoved() bool {
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.assign) > 0
}

// cardAllowed: manager owner may report pending card ix: one naming a machine, or a session, only on a machine of its
// own (ownsRef), so no card looks like it comes from another manager's session and gets its answer; a card naming
// neither is its own.
func (h *Hub) cardAllowed(owner string, ix proto.Interaction) bool {
	return h.ownsRef(owner, ix.MachineID, ix.SessionID)
}

// withdrawCardsOn drops the pending cards on machine mid of another manager than to (mid moved to it: its sessions
// and the questions about them are to's now), those naming one of its sessions without the machine too; the apps
// are told they are over. A card naming no session of mid's stays its manager's.
func (h *Hub) withdrawCardsOn(mid, to string) {
	on := map[string]bool{}
	if m := h.localMachine(mid); m != nil {
		for _, s := range m.sessionList() {
			on[s.SessionID] = true
		}
	}
	f := &h.reported
	f.mu.Lock()
	var gone []string
	for id, ix := range f.interactions {
		if cmp.Or(ix.Manager, assistantwire.AssistantID) == to {
			continue
		}
		if ix.MachineID == mid || ix.MachineID == "" && ix.SessionID != "" && on[ix.SessionID] {
			gone = append(gone, id)
			delete(f.interactions, id)
		}
	}
	f.mu.Unlock()
	for _, id := range gone {
		h.broadcast(map[string]any{"type": "interaction_resolved", "id": id})
	}
}

// forgetMachineOwner drops who carries machine mid, a hub.json entry removed (removeMachineLive): an entry added
// again later starts at the carrier.
func (h *Hub) forgetMachineOwner(ctx context.Context, mid string) {
	d := &h.devices
	d.moving.Lock()
	defer d.moving.Unlock()
	d.mu.Lock()
	_, had := d.assign[mid]
	next := make(map[string]string, len(d.assign))
	for k, v := range d.assign {
		if k != mid {
			next[k] = v
		}
	}
	read := d.assignRead
	d.mu.Unlock()
	if !had || !read {
		return
	}
	if s := h.managerStore(); s != nil {
		if err := s.PutSetting(ctx, machineOwnersKey, next); err != nil {
			h.logf("managers: [%s] forget who carried it: %v", mid, err)
			return
		}
	}
	d.mu.Lock()
	d.assign = next
	d.mu.Unlock()
}

// callerKey carries the manager whose device.call a node call is (withCaller).
type callerKey struct{}

// withCaller marks ctx as manager id's device.call: the node is reached only while the machine is id's (busNode.Call).
func withCaller(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callerKey{}, id)
}

// callerOf is the manager withCaller put on ctx.
func callerOf(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(callerKey{}).(string)
	return id, ok
}

// deviceCallAllowed: manager id may make device.call p: on a machine of its own only (the calls about every machine,
// devices.list, answer with its own machines). Another manager's machine, its sessions and its node are refused
// no_device and never reached; the assistant the hub starts is a manager like the others.
func (h *Hub) deviceCallAllowed(id string, p assistantwire.DeviceCall) error {
	if p.Device == assistantwire.DeviceAll || h.machineOwner(p.Device) == id {
		return nil
	}
	return &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("%s on machine %q: another manager's", clip(p.Method, 64), clip(p.Device, 64))}
}
