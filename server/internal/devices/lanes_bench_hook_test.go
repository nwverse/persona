package devices

import "github.com/nwverse/persona/server/internal/rpc"

// laneBenchNode makes the fake node's peer able to frame its connection, and returns the capabilities its node.info
// announces; ok is false while this build has no lane framing (the bench then runs lanes-off only).
func laneBenchNode(p *rpc.Peer) (caps []string, ok bool) { return nil, false }
