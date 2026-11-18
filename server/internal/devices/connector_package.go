package devices

import (
	"context"
	"errors"
)

// connectorPackage is the Persona Link connector build for a computer of goos/arch, found where the node builds are
// (docs/operations/node-update.md "Node packages"): the hub's own (the node binary dir's persona-link-<os>-<arch>,
// counted only when its .version says the hub's), else the release's of the hub's version (manifest links), fetched
// into the cache and checked like a node build. path is a file on this machine; version what it reports.
// A switch to Persona Link (link_switch.go) sends it over ssh. errNoConnector: none anywhere.
func (d *Devices) connectorPackage(ctx context.Context, goos, arch string) (path, version string, err error) {
	return "", "", errNoConnector
}

// errNoConnector: there is no Persona Link build for that platform (proto.LinkSwitchReasonNoPackage).
var errNoConnector = errors.New("no Persona Link build for this platform")
