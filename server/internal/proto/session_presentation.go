package proto

import "slices"

// SessionPresentation is the manager's display-only metadata. It lives behind SessionHierarchy's optional pointer so
// an ordinary Session's size is unchanged and an ordinary hierarchy allocates no presentation object.
// Its members are flattened onto the session's wire object.
type SessionPresentation struct {
	// DisplayName is the user's title override. Title carries the same effective name for older clients; empty restores
	// the current automatic/native title without touching the runtime session.
	DisplayName string `json:"display_name,omitempty"`
	// Tags are the user's labels in their chosen order. Treat the slice as immutable, like the rest of a published row.
	Tags []string `json:"tags,omitempty"`
}

func (p *SessionPresentation) empty() bool {
	return p == nil || p.DisplayName == "" && len(p.Tags) == 0
}

// DisplayName is the user's presentation-only title, or "" when the automatic/native title is shown.
func (s *Session) DisplayName() string {
	if s.Hierarchy == nil || s.Hierarchy.Presentation == nil {
		return ""
	}
	return s.Hierarchy.Presentation.DisplayName
}

// Tags is the user's presentation-only labels. Treat the returned slice as immutable.
func (s *Session) Tags() []string {
	if s.Hierarchy == nil || s.Hierarchy.Presentation == nil {
		return nil
	}
	return s.Hierarchy.Presentation.Tags
}

// SetPresentation replaces the display-only name and tags without changing the runtime session or its hierarchy.
func (s *Session) SetPresentation(displayName string, tags []string) {
	if s.DisplayName() == displayName && slices.Equal(s.Tags(), tags) {
		return
	}
	h := s.hierarchy()
	if displayName == "" && len(tags) == 0 {
		h.Presentation = nil
	} else {
		h.Presentation = &SessionPresentation{DisplayName: displayName, Tags: slices.Clone(tags)}
	}
	s.setHierarchy(h)
}
