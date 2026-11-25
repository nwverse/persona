package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"fmt"
	"github.com/nwverse/persona/server/internal/kb"
	"os"
	"slices"
	"testing"
	"time"
)

// photoUpload is photo i (its id made of i) taken at seconds into the recording: a JPEG, its digest covering the body.
func photoUpload(i int, at float64, caption, text string) kb.PhotoUpload {
	id := fmt.Sprintf("%08x-0000-4000-8000-000000000000", i)
	data := []byte(fmt.Sprintf("image bytes %d", i))
	return kb.PhotoUpload{ID: id, At: at, Mime: kb.PhotoJPEG, Data: data, Width: 1600, Height: 1200, Text: text, Caption: caption,
		Digest: fmt.Sprintf("%s|%v|%s|%s|%s", id, at, caption, text, data)}
}

func addPhoto(t *testing.T, e *testEngine, id string, u kb.PhotoUpload, queue bool) kb.Photo {
	t.Helper()
	p, created, err := e.PhotoAdd(id, u, queue)
	if err != nil || !created {
		t.Fatalf("add photo %s: %v (created %v)", u.ID, err, created)
	}
	return p
}

// photoMeeting stores a complete meeting recording of three lines, 10 seconds apart, that ended 50 seconds after start.
func photoMeeting(t *testing.T, e *testEngine, id string, start time.Time) {
	t.Helper()
	u := meetingUpload(1, start, "第一句", "第二句", "第三句")
	end := start.Add(50 * time.Second)
	u.EndedAt = &end
	putRecording(t, e, id, u, start.Add(time.Minute))
}

// photosTold counts MeetingChanged's PartPhotos per recording.
func photosTold(e *testEngine) map[string]int {
	told := map[string]int{}
	e.MeetingChanged = func(id string, parts []string) {
		if slices.Contains(parts, PartPhotos) {
			told[id]++
		}
	}
	return told
}

// readFile is the text of a file of recording id.
func readFile(t *testing.T, e *testEngine, id, name string) string {
	t.Helper()
	raw, err := os.ReadFile(e.Store.Path(KindRecording, id, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
