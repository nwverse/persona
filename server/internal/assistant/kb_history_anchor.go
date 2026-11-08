package assistant

import (
	"context"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/kb"
)

// Anchor mode for a source that can search (history_anchors), whose positions are consecutive numbers:
//
//  1. The owner's messages (sent and mentioned) are gathered for the whole account, one calendar year at a time from
//     this year back to the text retention, into history/anchors-<source>.json, then grouped by chat. A chat the plan
//     does not list (often one the account left) is added to it, by its anchors.
//  2. In each anchor chat, the window of 50 positions on each side of every anchor is read: around the anchor first,
//     then before or after what was read until the window is covered. Windows that overlap or touch merge, and what
//     was read already is never asked for again. A message read that is the owner's or mentions the owner becomes an
//     anchor too (a search can miss one). Once every window is read, each gap between two windows gets one omission
//     line with the number of positions it leaves out, and so does the gap below the oldest window; above the
//     newest none is written, since the newer messages may be the live archive's.
//  3. A year the search found nothing in is taken as not covered by it (an index may not reach that far back): there,
//     anchor chats are read backwards through kb.AnchorFilter instead, that year only, and only the years since the
//     conversation began (one page around position 0 says when, unless the windows reached it).
//
// A chat that cannot be read any more (unreadable, not found) keeps only the anchors' own messages the search
// returned. A chat whose positions are not numbers is read backwards through the filter, as from a source that
// cannot search.

type historyAnchorYear struct {
	Year   int       `json:"year"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	Cursor string    `json:"cursor,omitempty"`
	Done   bool      `json:"done,omitempty"`
	Hits   int       `json:"hits"`
}

// historyAnchorsCache is anchors-<source>.json.
type historyAnchorsCache struct {
	Source    string                        `json:"source"`
	Years     []historyAnchorYear           `json:"years"`
	Anchors   []connectorwire.HistoryAnchor `json:"anchors,omitempty"`
	Done      bool                          `json:"done,omitempty"`
	UpdatedAt time.Time                     `json:"updated_at"`
}

func historyAnchorsPath(data, source string) string {
	return filepath.Join(historyDir(data), "anchors-"+kb.SafeID(source)+".json")
}

func loadHistoryAnchors(data, source string) (*historyAnchorsCache, error) {
	c := &historyAnchorsCache{Source: source}
	if _, err := readJSONFile(historyAnchorsPath(data, source), c); err != nil {
		return nil, err
	}
	return c, nil
}

func (d *historyDriver) saveAnchors(c *historyAnchorsCache) error {
	c.UpdatedAt = d.now().UTC()
	if err := writeJSONFile(historyAnchorsPath(d.data, d.source), c); err != nil {
		return halt(true, "anchors_save_failed", "class", errClass(err))
	}
	return nil
}

// uncovered are the years whose search found nothing, newest first.
func (c *historyAnchorsCache) uncovered() []int {
	var out []int
	for _, y := range c.Years {
		if y.Done && y.Hits == 0 {
			out = append(out, y.Year)
		}
	}
	return out
}

// historyYears are the calendar years from now back to the text retention (in zone), newest first.
func historyYears(now, cut time.Time, zone *time.Location) []historyAnchorYear {
	now = now.In(zone)
	var out []historyAnchorYear
	for y := now.Year(); y >= cut.In(zone).Year(); y-- {
		since := time.Date(y, 1, 1, 0, 0, 0, 0, zone)
		until := time.Date(y+1, 1, 1, 0, 0, 0, 0, zone).Add(-time.Second)
		if since.Before(cut) {
			since = cut
		}
		if until.After(now) {
			until = now
		}
		out = append(out, historyAnchorYear{Year: y, Since: since.UTC(), Until: until.UTC()})
	}
	return out
}

// gatherAnchors gathers the source's anchors once (going on where it stopped), when the plan imports a chat by
// anchors that is not finished yet; then adds the chats the plan does not list.
func (d *historyDriver) gatherAnchors(ctx context.Context) error {
	p, ok, err := loadHistoryPlan(d.data, d.source)
	if err != nil || !ok {
		return halt(false, "plan_gone")
	}
	cache, err := loadHistoryAnchors(d.data, d.source)
	if err != nil {
		return halt(true, "anchors_unreadable")
	}
	need := !cache.Done && p.wantsUnlisted()
	for _, c := range p.importing() {
		if c.effective() == kb.HistoryAnchor && !historyChatDone(d.data, kb.ChatID(c.filing(d.source), c.ID), kb.HistoryAnchor) {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	if !cache.Done {
		e, err := d.eng(ctx)
		if err != nil {
			return halt(false, "kb_unavailable")
		}
		if len(cache.Years) == 0 {
			cache.Years = historyYears(d.now(), e.TextCutoff(), e.Store.Zone())
		}
		for i := range cache.Years {
			if err := d.gatherYear(ctx, cache, &cache.Years[i]); err != nil {
				return err
			}
		}
		cache.Done = true
		if err := d.saveAnchors(cache); err != nil {
			return err
		}
	}
	return d.addUnlisted(cache)
}

// gatherYear pages through one year's anchors.
func (d *historyDriver) gatherYear(ctx context.Context, cache *historyAnchorsCache, y *historyAnchorYear) error {
	for !y.Done {
		q := connectorwire.HistoryAnchorsQuery{Since: y.Since, Until: y.Until, Cursor: y.Cursor, Limit: d.pacer.pace.PageLimit}
		var ans connectorwire.HistoryAnchors
		c, err := d.call(ctx, "history_anchors", d.source, "", false, func(cctx context.Context) (int, string, string, error) {
			a, err := d.src.Anchors(cctx, d.source, q)
			ans = a
			res := "ok"
			if a.Pace != nil {
				res = "paced"
			}
			return len(a.Anchors), res, paceText(a.Pace), err
		})
		if err != nil {
			return err
		}
		switch {
		case c.err != nil:
			o, code := c.classify()
			switch o {
			case historyPauseIt:
				if c.stop != nil {
					return c.stop
				}
				if err := d.pause(ctx, d.pauseWait(code), pauseFor(code)); err != nil {
					return err
				}
				continue
			case historyStopIt:
				return haltWhy(true, c.stopWhy(code))
			}
			y.Done = true // the search refused this window: the year is not covered, its chats are read backwards
		case ans.Pace != nil && ans.Pace.Stop:
			return historySourceStop(ans.Pace)
		case ans.Pace != nil:
			if c.stop != nil {
				return c.stop
			}
			if err := d.pause(ctx, d.pauseWait(paceText(ans.Pace)), pauseFor(paceText(ans.Pace))); err != nil {
				return err
			}
			continue
		default:
			for _, a := range ans.Anchors {
				if a.Chat == "" || a.Position == "" {
					continue
				}
				if a.Message != nil { // identity hints are kept in memory only
					m := *a.Message
					m.SenderHandles = nil
					a.Message = &m
				}
				cache.Anchors = append(cache.Anchors, a)
				y.Hits++
			}
			if !ans.More || ans.Next == "" || ans.Next == y.Cursor {
				y.Done = true
			} else {
				y.Cursor = ans.Next
			}
		}
		if err := d.saveAnchors(cache); err != nil {
			return err
		}
		if err := d.after(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// addUnlisted adds the chats anchors were found in that the plan does not list. Ones that would be imported (their
// default is not off) take the plan's approval back, as new chats in a refreshed list do: the run ends, the owner is
// told, and nothing of them is read until the owner approves the plan with them in it.
func (d *historyDriver) addUnlisted(cache *historyAnchorsCache) error {
	p, ok, err := loadHistoryPlan(d.data, d.source)
	if err != nil || !ok {
		return halt(false, "plan_gone")
	}
	changed, importing := false, 0
	for _, a := range cache.Anchors {
		filed := a.Source
		if filed == d.source {
			filed = ""
		}
		if p.addUnlisted(a.Chat, filed) {
			changed = true
			if p.chat(a.Chat).effective() != historyOff {
				importing++
			}
		}
	}
	if !changed {
		return nil
	}
	if importing > 0 {
		p.ApprovedAt = nil
	}
	if err := saveHistoryPlan(d.data, p); err != nil {
		return halt(true, "plan_save_failed", "class", errClass(err))
	}
	if importing > 0 {
		return halt(false, "plan_changed", "count", strconv.Itoa(importing))
	}
	return nil
}

// initPositions starts a chat's anchor import from the anchors gathered: by position, or (positions that are not
// numbers) through the filter. Nothing to read at all: done.
func (d *historyDriver) initPositions(w historyWork, st *kb.HistoryState) {
	cache, err := loadHistoryAnchors(d.data, d.source)
	if err != nil {
		cache = &historyAnchorsCache{}
	}
	ps := &kb.PositionState{Fallback: cache.uncovered()}
	seen := map[int64]bool{}
	for _, a := range cache.Anchors {
		if a.Chat != w.chat.ID {
			continue
		}
		p, err := strconv.ParseInt(a.Position, 10, 64)
		if err != nil || p < 0 {
			st.Filter = &kb.AnchorFilterState{} // not numbers: read backwards through the filter
			return
		}
		if !seen[p] {
			seen[p] = true
			ps.Anchors = append(ps.Anchors, kb.PositionAnchor{Pos: p, At: a.At})
		}
	}
	sort.Slice(ps.Anchors, func(i, j int) bool { return ps.Anchors[i].Pos < ps.Anchors[j].Pos })
	st.Positions = ps
	if len(ps.Anchors) == 0 && len(ps.Fallback) == 0 {
		st.State = kb.HistoryDone
	}
}

// historyWindow is a merged window of positions, inclusive.
type historyWindow struct{ lo, hi int64 }

// historyWindows are the anchors' windows (AnchorSpan on each side), merged where they overlap or touch, oldest first.
func historyWindows(anchors []kb.PositionAnchor) []historyWindow {
	var out []historyWindow
	for _, a := range anchors { // sorted by position
		w := historyWindow{max(0, a.Pos-kb.AnchorSpan), a.Pos + kb.AnchorSpan}
		if n := len(out); n > 0 && w.lo <= out[n-1].hi+1 {
			out[n-1].hi = max(out[n-1].hi, w.hi)
			continue
		}
		out = append(out, w)
	}
	return out
}

// historyCover adds [lo, hi] to the merged ranges have.
func historyCover(have [][2]int64, lo, hi int64) [][2]int64 {
	if hi < lo {
		return have
	}
	have = append(have, [2]int64{lo, hi})
	sort.Slice(have, func(i, j int) bool { return have[i][0] < have[j][0] })
	out := have[:1]
	for _, r := range have[1:] {
		last := &out[len(out)-1]
		if r[0] <= last[1]+1 || last[1] == math.MaxInt64 {
			last[1] = max(last[1], r[1])
			continue
		}
		out = append(out, r)
	}
	return out
}

func historyCovered(have [][2]int64, p int64) bool {
	for _, r := range have {
		if p >= r[0] && p <= r[1] {
			return true
		}
	}
	return false
}

// historyGap is the newest part of w that have does not cover (false: w is covered).
func historyGap(have [][2]int64, w historyWindow) (int64, int64, bool) {
	hi := w.hi
	for hi >= w.lo {
		if !historyCovered(have, hi) {
			lo := hi
			for lo > w.lo && !historyCovered(have, lo-1) {
				lo--
			}
			return lo, hi, true
		}
		for _, r := range have { // jump below the range covering hi
			if hi >= r[0] && hi <= r[1] {
				hi = r[0] - 1
				break
			}
		}
	}
	return 0, 0, false
}

func (d *historyDriver) stepPositional(ctx context.Context, e *kb.Engine, w historyWork, st *kb.HistoryState) (bool, error) {
	ps := st.Positions
	if ps.WindowsDone {
		return d.stepFallback(ctx, e, w, st)
	}
	windows := historyWindows(ps.Anchors)
	var a, b int64
	found := false
	for i := len(windows) - 1; i >= 0 && !found; i-- {
		a, b, found = historyGap(ps.Have, windows[i])
	}
	if !found {
		if err := d.markGaps(ctx, e, w, ps); err != nil {
			return false, err
		}
		ps.WindowsDone = true
		done := len(ps.Fallback) == 0
		if done {
			st.State = kb.HistoryDone
		}
		return done, d.saveState(e, w, st)
	}
	var q connectorwire.HistoryQuery
	for i := len(ps.Anchors) - 1; i >= 0; i-- {
		if p := ps.Anchors[i].Pos; p >= a && p <= b && !historyCovered(ps.Have, p) {
			q.Around = strconv.FormatInt(p, 10)
			break
		}
	}
	if q.Around == "" {
		if a > 0 && historyCovered(ps.Have, a-1) {
			q.After = strconv.FormatInt(a-1, 10)
		} else {
			q.Before = strconv.FormatInt(b+1, 10)
		}
	}
	page, c, callErr := d.page(ctx, w, q)
	if handled, done, err := d.answered(ctx, e, w, st, page, c, callErr, func() error { return d.keepAnchorHits(ctx, e, w) }); handled {
		return done, err
	}
	before := slices.Clone(ps.Have)
	lo, hi := int64(math.MaxInt64), int64(-1)
	var parsed []kb.SourceMessage
	var pos []int64
	for _, m := range page.Messages {
		p, err := strconv.ParseInt(m.Position, 10, 64)
		if err != nil || p < 0 {
			continue
		}
		parsed, pos = append(parsed, m), append(pos, p)
		lo, hi = min(lo, p), max(hi, p)
		if historyAnchor(m) && !slices.ContainsFunc(ps.Anchors, func(x kb.PositionAnchor) bool { return x.Pos == p }) {
			ps.Anchors = append(ps.Anchors, kb.PositionAnchor{Pos: p, At: m.At})
		}
	}
	sort.Slice(ps.Anchors, func(i, j int) bool { return ps.Anchors[i].Pos < ps.Anchors[j].Pos })
	for i, p := range pos {
		if p == 0 {
			ps.Start = parsed[i].At
		}
	}
	// Covered is what was kept: the page's positions inside the windows as they are now (with the anchors it brought).
	// A position read outside them is not, so a window an anchor found later widens over it is read there again.
	windows = historyWindows(ps.Anchors)
	if hi >= 0 {
		for _, w := range windows {
			ps.Have = historyCover(ps.Have, max(lo, w.lo), min(hi, w.hi))
		}
		if !page.More || page.Older == "" { // nothing older than what came
			ps.Have = historyCover(ps.Have, 0, lo-1)
		}
		if page.Newer == "" && q.Before == "" { // nothing newer
			ps.Have = historyCover(ps.Have, hi+1, math.MaxInt64)
		}
	}
	switch {
	case hi < 0 && q.Before != "": // nothing older than b+1
		ps.Have = historyCover(ps.Have, 0, b)
	case hi < 0 && q.After != "": // nothing newer than a-1
		ps.Have = historyCover(ps.Have, a, math.MaxInt64)
	}
	if slices.Equal(before, ps.Have) { // nothing new came: the gap is given up, not asked for forever
		ps.Have = historyCover(ps.Have, a, b)
	}
	edges := map[int64]bool{}
	for _, x := range ps.Anchors {
		edges[x.Pos-kb.AnchorSpan], edges[x.Pos+kb.AnchorSpan] = true, true
	}
	var kept []kb.SourceMessage
	for i := range parsed {
		p := pos[i]
		if !slices.ContainsFunc(windows, func(w historyWindow) bool { return p >= w.lo && p <= w.hi }) {
			continue
		}
		kept = append(kept, parsed[i])
		if edges[p] {
			if ps.Edges == nil {
				ps.Edges = map[int64]time.Time{}
			}
			ps.Edges[p] = parsed[i].At
		}
	}
	added, err := d.write(ctx, e, w, kept, kb.HistoryAnchor)
	if err != nil {
		return false, err
	}
	st.Read += len(page.Messages)
	st.Written += added
	st.State, st.LastError = kb.HistoryRunning, ""
	if err := d.saveState(e, w, st); err != nil {
		return false, err
	}
	if h := d.takeHalt(); h != nil {
		return false, h
	}
	return false, d.after(ctx, c)
}

// markGaps writes the omission lines between the chat's windows and below the oldest, counted from the positions
// they leave out. Nothing when nothing of the chat was kept (no chat is made out of omission lines).
func (d *historyDriver) markGaps(ctx context.Context, e *kb.Engine, w historyWork, ps *kb.PositionState) error {
	if _, ok := e.Store.Chat(w.id); !ok {
		return nil
	}
	windows := historyWindows(ps.Anchors)
	var lines []kb.SourceMessage
	for i := 1; i < len(windows); i++ {
		if n := windows[i].lo - windows[i-1].hi - 1; n > 0 {
			// After the older window's newest message, so it sorts between the two.
			if at := historyEdgeAt(ps, windows[i-1], false); !at.IsZero() {
				lines = append(lines, kb.OmissionLine(at, int(n)))
			}
		}
	}
	if len(windows) > 0 && windows[0].lo > 0 {
		// Before the oldest window: the minute before its oldest message.
		if at := historyEdgeAt(ps, windows[0], true); !at.IsZero() {
			lines = append(lines, kb.OmissionLine(at.Truncate(time.Minute).Add(-time.Second), int(windows[0].lo)))
		}
	}
	_, err := d.write(ctx, e, w, lines, kb.HistoryAnchor)
	return err
}

// historyEdgeAt is the time of a window's oldest (low) or newest message: the one read at its edge, else its
// outermost anchor's.
func historyEdgeAt(ps *kb.PositionState, w historyWindow, low bool) time.Time {
	edge := w.hi
	if low {
		edge = w.lo
	}
	if t, ok := ps.Edges[edge]; ok {
		return t
	}
	var at time.Time
	for _, a := range ps.Anchors { // sorted by position
		if a.Pos < w.lo || a.Pos > w.hi {
			continue
		}
		if low {
			return a.At
		}
		at = a.At
	}
	return at
}

// keepAnchorHits writes what the search returned of an unreadable chat: the anchors' own messages.
func (d *historyDriver) keepAnchorHits(ctx context.Context, e *kb.Engine, w historyWork) error {
	cache, err := loadHistoryAnchors(d.data, d.source)
	if err != nil {
		return nil
	}
	var msgs []kb.SourceMessage
	for _, a := range cache.Anchors {
		if a.Chat == w.chat.ID && a.Message != nil {
			m := *a.Message
			if m.At.IsZero() {
				m.At = a.At
			}
			m.Mine = m.Mine || a.Mine
			msgs = append(msgs, m)
		}
	}
	saved := d.info.Media
	d.info.Media = false // the chat cannot be read: nor can its images
	_, err = d.write(ctx, e, w, msgs, kb.HistoryAnchor)
	d.info.Media = saved
	return err
}

// probeStart asks for the start of the conversation (one page around position 0) before any uncovered year is read
// backwards, so the years before it began are never streamed through: with a long text retention, every empty year
// would otherwise read the whole chat below its windows down to its first message.
func (d *historyDriver) probeStart(ctx context.Context, e *kb.Engine, w historyWork, st *kb.HistoryState) (bool, error) {
	ps := st.Positions
	page, c, callErr := d.page(ctx, w, connectorwire.HistoryQuery{Around: "0", Limit: 1})
	if handled, done, err := d.answered(ctx, e, w, st, page, c, callErr, func() error { return d.keepAnchorHits(ctx, e, w) }); handled {
		return done, err
	}
	var oldest time.Time
	for _, m := range page.Messages {
		if m.Position == "0" {
			oldest = m.At
			break
		}
		if !m.At.IsZero() && (oldest.IsZero() || m.At.Before(oldest)) && page.AtStart() {
			oldest = m.At
		}
	}
	ps.Start, ps.Probed = oldest, true
	st.Read += len(page.Messages)
	st.State, st.LastError = kb.HistoryRunning, ""
	if err := d.saveState(e, w, st); err != nil {
		return false, err
	}
	return false, d.after(ctx, c)
}

// historyYearBounds is the year's [start, end) in zone.
func historyYearBounds(y int, zone *time.Location) (time.Time, time.Time) {
	return time.Date(y, 1, 1, 0, 0, 0, 0, zone), time.Date(y+1, 1, 1, 0, 0, 0, 0, zone)
}

// historyStart is where a year's backward read starts: below the newest-reaching range already read around an anchor
// newer than the year ("" the newest page).
func historyStart(ps *kb.PositionState, end time.Time) string {
	best := int64(-1)
	for _, a := range ps.Anchors {
		if a.At.Before(end) {
			continue
		}
		for _, r := range ps.Have {
			if a.Pos >= r[0] && a.Pos <= r[1] && (best < 0 || r[0] < best) {
				best = r[0]
			}
		}
	}
	if best < 0 {
		return ""
	}
	return strconv.FormatInt(best, 10)
}

// stepFallback reads an uncovered year of the chat backwards through the anchor filter (only that year's messages
// go into it), then the next uncovered year; the chat is done when none is left.
func (d *historyDriver) stepFallback(ctx context.Context, e *kb.Engine, w historyWork, st *kb.HistoryState) (bool, error) {
	ps := st.Positions
	zone := e.Store.Zone()
	if ps.Year == 0 && len(ps.Fallback) > 0 && ps.Start.IsZero() && !ps.Probed {
		return d.probeStart(ctx, e, w, st)
	}
	if ps.Year == 0 {
		// Years that end before the conversation began have nothing of it: not read.
		if !ps.Start.IsZero() {
			ps.Fallback = slices.DeleteFunc(ps.Fallback, func(y int) bool {
				_, end := historyYearBounds(y, zone)
				return !end.After(ps.Start)
			})
		}
		if len(ps.Fallback) == 0 {
			st.State = kb.HistoryDone
			return true, d.saveState(e, w, st)
		}
		ps.Year, ps.Fallback = ps.Fallback[0], ps.Fallback[1:]
		st.Filter = &kb.AnchorFilterState{}
		if st.Cursor == "" {
			_, end := historyYearBounds(ps.Year, zone)
			st.Cursor = historyStart(ps, end)
		}
		if err := d.saveState(e, w, st); err != nil {
			return false, err
		}
	}
	start, end := historyYearBounds(ps.Year, zone)
	page, c, callErr := d.page(ctx, w, connectorwire.HistoryQuery{Before: st.Cursor})
	if handled, done, err := d.answered(ctx, e, w, st, page, c, callErr, func() error { return d.keepAnchorHits(ctx, e, w) }); handled {
		return done, err
	}
	msgs := page.Messages
	cut := e.TextCutoff()
	var in []kb.SourceMessage
	older := false
	for _, m := range msgs {
		switch {
		case m.At.Before(start) || m.At.Before(cut):
			older = true
		case m.At.Before(end):
			in = append(in, m)
		}
	}
	atStart := page.AtStart() || page.Older == "" || historyAtZero(msgs) || historyBefore(msgs, cut)
	if !atStart && !older && page.Older == st.Cursor {
		_, done, err := d.outcome(ctx, e, w, st, c, historyChatError, "cursor_stuck", nil)
		return done, err
	}
	f := kb.NewAnchorFilter(historyAnchor, 0, st.Filter)
	out := f.Push(in)
	switch {
	case older || atStart: // the year is read; the next one starts from this page again (or nothing is older)
		out = append(out, f.Flush()...)
		ps.Year, st.Filter = 0, nil
		if atStart {
			ps.Fallback, st.Cursor = nil, ""
		}
	default:
		fs := f.State()
		st.Filter, st.Cursor = &fs, page.Older
	}
	added, err := d.write(ctx, e, w, out, kb.HistoryAnchor)
	if err != nil {
		return false, err
	}
	st.Read += len(msgs)
	st.Written += added
	st.State, st.LastError = kb.HistoryRunning, ""
	done := ps.Year == 0 && len(ps.Fallback) == 0
	if done {
		st.State = kb.HistoryDone
	}
	if err := d.saveState(e, w, st); err != nil {
		return false, err
	}
	if h := d.takeHalt(); h != nil {
		return false, h
	}
	return done, d.after(ctx, c)
}
