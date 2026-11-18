package devices

import (
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// laneBenchNode makes the fake node's peer able to frame its connection, as a node does (node.Serve), and returns the
// capabilities its node.info announces for it.
func laneBenchNode(p *rpc.Peer) (caps []string, ok bool) {
	p.EnableLanes(nodeproto.ControlLane, nodeproto.BulkLane)
	return []string{nodeproto.NodeLanes}, true
}
