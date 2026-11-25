package fleetcount

import (
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Agent is one agent as fleetcount knows it. The zero Agent is no agent; only Build makes one, and every method raises
// a defect on it.
type Agent struct {
	id           AgentID
	state        State
	role         Role
	lineage      Lineage
	muting       Muting
	visibility   Visibility
	cardStanding CardStanding
	telling      Telling
	holding      Holding
	why          Why
	surface      Surface
}

// AgentStateStep is the builder's stage for the state. The zero AgentStateStep is a skipped stage, and Build refuses
// what it leaves out.
type AgentStateStep struct{ agentStateStep Agent }

// AgentRoleStep is the builder's stage for the role. The zero AgentRoleStep is a skipped stage, and Build refuses what
// it leaves out.
type AgentRoleStep struct{ agentRoleStep Agent }

// AgentLineageStep is the builder's stage for the lineage. The zero AgentLineageStep is a skipped stage, and Build
// refuses what it leaves out.
type AgentLineageStep struct{ agentLineageStep Agent }

// AgentMutingStep is the builder's stage for the muting. The zero AgentMutingStep is a skipped stage, and Build refuses
// what it leaves out.
type AgentMutingStep struct{ agentMutingStep Agent }

// AgentVisibilityStep is the builder's stage for the visibility. The zero AgentVisibilityStep is a skipped stage, and
// Build refuses what it leaves out.
type AgentVisibilityStep struct{ agentVisibilityStep Agent }

// AgentCardStandingStep is the builder's stage for the card standing. The zero AgentCardStandingStep is a skipped
// stage, and Build refuses what it leaves out.
type AgentCardStandingStep struct{ agentCardStandingStep Agent }

// AgentTellingStep is the builder's stage for the telling. The zero AgentTellingStep is a skipped stage, and Build
// refuses what it leaves out.
type AgentTellingStep struct{ agentTellingStep Agent }

// AgentHoldingStep is the builder's stage for the holding. The zero AgentHoldingStep is a skipped stage, and Build
// refuses what it leaves out.
type AgentHoldingStep struct{ agentHoldingStep Agent }

// AgentWhyStep is the builder's stage for the why. The zero AgentWhyStep is a skipped stage, and Build refuses what it
// leaves out.
type AgentWhyStep struct{ agentWhyStep Agent }

// AgentSurfaceStep is the builder's stage for the surface. The zero AgentSurfaceStep is a skipped stage, and Build
// refuses what it leaves out.
type AgentSurfaceStep struct{ agentSurfaceStep Agent }

// AgentBuilder is the builder's last stage. The zero AgentBuilder is a skipped stage, and Build refuses what it leaves
// out.
type AgentBuilder struct{ agentBuilder Agent }

// NewAgentBuilder starts an agent: its identity, then one stage per part.
func NewAgentBuilder(id AgentID) AgentStateStep {
	return AgentStateStep{agentStateStep: Agent{id: id}}
}

// State is the agent's state.
func (stage AgentStateStep) State(state State) AgentRoleStep {
	a := stage.agentStateStep
	a.state = state
	return AgentRoleStep{agentRoleStep: a}
}

// Role is the agent's role.
func (stage AgentRoleStep) Role(role Role) AgentLineageStep {
	a := stage.agentRoleStep
	a.role = role
	return AgentLineageStep{agentLineageStep: a}
}

// Lineage is the agent's lineage.
func (stage AgentLineageStep) Lineage(lineage Lineage) AgentMutingStep {
	a := stage.agentLineageStep
	a.lineage = lineage
	return AgentMutingStep{agentMutingStep: a}
}

// Muting is the agent's muting.
func (stage AgentMutingStep) Muting(muting Muting) AgentVisibilityStep {
	a := stage.agentMutingStep
	a.muting = muting
	return AgentVisibilityStep{agentVisibilityStep: a}
}

// Visibility is the agent's visibility.
func (stage AgentVisibilityStep) Visibility(visibility Visibility) AgentCardStandingStep {
	a := stage.agentVisibilityStep
	a.visibility = visibility
	return AgentCardStandingStep{agentCardStandingStep: a}
}

// CardStanding is the agent's card standing.
func (stage AgentCardStandingStep) CardStanding(cardStanding CardStanding) AgentTellingStep {
	a := stage.agentCardStandingStep
	a.cardStanding = cardStanding
	return AgentTellingStep{agentTellingStep: a}
}

// Telling is the agent's telling.
func (stage AgentTellingStep) Telling(telling Telling) AgentHoldingStep {
	a := stage.agentTellingStep
	a.telling = telling
	return AgentHoldingStep{agentHoldingStep: a}
}

// Holding is the agent's holding.
func (stage AgentHoldingStep) Holding(holding Holding) AgentWhyStep {
	a := stage.agentHoldingStep
	a.holding = holding
	return AgentWhyStep{agentWhyStep: a}
}

// Why is the agent's why.
func (stage AgentWhyStep) Why(why Why) AgentSurfaceStep {
	a := stage.agentWhyStep
	a.why = why
	return AgentSurfaceStep{agentSurfaceStep: a}
}

// Surface is the agent's surface.
func (stage AgentSurfaceStep) Surface(surface Surface) AgentBuilder {
	a := stage.agentSurfaceStep
	a.surface = surface
	return AgentBuilder{agentBuilder: a}
}

// Build is the agent, or every part a skipped stage or a value that is none left out, in order.
func (b AgentBuilder) Build() (out outcome.Of[Agent, Fail]) {
	a := b.agentBuilder
	if parts := a.wrongParts(); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(a)
}

// RestoreAgent is a stored agent, from the parts b holds: only adapters call it, and it refuses what Build refuses.
func RestoreAgent(b AgentBuilder) (out outcome.Of[Agent, Fail]) {
	a := b.agentBuilder
	if parts := a.wrongParts(); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(a)
}

// wrongParts is every part of a a rule refuses, in order.
func (a Agent) wrongParts() []fail.Invalid {
	var noAgentID AgentID
	var noState State
	var noRole Role
	var noLineage Lineage
	var noMuting Muting
	var noVisibility Visibility
	var noCardStanding CardStanding
	var noTelling Telling
	var noHolding Holding
	var noWhy Why
	var noSurface Surface
	var parts []fail.Invalid
	parts = wrong(parts, a.id != noAgentID, "fleetcount.agent.no_id", "id")
	parts = wrong(parts, a.state != noState, "fleetcount.agent.no_state", "state")
	parts = wrong(parts, a.role != noRole, "fleetcount.agent.no_role", "role")
	parts = wrong(parts, a.lineage != noLineage, "fleetcount.agent.no_lineage", "lineage")
	parts = wrong(parts, a.muting != noMuting, "fleetcount.agent.no_muting", "muting")
	parts = wrong(parts, a.visibility != noVisibility, "fleetcount.agent.no_visibility", "visibility")
	parts = wrong(parts, a.cardStanding != noCardStanding, "fleetcount.agent.no_card_standing", "cardStanding")
	parts = wrong(parts, a.telling != noTelling, "fleetcount.agent.no_telling", "telling")
	parts = wrong(parts, a.holding != noHolding, "fleetcount.agent.no_holding", "holding")
	parts = wrong(parts, a.why != noWhy, "fleetcount.agent.no_why", "why")
	parts = wrong(parts, a.surface != noSurface, "fleetcount.agent.no_surface", "surface")
	return parts
}

// known raises a defect when a is the zero Agent, which no function takes; op names the function it was handed to.
// Every constructor refuses a zero identity, so the identity tells.
func (a Agent) known(op string) {
	if a.id == (AgentID{}) {
		defect.Raise(op, "the zero Agent")
	}
}

// ID is the agent's identity, which stores and other agents refer to it by.
func (a Agent) ID() AgentID {
	a.known("fleetcount.Agent.ID")
	return a.id
}

// IsSame says whether a and other are the same agent, whatever their parts.
func (a Agent) IsSame(other Agent) bool {
	a.known("fleetcount.Agent.IsSame")
	other.known("fleetcount.Agent.IsSame")
	return a.id == other.id
}

// AgentVisitor is told the agent's identity and parts.
type AgentVisitor interface {
	VisitAgentID(id AgentID)
	VisitState(state State)
	VisitRole(role Role)
	VisitLineage(lineage Lineage)
	VisitMuting(muting Muting)
	VisitVisibility(visibility Visibility)
	VisitCardStanding(cardStanding CardStanding)
	VisitTelling(telling Telling)
	VisitHolding(holding Holding)
	VisitWhy(why Why)
	VisitSurface(surface Surface)
}

// Accept tells v the agent's identity and parts.
func (a Agent) Accept(v AgentVisitor) {
	a.known("fleetcount.Agent.Accept")
	v.VisitAgentID(a.id)
	v.VisitState(a.state)
	v.VisitRole(a.role)
	v.VisitLineage(a.lineage)
	v.VisitMuting(a.muting)
	v.VisitVisibility(a.visibility)
	v.VisitCardStanding(a.cardStanding)
	v.VisitTelling(a.telling)
	v.VisitHolding(a.holding)
	v.VisitWhy(a.why)
	v.VisitSurface(a.surface)
}
