package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// TaskExecution is the shared execution context for scheduled prompts and their content references.
// An empty model or effort means the launcher's own default, not another provider's default.
type TaskExecution struct {
	MachineID      string `json:"machine_id"`
	Launcher       string `json:"launcher"`
	Agent          string `json:"agent,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Runtime        string `json:"runtime,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
}

func (s *Service) taskMachine(ctx context.Context, t Task) string {
	return cmp.Or(t.ExecutionMachineID, s.settings(ctx).MachineID)
}

func (s *Service) taskExecution(ctx context.Context, t Task, validate bool, binding ...ModelBinding) (TaskExecution, error) {
	machine := s.taskMachine(ctx, t)
	ex := TaskExecution{MachineID: machine, Cwd: t.Cwd, PermissionMode: cmp.Or(t.PermissionMode, s.settings(ctx).DefaultPermissionMode, "bypassPermissions")}
	var b ModelBinding
	if len(binding) > 0 {
		b = binding[0]
	} else {
		var err error
		b, err = s.resolveModel(ctx, "task."+t.ID)
		if err != nil {
			return ex, err
		}
	}
	ex.Launcher, ex.Model, ex.Effort, ex.Runtime = strings.TrimPrefix(b.Provider, "launcher."), b.Model, b.Effort, b.Runtime
	ex.Fingerprint = s.modelBindingFingerprint(ctx, b)
	if !validate {
		if ex.Cwd == "" {
			if d, ok := s.device(ctx, machine); ok {
				ex.Cwd = s.assistantCwd(d)
				if t.Space == SpaceHealth {
					ex.Cwd = s.healthCwd(d)
				}
			}
		}
		return ex, nil
	}
	d, ok := s.device(ctx, machine)
	if machine == "" {
		return ex, s.missingAssistantMachine(ctx)
	}
	if !ok || !d.Online {
		return ex, i18n.Err("assistant-schedule.execution_offline")
	}
	if s.isWorkMachine(machine) {
		return ex, errWorkAssistant
	}
	p := ModelPurpose{ID: "task." + t.ID, Kind: "launcher", CategoryID: "assistant", Parent: "assistant", MachineID: machine}
	if err := checkModelID(b.Model); err != nil {
		return ex, err
	}
	if err := s.validateModel(ctx, p, b, s.modelProviders(ctx)); err != nil {
		return ex, err
	}
	if err := s.checkTaskModelCapabilities(ctx, machine, b); err != nil {
		return ex, err
	}
	ex.Agent = agentKind(s.launcherAgent(ctx, machine, ex.Launcher))
	if ex.Cwd != "" {
		var result struct {
			Cwd string `json:"cwd"`
		}
		if err := s.call(ctx, machine, assistantwire.DeviceAssistantDirectory, map[string]any{"path": ex.Cwd}, &result); err != nil {
			return ex, i18n.Err("assistant-schedule.execution_directory", i18n.Args{"error": err.Error()})
		}
		if result.Cwd == "" {
			return ex, i18n.Err("assistant-schedule.execution_directory", i18n.Args{"error": "directory validation is unavailable; update this computer"})
		}
		ex.Cwd = result.Cwd
	}
	return ex, nil
}

// patchTaskExecution validates a sparse task edit. The caller holds models.mu before the schedule lock.
// Model configuration keeps its existing namespace; task and model changes commit in one settings transaction.
func (s *Service) patchTaskExecution(ctx context.Context, t *Task, raw map[string]json.RawMessage) (*modelRecord, error) {
	changed := false
	for key, dst := range map[string]*string{"execution_machine_id": &t.ExecutionMachineID, "cwd": &t.Cwd} {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return nil, err
			}
			if key != "cwd" {
				*dst = strings.TrimSpace(*dst)
			}
			if strings.ContainsAny(*dst, "\x00\r\n") {
				return nil, i18n.Err("assistant-models.err_field_invalid")
			}
			changed = true
		}
	}
	old, err := s.modelRecord(ctx, "task."+t.ID)
	if err != nil {
		return nil, err
	}
	patch := modelPatch{}
	for key, dst := range map[string]*json.RawMessage{"launcher": &patch.Provider, "model": &patch.Model, "effort": &patch.Effort} {
		if v, ok := raw[key]; ok {
			var text string
			if err := json.Unmarshal(v, &text); err != nil {
				return nil, err
			}
			text = strings.TrimSpace(text)
			if key == "launcher" && text != "" {
				if !launcherName.MatchString(text) {
					return nil, i18n.Err("assistant-models.err_field_invalid")
				}
				text = launcherProvider(text)
			}
			*dst = jsonStringOrNull(&text)
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	if t.SessionID != "" {
		return nil, errTaskModelNeedsOwnConversation
	}
	if v, ok := raw["model_revision"]; ok {
		var rev int64
		if err := json.Unmarshal(v, &rev); err != nil {
			return nil, err
		}
		if rev != old.Revision {
			return nil, errModelConflict
		}
	}
	b, err := patchModel(old.Binding, patch)
	if err != nil {
		return nil, err
	}
	p := ModelPurpose{ID: "task." + t.ID, Kind: "launcher", CategoryID: "assistant", Parent: "assistant", MachineID: s.taskMachine(ctx, *t)}
	base, _, err := s.modelBaseline(ctx, p)
	if err != nil {
		return nil, err
	}
	if b.Provider == "" && (b.Model != "" || b.Effort != "") {
		b.Provider = base.Provider
	}
	effective := mergeModel(base, b)
	if err := checkModelID(effective.Model); err != nil {
		return nil, err
	}
	d, ok := s.device(ctx, p.MachineID)
	if !ok || !d.Online {
		return nil, i18n.Err("assistant-schedule.execution_offline")
	}
	if s.isWorkMachine(p.MachineID) {
		return nil, errWorkAssistant
	}
	if err := s.validateModel(ctx, p, effective, s.modelProviders(ctx)); err != nil {
		return nil, err
	}
	if err := s.checkTaskModelCapabilities(ctx, p.MachineID, effective); err != nil {
		return nil, err
	}
	if t.Cwd != "" {
		var result struct {
			Cwd string `json:"cwd"`
		}
		if err := s.call(ctx, p.MachineID, assistantwire.DeviceAssistantDirectory, map[string]any{"path": t.Cwd}, &result); err != nil {
			return nil, i18n.Err("assistant-schedule.execution_directory", i18n.Args{"error": err.Error()})
		}
		if result.Cwd == "" {
			return nil, i18n.Err("assistant-schedule.execution_directory", i18n.Args{"error": "directory validation is unavailable; update this computer"})
		}
		// Keep the path the user chose. Each run resolves symlinks again on that computer.
	}
	if b == old.Binding {
		return nil, nil
	}
	return &modelRecord{Revision: old.Revision + 1, Binding: b}, nil
}

func (s *Service) saveTaskConfiguredLocked(ctx context.Context, sc *schedule, tasks []Task, id string, record *modelRecord) error {
	if record == nil {
		return s.saveLocked(sc, tasks)
	}
	batch, ok := s.store().(interface {
		PutSettings(context.Context, map[string]any) error
	})
	if !ok {
		return errors.New("atomic task configuration storage is unavailable")
	}
	if err := batch.PutSettings(ctx, map[string]any{tasksKey: tasks, modelRecordKey("task." + id): *record}); err != nil {
		return err
	}
	sc.tasks = tasks
	return nil
}

func compatibleTaskExecution(old, next TaskExecution) bool {
	// Permission changes can be applied to a compatible session; the agent, directory and model identity cannot.
	old.PermissionMode, next.PermissionMode = "", ""
	return old == next
}

// taskRunSnapshot pairs the task's directory/computer with its model record under the writers' lock order.
func (s *Service) taskRunSnapshot(ctx context.Context, t Task) (Task, ModelBinding, error) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return t, ModelBinding{}, err
	}
	if i := sc.indexLocked(t.ID); i >= 0 {
		t = sc.tasks[i]
	}
	sc.mu.Unlock()
	b, err := s.resolveModel(ctx, "task."+t.ID)
	return t, b, err
}

// Only explicit pre-delivery refusals permit starting a replacement conversation.
func taskConversationUnavailable(err error, id string) bool {
	text := err.Error()
	return text == "conversation "+id+" is gone" ||
		text == "task execution context differs from this conversation; start a new conversation" ||
		strings.HasPrefix(text, "config_dir cannot move an existing conversation;")
}

// Use the launcher's own capability metadata when it advertises model-specific thinking levels.
func (s *Service) checkTaskModelCapabilities(ctx context.Context, machine string, b ModelBinding) error {
	launcher := strings.TrimPrefix(b.Provider, "launcher.")
	if b.Model != "" {
		if err := s.pickedModelRefusal(ctx, machine, launcher, b.Model); err != nil {
			return err
		}
	}
	if b.Effort == "" {
		return nil
	}
	list, err := s.askLauncherModels(ctx, machine, launcher, false)
	if err != nil {
		return nil
	} // older catalogs do not advertise these levels; the node still validates its input
	for _, m := range list.Models {
		if b.Model == m.ID || b.Model != "" && b.Model == m.Resolved || b.Model == "" && m.Default {
			if len(m.Efforts) > 0 && !slices.Contains(m.Efforts, b.Effort) {
				return i18n.Err("assistant-models.err_effort_unsupported")
			}
			break
		}
	}
	return nil
}
