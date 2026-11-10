// Package buildtags computes the -tags value every program in server/cmd is built with, the one line in
// server/build-tags.txt: the public marker plus a ts_omit_ tag for each tailscale.com feature persona never uses.
//
// tailscale.com lists its omittable features in feature/featuretags, each left out by its ts_omit_ build tag.
// tailcat (persona's tunnel library) builds its own releases that way. persona does the same with its own keep-list:
// the keep-list plus each kept feature's dependencies (featuretags.Requires) stay, and every other feature in the
// registry gets its tag. That leaves out tsweb's debug pages and varz, the web client, the eventbus debug page, SSH,
// Taildrop and dozens more. A plain `go build` without the tags builds the same persona code; it is only bigger.
//
// Regenerate the file after a tailscale.com bump (TestTagsFileInSync fails until then):
//
//	cd server && go run ./internal/buildtags/printtags > build-tags.txt
package buildtags

import (
	"slices"
	"strings"

	"tailscale.com/feature/featuretags"
)

// Marker is the tag that marks a published build for scripts/release/check_bundle_privacy.py. It changes no code
// (update.TestOneBuild).
const Marker = "public"

// keep lists the tailscale.com features persona's programs link. The first four are what tailcat's releases keep
// besides ssh, which persona never calls (internal/tunnel uses addresses, clients, servers, keys and DERP maps only):
// netstack (the userspace TCP the tunnel runs on), gro (receive offload in netstack on Linux), bakedroots (embedded
// roots, so DERP still verifies where the system CA store is missing) and androidbin (raw binaries on Android).
// The other four keep the tunnel's network behavior as in a build without the tags: portmapper (NAT-PMP, PCP and
// UPnP mappings for direct paths), captiveportal (captive portal detection in netcheck), useproxy (system and
// environment proxies to reach DERP) and cloud (a cloud host's public IPs as direct endpoints, and its DNS resolver
// as a fallback for DERP host names).
var keep = []featuretags.FeatureTag{
	"netstack",
	"gro",
	"bakedroots",
	"androidbin",
	"portmapper",
	"captiveportal",
	"useproxy",
	"cloud",
}

// Tags returns the comma-joined -tags value, sorted so the same tailscale.com version always gives the same line:
// Marker and the ts_omit_ tag of every omittable feature that keep does not need.
func Tags() string {
	kept := map[featuretags.FeatureTag]bool{}
	for _, ft := range keep {
		for dep := range featuretags.Requires(ft) {
			kept[dep] = true
		}
	}
	tags := []string{Marker}
	for ft := range featuretags.Features {
		if ft != "" && ft.IsOmittable() && !kept[ft] {
			tags = append(tags, ft.OmitTag())
		}
	}
	slices.Sort(tags)
	return strings.Join(tags, ",")
}
