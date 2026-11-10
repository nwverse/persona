package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The hub relays assistantwire.Routes (it does not run this package): they are exactly the ones the assistant serves.
func TestRoutesAreTheWireOnes(t *testing.T) {
	got, want := newTestService(t, newFakeHub()).Routes(), slices.Concat(assistantwire.Routes, assistantwire.InboxRoutes)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the assistant serves %v\nthe hub relays %v", got, want)
	}
}

// The hub's coded errors get their kind back over the bus (the assistant's code checks errors.Is), with the hub's
// message; others stay as they came.
func TestBusErrorKinds(t *testing.T) {
	for code, is := range map[string]func(error) bool{
		assistantwire.CodeNoDevice:       func(err error) bool { return errors.Is(err, ErrNoDevice) },
		assistantwire.CodeMachineOffline: func(err error) bool { return errors.Is(err, apierr.ErrOffline) },
		assistantwire.CodeNodeClosed:     func(err error) bool { return errors.Is(err, rpc.ErrClosed) },
		assistantwire.CodeStaleSnapshot:  func(err error) bool { return errors.Is(err, ErrStaleSnapshot) },
	} {
		err := busError(&rpc.Error{Code: code, Message: "说明"})
		if !is(err) || err.Error() != "说明" {
			t.Errorf("%s: %v (%T)", code, err, err)
		}
	}
	var re *rpc.Error
	if err := busError(&rpc.Error{Code: "permission_denied", Message: "不行"}); !errors.As(err, &re) || re.Code != "permission_denied" {
		t.Errorf("a node's own refusal: %v", err)
	}
	if err := busError(context.DeadlineExceeded); err != context.DeadlineExceeded {
		t.Errorf("not coded: %v", err)
	}
	if busError(nil) != nil {
		t.Error("nil")
	}
}

// The whole snapshot a new connection reports reaches the hub: it is sent once the connection counts as up, not
// while the hello's answer is still being kept (bus.Client calls OnHello before the connection is up, and a snapshot
// sent then was refused as offline, so the hub showed nothing of the assistant until its next change).
func TestBusSnapshotWaitsForTheConnection(t *testing.T) {
	snapshots := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn := bus.NewConn(r.Context(), ws)
		p := rpc.New("h", conn)
		p.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			switch method {
			case assistantwire.MethodHello:
				return map[string]any{}, nil
			case assistantwire.MethodSnapshot:
				snapshots <- struct{}{}
				return map[string]any{}, nil
			}
			return nil, &rpc.Error{Code: "not_found", Message: method}
		}
		_ = p.Serve(r.Context(), conn)
	}))
	defer srv.Close()

	b := NewBusHub(BusOptions{Hub: srv.URL, Token: "jb_x", Logf: testLogf(t)})
	// Widen the gap between the hello's answer and the connection counting as up, so a snapshot sent in it fails.
	onHello := b.client.OnHello
	b.client.OnHello = func(res json.RawMessage) error {
		err := onHello(res)
		time.Sleep(100 * time.Millisecond)
		return err
	}
	st := &memStore{}
	svc := New(Options{Hub: b, Store: func() Store { return st }, Logf: testLogf(t)})
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- b.Serve(ctx, svc) }()
	defer func() {
		cancel()
		<-served
	}()

	select {
	case <-snapshots:
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot reached the hub after connecting")
	}
}

// The hub's own manager offers the hub no compression (bus.Client.Uncompressed): its child, and the built-in manager's
// service dialing the hub's local address. A manager elsewhere, or one made in the app, compresses as before (a
// manager's own service is DeployService too, manager/service.go).
func TestBusHubUncompressedOnTheHubsMachine(t *testing.T) {
	for _, c := range []struct {
		deploy, id, hub string
		want            bool
	}{
		{"child", "", "http://127.0.0.1:18660", true},
		{"child", "", "http://198.51.100.5:18660", true},
		{DeployService, "", "http://127.0.0.1:18660", true},
		{DeployService, assistantwire.AssistantID, "http://localhost:18660", true},
		{DeployService, assistantwire.AssistantID, "http://[::1]:18660", true},
		{DeployService, assistantwire.AssistantID, "https://203.0.113.7:18661", false},
		{DeployService, "m_1", "http://127.0.0.1:18660", false},
		{"external", "", "http://127.0.0.1:18660", false},
		{"docker", "", "http://127.0.0.1:18660", false},
	} {
		b := NewBusHub(BusOptions{ID: c.id, Deploy: c.deploy, Hub: c.hub, Token: "t"})
		if b.client.Uncompressed != c.want {
			t.Errorf("%+v: Uncompressed %v", c, b.client.Uncompressed)
		}
	}
}
