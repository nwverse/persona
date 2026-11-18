package assistantwire

import "testing"

func TestSealedCompactionIsAFailedTurnNotAnAnswer(t *testing.T) {
	text := "This conversation was compacted natively by <account>: the compaction is sealed (encrypted) to that account"
	if !CutOff(text) || !ErrorOnlyText(text) {
		t.Fatal("raw account-sealed compaction was treated as an answer")
	}
	if CutOff("I investigated the error: " + text) {
		t.Fatal("a reply discussing a sealed compaction was treated as an error")
	}
}
