package recordingwire

import (
	"regexp"
	"testing"
)

// RecordingID accepts exactly what the pattern the knowledge base used accepts.
func TestRecordingIDIsAUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	for _, raw := range []string{"6f1c2a3b-0000-4000-8000-00000000c0de", " 6F1C2A3B-0000-4000-8000-00000000C0DE\n", "",
		"6f1c2a3b-0000-4000-8000-00000000c0d", "6f1c2a3b-0000-4000-8000-00000000c0dee", "6f1c2a3b00000-4000-8000-00000000c0de",
		"6f1c2a3b-0000-4000-8000-00000000c0dg", "6f1c2a3b_0000-4000-8000-00000000c0de", "../../etc/passwd-0000-4000-8000-0000",
		"6f1c2a3b-0000-4000-8000-00000000c0de\n6f1c2a3b-0000-4000-8000-00000000c0de", "６f1c2a3b-0000-4000-8000-00000000c0de"} {
		id, ok := RecordingID(raw)
		if want := re.MatchString(id); ok != want {
			t.Errorf("RecordingID(%q) = %q, %v; the pattern says %v", raw, id, ok, want)
		}
	}
}

func TestIsMeetingAudioPath(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments/audio", true},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments/audio/", true},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments/audio/0.pcm", true},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments/audio/index.json", true},
		{"recordings/sample-meeting/attachments/audio/nested/chunk.pcm", true},
		{"./recordings/sample-meeting/attachments/audio/0.pcm", true},
		{"/recordings/sample-meeting/attachments/audio/0.pcm", true},
		{"recordings/../recordings/sample-meeting/attachments/audio/0.pcm", true},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments/p1.jpg", false},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments/audio.jpg", false},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/attachments", false},
		{"recordings/0f8fad5b-d9cb-469f-a165-70867728950e/hub_segments.json", false},
		{"recordings/attachments/audio/0.pcm", false},
		{"recordings/./attachments/audio/0.pcm", false},
		{"recordings/../chats/c1/attachments/audio/0.pcm", false},
		{"../recordings/sample-meeting/attachments/audio/0.pcm", false},
		{"chats/c1/attachments/audio/note.ogg", false},
		{"", false},
		{".", false},
	}
	for _, tc := range cases {
		if got := IsMeetingAudioPath(tc.rel); got != tc.want {
			t.Errorf("IsMeetingAudioPath(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}
