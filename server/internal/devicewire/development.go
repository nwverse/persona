package devicewire

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	DevelopmentPackagesPath       = "/api/v1/bus/development-packages"
	DevelopmentPackagesCapability = "devices.development_packages"
	DevelopmentCommitHeader       = "X-Persona-Development-Commit"
	DevelopmentRepository         = "https://github.com/nwverse/persona.git"
)

// DevelopmentTarget identifies one confirmed adoption. Packages is relative to the
// manager's authenticated Hub connection, including any reverse proxy prefix.
type DevelopmentTarget struct {
	Commit     string `json:"commit"`
	Version    string `json:"version"`
	Build      int64  `json:"build"`
	Repository string `json:"repository"`
	Packages   string `json:"packages"`
}

func DevelopmentBuild(commit, version string, build int64) DevelopmentTarget {
	return DevelopmentTarget{Commit: commit, Version: version, Build: build, Repository: DevelopmentRepository,
		Packages: DevelopmentPackagesPath + "/" + commit + "/" + version}
}

func ValidDevelopmentCommit(commit string) bool {
	if len(commit) != 40 || commit != strings.ToLower(commit) {
		return false
	}
	_, err := hex.DecodeString(commit)
	return err == nil
}

func (t DevelopmentTarget) Valid() bool {
	return validBuildNumber(t.Build) && ValidDevelopmentCommit(t.Commit) && t.Version == fmt.Sprintf("web-dev-%s-%d", t.Commit[:12], t.Build) &&
		t == DevelopmentBuild(t.Commit, t.Version, t.Build)
}

// validBuildNumber is update.ParseNumber's rule (YYYYMMDDHHMM, a real time), kept here: a contract package imports no
// business package (docs/server/architecture.md "Contract packages"); TestValidBuildNumberMatchesUpdate holds them equal.
func validBuildNumber(n int64) bool {
	s := strconv.FormatInt(n, 10)
	if len(s) != 12 {
		return false
	}
	_, err := time.Parse("200601021504", s)
	return err == nil
}
