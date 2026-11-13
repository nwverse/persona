package devices

import (
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// Codes of proto.Machine.NodeIncompatible.
const (
	incompatibleNodeOld = "node_protocol_old" // an older protocol, or a required capability missing: update the node
	incompatibleNodeNew = "node_protocol_new" // the node needs a newer manager
)

// nodeCompat is what the manager makes of a node's handshake (docs/protocol/protocol.md "Node handshake").
type nodeCompat struct {
	// Incompatible is a NodeIncompatible code, "" when the manager can drive the node.
	Incompatible string
	// Missing are the required capabilities the node did not announce.
	Missing []string
}

// compatOf judges a node by its protocol and capabilities, never by its build.
func compatOf(info proto.NodeInfo) nodeCompat {
	if info.Protocol == 0 { // from before the handshake: driven as before (nodeHas reads its old flags)
		return nodeCompat{}
	}
	var c nodeCompat
	for _, want := range nodeproto.Required {
		if !slices.Contains(info.Capabilities, want) {
			c.Missing = append(c.Missing, want)
		}
	}
	switch {
	case info.Protocol < nodeproto.MinVersion || len(c.Missing) > 0:
		c.Incompatible = incompatibleNodeOld
	case info.ProtocolMin > nodeproto.Version:
		c.Incompatible = incompatibleNodeNew
	}
	return c
}

// nodeHas says whether the node can do capability cap: what it announced, or for a node from before the handshake,
// the node.info flag that said so then (a capability without one is taken as there: those nodes all had it).
func nodeHas(info proto.NodeInfo, cap string) bool {
	if info.Protocol > 0 {
		return slices.Contains(info.Capabilities, cap)
	}
	switch cap {
