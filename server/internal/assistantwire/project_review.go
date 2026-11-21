package assistantwire

import "time"

const (
	CapabilityProjectReview       = "project_review.owner_input"
	MethodProjectReviewOwnerInput = "project_review.owner_input"
)

// ProjectReviewOwnerInput comes from the hub's authenticated app-message route.
type ProjectReviewOwnerInput struct {
	SessionRef
	MessageID string    `json:"message_id"`
	At        time.Time `json:"at"`
	Unscoped  bool      `json:"unscoped,omitempty"`
}
