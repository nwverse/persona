package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/proto"
	"net/http"
	"testing"
)

func TestTaskExecutionSaveAndRun(t *testing.T) {
	f := newFakeHub().withMachine()
	f.sessions["remote"] = nil
	f.zshrc = map[string][]proto.Launcher{"remote": {{Name: "codex", Agent: "codex"}}}
	f.answerNode("assistant.directory", func(machine string, raw json.RawMessage) (any, error) {
		if machine != "remote" {
			t.Errorf("directory checked on %s", machine)
		}
		return map[string]any{"cwd": "/work/project"}, nil
	})
	s := newTestService(t, f)
	body := `{"title":"Check files","prompt":"Summarize files","repeat":"interval","every_minutes":30,"enabled":true,"launcher":"codex","model":"test-model","execution_machine_id":"remote","cwd":"/work/project"}`
	r := request(s, "POST", "/api/v1/assistant/tasks", body)
	if r.Code != http.StatusOK {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var got struct{ Task map[string]any }
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Task["launcher"] != "codex" || got.Task["model"] != "test-model" || got.Task["cwd"] != "/work/project" {
		t.Fatalf("saved configuration: %v", got.Task)
	}
	id := got.Task["id"].(string)
	if b, _ := s.resolveModel(context.Background(), "assistant"); b.Provider != "launcher.claude" {
		t.Fatalf("main changed: %+v", b)
	}
	first, err := runNow(t, s, id)
	if err != nil {
		t.Fatal(err)
	}
	if first.MachineID != "remote" {
		t.Fatalf("started on %s", first.MachineID)
	}
	p := f.lastSend(t)
	if p["cwd"] != "/work/project" || p["launcher"] != "codex" || p["model"] != "test-model" {
		t.Fatalf("execution: %v", p)
	}
	runs := waitRuns(t, s, 1)
	raw, _ := json.Marshal(runs[0])
	var history map[string]any
	_ = json.Unmarshal(raw, &history)
	execution, _ := history["execution"].(map[string]any)
	if execution["cwd"] != "/work/project" || execution["model"] != "test-model" {
		t.Fatalf("history: %s", raw)
	}
	// Omitted fields from an old client keep the configuration.
	r = request(s, "PUT", "/api/v1/assistant/tasks/"+id, `{"title":"Changed title","prompt":"Summarize files","repeat":"interval","every_minutes":30,"enabled":true}`)
	if r.Code != http.StatusOK {
		t.Fatalf("old edit: %s", r.Body.String())
	}
	second, err := runNow(t, s, id)
	if err != nil || second.SessionID != first.SessionID {
		t.Fatalf("compatible resume: %+v %v", second, err)
	}
	// A directory change starts a new conversation and keeps the old run's evidence.
	f.answerNode("assistant.directory", func(string, json.RawMessage) (any, error) { return map[string]any{"cwd": "/work/other"}, nil })
	r = request(s, "PUT", "/api/v1/assistant/tasks/"+id, `{"title":"Changed title","prompt":"Summarize files","repeat":"interval","every_minutes":30,"enabled":true,"cwd":"/work/other"}`)
	if r.Code != http.StatusOK {
		t.Fatalf("directory edit: %s", r.Body.String())
	}
	r = request(s, "POST", "/api/v1/assistant/tasks/"+id+"/run", `{}`)
	if r.Code != http.StatusOK {
		t.Fatalf("manual run: %s", r.Body.String())
	}
	var third struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &third); err != nil {
		t.Fatal(err)
	}
	if third.SessionID == first.SessionID {
		t.Fatalf("manual run reused incompatible conversation: %+v", third)
	}
	historyRuns := waitRuns(t, s, 3)
	if !historyRuns[2].Manual || historyRuns[2].Execution.Cwd != "/work/other" {
		t.Fatalf("manual configuration absent: %+v", historyRuns[2])
	}
}

func TestTaskExecutionUnavailableDoesNotSend(t *testing.T) {
	for _, failure := range []string{"directory", "model", "offline"} {
		t.Run(failure, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newTestService(t, f)
			setTasks(s, Task{ID: "job", Title: "Check", Prompt: "Check", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true})
			r := request(s, "PUT", "/api/v1/assistant/tasks/job", `{"title":"Check","prompt":"Check","repeat":"interval","every_minutes":30,"enabled":true,"cwd":"/work/project"}`)
			// The fake validates the directory without touching this machine's filesystem.
			if r.Code != http.StatusOK {
				t.Fatalf("save: %s", r.Body.String())
			}
			switch failure {
			case "directory":
				f.answerNode("assistant.directory", func(string, json.RawMessage) (any, error) { return nil, errors.New("directory missing") })
			case "model":
				setModel(t, s, "task.job", ModelBinding{Provider: "launcher.claude", Model: "missing-model"})
				f.answerNode("launchers.model.check", func(string, json.RawMessage) (any, error) { return nil, errors.New("model unavailable") })
			case "offline":
				f.devices = []Device{{ID: "server", Online: false}}
			}
			before := f.sent()
			if _, err := runNow(t, s, "job"); err == nil {
				t.Fatal("unavailable configuration ran")
			}
			if f.sent() != before {
				t.Fatal("sent with unavailable configuration")
			}
		})
	}
}

func TestTaskExecutionSaveIsAtomicAndRevisionChecked(t *testing.T) {
	f := newFakeHub().withMachine()
	st := newSettingsFaultStore()
	s := newServiceOn(t, f, st)
	setTasks(s, Task{ID: "job", Title: "Before", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true})
	body := `{"title":"After","prompt":"Check files","repeat":"interval","every_minutes":30,"enabled":true,"model":"test-model","model_revision":0,"cwd":"/work/project"}`
	st.failBatch = tasksKey
	if r := request(s, "PUT", "/api/v1/assistant/tasks/job", body); r.Code == http.StatusOK {
		t.Fatal("failed transaction was accepted")
	}
	task, _ := taskByID(s, "job")
	record, err := s.modelRecord(context.Background(), "task.job")
	if err != nil || task.Title != "Before" || task.Cwd != "" || record.Revision != 0 {
		t.Fatalf("partial save: %+v %+v %v", task, record, err)
	}
	st.failBatch = ""
	if r := request(s, "PUT", "/api/v1/assistant/tasks/job", body); r.Code != http.StatusOK {
		t.Fatalf("save: %s", r.Body.String())
	}
	if r := request(s, "PUT", "/api/v1/assistant/tasks/job", body); r.Code != http.StatusConflict {
		t.Fatalf("stale edit: %d %s", r.Code, r.Body.String())
	}
	// Reconstruct the service to prove both settings survive a restart.
	restarted := newServiceOn(t, f, st)
	view, ok := restarted.taskView(context.Background(), "job")
	if !ok || view.Model != "test-model" || view.Launcher != "claude" || view.Cwd != "/work/project" || view.ModelRevision != 1 {
		t.Fatalf("restart: %+v", view)
	}
}

func TestTaskExecutionModelAndComputerChangesStartFresh(t *testing.T) {
	f := newFakeHub().withMachine()
	f.sessions["remote"] = nil
	s := newTestService(t, f)
	setTasks(s, Task{ID: "job", Title: "Check", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true})
	first, err := runNow(t, s, "job")
	if err != nil {
		t.Fatal(err)
	}
	setModel(t, s, "task.job", ModelBinding{Provider: "launcher.claude", Model: "test-model"})
	second, err := runNow(t, s, "job")
	if err != nil || first.SessionID == second.SessionID {
		t.Fatalf("model change resumed: %+v %v", second, err)
	}
	r := request(s, "PUT", "/api/v1/assistant/tasks/job", `{"title":"Check","prompt":"Check files","repeat":"interval","every_minutes":30,"enabled":true,"execution_machine_id":"remote"}`)
	if r.Code != http.StatusOK {
		t.Fatalf("computer edit: %s", r.Body.String())
	}
	third, err := runNow(t, s, "job")
	if err != nil || third.SessionID == second.SessionID || third.MachineID != "remote" {
		t.Fatalf("computer change: %+v %v", third, err)
	}
	runs := waitRuns(t, s, 3)
	if runs[1].ConversationReason != "execution_changed" || runs[2].ConversationReason != "execution_changed" {
		t.Fatalf("context transitions absent from history: %+v", runs)
	}
}

func TestTaskExecutionChecksAdvertisedModelEffort(t *testing.T) {
	f := newFakeHub().withMachine()
	f.agentModels = map[string][]proto.AgentModel{"claude": {{ID: "test-model", Efforts: []string{"low"}}}}
	s := newTestService(t, f)
	body := `{"title":"Check","prompt":"Check files","repeat":"interval","every_minutes":30,"enabled":true,"model":"test-model","effort":"high"}`
	if r := request(s, "POST", "/api/v1/assistant/tasks", body); r.Code != http.StatusBadRequest {
		t.Fatalf("unsupported effort saved: %d %s", r.Code, r.Body.String())
	}
}

func TestTaskExecutionLegacyDefaultsRemainInherited(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	st := newSettingsFaultStore()
	// Persist a pre-configuration record with none of the new execution fields.
	legacy := []map[string]any{{"id": "legacy", "title": "Old task", "prompt": "Check files", "repeat": "interval", "every_minutes": 30, "enabled": true}}
	if err := st.PutSetting(ctx, tasksKey, legacy); err != nil {
		t.Fatal(err)
	}
	s := newServiceOn(t, f, st)
	main, err := s.resolveModel(ctx, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	view, ok := s.taskView(ctx, "legacy")
	if !ok || view.Execution == nil || view.Execution.Launcher != "claude" || view.Execution.Model != main.Model || view.Execution.Effort != main.Effort {
		t.Fatalf("legacy defaults: %+v", view)
	}
	r := request(s, "PUT", "/api/v1/assistant/tasks/legacy", `{"title":"Edited old task","prompt":"Check files","repeat":"interval","every_minutes":30,"enabled":true}`)
	if r.Code != http.StatusOK {
		t.Fatalf("legacy edit: %s", r.Body.String())
	}
	var override modelRecord
	if exists, err := st.Setting(ctx, modelRecordKey("task.legacy"), &override); err != nil || exists {
		t.Fatalf("legacy edit created an override: %v %v %+v", exists, err, override)
	}
	first, err := runNow(t, s, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	waitRuns(t, s, 1)
	// An older stored conversation has no execution snapshot; defaults still resume it after a restart.
	stored, _ := taskByID(s, "legacy")
	stored.RunExecution = nil
	if err := st.PutSetting(ctx, tasksKey, []Task{stored}); err != nil {
		t.Fatal(err)
	}
	restarted := newServiceOn(t, f, st)
	second, err := runNow(t, restarted, "legacy")
	if err != nil || second.SessionID != first.SessionID {
		t.Fatalf("legacy reuse: %+v %v", second, err)
	}
	if got := f.lastSend(t); got["launcher"] != "claude" {
		t.Fatalf("legacy execution: %v", got)
	}
}
