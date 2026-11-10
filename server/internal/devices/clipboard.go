package devices

// Sharing a computer's clipboard with the phone (#3064, docs/operations/clipboard.md). Each machine has a switch, off
// until the user turns it on (Settings › Machines, or the first use's prompt); the manager keeps it in settings and
// relays GET / PUT /api/v1/machines/{mid}/clipboard to the node's clipboard.get / clipboard.set while it is on.
// Only a device with full credentials gets here: the hub keeps answer-only devices (the watch) and extensions' tokens
// out, and this checks the role it relays again. Clipboard content is never logged: only its kind and size.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// clipboardKey is the settings key holding map[machineID]bool: the machines the user turned clipboard sharing on for.
const clipboardKey = "machine_clipboard"

// clipboardSwitches caches clipboardKey, loaded on first use (machine lists read it on every report).
type clipboardSwitches struct {
	mu       sync.Mutex
	loadedAt time.Time // zero: never loaded
	failedAt time.Time
	on       map[string]bool
}

// clipboardFresh is how long a load of the switches is trusted: another manager process on the same settings (a
// blue-green switch) may have turned one off, and an off switch must not stay on here for long.
const clipboardFresh = 10 * time.Second

// clipboardOn is whether the user turned clipboard sharing on for machine; off when never set, and off while the
// settings cannot be read (it fails closed).
func (d *Devices) clipboardOn(machine string) bool {
	c := &d.clipboard
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.loadedAt) >= clipboardFresh && (c.failedAt.IsZero() || time.Since(c.failedAt) >= engineRetry) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.loadClipboardLocked(ctx); err != nil {
			d.logf("clipboard switches: %v (retried in %s)", err, engineRetry)
			c.failedAt, c.on = time.Now(), nil // fails closed
		}
	}
	return c.on[machine]
}

func (d *Devices) loadClipboardLocked(ctx context.Context) error {
	c := &d.clipboard
	on := map[string]bool{}
	if d.opts.Store != nil {
		if _, err := d.opts.Store.Setting(ctx, clipboardKey, &on); err != nil {
			return err
		}
	}
	c.on, c.loadedAt, c.failedAt = on, time.Now(), time.Time{}
	return nil
}

// setClipboardOn stores the switch of machine: read, changed and written under the lock, so two switches flipped at
// once both stay.
func (d *Devices) setClipboardOn(ctx context.Context, machine string, on bool) error {
	if d.opts.Store == nil {
		return i18n.Err("devices-api.no_store")
	}
	c := &d.clipboard
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := d.loadClipboardLocked(ctx); err != nil {
		return err
	}
	next := make(map[string]bool, len(c.on)+1)
	for id, v := range c.on {
		next[id] = v
	}
	if on {
		next[machine] = true
	} else {
		delete(next, machine)
	}
	if err := d.opts.Store.PutSetting(ctx, clipboardKey, next); err != nil {
		return err
	}
	c.on = next
	return nil
}

// clipboardSupportLocked is what the node said it can do (m.mu held): an older node cannot share at all; a machine
// never reached has said nothing yet.
func (m *Machine) clipboardSupportLocked() (supported bool, reason string) {
	switch {
	case m.info.Clipboard != nil:
		return m.info.Clipboard.Supported, m.info.Clipboard.Reason
	case m.info.Version == "" && m.info.OS == "":
		return false, ""
	case m.info.Protocol > 0 && nodeHas(m.info, nodeproto.Clipboard):
		return true, "" // announced the methods without the field: trust them
	}
	return false, proto.ClipboardReasonNodeOutdated
}

// clipboardState is proto.Machine.Clipboard; its Reason is filled in the reader's language at the hub
// (i18n.ClipboardStatus).
func (m *Machine) clipboardState(enabled bool) *proto.MachineClipboard {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clipboardStateLocked(enabled)
}

func (m *Machine) clipboardStateLocked(enabled bool) *proto.MachineClipboard {
	supported, reason := m.clipboardSupportLocked()
	return &proto.MachineClipboard{Supported: supported, Enabled: enabled, ReasonCode: reason}
}

// clipboardOf is the clipboard state of m as the routes answer it, in the request's language.
func (d *Devices) clipboardOf(r *http.Request, m *Machine) *proto.MachineClipboard {
	return i18n.ClipboardStatus(i18n.FromRequest(r), m.clipboardState(d.clipboardOn(m.cfg.ID)))
}

// clipboardRoleOK refuses a device that may only read and answer (the hub already does; this is the manager's own
// check of the role it relays) and an extension's token.
func clipboardRoleOK(w http.ResponseWriter, r *http.Request) bool {
	switch r.Header.Get(assistantwire.RoleHeader) {
	case "", "full":
		return true
	}
	writeErrCode(w, http.StatusForbidden, proto.CodeReadOnly, i18n.Err("devices-clipboard.read_only"))
	return false
}

// clipboardReady answers the request itself, and false, when the clipboard may not be used now: the switch is off
// (403) or the computer cannot share it (409). An offline one is found by the call (503).
func (d *Devices) clipboardReady(w http.ResponseWriter, r *http.Request, m *Machine) bool {
	if !clipboardRoleOK(w, r) {
		return false
	}
	st := d.clipboardOf(r, m)
	if !st.Enabled {
		writeErrCode(w, http.StatusForbidden, proto.CodeClipboardDisabled, i18n.Err("devices-clipboard.disabled", i18n.Args{"name": m.cfg.Name}))
		return false
	}
	if !st.Supported && st.ReasonCode != "" {
		writeClipboardUnsupported(w, r, st.ReasonCode)
		return false
	}
	return true
}

func writeClipboardUnsupported(w http.ResponseWriter, r *http.Request, reason string) {
	st := i18n.ClipboardStatus(i18n.FromRequest(r), &proto.MachineClipboard{ReasonCode: reason})
	writeErrCode(w, http.StatusConflict, proto.CodeClipboardUnsupported, errors.New(st.Reason))
}

// writeClipboardCallErr answers a failed clipboard.get / clipboard.set by what the node said.
func (d *Devices) writeClipboardCallErr(w http.ResponseWriter, r *http.Request, m *Machine, op string, err error) {
	var coded *rpc.Error
	errors.As(err, &coded)
	code := ""
	if coded != nil {
		code = coded.Code
	}
	switch {
	case isOffline(err):
		writeErrCode(w, http.StatusServiceUnavailable, "offline", i18n.Err("devices-clipboard.offline", i18n.Args{"name": m.cfg.Name}))
	case code == proto.CodeClipboardUnsupported:
		writeClipboardUnsupported(w, r, coded.Message)
	case code == proto.CodeNodeOutdated, coded != nil && strings.HasPrefix(coded.Message, "unknown method"):
		writeClipboardUnsupported(w, r, proto.ClipboardReasonNodeOutdated)
	case code == proto.CodeTooLarge:
		writeErrCode(w, http.StatusRequestEntityTooLarge, proto.CodeTooLarge, i18n.Err("devices-clipboard.too_large"))
	case code == proto.CodeClipboardBadImage:
		writeErrCode(w, http.StatusBadRequest, proto.CodeClipboardBadImage, i18n.Err("devices-clipboard.bad_image"))
	default:
		// The node's message says what failed (never the content); it is logged, the app gets the plain words.
		d.logf("[%s] clipboard %s: %v", m.cfg.ID, op, err)
		writeErrCode(w, http.StatusBadGateway, proto.CodeClipboardFailed, i18n.Err("devices-clipboard.failed", i18n.Args{"name": m.cfg.Name}))
	}
}

// clipboardCallTimeout bounds one call to the node: it gives up on the clipboard after clipTimeout (15 s).
const clipboardCallTimeout = 20 * time.Second

// getClipboard: GET /api/v1/machines/{mid}/clipboard → {kind, text?, image?: {mime, data}}.
func (d *Devices) getClipboard(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !d.clipboardReady(w, r, m) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), clipboardCallTimeout)
	defer cancel()
	var out proto.ClipboardContent
	if err := m.call(ctx, "clipboard.get", map[string]any{}, &out); err != nil {
		d.writeClipboardCallErr(w, r, m, "get", err)
		return
	}
	switch out.Kind {
	case proto.ClipboardKindText, proto.ClipboardKindImage, proto.ClipboardKindConcealed:
	default:
		out = proto.ClipboardContent{Kind: proto.ClipboardKindEmpty}
	}
	if out.Kind == proto.ClipboardKindConcealed {
		out.Text, out.Image = nil, nil
	}
	d.logf("[%s] clipboard read by the app: %s, %d bytes", m.cfg.ID, out.Kind, clipboardSize(out))
	writeJSON(w, out)
}

// putClipboard: PUT /api/v1/machines/{mid}/clipboard {"text"} or {"image": {"mime", "data"}} → 204.
func (d *Devices) putClipboard(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !d.clipboardReady(w, r, m) { // before the body is read: a switch that is off costs nothing
		return
	}
	var in proto.ClipboardContent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, proto.ClipboardBodyMost)).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErrCode(w, http.StatusRequestEntityTooLarge, proto.CodeTooLarge, i18n.Err("devices-clipboard.too_large"))
			return
		}
		writeErrCode(w, http.StatusBadRequest, "bad_request", i18n.Err("devices-clipboard.need_content"))
		return
	}
	if (in.Text == nil) == (in.Image == nil) || in.Image != nil && len(in.Image.Data) == 0 {
		writeErrCode(w, http.StatusBadRequest, "bad_request", i18n.Err("devices-clipboard.need_content"))
		return
	}
	in.Kind = ""
	ctx, cancel := context.WithTimeout(r.Context(), clipboardCallTimeout)
	defer cancel()
	if err := m.call(ctx, "clipboard.set", in, nil); err != nil {
		d.writeClipboardCallErr(w, r, m, "set", err)
		return
	}
	kind := proto.ClipboardKindText
	if in.Image != nil {
		kind = proto.ClipboardKindImage
	}
	d.logf("[%s] clipboard set from the app: %s, %d bytes", m.cfg.ID, kind, clipboardSize(in))
	w.WriteHeader(http.StatusNoContent)
}

// putClipboardSettings: PUT /api/v1/machines/{mid}/clipboard/settings {"enabled": bool} → the machine's clipboard
// state. Stored while the machine is offline too.
func (d *Devices) putClipboardSettings(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !clipboardRoleOK(w, r) {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || in.Enabled == nil {
		writeErrCode(w, http.StatusBadRequest, "bad_request", i18n.Err("devices-clipboard.need_enabled"))
		return
	}
	if err := d.setClipboardOn(r.Context(), m.cfg.ID, *in.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	d.logf("[%s] clipboard sharing turned %s", m.cfg.ID, map[bool]string{true: "on", false: "off"}[*in.Enabled])
	d.changed()
	writeJSON(w, d.clipboardOf(r, m))
}

// getClipboardSettings: GET /api/v1/machines/{mid}/clipboard/settings → the machine's clipboard state.
func (d *Devices) getClipboardSettings(w http.ResponseWriter, r *http.Request, m *Machine) {
	writeJSON(w, d.clipboardOf(r, m))
}

func clipboardSize(c proto.ClipboardContent) int {
	switch {
	case c.Text != nil:
		return len(*c.Text)
	case c.Image != nil:
		return len(c.Image.Data)
	}
	return 0
}
