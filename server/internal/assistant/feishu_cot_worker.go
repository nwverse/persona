package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/proto"
)

// cotWorker separates native I/O from the card's progress mutex. It owns the native
// cursor and one replaceable snapshot of the bounded item history, never a queue
// of ticks. A terminal snapshot supersedes progress and cancels its in-flight I/O.
type cotWorker struct {
	mu                      sync.Mutex
	wake                    chan struct{}
	done                    chan struct{}
	pending                 []proto.Item
	queued, ending, aborted bool
	usable                  bool
	cancel                  context.CancelFunc
	run                     channel.Run
	live                    chanLive // worker-owned native cursor, never the card's chanLive
}

// topicCotQueue runs under the progress mutex, but never waits for native I/O.
// Its return value is the last confirmed native availability, for card fallback.
func (f feishuSink) topicCotQueue(r *channel.Run, finish bool) bool {
	s := f.s
	c := &s.chans
	c.mu.Lock()
	owner := c.live[r.ID]
	if owner == nil {
		c.mu.Unlock()
		return false
	}
	w := owner.native
	if w == nil {
		snap := *r
		snap.Ext = cloneExt(r.Ext)
		w = &cotWorker{wake: make(chan struct{}, 1), done: make(chan struct{}), run: snap}
		w.live.cot = owner.cot
		w.usable = snap.Ext["cot"] != "" && snap.Ext["cot_msg"] != "" && snap.Ext["cot_off"] == ""
		owner.native = w
		w.live.cot.persist = func(snap *channel.Run, key, value string) {
			w.mu.Lock()
			defer w.mu.Unlock()
			c.mu.Lock()
			defer c.mu.Unlock()
			if snap.Ext == nil {
				snap.Ext = map[string]string{}
			}
			snap.Ext[key] = value
			w.usable = snap.Ext["cot"] != "" && snap.Ext["cot_msg"] != "" && snap.Ext["cot_off"] == ""
			// Completion, a held next turn, or removal must never inherit a late write.
			if !w.aborted && c.live[r.ID] == owner && owner.native == w {
				real := r
				for _, candidate := range c.st.Runs {
					if candidate.ID == r.ID {
						real = candidate
						break
					}
				}
				if real.Ext == nil {
					real.Ext = map[string]string{}
				}
				real.Ext[key] = value
				s.saveChannelStateLocked()
			}
		}
		go w.work(f)
		go func() {
			select {
			case <-owner.done:
				w.abort()
			case <-s.runCtx().Done():
				w.abort()
			case <-w.done:
			}
		}()
	}
	items := append([]proto.Item(nil), owner.items...)
	c.mu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	usable := w.usable
	if w.ending || w.aborted {
		return usable
	}
	w.pending, w.queued = items, true
	if finish {
		w.ending = true
		if w.cancel != nil {
			w.cancel()
		}
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return usable
}

func (w *cotWorker) abort() {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A terminal snapshot is already independent of the now-removed run.
	if w.ending {
		return
	}
	w.aborted, w.pending, w.queued = true, nil, false
	if w.cancel != nil {
		w.cancel()
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *cotWorker) work(f feishuSink) {
	defer close(w.done)
	defer func() {
		// A folded/removed run can receive a successful create after cancellation.
		// Close that known message without publishing state into its successor.
		w.mu.Lock()
		aborted := w.aborted
		w.mu.Unlock()
		if aborted && w.live.cot.cot != "" && !w.live.cot.done && !w.live.cot.off {
			_ = f.cotEnd(context.Background(), w.live.cot.cot, w.live.cot.msg)
		}
	}()
	for range w.wake {
		w.mu.Lock()
		if w.aborted {
			w.mu.Unlock()
			return
		}
		if !w.queued {
			w.mu.Unlock()
			continue
		}
		items, finish := w.pending, w.ending
		w.pending, w.queued = nil, false
		ctx, cancel := context.WithTimeout(f.s.runCtx(), 15*time.Second)
		w.cancel = cancel
		w.mu.Unlock()
		ctx = feishuSendAs(ctx, "progress", w.run.MachineID, w.run.SessionID)
		if finish {
			f.cotFinishNowItems(ctx, &w.run, &w.live, items)
		} else {
			f.cotProgressNowItems(ctx, &w.run, &w.live, items)
		}
		cancel()
		w.mu.Lock()
		w.cancel = nil
		w.usable = w.live.cot.cot != "" && !w.live.cot.off
		aborted := w.aborted
		w.mu.Unlock()
		if finish || aborted {
			return
		}
	}
}
