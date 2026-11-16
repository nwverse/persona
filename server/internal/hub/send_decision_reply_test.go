package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// The normal Agent send route must tell the manager even before the node finishes delivery.
// Waiting for a later turn-start hook leaves a pending decision's default free to run during startup.
func TestAgentSendTellsManagerTheOwnerSpoke(t *testing.T) {
	h := newTestHub(t)
	h.store = newSentStore()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	pipeNode(t, h, "box", func(ctx context.Context) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	m := h.byID["box"]
	editSessions(m, func([]proto.Session) []proto.Session {
		return []proto.Session{{SessionID: "s1", Agent: "codex", Status: proto.StatusWorking}}
	})
	h.bus.idents = &memIdents{}
	mux := http.NewServeMux()
	h.busRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	manager := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, func(string, json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})
	spoke := make(chan assistantwire.SessionRef, 1)
	manager.mu.Lock()
	manager.onEvent = func(method string, raw json.RawMessage) {
		if method == assistantwire.EventUserSpoke {
			var r assistantwire.SessionRef
			if json.Unmarshal(raw, &r) == nil {
				spoke <- r
			}
		}
	}
	manager.mu.Unlock()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, sendPath, strings.NewReader(`{"text":"Use the other option; I will confirm the details.","client_msg_id":"decision-reply-1"}`))
	r.SetPathValue("sid", "s1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.sendMessage(rec, r, m)
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case got := <-spoke:
		if got.MachineID != "box" || got.SessionID != "s1" {
			t.Fatalf("wrong owner-message target: %+v", got)
		}
	case <-timer.C:
		t.Fatal("the Agent send did not tell the manager the owner spoke")
	}
	unblock()
	select {
	case <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
		}
	case <-timer.C:
		t.Fatal("the Agent send did not finish")
	}
}
