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

func TestPairConnectorReplyKeepsLegacyWire(t *testing.T) {
	for _, reply := range []devicewire.PairConnectorReply{
		{},
		{Address: "https://host.example", Version: "v1"},
	} {
		want, _ := json.Marshal(map[string]any{"address": reply.Address, "version": reply.Version})
		if got, err := json.Marshal(reply); err != nil || string(got) != string(want) {
			t.Errorf("wire reply = %s, %v, want %s", got, err, want)
		}
	}
}
