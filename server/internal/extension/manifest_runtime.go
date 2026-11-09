package extension

import (
	"slices"
	"strings"
)

// Runtime is how the plugin's program runs; a plugin without one runs no code.
type Runtime struct {
	// Mode is process (the default) or deno, a prebuilt source bundle in an independently confined runtime.
	Mode    string            `json:"mode,omitempty"`
	Source  *SourceRuntime    `json:"source,omitempty"`
	Command Command           `json:"command,omitzero"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// RPC: "stdio" (the default) speaks the plugin protocol on stdin / stdout; "none" does not.
	RPC string `json:"rpc,omitempty"`
	// HTTP: the program serves HTTP on PERSONA_PLUGIN_SOCKET, and the hub relays /api/v1/plugins/<id>/… to it.
	HTTP bool `json:"http,omitempty"`
	// Resources are the most it may use when the hub runs it in a sandbox (docs/extensions/extensions.md "Sandbox"); the defaults
	// otherwise.
	Resources *Resources `json:"resources,omitempty"`
}

// Resources cap a sandboxed program: memory in MB and CPU in percent of one core.
type Resources struct {
	MemoryMB   int `json:"memory_mb,omitempty"`
	CPUPercent int `json:"cpu_percent,omitempty"`
}

// The bounds of Resources, and what a program that names none gets.
const (
	ResourcesMemoryMin, ResourcesMemoryMax, ResourcesMemoryDefault = 64, 8192, 1024
	ResourcesCPUMin, ResourcesCPUMax, ResourcesCPUDefault          = 10, 800, 100
)

// Limits are r with the defaults filled in.
func (r *Resources) Limits() Resources {
	out := Resources{MemoryMB: ResourcesMemoryDefault, CPUPercent: ResourcesCPUDefault}
	if r != nil && r.MemoryMB > 0 {
		out.MemoryMB = r.MemoryMB
	}
	if r != nil && r.CPUPercent > 0 {
		out.CPUPercent = r.CPUPercent
	}
	return out
}

// SpeaksRPC says whether the program speaks the plugin protocol.
func (r *Runtime) SpeaksRPC() bool { return r != nil && r.RPC != "none" }

// NodeRuntime is the plugin's program that runs on computers, not on the hub (docs/plans/plan-extensions.md "7"): on each
// machine the user switched on in the configuration field Machines, started and stopped by that machine's node.
type NodeRuntime struct {
	// Command is one per platform ("darwin-arm64": "node/bin/x"); its platforms are the computers it runs on.
	Command Command           `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Machines names the configuration field that picks the computers: an array of format machine, its x-os the
	// systems Command has programs for (its maxItems, when it may run on one only: iMessage's Mac).
	Machines string `json:"machines"`
	RPC      string `json:"rpc,omitempty"`
	HTTP     bool   `json:"http,omitempty"`
	// KeepRunning is what it does while the hub cannot reach its machine: "lease" (the default) stops half an hour
	// after the hub last renewed it; "always" goes on (one that has to keep receiving, as iMessage).
	KeepRunning string `json:"keep_running,omitempty"`
	// Secrets are the names (of Manifest.Secrets) its machines get; the others stay on the hub.
	Secrets     []string        `json:"secrets,omitempty"`
	Permissions NodePermissions `json:"permissions,omitzero"`
}

// NodeBuiltin is node.command of a program built into personad (cua, ego, imessage; node/builtin.go): there is no
// package, the node answers plugin.node.call itself. Only the hub's own built-in extensions have it; a package with it
// is refused.
const NodeBuiltin = "builtin"

// Builtin says whether this is a program built into personad (node.command "builtin").
func (r *NodeRuntime) Builtin() bool {
	return r != nil && r.Command.ByPlatform == nil && r.Command.Path == NodeBuiltin
}

// NodeProgram is the node section of a program the hub sends to computers as a package: nil without one, and nil for
// a built-in program (NodeBuiltin), which has no package, no lease, no permission lines and no consent of its own. Use
// it, not Node, wherever packages, states, the field's confirm or consent are concerned.
func (m *Manifest) NodeProgram() *NodeRuntime {
	if m == nil || m.Node.Builtin() {
		return nil
	}
	return m.Node
}

// NodePermissions are what the program on the computers asks for, shown before installing and before a machine is
// switched on. Like Permissions, shown and recorded; the node does not enforce them yet.
type NodePermissions struct {
	// Network are the hosts it reaches from the computers.
	Network []string `json:"network,omitempty"`
	// Files: it reads and writes files outside its own data directory.
	Files bool `json:"files,omitempty"`
	// MacOSPrivacy: it asks for macOS's privacy permissions (Full Disk Access, Accessibility…), granted on that Mac.
	MacOSPrivacy bool `json:"macos_privacy,omitempty"`
}

// Keep-running choices of a node program.
const (
	KeepLease  = "lease"
	KeepAlways = "always"
)

// nodeSystems are the systems a node program can be for.
var nodeSystems = []string{"darwin", "linux"}

// SpeaksRPC says whether the program speaks the plugin protocol.
func (r *NodeRuntime) SpeaksRPC() bool { return r != nil && r.RPC != "none" }

// Systems are the systems the program has a build for ("darwin", "linux"), sorted, once each.
func (r *NodeRuntime) Systems() []string {
	var out []string
	for platform := range r.Command.ByPlatform {
		if sys, _, ok := strings.Cut(platform, "-"); ok && !slices.Contains(out, sys) {
			out = append(out, sys)
		}
	}
	slices.Sort(out)
	return out
}

// NodeMachines is the configuration field that picks the computers of the node program; nil without one.
func (m *Manifest) NodeMachines() *Field {
	if m.NodeProgram() == nil || m.Node.Machines == "" {
		return nil
	}
	sc, err := ParseSchema(m.Configuration)
	if err != nil || sc == nil {
		return nil
	}
	for i := range sc.Fields {
		if sc.Fields[i].Name == m.Node.Machines {
			return &sc.Fields[i]
		}
	}
	return nil
}
