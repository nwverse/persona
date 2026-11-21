package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestTaskInputContextUsesDraftExecutionWithoutSaving(t *testing.T) {
	f := newFakeHub().withMachine()
	f.sessions["remote"] = nil
	f.zshrc = map[string][]proto.Launcher{"remote": {{Name: "codex", Agent: "codex"}}}
	f.answerNode("assistant.directory", func(machine string, raw json.RawMessage) (any, error) {
		if machine != "remote" {
			t.Fatalf("directory checked on %s", machine)
		}
		return map[string]any{"cwd": "/work/project"}, nil
	})
	f.answerNode("assistant.content", func(string, json.RawMessage) (any, error) { return map[string]bool{"valid": true}, nil })
	var catalog map[string]any
	f.answerNode("skills.list", func(machine string, raw json.RawMessage) (any, error) {
		if machine != "remote" {
			t.Fatalf("catalog on %s", machine)
		}
		_ = json.Unmarshal(raw, &catalog)
		return assistantskills.Catalog{Version: 1, Complete: true}, nil
	})
	s := newTestService(t, f)
	setTasks(s)
	r := request(s, "POST", "/api/v1/assistant/tasks/input-context", `{"launcher":"codex","model":"test-model","execution_machine_id":"remote","cwd":"/work/project","catalog":true}`)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"task_input":true`) || !strings.Contains(r.Body.String(), `"machine_id":"remote"`) || catalog["cwd"] != "/work/project" || catalog["launcher"] != "codex" {
		t.Fatalf("context: %d %s %+v", r.Code, r.Body.String(), catalog)
	}
	if len(s.tasksView(context.Background(), nil)) != 0 {
		t.Fatal("discovery saved a task")
	}
	b, _ := s.modelRecord(context.Background(), "task.__input_draft")
	if b.Revision != 0 {
		t.Fatal("discovery saved a model override")
	}
}

func TestTaskPromptKeepsSelectionsBeforeScheduleHeader(t *testing.T) {
	block := `<persona-context kind="skill" ref="selected">Review</persona-context>`
	got := taskPrompt(Task{Title: "Review", PromptSelections: true, Prompt: block + "\n\nCheck files", Repeat: repeatInterval, EveryMinutes: 30}, time.Now(), time.Now(), time.UTC, false, false, false)
	refs, err := assistantskills.References(got)
	if err != nil || len(refs) != 1 || refs[0] != "selected" || !strings.Contains(got, "Check files") {
		t.Fatalf("lost selection: %s %v", got, err)
	}
}

func TestTaskInputSaveReopenAndBothRunPaths(t *testing.T) {
	f := newFakeHub().withMachine()
	f.sessions["remote"] = nil
	f.zshrc = map[string][]proto.Launcher{"remote": {{Name: "claude", Agent: "claude"}}}
	s := newTestService(t, f)
	setTasks(s)
	content := 0
	var resumedSession string
	f.answerNode("assistant.content", func(machine string, raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["text"] == "" {
			return map[string]bool{"valid": true}, nil
		}
		content++
		if machine != "remote" || p["cwd"] != "/work/project" || p["launcher"] != "claude" || p["skill_owner"] != s.skillOwner() || p["config_dir"] != meetingProfile || strings.Contains(p["text"].(string), "Literal reference") {
			t.Fatalf("save context: %s %v", machine, p)
		}
		return map[string]any{"valid": true}, nil
	})
	f.answerNode("skills.list", func(machine string, raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if machine != "remote" || p["cwd"] != "/work/project" {
			t.Fatalf("run catalog: %s %v", machine, p)
		}
		catalog := assistantskills.Catalog{Version: 1, Complete: true}
		if (p["session_id"] == "" && p["config_dir"] == meetingProfile) || (resumedSession != "" && p["session_id"] == resumedSession) {
			catalog.Skills = []assistantskills.Entry{{ID: "review", Name: "Review"}}
		}
		return catalog, nil
	})
	prompt := `<persona-context kind="skill" ref="review">Review</persona-context>` + "\n" + `<persona-context kind="file" path="notes.txt" machine="remote" cwd="/work/project" launcher="claude" runtime="">Notes</persona-context>` + "\n\n" + `<persona-context kind="skill" ref="literal">Literal reference</persona-context>` + "\nReview files"
	preview := func(payload map[string]any) {
		t.Helper()
		payload["catalog"] = true
		body, _ := json.Marshal(payload)
		got := request(s, "POST", "/api/v1/assistant/tasks/input-context", string(body))
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"id":"review"`) {
			t.Fatalf("profile-specific preview: %d %s", got.Code, got.Body.String())
		}
	}
	preview(map[string]any{"execution_machine_id": "remote", "cwd": "/work/project", "model": "test-model"})
	body, _ := json.Marshal(map[string]any{"title": "Review", "structured_prompt": prompt, "repeat": "interval", "every_minutes": 30, "enabled": true, "execution_machine_id": "remote", "cwd": "/work/project", "model": "test-model"})
	r := request(s, "POST", "/api/v1/assistant/tasks", string(body))
	if r.Code != 200 {
		t.Fatalf("save: %d %s", r.Code, r.Body.String())
	}
	var reply struct{ Task Task }
	_ = json.Unmarshal(r.Body.Bytes(), &reply)
	if reply.Task.Prompt != prompt || !reply.Task.PromptSelections || content != 1 {
		t.Fatalf("save lost references: %+v", reply)
	}
	restarted := newServiceOn(t, f, s.store())
	reopened := restarted.tasksView(context.Background(), nil)
	if len(reopened) != 1 || reopened[0].Prompt != prompt || !reopened[0].PromptSelections {
		t.Fatalf("reopen: %+v", reopened)
	}
	manual := request(s, "POST", "/api/v1/assistant/tasks/"+reply.Task.ID+"/run", "{}")
	if manual.Code != 200 {
		t.Fatal(manual.Body.String())
	}
	var first proto.Session
	err := json.Unmarshal(manual.Body.Bytes(), &first)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.lastSend(t); got["task_input"] != true || got["skill_owner"] != s.skillOwner() || !strings.Contains(got["text"].(string), `path="notes.txt"`) || got["cwd"] != "/work/project" || got["model"] != "test-model" || got["config_dir"] != meetingProfile {
		t.Fatalf("manual start lost selection or execution parameters: %v", got)
	}
	f.setSessions("remote", proto.Session{SessionID: first.SessionID, Assistant: true, Cwd: "/work/project", Launcher: "claude", Agent: "claude"})
	resumedSession = first.SessionID
	preview(map[string]any{"id": reply.Task.ID})
	task, _ := taskByID(s, reply.Task.ID)
	if _, err := s.runTask(task, time.Now(), time.Now(), false, false); err != nil {
		t.Fatal(err)
	}
	if got := f.lastSend(t); got["task_input"] != true || got["skill_owner"] != s.skillOwner() || got["session_id"] != resumedSession || !strings.Contains(got["text"].(string), `ref="review"`) {
		t.Fatalf("resume lost refs: %v", got)
	}
	f.answerNode("assistant.content", func(string, json.RawMessage) (any, error) { return nil, errors.New("reference changed") })
	before, err := s.modelRecord(t.Context(), "task."+reply.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	edit := strings.NewReplacer(`"title":"Review"`, `"title":"Changed"`, `"model":"test-model"`, `"model":"new-model"`).Replace(string(body))
	r = request(s, "PUT", "/api/v1/assistant/tasks/"+reply.Task.ID, edit)
	if r.Code == 200 {
		t.Fatal("stale reference accepted")
	}
	kept, _ := taskByID(s, reply.Task.ID)
	after, err := s.modelRecord(t.Context(), "task."+reply.Task.ID)
	if err != nil || before != after || kept.Prompt != prompt || kept.Title != "Review" {
		t.Fatal("failed save changed task")
	}
}

func TestTaskInputDoesNotGrantUnattendedStarts(t *testing.T) {
	for _, kind := range []string{"file", "skill"} {
		t.Run(kind, func(t *testing.T) {
			task := Task{PromptSelections: true, Prompt: `<persona-context kind="` + kind + `">Selection</persona-context> Read the notes`}
			if task.grantEligible() {
				t.Fatal("mutable reference admitted a clean-round start grant")
			}
			plain := task
			plain.PromptSelections = false
			plain.StartGrant = &StartGrant{Basis: plain.grantBasis(), Kinds: []string{"edit"}}
			task.StartGrant = plain.StartGrant
			updated := []Task{task}
			dropEditedTaskGrants([]Task{plain}, updated)
			if updated[0].StartGrant != nil {
				t.Fatal("authorizing a reference retained its former literal prompt grant")
			}
		})
	}
}

func TestTaskInputOfflineRefusalIsAtomic(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s, Task{ID: "offline", Title: "Plain", Prompt: "Notes", Repeat: repeatInterval, EveryMinutes: 30})
	f.devices = []Device{{ID: "server", Online: false}}
	prompt := `<persona-context kind="file" machine="server" path="notes" cwd="/work" launcher="claude">Notes</persona-context>`
	body, _ := json.Marshal(map[string]any{"title": "Changed", "structured_prompt": prompt, "repeat": "interval", "every_minutes": 30})
	r := request(s, "PUT", "/api/v1/assistant/tasks/offline", string(body))
	task, _ := taskByID(s, "offline")
	if r.Code == 200 || task.Prompt != "Notes" || task.Title != "Plain" || f.sent() != 0 {
		t.Fatalf("offline edit: %d %+v", r.Code, task)
	}
}

func TestTaskInputLiteralPromptsStayLiteral(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s)
	const block = `<persona-context kind="skill" ref="review">Review</persona-context>`
	const prompt = block + "\n\nExplain the example"
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "prompt_selections": true, "repeat": "interval", "every_minutes": 30})
	got := request(s, "POST", "/api/v1/assistant/tasks", string(body))
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	routine, _ := json.Marshal(map[string]any{"title": "Routine", "prompt": prompt, "time": "12:00"})
	if _, err := s.toolScheduleRoutine(t.Context(), "server", "", routine); err != nil {
		t.Fatal(err)
	}
	followup, _ := json.Marshal(map[string]any{"prompt": prompt, "at": "30m"})
	if _, err := s.toolScheduleFollowup(t.Context(), "server", "source", followup); err != nil {
		t.Fatal(err)
	}
	tasks := s.tasksView(t.Context(), nil)
	if len(tasks) != 3 {
		t.Fatalf("expected three independent tasks, got %d", len(tasks))
	}
	for _, task := range tasks {
		msg := taskPrompt(task, time.Now(), time.Now(), time.UTC, true, false, false)
		refs, err := assistantskills.References(msg)
		if task.Prompt != prompt || task.PromptSelections || !strings.Contains(msg, prompt) || err != nil || len(refs) != 0 {
			t.Fatalf("literal task promoted to a selection: %+v %s", task, msg)
		}
	}
	unknown := Task{}
	const unknownBlock = `<persona-context kind="quote">Quoted</persona-context>`
	unknown.setPrompt(unknownBlock+"\n\nRead", true)
	if unknown.PromptSelections || !unknown.grantEligible() {
		t.Fatal("an ordinary leading context changed grant eligibility")
	}
}

func TestTaskInputSelfFollowupKeepsOwnerSelections(t *testing.T) {
	s := newStorelessService(t, newFakeHub())
	const prefix = `<persona-context kind="skill" ref="one">First</persona-context>`
	task := Task{ID: "job", Title: "Review", Prompt: prefix + "\n\nReview notes", PromptSelections: true, Repeat: repeatInterval, EveryMinutes: 30, Enabled: true}
	setTasks(s, task)
	run := ScheduleRun{Kind: "routine", RefID: task.ID, Title: task.Title, Status: runOK, MachineID: "server", SessionID: "run"}
	s.recordRun(run)
	rendered := taskPrompt(task, time.Now(), time.Now(), time.UTC, false, false, false)
	if !sameText(rendered, task.Prompt) || !copiesRun(run, "", rendered) {
		t.Fatalf("run header no longer identifies its task: %q", rendered)
	}
	args, _ := json.Marshal(map[string]any{"at": "30m", "prompt": rendered})
	if _, err := s.toolScheduleFollowup(t.Context(), "server", "run", args); err != nil {
		t.Fatal(err)
	}
	tasks := s.tasksView(t.Context(), nil)
	if len(tasks) != 1 || tasks[0].Prompt != task.Prompt || !tasks[0].PromptSelections {
		t.Fatalf("copied run changed task or its selections: %+v", tasks)
	}
	args, _ = json.Marshal(map[string]any{"id": task.ID, "prompt": strings.Replace(task.Prompt, `ref="one"`, `ref="other"`, 1)})
	if _, err := s.toolScheduleRoutine(t.Context(), "server", "run", args); err != nil {
		t.Fatal(err)
	}
	updated, _ := taskByID(s, task.ID)
	if updated.PromptSelections {
		t.Fatal("model-authored replacement retained selection authority")
	}
}

func TestTaskInputDiscoveryNeedsTargetAcknowledgement(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s)
	f.answerNode("assistant.content", func(string, json.RawMessage) (any, error) { return map[string]bool{}, nil })
	f.answerNode("skills.list", func(string, json.RawMessage) (any, error) {
		t.Fatal("discovery exposed a catalog without scheduled-input support")
		return nil, nil
	})
	for _, catalog := range []bool{false, true} {
		payload, _ := json.Marshal(map[string]any{"catalog": catalog, "cwd": "/work/project"})
		w := request(s, "POST", "/api/v1/assistant/tasks/input-context", string(payload))
		var reply struct {
			Execution  TaskExecution
			TaskInput  *bool  `json:"task_input"`
			InputError string `json:"input_error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &reply); w.Code != 200 || err != nil || reply.TaskInput == nil || *reply.TaskInput || !strings.Contains(reply.InputError, "Persona") || reply.Execution.MachineID != "server" {
			t.Fatalf("discovery did not explain the unavailable picker: %d %s", w.Code, w.Body.String())
		}
	}
	w := request(s, "POST", "/api/v1/assistant/tasks", `{"title":"Ordinary","prompt":"Check notes","repeat":"interval","every_minutes":30}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
