// Package nodeproto is the node protocol's handshake contract (manager ↔ device, docs/protocol/protocol.md "Node handshake"):
// the protocol version and the capability names a node announces in node.info, and the version a manager speaks.
// It holds only constants and name lists and imports nothing, so the manager, the node and any other implementation
// can share it without depending on each other.
//
// Compatibility is decided by these, not by build strings: a node and a manager built from different commits work
// together as long as their protocol ranges overlap and the node has the capabilities the manager requires. A build
// difference is only an upgrade hint.
package nodeproto

// EventRestarting announces a planned resident exec; CodeRestarting refuses a request before it starts.
// Both are optional: older managers ignore the event and newer managers never replay an ambiguous dropped call.
const (
	EventRestarting = "node.restarting"
	CodeRestarting  = "node_restarting"
)

// PersonaContracts is the optional bidirectional naming capability. It does not
// change the node protocol floor or become a required capability.
const PersonaContracts = "persona_contracts_v1"

// ManagerUpdateInventory is the optional read-only local update.inventory
// contract. A legacy peer remains compatible, but cannot produce idle evidence.
const ManagerUpdateInventory = "manager_update_inventory_v1"

// Version is the node protocol this code speaks. Raise it only for a change an older peer cannot follow (a method
// whose params or results changed meaning); additions go in as capabilities instead.
//
// Protocol 0 is the implicit version of nodes from before the handshake: they leave protocol and capabilities out.
const Version = 1

// MinVersion is the oldest announcing node protocol a manager built from this code still drives. Nodes from before
// the handshake (protocol 0) are not judged by it: the manager keeps working with them as before.
const MinVersion = 1

// MinManagerVersion is what a node built from this code announces as protocol_min: the oldest manager protocol it
// still answers.
const MinManagerVersion = 1

// Capabilities a node announces (stable names: never renamed or reused; a node drops one only with a protocol bump).
const (
	// Sessions: sessions.list / session.watch / session.unwatch / session.start / session.send and the session events.
	Sessions = "sessions"
	// ModelConfiguration: assistant.send / assistant.warm / session.start / session.send take a launcher's model and
	// model_fingerprint (docs/assistant/model-config.md). Older nodes said so with node.info's model_configuration.
	ModelConfiguration = "model_configuration"
	// AssistantConfigDir: assistant.send / assistant.warm / session.send honor an explicit
	// config_dir and preserve the selected Claude profile when resuming a conversation.
	AssistantConfigDir = "assistant_config_dir"
var Announced = []string{ShareProjection, CreationReceipt, PersonaContracts, ManagerUpdateInventory, Sessions, ModelConfiguration, AssistantConfigDir, TaskExecution, NodeUpdate, Engines, MachineUID, HerdrServer, PluginNodeCall, Ultracode, HealthSpaces, Preview, SecretRun, Clipboard, SecretEnv, LauncherSwitch, ModelHTTP, EngineSwitch, ModelRun, LauncherSwitchNow, LauncherSwitchIdle, PaneCwd, NodeUpdateIdle, PaneNewSpace}
