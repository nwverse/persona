package badapp

import (
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// peek reads a tally's total through a generic visitor: peek[slot.Slot, fleetcount.Why, instant.Instant,
// fleetcount.RowCount] implements fleetcount.TallyVisitor without naming a type of the core.
type peek[S, W, I, T any] struct{ total *T }

func (p peek[S, W, I, T]) VisitAgent(S, W, I)       {}                   // out-visitor-impl
func (p peek[S, W, I, T]) VisitCard(S, W)           {}                   // out-visitor-impl
func (p peek[S, W, I, T]) VisitAgentByCard(S, S, W) {}                   // out-visitor-impl
func (p peek[S, W, I, T]) VisitTotal(total T)       { *p.total = total } // out-visitor-impl

// busy decides on a tally's total itself.
func busy(t fleetcount.Tally, quiet fleetcount.RowCount) bool {
	var total fleetcount.RowCount
	t.Accept(peek[slot.Slot, fleetcount.Why, instant.Instant, fleetcount.RowCount]{total: &total})
	return total != quiet
}
