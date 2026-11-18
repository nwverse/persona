package devices

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestShareConcurrentPublicReadersDoNotExhaustPool(t *testing.T) {
	d, store, s, token, _ := sharingConcurrencyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := pgx.Connect(ctx, os.Getenv(dbtest.Env))
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_lock(35713604)`); err != nil {
		t.Fatal(err)
	}
	handler := d.Handler(http.NotFoundHandler())
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() {
			r := httptest.NewRequest("GET", "/api/public/agent-shares/"+token, nil).WithContext(devicewire.WithCaller(ctx, devicewire.Caller{Kind: "anonymous"}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			results <- w.Code
		}()
	}
	// Establish the exact pool contention which previously made the first reader
	// hold the writer fence while waiting for a nonexistent ninth connection.
	waitCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := blocker.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND query='SELECT pg_advisory_xact_lock(35713604)' AND wait_event='advisory'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 8 {
			break
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatalf("could not establish eight waiting readers: %d", waiting)
		}
	}
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_unlock(35713604)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		select {
		case code := <-results:
			if code != 200 {
				t.Fatalf("concurrent reader returned %d", code)
			}
		case <-ctx.Done():
			t.Fatal("public readers exhausted the manager pool")
		}
	}
	queryCtx, done := context.WithTimeout(ctx, time.Second)
	defer done()
	if _, err := store.SessionShareOwner(queryCtx, s.Owner.Machine, s.Owner.SessionID); err != nil {
		t.Fatalf("pool capacity was not released: %v", err)
	}
}

func TestShareOutOfOrderFrontiersDoNotRewindWithoutContentChange(t *testing.T) {
	d, store, s, _, _ := sharingConcurrencyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base := []byte("{\"type\":\"assistant\",\"uuid\":\"historical\",\"message\":{\"role\":\"assistant\",\"content\":\"Historical answer\"}}\n{\"type\":\"system\",\"subtype\":\"turn_duration\",\"uuid\":\"historical-end\",\"parentUuid\":\"historical\"}\n")
	p, err := transcript.ProjectShareBytes(ctx, base, "synthetic-source", s.Owner.Incarnation, nil)
	if err != nil || !p.Complete {
		t.Fatalf("base projection: %v %s", err, p.Reason)
	}
	applyShareFuture(&s, p)
	if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		row, err := tx.Share(s.Meta.ID)
		if err != nil {
			return err
		}
		return saveSealed(tx, row, s)
	}); err != nil {
		t.Fatal(err)
	}
	early := append(append([]byte(nil), base...), []byte("{\"type\":\"assistant\",\"uuid\":\"pending\",\"message\":{\"role\":\"assistant\",\"content\":\"Pending answer\"}}\n")...)
	later := append(append([]byte(nil), early...), []byte(`{"type":"system","subtype":"turn_duration","uuid":"partial`)...)
	oldEntered, release := make(chan struct{}), make(chan struct{})
	managerConn, nodeConn := net.Pipe()
	node, manager := rpc.New("synthetic-node", nodeConn), rpc.New("synthetic-manager", managerConn)
	var calls atomic.Int32
	node.OnRequest = func(ctx context.Context, _ string, params json.RawMessage) (any, error) {
		var req devicewire.ShareProjectionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		data := later
		if calls.Add(1) == 1 {
			data = early
			close(oldEntered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return transcript.ProjectShareBytes(ctx, data, "synthetic-source", req.Incarnation, req.Previous)
	}
	go node.Serve(ctx, nodeConn)
	go manager.Serve(ctx, managerConn)
	defer node.Close(nil)
	defer manager.Close(nil)
	d.byID[s.Owner.Machine] = &Machine{cfg: MachineConfig{ID: s.Owner.Machine}, d: d, peer: manager, info: proto.NodeInfo{Capabilities: []string{devicewire.ProjectionCapability}}}
	oldResult := make(chan error, 1)
	go func() { oldResult <- d.ingestShare(ctx, s.Meta.ID) }()
	select {
	case <-oldEntered:
	case <-ctx.Done():
		t.Fatal("old projection did not start")
	}
	if err := d.ingestShare(ctx, s.Meta.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-oldResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("old projection did not finish")
	}
	if err := store.ShareTransaction(ctx, func(tx *db.ShareTx) error {
		_, current, err := loadSealed(tx, s.Meta.ID)
		if err == nil && (current.Frontier.EOF != int64(len(later)) || current.Meta.ContentVersion != 1 || current.Meta.Revision != 1 || len(current.Messages) != 0) {
			t.Error("stale projection rewound a frontier without a visible content change")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
