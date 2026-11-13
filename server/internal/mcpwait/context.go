// Package mcpwait carries an opted-in MCP call's opaque correlator through in-process helpers. It holds no wait
// content or state; the node and manager still relay only proto.MCPWaitEvent over their existing connections.
package mcpwait

import "context"

type callContextKey struct{}
type machineContextKey struct{}

// WithCall returns ctx with callID attached. An empty ID leaves the context unchanged, preserving old-call behavior.
func WithCall(ctx context.Context, callID string) context.Context {
	if callID == "" {
		return ctx
	}
	return context.WithValue(ctx, callContextKey{}, callID)
}

// CallFrom returns the opaque MCP call correlator, or "" for an old or non-opted-in call.
func CallFrom(ctx context.Context) string {
	callID, _ := ctx.Value(callContextKey{}).(string)
	return callID
}

// WithMachine attaches the originating node's routing ID when a tool operates a different computer. It is correlation
// metadata on those existing requests, never part of the enum-only wait event.
func WithMachine(ctx context.Context, machine string) context.Context {
	if machine == "" {
		return ctx
	}
	return context.WithValue(ctx, machineContextKey{}, machine)
}

// MachineFrom returns the originating node's routing ID, or "" when the tool stays on the calling node.
func MachineFrom(ctx context.Context) string {
	machine, _ := ctx.Value(machineContextKey{}).(string)
	return machine
}
