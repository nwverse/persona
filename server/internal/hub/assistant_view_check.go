package hub

import (
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/buswire"
	"github.com/nwverse/persona/server/internal/prompts"
)

func (m *Machine) checkAssistantView(params json.RawMessage) (assistantwire.ViewCheckResponse, error) {
	return handleMachineRequest(assistantwire.ViewCheckMethod, params, m.validateAssistantView)
}

func (m *Machine) validateAssistantView(input assistantwire.ViewCheckRequest) assistantwire.ViewCheckResponse {
	var result assistantwire.ViewCheckResponse
	session, known := m.session(input.SessionID)
	if !known || !m.isAssistant(session) || !wire.HasAssistantView(input.Message) {
		return result
	}
	var problems []string
	if len(input.Message) > 1<<20 {
		problems = []string{"message: at most 1 MB for assistant card validation"}
	} else {
		problems = wire.AssistantBlockErrors(input.Message)
	}
	if len(problems) == 0 {
		return result
	}
	bounded := make([]string, min(len(problems), 4))
	for i := range bounded {
		text := []rune(problems[i])
		bounded[i] = string(text[:min(len(text), 2000)])
	}
	result.Reason = prompts.RenderAssistantViewRepair(prompts.AssistantViewRepair{Errors: bounded})
	return result
}

// handleMachineRequest binds a node request to its handler and keeps decoding lenient during rolling upgrades.
func handleMachineRequest[P, R any](_ buswire.Method[P, R], params json.RawMessage, run func(P) R) (R, error) {
	var input P
	if err := json.Unmarshal(params, &input); err != nil {
		var zero R
		return zero, err
	}
	return run(input), nil
}
