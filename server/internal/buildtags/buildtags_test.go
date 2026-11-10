package buildtags

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// build-tags.txt is what the build scripts read (publish.sh, deploy.sh, the Dockerfile, scripts/ci/server.sh): it must
// be Tags() for the tailscale.com version in go.mod, which a bump can change by adding or removing features.
func TestTagsFileInSync(t *testing.T) {
	b, err := os.ReadFile("../../build-tags.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(b), "\r\n", "\n")
	if want := Tags() + "\n"; got != want {
		t.Errorf("server/build-tags.txt is stale; regenerate with: cd server && go run ./internal/buildtags/printtags > build-tags.txt\n got: %s\nwant: %s", got, want)
	}
	for _, tag := range strings.Split(Tags(), ",") {
		if tag != Marker && !strings.HasPrefix(tag, "ts_omit_") {
			t.Errorf("tag %q is neither %q nor a ts_omit_ feature tag", tag, Marker)
		}
	}
}

// The programs built with the tags link none of tsweb (its debug pages and varz, whose init alone took a third of
// personad's start before the tags): a tag that stops omitting it, or a new import, shows up here.
func TestProgramsLinkNoTSWeb(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	cmd := exec.Command(goBin, "list", "-deps", "-tags", Tags(), "-f", "{{.ImportPath}}",
		"./cmd/personad", "./cmd/persona-manager", "./cmd/persona-node")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		t.Fatalf("go list -deps: %v %s", err, msg)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "tailscale.com/tsweb" || strings.HasPrefix(dep, "tailscale.com/tsweb/") {
			t.Errorf("a program built with build-tags.txt links %s", dep)
		}
	}
}
