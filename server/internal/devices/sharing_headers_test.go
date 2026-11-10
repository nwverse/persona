package devices

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
)

func TestShareMetadataListingRemainsSmallAndOwnerFencedWithLargeBodies(t *testing.T) {
	d, store, s, _, caller := sharingConcurrencyFixture(t)
	ctx := devicewire.WithCaller(context.Background(), caller)
	for i := 0; i < 100; i++ {
		id := "synthetic-message-" + strconv.Itoa(i)
		s.Messages = append(s.Messages, devicewire.ShareProjectionMessage{Identity: id, Message: devicewire.PublicShareMessage{ID: id, Role: "assistant", Text: strings.Repeat("synthetic private body ", 1000)}})
	}
	s.Meta.Selection.Count = len(s.Messages)
	if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		row, err := tx.Share(s.Meta.ID)
		if err != nil {
			return err
		}
		return saveSealed(tx, row, s)
	}); err != nil {
		t.Fatal(err)
	}
	handler := d.Handler(http.NotFoundHandler())
	read := func(who devicewire.Caller) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/agent-shares", nil).WithContext(devicewire.WithCaller(context.Background(), who))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := read(caller)
	if w.Code != 200 || w.Body.Len() > 1024 || strings.Contains(w.Body.String(), "synthetic private body") || !strings.Contains(w.Body.String(), s.Meta.ID) {
		t.Fatalf("metadata list expanded or omitted valid share: %d (%d bytes)", w.Code, w.Body.Len())
	}
	other := caller
	other.OwnerSubject = "80000000-0000-4000-8000-000000000002"
	if w := read(other); w.Code != 200 || strings.Contains(w.Body.String(), s.Meta.ID) {
		t.Fatal("other subject read prepared metadata")
	}
	r := httptest.NewRequest("DELETE", "/api/v1/agent-shares/"+s.Meta.ID, strings.NewReader(`{"expected_revision":1}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("revoke failed")
	}
	if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		retained, active, err := tx.ShareCounts(s.Owner.Issuer, s.Owner.Subject, s.Meta.Created)
		if err != nil {
			return err
		}
		if retained != 1 || active != 0 {
			t.Errorf("revoke left quota active: %d/%d", retained, active)
		}
		rows, err := tx.ShareHeaders(s.Owner.Issuer, s.Owner.Subject)
		if err != nil {
			return err
		}
		var header sealedShareHeader
		if len(rows) != 1 {
			t.Fatal("header missing after revoke")
		}
		if err := json.Unmarshal(rows[0].Header, &header); err != nil {
			return err
		}
		if header.Meta.Status != "revoked" || rows[0].Active || rows[0].Live || len(rows[0].Body) != 0 {
			t.Error("metadata/body/quota state did not commit atomically")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
