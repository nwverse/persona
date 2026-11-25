package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/db"
)

// pageReadingWritten runs after a reading is stored on watch page p (page patch).
// Phase 2 seam (#13020): S6 replaces this no-op to evaluate the watch's alert rule, record alert state,
// and send a page_alert push when the owner's alert switch is on and the rule newly trips.
func (s *Service) pageReadingWritten(ctx context.Context, p *db.AssistantPageRow) {
	_, _ = ctx, p
}
