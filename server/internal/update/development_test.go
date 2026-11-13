package update

import (
	"os"
	"strings"
	"testing"
)

func TestDevelopmentAdoptionFenceMatchesExactAttempt(t *testing.T) {
	dir := t.TempDir()
	p := Pending{ToBuild: 202611130059, Development: &DevelopmentCandidate{Commit: strings.Repeat("a", 40)}}
	if err := BeginDevelopmentAdoption(dir, p); err != nil {
		t.Fatal(err)
	}
	if err := BeginDevelopmentAdoption(dir, p); err == nil {
		t.Fatal("replayed unknown adoption")
	}
	other := p
	other.ToBuild++
	ClearDevelopmentAdoption(dir, other)
	if _, err := os.Stat(DevelopmentAdoptionPath(dir)); err != nil {
		t.Fatal("another attempt cleared the fence")
	}
	ClearDevelopmentAdoption(dir, p)
	if _, err := os.Stat(DevelopmentAdoptionPath(dir)); !os.IsNotExist(err) {
		t.Fatal("matching fence retained", err)
	}
	if got := DevelopmentNumber(p.ToBuild); got <= p.ToBuild {
		t.Fatal("non-monotonic identity", got)
	}
}
