package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The backfill reads every transcript from since to before with the live meter's rules and sums the replies by the
// hour: a reply in two files once, <synthetic> skipped, nothing before since or in the live window (from before on).
func TestUsageBackfillScan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	n, home := testNode(t)
	now := time.Now().UTC().Truncate(time.Hour)
	since, before := now.Add(-10*24*time.Hour), now.Add(-26*time.Hour)
	day := now.Add(-3 * 24 * time.Hour)
	project := filepath.Join(home, ".claude", "projects", "-work-app")
	files := map[string]string{
		filepath.Join(project, "s1.jsonl"): claudeReply(since.Add(-time.Hour), "old", "claude-opus-5-5", 1, 1, 1, 1) + // before since
			claudeReply(day.Add(5*time.Minute), "r1", "claude-opus-5-5", 2, 100, 1000, 50) +
			claudeReply(day.Add(5*time.Minute), "r1", "claude-opus-5-5", 2, 100, 1000, 50) + // its next content block
			claudeReply(day.Add(50*time.Minute), "r2", "claude-opus-5-5", 3, 0, 500, 20) +
			claudeReply(day.Add(70*time.Minute), "r3", "<synthetic>", 0, 0, 0, 0) +
			claudeReply(before.Add(time.Minute), "live", "claude-opus-5-5", 9, 9, 9, 9), // the live meter's
		filepath.Join(project, "s2.jsonl"): claudeReply(day.Add(5*time.Minute), "r1", "claude-opus-5-5", 2, 100, 1000, 50) + // a fork's copy
			claudeReply(day.Add(2*time.Hour), "r4", "claude-sonnet-5-5", 4, 0, 40, 7),
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	buckets, err := n.backfillScan(context.Background(), since, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v", buckets)
	}
	if b := buckets[0]; b.Minute != day.Unix() || b.Model != "claude-opus-5-5" || b.Requests != 2 || b.Output != 70 || b.CacheRead != 1500 || b.Input != 5 {
		t.Fatalf("the first hour = %+v", b)
	}
	if b := buckets[1]; b.Minute != day.Add(2*time.Hour).Unix() || b.Output != 7 {
		t.Fatalf("two hours on = %+v", b)
	}

	// The connection ended: the scan stops.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.backfillScan(ctx, since, before); err == nil {
		t.Fatal("a scan after its connection ended")
	}
}

func TestBackfillChunks(t *testing.T) {
	hour := func(h int64, model string) proto.UsageBucket {
		return proto.UsageBucket{Minute: h * 3600, Model: model}
	}
	list := []proto.UsageBucket{hour(1, "a"), hour(1, "b"), hour(1, "c"), hour(2, "a"), hour(3, "a"), hour(3, "b")}
	chunks := backfillChunks(list, 2)
	// Two buckets a request, but hour 1's three stay together.
	if len(chunks) != 2 || len(chunks[0]) != 3 || len(chunks[1]) != 3 || chunks[1][0].Minute != 2*3600 {
		t.Fatalf("chunks = %v", chunks)
	}
	if c := backfillChunks(list, 100); len(c) != 1 || len(c[0]) != 6 {
		t.Fatalf("one request = %v", c)
	}
	if c := backfillChunks(nil, 100); len(c) != 1 || len(c[0]) != 0 {
		t.Fatalf("none = %v", c)
	}
}

// The limiter keeps the reads to its rate, and a connection that ends stops the wait.
func TestBackfillReadLimiter(t *testing.T) {
	l := &readLimiter{rate: 1 << 20, start: time.Now()}
	started := time.Now()
	if err := l.wait(context.Background(), 200<<10); err != nil { // 0.2 MB at 1 MB a second
		t.Fatal(err)
	}
	if took := time.Since(started); took < 150*time.Millisecond {
		t.Fatalf("200 KB took %s at 1 MB/s", took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.wait(ctx, 10<<20); err == nil {
		t.Fatal("waited past the connection's end")
	}
}
