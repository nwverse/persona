package fleetcountadapt

import (
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Record is the stored agent: plain and tagged, owned by this adapter.
type Record struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	Role         string `json:"role"`
	Lineage      string `json:"lineage"`
	Muting       string `json:"muting"`
	Visibility   string `json:"visibility"`
	CardStanding string `json:"card_standing"`
	Telling      string `json:"telling"`
	Holding      string `json:"holding"`
	Why          string `json:"why"`
	Surface      string `json:"surface"`
}

// Restore is the stored agent r holds: a part that does not parse fails with fleetcount's failure, and a stored word
// this adapter does not know goes in as the zero value, which fleetcount refuses.
func Restore(r Record) (out outcome.Of[fleetcount.Agent, fail.Fail]) {
	id, f := outcome.Unpack(fleetcount.ParseAgentID(r.ID))
	if f != nil {
		return out.Err(f)
	}
	return outcome.Widen(fleetcount.RestoreAgent(fleetcount.NewAgentBuilder(id).
		State(storedState(r.State)).
		Role(storedRole(r.Role)).
		Lineage(storedLineage(r.Lineage)).
		Muting(storedMuting(r.Muting)).
		Visibility(storedVisibility(r.Visibility)).
		CardStanding(storedCardStanding(r.CardStanding)).
		Telling(storedTelling(r.Telling)).
		Holding(storedHolding(r.Holding)).
		Why(storedWhy(r.Why)).
		Surface(storedSurface(r.Surface))))
}

// Store is x as stored.
func Store(x fleetcount.Agent) Record {
	var r recorder
	x.Accept(&r)
	return r.record
}

// recorder fills a Record from an agent. The visitors it reads closed sets with are fields, so the nested Accept calls
// do not allocate.
type recorder struct {
	record       Record
	state        stateWord
	role         roleWord
	lineage      lineageWord
	muting       mutingWord
	visibility   visibilityWord
	cardStanding cardStandingWord
	telling      tellingWord
	holding      holdingWord
	why          whyWord
	surface      surfaceWord
}

func (r *recorder) VisitAgentID(id fleetcount.AgentID) { r.record.ID = id.Wire() }

func (r *recorder) VisitState(x fleetcount.State) {
	x.Accept(&r.state)
	r.record.State = r.state.word
}

func (r *recorder) VisitRole(x fleetcount.Role) {
	x.Accept(&r.role)
	r.record.Role = r.role.word
}

func (r *recorder) VisitLineage(x fleetcount.Lineage) {
	x.Accept(&r.lineage)
	r.record.Lineage = r.lineage.word
}

func (r *recorder) VisitMuting(x fleetcount.Muting) {
	x.Accept(&r.muting)
	r.record.Muting = r.muting.word
}

func (r *recorder) VisitVisibility(x fleetcount.Visibility) {
	x.Accept(&r.visibility)
	r.record.Visibility = r.visibility.word
}

func (r *recorder) VisitCardStanding(x fleetcount.CardStanding) {
	x.Accept(&r.cardStanding)
	r.record.CardStanding = r.cardStanding.word
}

func (r *recorder) VisitTelling(x fleetcount.Telling) {
	x.Accept(&r.telling)
	r.record.Telling = r.telling.word
}

func (r *recorder) VisitHolding(x fleetcount.Holding) {
	x.Accept(&r.holding)
	r.record.Holding = r.holding.word
}

func (r *recorder) VisitWhy(x fleetcount.Why) {
	x.Accept(&r.why)
	r.record.Why = r.why.word
}

func (r *recorder) VisitSurface(x fleetcount.Surface) {
	x.Accept(&r.surface)
	r.record.Surface = r.surface.word
}

// Log is x for a log line, and only for one: its identity.
func Log(x fleetcount.Agent) string { return x.ID().Wire() }

// storedState is a stored state as fleetcount knows it; a word this adapter does not know is the zero State, which
// fleetcount refuses.
func storedState(word string) fleetcount.State {
	switch word {
	case "working":
		return fleetcount.StateWorking()
	case "needs_you":
		return fleetcount.StateNeedsYou()
	case "finished":
		return fleetcount.StateFinished()
	case "idle":
		return fleetcount.StateIdle()
	}
	var unknown fleetcount.State
	return unknown
}

// stateWord is a state as the store spells it.
type stateWord struct{ word string }

func (x *stateWord) VisitWorking()  { x.word = "working" }
func (x *stateWord) VisitNeedsYou() { x.word = "needs_you" }
func (x *stateWord) VisitFinished() { x.word = "finished" }
func (x *stateWord) VisitIdle()     { x.word = "idle" }

// storedRole is a stored role as fleetcount knows it; a word this adapter does not know is the zero Role, which
// fleetcount refuses.
func storedRole(word string) fleetcount.Role {
	switch word {
	case "worker":
		return fleetcount.RoleWorker()
	case "assistant":
		return fleetcount.RoleAssistant()
	}
	var unknown fleetcount.Role
	return unknown
}

// roleWord is a role as the store spells it.
type roleWord struct{ word string }

func (x *roleWord) VisitWorker()    { x.word = "worker" }
func (x *roleWord) VisitAssistant() { x.word = "assistant" }

// storedLineage is a stored lineage as fleetcount knows it; a word this adapter does not know is the zero Lineage,
// which fleetcount refuses.
func storedLineage(word string) fleetcount.Lineage {
	switch word {
	case "root":
		return fleetcount.LineageRoot()
	case "subtask":
		return fleetcount.LineageSubtask()
	}
	var unknown fleetcount.Lineage
	return unknown
}

// lineageWord is a lineage as the store spells it.
type lineageWord struct{ word string }

func (x *lineageWord) VisitRoot()    { x.word = "root" }
func (x *lineageWord) VisitSubtask() { x.word = "subtask" }

// storedMuting is a stored muting as fleetcount knows it; a word this adapter does not know is the zero Muting, which
// fleetcount refuses.
func storedMuting(word string) fleetcount.Muting {
	switch word {
	case "heard":
		return fleetcount.MutingHeard()
	case "muted":
		return fleetcount.MutingMuted()
	}
	var unknown fleetcount.Muting
	return unknown
}

// mutingWord is a muting as the store spells it.
type mutingWord struct{ word string }

func (x *mutingWord) VisitHeard() { x.word = "heard" }
func (x *mutingWord) VisitMuted() { x.word = "muted" }

// storedVisibility is a stored visibility as fleetcount knows it; a word this adapter does not know is the zero
// Visibility, which fleetcount refuses.
func storedVisibility(word string) fleetcount.Visibility {
	switch word {
	case "listed":
		return fleetcount.VisibilityListed()
	case "quiet":
		return fleetcount.VisibilityQuiet()
	case "hidden_by_directory":
		return fleetcount.VisibilityHiddenByDirectory()
	}
	var unknown fleetcount.Visibility
	return unknown
}

// visibilityWord is a visibility as the store spells it.
type visibilityWord struct{ word string }

func (x *visibilityWord) VisitListed()            { x.word = "listed" }
func (x *visibilityWord) VisitQuiet()             { x.word = "quiet" }
func (x *visibilityWord) VisitHiddenByDirectory() { x.word = "hidden_by_directory" }

// storedCardStanding is a stored card standing as fleetcount knows it; a word this adapter does not know is the zero
// CardStanding, which fleetcount refuses.
func storedCardStanding(word string) fleetcount.CardStanding {
	switch word {
	case "stands":
		return fleetcount.CardStandingStands()
	case "form":
		return fleetcount.CardStandingForm()
	}
	var unknown fleetcount.CardStanding
	return unknown
}

// cardStandingWord is a card standing as the store spells it.
type cardStandingWord struct{ word string }

func (x *cardStandingWord) VisitStands() { x.word = "stands" }
func (x *cardStandingWord) VisitForm()   { x.word = "form" }

// storedTelling is a stored telling as fleetcount knows it; a word this adapter does not know is the zero Telling,
// which fleetcount refuses.
func storedTelling(word string) fleetcount.Telling {
	switch word {
	case "told":
		return fleetcount.TellingTold()
	case "untold":
		return fleetcount.TellingUntold()
	}
	var unknown fleetcount.Telling
	return unknown
}

// tellingWord is a telling as the store spells it.
type tellingWord struct{ word string }

func (x *tellingWord) VisitTold()   { x.word = "told" }
func (x *tellingWord) VisitUntold() { x.word = "untold" }

// storedHolding is a stored holding as fleetcount knows it; a word this adapter does not know is the zero Holding,
// which fleetcount refuses.
func storedHolding(word string) fleetcount.Holding {
	switch word {
	case "held_by_persona":
		return fleetcount.HoldingHeldByPersona()
	case "held_by_supervisor":
		return fleetcount.HoldingHeldBySupervisor()
	case "for_the_owner":
		return fleetcount.HoldingForTheOwner()
	}
	var unknown fleetcount.Holding
	return unknown
}

// holdingWord is a holding as the store spells it.
type holdingWord struct{ word string }

func (x *holdingWord) VisitHeldByPersona()    { x.word = "held_by_persona" }
func (x *holdingWord) VisitHeldBySupervisor() { x.word = "held_by_supervisor" }
func (x *holdingWord) VisitForTheOwner()      { x.word = "for_the_owner" }

// storedWhy is a stored why as fleetcount knows it; a word this adapter does not know is the zero Why, which fleetcount
// refuses.
func storedWhy(word string) fleetcount.Why {
	switch word {
	case "needs_you":
		return fleetcount.WhyNeedsYou()
	case "held":
		return fleetcount.WhyHeld()
	case "muted":
		return fleetcount.WhyMuted()
	case "quiet":
		return fleetcount.WhyQuiet()
	}
	var unknown fleetcount.Why
	return unknown
}

// whyWord is a why as the store spells it.
type whyWord struct{ word string }

func (x *whyWord) VisitNeedsYou() { x.word = "needs_you" }
func (x *whyWord) VisitHeld()     { x.word = "held" }
func (x *whyWord) VisitMuted()    { x.word = "muted" }
func (x *whyWord) VisitQuiet()    { x.word = "quiet" }

// storedSurface is a stored surface as fleetcount knows it; a word this adapter does not know is the zero Surface,
// which fleetcount refuses.
func storedSurface(word string) fleetcount.Surface {
	switch word {
	case "badge":
		return fleetcount.SurfaceBadge()
	case "live_activity":
		return fleetcount.SurfaceLiveActivity()
	case "link":
		return fleetcount.SurfaceLink()
	}
	var unknown fleetcount.Surface
	return unknown
}

// surfaceWord is a surface as the store spells it.
type surfaceWord struct{ word string }

func (x *surfaceWord) VisitBadge()        { x.word = "badge" }
func (x *surfaceWord) VisitLiveActivity() { x.word = "live_activity" }
func (x *surfaceWord) VisitLink()         { x.word = "link" }
