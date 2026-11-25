package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"github.com/nwverse/persona/server/internal/kb"
	"time"
)

// attendedUpload is the upload an attending phone sends when the meeting starts: a meeting, no text yet.
func attendedUpload(rev int64, start time.Time, role string) RecordingUpload {
	u := recUpload(rev, start, false)
	u.Kind, u.Attendance = kb.RecordingMeeting, role
	return u
}
