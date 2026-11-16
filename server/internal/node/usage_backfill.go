package node

import (
	"cmp"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Token usage backfill (docs/server/machines.md "Backfill", #9484): the hub keeps the token usage history from the
// day it started; the days before are only in the transcripts here. Once a machine, the node reads them with the
// live meter's rules (usage.go: a reply counted once, <synthetic> skipped), sums them by the hour, model and launcher,
// and sends the hours to the manager, oldest first. Only numbers leave: no text, no file names. The manager says
// where to start (where the history begins, or the hour after the last it stored) and where to stop (the live
// window's start), stores only hours that have no usage yet, and records how far it got, so a scan cut off continues
// and one run again counts nothing twice.
//
// The scan is gentle: it waits a few minutes after the connection, reads at backfillRate at most, and ends with the
// connection.

const (
	backfillDelay = 3 * time.Minute // after the connection: a node's start is busy enough
	backfillRate  = 4 << 20         // bytes a second, all transcripts together
	backfillRead  = 256 << 10       // the most one read takes before it waits its turn
	backfillChunk = 2000            // buckets a request, whole hours
	backfillCall  = 30 * time.Second
)

// backfillStatus is the manager's answer to usage.backfill.status.
type backfillStatus struct {
	Done   bool  `json:"done"`
	Since  int64 `json:"since"`  // Unix: the first hour to count
	Before int64 `json:"before"` // Unix: count only what came before (the live window starts there)
}

// backfillUsage asks the manager whether this machine's history wants a backfill and does it.
func (n *Node) backfillUsage(ctx context.Context, stateLoaded <-chan struct{}) {
	select { // the launchers come from the sessions' state, as the live meter's
	case <-ctx.Done():
		return
	case <-stateLoaded:
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(backfillDelay):
	}
	if n.peer == nil {
		return
	}
	var st backfillStatus
	cctx, cancel := context.WithTimeout(ctx, backfillCall)
	err := n.peer.Call(cctx, "usage.backfill.status", map[string]any{}, &st)
	cancel()
	if err != nil || st.Done || st.Since >= st.Before { // an older manager does not know it
		return
	}
	since, before := time.Unix(st.Since, 0), time.Unix(st.Before, 0)
	started := time.Now()
	buckets, err := n.backfillScan(ctx, since, before)
	if err != nil {
		return
	}
	hours := 0
	chunks := backfillChunks(buckets, backfillChunk)
	for i, chunk := range chunks {
		var through int64
		if len(chunk) > 0 {
			through = chunk[len(chunk)-1].Minute
		}
		last := i == len(chunks)-1
		var res struct {
			Stored int `json:"stored"`
		}
		cctx, cancel := context.WithTimeout(ctx, backfillCall)
		err := n.peer.Call(cctx, "usage.backfill", map[string]any{"buckets": chunk, "through": through, "done": last}, &res)
		cancel()
		if err != nil { // the next connection asks again, from the hour after the last stored
			n.logger.Printf("usage backfill: %v", err)
			return
		}
		hours += res.Stored
	}
	n.logger.Printf("usage backfill: %d buckets read in %s, %d hours stored", len(buckets), time.Since(started).Round(time.Second), hours)
}

// backfillScan sums the replies of every transcript from since to before by the hour, model and launcher, in time
// order. Only ctx ending stops it (its error).
func (n *Node) backfillScan(ctx context.Context, since, before time.Time) ([]proto.UsageBucket, error) {
	m := newUsageMeter()
	m.step, m.until, m.seen = 3600, before, map[uint64]struct{}{}
	m.launcherOf = func(session string) string {
		st, _ := n.state.get(session)
		return st.Launcher
	}
	lim := &readLimiter{rate: backfillRate, start: time.Now()}
	for _, f := range n.usageFiles(since) {
		if err := m.scanAll(ctx, f.path, f.st, since, lim); err != nil {
			return nil, err
		}
	}
	out := make([]proto.UsageBucket, 0, len(m.buckets))
	for _, b := range m.buckets {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b proto.UsageBucket) int {
		return cmp.Or(cmp.Compare(a.Minute, b.Minute), cmp.Compare(a.Model, b.Model), cmp.Compare(a.Launcher, b.Launcher))
	})
	return out, nil
}

// backfillChunks splits buckets (in time order) into requests of at most size buckets, never an hour across two (an
// hour with more is one request of its own); one empty request when there are none, so the manager hears it is done.
func backfillChunks(buckets []proto.UsageBucket, size int) [][]proto.UsageBucket {
	if len(buckets) == 0 {
		return [][]proto.UsageBucket{nil}
	}
	var out [][]proto.UsageBucket
	start := 0
	for i := 1; i < len(buckets); i++ {
		if i-start >= size && buckets[i].Minute != buckets[i-1].Minute {
			out = append(out, buckets[start:i])
			start = i
		}
	}
	return append(out, buckets[start:])
}

// scanAll reads path whole from about since (startOffset), through lim, as scan reads what a file gained.
func (m *usageMeter) scanAll(ctx context.Context, path string, st os.FileInfo, since time.Time, lim *readLimiter) error {
	f, err := os.Open(path)
	if err != nil {
		return ctx.Err()
	}
	defer f.Close()
	uf := &usageFile{session: usageSession(path)}
	off := startOffset(path, st.Size(), since)
	codex := strings.HasPrefix(filepath.Base(path), "rollout-")
	r := &limitedReader{ctx: ctx, r: io.NewSectionReader(f, off, st.Size()-off), lim: lim}
	eachLine(r, func(line []byte) {
		if codex {
			m.codexLine(uf, line, since)
		} else {
			m.claudeLine(uf, line, since)
		}
	})
	return ctx.Err()
}

// readLimiter keeps the reads of a backfill to rate bytes a second on average.
type readLimiter struct {
	rate  float64
	start time.Time
	read  int64
}

// wait takes n bytes read and waits until they are within the rate; ctx's error when it ended first.
func (l *readLimiter) wait(ctx context.Context, n int) error {
	l.read += int64(n)
	ahead := time.Duration(float64(l.read)/l.rate*float64(time.Second)) - time.Since(l.start)
	if ahead <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(ahead)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// limitedReader reads r through lim, at most backfillRead a read; it ends with ctx.
type limitedReader struct {
	ctx context.Context
	r   io.Reader
	lim *readLimiter
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if len(p) > backfillRead {
		p = p[:backfillRead]
	}
	n, err := r.r.Read(p)
	if werr := r.lim.wait(r.ctx, n); werr != nil && err == nil {
		err = werr
	}
	return n, err
}
