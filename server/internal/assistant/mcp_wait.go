package assistant

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/mcpwait"
	"github.com/nwverse/persona/server/internal/proto"
)

// mcpWaitReporter belongs to one pending node request. Its stack restores an outer wait if interactions nest; with
// no interaction left, the remote call is running on the computer again.
type mcpWaitReporter struct {
	machine string
	callID  string
	notify  func(devicewire.NodeEvent)

	mu    sync.Mutex
	stack []proto.MCPWaitKind
}

type mcpWaitContextKey struct{}

type mcpWaitNotifier interface {
	Notify(devicewire.NodeEvent)
}

// mcpWaitContext opts in only a request that carries both fields from a node whose hub advertised the feature. A
// devices implementation without Notify simply keeps the unknown fallback.
func (s *Service) mcpWaitContext(ctx context.Context, machine, callID string, enabled bool) context.Context {
	notifier, ok := s.devs.(mcpWaitNotifier)
	if !enabled || callID == "" || !ok {
		return ctx
	}
	r := &mcpWaitReporter{machine: machine, callID: callID, notify: notifier.Notify}
	r.mu.Lock()
	r.publishLocked(proto.MCPWaitComputer)
	r.mu.Unlock()
	ctx = mcpwait.WithCall(ctx, callID)
	return context.WithValue(ctx, mcpWaitContextKey{}, r)
}

func mcpWaitFrom(ctx context.Context) *mcpWaitReporter {
	r, _ := ctx.Value(mcpWaitContextKey{}).(*mcpWaitReporter)
	return r
}

func (r *mcpWaitReporter) publishLocked(kind proto.MCPWaitKind) {
	params, err := json.Marshal(proto.MCPWaitEvent{CallID: r.callID, Kind: proto.NormalizeMCPWaitKind(kind)})
	if err != nil {
		return
	}
	r.notify(devicewire.NodeEvent{MachineID: r.machine, Event: proto.EventMCPWait, Params: params})
}

// pushMCPWait publishes kind and returns the one-time restoration of the previous kind (computer at the outermost
// level). The restoration is deferred by the interaction owner, so resolution and cancellation both move the call on.
func pushMCPWait(ctx context.Context, kind proto.MCPWaitKind) func() {
	r := mcpWaitFrom(ctx)
	if r == nil {
		return func() {}
	}
	kind = proto.NormalizeMCPWaitKind(kind)
	r.mu.Lock()
	r.stack = append(r.stack, kind)
	r.publishLocked(kind)
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if len(r.stack) > 0 {
				r.stack = r.stack[:len(r.stack)-1]
			}
			previous := proto.MCPWaitComputer
			if len(r.stack) > 0 {
				previous = r.stack[len(r.stack)-1]
			}
			r.publishLocked(previous)
			r.mu.Unlock()
		})
	}
}

// mcpWaitParams adds only the correlators the hub needs to relay an approval's wait kind back through the existing
// devices.notify channel. The tool name and arguments stay in their existing fields, never in the signal.
func mcpWaitParams(ctx context.Context, params map[string]any) {
	if r := mcpWaitFrom(ctx); r != nil {
		params["call_id"], params["machine_id"] = r.callID, r.machine
	}
}

func pushMCPInteraction(ctx context.Context, ix proto.Interaction) func() {
	switch ix.Kind {
	case proto.IxPermission, proto.IxPlan:
		return pushMCPWait(ctx, proto.MCPWaitApproval)
	case proto.IxQuestion, proto.IxElicitation:
		return pushMCPWait(ctx, proto.MCPWaitAnswer)
	default:
		return func() {}
	}
}
