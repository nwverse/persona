package devices

// The file transfer switch (#3065, docs/protocol/file-transfer.md): each machine has one, off until the user turns it
// on. While it is off nothing is sent to the machine to keep (dest=inbox, which the hub checks against this switch as
// the manager reports it on the machine) and no transfer starts from it or to it. Chat attachments and file previews /
// downloads never depend on it. The manager keeps the switches in settings (machine_file_transfer) and answers
// GET / PUT /api/v1/machines/{mid}/file-transfer/settings.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// fileTransferKey is the settings key holding map[machineID]bool: the machines the user turned file transfer on for.
const fileTransferKey = "machine_file_transfer"

// fileTransferSwitches caches fileTransferKey, loaded on first use (machine lists read it on every report).
type fileTransferSwitches struct {
	mu       sync.Mutex
	loadedAt time.Time // zero: never loaded
	failedAt time.Time
	on       map[string]bool
}

// fileTransferFresh is how long a load of the switches is trusted: another manager process on the same settings (a
// blue-green switch) may have turned one off, and an off switch must not stay on here for long.
const fileTransferFresh = 10 * time.Second

// fileTransferOn is whether the user turned file transfer on for machine; off when never set, and off while the
// settings cannot be read (it fails closed).
func (d *Devices) fileTransferOn(machine string) bool {
	c := &d.fileTransfer
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.loadedAt) >= fileTransferFresh && (c.failedAt.IsZero() || time.Since(c.failedAt) >= engineRetry) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.loadFileTransferLocked(ctx); err != nil {
			d.logf("file transfer switches: %v (retried in %s)", err, engineRetry)
			c.failedAt, c.on = time.Now(), nil // fails closed
		}
	}
	return c.on[machine]
}

func (d *Devices) loadFileTransferLocked(ctx context.Context) error {
	c := &d.fileTransfer
	on := map[string]bool{}
	if d.opts.Store != nil {
		if _, err := d.opts.Store.Setting(ctx, fileTransferKey, &on); err != nil {
			return err
		}
	}
	c.on, c.loadedAt, c.failedAt = on, time.Now(), time.Time{}
	return nil
}

// setFileTransferOn stores the switch of machine: read, changed and written under the lock, so two switches flipped
// at once both stay.
func (d *Devices) setFileTransferOn(ctx context.Context, machine string, on bool) error {
	if d.opts.Store == nil {
		return i18n.Err("devices-api.no_store")
	}
	c := &d.fileTransfer
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := d.loadFileTransferLocked(ctx); err != nil {
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
	if err := d.opts.Store.PutSetting(ctx, fileTransferKey, next); err != nil {
		return err
	}
	c.on = next
	return nil
}

// fileTransferState is proto.Machine.FileTransfer: the switch, and the receive folder while it is on.
func (m *Machine) fileTransferState(enabled bool) *proto.MachineFileTransfer {
	st := &proto.MachineFileTransfer{Enabled: enabled}
	if enabled {
		st.Inbox = strings.TrimSpace(m.cfg.InboxDir)
		if st.Inbox == "" {
			st.Inbox = proto.DefaultInboxDir
		}
	}
	return st
}

// writeFileTransferOff answers 403 file_transfer_disabled for machine m.
func writeFileTransferOff(w http.ResponseWriter, r *http.Request, m *Machine) {
	body := map[string]any{"error": i18n.T(i18n.FromRequest(r), "devices-file_transfer.disabled", i18n.Args{"name": m.cfg.Name}),
		"code": proto.CodeFileTransferDisabled, "machine": m.cfg.ID, "retryable": false}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(body)
}

// getFileTransferSettings: GET /api/v1/machines/{mid}/file-transfer/settings → {enabled, inbox?}.
func (d *Devices) getFileTransferSettings(w http.ResponseWriter, r *http.Request, m *Machine) {
	writeJSON(w, m.fileTransferState(d.fileTransferOn(m.cfg.ID)))
}

// putFileTransferSettings: PUT /api/v1/machines/{mid}/file-transfer/settings {"enabled": bool} → {enabled, inbox?}.
// Only a full device turns it on or off; stored while the machine is offline too.
func (d *Devices) putFileTransferSettings(w http.ResponseWriter, r *http.Request, m *Machine) {
	if !fullRole(r) {
		writeErrCode(w, http.StatusForbidden, proto.CodeReadOnly, i18n.Err("devices-file_transfer.read_only"))
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || in.Enabled == nil {
		writeErrCode(w, http.StatusBadRequest, "bad_request", i18n.Err("devices-file_transfer.need_enabled"))
		return
	}
	if err := d.setFileTransferOn(r.Context(), m.cfg.ID, *in.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	d.logf("[%s] file transfer turned %s", m.cfg.ID, map[bool]string{true: "on", false: "off"}[*in.Enabled])
	d.changed() // the machine list carries the switch, and the hub checks dest=inbox uploads against it
	writeJSON(w, m.fileTransferState(*in.Enabled))
}
