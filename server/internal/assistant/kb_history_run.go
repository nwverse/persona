package assistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/kb"
)

// One step of a chat's import: one call (and the image downloads of the page it answered), its messages written,
// its state saved. A chat's mode decides how:
//
//   - full: every message, read backwards from the newest page, going on only from the cursor each page answers (a
//     limit is a hint: sources answer more or fewer), until the start of the conversation (AtStart, position 0) or
//     the text retention.
//   - anchor, from a source that cannot search: the same read, through kb.AnchorFilter (the owner's messages and
//     mentions, 50 on each side).
//   - anchor, from a source that can: windows by position around the anchors its search found
//     (kb_history_anchor.go).

// step makes the chat's next call in mode; done: the chat needs none any more.
func (d *historyDriver) step(ctx context.Context, w historyWork, mode string) (bool, error) {
	e, err := d.eng(ctx)
	if err != nil {
		return false, halt(false, "kb_unavailable")
	}
	st, err := d.loadState(e, w, mode)
	if err != nil {
		return false, halt(true, "state_unreadable", "chat", historyHash(d.key, w.chat.filing(d.source), w.chat.ID))
	}
	if st.Finished() {
		return true, nil
	}
	switch {
	case mode == kb.HistoryFull:
		return d.stepBackwards(ctx, e, w, &st, false)
	case d.info.HistoryAnchors:
		if st.Positions == nil && st.Filter == nil {
			d.initPositions(w, &st)
			if err := d.saveState(e, w, &st); err != nil {
				return false, err
			}
			if st.Finished() {
				return true, nil
			}
		}
		if st.Positions != nil {
			return d.stepPositional(ctx, e, w, &st)
		}
	}
	return d.stepBackwards(ctx, e, w, &st, true)
}

// loadState is the chat's state in mode: a new one when there is none or it was another mode (the owner changed it:
// it starts over; what was written stays, and a page read twice adds nothing).
func (d *historyDriver) loadState(e *kb.Engine, w historyWork, mode string) (kb.HistoryState, error) {
	st, ok, err := e.Store.HistoryState(w.id)
	if err != nil {
		return kb.HistoryState{}, err
	}
	if !ok || st.Mode != mode {
		st = kb.HistoryState{Source: w.chat.filing(d.source), Mode: mode, State: kb.HistoryRunning, Read: st.Read, Written: st.Written}
	}
	if st.State == kb.HistoryPaused {
		st.State = kb.HistoryRunning
	}
	return st, nil
}

func (d *historyDriver) saveState(e *kb.Engine, w historyWork, st *kb.HistoryState) error {
	st.UpdatedAt = d.now().UTC()
	if err := e.Store.PutHistoryState(w.id, *st); err != nil {
		return halt(true, "state_save_failed", "class", errClass(err))
	}
	return nil
}

// kbChat names the chat for WriteHistory.
func (d *historyDriver) kbChat(w historyWork) *kb.Chat {
	kind := w.chat.Kind
	if kind == historyUnlisted {
		kind = connectorwire.HistoryKindGroup
	}
	return &kb.Chat{Source: w.chat.filing(d.source), SourceID: w.chat.ID, Kind: kind, Name: w.chat.Name}
}

// historyAnchor picks an anchor: the owner's own message, or one that mentions the owner.
func historyAnchor(m kb.SourceMessage) bool { return m.Mine || m.Mentioned }

// page reads one page of the chat (one call).
func (d *historyDriver) page(ctx context.Context, w historyWork, q connectorwire.HistoryQuery) (connectorwire.HistoryPage, historyCall, error) {
	q.Chat = w.chat.ID
	if q.Limit == 0 {
		q.Limit = d.pacer.pace.PageLimit
	}
	var page connectorwire.HistoryPage
	c, err := d.call(ctx, "history", w.chat.filing(d.source), w.chat.ID, false, func(cctx context.Context) (int, string, string, error) {
		p, err := d.src.Page(cctx, d.source, q)
		page = p
		return len(p.Messages), pageResult(p), paceText(p.Pace), err
	})
	return page, c, err
}

func pageResult(p connectorwire.HistoryPage) string {
	switch {
	case p.Pace != nil:
		return "paced"
	case p.Unreadable:
		return "unreadable"
	}
	return "ok"
}

func paceText(p *connectorwire.Pace) string {
	switch {
	case p == nil:
		return ""
	case p.Stop:
		return "stop"
	}
	return fmt.Sprintf("retry_after=%d", p.RetryAfterMS)
}

// answered handles what a page call came to before its messages are written: the gate ended the run (the chat is
// paused), the call failed, the source paced or the chat is unreadable. handled false: the page is to be written.
// unreadable writes what can be kept of an unreadable chat (nil: nothing).
func (d *historyDriver) answered(ctx context.Context, e *kb.Engine, w historyWork, st *kb.HistoryState, page connectorwire.HistoryPage,
	c historyCall, callErr error, unreadable func() error) (handled, done bool, err error) {
	if callErr != nil {
		st.State, st.LastError = kb.HistoryPaused, "stopped"
		if serr := d.saveState(e, w, st); serr != nil {
			return true, false, serr
		}
		return true, false, callErr
	}
	if c.err != nil {
		outcome, code := c.classify()
		return d.outcome(ctx, e, w, st, c, outcome, code, unreadable)
	}
	if p := page.Pace; p != nil {
		if p.Stop {
			st.State, st.LastError = kb.HistoryPaused, "source_stop"
			if err := d.saveState(e, w, st); err != nil {
				return true, false, err
			}
			return true, false, historySourceStop(p)
		}
		return d.outcome(ctx, e, w, st, c, historyPauseIt, paceText(p), nil)
	}
	if page.Unreadable {
		return d.outcome(ctx, e, w, st, c, historyUnreadable, "unreadable", unreadable)
	}
	return false, false, nil
}

// historySourceStop is the stop a source asked for (Pace.Stop), with its own words.
func historySourceStop(p *connectorwire.Pace) *historyHalt {
	h := halt(true, "source_stop")
	h.why.Text = clipRunes(strings.TrimSpace(p.Reason), 200)
	return h
}

// outcome acts on what a call came to for the chat: pause and read the same again, stop everything, or end the
// chat (error, unreadable).
func (d *historyDriver) outcome(ctx context.Context, e *kb.Engine, w historyWork, st *kb.HistoryState, c historyCall,
	o historyOutcome, code string, unreadable func() error) (handled, done bool, err error) {
	switch o {
	case historyPauseIt:
		st.State, st.LastError = kb.HistoryPaused, code
		if err := d.saveState(e, w, st); err != nil {
			return true, false, err
		}
		if c.stop != nil { // the call was also the second very slow one
			return true, false, c.stop
		}
		if err := d.pause(ctx, d.pauseWait(code), pauseFor(code)); err != nil {
			return true, false, err
		}
		return true, false, d.after(ctx, c)
	case historyStopIt:
		st.State, st.LastError = kb.HistoryPaused, code
		if err := d.saveState(e, w, st); err != nil {
			return true, false, err
		}
		return true, false, haltWhy(true, c.stopWhy(code))
	case historyUnreadable:
		if unreadable != nil {
			if err := unreadable(); err != nil {
				return true, false, err
			}
		}
		st.State, st.LastError = kb.HistoryUnreadable, ""
	default:
		st.State, st.LastError = kb.HistoryError, code
	}
	if err := d.saveState(e, w, st); err != nil {
		return true, false, err
	}
	return true, true, d.after(ctx, c)
}

// pauseWait is how long a pause for code lasts: what the source asked (retry_after) and a little more, else 5–10
// minutes.
func (d *historyDriver) pauseWait(code string) time.Duration {
	if ms, ok := strings.CutPrefix(code, "retry_after="); ok {
		n, _ := strconv.ParseInt(ms, 10, 64)
		return time.Duration(n)*time.Millisecond + time.Duration((5+25*d.rng.Float64())*float64(time.Second))
	}
	return historyTransientWait + time.Duration(d.rng.Float64()*float64(historyTransientWait))
}

// write writes msgs of the chat (WriteHistory, with the source's images when it hands them over). When an image's
// download was cut short (a stop, a pause that ran out, the import turned off), nothing of the page is written: the
// caller returns the halt without saving its state, so the page is read again on resume and its images with it (the
// ones already saved are not downloaded twice).
func (d *historyDriver) write(ctx context.Context, e *kb.Engine, w historyWork, msgs []kb.SourceMessage, mode string) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	var dl kb.DownloadFunc
	if d.info.Media {
		dl = d.download(ctx, w)
	}
	added, err := e.WriteHistory(ctx, d.kbChat(w), msgs, kb.HistoryWrite{Mode: mode, Download: dl})
	if errors.Is(err, kb.ErrDownloadAborted) {
		if h := d.takeHalt(); h != nil {
			return 0, h
		}
		return 0, halt(false, "image_aborted")
	}
	if err != nil {
		return added, halt(true, "write_failed", "class", errClass(err))
	}
	return added, nil
}

// takeHalt is the halt an image download met while a page was written (nil: none), cleared.
func (d *historyDriver) takeHalt() error {
	h := d.halted
	d.halted = nil
	return h
}

// stepBackwards reads the chat's next page backwards: every message (full), or through the anchor filter.
func (d *historyDriver) stepBackwards(ctx context.Context, e *kb.Engine, w historyWork, st *kb.HistoryState, anchor bool) (bool, error) {
	page, c, callErr := d.page(ctx, w, connectorwire.HistoryQuery{Before: st.Cursor})
	if handled, done, err := d.answered(ctx, e, w, st, page, c, callErr, nil); handled {
		return done, err
	}
	msgs := page.Messages
	cut := e.TextCutoff()
	reached := page.AtStart() || page.Older == "" || historyAtZero(msgs) || historyBefore(msgs, cut)
	if !reached && page.Older == st.Cursor {
		// The source answered the same cursor: going on would read the same page forever.
		_, done, err := d.outcome(ctx, e, w, st, c, historyChatError, "cursor_stuck", nil)
		return done, err
	}
	out := msgs
	mode := kb.HistoryFull
	if anchor {
		mode = kb.HistoryAnchor
		f := kb.NewAnchorFilter(historyAnchor, 0, st.Filter)
		out = f.Push(historyFrom(msgs, cut))
		if reached {
			out = append(out, f.Flush()...)
		}
		fs := f.State()
		st.Filter = &fs
	}
	added, err := d.write(ctx, e, w, out, mode)
	if err != nil {
		return false, err
	}
	st.Read += len(msgs)
	st.Written += added
	st.LastError = ""
	if reached {
		st.State, st.Cursor = kb.HistoryDone, ""
	} else {
		st.State, st.Cursor = kb.HistoryRunning, page.Older
	}
	if err := d.saveState(e, w, st); err != nil {
		return false, err
	}
	if h := d.takeHalt(); h != nil {
		return false, h
	}
	return reached, d.after(ctx, c)
}

// historyAtZero: a message at position 0, the first of a conversation whose positions are numbers.
func historyAtZero(msgs []kb.SourceMessage) bool {
	for _, m := range msgs {
		if m.Position == "0" {
			return true
		}
	}
	return false
}

// historyBefore: a message is before the text retention.
func historyBefore(msgs []kb.SourceMessage, cut time.Time) bool {
	for _, m := range msgs {
		if !m.At.IsZero() && m.At.Before(cut) {
			return true
		}
	}
	return false
}

// historyFrom are msgs at or after cut.
func historyFrom(msgs []kb.SourceMessage, cut time.Time) []kb.SourceMessage {
	var out []kb.SourceMessage
	for _, m := range msgs {
		if m.At.IsZero() || !m.At.Before(cut) {
			out = append(out, m)
		}
	}
	return out
}

// download fetches a page's images through the hub (connectors.archive_media), each a paced call of its own: only
// pictures are written (anything else that comes back is refused, so the line keeps its placeholder), and one saved
// already (the page read again after a stop) is not asked for again. A transient error pauses and asks for the same
// image again. A stop (a timeout, an unknown error, the gate during the wait, a pause that ran out) aborts the page's
// write (kb.ErrDownloadAborted): the halt is acted on once WriteHistory returns, and the page is read again later.
func (d *historyDriver) download(ctx context.Context, w historyWork) kb.DownloadFunc {
	filing := w.chat.filing(d.source)
	abort := func(err error) error {
		d.halted = err
		return kb.ErrDownloadAborted
	}
	return func(_ context.Context, ref, dst string) error {
		if d.halted != nil {
			return kb.ErrDownloadAborted
		}
		if info, err := os.Stat(dst); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return nil
		}
		for {
			var m connector.Media
			c, err := d.call(ctx, historyImageMethod, filing, w.chat.ID, true, func(cctx context.Context) (int, string, string, error) {
				got, err := d.src.Media(cctx, d.source, ref)
				m = got
				return 1, "ok", "", err
			})
			if err != nil {
				return abort(err)
			}
			if c.err != nil {
				o, code := c.classify()
				switch {
				case c.stop != nil:
					return abort(c.stop)
				case o == historyStopIt:
					return abort(haltWhy(true, c.stopWhy(code)))
				case o == historyPauseIt:
					if err := d.pause(ctx, d.pauseWait(code), pauseFor(code)); err != nil {
						return abort(err)
					}
					continue // the same image again
				}
				// Gone or refused: this image only; the line keeps its placeholder.
				if err := d.after(ctx, c); err != nil {
					d.halted = err
				}
				return c.err
			}
			saveErr := saveHistoryImage(m, dst)
			if err := d.after(ctx, c); err != nil {
				d.halted = err // after the image is saved: the next one aborts, or the page is written and the run ends
			}
			return saveErr
		}
	}
}

// saveHistoryImage writes an image the source handed over (0600): only a picture within the size an attachment may be.
func saveHistoryImage(m connector.Media, dst string) error {
	if !strings.HasPrefix(strings.ToLower(m.Mime), "image/") {
		return fmt.Errorf("not an image: %q", m.Mime)
	}
	if len(m.Data) == 0 || len(m.Data) > connector.ArchiveMediaMost {
		return fmt.Errorf("an image of %d bytes", len(m.Data))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, m.Data, 0o600)
}
