package fleetcount_test

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount/fleetcounttest"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
)

// minute is the test's clock: minute m of a day.
func minute(m int) instant.Instant {
	return instant.At(time.Date(2026, 11, 22, 9, m, 0, 0, time.UTC))
}

// ref is a session on the test's computer.
func ref(t *testing.T, session string) agentref.Ref {
	t.Helper()
	return domaintest.Ok(t, agentref.ParseRef("mac", session))
}

// agent is a worker the owner hears from, listed on a row of its own, at place i, in state since minute m.
type agent struct {
	session string
	i, m    int
	state   fleetcount.State
	role    fleetcount.Role
	lineage fleetcount.Lineage
	muting  fleetcount.Muting
	shown   fleetcount.Visibility
	under   string
}

func waiting(session string, i, m int) agent {
	return agent{session: session, i: i, m: m, state: fleetcount.StateNeedsYou()}
}

func (a agent) build(t *testing.T) fleetcount.Agent {
	t.Helper()
	var (
		noRole    fleetcount.Role
		noLineage fleetcount.Lineage
		noMuting  fleetcount.Muting
		noShown   fleetcount.Visibility
	)
	role, lineage, muting, shown := a.role, a.lineage, a.muting, a.shown
	if role == noRole {
		role = fleetcount.RoleWorker()
	}
	if lineage == noLineage {
		lineage = fleetcount.LineageRoot()
	}
	if muting == noMuting {
		muting = fleetcount.MutingHeard()
	}
	if shown == noShown {
		shown = fleetcount.VisibilityListed()
	}
	placement := fleetcount.PlacementOwnRow()
	if a.under != "" {
		placement = fleetcount.PlacementUnder(ref(t, a.under))
	}
	return domaintest.Ok(t, fleetcount.NewAgentBuilder(ref(t, a.session), fleetcounttest.Slot(a.i)).
		State(a.state, opt.Some(minute(a.m)), minute(a.m)).
		Role(role, lineage, muting).
		Place(shown, placement).
		Build())
}

// card is a card the owner was not told of, which stands for its agent and is the owner's, at place i, asked at
// minute m by session ("" for none).
type card struct {
	session  string
	i, m     int
	standing fleetcount.CardStanding
	holding  fleetcount.Holding
	telling  fleetcount.Telling
}

func asked(session string, i, m int) card { return card{session: session, i: i, m: m} }

func (c card) build(t *testing.T) fleetcount.Card {
	t.Helper()
	var (
		noStanding fleetcount.CardStanding
		noHolding  fleetcount.Holding
		noTelling  fleetcount.Telling
	)
	standing, holding, telling := c.standing, c.holding, c.telling
	if standing == noStanding {
		standing = fleetcount.CardStandingStands()
	}
	if holding == noHolding {
		holding = fleetcount.HoldingForTheOwner()
	}
	if telling == noTelling {
		telling = fleetcount.TellingUntold()
	}
	by := opt.None[agentref.Ref]()
	if c.session != "" {
		by = opt.Some(ref(t, c.session))
	}
	id := domaintest.Ok(t, agentref.ParseInteractionID("ix-"+c.session+"-"+string(rune('a'+c.i))))
	return domaintest.Ok(t, fleetcount.NewCardBuilder(id, fleetcounttest.Slot(c.i)).
		Asker(by).
		Standing(standing, holding, telling, minute(c.m)).
		Build())
}

func tally(t *testing.T, on fleetcount.Surface, agents []agent, cards []card) string {
	t.Helper()
	var as []fleetcount.Agent
	for _, a := range agents {
		as = append(as, a.build(t))
	}
	var cs []fleetcount.Card
	for _, c := range cards {
		cs = append(cs, c.build(t))
	}
	return fleetcounttest.Tally(on, as, cs)
}

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("rows\n%s\nwant\n%s", got, want)
	}
}

// raises fails the test unless do raises the defect want ("<op>: <cause>").
func raises(t *testing.T, want string, do func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if d, ok := recover().(defect.Defect); !ok || d.Error() != want {
			t.Errorf("raised %v, want the defect %q", d, want)
		}
	}()
	do()
}

func TestBuildListsEveryPartASkippedStageLeftOut(t *testing.T) {
	var skipped fleetcount.AgentPlaceStep // a skipped stage still compiles; Build refuses it
	got := domaintest.Fails(t, skipped.Place(fleetcount.VisibilityListed(), fleetcount.PlacementOwnRow()).Build())
	const want = "invalid fleetcount.agent.no_session agent; invalid fleetcount.agent.no_state state; " +
		"invalid fleetcount.agent.no_role role; invalid fleetcount.agent.no_lineage lineage; " +
		"invalid fleetcount.agent.no_muting muting"
	if got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
}

func TestBuildRefusesEveryZeroPart(t *testing.T) {
	var none fleetcount.Visibility
	var nowhere fleetcount.Placement
	got := domaintest.Fails(t, fleetcount.NewAgentBuilder(ref(t, "s1"), fleetcounttest.Slot(0)).
		State(fleetcount.StateIdle(), opt.None[instant.Instant](), minute(0)).
		Role(fleetcount.RoleWorker(), fleetcount.LineageRoot(), fleetcount.MutingHeard()).
		Place(none, nowhere).Build())
	if want := "invalid fleetcount.agent.no_visibility visibility; invalid fleetcount.agent.no_placement placement"; got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
	var skipped fleetcount.CardStandingStep
	got = domaintest.Fails(t, skipped.Standing(fleetcount.CardStandingStands(), fleetcount.HoldingForTheOwner(),
		fleetcount.TellingTold(), minute(0)).Build())
	if want := "invalid fleetcount.card.no_id card"; got != want {
		t.Errorf("card Build = %q, want %q", got, want)
	}
}

// An agent the hub sent with no time at all still waits: the kit's zero Instant is unknown, and Build takes it.
func TestAnUnknownTimeIsNoReasonToDropAnAgent(t *testing.T) {
	var unknown instant.Instant
	a := domaintest.Ok(t, fleetcount.NewAgentBuilder(ref(t, "s1"), fleetcounttest.Slot(0)).
		State(fleetcount.StateNeedsYou(), opt.None[instant.Instant](), unknown).
		Role(fleetcount.RoleWorker(), fleetcount.LineageRoot(), fleetcount.MutingHeard()).
		Place(fleetcount.VisibilityListed(), fleetcount.PlacementOwnRow()).Build())
	expect(t, fleetcounttest.Tally(fleetcount.SurfaceLiveActivity(), []fleetcount.Agent{a}, nil), "agent 0 needs_you since unknown\ntotal 1")
}

func TestTheZeroValuesAreDefects(t *testing.T) {
	var noSurface fleetcount.Surface
	raises(t, "fleetcount.AgentsNeedsYou: the zero Surface", func() {
		fleetcount.AgentsNeedsYou(noSurface, fleetcount.NewAgents(nil), fleetcount.NewCards(nil))
	})
	var noAgent fleetcount.Agent
	raises(t, "fleetcount.NewAgents: the zero Agent", func() { fleetcount.NewAgents([]fleetcount.Agent{noAgent}) })
	var noCard fleetcount.Card
	raises(t, "fleetcount.NewCards: the zero Card", func() { fleetcount.NewCards([]fleetcount.Card{noCard}) })
	var noWhy fleetcount.Why
	raises(t, "fleetcount.Why.Counted: the zero Why", func() { noWhy.Counted() })
	var noVisibility fleetcount.Visibility
	raises(t, "fleetcount.Visibility.LeftOut: the zero Visibility", func() { noVisibility.LeftOut() })
	var noRef agentref.Ref
	raises(t, "fleetcount.PlacementUnder: the zero Ref", func() { fleetcount.PlacementUnder(noRef) })
}

// NewAgents and NewCards copy what they are handed: the caller may reuse its slices.
func TestTheCoreOwnsItsArrays(t *testing.T) {
	list := []fleetcount.Agent{waiting("s1", 0, 1).build(t)}
	cards := []fleetcount.Card{asked("s1", 0, 2).build(t)}
	agents, held := fleetcount.NewAgents(list), fleetcount.NewCards(cards)
	list[0], cards[0] = waiting("s2", 1, 3).build(t), asked("", 1, 4).build(t)
	var rows fleetcounttest.Rows
	fleetcount.AgentsNeedsYou(fleetcount.SurfaceBadge(), agents, held).Accept(&rows)
	expect(t, rows.String(), "agent 0 by card 0 needs_you\ntotal 1")
}

func TestVisibilityLeavesQuietAndHiddenAgentsOutOfTheFleet(t *testing.T) {
	for _, c := range []struct {
		v    fleetcount.Visibility
		left bool
	}{
		{fleetcount.VisibilityListed(), false},
		{fleetcount.VisibilityQuiet(), true},
		{fleetcount.VisibilityHiddenByDirectory(), true},
	} {
		if c.v.LeftOut() != c.left {
			t.Errorf("LeftOut of %v is not %v", c.v, c.left)
		}
	}
}

func TestOnlyWaitingRowsCount(t *testing.T) {
	for _, c := range []struct {
		why     fleetcount.Why
		counted bool
	}{
		{fleetcount.WhyNeedsYou(), true},
		{fleetcount.WhyHeld(), false},
		{fleetcount.WhyMuted(), false},
		{fleetcount.WhyQuiet(), false},
	} {
		if c.why.Counted() != c.counted {
			t.Errorf("Counted of %v is not %v", c.why, c.counted)
		}
	}
}

// The Live Activity lists an agent by its first card that stands, then the agents waiting in a terminal dialog; it
// counts the assistant only when it asks something, and leaves out muted agents, subtasks and a form.
func TestTheLiveActivityKeepsItsOwnRules(t *testing.T) {
	agents := []agent{
		waiting("dialog", 0, 1),
		{session: "worker", i: 1, m: 2, state: fleetcount.StateWorking()},
		{session: "persona", i: 2, m: 3, state: fleetcount.StateNeedsYou(), role: fleetcount.RoleAssistant()},
		{session: "muted", i: 3, m: 4, state: fleetcount.StateNeedsYou(), muting: fleetcount.MutingMuted()},
		{session: "sub", i: 4, m: 5, state: fleetcount.StateNeedsYou(), lineage: fleetcount.LineageSubtask()},
		{session: "form", i: 5, m: 6, state: fleetcount.StateWorking()},
	}
	cards := []card{
		asked("worker", 0, 1),
		{session: "worker", i: 1, m: 2},
		asked("persona", 2, 3),
		asked("muted", 3, 4),
		{session: "form", i: 4, m: 5, standing: fleetcount.CardStandingForm()},
		asked("gone", 5, 6), // its agent is not in the list
		asked("", 6, 7),     // no session: not the Live Activity's
	}
	expect(t, tally(t, fleetcount.SurfaceLiveActivity(), agents, cards),
		"agent 1 by card 0 needs_you\nagent 2 by card 2 needs_you\nagent 0 needs_you since 09:01\ntotal 3")
}

// A card its task's supervisor holds is not the owner's: an agent with only such cards is in no row, though its state
// says it waits; beside one of the owner's, the agent is a row.
func TestASupervisorsCardsAreNotTheOwners(t *testing.T) {
	agents := []agent{waiting("held", 0, 1), waiting("both", 1, 2)}
	cards := []card{
		{session: "held", i: 0, m: 1, holding: fleetcount.HoldingHeldBySupervisor()},
		{session: "both", i: 1, m: 2, holding: fleetcount.HoldingHeldBySupervisor()},
		asked("both", 2, 3),
	}
	expect(t, tally(t, fleetcount.SurfaceLiveActivity(), agents, cards), "agent 1 by card 1 needs_you\ntotal 1")
	expect(t, tally(t, fleetcount.SurfaceBadge(), agents, cards), "agent 1 by card 2 needs_you\ntotal 1")
}

// Persona Link counts every agent whose row says it waits, muted ones included and quiet ones not; its card is the
// first one handed to the owner, and a row none of whose cards was handed is held.
func TestPersonaLinkCountsAsTheWebBadge(t *testing.T) {
	agents := []agent{
		waiting("handed", 0, 1),
		waiting("undecided", 1, 2),
		{session: "muted", i: 2, m: 3, state: fleetcount.StateNeedsYou(), muting: fleetcount.MutingMuted()},
		{session: "quiet", i: 3, m: 4, state: fleetcount.StateNeedsYou(), shown: fleetcount.VisibilityQuiet()},
		{session: "working", i: 4, m: 5, state: fleetcount.StateWorking()},
	}
	cards := []card{
		{session: "handed", i: 0, m: 1, holding: fleetcount.HoldingHeldByPersona(), telling: fleetcount.TellingTold()},
		{session: "handed", i: 1, m: 2, telling: fleetcount.TellingTold()},
		asked("undecided", 2, 3),
		asked("working", 3, 4),
	}
	expect(t, tally(t, fleetcount.SurfaceLink(), agents, cards),
		"agent 0 by card 1 needs_you\nagent 1 by card 2 held\nagent 2 muted since 09:03\ntotal 3")
}

// The Agents list folds a child's and a side's cards under the row the hub folds them under, lists a card of no
// session as a row of its own, goes oldest first, and counts only the rows that wait for the owner.
func TestTheAgentsListFoldsAndOrders(t *testing.T) {
	agents := []agent{
		{session: "lead", i: 0, m: 1, state: fleetcount.StateWorking()},
		{session: "child", i: 1, m: 5, state: fleetcount.StateNeedsYou(), under: "lead"},
		waiting("asking", 2, 2),
		{session: "muted", i: 3, m: 3, state: fleetcount.StateNeedsYou(), muting: fleetcount.MutingMuted()},
		{session: "persona", i: 4, m: 0, state: fleetcount.StateNeedsYou(), role: fleetcount.RoleAssistant()},
		{session: "hidden", i: 5, m: 0, state: fleetcount.StateNeedsYou(), shown: fleetcount.VisibilityHiddenByDirectory()},
	}
	cards := []card{
		asked("child", 0, 6),
		{session: "child", i: 1, m: 7, holding: fleetcount.HoldingHeldByPersona()},
		asked("", 2, 4),
		{session: "asking", i: 3, m: 2, holding: fleetcount.HoldingHeldByPersona()},
		asked("hidden", 4, 0),
		asked("persona", 5, 0),
	}
	expect(t, tally(t, fleetcount.SurfaceBadge(), agents, cards),
		"agent 2 by card 3 held\nagent 3 muted since 09:03\ncard 2 needs_you\nagent 0 by card 0 needs_you\nunder 1\ntotal 2")
}
