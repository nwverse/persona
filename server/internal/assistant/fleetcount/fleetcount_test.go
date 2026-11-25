package fleetcount_test

import (
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
)

// someAgentID is a valid agent id.
func someAgentID(t *testing.T) fleetcount.AgentID {
	t.Helper()
	return domaintest.Ok(t, fleetcount.ParseAgentID("agent 1"))
}

// raises fails the test unless do raises the defect want ("<op>: <cause>").
func raises(t *testing.T, want string, do func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if d, ok := r.(defect.Defect); !ok || d.Error() != want {
			t.Errorf("raised %v, want the defect %q", r, want)
		}
	}()
	do()
}

func TestParseAgentIDNeverMakesTheZeroAgentID(t *testing.T) {
	var zero fleetcount.AgentID
	if someAgentID(t) == zero {
		t.Error("ParseAgentID made the zero AgentID")
	}
}

func TestParseAgentIDRefusesInvalidInput(t *testing.T) {
	got := domaintest.Fails(t, fleetcount.ParseAgentID(" "))
	if want := "invalid fleetcount.agent.empty agent"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheZeroStateIsADefect(t *testing.T) {
	var zero fleetcount.State
	raises(t, "fleetcount.State.Accept: the zero State", func() { zero.Accept(nil) })
}

// stateNames is the test's own visitor: it keeps the name of each state it is told.
type stateNames struct{ names []string }

func (r *stateNames) VisitWorking()  { r.names = append(r.names, "working") }
func (r *stateNames) VisitNeedsYou() { r.names = append(r.names, "needs_you") }
func (r *stateNames) VisitFinished() { r.names = append(r.names, "finished") }
func (r *stateNames) VisitIdle()     { r.names = append(r.names, "idle") }

func TestStateAcceptTellsEachState(t *testing.T) {
	var got stateNames
	for _, x := range []fleetcount.State{fleetcount.StateWorking(), fleetcount.StateNeedsYou(), fleetcount.StateFinished(), fleetcount.StateIdle()} {
		x.Accept(&got)
	}
	if want := []string{"working", "needs_you", "finished", "idle"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroRoleIsADefect(t *testing.T) {
	var zero fleetcount.Role
	raises(t, "fleetcount.Role.Accept: the zero Role", func() { zero.Accept(nil) })
}

// roleNames is the test's own visitor: it keeps the name of each role it is told.
type roleNames struct{ names []string }

func (r *roleNames) VisitWorker()    { r.names = append(r.names, "worker") }
func (r *roleNames) VisitAssistant() { r.names = append(r.names, "assistant") }

func TestRoleAcceptTellsEachRole(t *testing.T) {
	var got roleNames
	for _, x := range []fleetcount.Role{fleetcount.RoleWorker(), fleetcount.RoleAssistant()} {
		x.Accept(&got)
	}
	if want := []string{"worker", "assistant"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroLineageIsADefect(t *testing.T) {
	var zero fleetcount.Lineage
	raises(t, "fleetcount.Lineage.Accept: the zero Lineage", func() { zero.Accept(nil) })
}

// lineageNames is the test's own visitor: it keeps the name of each lineage it is told.
type lineageNames struct{ names []string }

func (r *lineageNames) VisitRoot()    { r.names = append(r.names, "root") }
func (r *lineageNames) VisitSubtask() { r.names = append(r.names, "subtask") }

func TestLineageAcceptTellsEachLineage(t *testing.T) {
	var got lineageNames
	for _, x := range []fleetcount.Lineage{fleetcount.LineageRoot(), fleetcount.LineageSubtask()} {
		x.Accept(&got)
	}
	if want := []string{"root", "subtask"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroMutingIsADefect(t *testing.T) {
	var zero fleetcount.Muting
	raises(t, "fleetcount.Muting.Accept: the zero Muting", func() { zero.Accept(nil) })
}

// mutingNames is the test's own visitor: it keeps the name of each muting it is told.
type mutingNames struct{ names []string }

func (r *mutingNames) VisitHeard() { r.names = append(r.names, "heard") }
func (r *mutingNames) VisitMuted() { r.names = append(r.names, "muted") }

func TestMutingAcceptTellsEachMuting(t *testing.T) {
	var got mutingNames
	for _, x := range []fleetcount.Muting{fleetcount.MutingHeard(), fleetcount.MutingMuted()} {
		x.Accept(&got)
	}
	if want := []string{"heard", "muted"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroVisibilityIsADefect(t *testing.T) {
	var zero fleetcount.Visibility
	raises(t, "fleetcount.Visibility.Accept: the zero Visibility", func() { zero.Accept(nil) })
}

// visibilityNames is the test's own visitor: it keeps the name of each visibility it is told.
type visibilityNames struct{ names []string }

func (r *visibilityNames) VisitListed()            { r.names = append(r.names, "listed") }
func (r *visibilityNames) VisitQuiet()             { r.names = append(r.names, "quiet") }
func (r *visibilityNames) VisitHiddenByDirectory() { r.names = append(r.names, "hidden_by_directory") }

func TestVisibilityAcceptTellsEachVisibility(t *testing.T) {
	var got visibilityNames
	for _, x := range []fleetcount.Visibility{fleetcount.VisibilityListed(), fleetcount.VisibilityQuiet(), fleetcount.VisibilityHiddenByDirectory()} {
		x.Accept(&got)
	}
	if want := []string{"listed", "quiet", "hidden_by_directory"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroCardStandingIsADefect(t *testing.T) {
	var zero fleetcount.CardStanding
	raises(t, "fleetcount.CardStanding.Accept: the zero CardStanding", func() { zero.Accept(nil) })
}

// cardStandingNames is the test's own visitor: it keeps the name of each card standing it is told.
type cardStandingNames struct{ names []string }

func (r *cardStandingNames) VisitStands() { r.names = append(r.names, "stands") }
func (r *cardStandingNames) VisitForm()   { r.names = append(r.names, "form") }

func TestCardStandingAcceptTellsEachCardStanding(t *testing.T) {
	var got cardStandingNames
	for _, x := range []fleetcount.CardStanding{fleetcount.CardStandingStands(), fleetcount.CardStandingForm()} {
		x.Accept(&got)
	}
	if want := []string{"stands", "form"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroTellingIsADefect(t *testing.T) {
	var zero fleetcount.Telling
	raises(t, "fleetcount.Telling.Accept: the zero Telling", func() { zero.Accept(nil) })
}

// tellingNames is the test's own visitor: it keeps the name of each telling it is told.
type tellingNames struct{ names []string }

func (r *tellingNames) VisitTold()   { r.names = append(r.names, "told") }
func (r *tellingNames) VisitUntold() { r.names = append(r.names, "untold") }

func TestTellingAcceptTellsEachTelling(t *testing.T) {
	var got tellingNames
	for _, x := range []fleetcount.Telling{fleetcount.TellingTold(), fleetcount.TellingUntold()} {
		x.Accept(&got)
	}
	if want := []string{"told", "untold"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroHoldingIsADefect(t *testing.T) {
	var zero fleetcount.Holding
	raises(t, "fleetcount.Holding.Accept: the zero Holding", func() { zero.Accept(nil) })
}

// holdingNames is the test's own visitor: it keeps the name of each holding it is told.
type holdingNames struct{ names []string }

func (r *holdingNames) VisitHeldByPersona()    { r.names = append(r.names, "held_by_persona") }
func (r *holdingNames) VisitHeldBySupervisor() { r.names = append(r.names, "held_by_supervisor") }
func (r *holdingNames) VisitForTheOwner()      { r.names = append(r.names, "for_the_owner") }

func TestHoldingAcceptTellsEachHolding(t *testing.T) {
	var got holdingNames
	for _, x := range []fleetcount.Holding{fleetcount.HoldingHeldByPersona(), fleetcount.HoldingHeldBySupervisor(), fleetcount.HoldingForTheOwner()} {
		x.Accept(&got)
	}
	if want := []string{"held_by_persona", "held_by_supervisor", "for_the_owner"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroWhyIsADefect(t *testing.T) {
	var zero fleetcount.Why
	raises(t, "fleetcount.Why.Accept: the zero Why", func() { zero.Accept(nil) })
}

// whyNames is the test's own visitor: it keeps the name of each why it is told.
type whyNames struct{ names []string }

func (r *whyNames) VisitNeedsYou() { r.names = append(r.names, "needs_you") }
func (r *whyNames) VisitHeld()     { r.names = append(r.names, "held") }
func (r *whyNames) VisitMuted()    { r.names = append(r.names, "muted") }
func (r *whyNames) VisitQuiet()    { r.names = append(r.names, "quiet") }

func TestWhyAcceptTellsEachWhy(t *testing.T) {
	var got whyNames
	for _, x := range []fleetcount.Why{fleetcount.WhyNeedsYou(), fleetcount.WhyHeld(), fleetcount.WhyMuted(), fleetcount.WhyQuiet()} {
		x.Accept(&got)
	}
	if want := []string{"needs_you", "held", "muted", "quiet"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestTheZeroSurfaceIsADefect(t *testing.T) {
	var zero fleetcount.Surface
	raises(t, "fleetcount.Surface.Accept: the zero Surface", func() { zero.Accept(nil) })
}

// surfaceNames is the test's own visitor: it keeps the name of each surface it is told.
type surfaceNames struct{ names []string }

func (r *surfaceNames) VisitBadge()        { r.names = append(r.names, "badge") }
func (r *surfaceNames) VisitLiveActivity() { r.names = append(r.names, "live_activity") }
func (r *surfaceNames) VisitLink()         { r.names = append(r.names, "link") }

func TestSurfaceAcceptTellsEachSurface(t *testing.T) {
	var got surfaceNames
	for _, x := range []fleetcount.Surface{fleetcount.SurfaceBadge(), fleetcount.SurfaceLiveActivity(), fleetcount.SurfaceLink()} {
		x.Accept(&got)
	}
	if want := []string{"badge", "live_activity", "link"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestPlacementRefusesPayloadsThatAreNoValue(t *testing.T) {
	var noRef agentref.Ref
	raises(t, "fleetcount.PlacementUnder: the zero Ref", func() { fleetcount.PlacementUnder(noRef) })
}

// placementNames is the test's own visitor: it keeps the name of each placement it is told.
type placementNames struct{ names []string }

func (r *placementNames) VisitOwnRow()            { r.names = append(r.names, "own_row") }
func (r *placementNames) VisitUnder(agentref.Ref) { r.names = append(r.names, "under") }

func TestPlacementAcceptTellsEachPlacement(t *testing.T) {
	var got placementNames
	for _, x := range []fleetcount.Placement{fleetcount.PlacementOwnRow(), fleetcount.PlacementUnder(domaintest.Ok(t, agentref.ParseRef("machine 1", "session 1")))} {
		x.Accept(&got)
	}
	if want := []string{"own_row", "under"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestBuildRefusesAgentWithoutID(t *testing.T) {
	var noAgentID fleetcount.AgentID
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(noAgentID).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_id id"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutState(t *testing.T) {
	var noState fleetcount.State
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(noState).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_state state"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutRole(t *testing.T) {
	var noRole fleetcount.Role
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(noRole).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_role role"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutLineage(t *testing.T) {
	var noLineage fleetcount.Lineage
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(noLineage).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_lineage lineage"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutMuting(t *testing.T) {
	var noMuting fleetcount.Muting
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(noMuting).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_muting muting"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutVisibility(t *testing.T) {
	var noVisibility fleetcount.Visibility
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(noVisibility).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_visibility visibility"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutCardStanding(t *testing.T) {
	var noCardStanding fleetcount.CardStanding
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(noCardStanding).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_card_standing cardStanding"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutTelling(t *testing.T) {
	var noTelling fleetcount.Telling
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(noTelling).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_telling telling"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutHolding(t *testing.T) {
	var noHolding fleetcount.Holding
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(noHolding).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_holding holding"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutWhy(t *testing.T) {
	var noWhy fleetcount.Why
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(noWhy).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if want := "invalid fleetcount.agent.no_why why"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesAgentWithoutSurface(t *testing.T) {
	var noSurface fleetcount.Surface
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(noSurface).
		Build())
	if want := "invalid fleetcount.agent.no_surface surface"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildListsEveryPartASkippedStageLeftOut(t *testing.T) {
	var skipped fleetcount.AgentSurfaceStep // a skipped stage still compiles; Build refuses it
	got := domaintest.Fails(t, skipped.Surface(fleetcount.SurfaceBadge()).Build())
	if want := "invalid fleetcount.agent.no_id id; " +
		"invalid fleetcount.agent.no_state state; " +
		"invalid fleetcount.agent.no_role role; " +
		"invalid fleetcount.agent.no_lineage lineage; " +
		"invalid fleetcount.agent.no_muting muting; " +
		"invalid fleetcount.agent.no_visibility visibility; " +
		"invalid fleetcount.agent.no_card_standing cardStanding; " +
		"invalid fleetcount.agent.no_telling telling; " +
		"invalid fleetcount.agent.no_holding holding; " +
		"invalid fleetcount.agent.no_why why"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheZeroAgentIsADefect(t *testing.T) {
	var zero fleetcount.Agent
	some := domaintest.Ok(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	raises(t, "fleetcount.Agent.ID: the zero Agent", func() { zero.ID() })
	raises(t, "fleetcount.Agent.IsSame: the zero Agent", func() { zero.IsSame(some) })
	raises(t, "fleetcount.Agent.IsSame: the zero Agent", func() { some.IsSame(zero) })
	raises(t, "fleetcount.Agent.Accept: the zero Agent", func() { zero.Accept(nil) })
}

func TestTwoAgentsOfOneIdentityAreTheSame(t *testing.T) {
	a := domaintest.Ok(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())
	if !a.IsSame(domaintest.Ok(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build())) {
		t.Error("two agents of one identity are not the same")
	}
}

// agentParts is the test's own visitor: it keeps the identity it is told.
type agentParts struct {
	id fleetcount.AgentID
}

func (r *agentParts) VisitAgentID(id fleetcount.AgentID)        { r.id = id }
func (r *agentParts) VisitState(fleetcount.State)               {}
func (r *agentParts) VisitRole(fleetcount.Role)                 {}
func (r *agentParts) VisitLineage(fleetcount.Lineage)           {}
func (r *agentParts) VisitMuting(fleetcount.Muting)             {}
func (r *agentParts) VisitVisibility(fleetcount.Visibility)     {}
func (r *agentParts) VisitCardStanding(fleetcount.CardStanding) {}
func (r *agentParts) VisitTelling(fleetcount.Telling)           {}
func (r *agentParts) VisitHolding(fleetcount.Holding)           {}
func (r *agentParts) VisitWhy(fleetcount.Why)                   {}
func (r *agentParts) VisitSurface(fleetcount.Surface)           {}

func TestAgentAcceptTellsItsIdentity(t *testing.T) {
	var got agentParts
	domaintest.Ok(t, fleetcount.NewAgentBuilder(someAgentID(t)).
		State(fleetcount.StateWorking()).
		Role(fleetcount.RoleWorker()).
		Lineage(fleetcount.LineageRoot()).
		Muting(fleetcount.MutingHeard()).
		Visibility(fleetcount.VisibilityListed()).
		CardStanding(fleetcount.CardStandingStands()).
		Telling(fleetcount.TellingTold()).
		Holding(fleetcount.HoldingHeldByPersona()).
		Why(fleetcount.WhyNeedsYou()).
		Surface(fleetcount.SurfaceBadge()).
		Build()).Accept(&got)
	if got.id != someAgentID(t) {
		t.Error("Accept told another identity")
	}
}
