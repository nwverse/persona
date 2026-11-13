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
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }
func TestSharePreparedHTTPContract(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv(dbtest.Env)
	if dsn == "" {
		t.Skip("test database is required")
	}
	store, err := db.Open(ctx, dsn, db.AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := db.SessionShareOwnership{Machine: "synthetic-share-machine", SessionID: "synthetic-share-session", Issuer: "synthetic-share-authority", Subject: "10000000-0000-4000-8000-000000000001", Incarnation: "synthetic-incarnation", Generation: "synthetic-generation"}
	owner, err = store.AttributeSession(ctx, owner, "synthetic-admin", "Synthetic acceptance", "synthetic-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	a := devicewire.ShareAuthority{Issuer: owner.Issuer, Machine: owner.Machine, Generation: owner.Generation}
	handle := make([]byte, 16)
	if _, err = rand.Read(handle); err != nil {
		t.Fatal(err)
	}
	d := New(Options{Store: store})
	d.cfg = devicewire.Config{ShareAuthorities: map[string]devicewire.ShareAuthority{owner.Machine: a}, SharePublicOrigin: "http://share.example.test:18661", ShareRoutingHandle: handle}
	caller := devicewire.Caller{Kind: "device", Issuer: owner.Issuer, OwnerSubject: owner.Subject, CredentialID: "synthetic-device-one", BindingRevision: 1, AdmissionID: "synthetic-admission"}
	snap := shareSnapshot{Machine: owner.Machine, Session: owner.SessionID, Owner: owner, Authority: a, Version: "synthetic-snapshot-version", Expires: time.Now().Add(5 * time.Minute), Projection: devicewire.ShareProjection{Complete: true, Messages: []devicewire.ShareProjectionMessage{{Identity: "synthetic-human", Message: devicewire.PublicShareMessage{ID: "selection-one", Role: "user", Text: "Synthetic input"}}, {Identity: "synthetic-turn", Message: devicewire.PublicShareMessage{ID: "selection-two", Role: "assistant", Text: "Synthetic answer", Execution: []devicewire.ShareExecution{{Kind: "tool", ToolID: "native-tool", Name: "command", Arguments: "Synthetic argument", Results: []devicewire.ShareResult{{Text: "Synthetic result"}}}}}}}}}
	if err = store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		return tx.PutObject("synthetic-snapshot", "snapshot", shareActor(caller), snap.Expires, snap)
	}); err != nil {
		t.Fatal(err)
	}
	mux := d.Handler(http.NotFoundHandler())
	call := func(c devicewire.Caller, method, path string, body any) (int, map[string]any, string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(devicewire.WithCaller(r.Context(), c))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("invalid response %d", w.Code)
		}
		return w.Code, out, w.Body.String()
	}
	policy := sharePolicy{Operation: "create", SnapshotID: "synthetic-snapshot", SnapshotVersion: snap.Version, Selection: &devicewire.ShareSelection{Mode: "all"}, Days: 3}
	sessionPath := "/api/v1/sessions/" + owner.Machine + "/" + owner.SessionID
	code, preview, _ := call(caller, "POST", sessionPath+"/share-preview", policy)
	if code != 200 {
		t.Fatalf("preview: %d %v", code, preview)
	}
	if preview["requires_insecure_transport_ack"] != true {
		t.Fatal("HTTP preview did not warn")
	}
	req := shareCreateRequest{RequestID: "20000000-0000-4000-8000-000000000001", Machine: owner.Machine, Session: owner.SessionID, SnapshotID: "synthetic-snapshot", SnapshotVersion: snap.Version, Selection: devicewire.ShareSelection{Mode: "all"}, Days: 3, Ack: shareAck{Sensitive: true, Policy: 1, Preview: preview["preview_id"].(string)}}
	code, out, _ := call(caller, "POST", "/api/v1/agent-shares", req)
	if code != 409 || out["code"] != "insecure_transport_ack_required" {
		t.Fatalf("insecure ack: %d %v", code, out)
	}
	req.Ack.Insecure = true
	code, out, raw := call(caller, "POST", "/api/v1/agent-shares", req)
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	token := strings.TrimPrefix(out["public_url"].(string), d.cfg.SharePublicOrigin+"/share/")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 48 {
		t.Fatal("invalid capability entropy envelope")
	}
	meta := out["share"].(map[string]any)
	id := meta["share_id"].(string)
	if meta["include_future"] != false || meta["show_tools"] != false || meta["publication_status"] != "frozen" {
		t.Fatal("unsafe defaults")
	}
	code, out, replay := call(caller, "POST", "/api/v1/agent-shares", req)
	if code != 409 || out["code"] != "creation_already_committed" || strings.Contains(replay, token) {
		t.Fatal("creation replay leaked capability or lost receipt")
	}
	req.Days = 7
	code, out, _ = call(caller, "POST", "/api/v1/agent-shares", req)
	if code != 409 || out["code"] != "idempotency_conflict" {
		t.Fatal("idempotency mutation accepted")
	}
	anonymous := devicewire.Caller{Kind: "anonymous"}
	code, out, public := call(anonymous, "GET", "/api/public/agent-shares/"+token, nil)
	if code != 200 || strings.Contains(public, "execution") || strings.Contains(public, "native-tool") || strings.Contains(public, "selection-one") {
		t.Fatalf("public whitelist: %d", code)
	}
	other := caller
	other.OwnerSubject = "10000000-0000-4000-8000-000000000002"
	code, _, _ = call(other, "GET", "/api/v1/agent-shares/"+id, nil)
	if code != 404 {
		t.Fatal("different subject managed share")
	}
	second := caller
	second.CredentialID = "synthetic-device-two"
	code, _, listed := call(second, "GET", "/api/v1/agent-shares", nil)
	if code != 200 || !strings.Contains(listed, id) || strings.Contains(listed, token) {
		t.Fatal("same-subject list is not tokenless")
	}
	code, out, _ = call(second, "GET", "/api/v1/agent-shares/"+id+"/messages", nil)
	if code != 200 {
		t.Fatal("editor unavailable")
	}
	messages := out["messages"].([]any)
	editorID := messages[0].(map[string]any)["message_id"].(string)
	delete(d.cfg.ShareAuthorities, owner.Machine)
	code, _, _ = call(anonymous, "GET", "/api/public/agent-shares/"+token, nil)
	if code != 404 {
		t.Fatal("lost authority served sealed content")
	}
	code, _, _ = call(second, "PATCH", "/api/v1/agent-shares/"+id, sharePatchRequest{Revision: 1, Remove: []string{editorID}})
	if code != 200 {
		t.Fatal("offline narrowing failed")
	}
	d.cfg.ShareAuthorities[owner.Machine] = a
	code, out, _ = call(anonymous, "GET", "/api/public/agent-shares/"+token+"?limit=1", nil)
	if code != 200 || len(out["messages"].([]any)) != 1 {
		t.Fatal("narrowing did not seal selection")
	}
	code, _, _ = call(second, "DELETE", "/api/v1/agent-shares/"+id, map[string]any{"expected_revision": 2})
	if code != 200 {
		t.Fatal("revoke failed")
	}
	code, _, _ = call(anonymous, "GET", "/api/public/agent-shares/"+token+"?cursor=invalid", nil)
	if code != 404 {
		t.Fatal("revoked cursor exposed details")
	}
	// A second share selects only the assistant turn with tools on: execution is
	// public, native tool identity is not, and the user message stays private.
	toolsPolicy := sharePolicy{Operation: "create", SnapshotID: "synthetic-snapshot", SnapshotVersion: snap.Version, Selection: &devicewire.ShareSelection{Mode: "individual", MessageIDs: []string{"selection-two"}}, Tools: new(bool), Days: 1}
	*toolsPolicy.Tools = true
	code, preview, _ = call(caller, "POST", sessionPath+"/share-preview", toolsPolicy)
	if code != 200 {
		t.Fatalf("tools preview: %d %v", code, preview)
	}
	toolsReq := shareCreateRequest{RequestID: "20000000-0000-4000-8000-000000000002", Machine: owner.Machine, Session: owner.SessionID, SnapshotID: "synthetic-snapshot", SnapshotVersion: snap.Version, Selection: *toolsPolicy.Selection, Tools: true, Days: 1, Ack: shareAck{Sensitive: true, Policy: 1, Preview: preview["preview_id"].(string), Insecure: true}}
	code, out, _ = call(caller, "POST", "/api/v1/agent-shares", toolsReq)
	if code != 201 {
		t.Fatalf("tools create: %d %v", code, out)
	}
	toolsToken := strings.TrimPrefix(out["public_url"].(string), d.cfg.SharePublicOrigin+"/share/")
	toolsID := out["share"].(map[string]any)["share_id"].(string)
	if toolsToken == token {
		t.Fatal("capability reused")
	}
	code, _, public = call(anonymous, "GET", "/api/public/agent-shares/"+toolsToken, nil)
	if code != 200 || !strings.Contains(public, "Synthetic result") || strings.Contains(public, "native-tool") || strings.Contains(public, "Synthetic input") {
		t.Fatalf("tools-on selection: %d", code)
	}
	code, _, _ = call(caller, "PATCH", "/api/v1/agent-shares/"+toolsID, sharePatchRequest{Revision: 1, Tools: new(bool)})
	if code != 200 {
		t.Fatal("tools-off narrowing failed")
	}
	code, _, public = call(anonymous, "GET", "/api/public/agent-shares/"+toolsToken, nil)
	if code != 200 || strings.Contains(public, "Synthetic result") || strings.Contains(public, "execution") {
		t.Fatal("tools-off still exposed execution")
	}
	// Expiry: once the deadline passes the capability answers exactly like an
	// unknown or revoked one, before any cursor is considered.
	if err = store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		row, s, err := loadSealed(tx, toolsID)
		if err != nil {
			return err
		}
		s.Meta.Expires = time.Now().Add(-time.Second)
		return saveSealed(tx, row, s)
	}); err != nil {
		t.Fatal(err)
	}
	_, _, expired := call(anonymous, "GET", "/api/public/agent-shares/"+toolsToken+"?cursor=stale", nil)
	code, _, _ = call(anonymous, "GET", "/api/public/agent-shares/"+toolsToken, nil)
	if code != 404 {
		t.Fatal("expired share still readable")
	}
	_, _, revoked := call(anonymous, "GET", "/api/public/agent-shares/"+token, nil)
	bogus := make([]byte, 48)
	copy(bogus, handle)
	_, _, unknown := call(anonymous, "GET", "/api/public/agent-shares/"+base64.RawURLEncoding.EncodeToString(bogus), nil)
	_, _, malformed := call(anonymous, "GET", "/api/public/agent-shares/not-a-token", nil)
	if expired != revoked || revoked != unknown || unknown != malformed {
		t.Fatal("expired, revoked, unknown and malformed capabilities are distinguishable")
	}
	if err = store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		row, err := tx.Share(id)
		if err != nil {
			return err
		}
		if strings.Contains(string(row.Body), token) || row.Hash == token || strings.Contains(string(row.Body), raw) {
			t.Fatal("raw capability persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("synthetic prepared API: HTTP acknowledgement, exact preview, entropy, hash-only persistence, one-time create, tokenless replay, two subjects/two devices, tools-off, tools-on selection, tools-off narrowing, authority fence, offline narrowing, revoke, expiry, uniform 404 PASS")
}

func TestShareFutureWindowAndCoherentSource(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv(dbtest.Env)
	if dsn == "" {
		t.Skip("test database is required")
	}
	store, err := db.Open(ctx, dsn, db.AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := db.SessionShareOwnership{Machine: "synthetic-future-machine", SessionID: "synthetic-future-session", Issuer: "synthetic-future-authority", Subject: "40000000-0000-4000-8000-000000000001", Incarnation: "synthetic-future-incarnation", Generation: "synthetic-future-generation"}
	owner, err = store.AttributeSession(ctx, owner, "synthetic-admin", "Synthetic future acceptance", "synthetic-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	a := devicewire.ShareAuthority{Issuer: owner.Issuer, Machine: owner.Machine, Generation: owner.Generation}
	var mu sync.Mutex
	data := []byte(`{"type":"assistant","uuid":"historical","message":{"role":"assistant","content":"Historical answer"}}
{"type":"system","subtype":"turn_duration","uuid":"historical-end","parentUuid":"historical"}
`)
	managerConn, nodeConn := net.Pipe()
	node := rpc.New("synthetic-node", nodeConn)
	manager := rpc.New("synthetic-manager", managerConn)
	node.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		var req devicewire.ShareProjectionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		mu.Lock()
		captured := append([]byte(nil), data...)
		mu.Unlock()
		return transcript.ProjectShareBytes(ctx, captured, "synthetic-source", req.Incarnation, req.Previous)
	}
	go node.Serve(ctx, nodeConn)
	go manager.Serve(ctx, managerConn)
	defer node.Close(nil)
	defer manager.Close(nil)
	d := New(Options{Store: store})
	d.cfg = devicewire.Config{ShareAuthorities: map[string]devicewire.ShareAuthority{owner.Machine: a}}
	d.byID[owner.Machine] = &Machine{cfg: MachineConfig{ID: owner.Machine}, d: d, peer: manager, info: proto.NodeInfo{Capabilities: []string{devicewire.ProjectionCapability}}}
	s := sealedShare{Owner: owner, Authority: a, Excluded: map[string]bool{}, Meta: shareMetadata{ID: "synthetic-future-share", Revision: 1, ContentVersion: 1, Status: "active", Future: true, Created: time.Now(), Expires: time.Now().Add(time.Hour)}}
	if err := d.enableShareFuture(ctx, &s); err != nil {
		t.Fatal(err)
	}
	row := db.ShareRow{ID: s.Meta.ID, Issuer: owner.Issuer, Subject: owner.Subject, Machine: owner.Machine, Session: owner.SessionID, Hash: shareHash([]byte("synthetic-future-hash"))}
	persist := func() {
		t.Helper()
		if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error { return saveSealed(tx, row, s) }); err != nil {
			t.Fatal(err)
		}
	}
	persist()
	read := func() {
		t.Helper()
		if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error { var err error; row, s, err = loadSealed(tx, row.ID); return err }); err != nil {
			t.Fatal(err)
		}
	}
	appendData := func(text string) { mu.Lock(); data = append(data, []byte(text)...); mu.Unlock() }
	appendData(`{"type":"assistant","uuid":"new-turn","message":{"role":"assistant","content":"Pending future answer"}}` + "\n")
	if err := d.ingestShare(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	read()
	if len(s.Messages) != 0 {
		t.Fatal("unfinished answer published")
	}
	appendData(`{"type":"system","subtype":"turn_duration","uuid":"new-end","parentUuid":"new-turn"}` + "\n")
	if err := d.ingestShare(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	read()
	if len(s.Messages) != 1 || s.Messages[0].Message.Text != "Pending future answer" {
		t.Fatal("future pending answer lost first-seen eligibility")
	}
	appendData(`{"type":"assistant","uuid":"old-partial","message":{"role":"assistant","content":"Old partial`)
	s.Meta.Future = false
	s.Meta.Publication = "frozen"
	persist()
	if err := d.enableShareFuture(ctx, &s); err != nil {
		t.Fatal(err)
	}
	s.Meta.Future = true
	persist()
	appendData(` answer"}}` + "\n" + `{"type":"system","subtype":"turn_duration","uuid":"old-partial-end","parentUuid":"old-partial"}` + "\n")
	if err := d.ingestShare(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	read()
	if len(s.Messages) != 1 {
		t.Fatal("pre-existing partial row was backfilled")
	}
	mu.Lock()
	data[20] = 'X'
	mu.Unlock()
	if err := d.ingestShare(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	read()
	if s.Meta.Publication != "paused" || len(s.Messages) != 1 {
		t.Fatal("source rewrite did not pause with sealed history")
	}
	t.Log("synthetic node RPC + Postgres future ingestion: disabled gap, partial EOF, pending first-seen completion, same-length rewrite pause, sealed history retention PASS")
}

func TestShareCommonClientFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/fixtures/agent-sharing.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for key, target := range map[string]any{"create_request": new(shareCreateRequest), "create_preview_request": new(sharePolicy), "share": new(shareMetadata), "tool_message": new(devicewire.PublicShareMessage), "narrow_patch": new(sharePatchRequest)} {
		decoder := json.NewDecoder(bytes.NewReader(fixture[key]))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(target); err != nil {
			t.Fatalf("common client fixture %s: %v", key, err)
		}
	}
	var req shareCreateRequest
	_ = json.Unmarshal(fixture["create_request"], &req)
	if !shareRequestUUID(req.RequestID) || !req.Ack.Insecure || req.Future || req.Tools || req.Days != 3 {
		t.Fatal("fixture changed safe defaults or HTTP acknowledgement")
	}
}

func TestShareSelectionAndExpiryUnions(t *testing.T) {
	messages := []devicewire.ShareProjectionMessage{{Message: devicewire.PublicShareMessage{ID: "a"}}, {Message: devicewire.PublicShareMessage{ID: "b"}}, {Message: devicewire.PublicShareMessage{ID: "c"}}}
	selected, err := selectShare(messages, devicewire.ShareSelection{Mode: "from", FromMessageID: "b"})
	if err != nil || len(selected) != 2 {
		t.Fatal("from does not cover complete catalog")
	}
	for _, s := range []devicewire.ShareSelection{{Mode: "all", MessageIDs: []string{"a"}}, {Mode: "individual", MessageIDs: []string{"a", "a"}}, {Mode: "from", FromMessageID: "missing"}} {
		if _, err := selectShare(messages, s); err == nil {
			t.Fatal("invalid union accepted")
		}
	}
	for _, days := range []int{1, 3, 7, 30} {
		if _, err := targetPolicy(sharePolicy{Operation: "create", SnapshotID: "s", SnapshotVersion: "v", Selection: &devicewire.ShareSelection{Mode: "all"}, Days: days}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := targetPolicy(sharePolicy{Operation: "create", SnapshotID: "s", SnapshotVersion: "v", Selection: &devicewire.ShareSelection{Mode: "all"}, Days: 31}, nil); err == nil {
		t.Fatal("creation cap not enforced")
	}
}
