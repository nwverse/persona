package devicewire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDevelopmentTargetPinsRepositoryCommitBuildAndRelativePackages(t *testing.T) {
	target := DevelopmentBuild(strings.Repeat("b", 40), "web-dev-bbbbbbbbbbbb-202611230942", 202611230942)
	if !target.Valid() {
		t.Fatal("valid development target rejected")
	}
	for _, invalid := range []DevelopmentTarget{
		DevelopmentBuild(target.Commit, "web-dev-bbbbbbbbbbbb-42", 42),
		DevelopmentBuild(target.Commit, "web-dev-bbbbbbbbbbbb-202611239942", 202611239942),
	} {
		if invalid.Valid() {
			t.Fatalf("accepted a build the installed program would report as zero: %d", invalid.Build)
		}
	}
	for _, alter := range []func(*DevelopmentTarget){
		func(t *DevelopmentTarget) { t.Commit = "main" },
		func(t *DevelopmentTarget) { t.Commit = strings.Repeat("z", 40) },
		func(t *DevelopmentTarget) { t.Commit = strings.ToUpper(t.Commit) },
		func(t *DevelopmentTarget) { t.Build++ },
		func(t *DevelopmentTarget) { t.Version = "latest" },
		func(t *DevelopmentTarget) { t.Repository = "https://example.com/project.git" },
		func(t *DevelopmentTarget) { t.Packages = "https://example.com/package" },
	} {
		bad := target
		alter(&bad)
		if bad.Valid() {
			t.Fatalf("accepted %+v", bad)
		}
	}
	data, err := json.Marshal(Config{})
	if err != nil || strings.Contains(string(data), "development_") {
		t.Fatal("optional fields on old handover", err)
	}
}
