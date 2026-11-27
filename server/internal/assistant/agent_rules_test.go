package assistant

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// fakeAgentRules answers every start with texts, and keeps what it was asked.
type fakeAgentRules struct {
	mu    sync.Mutex
	asked []wire.AgentRulesForStartParams
	texts []string
}

func (f *fakeAgentRules) AgentRulesFor(_ context.Context, start wire.AgentRulesForStartParams) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, start)
	return f.texts
}

// A new task Agent starts with the rules the agent_rules extensions answered for its agent type, launcher, model,
// directory and computer, each as its own block after Persona's framework rules: in task_instructions on a node that
// keeps them, in the first message on an older one. No rules (no source, or none answered) leave the start as it was.
func TestTaskStartCarriesAgentRules(t *testing.T) {
	const ruleA, ruleB = "<rules-a>Run the documentation tests before you finish.</rules-a>", "<rules-b>Keep commits small.</rules-b>"
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "separate"}[modern], func(t *testing.T) {
			start := func(src agentRuleSources) map[string]any {
				s, f, _ := outcomeEnv(t, nil, outcomeReply)
				f.mu.Lock()
				f.devices = []Device{{ID: "server", Online: true, Reachable: true}, {ID: "box", Online: true, Reachable: true, TaskInstructions: modern}}
				f.mu.Unlock()
				s.agentRules = src
				task := PlanTask{Title: "Fix the docs", Prompt: "Fix the heading levels in the README.", Machine: "box", Cwd: "/code/weather", Model: "example-model"}
				if _, _, err := s.startTaskAgent(t.Context(), &task, false); err != nil {
					t.Fatal(err)
				}
				return startsOf(f)[0]
			}
			text := func(st map[string]any) string {
				if modern {
					rules, _ := st["task_instructions"].(string)
					return rules
				}
				prompt, _ := st["prompt"].(string)
				return prompt
			}

			plain := start(nil)
			if strings.Contains(text(plain), "<rules-") {
				t.Fatal("rules without a source")
			}
			if got := text(start(&fakeAgentRules{})); got != text(plain) {
				t.Fatalf("no answers changed the start:\n%s\n---\n%s", got, text(plain))
			}

			src := &fakeAgentRules{texts: []string{ruleA, ruleB}}
			st := start(src)
			if got := text(st); !strings.Contains(got, "\n\n"+ruleA+"\n\n"+ruleB) {
				t.Fatalf("the rules, in order, each its own block: %q", got)
			}
			if modern {
				if prompt, _ := st["prompt"].(string); strings.Contains(prompt, "<rules-") {
					t.Fatalf("framework rules stay out of the assignment: %q", prompt)
				}
			}
			if len(src.asked) != 1 {
				t.Fatalf("asked %d times", len(src.asked))
			}
			asked, launcher, model := src.asked[0], st["launcher"], st["model"]
			if asked.Cwd != "/code/weather" || asked.Machine != "box" || asked.Model != model || asked.Launcher != launcher || asked.Agent == "" ||
				asked.Contribution != "" {
				t.Fatalf("asked %+v; started launcher=%v model=%v", asked, launcher, model)
			}
		})
	}
}
