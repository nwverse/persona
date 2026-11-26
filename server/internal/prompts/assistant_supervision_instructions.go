package prompts

import "fmt"

// SupervisionRole is a conversation's manager-authorized supervision role.
type SupervisionRole string

const (
	SupervisionRoleMain       SupervisionRole = "main"
	SupervisionRoleLeader     SupervisionRole = "leader"
	SupervisionRoleBackground SupervisionRole = "background"
	SupervisionRoleSide       SupervisionRole = "side"
)

type supervisionInstructionData struct {
	Leader     bool
	Background bool
}

// RenderSupervisionInstructions returns the full policy union for its golden and size budget.
// Conversations use RenderSupervisionInstructionsForRole instead.
func RenderSupervisionInstructions() string {
	return render("assistant/supervision_instructions", supervisionInstructionData{Leader: true, Background: true})
}

// RenderSupervisionInstructionsForRole keeps common supervision policy and only the named role's private paragraphs.
// Unknown roles fail closed rather than silently receiving main's or the full union's authority.
func RenderSupervisionInstructionsForRole(role SupervisionRole) (string, error) {
	d := supervisionInstructionData{}
	switch role {
	case SupervisionRoleMain, SupervisionRoleSide:
	case SupervisionRoleLeader:
		d.Leader = true
	case SupervisionRoleBackground:
		d.Background = true
	default:
		return "", fmt.Errorf("unknown supervision role %q", role)
	}
	return render("assistant/supervision_instructions", d), nil
}
