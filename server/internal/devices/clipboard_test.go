package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// clipNode is a machine whose node keeps a clipboard in memory (support says what node.info said about it).
type clipNode struct {
	mu    sync.Mutex
	board proto.ClipboardContent
	calls []string
	// fail, when set, is what the next clipboard call answers.
	fail error
}

func clipMachine(t *testing.T, h *Devices, id string, support *proto.ClipboardSupport) (*Machine, *clipNode) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
	n := &clipNode{board: proto.ClipboardContent{Kind: proto.ClipboardKindEmpty}}
	nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.calls = append(n.calls, method)
		if n.fail != nil {
			err := n.fail
			n.fail = nil
			return nil, err
		}
		switch method {
		case "clipboard.get":
			return n.board, nil
		case "clipboard.set":
			var in proto.ClipboardContent
			_ = json.Unmarshal(params, &in)
			in.Kind = proto.ClipboardKindText
			if in.Image != nil {
				in.Kind = proto.ClipboardKindImage
			}
			n.board = in
			return map[string]any{}, nil
		case "node.configure":
			return map[string]any{}, nil
		}
		return nil, &rpc.Error{Message: "unknown method " + method}
	}
	go func() { _ = hubSide.Serve(ctx, n2hR) }()
	go func() { _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = h2nW.Close(); _ = n2hW.Close() })
	m := newMachine(h, MachineConfig{ID: id, Name: id})
	m.peer, m.online = hubSide, true
	m.info = proto.NodeInfo{OS: "darwin", Version: "test", Protocol: nodeproto.Version, Capabilities: nodeproto.Announced, Clipboard: support}
	h.addEntry(m)
	return m, n
}

func clipCall(t *testing.T, mux http.Handler, method, path, body, role string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req.Header.Set(assistantwire.RoleHeader, role)
	}
	mux.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// Off until the user turns it on; the switch is kept per machine and shows in the machine list.
func TestClipboardSwitch(t *testing.T) {
	h := newTestDevices(t)
	m, n := clipMachine(t, h, "mac", &proto.ClipboardSupport{Supported: true})
	clipMachine(t, h, "other", &proto.ClipboardSupport{Supported: true})
	mux := h.Handler(http.NotFoundHandler())

	if c := m.snapshot().Clipboard; c == nil || !c.Supported || c.Enabled {
		t.Fatalf("default: %+v", c)
	}
	code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard", "", "full")
	if code != http.StatusForbidden || out["code"] != proto.CodeClipboardDisabled {
		t.Fatalf("off: %d %v", code, out)
	}
	if code, _ := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard", `{"text":"x"}`, "full"); code != http.StatusForbidden {
		t.Fatalf("set while off: %d", code)
	}
	if len(n.calls) != 0 {
		t.Fatalf("the node was asked while off: %v", n.calls)
	}
	if code, _ := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard/settings", `{}`, "full"); code != http.StatusBadRequest {
		t.Fatalf("no enabled: %d", code)
	}
	code, out = clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard/settings", `{"enabled":true}`, "full")
	if code != 200 || out["enabled"] != true || out["supported"] != true {
		t.Fatalf("turned on: %d %v", code, out)
	}
	if c := m.snapshot().Clipboard; !c.Enabled {
		t.Fatalf("machine list: %+v", c)
	}
	if c := h.machineByID("other").snapshot().Clipboard; c.Enabled {
		t.Fatal("another machine's switch moved")
	}
	// Kept in settings: a manager started again reads it.
	var stored map[string]bool
	if ok, err := h.opts.Store.Setting(context.Background(), clipboardKey, &stored); !ok || err != nil || !stored["mac"] {
		t.Fatalf("stored: %v %v %v", ok, err, stored)
	}
	h2 := New(Options{Hub: &fakeHost{}, Store: h.opts.Store, Logf: func(string, ...any) {}})
	if !h2.clipboardOn("mac") || h2.clipboardOn("other") {
		t.Fatal("not read back from settings")
	}
	if code, out := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard/settings", `{"enabled":false}`, "full"); code != 200 || out["enabled"] != false {
		t.Fatalf("turned off: %d %v", code, out)
	}
	if code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard/settings", "", ""); code != 200 || out["enabled"] != false {
		t.Fatalf("settings: %d %v", code, out)
	}
}

// With the switch on, text and images go both ways; concealed content comes back without it.
func TestClipboardGetSet(t *testing.T) {
	h := newTestDevices(t)
	_, n := clipMachine(t, h, "mac", &proto.ClipboardSupport{Supported: true})
	mux := h.Handler(http.NotFoundHandler())
	_ = h.setClipboardOn(context.Background(), "mac", true)

	if code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard", "", "full"); code != 200 || out["kind"] != "empty" {
		t.Fatalf("empty: %d %v", code, out)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/machines/mac/clipboard", strings.NewReader(`{"text":"从手机来的"}`)))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("set text: %d %q", rec.Code, rec.Body.String())
	}
	if code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard", "", "full"); code != 200 || out["kind"] != "text" || out["text"] != "从手机来的" {
		t.Fatalf("text: %d %v", code, out)
	}
	if code, _ := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard", `{"image":{"mime":"image/png","data":"iVBORw0KGgo="}}`, "full"); code != http.StatusNoContent {
		t.Fatalf("set image: %d", code)
	}
	code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard", "", "full")
	img, _ := out["image"].(map[string]any)
	if code != 200 || out["kind"] != "image" || img["mime"] != "image/png" || img["data"] != "iVBORw0KGgo=" {
		t.Fatalf("image: %d %v", code, out)
	}
	secret := "hunter2"
	n.mu.Lock()
	n.board = proto.ClipboardContent{Kind: proto.ClipboardKindConcealed, Text: &secret}
	n.mu.Unlock()
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/machines/mac/clipboard", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), secret) || !strings.Contains(rec.Body.String(), `"concealed"`) {
		t.Fatalf("concealed: %d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{``, `{}`, `{"text":"a","image":{"mime":"image/png","data":"iVBORw0KGgo="}}`, `{"image":{"mime":"image/png"}}`} {
		if code, _ := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard", body, "full"); code != http.StatusBadRequest {
			t.Errorf("%q: %d", body, code)
		}
	}
}

// Only full credentials: an answer-only device (the watch) and an extension's token get 403 even with the switch on.
func TestClipboardReadOnlyDevices(t *testing.T) {
	h := newTestDevices(t)
	_, n := clipMachine(t, h, "mac", &proto.ClipboardSupport{Supported: true})
	mux := h.Handler(http.NotFoundHandler())
	_ = h.setClipboardOn(context.Background(), "mac", true)
	for _, role := range []string{"answer", "extension"} {
		for _, rq := range [][2]string{{"GET", "/api/v1/machines/mac/clipboard"}, {"PUT", "/api/v1/machines/mac/clipboard"}, {"PUT", "/api/v1/machines/mac/clipboard/settings"}} {
			if code, _ := clipCall(t, mux, rq[0], rq[1], `{"text":"x","enabled":true}`, role); code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d", role, rq[0], rq[1], code)
			}
		}
	}
	if len(n.calls) != 0 {
		t.Fatalf("the node was asked: %v", n.calls)
	}
}

// A computer that cannot share (no desktop, an older node) says why: 409 with the reason, in the machine list too.
func TestClipboardUnsupported(t *testing.T) {
	h := newTestDevices(t)
	m, _ := clipMachine(t, h, "server", &proto.ClipboardSupport{Reason: proto.ClipboardReasonNoDisplay})
	mux := h.Handler(http.NotFoundHandler())
	_ = h.setClipboardOn(context.Background(), "server", true)
	if c := m.snapshot().Clipboard; c.Supported || c.ReasonCode != proto.ClipboardReasonNoDisplay {
		t.Fatalf("state: %+v", c)
	}
	code, out := clipCall(t, mux, "GET", "/api/v1/machines/server/clipboard", "", "full")
	if code != http.StatusConflict || out["code"] != proto.CodeClipboardUnsupported || out["error"] == "" {
		t.Fatalf("get: %d %v", code, out)
	}
	// An older node: no clipboard capability.
	old, _ := clipMachine(t, h, "old", nil)
	old.info.Capabilities = []string{nodeproto.Sessions}
	_ = h.setClipboardOn(context.Background(), "old", true)
	if c := old.snapshot().Clipboard; c.Supported || c.ReasonCode != proto.ClipboardReasonNodeOutdated {
		t.Fatalf("old: %+v", c)
	}
	if code, out := clipCall(t, mux, "PUT", "/api/v1/machines/old/clipboard", `{"text":"x"}`, "full"); code != http.StatusConflict || out["code"] != proto.CodeClipboardUnsupported {
		t.Fatalf("old set: %d %v", code, out)
	}
}

// What the node answers maps to the app's statuses; offline is 503; the content never reaches the log.
func TestClipboardErrors(t *testing.T) {
	var logged bytes.Buffer
	var logMu sync.Mutex
	h := newTestDevices(t)
	h.logf = func(f string, a ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		logged.WriteString(strings.TrimSpace(fmt.Sprintf(f, a...)) + "\n")
	}
	m, n := clipMachine(t, h, "mac", &proto.ClipboardSupport{Supported: true})
	mux := h.Handler(http.NotFoundHandler())
	_ = h.setClipboardOn(context.Background(), "mac", true)
	for _, tc := range []struct {
		err  error
		code int
		app  string
	}{
		{&rpc.Error{Code: proto.CodeTooLarge, Message: "the image is too large"}, http.StatusRequestEntityTooLarge, proto.CodeTooLarge},
		{&rpc.Error{Code: proto.CodeClipboardBadImage, Message: "bad"}, http.StatusBadRequest, proto.CodeClipboardBadImage},
		{&rpc.Error{Code: proto.CodeClipboardFailed, Message: "read the clipboard: exit status 1"}, http.StatusBadGateway, proto.CodeClipboardFailed},
		{&rpc.Error{Code: proto.CodeClipboardUnsupported, Message: proto.ClipboardReasonNoSession}, http.StatusConflict, proto.CodeClipboardUnsupported},
		{&rpc.Error{Message: "unknown method clipboard.set"}, http.StatusConflict, proto.CodeClipboardUnsupported},
	} {
		n.mu.Lock()
		n.fail = tc.err
		n.mu.Unlock()
		code, out := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard", `{"text":"secret-value-123"}`, "full")
		if code != tc.code || out["code"] != tc.app {
			t.Errorf("%v: %d %v", tc.err, code, out)
		}
	}
	if code, _ := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard", `{"text":"secret-value-123"}`, "full"); code != http.StatusNoContent {
		t.Fatalf("set: %d", code)
	}
	if code, _ := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard", "", "full"); code != 200 {
		t.Fatalf("get: %d", code)
	}
	logMu.Lock()
	if strings.Contains(logged.String(), "secret-value-123") || !strings.Contains(logged.String(), "clipboard set from the app: text, 16 bytes") {
		t.Fatalf("log: %s", logged.String())
	}
	logMu.Unlock()
	// Too large a body is refused before the node is asked.
	big := `{"text":"` + strings.Repeat("x", proto.ClipboardBodyMost) + `"}`
	if code, out := clipCall(t, mux, "PUT", "/api/v1/machines/mac/clipboard", big, "full"); code != http.StatusRequestEntityTooLarge || out["code"] != proto.CodeTooLarge {
		t.Fatalf("big: %d %v", code, out)
	}
	m.mu.Lock()
	m.peer, m.online = nil, false
	m.mu.Unlock()
	if code, _ := clipCall(t, mux, "GET", "/api/v1/machines/mac/clipboard", "", "full"); code != http.StatusServiceUnavailable {
		t.Fatalf("offline: %d", code)
	}
}

// The hub relays the clipboard's routes to the manager.
func TestClipboardRoutesRelayed(t *testing.T) {
	for _, want := range []string{"GET /api/v1/machines/{mid}/clipboard", "PUT /api/v1/machines/{mid}/clipboard",
		"GET /api/v1/machines/{mid}/clipboard/settings", "PUT /api/v1/machines/{mid}/clipboard/settings"} {
		found := false
		for _, r := range devicewire.Routes {
			found = found || r == want
		}
		if !found {
			t.Errorf("%s is not relayed", want)
		}
	}
}
