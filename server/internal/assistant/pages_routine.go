package assistant

import "context"

// A page's bound routine follows the page (#13020, docs/assistant/dashboards.md "The routine: schedule_routine.page_id"):
// removing the page turns its routine off, restoring the page turns it on again, and deleting the page for good
// (Delete now, the purge of Recently removed) deletes the routine with it. The owner's actions and the upkeep call these
// after the page's own change is stored; the routine side is kept here.

// pageRoutineRemoved: page id went to Recently removed; its routine routineID ("" when none) stops.
func (s *Service) pageRoutineRemoved(ctx context.Context, id, routineID string) {
	_, _, _ = ctx, id, routineID
}

// pageRoutineRestored: page id came back from Recently removed; its routine routineID ("" when none) runs again.
func (s *Service) pageRoutineRestored(ctx context.Context, id, routineID string) {
	_, _, _ = ctx, id, routineID
}

// pageRoutineDeleted: page id ("" when only the routine is known, as after a purge) is gone for good; its routine
// routineID ("" when none) goes too.
func (s *Service) pageRoutineDeleted(ctx context.Context, id, routineID string) {
	_, _, _ = ctx, id, routineID
}
