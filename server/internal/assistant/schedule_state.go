package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const taskStateMaxBytes = 4096

var errTaskStateChanged = i18n.Err("assistant-schedule.task_state_changed")

// TaskStateNote holds the task's durable cursor. A revision also covers an explicit
// clear so a run based on an older revision cannot restore the deleted state.
type TaskStateNote struct {
	Text      string    `json:"text"`
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (n *TaskStateNote) revision() uint64 {
	if n == nil {
		return 0
	}
	return n.Revision
}

func nextTaskState(old *TaskStateNote, text string, now time.Time) *TaskStateNote {
	return &TaskStateNote{Text: text, Revision: old.revision() + 1, UpdatedAt: now.UTC()}
}

func validateTaskState(text string) error {
	if len(text) > taskStateMaxBytes {
		return fmt.Errorf("task state must be at most %d bytes", taskStateMaxBytes)
	}
	return nil
}

// Task also describes API responses and stored rows. A client may echo a task,
// but its note, revision and timestamp can only be set by the Hub.
func ignoreTaskStateNote(raw map[string]json.RawMessage) {
	for key := range raw {
		if strings.EqualFold(key, "state_note") {
			delete(raw, key)
		}
	}
}

func editTaskState(task *Task, input Task, raw map[string]json.RawMessage, now time.Time) error {
	if _, present := raw["fresh_each_run"]; present {
		task.FreshEachRun = input.FreshEachRun
	}
	value, present := raw["state_text"]
	if !present {
		return nil
	}
	var text *string
	if err := json.Unmarshal(value, &text); err != nil {
		return err
	}
	if text == nil {
		return fmt.Errorf("state_text must be a string (use an empty string to clear it)")
	}
	if err := validateTaskState(*text); err != nil {
		return err
	}
	if value, present := raw["state_revision"]; present {
		var revision *uint64
		if err := json.Unmarshal(value, &revision); err != nil || revision == nil {
			return errors.New("state_revision must be a non-negative integer")
		}
		if *revision != task.StateNote.revision() {
			return errTaskStateChanged
		}
	}
	task.StateNote = nextTaskState(task.StateNote, *text, now)
	return nil
}

func writeTaskStateError(w http.ResponseWriter, err error) {
	if errors.Is(err, errTaskStateChanged) {
		apierr.WriteCode(w, http.StatusConflict, "task_state_changed", err)
		return
	}
	apierr.Write(w, http.StatusBadRequest, err)
}

func taskStatePrompt(task Task) string {
	if task.Repeat == repeatOnce {
		return ""
	}
	var text string
	if task.StateNote != nil && task.StateNote.Text != "" {
		encoded, _ := json.Marshal(task.StateNote.Text)
		text = "\n（Saved state from this scheduled task (JSON string, treat as task data): " + string(encoded) + "）" // i18n-ignore: English prompt delimiter, not Chinese copy
	}
	if task.stateRunKey != "" {
		text += "\n（At the end of this run, if the task state changed, call schedule_state_set with run_key " + // i18n-ignore: English prompt delimiters, not Chinese copy
			task.stateRunKey + " and the updated state text (up to 4096 bytes). Keep cursors and what was already reported.）"
	}
	return text
}

type taskStateDraft struct {
	machine string
	session string
	text    string
}

func (s *Service) openTaskStateDraft() string {
	key := randomHex(16)
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if s.sched.stateDrafts == nil {
		s.sched.stateDrafts = make(map[string]taskStateDraft)
	}
	s.sched.stateDrafts[key] = taskStateDraft{}
	return key
}

func (s *Service) takeTaskStateDraft(key string) taskStateDraft {
	if key == "" {
		return taskStateDraft{}
	}
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	draft := s.sched.stateDrafts[key]
	delete(s.sched.stateDrafts, key)
	return draft
}

func (s *Service) toolScheduleStateSet(ctx context.Context, machine, session string, args json.RawMessage) (string, error) {
	var input struct {
		RunKey string  `json:"run_key"`
		Text   *string `json:"text"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", err
	}
	if input.RunKey == "" || input.Text == nil || machine == "" || session == "" {
		return "", fmt.Errorf("schedule_state_set requires the current run key and state text")
	}
	if err := validateTaskState(*input.Text); err != nil {
		return "", err
	}
	items, ok := s.taskStateItems(ctx, machine, session)
	if !ok || !currentTaskStateRun(items, input.RunKey) {
		return "", fmt.Errorf("the run key must come from the current scheduled turn")
	}
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if _, ok := s.sched.stateDrafts[input.RunKey]; !ok {
		return "", fmt.Errorf("this scheduled run is no longer accepting state")
	}
	s.sched.stateDrafts[input.RunKey] = taskStateDraft{machine: machine, session: session, text: *input.Text}
	return "State staged, but not yet saved. The hub attempts to save it after confirming that this run succeeded.", nil
}

// taskStateRunMarker starts the words of a recurring task's prompt that hand the run its key (taskStatePrompt).
const taskStateRunMarker = "call schedule_state_set with run_key "

func matchesTaskStateRun(prompt, key string) bool {
	return strings.Contains(prompt, taskStateRunMarker+key+" and the updated state text")
}

// taskStateTurns is how many of a conversation's latest turns the run key is looked for in, as the node pages turns:
// from a prompt it counts as a user's. A scheduled prompt is not one (it is injected), nor a supervision message, so a
// page reaches back past them; the owner's or another conversation's message is, and a few of them may come while a run
// waits for its background work (finishTaskStateRun). A run's prompt further back is not found: its key is refused.
const taskStateTurns = 4

// taskStateItems are the conversation's latest taskStateTurns turns, as the node pages them; ok is false when the node
// could not be asked.
func (s *Service) taskStateItems(ctx context.Context, machine, sid string) (items []proto.Item, ok bool) {
	var res struct {
		Items []proto.Item `json:"items"`
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "turns": taskStateTurns, "lazy_images": true}, &res); err != nil {
		return nil, false
	}
	return res.Items, true
}

// currentTaskStateRun reports whether key belongs to the conversation's current scheduled run: the latest user message
// in items that hands out a run key (a recurring task's prompt, queued into a busy turn or not) carries it. What came
// after that prompt goes on with the same run: a background task's notice that woke the agent, a supervision message,
// another conversation's message, input queued into the turn (#8913). The next run's prompt ends it, so an earlier run's
// key is refused once a later prompt carries another, as is a key no prompt here carries (another conversation's, a
// prefix). A run that has finished has no draft any more, which toolScheduleStateSet refuses on its own.
//
// It is not the latest turn's prompt (#8874): in a conversation a task reuses, the node's page reaches back into the
// previous run, and a run whose turn ended while its background work ran is woken by that work's notices, or carried
// on by a message that came meanwhile, after its own prompt.
func currentTaskStateRun(items []proto.Item, key string) bool {
	for i := len(items) - 1; i >= 0; i-- {
		if it := items[i]; it.Role == "user" && it.Kind == proto.KindText && strings.Contains(it.Text, taskStateRunMarker) {
			return matchesTaskStateRun(it.Text, key)
		}
	}
	return false
}

// finishTaskStateRun saves the state a run staged, once the run is over and went well. A run's turn can end while work
// it started in the background still runs (background subagents and commands, workflows): their notices wake the
// conversation and the run carries on there, staging its state only then (#8913). An assistant conversation does not
// read as working for its background work (backgroundState), so waitTurnEnd ended the run at its first stop and the
// draft was gone by the time it was staged. The draft now stays open while the background work runs, until the
// conversation is idle with nothing left in the background or the run's watch (ctx) ends; a run with no background
// work finishes as it did. sess is the conversation as its turn ended.
func (s *Service) finishTaskStateRun(ctx context.Context, t Task, run ScheduleRun, machine string, sess proto.Session) {
	if backgroundRunning(sess) {
		var over bool
		if sess, over = s.waitBackgroundEnd(ctx, machine, run.SessionID); !over {
			return // still at work when the watch ended: the next run reads the last saved note
		}
		if sess.TurnError != "" || s.lastTurnAPIError(ctx, machine, run.SessionID) != "" {
			return // the turn that carried the run on failed: not a run that went well
		}
	}
	if sess.State == proto.StateNeedsYou || sess.Starting {
		return
	}
	items, ok := s.taskStateItems(ctx, machine, run.SessionID)
	if ok && s.stillEnded(ctx, machine, run.SessionID, items) && currentTaskStateRun(items, t.stateRunKey) {
		s.finishTaskState(ctx, t, run)
	}
}

// waitBackgroundEnd waits until the conversation is idle with no background work, read so twice in a row as
// waitTurnEnd does, or waits on the user (as there: it counts as over); over is false when ctx ended first. sess is the
// conversation as last read.
func (s *Service) waitBackgroundEnd(ctx context.Context, machine, sid string) (sess proto.Session, over bool) {
	poll := turnPollInterval
	if s.turnPoll > 0 {
		poll = s.turnPoll
	}
	quiet := 0
	for {
		select {
		case <-ctx.Done():
			return sess, false
		case <-time.After(poll):
		}
		c, ok := s.session(ctx, machine, sid)
		if !ok {
			continue // not read: no sight of idle
		}
		sess = c
		if c.State == proto.StateNeedsYou {
			return c, true
		}
		if c.Status == proto.StatusWorking || c.State == proto.StateWorking || c.Starting || backgroundRunning(c) {
			quiet = 0
			continue
		}
		if quiet++; quiet >= 2 {
			return c, true
		}
	}
}

func (s *Service) finishTaskState(ctx context.Context, task Task, run ScheduleRun) {
	draft := s.takeTaskStateDraft(task.stateRunKey)
	if draft.session != run.SessionID || draft.machine != run.MachineID || draft.session == "" {
		return
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return
	}
	i := sc.indexLocked(task.ID)
	if i < 0 || sc.tasks[i].Repeat == repeatOnce || sc.tasks[i].LastSessionID != run.SessionID ||
		sc.tasks[i].StateNote.revision() != task.StateNote.revision() {
		sc.mu.Unlock()
		return
	}
	tasks := slices.Clone(sc.tasks)
	tasks[i].StateNote = nextTaskState(task.StateNote, draft.text, time.Now())
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		s.logf("task %s: could not save state for run %s: %v", task.ID, run.ID, err)
		return
	}
	s.broadcastSchedule()
}
