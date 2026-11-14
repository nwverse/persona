package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Behaviour checks for the main-chat dispatch decision (#4829, docs/assistant/prompts.md "Main-chat dispatch checks"):
// testdata/dispatch/main_chat.json holds one user message and a minimal world (online machines, knowledge-base project
// mappings) per case, with the actions the assistant should take. TestDispatchScenariosAreWellFormed runs everywhere
// and keeps the cases consistent with the hub's own start rules; TestDispatchDecisionLive asks a real model and runs
// only when PERSONA_LIVE_AI is set:
//
//	PERSONA_LIVE_AI='{"base_url":"…","api_key_file":"…","model":"…"}' go test -run 'DispatchScenariosAreWellFormed|DispatchDecisionLive' -v ./internal/assistant
//
// The expectations are tool-call level, so a future end-to-end harness (a real headless assistant against a stub hub)
// can reuse the same case file unchanged.

var dispatchEvalTimeout = 90 * time.Second

// dispatchEvalTask is one task the assistant would hand to task_plan: as a case expects it, and as the model answers it
// (prompts/assistant/dispatch_eval.md). The title is not compared, only the fields the hub rules act on.
type dispatchEvalTask struct {
	Title         string `json:"title"`
	Machine       string `json:"machine"`
	Dir           string `json:"dir"`
	Permission    string `json:"permission"`
	UserRequested bool   `json:"user_requested"`
}

type dispatchEvalExpect struct {
	Tasks          []dispatchEvalTask `json:"tasks"`
	Start          string         `json:"start"` // auto | card | none, as dispatchStartOf derives
	AsksUser       bool           `json:"asks_user"`
	DirectToolsAny []string       `json:"direct_tools_any"` // instant-tier tool prefixes, any of which passes
}

type dispatchEvalMachine struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

type dispatchEvalProject struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type dispatchEvalScenario struct {
	Name     string            `json:"name"`
	Title    string            `json:"title"`
	Message  string            `json:"message"`
	Machines []dispatchEvalMachine `json:"machines"`
	Projects []dispatchEvalProject `json:"projects"`
	Known    string            `json:"known"`
	Expect   dispatchEvalExpect    `json:"expect"`
}

// dispatchEvalDecision is what the model answers (prompts/assistant/dispatch_eval.md).
type dispatchEvalDecision struct {
	Tasks       []dispatchEvalTask `json:"tasks"`
	AsksUser    bool           `json:"asks_user"`
	DirectTools []string       `json:"direct_tools"`
	Reply       string         `json:"reply"`
}

// dispatchStartOf applies the hub's own start rule (docs/assistant/unified-entry.md "Dispatch"): a plan starts
// without the card when every task is read-only (permission plan) or the user asked for it in so many words; a plan
// with anything else waits for the user's "Start".
func dispatchStartOf(tasks []dispatchEvalTask) string {
	if len(tasks) == 0 {
		return "none"
	}
	for _, t := range tasks {
		if strings.ToLower(t.Permission) != "plan" && !t.UserRequested {
			return "card"
		}
	}
	return "auto"
}

func loadDispatchScenarios(t *testing.T) []dispatchEvalScenario {
	t.Helper()
	f, err := os.Open("testdata/dispatch/main_chat.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var file struct {
		About     string             `json:"about"`
		Scenarios []dispatchEvalScenario `json:"scenarios"`
	}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields() // a misspelt field would otherwise mean nothing, silently
	if err := dec.Decode(&file); err != nil {
		t.Fatal(err)
	}
	return file.Scenarios
}

// TestDispatchScenariosAreWellFormed keeps the cases honest without a model: names are unique, machines and dirs are
// the world's own, permissions are ones the hub gives, the start a case wants is the one the hub's rule derives from
// its tasks, asking the user and planning tasks are exclusive, and every case decides on exactly one thing.
func TestDispatchScenariosAreWellFormed(t *testing.T) {
	scenarios := loadDispatchScenarios(t)
	if len(scenarios) < 6 {
		t.Fatalf("%d scenarios, want at least 6", len(scenarios))
	}
	seen := map[string]bool{}
	for _, sc := range scenarios {
		if sc.Name == "" || sc.Title == "" || sc.Message == "" {
			t.Fatalf("a scenario lacks name, title or message: %+v", sc)
		}
		if seen[sc.Name] {
			t.Fatalf("duplicate name %q", sc.Name)
		}
		seen[sc.Name] = true

		machines := map[string]bool{}
		for _, m := range sc.Machines {
			machines[m.ID] = true
		}
		dirs := map[string]bool{}
		for _, p := range sc.Projects {
			dirs[p.Dir] = true
		}
		for _, task := range sc.Expect.Tasks {
			if !machines[task.Machine] {
				t.Errorf("%s: task machine %q is not one of the world's machines", sc.Name, task.Machine)
			}
			if len(sc.Projects) > 0 && !dirs[task.Dir] {
				t.Errorf("%s: task dir %q is not one of the world's project dirs", sc.Name, task.Dir)
			}
			switch strings.ToLower(task.Permission) {
			case "plan", "default":
			default:
				t.Errorf("%s: task permission %q is not one this check gives (plan, default)", sc.Name, task.Permission)
			}
		}
		if got := dispatchStartOf(sc.Expect.Tasks); got != sc.Expect.Start {
			t.Errorf("%s: start %q, but the hub's rule derives %q from these tasks", sc.Name, sc.Expect.Start, got)
		}
		if sc.Expect.AsksUser && len(sc.Expect.Tasks) > 0 {
			t.Errorf("%s: asks_user but also plans %d tasks", sc.Name, len(sc.Expect.Tasks))
		}
		if len(sc.Expect.DirectToolsAny) > 0 && len(sc.Expect.Tasks) > 0 {
			t.Errorf("%s: direct tools but also plans %d tasks", sc.Name, len(sc.Expect.Tasks))
		}
		if len(sc.Expect.Tasks) == 0 && !sc.Expect.AsksUser && len(sc.Expect.DirectToolsAny) == 0 {
			t.Errorf("%s: expects nothing at all", sc.Name)
		}
	}
}

// TestDispatchEvalPromptsGolden pins the prompt pair (docs/assistant/prompts.md); regenerate with
// UPDATE_GOLDEN=1 go test ./internal/assistant -run DispatchEvalPromptsGolden and read the diff.
func TestDispatchEvalPromptsGolden(t *testing.T) {
	scenarios := loadDispatchScenarios(t)
	var sc dispatchEvalScenario
	for _, s := range scenarios {
		if s.Name == "readonly-research-autostart" { // machines and projects present, one task
			sc = s
			break
		}
	}
	if sc.Name == "" {
		t.Fatal("readonly-research-autostart scenario is gone; pick another with machines and projects")
	}
	golden(t, "dispatch_eval.system", prompts.RenderDispatchEval(prompts.DispatchEval{Owner: "Alex"}))
	in := prompts.DispatchEvalInput{Message: sc.Message}
	for _, m := range sc.Machines {
		in.Machines = append(in.Machines, prompts.DispatchEvalMachine{ID: m.ID, Note: m.Note})
	}
	for _, p := range sc.Projects {
		in.Projects = append(in.Projects, prompts.DispatchEvalProject{Name: p.Name, Dir: p.Dir})
	}
	golden(t, "dispatch_eval.input", prompts.RenderDispatchEvalInput(in))
}

// TestDispatchDecisionLive asks a real model (PERSONA_LIVE_AI) what the assistant would do for every case and prints
// one row each; a case it gets wrong fails, unless the case records why it is known to miss. Run it before and after
// changing dispatch_eval.md / dispatch_eval_input.md, the dispatch rules in docs/assistant/unified-entry.md, or the
// model behind the main chat, and put both tables in the PR.
func TestDispatchDecisionLive(t *testing.T) {
	raw := os.Getenv("PERSONA_LIVE_AI")
	if raw == "" {
		t.Skip("PERSONA_LIVE_AI not set")
	}
	var cfg ai.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	runDispatchLive(t, ai.New(cfg), cfg.Model)
}

func (sc dispatchEvalScenario) decide(ctx context.Context, m jsonModel) (dispatchEvalDecision, error) {
	system := prompts.RenderDispatchEval(prompts.DispatchEval{Owner: "Alex"})
	in := prompts.DispatchEvalInput{Message: sc.Message}
	for _, mm := range sc.Machines {
		in.Machines = append(in.Machines, prompts.DispatchEvalMachine{ID: mm.ID, Note: mm.Note})
	}
	for _, p := range sc.Projects {
		in.Projects = append(in.Projects, prompts.DispatchEvalProject{Name: p.Name, Dir: p.Dir})
	}
	var out dispatchEvalDecision
	err := m.JSON(ctx, system, prompts.RenderDispatchEvalInput(in), &out)
	return out, err
}

// mismatch compares the decision with the case, field by field: every expected task matched by one the model planned
// (machine, dir, permission, user-requested; titles are free), the start the hub would derive, ask_user, and the
// direct tools (any of the expected prefixes; none when the case expects none).
func (sc dispatchEvalScenario) mismatch(d dispatchEvalDecision) string {
	var miss []string
	if len(d.Tasks) != len(sc.Expect.Tasks) {
		miss = append(miss, fmt.Sprintf("%d tasks, want %d", len(d.Tasks), len(sc.Expect.Tasks)))
	} else {
		remaining := slices.Clone(sc.Expect.Tasks)
		for _, got := range d.Tasks {
			i := slices.IndexFunc(remaining, func(w dispatchEvalTask) bool {
				return strings.EqualFold(strings.TrimSpace(w.Machine), strings.TrimSpace(got.Machine)) &&
					strings.TrimSpace(w.Dir) == strings.TrimSpace(got.Dir) &&
					strings.EqualFold(strings.TrimSpace(w.Permission), strings.TrimSpace(got.Permission)) &&
					w.UserRequested == got.UserRequested
			})
			if i < 0 {
				miss = append(miss, fmt.Sprintf("task machine=%q dir=%q permission=%q user_requested=%v matches none of the expected %v",
					got.Machine, got.Dir, got.Permission, got.UserRequested, sc.Expect.Tasks))
				break
			}
			remaining = slices.Delete(remaining, i, i+1)
		}
	}
	if got := dispatchStartOf(d.Tasks); got != sc.Expect.Start {
		miss = append(miss, fmt.Sprintf("start %s, want %s", got, sc.Expect.Start))
	}
	if d.AsksUser != sc.Expect.AsksUser {
		miss = append(miss, fmt.Sprintf("asks_user %v, want %v", d.AsksUser, sc.Expect.AsksUser))
	}
	if len(sc.Expect.DirectToolsAny) > 0 {
		hit := slices.ContainsFunc(d.DirectTools, func(tool string) bool {
			return slices.ContainsFunc(sc.Expect.DirectToolsAny, func(p string) bool {
				return strings.HasPrefix(strings.ToLower(strings.TrimSpace(tool)), p)
			})
		})
		if !hit {
			miss = append(miss, fmt.Sprintf("no direct tool with prefix %v (got %v)", sc.Expect.DirectToolsAny, d.DirectTools))
		}
	} else if len(d.DirectTools) > 0 {
		miss = append(miss, fmt.Sprintf("direct tools %v, want none", d.DirectTools))
	}
	return strings.Join(miss, "; ")
}

// runDispatchLive decides every case with m (named model in the table) and fails on each case it gets wrong.
func runDispatchLive(t *testing.T, m jsonModel, model string) {
	t.Helper()
	var rows []string
	expected, failed := 0, 0
	for _, sc := range loadDispatchScenarios(t) {
		ctx, cancel := context.WithTimeout(context.Background(), dispatchEvalTimeout)
		start := time.Now()
		d, err := sc.decide(ctx, m)
		cancel()
		got := fmt.Sprintf("%dt/%s/ask=%v/tools=%v", len(d.Tasks), dispatchStartOf(d.Tasks), d.AsksUser, d.DirectTools)
		miss := sc.mismatch(d)
		if err != nil {
			miss = "error: " + err.Error()
		}
		mark := "ok  "
		if miss != "" {
			if err == nil && sc.Known != "" {
				mark = "KNOWN"
			} else {
				mark = "MISS"
				failed++
				if err == nil {
					t.Errorf("%s: %s (reply %q)", sc.Name, miss, d.Reply)
				} else {
					t.Errorf("%s: %s", sc.Name, miss)
				}
			}
		} else {
			expected++
		}
		want := fmt.Sprintf("%dt/%s/ask=%v", len(sc.Expect.Tasks), sc.Expect.Start, sc.Expect.AsksUser)
		rows = append(rows, fmt.Sprintf("%s %-30s %-46s want %-28s %5.1fs %s", mark, sc.Name, got, want,
			time.Since(start).Seconds(), oneLine(d.Reply)))
	}
	t.Logf("main-chat dispatch on %s: %d of %d as expected\n%s", model, expected, len(rows), strings.Join(rows, "\n"))
}
