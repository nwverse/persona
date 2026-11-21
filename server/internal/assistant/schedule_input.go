package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/promptcontent"
)

// Rich editors put their serialized prompt only in structured_prompt. An older manager requires
// prompt, so it rejects this write without changing the task or silently dropping its references.
func readStructuredTaskPrompt(raw map[string]json.RawMessage) error {
	content, rich := raw["structured_prompt"]
	if !rich {
		return nil
	}
	if _, plain := raw["prompt"]; plain {
		return errors.New("specify only one task prompt")
	}
	var prompt string
	if err := json.Unmarshal(content, &prompt); err != nil {
		return err
	}
	if prefix, _ := taskPromptBody(prompt); prefix == "" {
		return i18n.Err("assistant-schedule.input_context_unavailable")
	}
	raw["prompt"] = content
	return nil
}

// taskInputContext resolves a draft through the existing task execution contract, without saving or preparing files.
func (s *Service) taskInputContext(ctx context.Context, raw map[string]json.RawMessage) (taskInputTarget, error) {
	t := Task{ID: "__input_draft"}
	var id string
	if v, ok := raw["id"]; ok {
		if err := json.Unmarshal(v, &id); err != nil {
			return taskInputTarget{}, err
		}
	}
	if id != "" {
		sc, err := s.lockedSchedule(ctx)
		if err != nil {
			return taskInputTarget{}, err
		}
		i := sc.indexLocked(id)
		if i >= 0 {
			t = sc.tasks[i]
		}
		sc.mu.Unlock()
		if i < 0 {
			return taskInputTarget{}, i18n.Err("assistant-schedule.input_context_unavailable")
		}
	}
	previous := t
	if v, ok := raw["session_id"]; ok {
		var sid string
		if err := json.Unmarshal(v, &sid); err != nil {
			return taskInputTarget{}, err
		}
		// The edit route may detach the original chat, never attach an arbitrary one.
		if sid == "" {
			t.SessionID = ""
		}
	}
	for key, dst := range map[string]any{"push": &t.Push, "repeat": &t.Repeat, "cron": &t.Cron, "fresh_each_run": &t.FreshEachRun} {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return taskInputTarget{}, err
			}
		}
	}
	_, hasCron := raw["cron"]
	keepLegacyCronRule(previous, &t, hasCron)
	isolateScheduledConversation(&t, s.mayBeMainNow(ctx))
	edit, err := s.prepareTaskExecutionEdit(ctx, previous, &t, raw)
	if err != nil {
		return taskInputTarget{}, err
	}
	return s.taskInputExecution(ctx, t, edit.record)
}

// A task that wants visible updates runs in its own conversation when its source is another task's quiet chat.
func (s *Service) taskInputDestination(ctx context.Context, t Task) Task {
	if t.continuesSource() && !t.pushesOnlyAsked() && s.quietConversation(ctx, t.SessionID) {
		t.SessionID = ""
	}
	return t
}

type taskInputTarget struct {
	TaskExecution
	session, profile string
}

func (s *Service) taskInputExecution(ctx context.Context, t Task, record *modelRecord) (taskInputTarget, error) {
	t = s.taskInputDestination(ctx, t)
	if t.continuesSource() {
		c, ok := s.session(ctx, t.MachineID, t.SessionID)
		d, online := s.device(ctx, t.MachineID)
		if !online || !d.Online || s.isWorkMachine(t.MachineID) || !ok || !c.Assistant || c.Cwd == "" || s.isGuest(ctx, t.MachineID, t.SessionID) {
			return taskInputTarget{}, i18n.Err("assistant-schedule.input_context_unavailable")
		}
		if t.Space == SpaceHealth {
			var canonical struct {
				Cwd string `json:"cwd"`
			}
			if c.Space != SpaceHealth || s.call(ctx, t.MachineID, assistantwire.DeviceAssistantDirectory, map[string]any{"path": s.healthCwd(d)}, &canonical) != nil || canonical.Cwd != c.Cwd {
				return taskInputTarget{}, i18n.Err("assistant-schedule.input_context_unavailable")
			}
		}
		params := map[string]any{}
		if err := s.sendModel(ctx, t.MachineID, t.SessionID, params, s.requestCtxLang(ctx)); err != nil {
			return taskInputTarget{}, err
		}
		return taskInputTarget{TaskExecution: *s.sourceTaskExecution(ctx, t, params), session: t.SessionID}, nil
	}
	base, err := s.taskModelBaseline(ctx, s.taskMachine(ctx, t))
	if err != nil {
		return taskInputTarget{}, err
	}
	if record == nil {
		stored, err := s.modelRecord(ctx, "task."+t.ID)
		if err != nil {
			return taskInputTarget{}, err
		}
		record = &stored
	}
	b := mergeModel(base, record.Binding)
	ex, err := s.taskExecution(ctx, t, true, b)
	if err == nil && ex.Cwd == "" {
		if d, ok := s.device(ctx, ex.MachineID); ok {
			ex.Cwd = s.assistantCwd(d)
			if t.Space == SpaceHealth {
				ex.Cwd = s.healthCwd(d)
			}
		}
		var result struct {
			Cwd string `json:"cwd"`
		}
		if err = s.call(ctx, ex.MachineID, assistantwire.DeviceAssistantDirectory, map[string]any{"path": ex.Cwd}, &result); err == nil {
			if result.Cwd == "" {
				return taskInputTarget{}, i18n.Err("assistant-schedule.input_context_unavailable")
			}
			ex.Cwd = result.Cwd
		}
	}
	if err != nil {
		return taskInputTarget{}, err
	}
	target := taskInputTarget{TaskExecution: ex}
	// startConversation uses the same signed-in profile as meetings. A compatible task-owned
	// conversation keeps its own profile on resume (including the signed-out repair in resumeOwn).
	if !t.startsFresh() && t.RunSessionID != "" && t.RunMachineID == ex.MachineID && t.RunLauncher == ex.Launcher &&
		taskExecutionChangeReason(t, ex, record.Binding) == "" && !((t.pushesOnlyAsked() || t.Repeat != repeatOnce) && s.mayBeMain(ctx, t.RunSessionID)) {
		target.session = t.RunSessionID
		if c, ok := s.session(ctx, ex.MachineID, t.RunSessionID); ok && signedOut(c) && (c.Agent == "" || c.Agent == "claude") {
			target.profile = meetingProfile
		}
	} else if ex.Launcher == "claude" || ex.Agent == "" || ex.Agent == "claude" {
		target.profile = meetingProfile
	}
	return target, nil
}

func (s *Service) taskInputParams(target taskInputTarget, text string) map[string]any {
	params := map[string]any{"cwd": target.Cwd, "launcher": target.Launcher, "text": text, "task_execution": true, "machine_id": target.MachineID, "skill_owner": s.skillOwner()}
	if target.session != "" {
		params["session_id"] = target.session
	}
	if target.profile != "" {
		params["config_dir"] = target.profile
	}
	return params
}

func (s *Service) validateTaskInput(ctx context.Context, t Task, record *modelRecord) error {
	if !t.PromptSelections {
		return nil
	}
	prefix, _ := taskPromptBody(t.Prompt)
	skills, err := assistantskills.References(prefix)
	if err != nil {
		return skillReferenceError()
	}
	files, err := promptcontent.Files(prefix)
	if err != nil {
		return i18n.Err("assistant-schedule.selected_file_unavailable")
	}
	if len(skills) == 0 && len(files) == 0 {
		return nil
	}
	ex, err := s.taskInputExecution(ctx, t, record)
	if err != nil {
		return err
	}
	return s.checkTaskContent(ctx, ex, prefix)
}

// The node must acknowledge the task-input RPC, even when no skill inventory is requested.
// An empty prefix probes support without reading any file or running an assistant turn.
func (s *Service) checkTaskContent(ctx context.Context, ex taskInputTarget, prefix string) error {
	var ack struct {
		Valid bool `json:"valid"`
	}
	if err := s.call(ctx, ex.MachineID, "assistant.content", s.taskInputParams(ex, prefix), &ack); err != nil {
		return err
	}
	if !ack.Valid {
		return i18n.Err("hub-machine.node_no_task_input")
	}
	return nil
}

func (s *Service) taskInputRoutes() {
	s.handle("POST /api/v1/assistant/tasks/input-context", func(w http.ResponseWriter, r *http.Request) {
		raw := map[string]json.RawMessage{}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		if err := decoder.Decode(&raw); err != nil {
			apierr.Write(w, 400, err)
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			apierr.Write(w, 400, errors.New("invalid request"))
			return
		}
		ex, err := s.taskInputContext(r.Context(), raw)
		if err != nil {
			apierr.Call(w, err)
			return
		}
		result := map[string]any{"execution": ex.TaskExecution, "task_input": false}
		if err := s.checkTaskContent(r.Context(), ex, ""); err != nil {
			result["input_error"] = i18n.Localize(err, s.requestLang(r))
			apierr.WriteJSON(w, result)
			return
		}
		result["task_input"] = true
		var catalog bool
		_ = json.Unmarshal(raw["catalog"], &catalog)
		if catalog {
			c, e := s.skillCatalogFor(r.Context(), ex.MachineID, ex.session, s.taskInputParams(ex, ""))
			if e != nil {
				result["skill_error"] = e.Error()
			} else {
				result["catalog"] = c
			}
		}
		apierr.WriteJSON(w, result)
	})
}

// Keep the original envelope before the schedule header, so the shared selection parsers see it.
func taskPromptBody(prompt string) (string, string) {
	prefix, body := promptcontent.Split(prompt)
	if prefix == "" {
		return "", prompt
	}
	files, fileErr := promptcontent.Files(prefix)
	skills, skillErr := assistantskills.References(prefix)
	if fileErr == nil && skillErr == nil && len(files) == 0 && len(skills) == 0 {
		return "", prompt
	}
	return strings.TrimSpace(prefix), strings.TrimLeft(body, "\r\n")
}

var errTaskSelectionsOwner = errors.New("change the run settings of a task with selected content in the Persona app")

// A tool-authored prompt is literal even when it matches the task's previous text.
func (t *Task) setLiteralPrompt(prompt string) {
	t.Prompt, t.PromptSelections = prompt, false
}
