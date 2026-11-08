package node

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestUpdateCodexInventoryRetainsEphemeralThreads(t *testing.T) {
	n := remoteTestNode(t)
	f, c := connectRemoteFixture(t, n, t.TempDir(), "fixture", remoteRaw)
	f.mu.Lock()
	f.threads, f.ephemeral = []string{remoteRaw, remoteOther}, remoteOther
	f.mu.Unlock()
	out, err := c.observeUpdateThreads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Threads) != 2 || len(out.Revision) != 64 || out.Generation != c.generation {
		t.Fatalf("incomplete source observation: %+v", out)
	}
	for _, row := range out.Threads {
		if row.Ephemeral && row.Status != proto.StatusWorking {
			t.Fatalf("ephemeral activity was hidden as idle: %+v", row)
		}
	}
	if f.count("thread/resume") != 0 {
		t.Fatal("read-only inventory resumed a thread")
	}
}

func TestUpdateCodexInventoryRefusesUnreadThreadInsteadOfCachedIdle(t *testing.T) {
	n := remoteTestNode(t)
	f, c := connectRemoteFixture(t, n, t.TempDir(), "fixture", remoteRaw)
	f.mu.Lock()
	f.reject = "thread/read"
	f.mu.Unlock()
	if _, err := c.observeUpdateThreads(context.Background()); err == nil {
		t.Fatal("failed thread read was replaced by cached idle")
	}
	if err := c.refresh(); err != nil {
		t.Fatalf("ordinary display lost its existing cache behavior: %v", err)
	}
}

func TestUpdateCodexInventoryRefusesDuplicateAndCanceledRead(t *testing.T) {
	n := remoteTestNode(t)
	f, c := connectRemoteFixture(t, n, t.TempDir(), "fixture", remoteRaw)
	f.mu.Lock()
	f.threads = []string{remoteRaw, remoteRaw}
	f.mu.Unlock()
	if _, err := c.observeUpdateThreads(context.Background()); err == nil {
		t.Fatal("duplicate loaded IDs were certified complete")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.observeUpdateThreads(ctx); err == nil {
		t.Fatal("canceled read produced evidence")
	}
}
