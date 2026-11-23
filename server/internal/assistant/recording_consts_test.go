package assistant

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// What the recording module answers and names, as the core's integration tests expect it (the module's own code
// says the same: internal/modules/recording).
const (
	codeRecordingNotFound = "recording_not_found"
	codeTranscriptExpired = "transcript_expired"
	problemNoInterim      = "no_interim"
	problemUpstreamFailed = "upstream_failed"
	attendeeUnavailable   = "attendee_unavailable"
	attendeeNotAllowed    = "attendee_not_allowed"
	attendeeBusy          = "busy"
	dayPushPhone          = "app" // channel.App: the phone among a day push's destinations
	partRoom              = "room"
	titleMeeting          = "会议"            // i18n-ignore: a meeting conversation title's kind
	justMeMax             = 3 * time.Hour   // how long a Just me turn's mark lasts at most
	justMeAfter           = 2 * time.Minute // and after its turn ended
)

// meetingSessionID is the conversation id the module gives part ("room", "followup") of recording's meeting.
func meetingSessionID(recording, part string) string {
	sum := sha256.Sum256([]byte("persona-meeting:" + part + ":" + strings.ToLower(recording)))
	sum[6], sum[8] = sum[6]&0x0f|0x40, sum[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
