package node

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestAgyNewCommandBlockIgnoresPreviousSwitchUntilNewOutputAppears(t *testing.T) {
	before := strings.Join([]string{
		"Earlier answer",
		"> /model",
		"⎿  Model set to Gemini 4 Carbon (Low)",
		"────────────────────────────────────────",
		">",
		"────────────────────────────────────────",
		"? for shortcutsauto · Gemini 4 Carbon · low",
	}, "\n")

	// Unchanged screen after sending the next `/model` must remain unconfirmed rather than matching the previous block.
	if err := agyModelOutcome(before, before, "gemini-4-neon-xhigh"); err == nil {
		t.Fatal("expected unconfirmed while screen still shows only the earlier switch")
	}

	// Same model selected again appends a second block below the first.
	sameAgain := strings.Join([]string{
		"Earlier answer",
		"> /model",
		"⎿  Model set to Gemini 4 Carbon (Low)",
		"> /model",
		"⎿  Model already set to Gemini 4 Carbon (Low)",
		"────────────────────────────────────────",
		">",
		"────────────────────────────────────────",
	}, "\n")
	if err := agyModelOutcome(before, sameAgain, "gemini-4-carbon-low"); err != nil {
		t.Fatalf("already-set confirmation failed: %v", err)
	}

	// Rejected model surfaces an explicit refusal error.
	rejected := strings.Join([]string{
		"Earlier answer",
		"> /model",
		"⎿  Model set to Gemini 4 Carbon (Low)",
		"> /model",
		`⎿  Invalid model "bad-model" (available: gemini-4-carbon-low)`,
		"────────────────────────────────────────",
		">",
		"────────────────────────────────────────",
	}, "\n")
	if err := agyModelOutcome(before, rejected, "bad-model"); err == nil || !strings.Contains(err.Error(), "bad-model") {
		t.Fatalf("expected invalid-model refusal, got %v", err)
	}
}

func TestAgyPaneSwitchModelAndEffortEndToEnd(t *testing.T) {
	oldLook := modelLook
	modelLook = time.Millisecond
	t.Cleanup(func() { modelLook = oldLook })

	n, f, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	seedAgents(t, n, f, agyTestAgent(agyTestPane, home, agyTestID))
	n.noteWorkAgent(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), agyTestPane, agyTestRow, home, agyAgent, agyAgent, "gemini-4-neon-xhigh", "xhigh", agyStartMode(""))
	waitFor(t, "initial resume report", func() bool { return len(f.trace("pane.report_agent_session")) > 0 })

	t0 := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	transcriptFile := agyTestFile(t, n.agyDataRoot(), agyTestID,
		`{"step_index":0,"type":"PLANNER_RESPONSE","created_at":"`+t0.Format(time.RFC3339)+`","model":"gemini-4-neon-xhigh","input_tokens":1000,"output_tokens":50}`+"\n")

	f.setScreen(agyTestPane, "Welcome\n────────────────────────────────────────\n>\n────────────────────────────────────────\n? for shortcuts\n")
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		text := str(c.Params["prompt"])
		if text == "" {
			text = str(c.Params["text"])
		}
		switch text {
		case "/model gemini-4-carbon-high":
			f.setScreen(agyTestPane, "> /model\n⎿  Model set to Gemini 4 Carbon (High)\n────────────────────────────────────────\n>\n────────────────────────────────────────\n")
		case "/model gpt-oss-120b-medium":
			f.setScreen(agyTestPane, "> /model\n⎿  Model set to GPT-OSS 120B (Medium)\n────────────────────────────────────────\n>\n────────────────────────────────────────\n")
		case "/model gemini-pro-orchestrator":
			f.setScreen(agyTestPane, "> /model\n⎿  Model set to Orchestrator (Carbon)\n────────────────────────────────────────\n>\n────────────────────────────────────────\n")
		case "/effort low":
			f.setScreen(agyTestPane, "> /effort\n⎿  Effort set to low\n────────────────────────────────────────\n>\n────────────────────────────────────────\n")
		}
	})

	// 1. Switch model variant (`pickPlan` sends {model} then {effort} matching the variant's suffix).
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "model": "gemini-4-carbon-high"}); err != nil {
		t.Fatalf("switch model: %v", err)
	}
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "effort": "high"}); err != nil {
		t.Fatalf("follow-up matching effort: %v", err)
	}
	prompts := f.trace("agent.prompt")
	if len(prompts) != 1 || !strings.Contains(prompts[0], "/model gemini-4-carbon-high") {
		t.Fatalf("expected one /model prompt and no redundant /effort prompt, got %v", prompts)
	}

	row := listed(t, n)[agyTestRow]
	if row.Model != "gemini-4-carbon-high" || row.Effort != "high" || row.Capabilities == nil || row.Capabilities.Model == nil || !*row.Capabilities.Model {
		t.Fatalf("switched agy row = model %q effort %q caps %+v", row.Model, row.Effort, row.Capabilities)
	}
	rawInfo, err := nodeCall(t, n, "session.info", map[string]any{"session_id": agyTestRow})
	if err != nil {
		t.Fatalf("session.info: %v", err)
	}
	var info map[string]any
	_ = json.Unmarshal(rawInfo, &info)
	if info["model"] != "gemini-4-carbon-high" || info["effort"] != "high" {
		t.Fatalf("session.info after switch = %+v", info)
	}
	waitFor(t, "updated resume report", func() bool {
		reports := f.trace("pane.report_agent_session")
		return strings.Contains(reports[len(reports)-1], "--model gemini-4-carbon-high")
	})

	// 2. Direct effort-only change swaps the variant suffix via `/effort low`.
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "effort": "low"}); err != nil {
		t.Fatalf("switch effort: %v", err)
	}
	row = listed(t, n)[agyTestRow]
	if row.Model != "gemini-4-carbon-low" || row.Effort != "low" {
		t.Fatalf("after effort switch: model=%q effort=%q", row.Model, row.Effort)
	}

	// 3. Single-variant model followed by its own effort (`gpt-oss-120b-medium` + `medium`) succeeds without `/effort`.
	beforePrompts := len(f.trace("agent.prompt"))
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "model": "gpt-oss-120b-medium", "effort": "medium"}); err != nil {
		t.Fatalf("single-variant switch: %v", err)
	}
	if got := len(f.trace("agent.prompt")) - beforePrompts; got != 1 {
		t.Fatalf("expected 1 prompt for single-variant model+effort, got %d", got)
	}

	// 4. Switching to an effort-less orchestrator model clears stale effort.
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "model": "gemini-pro-orchestrator"}); err != nil {
		t.Fatalf("orchestrator switch: %v", err)
	}
	row = listed(t, n)[agyTestRow]
	if row.Model != "gemini-pro-orchestrator" || row.Effort != "" {
		t.Fatalf("after orchestrator switch: model=%q effort=%q", row.Model, row.Effort)
	}

	// 5. A newer transcript reply with another model supersedes the pending switch.
	later := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	agyTestAppend(t, transcriptFile, `{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"`+later+`","model":"gemini-4-neon-low","input_tokens":1200,"output_tokens":40}`+"\n")
	// Force fresh stat past the 1s cache window by clearing agyMetas.
	n.mu.Lock()
	n.agyMetas = nil
	n.mu.Unlock()
	row = listed(t, n)[agyTestRow]
	if row.Model != "gemini-4-neon-low" || row.Effort != "low" {
		t.Fatalf("after newer transcript step: model=%q effort=%q", row.Model, row.Effort)
	}
}

func TestAgyPaneSwitchRefusesWorkingAndOpenDialog(t *testing.T) {
	n, f, home := herdrNode(t)
	busy := agyTestAgent(agyTestPane, home, agyTestID)
	busy["agent_status"] = proto.StatusWorking
	seedAgents(t, n, f, busy)
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "model": "gemini-4-carbon-high"}); err == nil || !strings.Contains(err.Error(), "正在工作") {
		t.Fatalf("expected busy refusal, got %v", err)
	}

	idle := agyTestAgent(agyTestPane, home, agyTestID)
	seedAgents(t, n, f, idle)
	f.setScreen(agyTestPane, "Do you trust the contents of this project?\n> 1. Yes, I trust this folder\n  2. No, exit\n")
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": agyTestRow, "model": "gemini-4-carbon-high"}); refusalCode(err) != "dialog_open" {
		t.Fatalf("expected dialog_open refusal, got %v", err)
	}
	if prompts := f.trace("agent.prompt"); len(prompts) != 0 {
		t.Fatalf("unexpected prompts sent while busy or dialog open: %v", prompts)
	}
}
