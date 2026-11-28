package prompts

// SupervisionParts are the role-specific sections a conversation's supervision policy carries beside the common
// policy. The assistant fills them from the conversation's role (internal/assistant/supervisionrole).
type SupervisionParts struct {
	Main          bool
	Leader        bool
	Background    bool
	StartRemedies bool
}

type supervisionInstructionData struct {
	Main          bool
	Leader        bool
	Background    bool
	StartRemedies bool
}

// RenderSupervisionInstructions returns the full policy union for its golden and size budget.
// Conversations use RenderSupervisionInstructionsFor instead.
func RenderSupervisionInstructions() string {
	return render("assistant/supervision_instructions", supervisionInstructionData{Main: true, Leader: true, Background: true, StartRemedies: true})
}

// RenderSupervisionInstructionsFor keeps common supervision policy and only the given role-specific sections.
func RenderSupervisionInstructionsFor(p SupervisionParts) string {
	return render("assistant/supervision_instructions", supervisionInstructionData{
		Main:          p.Main,
		Leader:        p.Leader,
		Background:    p.Background,
		StartRemedies: p.StartRemedies,
	})
}
