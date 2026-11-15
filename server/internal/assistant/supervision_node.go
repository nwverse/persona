package assistant

import (
	"context"
	"fmt"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// An old resident node ignores new send parameters. Confirm support before sending facts without their policy.
func (s *Service) checkSupervisionNode(ctx context.Context, machine, id string) error {
	var capability struct {
		Instructions bool `json:"supervision_instructions"`
	}
	err := s.call(ctx, machine, assistantwire.DeviceAssistantExecution, map[string]string{"session_id": id}, &capability)
	if err == nil && capability.Instructions {
		return nil
	}
	message := i18n.T(s.lang(ctx), "assistant-todos.supervision_node_upgrade")
	if err != nil {
		return fmt.Errorf("%s: %w", message, err)
	}
	return fmt.Errorf("%s", message)
}
