package hub

import (
	"context"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// featureMeetingAttendance: the default manager attends recorded meetings (docs/assistant/meeting-minutes.md
// "Attending a meeting"). The recordings' routes go to it, so its hello alone decides; an extension's report cannot
// add the feature (features).
const featureMeetingAttendance = assistantwire.CapabilityMeetingAttendance

// featureMeetingConversation: the default manager takes the meeting conversation's phase 1
// (docs/assistant/meeting-minutes.md "Meeting conversation (#3862)", "Flags and compatibility").
const featureMeetingConversation = assistantwire.CapabilityMeetingConversation

// meetingAttendanceFeatures are the features while the default manager is connected and named their capabilities in
// its hello; nothing otherwise, so a phone offers no role, nor a room, to an older manager, which would ignore them.
// meeting_conversation comes only with meeting_attendance.
func (h *Hub) meetingAttendanceFeatures() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner := h.defaultOwner(ctx)
	if h.archiveManagerHas(owner, assistantwire.CapabilityMeetingAttendance) != http.StatusOK {
		return nil
	}
	out := []string{featureMeetingAttendance}
	if h.archiveManagerHas(owner, assistantwire.CapabilityMeetingConversation) == http.StatusOK {
		out = append(out, featureMeetingConversation)
	}
	return out
}
