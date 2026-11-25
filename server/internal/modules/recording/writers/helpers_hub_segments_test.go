package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

func hseg(start, end float64, text string) RecordingSegment {
	return RecordingSegment{Start: start, End: end, Text: text}
}
