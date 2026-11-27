package devicewire_test

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
)

func TestConnectorCredRequestWire(t *testing.T) {
	var req devicewire.ConnectorCredRequest
	if err := json.Unmarshal([]byte(`{"machine_id":"m1","future_field":true}`), &req); err != nil {
		t.Fatal(err)
	}
	if raw, err := json.Marshal(req); err != nil || string(raw) != `{"machine_id":"m1"}` {
		t.Fatalf("wire request = %s, %v", raw, err)
	}
}
