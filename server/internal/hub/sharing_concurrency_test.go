package hub

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestShareManagementAdmissionReservesPoolCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, h := setupTestDB(t), newTestHub(t)
	h.store = store
	if err := h.initHubID(ctx); err != nil {
		t.Fatal(err)
	}
	issuer := h.hubID()
	subject, err := store.CreateOwnerSubject(ctx, issuer, "Synthetic owner", "synthetic-admin", "Synthetic admission test")
	if err != nil {
		t.Fatal(err)
	}
	token, credential := rand.Text(), rand.Text()
	if err = store.CreateDeviceToken(ctx, db.DeviceToken{ID: credential, Name: "Synthetic device", Role: roleFull, Kind: "web"}, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnrollOwner(ctx, issuer, credential, subject.Subject, "synthetic-admin", "Synthetic enrollment", 0); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveBusIdentity(ctx, db.BusIdentity{ID: devicesOwner, Role: "manager", Name: "Synthetic manager"}, "synthetic-manager-hash"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 8), make(chan struct{})
	hubIn, managerOut := io.Pipe()
	managerIn, hubOut := io.Pipe()
	hp, mp := rpc.New("synthetic-hub", hubOut), rpc.New("synthetic-manager", managerOut)
	mp.OnRequest = func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return assistantwire.HTTPResponse{Status: 200, Body: []byte(`{"shares":[]}`)}, nil
	}
	go hp.Serve(ctx, hubIn)
	go mp.Serve(ctx, managerIn)
	defer hp.Close(io.EOF)
	defer mp.Close(io.EOF)
	defer hubIn.Close()
	defer managerIn.Close()
	defer hubOut.Close()
	defer managerOut.Close()
	h.bus.current = map[string]*busConn{devicesOwner: {peer: hp, presence: busPresence{Capabilities: []string{devicewire.SharingCapability}}}}
	results := make(chan int, 8)
	call := func() {
		r := httptest.NewRequest("GET", "http://app.example.test/api/v1/agent-shares", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set(ManagerHeader, devicesOwner)
		w := httptest.NewRecorder()
		h.forwardShareManagement(w, r)
		results <- w.Code
	}
	go call()
	go call()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("admitted relay did not reach manager")
		}
	}
	queryCtx, stop := context.WithTimeout(ctx, time.Second)
	_, err = store.OwnerSubjects(queryCtx, issuer)
	stop()
	if err != nil {
		t.Fatalf("two held relays exhausted the hub pool: %v", err)
	}
	for i := 0; i < 6; i++ {
		go call()
	}
	for i := 0; i < 6; i++ {
		select {
		case code := <-results:
			if code != 429 {
				t.Fatalf("busy waiter returned %d", code)
			}
		case <-ctx.Done():
			t.Fatal("busy waiters did not terminate")
		}
	}
	if len(entered) != 0 || len(shareManagementSlots) != 2 {
		t.Fatal("more than two relays admitted")
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case code := <-results:
			if code != 200 {
				t.Fatalf("admitted relay returned %d", code)
			}
		case <-ctx.Done():
			t.Fatal("released relay did not finish")
		}
	}
	if len(shareManagementSlots) != 0 {
		t.Fatal("management slot leaked")
	}
}
