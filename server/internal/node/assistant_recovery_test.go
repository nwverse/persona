package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A provider refusal after assistant.send returned must not end the conversation without the one bounded same-model
// retry the owner asked for (#3204). The fake CLI is the real node process/stream-json seam, not a model call.
func TestAssistantRefusalRetriesOnceOnTheSameModel(t *testing.T) {
	f := sdkTest(t)
	script := `printf '%s\n' "$*" >> "$SDK_FAKE_DIR/cli-argv"
echo '{"type":"system","subtype":"init"}'
while IFS= read -r line; do
  case "$line" in *'"type":"user"'*)
    printf '%s\n' "$line" >> "$SDK_FAKE_DIR/user.jsonl"
    count=$(cat "$SDK_FAKE_DIR/refusals" 2>/dev/null || echo 0)
    if [ "$count" -eq 0 ]; then
      echo 1 > "$SDK_FAKE_DIR/refusals"
      echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: safeguards flagged this message"}'
    else
      echo '{"type":"stream_event","event":{"type":"message_start","message":{"model":"primary-model","usage":{"input_tokens":5}}},"parent_tool_use_id":null}'
      echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"answered"}},"parent_tool_use_id":null}'
      echo '{"type":"result","subtype":"success","is_error":false,"result":"answered"}'
    fi ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "bin", "claude"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "original request", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sdkWait(t, "the same conversation to retry after its refusal", func() bool {
		return strings.Count(f.file("user.jsonl"), `"type":"user"`) == 2
	})
	if got := f.file("user.jsonl"); strings.Count(got, "original request") != 1 {
		t.Fatalf("the original user message was repeated instead of resumed: %s", got)
	}
	f.idle(t, id)
	if p := f.proc(id); p == nil || p.replyModel != "primary-model" {
		t.Fatalf("the answering model was not retained: %+v", p)
	}
}

func TestAssistantRefusalThenUsesTheNextLauncherModel(t *testing.T) {
	f := sdkTest(t)
	script := `printf '%s\n' "$*" >> "$SDK_FAKE_DIR/cli-argv"
echo '{"type":"system","subtype":"init"}'
echo '{"type":"system","subtype":"background_tasks_changed","tasks":[]}'
model=primary-model
case "$*" in *'--model fallback-model'*) model=fallback-model ;; esac
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*)
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"persona-models","response":{"models":[{"value":"default","displayName":"Default"},{"value":"primary-model","resolvedModel":"primary-model","displayName":"Primary"},{"value":"fallback-model","resolvedModel":"fallback-model","displayName":"Fallback"}]}}}' ;;
    *'"type":"user"'*)
      printf '%s\n' "$line" >> "$SDK_FAKE_DIR/user.jsonl"
      count=$(cat "$SDK_FAKE_DIR/attempts" 2>/dev/null || echo 0)
      count=$((count+1))
      echo "$count" > "$SDK_FAKE_DIR/attempts"
      if [ "$count" -le 2 ]; then
        if [ "$count" -eq 2 ]; then
          echo '{"type":"stream_event","event":{"type":"message_start","message":{"model":"primary-model","usage":{"input_tokens":5}}},"parent_tool_use_id":null}'
        fi
        echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: safeguards flagged this message"}'
      else
        printf '{"type":"stream_event","event":{"type":"message_start","message":{"model":"%s","usage":{"input_tokens":5}}},"parent_tool_use_id":null}\n' "$model"
        printf '{"type":"assistant","uuid":"final","timestamp":"2026-11-10T00:00:00Z","message":{"role":"assistant","model":"%s","content":[{"type":"text","text":"answered by fallback"}]}}\n' "$model" >> "$SDK_FAKE_DIR/transcript.jsonl"
        echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"answered by fallback"}},"parent_tool_use_id":null}'
        echo '{"type":"result","subtype":"success","is_error":false,"result":"answered by fallback"}'
      fi ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "bin", "claude"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "original request", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.n.mu.Lock()
	f.n.paths[id] = filepath.Join(f.dir, "transcript.jsonl")
	f.n.mu.Unlock()
	sdkWait(t, "the second refusal to resume on the next launcher model", func() bool {
		return strings.Count(f.file("user.jsonl"), `"type":"user"`) == 3
	})
	f.idle(t, id)
	if p := f.proc(id); p == nil || p.model.Model != "fallback-model" || p.replyModel != "fallback-model" {
		t.Fatalf("the next model did not answer: %+v", p)
	}
	if sess := f.session(t, id); sess.Model != "fallback-model" || sess.LastText != "answered by fallback" {
		t.Fatalf("the public session did not record the actual answering model: %+v", sess)
	}
	if got := f.file("cli-argv"); !strings.Contains(got, "--model fallback-model") {
		t.Fatalf("the replacement process did not select the fallback model: %s", got)
	}
	if got := f.file("user.jsonl"); strings.Count(got, "original request") != 1 {
		t.Fatalf("the original user message was repeated: %s", got)
	}
}

func TestModelRetryPromptGolden(t *testing.T) {
	goldenNode(t, "model_retry", prompts.RenderModelRetry())
}

func TestAssistantRecoverableErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		failed, want bool
	}{
		{"safeguards", "API Error: 400 safeguards flagged this message", true, true},
		{"429", "API Error: 429 Too Many Requests", true, true},
		{"subscription 429", "API Error: 429 account_quota_exhausted: Account usage limit reached", true, false},
		{"usage limit text", "API Error: 429 usage_limit_reached", true, false},
		{"rate limit", "API Error: rate_limit_error", true, true},
		{"500", "API Error: 500 internal server error", true, true},
		{"503", "API Error: 503 no eligible upstream", true, true},
		{"529", "API Error: 529 overloaded", true, true},
		{"own 400", "API Error: 400 invalid request", true, false},
		{"400 with upstream detail", "API Error: 400 rejected; upstream 500 was only diagnostic", true, false},
		{"401", "API Error: 401 invalid API key", true, false},
		{"model route", "API Error: 404 model not found", true, false},
		{"context", "Prompt is too long", true, false},
		{"4290 milliseconds is not 429", "API Error: timed out after 4290ms", true, false},
		{"successful result mentioning an error", "API Error: 503", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := assistantRecoverableError(tc.failed, tc.result); got != tc.want {
				t.Fatalf("assistantRecoverableError(%v, %q) = %v, want %v", tc.failed, tc.result, got, tc.want)
			}
		})
	}
}

func TestNextAssistantModelFollowsLauncherOrderOnce(t *testing.T) {
	list := proto.LauncherModels{Models: []proto.AgentModel{
		{ID: "default", Default: true},
		{ID: "primary", Resolved: "resolved-primary"},
		{ID: "fallback", Resolved: "resolved-fallback"},
		{ID: "third"},
	}}
	if got, ok := nextAssistantModel(list, "resolved-primary", map[string]bool{"resolved-primary": true, "primary": true}); !ok || got != "fallback" {
		t.Fatalf("next after a resolved alias = %q %v", got, ok)
	}
	if got, ok := nextAssistantModel(list, "fallback", map[string]bool{"fallback": true}); !ok || got != "third" {
		t.Fatalf("next after fallback = %q %v", got, ok)
	}
	if got, ok := nextAssistantModel(list, "custom", map[string]bool{"custom": true}); !ok || got != "primary" {
		t.Fatalf("a custom current model starts with the first concrete entry = %q %v", got, ok)
	}
	if got, ok := nextAssistantModel(list, "third", map[string]bool{"third": true}); ok || got != "" {
		t.Fatalf("the exhausted list selected %q", got)
	}
	list.Locked = "the launcher fixes its model"
	if got, ok := nextAssistantModel(list, "primary", nil); ok || got != "" {
		t.Fatalf("a locked launcher selected %q", got)
	}
}

func TestAssistantFallbackEffortFollowsTheNextModel(t *testing.T) {
	list := proto.LauncherModels{Models: []proto.AgentModel{{ID: "supports", Efforts: []string{"high", "max"}}, {ID: "limited", Efforts: []string{"low", "high"}}, {ID: "unknown"}}}
	for _, tc := range []struct {
		id, effort, want string
	}{
		{"supports", "max", "max"},
		{"limited", "max", effortAuto},
		{"unknown", "max", effortAuto},
		{"limited", effortAuto, effortAuto},
	} {
		got := assistantExecutionForModel(modelExecution{Effort: tc.effort, UseEffort: true}, list, tc.id)
		if got.Model != tc.id || got.Effort != tc.want || !got.UseEffort {
			t.Errorf("%s at %s: %+v, want effort %s", tc.id, tc.effort, got, tc.want)
		}
	}
}

func TestAssistantRecoveryStopsAfterBothModelsAndTheirRetries(t *testing.T) {
	f := sdkTest(t)
	script := `echo '{"type":"system","subtype":"init"}'
echo '{"type":"system","subtype":"background_tasks_changed","tasks":[]}'
model=primary-model
case "$*" in *'--model fallback-model'*) model=fallback-model ;; esac
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*)
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"persona-models","response":{"models":[{"value":"default"},{"value":"primary-model","resolvedModel":"primary-model"},{"value":"fallback-model","resolvedModel":"fallback-model"}]}}}' ;;
    *'"type":"user"'*)
      printf '%s\n' "$line" >> "$SDK_FAKE_DIR/user.jsonl"
      printf '{"type":"stream_event","event":{"type":"message_start","message":{"model":"%s","usage":{"input_tokens":5}}},"parent_tool_use_id":null}\n' "$model"
      echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: 503 no eligible upstream"}' ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "bin", "claude"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "original request", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	if got := strings.Count(f.file("user.jsonl"), `"type":"user"`); got != 4 {
		t.Fatalf("two models with one same-model retry each made %d requests, want 4", got)
	}
	if p := f.proc(id); p == nil || p.model.Model != "fallback-model" || p.replyModel != "fallback-model" || p.busy {
		t.Fatalf("the exhausted recovery did not retain the final model and error state: %+v", p)
	}
}

func TestAssistantRecoveryCancelReleasesTheHeldTurn(t *testing.T) {
	n := assistantNode(t)
	const id = "11111111-2222-3333-4444-555555555555"
	p := &assistantProc{id: id, busy: true}
	n.procs[id] = p
	n.recoveries = map[string]*assistantTurnRecovery{id: {running: true}}
	n.cancelAssistantRecovery(id)
	p.mu.Lock()
	busy := p.busy
	p.mu.Unlock()
	if busy {
		t.Fatal("a newer message's failed send would leave the cancelled turn working forever")
	}
	if _, ok := n.recoveries[id]; ok {
		t.Fatal("the newer message did not cancel the old recovery")
	}
}

func TestAssistantRecoveryIgnoresAReplacedProcessCleanup(t *testing.T) {
	n := assistantNode(t)
	const id = "11111111-2222-3333-4444-555555555555"
	old, current := &assistantProc{id: id}, &assistantProc{id: id}
	n.procs[id] = current
	n.recoveries = map[string]*assistantTurnRecovery{id: {running: true}}
	n.clearAssistantRecovery(old)
	if n.recoveries[id] == nil {
		t.Fatal("a replaced process cleared its replacement's recovery")
	}
	n.clearAssistantRecovery(current)
	if n.recoveries[id] != nil {
		t.Fatal("the current process did not clear its completed recovery")
	}
}

func TestAssistantInterruptCancelsRecovery(t *testing.T) {
	n := assistantNode(t)
	const id = "11111111-2222-3333-4444-555555555555"
	n.recoveries = map[string]*assistantTurnRecovery{id: {running: true}}
	if err := n.assistantInterrupt(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := n.recoveries[id]; ok {
		t.Fatal("an explicit Stop left a recovery able to restart the conversation")
	}
}

func TestAssistantFallbackNeverKillsUnknownOrRunningBackgroundTasks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		observed bool
		tasks    []string
	}{
		{"unknown inventory", false, nil},
		{"running task", true, []string{"deploy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := assistantNode(t)
			const id = "11111111-2222-3333-4444-555555555555"
			p := &assistantProc{id: id, agent: "claude", launcher: "claude", tasksObserved: tc.observed, tasks: tc.tasks}
			n.procs[id] = p
			r := &assistantTurnRecovery{}
			n.recoveries = map[string]*assistantTurnRecovery{id: r}
			_, err := n.assistantSend(withAssistantRecovery(context.Background(), r, true), id, "", "claude", "retry", nil, "", "")
			if !errors.Is(err, errAssistantRecoveryTasks) {
				t.Fatalf("restart error = %v, want %v", err, errAssistantRecoveryTasks)
			}
			if n.procs[id] != p {
				t.Fatal("the old process was stopped without a proven empty background inventory")
			}
		})
	}
}
