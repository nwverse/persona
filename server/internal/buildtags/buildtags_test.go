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

// maxTailscalePackages bounds the tailscale.com and github.com/tailscale packages the three programs link on
// linux/amd64 with the tags and CGO_ENABLED=0, as they are built: 168 when the tags came in (188 without them), plus
// 3%. Keeping another feature, or a new import that pulls one back, goes over it. Raise it only for a tailscale.com
// bump, and say so in the PR.
const maxTailscalePackages = 173

// unlinked are the omitted features the tags most visibly keep out: tsweb (debug pages and varz, whose init alone took a
// third of personad's start before the tags), the web client UI and tailcat's SSH server.
var unlinked = []string{"tailscale.com/tsweb", "tailscale.com/client/web", "github.com/tailscale/gliderssh"}

// The programs built with the tags link none of unlinked and no more of tailscale.com than the subset they need.
func TestProgramsLinkOnlyTheTailscaleSubset(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	cmd := exec.Command(goBin, "list", "-deps", "-tags", Tags(), "-f", "{{.ImportPath}}",
		"./cmd/personad", "./cmd/persona-manager", "./cmd/persona-node")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		t.Fatalf("go list -deps: %v %s", err, msg)
	}
	n := 0
	for _, dep := range strings.Fields(string(out)) {
		for _, p := range unlinked {
			if dep == p || strings.HasPrefix(dep, p+"/") {
				t.Errorf("a program built with build-tags.txt links %s", dep)
			}
		}
		if strings.HasPrefix(dep, "tailscale.com/") || strings.HasPrefix(dep, "github.com/tailscale/") {
			n++
		}
	}
	if n > maxTailscalePackages {
		t.Errorf("personad, persona-manager and persona-node link %d tailscale packages with build-tags.txt, over %d: "+
			"omit the feature that brought them (keep in buildtags.go), or raise the bound for a tailscale.com bump",
			n, maxTailscalePackages)
	}
}
