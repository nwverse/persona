package devices

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

func sharingConcurrencyFixture(t *testing.T) (*Devices, *db.DB, sealedShare, string, devicewire.Caller) {
	t.Helper()
	dsn := os.Getenv(dbtest.Env)
	if dsn == "" {
		t.Skip("test database is required")
	}
	store, err := db.Open(context.Background(), dsn, db.AllSets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	owner := db.SessionShareOwnership{Machine: "synthetic-concurrent-machine-" + t.Name(), SessionID: "synthetic-concurrent-session", Issuer: "synthetic-concurrent-issuer-" + t.Name(), Subject: "70000000-0000-4000-8000-000000000001", Incarnation: "synthetic-concurrent-incarnation", Generation: "synthetic-concurrent-generation"}
	owner, err = store.AttributeSession(context.Background(), owner, "synthetic-admin", "Synthetic concurrency test", "synthetic-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	a := devicewire.ShareAuthority{Issuer: owner.Issuer, Machine: owner.Machine, Generation: owner.Generation}
	handle := []byte("synthetic-handle")
	raw := append(append([]byte(nil), handle...), make([]byte, 32)...)
	if _, err := rand.Read(raw[16:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	d := New(Options{Store: store})
	d.cfg = devicewire.Config{ShareAuthorities: map[string]devicewire.ShareAuthority{owner.Machine: a}, SharePublicOrigin: "http://share.example.test:18661", ShareRoutingHandle: handle}
	s := sealedShare{Owner: owner, Authority: a, Excluded: map[string]bool{}, Meta: shareMetadata{ID: "synthetic-share-" + t.Name(), Revision: 1, ContentVersion: 1, Status: "active", Future: true, Tools: true, Created: time.Now(), Expires: time.Now().Add(time.Hour), Publication: "live"}}
	row := db.ShareRow{ID: s.Meta.ID, Issuer: owner.Issuer, Subject: owner.Subject, Machine: owner.Machine, Session: owner.SessionID, Hash: shareHash([]byte(token))}
	if err := store.ShareTransaction(context.Background(), func(tx *db.ShareTx) error { return saveSealed(tx, row, s) }); err != nil {
		t.Fatal(err)
	}
	caller := devicewire.Caller{Kind: "device", Issuer: owner.Issuer, OwnerSubject: owner.Subject, CredentialID: "synthetic-concurrent-device", BindingRevision: 1, AdmissionID: "synthetic-concurrent-admission"}
	return d, store, s, token, caller
}

func TestShareSlowNodeDoesNotBlockReadOrRevoke(t *testing.T) {
	d, store, s, token, caller := sharingConcurrencyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	managerConn, nodeConn := net.Pipe()
	node, manager := rpc.New("synthetic-node", nodeConn), rpc.New("synthetic-manager", managerConn)
	node.OnRequest = func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return transcript.ProjectShareBytes(ctx, []byte("{\"type\":\"assistant\",\"uuid\":\"new\",\"message\":{\"role\":\"assistant\",\"content\":\"Synthetic future answer\"}}\n{\"type\":\"system\",\"subtype\":\"turn_duration\",\"uuid\":\"end\",\"parentUuid\":\"new\"}\n"), "synthetic-source", s.Owner.Incarnation, nil)
	}
	go node.Serve(ctx, nodeConn)
	go manager.Serve(ctx, managerConn)
	t.Cleanup(func() { node.Close(nil); manager.Close(nil) })
	d.byID[s.Owner.Machine] = &Machine{cfg: MachineConfig{ID: s.Owner.Machine}, d: d, peer: manager, info: proto.NodeInfo{Capabilities: []string{devicewire.ProjectionCapability}}}
	ingested := make(chan error, 1)
	go func() { ingested <- d.ingestShare(ctx, s.Meta.ID) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("node projection never started")
	}
	handler := d.Handler(http.NotFoundHandler())
	for _, operation := range []struct {
		method, path, body string
		who                devicewire.Caller
	}{
		{"GET", "/api/public/agent-shares/" + token, "", devicewire.Caller{Kind: "anonymous"}},
		{"DELETE", "/api/v1/agent-shares/" + s.Meta.ID, `{"expected_revision":1}`, caller},
	} {
		requestCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		r := httptest.NewRequest(operation.method, operation.path, strings.NewReader(operation.body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(devicewire.WithCaller(requestCtx, operation.who))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		stop()
		if w.Code != 200 {
			t.Fatalf("%s blocked behind the node RPC: %d", operation.method, w.Code)
		}
	}
	close(release)
	select {
	case err := <-ingested:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("ingestion did not finish after releasing the node")
	}
	if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		_, current, err := loadSealed(tx, s.Meta.ID)
		if err == nil && (current.Meta.Status != "revoked" || current.Meta.Revision != 2 || len(current.Messages) != 0) {
			t.Error("stale projection undid revocation or appended content")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestShareFutureReplayAndOfflineNarrowingNeedNoNode(t *testing.T) {
	d, store, s, _, caller := sharingConcurrencyFixture(t)
	ctx := devicewire.WithCaller(context.Background(), caller)
	req := shareCreateRequest{RequestID: "70000000-0000-4000-8000-000000000002", Machine: s.Owner.Machine, Session: s.Owner.SessionID, Future: true, Selection: devicewire.ShareSelection{Mode: "all"}, Days: 3}
	if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		return tx.PutCreation(shareActor(caller), req.RequestID, shareValueHash(req), s.Meta.ID)
	}); err != nil {
		t.Fatal(err)
	}
	handler := d.Handler(http.NotFoundHandler())
	body, _ := json.Marshal(req)
	r := httptest.NewRequest("POST", "/api/v1/agent-shares", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "creation_already_committed") || strings.Contains(w.Body.String(), "public_url") {
		t.Fatalf("committed future request contacted offline node: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("PATCH", "/api/v1/agent-shares/"+s.Meta.ID, strings.NewReader(`{"expected_revision":1,"include_future":true,"show_tools":false}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("unchanged future prevented offline narrowing: %d %s", w.Code, w.Body.String())
	}
}
