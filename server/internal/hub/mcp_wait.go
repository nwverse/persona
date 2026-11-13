package hub

import (
	"encoding/json"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

// mcpWaitReporter relays a phone confirmation's enum through the manager that owns the pending call's machine. It
// checks the current connection on every send and stops if that manager is replaced or no longer owns the machine.
// No state is kept after the call.
func (h *Hub) mcpWaitReporter(from, machine, callID string) func(proto.MCPWaitKind) {
	if from == "" || machine == "" || callID == "" || !h.managerCan(from, assistantwire.CapabilityMCPWait) ||
		h.machineOwner(machine) != from {
		return nil
	}
	return func(kind proto.MCPWaitKind) {
		if !h.managerCan(from, assistantwire.CapabilityMCPWait) || h.machineOwner(machine) != from {
			return
		}
		params, err := json.Marshal(proto.MCPWaitEvent{CallID: callID, Kind: proto.NormalizeMCPWaitKind(kind)})
		if err != nil {
			return
		}
		_ = h.notifyBus(from, devicewire.EventNotify, devicewire.NodeEvent{
			MachineID: machine, Event: proto.EventMCPWait, Params: params,
		})
	}
}
