package devices

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

// File transfer is off for every machine until the user turns it on; the switch is kept in settings under
// machine_file_transfer, shown on the machine (file_transfer) and read back by GET.
func TestFileTransferSwitchDefaultsOffAndIsStored(t *testing.T) {
	d := newTestDevices(t)
	m := connectedMachine(t, d, "box", &testNode{})
	mux := d.Handler(http.NotFoundHandler())
	get := func() proto.MachineFileTransfer {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/machines/box/file-transfer/settings", nil))
		var st proto.MachineFileTransfer
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil {
			t.Fatalf("GET %d %s", rec.Code, rec.Body)
		}
		return st
	}
	if st := get(); st.Enabled || st.Inbox != "" {
		t.Fatalf("default %+v", st)
	}
	if ft := m.snapshot().FileTransfer; ft == nil || ft.Enabled {
		t.Fatalf("machine %+v", ft)
	}
	put := func(body string, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/v1/machines/box/file-transfer/settings", strings.NewReader(body))
		if role != "" {
			req.Header.Set(assistantwire.RoleHeader, role)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := put(`{}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("no enabled: %d", rec.Code)
	}
	if rec := put(`{"enabled":true}`, "answer"); rec.Code != http.StatusForbidden {
		t.Fatalf("answer-only device: %d", rec.Code)
	}
	if rec := put(`{"enabled":true}`, "full"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"inbox":"~/Downloads/Persona"`) {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}
	if st := get(); !st.Enabled || st.Inbox != proto.DefaultInboxDir {
		t.Fatalf("after on %+v", st)
	}
	if ft := m.snapshot().FileTransfer; ft == nil || !ft.Enabled {
		t.Fatalf("machine after on %+v", ft)
	}
	var stored map[string]bool
	if ok, err := d.opts.Store.Setting(t.Context(), fileTransferKey, &stored); !ok || err != nil || !stored["box"] {
		t.Fatalf("stored %v %v %v", ok, err, stored)
	}
	m.cfg.InboxDir = "~/Inbox"
	if st := get(); st.Inbox != "~/Inbox" {
		t.Fatalf("configured inbox %+v", st)
	}
	if rec := put(`{"enabled":false}`, ""); rec.Code != 200 {
		t.Fatalf("off: %d", rec.Code)
	}
	if st := get(); st.Enabled {
		t.Fatalf("after off %+v", st)
	}
	stored = nil
	if ok, _ := d.opts.Store.Setting(t.Context(), fileTransferKey, &stored); !ok || stored["box"] {
		t.Fatalf("stored after off %v", stored)
	}
}

// Settings that cannot be read are off.
func TestFileTransferSwitchFailsClosed(t *testing.T) {
	d := newTestDevices(t)
	switchOn(t, d, "box")
	store := d.opts.Store.(*memStore)
	store.mu.Lock()
	store.fail = errors.New("database down")
	store.mu.Unlock()
	d.fileTransfer.mu.Lock()
	d.fileTransfer.loadedAt = time.Time{} // as when the cache is stale
	d.fileTransfer.mu.Unlock()
	if d.fileTransferOn("box") {
		t.Fatal("on while the settings cannot be read")
	}
}

// A transfer needs the switch on at both ends: either one off is 403 file_transfer_disabled naming it, and neither
// node is asked anything.
func TestTransferNeedsTheSwitchAtBothEnds(t *testing.T) {
	for _, off := range []string{"a", "b"} {
		src, dst := &sourceNode{data: []byte("x")}, &targetNode{}
		d, _, _, _, mux := transferDevices(t, src, dst)
		if err := d.setFileTransferOn(t.Context(), off, false); err != nil {
			t.Fatal(err)
		}
		rec := postTransfer(t, mux, transferBody)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"`+proto.CodeFileTransferDisabled+`"`) ||
			!strings.Contains(rec.Body.String(), `"machine":"`+off+`"`) {
			t.Fatalf("%s off: %d %s", off, rec.Code, rec.Body)
		}
		src.mu.Lock()
		asked := len(src.requests)
		src.mu.Unlock()
		if calls, _, _ := dst.state(); asked != 0 || len(calls) != 0 {
			t.Fatalf("%s off: the nodes were asked: %d %v", off, asked, calls)
		}
	}
}

// Turning the switch off while a transfer runs stops it at the next chunk, and the target's upload is aborted.
func TestTransferStopsWhenTheSwitchTurnsOff(t *testing.T) {
	data := bytes.Repeat([]byte{3}, proto.FileDownloadChunkBytes*4)
	src := &sourceNode{data: data}
	dst := &targetNode{}
	var d *Devices
	dst.onChunk = func(n int) error {
		if n == 0 {
			if err := d.setFileTransferOn(t.Context(), "b", false); err != nil {
				return err
			}
		}
		return nil
	}
	d, _, _, _, mux := transferDevices(t, src, dst)
	end := waitTransfer(t, d, mux, started(t, postTransfer(t, mux, transferBody)).ID)
	if end.State != proto.TransferFailed || end.Code != proto.CodeFileTransferDisabled {
		t.Fatalf("end %+v", end)
	}
	if calls, _, aborted := dst.state(); !aborted || slices.Contains(calls, "file.upload.end") {
		t.Fatalf("aborted %v, calls %v", aborted, calls)
	}
}

func TestFileTransferSettingsAreRelayed(t *testing.T) {
	for _, want := range []string{"GET /api/v1/machines/{mid}/file-transfer/settings", "PUT /api/v1/machines/{mid}/file-transfer/settings"} {
		if !slices.Contains(devicewire.Routes, want) {
			t.Errorf("%s is not relayed by the hub", want)
		}
	}
}
