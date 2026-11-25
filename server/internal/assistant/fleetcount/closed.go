package fleetcount

import (
	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// State is a state as fleetcount knows it: working, needs you, finished or idle. The zero State is no state; only its
// constructors make one, and Accept raises a defect on it.
type State struct{ state uint8 }

// StateWorking is the working state.
func StateWorking() State { return State{state: 1} }

// StateNeedsYou is the needs you state.
func StateNeedsYou() State { return State{state: 2} }

// StateFinished is the finished state.
func StateFinished() State { return State{state: 3} }

// StateIdle is the idle state.
func StateIdle() State { return State{state: 4} }

// StateVisitor is told which state it is.
type StateVisitor interface {
	VisitWorking()
	VisitNeedsYou()
	VisitFinished()
	VisitIdle()
}

// Accept tells v which state it is; the zero State is a defect.
func (x State) Accept(v StateVisitor) {
	switch x.state {
	case 1:
		v.VisitWorking()
	case 2:
		v.VisitNeedsYou()
	case 3:
		v.VisitFinished()
	case 4:
		v.VisitIdle()
	default:
		defect.Raise("fleetcount.State.Accept", "the zero State")
	}
}

// Role is a role as fleetcount knows it: worker or assistant. The zero Role is no role; only its constructors make one,
// and Accept raises a defect on it.
type Role struct{ role uint8 }

// RoleWorker is the worker role.
func RoleWorker() Role { return Role{role: 1} }

// RoleAssistant is the assistant role.
func RoleAssistant() Role { return Role{role: 2} }

// RoleVisitor is told which role it is.
type RoleVisitor interface {
	VisitWorker()
	VisitAssistant()
}

// Accept tells v which role it is; the zero Role is a defect.
func (x Role) Accept(v RoleVisitor) {
	switch x.role {
	case 1:
		v.VisitWorker()
	case 2:
		v.VisitAssistant()
	default:
		defect.Raise("fleetcount.Role.Accept", "the zero Role")
	}
}

// Lineage is a lineage as fleetcount knows it: root or subtask. The zero Lineage is no lineage; only its constructors
// make one, and Accept raises a defect on it.
type Lineage struct{ lineage uint8 }

// LineageRoot is the root lineage.
func LineageRoot() Lineage { return Lineage{lineage: 1} }

// LineageSubtask is the subtask lineage.
func LineageSubtask() Lineage { return Lineage{lineage: 2} }

// LineageVisitor is told which lineage it is.
type LineageVisitor interface {
	VisitRoot()
	VisitSubtask()
}

// Accept tells v which lineage it is; the zero Lineage is a defect.
func (l Lineage) Accept(v LineageVisitor) {
	switch l.lineage {
	case 1:
		v.VisitRoot()
	case 2:
		v.VisitSubtask()
	default:
		defect.Raise("fleetcount.Lineage.Accept", "the zero Lineage")
	}
}

// Muting is a muting as fleetcount knows it: heard or muted. The zero Muting is no muting; only its constructors make
// one, and Accept raises a defect on it.
type Muting struct{ muting uint8 }

// MutingHeard is the heard muting.
func MutingHeard() Muting { return Muting{muting: 1} }

// MutingMuted is the muted muting.
func MutingMuted() Muting { return Muting{muting: 2} }

// MutingVisitor is told which muting it is.
type MutingVisitor interface {
	VisitHeard()
	VisitMuted()
}

// Accept tells v which muting it is; the zero Muting is a defect.
func (m Muting) Accept(v MutingVisitor) {
	switch m.muting {
	case 1:
		v.VisitHeard()
	case 2:
		v.VisitMuted()
	default:
		defect.Raise("fleetcount.Muting.Accept", "the zero Muting")
	}
}

// Visibility is a visibility as fleetcount knows it: listed, quiet or hidden by directory. The zero Visibility is no
// visibility; only its constructors make one, and Accept raises a defect on it.
type Visibility struct{ visibility uint8 }

// VisibilityListed is the listed visibility.
func VisibilityListed() Visibility { return Visibility{visibility: 1} }

// VisibilityQuiet is the quiet visibility.
func VisibilityQuiet() Visibility { return Visibility{visibility: 2} }

// VisibilityHiddenByDirectory is the hidden by directory visibility.
func VisibilityHiddenByDirectory() Visibility { return Visibility{visibility: 3} }

// VisibilityVisitor is told which visibility it is.
type VisibilityVisitor interface {
	VisitListed()
	VisitQuiet()
	VisitHiddenByDirectory()
}

// Accept tells v which visibility it is; the zero Visibility is a defect.
func (x Visibility) Accept(v VisibilityVisitor) {
	switch x.visibility {
	case 1:
		v.VisitListed()
	case 2:
		v.VisitQuiet()
	case 3:
		v.VisitHiddenByDirectory()
	default:
		defect.Raise("fleetcount.Visibility.Accept", "the zero Visibility")
	}
}

// CardStanding is a card standing as fleetcount knows it: stands or form. The zero CardStanding is no card standing;
// only its constructors make one, and Accept raises a defect on it.
type CardStanding struct{ cardStanding uint8 }

// CardStandingStands is the stands card standing.
func CardStandingStands() CardStanding { return CardStanding{cardStanding: 1} }

// CardStandingForm is the form card standing.
func CardStandingForm() CardStanding { return CardStanding{cardStanding: 2} }

// CardStandingVisitor is told which card standing it is.
type CardStandingVisitor interface {
	VisitStands()
	VisitForm()
}

// Accept tells v which card standing it is; the zero CardStanding is a defect.
func (c CardStanding) Accept(v CardStandingVisitor) {
	switch c.cardStanding {
	case 1:
		v.VisitStands()
	case 2:
		v.VisitForm()
	default:
		defect.Raise("fleetcount.CardStanding.Accept", "the zero CardStanding")
	}
}

// Telling is a telling as fleetcount knows it: told or untold. The zero Telling is no telling; only its constructors
// make one, and Accept raises a defect on it.
type Telling struct{ telling uint8 }

// TellingTold is the told telling.
func TellingTold() Telling { return Telling{telling: 1} }

// TellingUntold is the untold telling.
func TellingUntold() Telling { return Telling{telling: 2} }

// TellingVisitor is told which telling it is.
type TellingVisitor interface {
	VisitTold()
	VisitUntold()
}

// Accept tells v which telling it is; the zero Telling is a defect.
func (x Telling) Accept(v TellingVisitor) {
	switch x.telling {
	case 1:
		v.VisitTold()
	case 2:
		v.VisitUntold()
	default:
		defect.Raise("fleetcount.Telling.Accept", "the zero Telling")
	}
}

// Holding is a holding as fleetcount knows it: held by persona, held by supervisor or for the owner.
// The zero Holding is no holding; only its constructors make one, and Accept raises a defect on it.
type Holding struct{ holding uint8 }

// HoldingHeldByPersona is the held by persona holding.
func HoldingHeldByPersona() Holding { return Holding{holding: 1} }

// HoldingHeldBySupervisor is the held by supervisor holding.
func HoldingHeldBySupervisor() Holding { return Holding{holding: 2} }

// HoldingForTheOwner is the for the owner holding.
func HoldingForTheOwner() Holding { return Holding{holding: 3} }

// HoldingVisitor is told which holding it is.
type HoldingVisitor interface {
	VisitHeldByPersona()
	VisitHeldBySupervisor()
	VisitForTheOwner()
}

// Accept tells v which holding it is; the zero Holding is a defect.
func (h Holding) Accept(v HoldingVisitor) {
	switch h.holding {
	case 1:
		v.VisitHeldByPersona()
	case 2:
		v.VisitHeldBySupervisor()
	case 3:
		v.VisitForTheOwner()
	default:
		defect.Raise("fleetcount.Holding.Accept", "the zero Holding")
	}
}

// Why is a why as fleetcount knows it: needs you, held, muted or quiet. The zero Why is no why; only its constructors
// make one, and Accept raises a defect on it.
type Why struct{ why uint8 }

// WhyNeedsYou is the needs you why.
func WhyNeedsYou() Why { return Why{why: 1} }

// WhyHeld is the held why.
func WhyHeld() Why { return Why{why: 2} }

// WhyMuted is the muted why.
func WhyMuted() Why { return Why{why: 3} }

// WhyQuiet is the quiet why.
func WhyQuiet() Why { return Why{why: 4} }

// WhyVisitor is told which why it is.
type WhyVisitor interface {
	VisitNeedsYou()
	VisitHeld()
	VisitMuted()
	VisitQuiet()
}

// Accept tells v which why it is; the zero Why is a defect.
func (w Why) Accept(v WhyVisitor) {
	switch w.why {
	case 1:
		v.VisitNeedsYou()
	case 2:
		v.VisitHeld()
	case 3:
		v.VisitMuted()
	case 4:
		v.VisitQuiet()
	default:
		defect.Raise("fleetcount.Why.Accept", "the zero Why")
	}
}

// Surface is a surface as fleetcount knows it: badge, live activity or link. The zero Surface is no surface; only its
// constructors make one, and Accept raises a defect on it.
type Surface struct{ surface uint8 }

// SurfaceBadge is the badge surface.
func SurfaceBadge() Surface { return Surface{surface: 1} }

// SurfaceLiveActivity is the live activity surface.
func SurfaceLiveActivity() Surface { return Surface{surface: 2} }

// SurfaceLink is the link surface.
func SurfaceLink() Surface { return Surface{surface: 3} }

// SurfaceVisitor is told which surface it is.
type SurfaceVisitor interface {
	VisitBadge()
	VisitLiveActivity()
	VisitLink()
}

// Accept tells v which surface it is; the zero Surface is a defect.
func (x Surface) Accept(v SurfaceVisitor) {
	switch x.surface {
	case 1:
		v.VisitBadge()
	case 2:
		v.VisitLiveActivity()
	case 3:
		v.VisitLink()
	default:
		defect.Raise("fleetcount.Surface.Accept", "the zero Surface")
	}
}

// Placement is a placement as fleetcount knows it: own row or under. The zero Placement is nil; only its constructors
// make one.
type Placement interface {
	Accept(v PlacementVisitor)
	placement()
}

// PlacementVisitor is told which placement it is, with what it holds.
type PlacementVisitor interface {
	VisitOwnRow()
	VisitUnder(ref agentref.Ref)
}

// placementOwnRow is the own row placement.
type placementOwnRow struct{}

// placementUnder is the under placement, with its ref.
type placementUnder struct{ ref agentref.Ref }

// PlacementOwnRow is the own row placement.
func PlacementOwnRow() Placement { return placementOwnRow{} }

// PlacementUnder is the under placement with its ref; no ref is a defect.
func PlacementUnder(ref agentref.Ref) Placement {
	var noRef agentref.Ref
	if ref == noRef {
		defect.Raise("fleetcount.PlacementUnder", "the zero Ref")
	}
	return placementUnder{ref: ref}
}

func (placementOwnRow) Accept(v PlacementVisitor)  { v.VisitOwnRow() }
func (x placementUnder) Accept(v PlacementVisitor) { v.VisitUnder(x.ref) }
func (placementOwnRow) placement()                 {}
func (placementUnder) placement()                  {}
