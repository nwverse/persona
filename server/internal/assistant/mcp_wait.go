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

// mcpWaitContext opts in only a request that carries a valid UUID from a node whose hub advertised the feature. The
// manager records the origin itself: a later computer.elicit can only join this live call, never choose its machine.
// A duplicate live id or a devices implementation without Notify keeps the unknown fallback.
func (s *Service) mcpWaitContext(ctx context.Context, machine, callID string, enabled bool) context.Context {
	notifier, ok := s.devs.(mcpWaitNotifier)
	if !enabled || !proto.ValidMCPWaitCallID(callID) || !ok {
		return ctx
	}
	r := &mcpWaitReporter{machine: machine, callID: callID, notify: notifier.Notify}
	r.mu.Lock()
	s.mcpWaitMu.Lock()
	if s.mcpWaits[callID] != nil {
		s.mcpWaitMu.Unlock()
		r.mu.Unlock()
		return ctx
	}
	if s.mcpWaits == nil {
		s.mcpWaits = map[string]*mcpWaitReporter{}
	}
	s.mcpWaits[callID] = r
	s.mcpWaitMu.Unlock()
	r.publishLocked(proto.MCPWaitComputer)
	r.mu.Unlock()
	return context.WithValue(mcpwait.WithCall(ctx, callID), mcpWaitContextKey{}, r)
}

// mcpWaitElicitContext joins a nested elicitation to the reporter the manager issued for this call. It deliberately
// does not publish a new base state or register another call; the outer mcp.call owns the reporter's lifetime.
func (s *Service) mcpWaitElicitContext(ctx context.Context, callID string) context.Context {
	if !proto.ValidMCPWaitCallID(callID) {
		return ctx
	}
	s.mcpWaitMu.Lock()
	r := s.mcpWaits[callID]
	s.mcpWaitMu.Unlock()
	if r == nil {
		return ctx
	}
	return context.WithValue(mcpwait.WithCall(ctx, callID), mcpWaitContextKey{}, r)
}

// endMCPWait removes only the registration owned by this request. A duplicate id could not replace the live entry.
func (s *Service) endMCPWait(ctx context.Context) {
	r := mcpWaitFrom(ctx)
	if r == nil {
		return
	}
	s.mcpWaitMu.Lock()
	if s.mcpWaits[r.callID] == r {
		delete(s.mcpWaits, r.callID)
	}
	s.mcpWaitMu.Unlock()
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
