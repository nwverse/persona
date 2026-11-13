package devices

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/db/dbtest"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestShareReceiptInputIsInterleavedAndOrderingMetadataStaysPrivate(t *testing.T) {
	d, store, s, _, caller := sharingConcurrencyFixture(t)
	ctx := devicewire.WithCaller(context.Background(), caller)
	for _, receipt := range []struct{ id, text string }{{"synthetic-first-input", "Synthetic first input"}, {"synthetic-second-input", "Synthetic second input"}} {
		if err := store.PutShareSubmission(ctx, receipt.id, s.Owner.Machine, s.Owner.SessionID, s.Owner.Incarnation, "synthetic-digest", receipt.text); err != nil {
			t.Fatal(err)
		}
		if err := store.SettleShareSubmission(ctx, receipt.id, s.Owner.Machine, s.Owner.SessionID, "delivered"); err != nil {
			t.Fatal(err)
		}
	}
	// Set deterministic timestamps only in this binary's disposable test database.
	conn, err := pgx.Connect(ctx, os.Getenv(dbtest.Env))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for i, id := range []string{"synthetic-first-input", "synthetic-second-input"} {
		if _, err := conn.Exec(ctx, `UPDATE share_submission_receipts SET created_at=$1 WHERE submission_id=$2`, time.Date(2026, 1, 1, 0, 0, i*2, 0, time.UTC), id); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("{\"type\":\"assistant\",\"uuid\":\"answer-one\",\"timestamp\":\"2026-01-01T00:00:01Z\",\"message\":{\"role\":\"assistant\",\"content\":\"Synthetic first answer\"}}\n{\"type\":\"system\",\"subtype\":\"turn_duration\",\"uuid\":\"end-one\",\"parentUuid\":\"answer-one\"}\n{\"type\":\"assistant\",\"uuid\":\"answer-two\",\"timestamp\":\"2026-01-01T00:00:03Z\",\"message\":{\"role\":\"assistant\",\"content\":\"Synthetic second answer\"}}\n{\"type\":\"system\",\"subtype\":\"turn_duration\",\"uuid\":\"end-two\",\"parentUuid\":\"answer-two\"}\n")
	managerConn, nodeConn := net.Pipe()
	node, manager := rpc.New("synthetic-node", nodeConn), rpc.New("synthetic-manager", managerConn)
	node.OnRequest = func(ctx context.Context, _ string, params json.RawMessage) (any, error) {
		var req devicewire.ShareProjectionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return transcript.ProjectShareBytes(ctx, data, "synthetic-source", req.Incarnation, req.Previous)
	}
	go node.Serve(ctx, nodeConn)
	go manager.Serve(ctx, managerConn)
	defer node.Close(nil)
	defer manager.Close(nil)
	d.byID[s.Owner.Machine] = &Machine{cfg: MachineConfig{ID: s.Owner.Machine}, d: d, peer: manager, info: proto.NodeInfo{Capabilities: []string{devicewire.ProjectionCapability}}}
	p, err := d.projectShare(ctx, s.Owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Synthetic first input", "Synthetic first answer", "Synthetic second input", "Synthetic second answer"}
	if len(p.Messages) != len(want) {
		t.Fatalf("projected %d messages", len(p.Messages))
	}
	for i, text := range want {
		if p.Messages[i].Message.Text != text {
			t.Errorf("message %d = %q, want %q", i, p.Messages[i].Message.Text, text)
		}
		p.Messages[i].Message.ID = text
	}
	from, err := selectShare(p.Messages, devicewire.ShareSelection{Mode: "from", FromMessageID: want[2]})
	if err != nil || len(from) != 2 || from[1].Message.Text != want[3] {
		t.Fatal("from input did not retain its following reply")
	}
	public, err := json.Marshal(sharePublic(p.Messages, false, "synthetic-public-namespace"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "timestamp") || strings.Contains(string(public), "2026-01-01") || strings.Contains(string(public), "first_seen") || strings.Contains(string(public), "submission") {
		t.Fatal("ordering metadata escaped the public DTO whitelist")
	}
}
