package assistant

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
)

// The assistant's tools for the history import (docs/assistant/knowledge.md "History import"): list and change a
// source's plan, see how far it got, stop it, approve and resume it. Listing a plan never starts anything: only
// kb_history_resume does, when the owner says to start.

// historyListed is how many chats each group of a listed plan shows (the rest are counted).
const (
	historyListedFull   = 30
	historyListedAnchor = 30
	historyListedOff    = 20
)

func (s *Service) toolKBHistory(ctx context.Context, name string, args json.RawMessage) (string, error) {
	l := s.lang(ctx)
	e, err := s.kbGet(ctx)
	if err != nil {
		return "", err
	}
	data := e.Store.Dir
	set := s.kbSettings(ctx)
	var cfg *assistantwire.KBHistory
	if set != nil {
		cfg = set.History
	}
	switch name {
	case "kb_history_status":
		var a struct{ Source string }
		_ = json.Unmarshal(args, &a)
		return s.historyStatusText(ctx, l, data, cfg, a.Source), nil
	case "kb_history_stop":
		var a struct{ Reason string }
		_ = json.Unmarshal(args, &a)
		if e.Store.ReadOnly() {
			return "", kb.ErrReadOnly
		}
		return s.historyStop(ctx, l, data, a.Reason)
	}
	if e.Store.ReadOnly() {
		return "", kb.ErrReadOnly
	}
	if !cfg.On() {
		return i18n.T(l, "assistant-kb_history.off"), nil
	}
	switch name {
	case "kb_history_plan":
		var a struct {
			Source string `json:"source"`
			Set    []struct {
				ID   string  `json:"id"`
				Mode *string `json:"mode"`
			} `json:"set"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		sources, msg := historySources(l, cfg, a.Source)
		if msg != "" {
			return msg, nil
		}
		if len(a.Set) > 0 {
			if len(sources) != 1 {
				return i18n.T(l, "assistant-kb_history.set_needs_source"), nil
			}
			changes := map[string]string{}
			for _, c := range a.Set {
				mode := ""
				if c.Mode != nil {
					mode = *c.Mode
				}
				changes[c.ID] = mode
			}
			return s.historySetModes(ctx, l, data, sources[0], changes)
		}
		var b strings.Builder
		for i, src := range sources {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s.historyPlanText(ctx, l, data, src))
		}
		return b.String(), nil
	case "kb_history_resume":
		var a struct {
			Source string `json:"source"`
			Slow   bool   `json:"slow"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Source == "" {
			return i18n.T(l, "assistant-kb_history.resume_needs_source"), nil
		}
		sources, msg := historySources(l, cfg, a.Source)
		if msg != "" {
			return msg, nil
		}
		return s.historyResume(ctx, l, data, sources[0], a.Slow)
	}
	return "", fmt.Errorf("no tool %q", name)
}

// historySources are the configured sources a call names (all of them for ""), or a message saying why none.
func historySources(l i18n.Lang, cfg *assistantwire.KBHistory, source string) ([]assistantwire.KBHistorySource, string) {
	if len(cfg.Sources) == 0 {
		return nil, i18n.T(l, "assistant-kb_history.no_sources")
	}
	if source == "" {
		return cfg.Sources, ""
	}
	if src, ok := cfg.Source(source); ok {
		return []assistantwire.KBHistorySource{src}, ""
	}
	var names []string
	for _, s := range cfg.Sources {
		names = append(names, s.Name)
	}
	return nil, i18n.T(l, "assistant-kb_history.not_listed", i18n.Args{"source": source, "sources": strings.Join(names, ", ")})
}

// historyBusyElsewhere: the import runs now, in this process or another (a fresh lease): the list is not refreshed
// then, so a tool's call never comes between the import's paced ones.
func (s *Service) historyBusyElsewhere(data string) bool {
	if s.kb.history.running.Load() != nil {
		return true
	}
	lease, ok := readHistoryLease(data)
	return ok && time.Since(lease.Heartbeat) < historyLeaseStale
}

// historyPlanText refreshes the source's plan from its list (unless the import runs or is stopped) and lists it.
func (s *Service) historyPlanText(ctx context.Context, l i18n.Lang, data string, src assistantwire.KBHistorySource) string {
	var notes []string
	p, ok, err := loadHistoryPlan(data, src.Name)
	if err != nil {
		return i18n.T(l, "assistant-kb_history.plan_unreadable", i18n.Args{"source": src.Name})
	}
	if !ok {
		p = &historyPlan{Source: src.Name, Defaults: map[string]string{}}
	}
	_, stopped := readHistoryStop(data)
	status := readHistoryStatus(data)[src.Name]
	switch {
	case stopped:
		notes = append(notes, i18n.T(l, "assistant-kb_history.refresh_stopped"))
	case s.historyBusyElsewhere(data):
		notes = append(notes, i18n.T(l, "assistant-kb_history.refresh_running"))
	case status.Pace != nil && status.Pace.NotBefore.After(time.Now()): // a rest or a pause the source asked for
		notes = append(notes, i18n.T(l, "assistant-kb_history.refresh_waiting", i18n.Args{"at": status.Pace.NotBefore.In(s.location(ctx)).Format("2006-01-02 15:04")}))
	default:
		added, err := s.historyRefresh(ctx, data, src.Name, p)
		var stop *historyHalt
		switch {
		case errors.Is(err, errHistoryUnavailable):
			return i18n.T(l, "assistant-kb_history.unavailable", i18n.Args{"source": src.Name})
		case errors.As(err, &stop):
			notes = append(notes, i18n.T(l, "assistant-kb_history.refresh_stopped_now", i18n.Args{"reason": historyReasonText(l, stop.why)}))
		case err != nil:
			notes = append(notes, i18n.T(l, "assistant-kb_history.refresh_failed", i18n.Args{"code": historyErrCode(err)}))
		case added > 0 && p.ApprovedAt != nil:
			p.ApprovedAt = nil // new chats to import: the owner sees them before anything runs
			notes = append(notes, i18n.T(l, "assistant-kb_history.plan_changed", i18n.Args{"count": added}))
		}
		if err == nil {
			if err := saveHistoryPlan(data, p); err != nil {
				return i18n.T(l, "assistant-kb_history.plan_unreadable", i18n.Args{"source": src.Name})
			}
		}
	}
	if len(p.Chats) == 0 && p.GeneratedAt.IsZero() {
		return strings.Join(append(notes, i18n.T(l, "assistant-kb_history.no_plan", i18n.Args{"source": src.Name})), "\n")
	}
	loc := s.location(ctx)
	var b strings.Builder
	b.WriteString(i18n.T(l, "assistant-kb_history.plan_header", i18n.Args{"source": src.Name, "chats": len(p.Chats),
		"generated": p.GeneratedAt.In(loc).Format("2006-01-02 15:04")}))
	b.WriteString("\n")
	if p.ApprovedAt != nil {
		b.WriteString(i18n.T(l, "assistant-kb_history.plan_approved", i18n.Args{"at": p.ApprovedAt.In(loc).Format("2006-01-02 15:04")}))
	} else {
		b.WriteString(i18n.T(l, "assistant-kb_history.plan_not_approved"))
	}
	b.WriteString("\n")
	for _, n := range notes {
		b.WriteString(n + "\n")
	}
	groups := p.grouped()
	for _, g := range []struct {
		mode, key string
		most      int
	}{{kb.HistoryFull, "group_full", historyListedFull}, {kb.HistoryAnchor, "group_anchor", historyListedAnchor}, {historyOff, "group_off", historyListedOff}} {
		list := groups[g.mode]
		total := 0
		for _, c := range list {
			total += c.Estimate
		}
		b.WriteString("\n" + i18n.T(l, "assistant-kb_history."+g.key, i18n.Args{"count": len(list), "messages": total}) + "\n")
		for i, c := range list {
			if i == g.most {
				b.WriteString(i18n.T(l, "assistant-kb_history.more", i18n.Args{"count": len(list) - g.most}) + "\n")
				break
			}
			b.WriteString(historyChatLine(l, c) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func historyChatLine(l i18n.Lang, c historyPlanChat) string {
	kind := i18n.T(l, "assistant-kb_history.kind_other")
	switch c.Kind {
	case "dm", "group", "bot", "official", "system", historyUnlisted:
		kind = i18n.T(l, "assistant-kb_history.kind_"+c.Kind)
	}
	if c.Muted {
		kind += i18n.T(l, "assistant-kb_history.muted")
	}
	name := c.Name
	if name == "" {
		name = "?"
	}
	key := "assistant-kb_history.chat_line"
	if c.Mode != nil {
		key = "assistant-kb_history.chat_line_set"
	}
	return i18n.T(l, key, i18n.Args{"name": name, "kind": kind, "estimate": c.Estimate, "id": c.ID})
}

var errHistoryUnavailable = errors.New("the connector does not offer its history now")

// historyRefresh lays the source's list over p: one call, logged, never while another is in flight, and treated as the
// driver treats its own: the chat turn ending does not cancel it (only its own timeout does), and a timeout, a closed
// connection or a code it does not know stops the whole import (STOP, a push; the error is a *historyHalt) so that
// nothing, not even the next kb_history_plan, asks again until a person looked.
func (s *Service) historyRefresh(ctx context.Context, data, source string, p *historyPlan) (int, error) {
	d := s.historyDriver(data)
	d.source = source
	list, err := d.src.Archives(ctx)
	if err != nil {
		return 0, err
	}
	available := false
	for _, a := range list {
		available = available || a.ID == source && a.History
	}
	if !available {
		return 0, errHistoryUnavailable
	}
	key, err := historyLogKey(data)
	if err != nil {
		return 0, err
	}
	d.key, d.zone = key, s.location(ctx)
	d.calls.Lock()
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.timeout)
	start := d.now()
	chats, err := d.src.Chats(cctx, source)
	took := d.now().Sub(start)
	if err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded) {
		err = context.DeadlineExceeded
	}
	cancel()
	d.calls.Unlock()
	line := historyLogLine{Method: "history_chats", MS: took.Milliseconds(), Result: historyErrCode(err), N: len(chats)}
	d.log(line)
	line.At, line.Source = d.now().UTC(), source
	d.setStatus(source, func(st *historySourceStatus) { st.LastCall = &line; st.Calls++ })
	if took > historyVerySlow {
		why := reason("very_slow", "method", "history_chats", "ms", strconv.FormatInt(took.Milliseconds(), 10), "limit",
			strconv.FormatInt(historyVerySlow.Milliseconds(), 10), "minutes", "0")
		why.Code = "very_slow_list"
		d.log(historyLogLine{Event: "slow", historyReason: why})
		d.setStatus(source, func(st *historySourceStatus) { st.LastSlow = &historyEvent{At: d.now().UTC(), historyReason: why} })
	}
	if err != nil {
		if o, code := classify(err); o == historyStopIt {
			why := historyCall{method: "history_chats"}.stopWhy(code)
			d.stopAll(context.WithoutCancel(ctx), why)
			return 0, haltWhy(true, why)
		}
		return 0, err
	}
	p.Source = source
	return p.regenerate(chats, time.Now()), nil
}

// historySetModes changes chats' modes in a source's plan (the approval stays: the owner asked for it).
func (s *Service) historySetModes(ctx context.Context, l i18n.Lang, data string, src assistantwire.KBHistorySource, changes map[string]string) (string, error) {
	p, ok, err := loadHistoryPlan(data, src.Name)
	if err != nil {
		return "", err
	}
	if !ok {
		return i18n.T(l, "assistant-kb_history.no_plan", i18n.Args{"source": src.Name}), nil
	}
	n := 0
	var unknown []string
	for id, mode := range changes {
		if err := p.set(id, mode); err != nil {
			if p.chat(id) == nil {
				unknown = append(unknown, id)
				continue
			}
			return "", err
		}
		n++
	}
	if err := saveHistoryPlan(data, p); err != nil {
		return "", err
	}
	out := i18n.T(l, "assistant-kb_history.set_done", i18n.Args{"count": n})
	if len(unknown) > 0 {
		out += "\n" + i18n.T(l, "assistant-kb_history.set_unknown", i18n.Args{"ids": strings.Join(unknown, ", ")})
	}
	return out, nil
}

// historyStop writes STOP: the import stops before its next call (the one in flight finishes).
func (s *Service) historyStop(ctx context.Context, l i18n.Lang, data, words string) (string, error) {
	if st, stopped := readHistoryStop(data); stopped {
		return i18n.T(l, "assistant-kb_history.stop_already", i18n.Args{"reason": historyReasonText(l, st.historyReason)}), nil
	}
	// The owner's words stay in STOP and the status, where the owner reads them; the log gets only that it was asked
	// (they may name a chat or a person).
	why := reason("asked")
	if text := clipRunes(strings.TrimSpace(words), 200); text != "" {
		why.Text = text
	}
	now := time.Now().UTC()
	if err := writeHistoryStop(data, historyStop{At: now, historyReason: why, By: "tool"}); err != nil {
		return "", err
	}
	_ = appendHistoryLog(data, s.location(ctx), historyLogLine{At: now, Event: "stop", historyReason: why})
	if running := s.kb.history.running.Load(); running != nil {
		updateHistoryStatus(data, *running, func(st *historySourceStatus) { st.LastStop = &historyEvent{At: now, historyReason: why} })
	}
	return i18n.T(l, "assistant-kb_history.stop_done"), nil
}

// historyResume approves the source's plan, removes STOP and, with slow, keeps the slowest pace for two hours.
func (s *Service) historyResume(ctx context.Context, l i18n.Lang, data string, src assistantwire.KBHistorySource, slow bool) (string, error) {
	p, ok, err := loadHistoryPlan(data, src.Name)
	if err != nil {
		return "", err
	}
	if !ok || len(p.Chats) == 0 {
		return i18n.T(l, "assistant-kb_history.resume_no_plan", i18n.Args{"source": src.Name}), nil
	}
	now := time.Now().UTC()
	p.ApprovedAt = &now
	if err := saveHistoryPlan(data, p); err != nil {
		return "", err
	}
	if err := removeHistoryStop(data); err != nil {
		return "", err
	}
	updateHistoryStatus(data, src.Name, func(st *historySourceStatus) {
		// slow: the slowest pace for two hours from the first call (it may come later: a quiet period, a rest).
		st.SlowUntil, st.SlowPending = time.Time{}, slow
		if st.Pace != nil { // a person looked: the two hours of pausing count from now; rests asked for still hold
			st.Pace.PausedSince = time.Time{}
		}
		if st.State == "stopped" {
			st.State = "idle"
		}
	})
	_ = appendHistoryLog(data, s.location(ctx), historyLogLine{At: now, Source: src.Name, Event: "resume",
		historyReason: reason("resume", "pace", map[bool]string{true: "slow", false: "normal"}[slow])})
	s.kbHistoryWake()
	key := "assistant-kb_history.resume_done"
	if slow {
		key = "assistant-kb_history.resume_done_slow"
	}
	out := i18n.T(l, key, i18n.Args{"source": src.Name})
	if src.SearchIndexNeeded() && !historySearchIndexed {
		out += "\n" + i18n.T(l, "assistant-kb_history.needs_index")
	}
	return out, nil
}

// historyStatusText is each configured source's state, progress, pace and last events.
func (s *Service) historyStatusText(ctx context.Context, l i18n.Lang, data string, cfg *assistantwire.KBHistory, only string) string {
	switch {
	case cfg == nil:
		return i18n.T(l, "assistant-kb_history.off")
	case len(cfg.Sources) == 0 && !cfg.On():
		return i18n.T(l, "assistant-kb_history.off") + "\n" + i18n.T(l, "assistant-kb_history.no_sources")
	case len(cfg.Sources) == 0:
		return i18n.T(l, "assistant-kb_history.no_sources")
	}
	loc := s.location(ctx)
	at := func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") }
	var b strings.Builder
	if !cfg.On() {
		b.WriteString(i18n.T(l, "assistant-kb_history.off") + "\n")
	}
	if st, stopped := readHistoryStop(data); stopped {
		b.WriteString(i18n.T(l, "assistant-kb_history.status_stop_file", i18n.Args{"reason": historyReasonText(l, st.historyReason), "at": at(st.At)}) + "\n")
	}
	if lease, ok := readHistoryLease(data); ok && lease.Instance != s.kb.history.instance && time.Since(lease.Heartbeat) < historyLeaseStale {
		b.WriteString(i18n.T(l, "assistant-kb_history.status_other_process", i18n.Args{"at": at(lease.Heartbeat)}) + "\n")
	}
	statuses := readHistoryStatus(data)
	speed := historySpeed(data, loc, time.Now())
	for _, src := range cfg.Sources {
		if only != "" && src.Name != only {
			continue
		}
		st := statuses[src.Name]
		d := &historyDriver{data: data, indexed: func() bool { return historySearchIndexed }}
		why, p := d.historyBlock(src, nil)
		state := st.State
		switch why {
		case "needs_search_index", "no_plan", "not_approved", "plan_unreadable":
			state = why
		case "done":
			state = "done"
		}
		if state == "" {
			state = "idle"
		}
		b.WriteString("\n" + i18n.T(l, "assistant-kb_history.status_header", i18n.Args{"source": src.Name,
			"state": i18n.T(l, "assistant-kb_history.state_"+historyStateKey(state))}) + "\n")
		if why == "needs_search_index" {
			b.WriteString(i18n.T(l, "assistant-kb_history.needs_index") + "\n")
		}
		if p != nil {
			if p.ApprovedAt != nil {
				b.WriteString(i18n.T(l, "assistant-kb_history.plan_approved", i18n.Args{"at": at(*p.ApprovedAt)}) + "\n")
			} else {
				b.WriteString(i18n.T(l, "assistant-kb_history.plan_not_approved") + "\n")
			}
			b.WriteString(historyProgress(l, data, src.Name, p) + "\n")
		}
		if sp := speed[src.Name]; sp.calls > 0 {
			b.WriteString(i18n.T(l, "assistant-kb_history.status_speed", i18n.Args{"calls": sp.calls, "messages": sp.messages}) + "\n")
		}
		if !st.Until.IsZero() && (state == "resting" || state == "quiet" || state == "paused") {
			b.WriteString(i18n.T(l, "assistant-kb_history.status_until", i18n.Args{"at": at(st.Until)}) + "\n")
		}
		switch {
		case st.SlowPending:
			b.WriteString(i18n.T(l, "assistant-kb_history.status_slow_pending") + "\n")
		case st.SlowUntil.After(time.Now()):
			b.WriteString(i18n.T(l, "assistant-kb_history.status_slow_until", i18n.Args{"at": at(st.SlowUntil)}) + "\n")
		}
		for _, ev := range []struct {
			key string
			e   *historyEvent
		}{{"status_last_slow", st.LastSlow}, {"status_last_pause", st.LastPause}, {"status_last_stop", st.LastStop}} {
			if ev.e != nil {
				b.WriteString(i18n.T(l, "assistant-kb_history."+ev.key, i18n.Args{"reason": historyReasonText(l, ev.e.historyReason), "at": at(ev.e.At)}) + "\n")
			}
		}
		if c := st.LastCall; c != nil {
			key := "assistant-kb_history.status_last_call"
			if c.Chat != "" {
				key = "assistant-kb_history.status_last_call_chat"
			}
			b.WriteString(i18n.T(l, key, i18n.Args{"method": c.Method, "result": c.Result, "ms": c.MS, "at": at(c.At), "chat": c.Chat}) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func historyStateKey(state string) string {
	switch state {
	case "running", "resting", "quiet", "paused", "stopped", "done", "idle", "unavailable", "needs_search_index", "no_plan",
		"not_approved", "plan_unreadable":
		return state
	}
	return "idle"
}

// historyProgress counts the plan's chats by state, with what was read and written, and names a few in error.
func historyProgress(l i18n.Lang, data, source string, p *historyPlan) string {
	var done, todo, paused, failed, unreadable, read, written int
	var errs []string
	for _, c := range p.importing() {
		var hs kb.HistoryState
		ok, _ := readJSONFile(historyStatePathFor(data, kb.ChatID(c.filing(source), c.ID)), &hs)
		if ok {
			read += hs.Read
			written += hs.Written
		}
		switch {
		case !ok || hs.Mode != c.effective():
			todo++
		case hs.State == kb.HistoryDone:
			done++
		case hs.State == kb.HistoryError:
			failed++
			if len(errs) < 5 {
				errs = append(errs, orText(c.Name, c.ID)+" ("+hs.LastError+")")
			}
		case hs.State == kb.HistoryUnreadable:
			unreadable++
		case hs.State == kb.HistoryPaused:
			paused++
		default:
			todo++
		}
	}
	out := i18n.T(l, "assistant-kb_history.status_progress", i18n.Args{"done": done, "todo": todo, "paused": paused, "errors": failed,
		"unreadable": unreadable, "read": read, "written": written})
	if len(errs) > 0 {
		out += "\n" + i18n.T(l, "assistant-kb_history.status_errors", i18n.Args{"chats": strings.Join(errs, ", ")})
	}
	return out
}

type historyRate struct{ calls, messages int }

// historySpeed is each source's calls and messages read over the last hour, from the logs.
func historySpeed(data string, loc *time.Location, now time.Time) map[string]historyRate {
	out := map[string]historyRate{}
	since := now.Add(-time.Hour)
	for _, day := range []time.Time{since, now} {
		f, err := os.Open(filepath.Join(historyDir(data), "log-"+day.UTC().In(loc).Format("2006-01-02")+".jsonl"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var line historyLogLine
			if json.Unmarshal(sc.Bytes(), &line) != nil || line.Method == "" || line.At.Before(since) {
				continue
			}
			r := out[line.Source]
			r.calls++
			if line.Method == "history" {
				r.messages += line.N
			}
			out[line.Source] = r
		}
		f.Close()
		if since.In(loc).Format("2006-01-02") == now.In(loc).Format("2006-01-02") {
			break
		}
	}
	return out
}
