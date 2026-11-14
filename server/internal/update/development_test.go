package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentWebRecoveryPreservesConflictingOverride(t *testing.T) {
	dir := t.TempDir()
	p := Pending{RecoveryReady: true, Web: filepath.Join(dir, "web"), WebPrev: filepath.Join(dir, "previous")}
	for _, path := range []string{p.Web, p.WebPrev} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "index.html"), []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RestoreAll(p); err == nil {
		t.Fatal("overwrote a changed override")
	}
	for _, path := range []string{p.Web, p.WebPrev} {
		if got, err := os.ReadFile(filepath.Join(path, "index.html")); err != nil || string(got) != path {
			t.Fatal("changed Web recovery contents", err)
		}
	}
}

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
