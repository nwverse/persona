package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Watch cards (docs/assistant/watch-cards.md, #13020): the owner says "check X for me every day", the assistant drafts a
// card (watch_draft) and, once the owner agrees, confirms it (watch_confirm). A confirmed card owns one scheduled task
// (Task.WatchID) that the existing scheduler runs; each run answers the card's question and records it with
// watch_report, which keeps the latest value and a short history and pushes when the card's alert rule trips. Cards sit
// at the top of Overview in the owner's order; a card on a knowledge-base project also shows on that project's node.

// watchesKey keeps every card, drafts too, in Overview's order.
const watchesKey = "assistant.watches"

const (
	watchesMax       = 30
	watchHistoryMax  = 30
	watchDraftKeep   = 7 * 24 * time.Hour
	watchTitleMax    = 60
	watchQuestionMax = 500
	watchTextMax     = 200
	watchUnitMax     = 12
	watchTableCols   = 6
	watchTableRows   = 10
	watchCellMax     = 60
	watchTaskPrefix  = "watch-"
)

// The display kinds.
const (
	watchNumber = "number"
	watchStatus = "status"
	watchTable  = "table"
	watchTrend  = "trend"
)

// The card states: a draft waits for the owner's agreement and has no task; an active card has one.
const (
	watchDraft  = "draft"
	watchActive = "active"
)

// The status light a run may report.
const (
	watchOK   = "ok"
	watchWarn = "warn"
	watchBad  = "bad"
)

// WatchAlert is when a run's reading pushes: above / below Value, a bad status, or any change from the last reading.
type WatchAlert struct {
	When  string   `json:"when"`
	Value *float64 `json:"value,omitempty"`
}

// WatchSchedule is when the card refreshes, as its task's rule: daily at Time (on Weekdays), every EveryMinutes, or Cron.
type WatchSchedule struct {
	Repeat       string `json:"repeat"`
	Time         string `json:"time,omitempty"`
	Weekdays     []int  `json:"weekdays,omitempty"`
	EveryMinutes int    `json:"every_minutes,omitempty"`
	Cron         string `json:"cron,omitempty"`
}

// WatchTable is a table reading: a header and up to watchTableRows rows of the same width.
type WatchTable struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// WatchReading is one run's answer.
type WatchReading struct {
	At     time.Time   `json:"at"`
	Number *float64    `json:"number,omitempty"`
	Status string      `json:"status,omitempty"`
	Text   string      `json:"text,omitempty"`
	Table  *WatchTable `json:"table,omitempty"`
	// Alert: the card's rule tripped on this reading (and it was pushed).
	Alert bool `json:"alert,omitempty"`
}

// WatchPoint is a reading as the short history keeps it, for the trend and the light's past.
type WatchPoint struct {
	At     time.Time `json:"at"`
	Number *float64  `json:"number,omitempty"`
	Status string    `json:"status,omitempty"`
	Alert  bool      `json:"alert,omitempty"`
}

// Watch is one card.
type Watch struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Question string        `json:"question"`
	Display  string        `json:"display"`
	Unit     string        `json:"unit,omitempty"`
	Schedule WatchSchedule `json:"schedule"`
	Alert    *WatchAlert   `json:"alert,omitempty"`
	// Project is the knowledge-base project slug the card belongs to; "" none.
	Project string `json:"project,omitempty"`
	State   string `json:"state"`
	Paused  bool   `json:"paused,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	// Latest is the last reading in full; History the last watchHistoryMax readings, oldest first, without text or table.
	Latest      *WatchReading `json:"latest,omitempty"`
	History     []WatchPoint  `json:"history,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	ConfirmedAt time.Time     `json:"confirmed_at,omitzero"`
}

// watchInput is a change as the tools and the app send it: nil leaves a field as it is. Alert is raw so null can clear it.
type watchInput struct {
	Title    *string         `json:"title"`
	Question *string         `json:"question"`
	Schedule *string         `json:"schedule"`
	Display  *string         `json:"display"`
	Unit     *string         `json:"unit"`
	Alert    json.RawMessage `json:"alert"`
	Project  *string         `json:"project"`
	Paused   *bool           `json:"paused"`
}

// watchBook serializes the changes to the cards: each reads the list, changes it and writes it back.
type watchBook struct {
	mu sync.Mutex
}

var (
	errWatchStore    = i18n.Err("assistant-watch.err_store")
	errWatchReadOnly = i18n.Err("assistant-watch.err_read_only")
	errWatchNotFound = i18n.Err("assistant-watch.err_not_found")
	errWatchQuestion = i18n.Err("assistant-watch.err_question", i18n.Args{"max": watchQuestionMax})
	errWatchTitle    = i18n.Err("assistant-watch.err_title", i18n.Args{"max": watchTitleMax})
	errWatchUnit     = i18n.Err("assistant-watch.err_unit", i18n.Args{"max": watchUnitMax})
	errWatchDisplay  = i18n.Err("assistant-watch.err_display")
	errWatchAlert    = i18n.Err("assistant-watch.err_alert")
	errWatchSchedule = i18n.Err("assistant-watch.err_schedule")
	errWatchProject  = i18n.Err("assistant-watch.err_project")
	errWatchFull     = i18n.Err("assistant-watch.err_too_many", i18n.Args{"max": watchesMax})
	errWatchNotDraft = i18n.Err("assistant-watch.err_not_draft")
	errWatchDraft    = i18n.Err("assistant-watch.err_draft")
	errWatchText     = i18n.Err("assistant-watch.err_text", i18n.Args{"max": watchTextMax})
	errWatchNumber   = i18n.Err("assistant-watch.err_number")
	errWatchStatus   = i18n.Err("assistant-watch.err_status")
	errWatchTable    = i18n.Err("assistant-watch.err_table", i18n.Args{"cols": watchTableCols, "rows": watchTableRows})
)

// watchLine: s fits on one line of a card.
func watchLine(s string, max int) bool {
	return utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, unicode.IsControl)
}

func validDisplay(d string) bool {
	return d == watchNumber || d == watchStatus || d == watchTable || d == watchTrend
}

// parseWatchSchedule reads the tools' and the app's schedule: "HH:MM" every day, "30m" / "2h" / "1d" an interval, or a
// 5-field cron expression (or alias) in the owner's zone. Empty is every day at 09:00.
func parseWatchSchedule(v string) (WatchSchedule, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = "09:00"
	}
	if m := hhmm.FindStringSubmatch(v); m != nil {
		return WatchSchedule{Repeat: repeatDaily, Time: pad2(m[1]) + ":" + m[2]}, nil
	}
	if d, ok := relative(v); ok {
		minutes := int(d / time.Minute)
		if d%time.Minute != 0 || minutes < minEvery || minutes > maxEvery {
			return WatchSchedule{}, i18n.Err("assistant-schedule.interval_range", i18n.Args{"min": minEvery})
		}
		return WatchSchedule{Repeat: repeatInterval, EveryMinutes: minutes}, nil
	}
	r, err := parseCron(v)
	if err != nil {
		return WatchSchedule{}, errWatchSchedule
	}
	return WatchSchedule{Repeat: repeatCron, Cron: r.expr}, nil
}

func pad2(h string) string {
	if len(h) == 1 {
		return "0" + h
	}
	return h
}

// parseWatchAlert reads an alert: null (or absent when has is false) is none.
func parseWatchAlert(raw json.RawMessage) (*WatchAlert, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var a WatchAlert
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, errWatchAlert
	}
	switch a.When {
	case "above", "below":
		if a.Value == nil || math.IsNaN(*a.Value) || math.IsInf(*a.Value, 0) {
			return nil, errWatchAlert
		}
	case watchBad, "change":
		a.Value = nil
	case "", "never":
		return nil, nil
	default:
		return nil, errWatchAlert
	}
	return &a, nil
}

// watchProject resolves a project name or slug to its slug; with no knowledge base open, a slug is taken as given.
func (s *Service) watchProject(ctx context.Context, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	e, _ := s.kbGet(ctx)
	if e == nil {
		if kb.Slug(v) != v {
			return "", errWatchProject
		}
		return v, nil
	}
	if p, ok := e.Store.Project(v); ok && p.MergedInto == "" && p.Status != "ignored" {
		return p.Slug, nil
	}
	if p := e.FindProject(v); p != nil {
		return p.Slug, nil
	}
	return "", errWatchProject
}

// apply sets what in changes on w and checks the result.
func (s *Service) applyWatch(ctx context.Context, w *Watch, in watchInput) error {
	if in.Question != nil {
		w.Question = strings.TrimSpace(*in.Question)
	}
	if w.Question == "" || utf8.RuneCountInString(w.Question) > watchQuestionMax {
		return errWatchQuestion
	}
	if in.Title != nil {
		w.Title = strings.TrimSpace(*in.Title)
	}
	if w.Title == "" {
		w.Title = clipRunesTo(oneLine(w.Question), 40)
	}
	if !watchLine(w.Title, watchTitleMax) {
		return errWatchTitle
	}
	if in.Display != nil {
		w.Display = strings.TrimSpace(*in.Display)
	}
	if w.Display == "" {
		w.Display = watchNumber
	}
	if !validDisplay(w.Display) {
		return errWatchDisplay
	}
	if in.Unit != nil {
		w.Unit = strings.TrimSpace(*in.Unit)
	}
	if !watchLine(w.Unit, watchUnitMax) {
		return errWatchUnit
	}
	if in.Schedule != nil || w.Schedule.Repeat == "" {
		v := ""
		if in.Schedule != nil {
			v = *in.Schedule
		}
		sch, err := parseWatchSchedule(v)
		if err != nil {
			return err
		}
		w.Schedule = sch
	}
	if in.Alert != nil {
		a, err := parseWatchAlert(in.Alert)
		if err != nil {
			return err
		}
		w.Alert = a
	}
	if in.Project != nil {
		p, err := s.watchProject(ctx, *in.Project)
		if err != nil {
			return err
		}
		w.Project = p
	}
	if in.Paused != nil {
		w.Paused = *in.Paused
	}
	return nil
}

// watches are the cards in Overview's order, drafts past watchDraftKeep left out.
func (s *Service) watches(ctx context.Context) ([]Watch, error) {
	st := s.store()
	if st == nil {
		return nil, errWatchStore
	}
	var ws []Watch
	if _, err := st.Setting(ctx, watchesKey, &ws); err != nil {
		return nil, errWatchStore
	}
	now := time.Now()
	return slices.DeleteFunc(ws, func(w Watch) bool { return w.State == watchDraft && now.Sub(w.UpdatedAt) > watchDraftKeep }), nil
}

// changeWatches runs change on the cards and keeps what it returns; one change at a time. after runs once the list is
// saved, still in turn.
func (s *Service) changeWatches(ctx context.Context, change func([]Watch) ([]Watch, error)) error {
	s.watchBook.mu.Lock()
	defer s.watchBook.mu.Unlock()
	ws, err := s.watches(ctx)
	if err != nil {
		return err
	}
	next, err := change(slices.Clone(ws))
	if err != nil {
		return err
	}
	if next == nil {
		next = []Watch{}
	}
	if err := s.store().PutSetting(ctx, watchesKey, next); err != nil {
		s.logf("save watches: %v", err)
		return errWatchStore
	}
	s.event(context.Background(), map[string]any{"type": "assistant_watches"})
	return nil
}

func watchIndex(ws []Watch, id string) int {
	return slices.IndexFunc(ws, func(w Watch) bool { return w.ID == id })
}

// draftWatch makes a draft, or revises draft id; a draft has no task and is not refreshed.
func (s *Service) draftWatch(ctx context.Context, id string, in watchInput) (Watch, error) {
	var out Watch
	err := s.changeWatches(ctx, func(ws []Watch) ([]Watch, error) {
		now := time.Now().UTC()
		i := -1
		w := Watch{ID: "w_" + randomHex(4), State: watchDraft, CreatedAt: now}
		if id != "" {
			if i = watchIndex(ws, id); i < 0 {
				return nil, errWatchNotFound
			}
			if ws[i].State != watchDraft {
				return nil, errWatchNotDraft
			}
			w = ws[i]
		} else if len(ws) >= watchesMax {
			return nil, errWatchFull
		}
		in.Paused = nil
		if err := s.applyWatch(ctx, &w, in); err != nil {
			return nil, err
		}
		w.UpdatedAt = now
		out = w
		if i < 0 {
			return append(ws, w), nil
		}
		ws[i] = w
		return ws, nil
	})
	return out, err
}

// confirmWatch puts draft id on Overview, first, and gives it its task.
func (s *Service) confirmWatch(ctx context.Context, id string) (Watch, error) {
	var out Watch
	created := ""
	err := s.changeWatches(ctx, func(ws []Watch) ([]Watch, error) {
		i := watchIndex(ws, id)
		if i < 0 {
			return nil, errWatchNotFound
		}
		w := ws[i]
		if w.State != watchDraft {
			out = w // confirmed already: nothing to do
			return ws, nil
		}
		now := time.Now().UTC()
		w.State, w.ConfirmedAt, w.UpdatedAt, w.TaskID = watchActive, now, now, watchTaskPrefix+w.ID
		if err := s.syncWatchTask(ctx, w); err != nil {
			return nil, err
		}
		created = w.TaskID
		out = w
		return append([]Watch{w}, slices.Delete(ws, i, i+1)...), nil
	})
	if err != nil && created != "" {
		_, _ = s.deleteTask(ctx, created) // the card was not saved: its task is not left behind
	}
	return out, err
}

// updateWatch changes card id; an active card's task follows.
func (s *Service) updateWatch(ctx context.Context, id string, in watchInput) (Watch, error) {
	var out Watch
	err := s.changeWatches(ctx, func(ws []Watch) ([]Watch, error) {
		i := watchIndex(ws, id)
		if i < 0 {
			return nil, errWatchNotFound
		}
		w := ws[i]
		if err := s.applyWatch(ctx, &w, in); err != nil {
			return nil, err
		}
		if w.State == watchDraft && w.Paused {
			return nil, errWatchDraft
		}
		w.UpdatedAt = time.Now().UTC()
		if w.State == watchActive {
			if w.TaskID == "" {
				w.TaskID = watchTaskPrefix + w.ID
			}
			if err := s.syncWatchTask(ctx, w); err != nil {
				return nil, err
			}
		}
		ws[i] = w
		out = w
		return ws, nil
	})
	return out, err
}

// deleteWatch removes card id and its task; false when there is none.
func (s *Service) deleteWatch(ctx context.Context, id string) (bool, error) {
	found := false
	err := s.changeWatches(ctx, func(ws []Watch) ([]Watch, error) {
		i := watchIndex(ws, id)
		if i < 0 {
			return ws, nil
		}
		found = true
		if task := ws[i].TaskID; task != "" {
			if _, err := s.deleteTask(ctx, task); err != nil {
				return nil, err
			}
		}
		return slices.Delete(ws, i, i+1), nil
	})
	return found, err
}

// reorderWatches puts ids first, in that order; the cards it leaves out follow in their order. An unknown id is an error.
func (s *Service) reorderWatches(ctx context.Context, ids []string) error {
	return s.changeWatches(ctx, func(ws []Watch) ([]Watch, error) {
		out := make([]Watch, 0, len(ws))
		for _, id := range ids {
			i := watchIndex(ws, id)
			if i < 0 {
				if watchIndex(out, id) >= 0 {
					continue
				}
				return nil, errWatchNotFound
			}
			out = append(out, ws[i])
			ws = slices.Delete(ws, i, i+1)
		}
		return append(out, ws...), nil
	})
}

// syncWatchTask creates or updates the scheduled task that refreshes w (w.TaskID): its title, prompt, rule and switch
// follow the card; what the scheduler keeps (last run, its conversation) stays. Runs push only what they push
// themselves: the alert is the card's own push (reportWatch).
func (s *Service) syncWatchTask(ctx context.Context, w Watch) error {
	lang := ""
	if l := s.lang(ctx); l != i18n.ZhHans {
		lang = string(l)
	}
	prompt := prompts.RenderWatchRun(prompts.WatchRun{Lang: lang, ID: w.ID, Title: w.Title, Question: w.Question, Display: w.Display, Unit: w.Unit})
	now := time.Now()
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return err
	}
	var old Task
	t := Task{ID: w.TaskID, CreatedBy: "assistant", CreatedAt: now.UTC()}
	i := sc.indexLocked(w.TaskID)
	if i >= 0 {
		old = sc.tasks[i]
		t = old
	}
	t.WatchID, t.Title, t.Prompt, t.Enabled, t.Push = w.ID, w.Title, prompt, !w.Paused, pushAsked
	t.SessionID = "" // the card's own conversation: an asked task never runs in another
	sch := w.Schedule
	t.Repeat, t.Time, t.Weekdays, t.EveryMinutes, t.Cron = sch.Repeat, sch.Time, sch.Weekdays, sch.EveryMinutes, sch.Cron
	if t.Repeat == repeatInterval {
		every := time.Duration(t.EveryMinutes) * time.Minute
		if old.Repeat != repeatInterval || old.EveryMinutes != t.EveryMinutes || !old.At.After(now) || (!old.Enabled && t.Enabled) {
			t.At = now.Add(every)
		}
	}
	if err := checkTask(&t, now); err != nil {
		sc.mu.Unlock()
		return err
	}
	rearm(old, &t, now)
	tasks := slices.Clone(sc.tasks)
	if i < 0 {
		tasks = append(tasks, t)
	} else {
		tasks[i] = t
	}
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		return err
	}
	s.broadcastSchedule()
	return nil
}

// readingIn checks a run's reading (watch_report) and tidies it.
func readingIn(r WatchReading) (WatchReading, error) {
	r.Text = strings.TrimSpace(oneLine(r.Text))
	if r.Text == "" || utf8.RuneCountInString(r.Text) > watchTextMax {
		return r, errWatchText
	}
	if r.Number != nil && (math.IsNaN(*r.Number) || math.IsInf(*r.Number, 0)) {
		return r, errWatchNumber
	}
	switch r.Status {
	case "", watchOK, watchWarn, watchBad:
	default:
		return r, errWatchStatus
	}
	if t := r.Table; t != nil {
		if len(t.Columns) == 0 || len(t.Columns) > watchTableCols || len(t.Rows) > watchTableRows {
			return r, errWatchTable
		}
		for i, c := range t.Columns {
			if t.Columns[i] = strings.TrimSpace(c); !watchLine(t.Columns[i], watchCellMax) {
				return r, errWatchTable
			}
		}
		for i, row := range t.Rows {
			if len(row) > len(t.Columns) {
				return r, errWatchTable
			}
			cells := make([]string, len(t.Columns))
			for j, c := range row {
				if cells[j] = strings.TrimSpace(c); !watchLine(cells[j], watchCellMax) {
					return r, errWatchTable
				}
			}
			t.Rows[i] = cells
		}
	}
	r.Alert = false
	return r, nil
}

// trips: the alert fires on reading r, prev being the reading before it (nil: the first).
func (a *WatchAlert) trips(prev *WatchReading, r WatchReading) bool {
	if a == nil {
		return false
	}
	switch a.When {
	case "above":
		return r.Number != nil && a.Value != nil && *r.Number > *a.Value
	case "below":
		return r.Number != nil && a.Value != nil && *r.Number < *a.Value
	case watchBad:
		return r.Status == watchBad
	case "change":
		if prev == nil {
			return false
		}
		switch {
		case prev.Number != nil || r.Number != nil:
			return prev.Number == nil || r.Number == nil || *prev.Number != *r.Number
		case prev.Status != "" || r.Status != "":
			return prev.Status != r.Status
		case prev.Table != nil || r.Table != nil:
			a, _ := json.Marshal(prev.Table)
			b, _ := json.Marshal(r.Table)
			return string(a) != string(b)
		}
		return prev.Text != r.Text
	}
	return false
}

// reportWatch records a run's reading on card id, from conversation self on machine; when the card's rule trips it
// pushes the reading (opening that conversation) and says so.
func (s *Service) reportWatch(ctx context.Context, machine, self, id string, r WatchReading) (Watch, bool, error) {
	r, err := readingIn(r)
	if err != nil {
		return Watch{}, false, err
	}
	var out Watch
	tripped := false
	err = s.changeWatches(ctx, func(ws []Watch) ([]Watch, error) {
		i := watchIndex(ws, id)
		if i < 0 {
			return nil, errWatchNotFound
		}
		w := ws[i]
		if w.State != watchActive {
			return nil, errWatchDraft
		}
		r.At = time.Now().UTC()
		tripped = w.Alert.trips(w.Latest, r)
		r.Alert = tripped
		w.Latest = &r
		w.History = append(w.History, WatchPoint{At: r.At, Number: r.Number, Status: r.Status, Alert: tripped})
		if len(w.History) > watchHistoryMax {
			w.History = slices.Clone(w.History[len(w.History)-watchHistoryMax:])
		}
		ws[i] = w
		out = w
		return ws, nil
	})
	if err != nil {
		return Watch{}, false, err
	}
	if tripped {
		l := s.lang(ctx)
		s.push(ctx, Notification{Subtitle: i18n.T(l, "assistant-watch.push_subtitle"), DeviceID: machine, SessionID: self,
			Title: out.Title, Body: r.Text, Asked: true, Level: assistantwire.LevelP1, RelatedKey: "watch:" + out.ID})
	}
	return out, tripped, nil
}

// WatchView is a card as the app reads it: the card, its project's name and its task's next and last run.
type WatchView struct {
	Watch
	ProjectName string    `json:"project_name,omitempty"`
	CronText    string    `json:"cron_text,omitempty"`
	NextRun     time.Time `json:"next_run,omitzero"`
	LastRun     time.Time `json:"last_run,omitzero"`
	LastStatus  string    `json:"last_status,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	// RunMachineID and RunSessionID are the conversation the card's runs go into, to open from the card.
	RunMachineID string `json:"run_machine_id,omitempty"`
	RunSessionID string `json:"run_session_id,omitempty"`
	// NoTask: an active card whose task is gone (deleted on the Scheduled tasks page): it does not refresh until it is
	// resumed, which makes the task again.
	NoTask bool `json:"no_task,omitempty"`
}

// watchViews are the cards in order; with project, only that project's confirmed ones.
func (s *Service) watchViews(ctx context.Context, project string) ([]WatchView, error) {
	ws, err := s.watches(ctx)
	if err != nil {
		return nil, err
	}
	tasks := map[string]Task{}
	readable := s.scheduleReadable(ctx) == nil
	if readable {
		for _, t := range s.tasksView(ctx, func(t Task) bool { return t.WatchID != "" }) {
			tasks[t.ID] = t
		}
	}
	e, _ := s.kbGet(ctx)
	out := []WatchView{}
	for _, w := range ws {
		if project != "" && (w.Project != project || w.State != watchActive) {
			continue
		}
		v := WatchView{Watch: w}
		if e != nil && w.Project != "" {
			if p, ok := e.Store.Project(w.Project); ok {
				v.ProjectName = p.Name
			}
		}
		if w.State == watchActive {
			if t, ok := tasks[w.TaskID]; ok {
				v.CronText, v.NextRun, v.LastRun, v.LastStatus, v.LastError = t.CronText, t.NextRun, t.LastRun, t.LastStatus, t.LastError
				v.RunMachineID, v.RunSessionID = t.RunMachineID, t.RunSessionID
				if !t.Enabled {
					v.Paused = true
				}
			} else if readable {
				v.NoTask = true
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// watchErrorText is err in English, for the model.
func watchErrorText(err error) string {
	var msg *i18n.Message
	if errors.As(err, &msg) {
		return msg.Text(i18n.En)
	}
	return err.Error()
}
