package node

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	agyTaskPrefix           = "Tool is running as a background task with task id:"
	agyBackgroundEndedLimit = 20
	agyEmptyTaskList        = "No background tasks are currently running."
)

var (
	agyTaskName      = lazyre.New(`^task-[0-9]+$`)
	agyTaskEnd       = lazyre.New(`(?i)\bTask id "([^"\n]+)" (?:was )?(finished|failed|canceled|cancelled|killed|stopped)\b`)
	agyTaskMessage   = lazyre.New(`(?m)^[ \t]*\[Message\] timestamp=(\S+) sender=(\S+) priority=\S+ content=([\s\S]*)`)
	agyCronIteration = lazyre.New(`\(iteration [0-9]+\)$`)
	agyServerRestart = lazyre.New(`(?i)^(?:\[Notice\]\s+)?All your subagents and background tasks have been stopped due to server restart\b`)
	agyTaskListRow   = lazyre.New("^- .* \\(`([^`\\s]+)`\\)$")
)

func agyBackgroundNotice(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "Created At:") {
		_, content, _ = strings.Cut(content, "\n")
	}
	if content, ok := strings.CutPrefix(strings.TrimSpace(content), agyTaskPrefix); ok {
		return content
	}
	return ""
}

func agyToolString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	// Native exports can encode a tool argument as JSON inside a string.
	var decoded string
	if strings.HasPrefix(value, `"`) && json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}

type agyBackgroundTasks struct {
	running []proto.BackgroundTask
	ended   []proto.BackgroundTask
}

func (b agyBackgroundTasks) apply(s *proto.Session) {
	s.Background = nil
	if !agyStopped(s.Status) {
		s.Background = b.running
	}
	s.SetBackgroundEnded(recentBackgroundEnded(b.ended))
}

type agyTaskRecord struct {
	task          proto.BackgroundTask
	lastSeen      time.Time
	lastSeenOrder int
	inferred      bool
	oneShot       bool
}

func (r *agyTaskRecord) recordSeen(at time.Time, order int) {
	if !at.IsZero() {
		if r.lastSeen.IsZero() || at.After(r.lastSeen) {
			r.lastSeen = at
			r.lastSeenOrder = order
		} else if at.Equal(r.lastSeen) && order > r.lastSeenOrder {
			r.lastSeenOrder = order
		}
		return
	}
	if r.lastSeen.IsZero() && order > r.lastSeenOrder {
		r.lastSeenOrder = order
	}
}

func (r *agyTaskRecord) finish(status string, at time.Time, inferred bool) {
	if r == nil || (r.task.Status != "" && (!r.inferred || inferred)) {
		return
	}
	r.task.Status, r.task.EndedAt, r.inferred = status, &at, inferred
}

type agyBackgroundCall struct {
	planner      *agyStep
	plannerOrder int
	tool         *agyToolCall
}

type agyReconcileEvent struct {
	at    time.Time
	order int
}

func compareAgyReconcileEvents(a, b agyReconcileEvent) int {
	if !a.at.IsZero() && !b.at.IsZero() {
		if cmp := a.at.Compare(b.at); cmp != 0 {
			return cmp
		}
	}
	return a.order - b.order
}

func agyRestartAfterTask(restart agyReconcileEvent, lastSeen time.Time, lastSeenOrder int) bool {
	if !restart.at.IsZero() && !lastSeen.IsZero() {
		return restart.at.After(lastSeen) || (restart.at.Equal(lastSeen) && restart.order > lastSeenOrder)
	}
	return restart.order > lastSeenOrder
}

func agyBackground(steps []agyStep, conversation, dir string, brain *os.Root) agyBackgroundTasks {
	byID := make(map[string]*agyTaskRecord)
	var ordered []*agyTaskRecord
	pending := make(map[int]agyBackgroundCall)
	var restarts []agyReconcileEvent
	var inventories []agyReconcileEvent
	inventorySeen := make(map[string]agyReconcileEvent)
	for i := range steps {
		step := &steps[i]
		at, _ := time.Parse(time.RFC3339Nano, step.CreatedAt)
		slot := pending[step.seq]
		delete(pending, step.seq)
		switch step.Type {
		case "PLANNER_RESPONSE":
			for offset, call := range step.ToolCalls {
				// Late results retain their native step index. An overlapping claim makes the slot ambiguous.
				seq := step.seq + 1 + offset
				if _, claimed := pending[seq]; claimed {
					pending[seq] = agyBackgroundCall{}
				} else {
					pending[seq] = agyBackgroundCall{planner: step, plannerOrder: i, tool: call}
				}
				if call == nil || call.Name != "manage_task" {
					continue
				}
				var args map[string]json.RawMessage
				if json.Unmarshal(call.Args, &args) == nil && strings.EqualFold(agyToolString(args["Action"]), "kill") {
					byID[agyQualifiedTaskID(conversation, agyToolString(args["TaskId"]))].finish(proto.WorkflowKilled, at, false)
				}
			}
		case "SYSTEM_MESSAGE":
			end := agyTaskEnd.FindStringSubmatch(step.Content)
			if end != nil {
				status := proto.WorkflowKilled
				switch strings.ToLower(end[2]) {
				case "finished":
					status = proto.WorkflowCompleted
				case "failed":
					status = proto.WorkflowFailed
				}
				byID[agyQualifiedTaskID(conversation, end[1])].finish(status, at, false)
			}
			m := agyTaskMessage.FindStringSubmatch(step.Content)
			if restartAt, ok := agyServerRestartTime(step.Content, m, at); ok {
				restarts = append(restarts, agyReconcileEvent{at: restartAt, order: i})
				continue
			}
			if m != nil {
				sender := agyQualifiedTaskID(conversation, m[2])
				if record := byID[sender]; record != nil {
					record.recordSeen(at, i)
					if end == nil && record.oneShot {
						message := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[3]), "</SYSTEM_MESSAGE>"))
						if agyCronIteration.MatchString(message) {
							record.oneShot = false
						} else if !strings.Contains(strings.ToLower(message), "still running") {
							record.finish(proto.WorkflowCompleted, at, false)
						}
					}
				}
			}
		case "GENERIC":
			var call *agyToolCall
			if slot.planner != nil && slot.planner.branch == step.branch {
				calledAt, _ := time.Parse(time.RFC3339Nano, slot.planner.CreatedAt)
				if at.IsZero() || calledAt.IsZero() || !at.Before(calledAt) {
					call = slot.tool
				}
				if ids, ok := agyTaskInventory(step, slot, calledAt, at, conversation); ok {
					ev := agyReconcileEvent{at: calledAt, order: slot.plannerOrder}
					inventories = append(inventories, ev)
					for id := range ids {
						if prev, exists := inventorySeen[id]; !exists || compareAgyReconcileEvents(ev, prev) > 0 {
							inventorySeen[id] = ev
						}
					}
				}
			}
			notice := agyBackgroundNotice(firstNonEmpty(step.backgroundStart, step.Content))
			id, details, _ := strings.Cut(notice, "\n")
			id = strings.TrimSpace(id)
			name, ours := strings.CutPrefix(id, conversation+"/")
			if !ours || !agyTaskName.MatchString(name) {
				continue
			}
			record := byID[id]
			if record == nil {
				task := proto.BackgroundTask{ID: id, Kind: "shell", StartedAt: at}
				for line := range strings.SplitSeq(details, "\n") {
					line = strings.TrimSpace(line)
					if value, ok := strings.CutPrefix(line, "Task Description:"); ok && task.Description == "" {
						task.Description = strings.Clone(strings.TrimSpace(value))
					}
					if value, ok := strings.CutPrefix(line, "Task logs are available at:"); ok && task.OutputFile == "" {
						task.OutputFile = agyTaskLog(strings.TrimSpace(value), dir, conversation, name, brain)
					}
				}
				if call != nil {
					setAgyTaskKind(&task, call.Name, call.Args)
				}
				record = &agyTaskRecord{task: task, lastSeenOrder: i, oneShot: agyOneShotSchedule(task.Description, call)}
				byID[id] = record
				ordered = append(ordered, record)
			}
			record.recordSeen(at, i)
			if updated, err := time.Parse(time.RFC3339Nano, step.UpdatedAt); err == nil {
				at = updated
				record.recordSeen(updated, i)
			}
			switch strings.ToUpper(step.Status) {
			case "DONE":
				record.finish(proto.WorkflowCompleted, at, true)
			case "ERROR":
				record.finish(proto.WorkflowFailed, at, true)
			case "CANCELED", "CANCELLED":
				record.finish(proto.WorkflowKilled, at, true)
			}
		}
	}
	slices.SortFunc(inventories, compareAgyReconcileEvents)
	var result agyBackgroundTasks
	for _, record := range ordered {
		if record.task.Status == "" {
			lastSeen, lastSeenOrder := record.lastSeen, record.lastSeenOrder
			if listed, ok := inventorySeen[record.task.ID]; ok {
				if lastSeen.IsZero() || listed.at.After(lastSeen) || (listed.at.Equal(lastSeen) && listed.order > lastSeenOrder) {
					lastSeen, lastSeenOrder = listed.at, listed.order
				}
			}
			var restart *agyReconcileEvent
			for j := range restarts {
				if !agyRestartAfterTask(restarts[j], lastSeen, lastSeenOrder) {
					continue
				}
				if restart == nil || compareAgyReconcileEvents(restarts[j], *restart) < 0 {
					restart = &restarts[j]
				}
			}
			var inventory *agyReconcileEvent
			if !record.task.StartedAt.IsZero() && !lastSeen.IsZero() && len(inventories) > 0 {
				// Any snapshot strictly after the last evidence of this task omitted it. Wait until
				// the whole transcript is read because newer activity can arrive out of order.
				if j := sort.Search(len(inventories), func(j int) bool { return inventories[j].at.After(lastSeen) }); j < len(inventories) {
					inventory = &inventories[j]
				}
			}
			switch {
			case restart != nil && inventory != nil:
				if compareAgyReconcileEvents(*inventory, *restart) < 0 {
					record.finish(proto.WorkflowCompleted, inventory.at, true)
				} else {
					record.finish(proto.WorkflowKilled, restart.at, true)
				}
			case restart != nil:
				record.finish(proto.WorkflowKilled, restart.at, true)
			case inventory != nil:
				record.finish(proto.WorkflowCompleted, inventory.at, true)
			}
		}
		if record.task.EndedAt != nil {
			result.ended = append(result.ended, record.task)
		} else if len(result.running) < maxRuntimeBackground {
			result.running = append(result.running, record.task)
		}
	}
	slices.SortStableFunc(result.ended, func(a, b proto.BackgroundTask) int { return a.EndedAt.Compare(*b.EndedAt) })
	if extra := len(result.ended) - agyBackgroundEndedLimit; extra > 0 {
		result.ended = slices.Clone(result.ended[extra:])
	}
	return result
}

func agyQualifiedTaskID(conversation, id string) string {
	if agyTaskName.MatchString(id) {
		return conversation + "/" + id
	}
	return id
}

func agySystemMessageBody(content string) string {
	trimmed := strings.TrimSpace(content)
	if i := strings.LastIndex(trimmed, "<SYSTEM_MESSAGE>"); i >= 0 {
		trimmed = trimmed[i+len("<SYSTEM_MESSAGE>"):]
	}
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.TrimSuffix(trimmed, "</SYSTEM_MESSAGE>")
	return strings.TrimSpace(trimmed)
}

// agyServerRestartTime recognizes the global stop notice Antigravity writes when its server restarts.
func agyServerRestartTime(content string, message []string, createdAt time.Time) (time.Time, bool) {
	if message != nil {
		if message[2] != "system" {
			return time.Time{}, false
		}
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(message[3]), "</SYSTEM_MESSAGE>"))
		if !agyServerRestart.MatchString(body) {
			return time.Time{}, false
		}
		if !createdAt.IsZero() {
			return createdAt, true
		}
		ts, _ := time.Parse(time.RFC3339Nano, message[1])
		return ts, true
	}
	if !agyServerRestart.MatchString(agySystemMessageBody(content)) {
		return time.Time{}, false
	}
	return createdAt, true
}

// agyTaskInventory accepts complete, unfiltered manage_task(Action="list") results from the current conversation.
func agyTaskInventory(step *agyStep, slot agyBackgroundCall, calledAt, at time.Time, conversation string) (map[string]struct{}, bool) {
	call := slot.tool
	if call == nil || call.Name != "manage_task" || step.Error != "" || !strings.EqualFold(step.Status, "DONE") ||
		step.Index == nil || *step.Index < 0 || slot.planner.Index == nil || *slot.planner.Index < 0 ||
		calledAt.IsZero() || at.IsZero() || at.Before(calledAt) ||
		slices.Contains(step.TruncatedFields, "content") || slices.Contains(slot.planner.TruncatedFields, "tool_calls") {
		return nil, false
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(call.Args, &args) != nil || !strings.EqualFold(agyToolString(args["Action"]), "list") {
		return nil, false
	}
	// A nonempty selector or an unknown option may make the inventory a subset.
	delete(args, "Action")
	if raw, ok := args["TaskId"]; ok {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "null" || (strings.HasPrefix(trimmed, `"`) && agyToolString(raw) == "") {
			delete(args, "TaskId")
		}
	}
	if len(args) != 0 {
		return nil, false
	}
	// The tool's timestamp can predate the result step when an old result is exported late.
	if header, ok := strings.CutPrefix(step.Content, "Created At: "); ok {
		created, _, _ := strings.Cut(header, "\n")
		started, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(created))
		if err != nil || started.Before(calledAt) {
			return nil, false
		}
	}
	output := strings.TrimSpace(agyToolOutput(step.Content))
	if output == agyEmptyTaskList {
		return nil, true
	}
	header, rows, found := strings.Cut(output, "\n")
	if !found {
		return nil, false
	}
	countText, ok := strings.CutPrefix(strings.TrimSpace(header), "You have ")
	if !ok {
		return nil, false
	}
	countText, ok = strings.CutSuffix(countText, " background task(s) currently running:")
	count, err := strconv.Atoi(countText)
	if !ok || err != nil || count <= 0 {
		return nil, false
	}
	ids := make(map[string]struct{})
	for line := range strings.SplitSeq(rows, "\n") {
		match := agyTaskListRow.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			return nil, false
		}
		id := agyQualifiedTaskID(conversation, match[1])
		if name, ok := strings.CutPrefix(id, conversation+"/"); !ok || !agyTaskName.MatchString(name) {
			return nil, false
		}
		if _, duplicate := ids[id]; duplicate {
			return nil, false
		}
		ids[id] = struct{}{}
	}
	return ids, len(ids) == count
}

// agyOneShotSchedule recognizes timers from either the tool arguments or the task description.
// A cron expression or description takes precedence when both forms are present.
func agyOneShotSchedule(description string, call *agyToolCall) bool {
	if strings.HasPrefix(description, "Cron:") {
		return false
	}
	var args map[string]json.RawMessage
	if call != nil && call.Name == "schedule" && json.Unmarshal(call.Args, &args) == nil {
		if strings.TrimSpace(agyToolString(args["CronExpression"])) != "" {
			return false
		}
		if duration := args["DurationSeconds"]; duration != nil && string(duration) != "null" {
			return true
		}
	}
	return strings.HasPrefix(description, "Timer:")
}

func setAgyTaskKind(task *proto.BackgroundTask, tool string, raw json.RawMessage) {
	switch tool {
	case "schedule":
		task.Kind = "monitor"
	case "run_workflow":
		task.Kind = "workflow"
	case "invoke_subagent":
		task.Kind = "agent"
	case "run_command":
		var args map[string]json.RawMessage
		if json.Unmarshal(raw, &args) == nil {
			task.Command = firstNonEmpty(agyToolString(args["CommandLine"]), agyToolString(args["command"]))
		}
	}
}

// The export can name only this task's log inside its conversation directory.
func agyTaskLog(uri, dir, conversation, name string, brain *os.Root) string {
	path, err := url.PathUnescape(agyLocalFileURLToPath(uri))
	rel := filepath.Join(".system_generated", "tasks", name+".log")
	expected, absErr := filepath.Abs(filepath.Join(dir, "brain", conversation, rel))
	// The log can be announced before it is created.
	if err != nil || absErr != nil || !filepath.IsAbs(path) || filepath.Clean(path) != expected {
		return ""
	}
	info, err := brain.Stat(rel)
	if os.IsNotExist(err) || (err == nil && info.Mode().IsRegular()) {
		return expected
	}
	return ""
}

func agyStopped(status string) bool {
	return status == proto.StatusOffline || status == proto.StatusInterrupted
}

// agyPaneBackgroundFromRows reads the tasks the companion hides on ACP history rows.
func agyPaneBackgroundFromRows(params json.RawMessage) map[string][]proto.BackgroundTask {
	var push struct {
		Sessions []struct {
			ID    string                 `json:"session_id"`
			Tasks []proto.BackgroundTask `json:"agy_pane_background"`
		} `json:"sessions"`
	}
	if json.Unmarshal(params, &push) != nil {
		return nil
	}
	var background map[string][]proto.BackgroundTask
	for _, row := range push.Sessions {
		if len(row.Tasks) == 0 || !runtimeIDRe.MatchString(row.ID) {
			continue
		}
		if background == nil {
			background = make(map[string][]proto.BackgroundTask)
		}
		background[row.ID] = runtimeBackground(row.Tasks, maxRuntimeBackground, true)
	}
	return background
}

// agyPaneRuntimeRow reads the companion row and its pane inventory from the same list update.
func (b *runtimeRegistry) agyPaneRuntimeRow(id string) (proto.Session, agyPreview, bool) {
	h := b.ownerOf(id)
	if h == nil {
		return proto.Session{}, agyPreview{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hello.Runtime != agyRuntimeID || h.daemon != "" {
		return proto.Session{}, agyPreview{}, false
	}
	row, ok := h.rowLocked(id)
	if tasks := h.agyPaneBackground[id]; len(row.Background) == 0 && len(tasks) > 0 {
		row.Background = tasks
	}
	return row, h.agyPreviews[id], ok
}

// Runtime task metadata cannot grant access to node-local files. Join the log
// paths independently confirmed by the local conversation's transcript.
func (n *Node) agyRuntimeTaskLogs(s *proto.Session, conversation string) {
	if len(s.Background) == 0 && len(s.BackgroundEnded()) == 0 {
		return
	}
	meta, ok := n.agyTranscriptMeta(conversation)
	if !ok {
		return
	}
	logs := make(map[string]string)
	for _, tasks := range [][]proto.BackgroundTask{meta.background.running, meta.background.ended} {
		for _, task := range tasks {
			if task.OutputFile != "" {
				logs[task.ID] = task.OutputFile
			}
		}
	}
	if len(logs) == 0 {
		return
	}
	attach := func(tasks []proto.BackgroundTask) []proto.BackgroundTask {
		tasks = slices.Clone(tasks)
		for i := range tasks {
			tasks[i].OutputFile = logs[tasks[i].ID]
		}
		return tasks
	}
	s.Background = attach(s.Background)
	s.SetBackgroundEnded(attach(s.BackgroundEnded()))
}
