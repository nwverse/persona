// Package proto holds the wire types shared by hub, node and clients. See docs/protocol/protocol.md.
package proto

import (
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/gif" // image sizes for lazy placeholders
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/dialogwire"
	"github.com/nwverse/persona/server/internal/nodeproto"
)

type Machine struct {
	ID string `json:"id"`
	// Manager is the manager it belongs to (docs/assistant/managers.md, "Ownership"); the hub stamps it on what it sends the apps.
	Manager string `json:"manager,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // local | ssh | tailcat | link
	Icon    string `json:"icon"` // server | laptop | desktop
	SSHHost string `json:"ssh_host,omitempty"`
	Online  bool   `json:"online"`
	Error   string `json:"error,omitempty"`
	// ErrorCode is what Error is about, for a client's own words (docs/assistant/onboarding.md 5.3): node_service_down,
	// link_offline, never_connected, node_outdated; "" none of them. An unknown one shows Error.
	ErrorCode string `json:"error_code,omitempty"`
	// Trust: pending while the user has not confirmed the computer is theirs (POST …/confirm); "" is confirmed.
	Trust         string    `json:"trust,omitempty"`
	Hostname      string    `json:"hostname,omitempty"`
	OS            string    `json:"os,omitempty"`
	Arch          string    `json:"arch,omitempty"`
	Home          string    `json:"home,omitempty"`
	HerdrVersion  string    `json:"herdr_version,omitempty"`
	ClaudeVersion string    `json:"claude_version,omitempty"`
	LastSeen      time.Time `json:"last_seen,omitempty"`
	// NodeVersion is the machine's personad build. NodeMismatch: the hub offers to redeploy the node, because it runs
	// another build than the hub's (deploy.sh missed it, or it was installed on its own) or because its manager cannot
	// drive it (NodeIncompatible). Another build alone is an upgrade offer, not a fault: whether the node can be used
	// is decided by its protocol and capabilities (docs/protocol/protocol.md "Node handshake").
	NodeVersion  string `json:"node_version,omitempty"`
	NodeMismatch bool   `json:"node_mismatch,omitempty"`
	// NodeProtocol is the node protocol the node announced (0: from before the handshake). NodeIncompatible is a
	// code when its manager cannot drive it (it then refuses everything but updating it): node_protocol_old (an older
	// protocol, or a required capability missing: update the node) or node_protocol_new (it needs a newer manager).
	NodeProtocol     int    `json:"node_protocol,omitempty"`
	NodeIncompatible string `json:"node_incompatible,omitempty"`
	// Deploying: the hub is installing its own node build there (POST …/deploy); DeployError: why the last one failed.
	Deploying   bool   `json:"deploying,omitempty"`
	DeployError string `json:"deploy_error,omitempty"`
	// CanDeploy is explicit so clients can distinguish an older hub without capability reporting.
	CanDeploy    *bool  `json:"can_deploy,omitempty"`
	DeployReason string `json:"deploy_reason,omitempty"`
	DeployPhase  string `json:"deploy_phase,omitempty"`
	// System: OS and hardware, from the node's last connection (kept across hub restarts).
	System *System `json:"system,omitempty"`
	// Link: kind link, while the computer's persona-link is connected (docs/operations/link.md).
	Link *MachineLink `json:"link,omitempty"`
	// Transport is how the hub reaches it now: local | ssh | tailcat | link (empty while offline); Tailcat: it can be
	// reached over tailcat (an address is set); TailcatPath, over tailcat: direct (NAT traversal worked) | relay.
	Transport   string `json:"transport,omitempty"`
	Tailcat     bool   `json:"tailcat,omitempty"`
	TailcatPath string `json:"tailcat_path,omitempty"`
	// Engine is the terminal host the machine's node drives (EngineHerdr when never chosen); Engines, what the node
	// found on the machine (empty while offline). docs/protocol/protocol.md "Engines".
	Engine  string   `json:"engine"`
	Engines []Engine `json:"engines"`
	// EngineHosts and EngineDefault are the hosts the machine enables side by side and its default host (#2576,
	// enginehosts.go): Engine is the single engine closest to them, for older clients. A machine never set has
	// [herdr]; the empty set has no default. EngineSuggested is the set to preselect when the machine is paired
	// (what the node found available, else herdr; only while online). EngineSetsUnsupported: the node is online, too
	// old for sets, and the chosen set is not one it can run as chosen — it runs Engine until it is updated.
	EngineHosts           []string      `json:"engine_hosts"`
	EngineDefault         string        `json:"engine_default"`
	EngineSuggested       *EngineChoice `json:"engine_suggested,omitempty"`
	EngineSetsUnsupported bool          `json:"engine_sets_unsupported,omitempty"`
	// Computer: operating it through Cua Driver, while the plugin cua is on and it is a Mac (Computer).
	Computer *Computer `json:"computer,omitempty"`
	// Browser: its ego lite, while the plugin ego is on and it is a Mac (Browser).
	Browser *Browser `json:"browser,omitempty"`
	// Hub mesh (docs/server/mesh.md): the hub that connects to this machine (its owner), its name, the machine's own id
	// (~/.persona/machine-uid, empty for an old node or one never reached) and how the hub the client talks to gets
	// there. A hub without peers fills them in for its own machines too.
	HubID      string        `json:"hub_id,omitempty"`
	HubName    string        `json:"hub_name,omitempty"`
	MachineUID string        `json:"machine_uid,omitempty"`
	Route      *MachineRoute `json:"route,omitempty"`
	// Paths: every way the center has to this computer (docs/archive/operations/plan-hub-center.md 5), when there is more than one or it
	// is a member hub's; the machine detail and the admin page show them.
	Paths []MachinePath `json:"paths,omitempty"`
}

// MachineRoute is how the center reaches a machine now (docs/archive/operations/plan-hub-center.md 5): via one of its own ways (local |
// service | link | ssh | tailcat, hops 0) or through a member hub (hub, hops 1); RTTMS is the node's round trip
// when it was measured.
type MachineRoute struct {
	Via   string `json:"via"`
	Hops  int    `json:"hops"`
	RTTMS *int   `json:"rtt_ms,omitempty"`
}

// MachinePath is one way to a machine: how, through which hub (the center itself for its own), whether it can be used
// now, its last round trip, and whether it is the one in use.
type MachinePath struct {
	Via    string `json:"via"`
	HubID  string `json:"hub_id"`
	OK     bool   `json:"ok"`
	RTTMS  int    `json:"rtt_ms,omitempty"`
	Active bool   `json:"active,omitempty"`
}

// MachineLink is a live persona-link: its build, the hub URL it went to and since when.
type MachineLink struct {
	Version string    `json:"version,omitempty"`
	Address string    `json:"address,omitempty"`
	Since   time.Time `json:"since"`
}

// Terminal engines: the terminal host(s) a machine's node drives, chosen per machine. EngineHerdrBoth runs classic
// herdr and herdr-core side by side (docs/integrations/engines.md "Side by side"): a machine used both locally and through the Herdr
// app's remote sessions.
const (
	EngineHerdr     = "herdr"      // the classic herdr CLI
	EngineHerdrCore = "herdr-core" // the runtime of the Herdr desktop app
	EngineHerdrBoth = "herdr-both" // classic herdr and herdr-core together
	EngineOrca      = "orca"       // the Orca desktop app's runtime
	// EnginePaseo is not a terminal: the Paseo daemon's agents, through its runtime (node/runtimecontrol.go,
	// docs/integrations/paseo.md). New sessions and sessions to continue go to Paseo.
	EnginePaseo = "paseo"
)

// EngineNames are the engines a machine may use, with the names the app shows.
var EngineNames = map[string]string{
	EngineHerdr:     "herdr",
	EngineHerdrCore: "Herdr App",
	EngineHerdrBoth: "herdr + Herdr App",
	EngineOrca:      "Orca",
	EnginePaseo:     "Paseo",
}

// Engine is one engine as found on a machine: Available when its socket is reachable (herdr: or its binary is there).
// Note says why one that is there cannot be used (Orca too old or too new), in Chinese for the app.
type Engine struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Computer is a Mac the assistant may operate through Cua Driver (the built-in plugin cua, docs/extensions/computer-use.md). In
// proto.Machine only while the plugin is on, and only for a Mac. Enabled: the user turned it on for this machine; the
// rest is what the node found when last asked (computer.status), and only for an enabled one.
type Computer struct {
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// Daemon: Cua Driver's daemon (CuaDriver.app) runs; nothing works without it.
	Daemon bool `json:"daemon"`
	// Accessibility, ScreenRecording: the daemon's macOS permissions, granted | missing | unknown.
	Accessibility   string `json:"accessibility,omitempty"`
	ScreenRecording string `json:"screen_recording,omitempty"`
	// Desktop: active (this user's desktop is on screen) | locked (its screen is locked) | away (logged out, or another
	// user is on the console). Cua Driver needs it active.
	Desktop string `json:"desktop,omitempty"`
	// ScreenSharing: macOS's Screen Sharing answers on 127.0.0.1:5900, so the app can show this Mac's live screen
	// (docs/extensions/computer-use.md "Live view"). Not needed to operate it.
	ScreenSharing bool `json:"screen_sharing"`
	// NoAutoLock: "Don't auto-lock while the extension is on" is on for this Mac (the hub's, from the plugin's settings).
	NoAutoLock bool `json:"no_auto_lock,omitempty"`
	// Backend is which driver this status is for: "cua" (Cua Driver) or "lcu" (amontlabs/lcu). Empty from a hub that
	// only knows Cua Driver.
	Backend string `json:"backend,omitempty"`
	// ChatGPT: the official ChatGPT desktop app is installed. LCU needs it for the runtime; Cua Driver does not.
	ChatGPT bool `json:"chatgpt,omitempty"`
	// Ready: all of the above in place. Note says what is missing and what to do, in Chinese for the app.
	Ready   bool      `json:"ready"`
	Note    string    `json:"note,omitempty"`
	Checked time.Time `json:"checked,omitempty"`
	// Job: installing Cua Driver or asking for its permissions from the app (computer.install / computer.grant),
	// the one running or the last one since the node started.
	Job *ComputerJob `json:"job,omitempty"`
}

// Browser is a Mac's ego lite, the agent browser the assistant may drive (the built-in plugin ego,
// docs/plans/plan-ego-browser.md). In proto.Machine only while the plugin is on, and only for a Mac; Enabled: the user turned
// it on for this machine. The rest is what the node found when last asked (browser.status).
type Browser struct {
	Enabled bool `json:"enabled"`
	// Installed: /Applications/ego lite.app is there; Version, its version.
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// SetUp: its first-run setup is done (it registers ego-browser and records it in its Local State); Imported: that
	// setup imported another browser's data (sign-ins, cookies…).
	SetUp    bool `json:"set_up"`
	Imported bool `json:"imported"`
	// Analytics: Chromium's usage statistics, on | off | unknown.
	Analytics string `json:"analytics,omitempty"`
	// Ready: a read-only call through ego-browser worked a moment ago. Note: what is missing, in Chinese for the app.
	Ready         bool         `json:"ready"`
	Note          string       `json:"note,omitempty"`
	ScreenSharing bool         `json:"screen_sharing"`
	Job           *ComputerJob `json:"job,omitempty"`
	Checked       time.Time    `json:"checked,omitempty"`
}

// BrowserCookie is one cookie imported into ego lite's default profile (hub → node browser.cookies.import, #1304). The
// client parses the file the user picked (cookies.txt or an extension's JSON) into these; the hub passes them on and
// keeps nothing. Expires is Unix seconds, 0 for a session cookie; HostOnly: Domain is the host itself (no leading dot).
type BrowserCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	HTTPOnly bool    `json:"http_only,omitempty"`
	SameSite string  `json:"same_site,omitempty"` // Strict | Lax | None
	HostOnly bool    `json:"host_only,omitempty"`
}

// BrowserCookieSite is a site with cookies in ego lite's profile (browser.cookies.list): never a value or a name.
type BrowserCookieSite struct {
	Domain  string    `json:"domain"` // the registrable part shown to the user: example.com for .example.com and www.example.com
	Count   int       `json:"count"`
	Expires time.Time `json:"expires,omitzero"` // the latest, zero when all are session cookies
}

// BrowserCookies answers browser.cookies.*: the sites now (after an import or a clear), and what was done.
type BrowserCookies struct {
	Sites    []BrowserCookieSite `json:"sites"`
	Imported int                 `json:"imported,omitempty"`
	Skipped  int                 `json:"skipped,omitempty"` // expired or malformed
	Removed  int                 `json:"removed,omitempty"`
}

// ComputerJob is one install or grant the node runs in the background (docs/extensions/computer-use.md "One-click install").
type ComputerJob struct {
	Kind  string `json:"kind"`  // install | grant | open
	State string `json:"state"` // running | done | failed | cancelled
	// Step: what it does now, in Chinese for the app; Error: why it failed (the installer's last lines).
	Step    string    `json:"step,omitempty"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started"`
}

// ComputerResult is what one Cua Driver tool answered (node computer.call): its JSON (Output), or Text when it was not
// JSON, and the screenshot it took, if any.
type ComputerResult struct {
	Output json.RawMessage `json:"output,omitempty"`
	Text   string          `json:"text,omitempty"`
	Image  *Image          `json:"image,omitempty"`
}

// Protocol is the app <-> hub protocol's major version: it goes up only for a change an older app cannot live with
// (features cover everything finer). MinAppBuild is the oldest app build (CFBundleVersion) this hub works with; 0:
// any. Both are in /api/v1/health and the WebSocket hello (docs/operations/hub-update.md "Compatibility").
const (
	Protocol    = 1
	MinAppBuild = 0
)

// Features are what this hub supports beyond the first protocol, listed in the WebSocket hello and /health for
// clients that need to know before they try (docs/protocol/protocol.md).
var Features = []string{
	nodeproto.PersonaContracts, // bidirectional pre-cutover runtime identities
	"locale",                   // Accept-Language, client_hello.locale and a device's locale pick the language of text for people (docs/protocol/i18n.md)
	"assistant_compact",        // POST /api/v1/assistant/conversations/{id}/compact, session.compaction
	"assistant_main_new",       // POST /api/v1/assistant/main/new: archive the main conversation, the next message starts one
	"assistant_readiness",      // GET /api/v1/assistant/readiness, …/readiness/dismiss, warm's readiness (docs/assistant/onboarding.md 5.1)
	"android_update",           // authenticated Android release metadata and APK downloads
	"error_codes",              // stable HTTP error codes and optional delivery information
	"client_hello",             // client platform, version and capabilities
	"heartbeat",                // per-connection heartbeat negotiation
	"app_ping",                 // application ping/pong with a string id
	"ws_notify",                // notification alerts for capable connected clients
	"elicitation",              // MCP forms and URLs for opted-in clients
	"lock_test",                // scheduled device notification tests with durable receipts
	"ultracode",                // session-only Claude Code workflow switch, queued changes and resume-loss notices
	"model_configuration",      // unified per-purpose models and provider selection
	"client_msg_id",            // session.send dedupes retries
	"client_ops",               // durable operation receipts and read-only status queries
	"send_refusals",            // 409 dialog_open / input_draft, clear_draft
	"screen_dialog",            // interaction.dialog, Resolution.option, 409 prompt_changed
	"resumable_interactions",   // hook questions survive hub / node restarts
	"secret_prompts",           // interaction.secret
	"request_id",               // X-Request-Id
	"terminal_stream",          // WebSocket terminal_subscribe: the pane's live frames (docs/integrations/terminal.md)
	"terminal_control",         // terminal_subscribe with control: the phone drives the terminal at its size; terminal_input
	"terminal_scroll",          // terminal_input with scroll: the wheel over a terminal the app drives, to herdr (docs/integrations/terminal.md)
	"terminal_click",           // terminal_input with click: a tap on a terminal the app drives, a left click there (docs/integrations/terminal.md)
	"terminal_mouse",           // terminal_input with mouse: press / release / drag / move, a button and modifiers at a cell (docs/integrations/terminal.md "Mouse")
	"herdr_client",             // WebSocket herdr_open / herdr_input / herdr_resize / herdr_ack / herdr_close: herdr's own client in a PTY for the web, full devices only (docs/integrations/terminal.md "Full herdr view")
	"herdr_focus",              // WebSocket herdr_focus (browser -> hub) and herdr_focus (hub -> browser): the full herdr view moves herdr's focus and follows it, full devices only (docs/integrations/terminal.md "Linked focus")
	"terminal_panes",           // GET/POST /api/v1/machines/{mid}/panes, terminal ids "pane:<id>": any pane as a terminal (docs/integrations/terminal.md "Standalone terminals")
	"file_download",            // GET /api/v1/machines/{mid}/file/download
	"file_stream",              // file?stream=1 (binary described, bytes by Range), file/download?inline=1, sniffed MIME
	"file_thumb",               // GET /api/v1/machines/{mid}/file/thumb (docs/protocol/protocol.md "File preview")
	"file_convert",             // GET /api/v1/machines/{mid}/file/convert: LibreOffice → PDF on the machine
	"git_blob",                 // GET /api/v1/machines/{mid}/git/blob: a file at a version, for comparing
	"git_write",                // the Git panel's writes and the branch's pull request under /api/v1/machines/{mid}/git/ (#6121)
	"file_upload",              // POST /api/v1/machines/{mid}/files (docs/protocol/protocol.md "Sending files")
	"machine_trust",            // machines' trust and error_code, POST /api/v1/machines/{id}/confirm|reject (docs/assistant/onboarding.md 5.3)
	"machines_add",             // POST /api/v1/auth/pair-codes with add / machine from a signed-in device: it adds a computer and pairs it (docs/assistant/onboarding.md 5.3)
	"todo_threads",             // a to-do's chat thread: PUT/DELETE /api/v1/todos/{id}/thread, and `thread` on the to-dos GET returns (docs/assistant/todos.md "Chat thread")
	"tls",                      // optional TLS listener and SPKI pin (hub-changes.md 1.1)
	"endpoints",                // authenticated address discovery and capability-gated updates
	"plugins",                  // GET /api/v1/plugins and the relay under /api/v1/plugins/{id}/ (docs/extensions/extensions.md)
	"plugin_manager",           // a plugin's detail, form, settings, secrets and switch under /api/v1/plugins/{id}/-/ (docs/extensions/extensions.md)
	"plugin_ui_slots",          // plugins' UI slots (nav_page, overview_card, machine_section), UI version 2, plugin_ui events (docs/extensions/extensions.md "UI slots")
	"extension_operations",     // installs, upgrades and uninstalls as operations with steps: /api/v1/extensions/-/operations, extension_operation events, async: true (docs/protocol/protocol.md "Extension install, upgrade and uninstall progress")
	"extension_auth",           // extensions that sign in (manifest.auth): /api/v1/extensions/{id}/-/auth…, extension_auth events, the tools install step (docs/extensions/extensions.md "Sign-in (auth)")
	"plugin_auto_upgrade",      // market plugins upgrade themselves: /api/v1/plugins/-/upgrades, …/{id}/-/auto-upgrade, plugin_upgrade events (docs/extensions/extensions.md "Auto-upgrade")
	// The same under the new names (docs/extensions/extensions.md "Naming and compatibility", #1844): /api/v1/extensions/… answering with the
	// keys extensions / extension / extension_name, extension_ui and extension_upgrade events. The plugin ones above
	// stay for the apps out there (until at least 2027-05-05).
	"extensions",               // GET /api/v1/extensions and the relay under /api/v1/extensions/{id}/
	"extension_manager",        // an extension's detail, form, settings, secrets and switch under /api/v1/extensions/{id}/-/
	"extension_ui_slots",       // UI slots under /api/v1/extensions/-/ui, extension_ui events
	"extension_auto_upgrade",   // /api/v1/extensions/-/upgrades, …/{id}/-/auto-upgrade, extension_upgrade events
	"extension_market_spec",    // PUT /api/v1/extensions/-/markets/{id} {spec}: a market moves, keeping its id
	"extension_ui_v3",          // UI version 3 (markdown, table, empty, loading, search, filter, more, push / pop) and extension_ui {invalidate} (docs/extensions/extensions.md "UI version 3")
	"extension_widgets",        // widgets outside the app: GET /api/v1/extensions/-/widgets, POST …/{id}/-/widget/{ui}, PUT /api/v1/devices/{id}/widgets, extension_widget pushes (docs/extensions/extensions.md "System-level places")
	"extension_shortcuts",      // extensions' actions in Shortcuts and Siri: GET /api/v1/extensions/-/shortcuts, POST …/{id}/-/shortcut/{sid} (docs/extensions/extensions.md "System-level places")
	"extension_notify_actions", // notification buttons: POST /api/v1/notify actions / ui / state, aps.category and extension_notification (docs/extensions/extensions.md "System-level places")
	"desktops",                 // register_desktop, the desktops event, NotifyPrefs.mute_while_desktop ("Mute phone")
	"sessions_delta",           // ?features=sessions_delta on the WebSocket: a snapshot, then sessions_delta (docs/protocol/protocol.md "Session deltas")
	"connectors",               // GET /api/v1/connectors (docs/plans/plan-connectors.md)
	"channels",                 // GET /api/v1/channels and each channel's settings, binding, test (docs/extensions/channels.md)
	"runtime_cli",              // phone_runtime_cli kept out of the phone's tools, POST /api/v1/phone/runtime-cli (docs/operations/runtime-cli.md)
	"conversation_origin",      // Session.origin on assistant conversations (docs/protocol/protocol.md "Origin of assistant conversations")
	"builtin_presets",          // the hub's own provider presets in GET /api/v1/launchers/presets; POST /api/v1/launchers with a preset takes an empty name, display_name and assign (docs/integrations/launchers.md "Built-in presets")
	"upload_dir",               // POST /api/v1/machines/{mid}/files?dest=dir&dir=&conflict=: into a folder of the home (docs/protocol/protocol.md "Changing files")
	"task_push",                // Task.push on /api/v1/assistant/tasks: "Push when done", every run or only what a run sends (docs/assistant/assistant.md "Push when done")
}

// Files sent from the phone (POST /api/v1/machines/{mid}/files): the hub streams the body to the node in chunks.
const (
	MaxUploadBytes   = 200 << 20
	UploadChunkBytes = 1 << 20
	// A file pasted or dropped into a web terminal (dest=terminal) lands under the node's Persona directory and is
	// smaller: what an agent reads by path is a screenshot or a document, not a video.
	UploadDestTerminal     = "terminal"
	MaxTerminalUploadBytes = 50 << 20
	// A checkup report or lab result for the health space (dest=health, docs/assistant/health.md "Checkup reports"): it lands in the
	// health directory's reports/ and stays, where the health conversations read it and the others are denied.
	UploadDestHealth = "health"
	// A file put into a folder the app is browsing (dest=dir&dir=…, docs/protocol/protocol.md "Changing files"): it keeps its own
	// name, only folders in the home take it (node/writeguard.go), and it may be bigger.
	UploadDestDir     = "dir"
	MaxDirUploadBytes = 2 << 30
	// What dest=dir does when the name is taken: "" refuses with CodeExists (409, the app asks), replace overwrites
	// the file, keep puts it beside as "name 2.ext".
	UploadConflictReplace = "replace"
	UploadConflictKeep    = "keep"
	// CodeReadOnly: the path is outside what the phone may change (409); CodeExists: the name is taken (409, name).
	CodeReadOnly = "read_only"
	CodeExists   = "exists"
	// CodeTooLarge: over MaxUploadBytes (413); CodeNodeOutdated: the machine's node predates the feature (409).
	CodeTooLarge     = "too_large"
	CodeNodeOutdated = "node_outdated"
)

// Codes of a failed credential.gh_token (hub → node, node/credential.go).
const (
	CodeNoGh        = "no_gh"
	CodeNotSignedIn = "not_signed_in"
)

// Codes of a failed connector.chats / connector.older (hub → node, node/connectors.go): macOS does not let the
// node read the connector's data (Full Disk Access), or no conversation has that id.
const (
	CodeNoPermission = "no_permission"
	CodeNoChat       = "no_chat"
)

// UploadedFile is where an upload landed on the machine.
type UploadedFile struct {
	Path string `json:"path"` // absolute
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// HealthReport is one report in the health space's reports/ (docs/assistant/health.md"Health reports"): an original, its record, or both.
type HealthReport struct {
	Name       string    `json:"name"`                  // the file: the key to delete it
	Title      string    `json:"title"`                 // the name without the upload's time
	Path       string    `json:"path,omitempty"`        // the original; empty for a record alone
	RecordPath string    `json:"record_path,omitempty"` // the .md the health conversation wrote; empty until it has
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// HealthReports is health.reports.list's answer, the newest first.
type HealthReports struct {
	Reports []HealthReport `json:"reports"`
	// HealthDir is the health space they are of when the request named one (another manager's, health-<id>).
	HealthDir string `json:"health_dir,omitempty"`
}

type NodeInfo struct {
	// Protocol, ProtocolMin and Capabilities are the node protocol handshake (internal/nodeproto, docs/protocol/protocol.md
	// "Node handshake"): the node protocol it speaks, the oldest manager protocol it answers, and what it can do.
	// Nodes from before the handshake leave all three out (protocol 0); managers from before it ignore them.
	Protocol           int      `json:"protocol,omitempty"`
	ProtocolMin        int      `json:"protocol_min,omitempty"`
	Capabilities       []string `json:"capabilities,omitempty"`
	NodeUpdate         bool     `json:"node_update,omitempty"`
	NodeUpdateReason   string   `json:"node_update_reason,omitempty"`
	ModelConfiguration bool     `json:"model_configuration,omitempty"`
	Hostname           string   `json:"hostname"`
	OS                 string   `json:"os"`
	Arch               string   `json:"arch"`
	Home               string   `json:"home"`
	DataDir            string   `json:"data_dir,omitempty"`
	HerdrVersion       string   `json:"herdr_version"`
	ClaudeVersion      string   `json:"claude_version"`
	Version            string   `json:"version"`
	System             *System  `json:"system,omitempty"`
	// Program is the program the node runs as (nodeproto.ProgramPersonad / ProgramPersonaNode) and Executable the file
	// it runs from: an update brings the same kind of program, to that file. Older nodes leave both out (personad, at the
	// machine's remote_bin).
	Program    string `json:"program,omitempty"`
	Executable string `json:"executable,omitempty"`
	// Engine is the engine the node drives now; Engines, what it found (proto.Machine.Engines). Older nodes leave
	// both out.
	Engine  string   `json:"engine,omitempty"`
	Engines []Engine `json:"engines,omitempty"`
	// EngineHosts is the set of hosts the node drives now (enginehosts.go): reported by nodes that take a set in
	// engine.use (its hosts and default fields), left out by older ones — the hub then sends them only the engine.
	EngineHosts *EngineChoice `json:"engine_hosts,omitempty"`
	// MachineUID is the computer's own id (~/.persona/machine-uid, a UUIDv4 made on the node's first run): hubs that
	// both reach it tell it is the same computer (docs/server/mesh.md). Older nodes leave it out.
	MachineUID string `json:"machine_uid,omitempty"`
	// HerdrRunning and HerdrRunningKnown: whether classic herdr's server answers on this machine now, so the Agents
	// list can offer "Start" instead of waiting for a start to fail (docs/integrations/engines.md). An older node sets neither, and
	// the hub then tells "it does not run" apart from "nobody said".
	HerdrRunning      bool `json:"herdr_running,omitempty"`
	HerdrRunningKnown bool `json:"herdr_running_known,omitempty"`
	// BuiltinPrograms are the extensions' programs built into this node (cua, ego): the hub asks them over
	// plugin.node.call (node/builtin.go). An older node leaves it out, and the hub asks it by the old method names.
	BuiltinPrograms []string `json:"builtin_programs,omitempty"`
}

// System is what a machine is: OS, hardware, since when it runs (sysmon; docs/server/machines.md). Read once when the node
// starts and sent with node.info; the hub keeps the last one of every machine in Postgres.
type System struct {
	OS       string `json:"os"`                  // runtime.GOOS: linux | darwin
	Distro   string `json:"distro"`              // os-release ID (ubuntu, debian, fedora…), "macos"
	Like     string `json:"like,omitempty"`      // os-release ID_LIKE ("ubuntu debian" for Pop!_OS): for the icon
	Name     string `json:"name"`                // "Ubuntu 24.04.1 LTS", "macOS 26.6.2"
	Version  string `json:"version"`             // "24.04", "26.6.2"
	Build    string `json:"build,omitempty"`     // macOS build ("25G83")
	Kernel   string `json:"kernel"`              // "6.8.0-45-generic", "Darwin 25.6.0"
	Arch     string `json:"arch"`                // amd64 | arm64
	Model    string `json:"model,omitempty"`     // "Mac Studio", a PC's DMI product name
	CPU      string `json:"cpu,omitempty"`       // "Apple M1 Ultra", "AMD Ryzen 9 7950X"
	Cores    int    `json:"cores"`               // logical CPUs
	Memory   int64  `json:"memory"`              // bytes
	BootTime int64  `json:"boot_time,omitempty"` // Unix seconds
}

// MetricSample is one reading of a machine, every MetricEvery (sysmon; docs/server/machines.md "What is measured"). A nil field
// was not measured (the first sample has no CPU or network rate: those need two readings).
type MetricSample struct {
	T         int64     `json:"t"`               // Unix seconds
	CPU       *float64  `json:"cpu,omitempty"`   // busy %, all cores (0–100)
	Cores     []float64 `json:"cores,omitempty"` // busy % per core (Linux, Windows; the latest sample only, not kept)
	Load      *float64  `json:"load,omitempty"`  // 1-minute load average
	MemUsed   int64     `json:"mem_used"`        // bytes
	MemTotal  int64     `json:"mem_total"`
	SwapUsed  int64     `json:"swap_used"`
	SwapTotal int64     `json:"swap_total"`
	NetRx     *float64  `json:"net_rx,omitempty"` // bytes/s received on physical interfaces
	NetTx     *float64  `json:"net_tx,omitempty"` // bytes/s sent
	DiskUsed  int64     `json:"disk_used"`        // the system disk (/, or the macOS data volume), like df
	DiskTotal int64     `json:"disk_total"`       // used + available
}

// MetricPoint is one step of a machine's history (GET /api/v1/machines/{id}/metrics): the average over the step,
// or a break (Gap) where the machine sent nothing, which charts must not bridge. Memory, swap and disk are bytes used.
type MetricPoint struct {
	T    int64    `json:"t"` // Unix seconds, the step's start
	Gap  bool     `json:"gap,omitempty"`
	CPU  *float64 `json:"cpu,omitempty"`
	Load *float64 `json:"load,omitempty"`
	Mem  *float64 `json:"mem,omitempty"`
	Swap *float64 `json:"swap,omitempty"`
	Rx   *float64 `json:"rx,omitempty"`
	Tx   *float64 `json:"tx,omitempty"`
	Disk *float64 `json:"disk,omitempty"`
}

// MetricEvery is how often a node samples (and the finest step of the history).
const MetricEvery = 10 * time.Second

// Claude Code's effort levels and permission modes (node/modes.go sets them; the hub checks a scheduled task's).
var (
	EffortLevels    = []string{"low", "medium", "high", "xhigh", "max"}
	PermissionModes = []string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"}
)

// ModeBypass skips every permission prompt; a headless claude only takes it at start (--permission-mode).
const ModeBypass = "bypassPermissions"

type Launcher struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// Source: "zshrc" (a function in the machine's ~/.zshrc), "path" (a wrapper script on its PATH, node/pathlaunchers.go) or "persona" (managed in Settings › Models, the manager's assistant/models_launchers.go). Empty
	// from an older node: zshrc.
	Source string `json:"source,omitempty"`
	// Agent is what it starts when not Claude Code: "codex" (plain codex, or an alias running it; docs/integrations/codex.md
	// "Starting from the phone"). A Codex start needs its first message with it.
	Agent string `json:"agent,omitempty"`
	// Assistant: a runtime's launcher that may run the Persona assistant (docs/integrations/runtimes.md "Assistant"), whatever its agent.
	Assistant bool `json:"assistant,omitempty"`
}

// LauncherDetail is what a ~/.zshrc launcher really sets (the node runs it with the agent stubbed out,
// node/launcherprobe.go): every field the page shows; secrets are never in it.
type LauncherDetail struct {
	Agent         string          `json:"agent"` // claude, codex, traex
	BaseURL       string          `json:"base_url,omitempty"`
	Auth          string          `json:"auth,omitempty"` // which variable carries the key (its value is not here)
	Models        []LauncherModel `json:"models,omitempty"`
	ContextWindow int             `json:"context_window,omitempty"`
	CompactWindow int             `json:"compact_window,omitempty"` // when it differs from the window
	Effort        string          `json:"effort,omitempty"`
	TimeoutMS     int             `json:"timeout_ms,omitempty"`
	Args          []string        `json:"args,omitempty"`
	Env           []LauncherEnv   `json:"env,omitempty"` // the other settings, secrets as •••
	Error         string          `json:"error,omitempty"`
}

// LauncherCheck is one "Test" run: a real one-word request through the launcher.
type LauncherCheck struct {
	OK     bool   `json:"ok"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"` // which model answered
	Answer string `json:"answer,omitempty"`
	Millis int    `json:"millis"`
	Error  string `json:"error,omitempty"`
}

// ClaudeAuthStatus is what a node's claude.auth_status found: `claude auth status --json` run as the launcher runs
// claude (docs/assistant/onboarding.md 4.3). It spends no tokens. Only these fields leave the machine: never the account's
// email or organization, never a key (KeySource names where a key came from, e.g. ANTHROPIC_API_KEY).
type ClaudeAuthStatus struct {
	State       string `json:"state"`                  // ClaudeLoggedIn, ClaudeNotLoggedIn or ClaudeAuthUnknown
	AuthMethod  string `json:"auth_method,omitempty"`  // Claude Code's authMethod: claude.ai, api_key, api_key_helper, oauth_token, third_party, none
	APIProvider string `json:"api_provider,omitempty"` // firstParty, bedrock, vertex…
	KeySource   string `json:"key_source,omitempty"`
	Version     string `json:"version,omitempty"` // Claude Code's, when known
	Reason      string `json:"reason,omitempty"`  // why it is unknown: ClaudeAuthOld, ClaudeAuthNotClaude, ClaudeAuthUnreadable, ClaudeAuthTimeout
}

// ClaudeAuthStatus states and the reasons for an unknown one.
const (
	ClaudeLoggedIn    = "logged_in"
	ClaudeNotLoggedIn = "not_logged_in"
	ClaudeAuthUnknown = "unknown"

	ClaudeAuthOld        = "old_claude" // a Claude Code before `claude auth` (2.1.41)
	ClaudeAuthNotClaude  = "not_claude" // the launcher runs Codex or a runtime
	ClaudeAuthUnreadable = "unreadable" // no status in what it printed (claude not found, a launcher that drops the arguments)
	ClaudeAuthTimeout    = "timeout"
)

// LauncherRepair is what a node did for launchers.repair (docs/server/launcher-health.md): one of its fixed repairs.
type LauncherRepair struct {
	OK          bool   `json:"ok"`
	Unsupported bool   `json:"unsupported,omitempty"` // this repair does not apply to the launcher's agent
	Output      string `json:"output,omitempty"`      // the command's last lines, secrets as •••
}

type LauncherModel struct {
	Role  string `json:"role"` // main model, Opus, small model, subagent… (in Chinese)
	Env   string `json:"env"`
	Model string `json:"model"`
}

// LauncherModels is what a launcher's agent offers in its /model menu, for "New agent"'s model picker
// (node/launcher_models.go). No models: its agent has no list to ask for (a compatible CLI, a runtime's).
type LauncherModels struct {
	Agent  string       `json:"agent"`
	Models []AgentModel `json:"models"`
	// Locked: why a model picked here would not reach the agent (the launcher fixes its own --model); empty: it would.
	Locked string `json:"locked,omitempty"`
}

// AgentModel is one model of the list: Claude Code's initialize, Codex's model/list.
type AgentModel struct {
	ID          string `json:"id"` // what --model takes (Claude's aliases too: opus, sonnet…)
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Resolved: the model an alias stands for with this launcher's settings (opus → its model id).
	Resolved string `json:"resolved,omitempty"`
	// Default: what the agent runs when no model is given; picking it is the same as picking none.
	Default bool `json:"default,omitempty"`
	// Efforts: the thinking efforts it takes, weakest first; none: no effort setting.
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
}

type LauncherEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ManagedLauncher is a launcher kept by the hub (Settings › Models): what `personad launch <name>` sets before it runs claude.
// The key only goes to nodes (in memory), never to the app.
type ManagedLauncher struct {
	Kind       string `json:"kind,omitempty"`  // launcher (default) or api
	Agent      string `json:"agent,omitempty"` // claude (default), codex, or agy
	API        string `json:"api,omitempty"`   // messages or responses for direct API providers
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	SmallModel string `json:"small_model,omitempty"`
	// SubagentModel is CLAUDE_CODE_SUBAGENT_MODEL; "" keeps the model (the old behaviour).
	SubagentModel string `json:"subagent_model,omitempty"`
	// CustomModel is the gateway's /model extra entry (ANTHROPIC_CUSTOM_MODEL_OPTION) and how it reads.
	CustomModel            string `json:"custom_model,omitempty"`
	CustomModelName        string `json:"custom_model_name,omitempty"`
	CustomModelDescription string `json:"custom_model_description,omitempty"`
	// Args go before the caller's (e.g. --dangerously-skip-permissions).
	Args   []string `json:"args,omitempty"`
	APIKey string   `json:"api_key,omitempty"`
	// Auth is how the key goes to the gateway: "api_key" (x-api-key, ANTHROPIC_API_KEY; the default) or "bearer"
	// (Authorization: Bearer, ANTHROPIC_AUTH_TOKEN), for gateways that take only that.
	Auth string `json:"auth,omitempty"`

	// The provider as cindy models it (#2455): what is known of each model the gateway serves, the model per Claude
	// Code tier, and the gateway's own needs. All optional; an older launcher reads as before.
	//
	// Models: the gateway's models with their context window and thinking levels (the first is not the default; Model
	// is). A model whose window is over 200K runs as "<id>[1m]" (CLI and SDK), its compact window capped to it.
	Models []ProviderModel `json:"models,omitempty"`
	// The model of each Claude Code tier (ANTHROPIC_DEFAULT_*_MODEL); "" is Model.
	OpusModel   string `json:"opus_model,omitempty"`
	SonnetModel string `json:"sonnet_model,omitempty"`
	HaikuModel  string `json:"haiku_model,omitempty"`
	FableModel  string `json:"fable_model,omitempty"`
	// SubagentForce: CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1 (the subagent model wins over an agent's own model:).
	SubagentForce bool `json:"subagent_force,omitempty"`
	// Headers: ANTHROPIC_CUSTOM_HEADERS, one "Name: value" a line. A secret like the key: sealed at rest, never sent
	// back to clients (they see HasHeaders).
	Headers    string `json:"headers,omitempty"`
	HasHeaders bool   `json:"has_headers,omitempty"`
	// Proxy / NoProxy: HTTP_PROXY and HTTPS_PROXY / NO_PROXY for the gateway.
	Proxy   string `json:"proxy,omitempty"`
	NoProxy string `json:"no_proxy,omitempty"`
	// Env: more environment for the launch, one NAME=value a line (#5953), set last: it wins over what Persona sets,
	// the shell's environment and the user's settings env. A secret like the key: sealed at rest, never sent back to
	// clients (they see HasEnv).
	Env    string `json:"env,omitempty"`
	HasEnv bool   `json:"has_env,omitempty"`
	// TimeoutMS: API_TIMEOUT_MS; 0 is 3000000 for a gateway (BaseURL set), Claude Code's own default otherwise.
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// CompactWindow: CLAUDE_CODE_AUTO_COMPACT_WINDOW; 0 is the default model's context window (none when unknown).
	// The main conversation keeps its own 200000 whatever this says.
	CompactWindow int `json:"compact_window,omitempty"`
	// MaxContext: CLAUDE_CODE_MAX_CONTEXT_TOKENS; 0 is the default model's context window when it is over 200K.
	MaxContext int `json:"max_context,omitempty"`
	// Effort: the default thinking level (CLAUDE_CODE_EFFORT_LEVEL) when the caller gives none.
	Effort string `json:"effort,omitempty"`
	// Source: where it came from: "" (made by hand), "zshrc:<alias>@<machine id>" (imported), "preset:<id>".
	Source string `json:"source,omitempty"`
}

// ProviderModel is one model a provider's gateway serves (cindy's ProviderRuntimeModelConfig, the Anthropic part).
type ProviderModel struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// ContextWindow in tokens; 0 is unknown (Claude Code then assumes 200K).
	ContextWindow int `json:"context_window,omitempty"`
	// Efforts: the thinking levels it takes (low, medium, high, xhigh, max); empty is unknown (all are offered).
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
}

// The assistant's runtimes for a Claude launcher (model_config.assistant's "runtime", the assistant.send / .start /
// .warm params' "runtime"): the Claude Code CLI in stream-json mode (the default), or the Claude Agent SDK through the
// node's Node bridge (integrations/agent-sdk, #2455), which falls back to the CLI where no Node 22+ is found.
const (
	AssistantRuntimeCLI      = "cli"
	AssistantRuntimeAgentSDK = "agentsdk"
)

// Why an assistant conversation that asked for the Agent SDK runs on the CLI (Session.RuntimeFallback), as the node
// writes it: in Chinese, the ones ending in a full-width colon followed by the cause, FallbackShellLauncher with the launcher's name.
// The hub rewrites them in the app's language (hub/transcript_text.go runtimeFallbackText).
const (
	FallbackShellLauncher  = "启动器 %s 是这台电脑上的 shell 命令，Agent SDK 用不了它的设置"
	FallbackReadBridge     = "读不到 Agent SDK 桥："
	FallbackNoBridge       = "这个 personad 没有带 Agent SDK 桥"
	FallbackWriteBridge    = "写不出 Agent SDK 桥："
	FallbackNoBridgeScript = "Agent SDK 桥的包里没有 bridge.mjs"
	FallbackNoNode         = "没有 Node.js 22 及以上："
	FallbackNoClaude       = "找不到 claude："
)

// BearerAuth: the launcher's key goes as Authorization: Bearer (ANTHROPIC_AUTH_TOKEN) rather than x-api-key.
func (l ManagedLauncher) BearerAuth() bool { return l.Auth == "bearer" }

// AgentType is the agent CLI this managed launcher runs ("claude" by default, or "codex" / "agy").
func (l ManagedLauncher) AgentType() string {
	if l.Agent != "" {
		return l.Agent
	}
	return "claude"
}

type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsGit bool   `json:"is_git"`
}

type RecentDir struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	LastUsed time.Time `json:"last_used"`
}

type DirList struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent"`
	Entries []DirEntry  `json:"entries"`
	Recent  []RecentDir `json:"recent"`
}

// Session statuses mirror herdr agent states, plus offline for transcripts with no live agent.
const (
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
	StatusUnknown = "unknown"
	StatusOffline = "offline"
	// StatusInterrupted: it was running in a pane when the node restarted, which took herdr's panes with it. The
	// conversation is still resumable (session.start with resume) but nothing carries it on until the user says so
	// (#1917, interrupted.go in the node).
	StatusInterrupted = "interrupted"
)

// Unified agent states (docs/protocol/agent-states.md).
const (
	StateNeedsYou = "needs_you" // waiting for the user: a question, plan, permission or any TUI dialog
	StateWorking  = "working"
	StateUnread   = "unread" // finished, result not looked at yet
	StateIdle     = "idle"   // finished and seen, ready for input
	StateOffline  = "offline"
	StateUnknown  = "unknown"
)

type Session struct {
	// Manager is the manager it belongs to (docs/assistant/managers.md, "Ownership"); the hub stamps it on what it sends the apps.
	Manager          string               `json:"manager,omitempty"`
	MachineID        string               `json:"machine_id"`
	SessionID        string               `json:"session_id"`
	SourceKind       string               `json:"source_kind,omitempty"`
	SourceThreadID   string               `json:"source_thread_id,omitempty"`
	SourceInstanceID string               `json:"source_instance_id,omitempty"`
	ParentThreadID   string               `json:"parent_thread_id,omitempty"`
	ParentSessionID  string               `json:"parent_session_id,omitempty"` // Public ID scoped to the Codex home on this machine.
	RootSessionID    string               `json:"root_session_id,omitempty"`
	IsSubagent       bool                 `json:"is_subagent,omitempty"`
	ReadOnlyReason   string               `json:"read_only_reason,omitempty"`
	Capabilities     *SessionCapabilities `json:"capabilities,omitempty"`
	Attached         bool                 `json:"attached,omitempty"`
	// CanFork: "Branch to new chat" works for it (a Claude Code conversation the node can copy, node/fork.go); older nodes
	// never say so, and the clients hide it.
	CanFork bool   `json:"can_fork,omitempty"`
	Title   string `json:"title"`
	Cwd     string `json:"cwd"`
	// WorkspaceCwd is the node-verified physical directory for Cwd. Consumers
	// matching workspace bindings prefer it and fall back to Cwd on older nodes.
	WorkspaceCwd string `json:"workspace_cwd,omitempty"`
	// RepoRoot is the main checkout of the repository Cwd belongs to, when the host knows it (#2840: the Paseo bridge,
	// whose worktrees live under Paseo's own folder): "By repo" groups by it; without it the clients infer the repository
	// from Cwd's path.
	RepoRoot    string `json:"repo_root,omitempty"`
	Status      string `json:"status"`
	PaneID      string `json:"pane_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
	// CodexRoute: how Persona reaches a Codex in a terminal (docs/integrations/codex.md "Sessions on a daemon") — "app_server" (joined on
	// the shared app-server: questions, approvals and messages go through its protocol) or "terminal" (its rollout and
	// screen, keys and typing); CodexRouteNote says why when it is the terminal.
	CodexRoute     string `json:"codex_route,omitempty"`
	CodexRouteNote string `json:"codex_route_note,omitempty"`
	// Agent is the coding agent in the pane: "claude" (also when empty, from older nodes) or "codex"; for a session a
	// runtime lists (docs/integrations/runtimes.md), the agent it says it runs ("dsh").
	Agent string `json:"agent,omitempty"`
	// AgentLabel is what the apps call the agent when they do not know it themselves: a runtime's own name ("DSH").
	// Empty for the agents the apps know (Claude Code, Codex, a compatible CLI).
	AgentLabel string `json:"agent_label,omitempty"`
	// AgentSurface is the client the agent runs in: "desktop" (Claude's or Codex's desktop app) or "cli" (its command
	// line); empty when its record does not say (an SDK, an older version, a runtime). The apps badge the agent's mark
	// with </> for "cli" (#6390).
	AgentSurface string `json:"agent_surface,omitempty"`
	Launcher     string `json:"launcher,omitempty"`
	Model        string `json:"model,omitempty"`
	// PermissionMode and Effort are what Claude Code runs with now: default / acceptEdits / plan / auto /
	// bypassPermissions, and low … max. Empty while unknown (before the first turn, Codex). A Codex thread the phone
	// drives through its app-server has an Effort too (Codex's levels, see Efforts) but never a PermissionMode.
	PermissionMode string `json:"permission_mode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	// Ultracode is Claude Code's independent session-only workflow switch: nil means unknown.
	// Pending means a requested change waits for an idle input box; LostAt asks the client to offer reopening
	// after a resumed process reset an enabled switch. Reopening or dismissing the notice clears LostAt.
	Ultracode        *bool      `json:"ultracode,omitempty"`
	UltracodeNote    string     `json:"ultracode_note,omitempty"`
	UltracodePending bool       `json:"ultracode_pending,omitempty"`
	UltracodeLostAt  *time.Time `json:"ultracode_lost_at,omitempty"`
	// ContextTokens is what the model saw in the agent's latest reply (Claude: input + cache read + cache creation;
	// Codex: the last token_count's input) and ContextWindow the model's window, both read off the transcript tail the
	// node already reads (#2430). ContextTokens is omitted while unknown (no reply yet); ContextWindow too, when the
	// node cannot tell the model's window. The apps show used / window as the context bar.
	ContextTokens int `json:"context_tokens,omitempty"`
	ContextWindow int `json:"context_window,omitempty"`
	// Efforts are the levels session.modes takes for Effort when they are not Claude Code's (which the apps know): a
	// Codex thread the phone drives through its app-server (node/codex_effort.go), its model's levels weakest first.
	// Only Effort can change there. Empty for every other session: Claude's and the assistant's keep the apps' own
	// list, the rest cannot change it from the phone.
	Efforts          []string `json:"efforts,omitempty"`
	StartedByPersona bool     `json:"started_by_persona"`
	Assistant        bool     `json:"assistant"` // runs in the assistant directory (the app's "Chat" tab)
	// Space is the assistant's space it runs in: "health" for the health space's (docs/assistant/health.md), "" for the
	// assistant's own. Set by the hub on the assistant's conversations.
	Space string `json:"space,omitempty"`
	// Archived: a retained conversation moved out of the normal list. Assistant
	// conversations and Claude/Codex Agents are managed by their owning manager.
	Archived bool `json:"archived,omitempty"`
	// ArchiveChangedAt is the manager's last explicit archive/restore visibility change.
	// The hub uses it only to order its legacy delete tombstones against that report.
	ArchiveChangedAt time.Time `json:"archive_changed_at,omitzero"`
	// NativeArchive is whether the tool that owns an archived (or just restored) agent session has it the same way
	// in its own archive (Codex's archived threads): only on GET /api/v1/sessions/archived rows, nil when the manager
	// never asked the tool.
	NativeArchive *NativeArchive `json:"native_archive,omitempty"`
	// Origin is who opened an assistant conversation and OriginRef what of (docs/protocol/protocol.md"Assistant conversation origins"):
	// user, channel, inbox, task, spawned, lark_agent, extension; "" is the user's. Set by the manager's fleet from
	// the assistant's snapshot (internal/assistant/fleet.go).
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`
	// Compaction is the last compaction the user asked for in an assistant conversation (POST …/compact), kept by the
	// node until the next one: running, then done or failed (docs/assistant/assistant.md "Manual compaction").
	Compaction *Compaction `json:"compaction,omitempty"`
	// Channels are the channels messages came into this conversation from, newest first ("wechat", "feishu"):
	// the conversation list draws the latest's icon (docs/extensions/channels.md"Source labels"). Set by the manager's fleet
	// (internal/assistant/fleet.go).
	Channels []string `json:"channels,omitempty"`
	// Space / slot generalize where an agent lives in its host tool: for herdr a workspace and a tab
	// (docs/protocol/agent-states.md). Agents sharing a space are shown together as one collaborating group.
	SpaceKind  string `json:"space_kind,omitempty"` // herdr
	SpaceID    string `json:"space_id,omitempty"`
	SpaceName  string `json:"space_name,omitempty"`
	SpaceIndex int    `json:"space_index,omitempty"`
	// ParentSpaceID is the space this one is nested under in its host, same kind and machine (Orca: a child
	// worktree's parent, #2073); empty for a top-level space. Clients that know it fold the child under its parent.
	ParentSpaceID string `json:"parent_space_id,omitempty"`
	SlotID        string `json:"slot_id,omitempty"`
	SlotName      string `json:"slot_name,omitempty"`
	SlotIndex     int    `json:"slot_index,omitempty"`
	// PaneIndex is the pane's 0-based place in its slot by layout, top left first: 0 is the pane the others were split
	// from (the group's "Initiator" when an agent runs there). Absent (nil) when the host does not say where its panes sit
	// (older nodes, hosts without layouts, a pane missing from the layout). A pointer so that 0 is sent.
	PaneIndex *int `json:"pane_index,omitempty"`
	// State is the unified agent state the apps render (needs_you, working, unread, idle, offline, unknown),
	// computed by the hub from the host status, pending interactions and whether the user has seen the result.
	State string `json:"state"`
	// BackgroundWorking: State is working only because of background work (#2935): its turn ended while its
	// background commands, subagents, Monitors or workflows still run. Clients say "Working (background)". Set by the hub.
	BackgroundWorking bool `json:"background_working,omitempty"`
	// BackgroundWaiting: BackgroundWorking, and all that background work only waits (#6045): its background commands
	// sleep, loop on a check or watch something, using next to no CPU; no subagent or workflow runs. Clients show
	// "Waiting in the background" apart from Working (lighter, not in the Working count); State stays working, so an
	// older app shows Working. The node says whether its background commands wait; the manager keeps it only so.
	BackgroundWaiting bool      `json:"background_waiting,omitempty"`
	SeenAt            time.Time `json:"seen_at,omitempty"`
	// StateSince is when the agent entered its current state, as the hub saw it (the hub restarting resets it):
	// lists sort "Working" by it, since updated_at moves on every step.
	StateSince time.Time `json:"state_since,omitempty"`
	// Queued is input typed while the agent works, waiting to be taken (shown above the composer, not in the chat).
	Queued []string `json:"queued,omitempty"`
	// Background lists commands / agents Claude started in the background that have not reported back yet
	// (the TUI's "2 shells still running").
	Background []BackgroundTask `json:"background,omitempty"`
	// Subagents the agent launched (Agent / Task tool); each one's transcript is the session "<id>@<agent id>".
	Subagents []Subagent `json:"subagents,omitempty"`
	// Workflows are the Claude Code workflow runs (the Workflow tool) the agent launched in the last day, newest first:
	// a running one is background work, as Background is (a running one is in Background too, kind "workflow").
	Workflows []Workflow `json:"workflows,omitempty"`
	// Loop is the /loop driving this agent, when there is one (it wakes itself up on a schedule).
	Loop *LoopInfo `json:"loop,omitempty"`
	// Goal is the goal the agent works toward, when one is set (Claude Code's /goal, a Codex thread goal; docs/integrations/agent-goal.md).
	Goal *GoalInfo `json:"goal,omitempty"`
	// Plan is the agent's own task list (Claude Code's TodoWrite, or TaskCreate / TaskUpdate), when it keeps one: how far
	// along it is and the step in progress (docs/integrations/agent-goal.md "Plan"). Absent for Codex and for runtimes
	// whose conversation is not a Claude transcript on the node.
	Plan *PlanInfo `json:"plan,omitempty"`
	// Ports are the TCP ports the agent's processes listen on that answer HTTP (docs/operations/ports.md): the dev servers
	// it started, which the apps open through the hub.
	Ports     []Port    `json:"ports,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	LastText  string    `json:"last_text,omitempty"`
	// Asks is what the agent's last reply asks the user, when its closing words put a question or a decision to
	// them (transcript.AskOf, #5096): read by the node off the whole reply, since LastText is only its beginning.
	Asks string `json:"asks,omitempty"`
	// AsksRead: the node read Asks off the reply (an empty Asks then means it asks nothing, or the user answered it);
	// without it (an older node, a host that does not say) the hub reads the list's last words when they are whole.
	AsksRead bool `json:"asks_read,omitempty"`
	// AwaitsReply: the agent stopped on a question to the user nobody has answered yet (docs/protocol/agent-states.md "Awaiting your reply",
	// #5096) — it asks (Asks, or the triage says it waits on the user) and has no card, error or background work. Set by
	// the hub on a stopped session (Unread or Seen); an Seen one is listed as Awaiting Reply, not folded with the idle ones.
	AwaitsReply bool `json:"awaits_reply,omitempty"`
	// TurnError: the agent's latest turn ended in a model API error (the connection lost mid-response, overloaded,
	// rate limited, the model service down…). The node sets it when the agent's own record says the turn failed
	// (Codex: a rollout's task_complete.error, an app-server's failed turn, #2185); otherwise the hub reads it off the
	// last words, as Claude Code writes them ("API Error: …"). The hub keeps it only while the agent is stopped: empty
	// while it works or when the turn ended normally. The task it works for is interrupted, not done
	// (docs/assistant/todos.md "How the state is computed", #1516).
	TurnError string `json:"turn_error,omitempty"`
	// TurnErrorKind is what the agent's record says of TurnError, when it says (Codex's error kinds):
	// TurnErrorTransient (worth a "Continue") or TurnErrorUser (the user must act); empty: read off the text.
	TurnErrorKind string `json:"turn_error_kind,omitempty"`
	// TurnErrorCode says what is wrong when the hub can tell from TurnError (docs/assistant/onboarding.md 5.2): one of the
	// TurnErrorCode* values, for a card that says what to fix (sign in, the key, the model). Empty: not known.
	TurnErrorCode string `json:"turn_error_code,omitempty"`
	// Work is where the session stands in its work (docs/assistant/agent-work.md): working, blocked on a named precondition, or
	// done, with a line saying what it waits for or what it concluded. Set by the hub; absent before the hub has
	// triaged a stopped session's turn.
	Work *WorkState `json:"work,omitempty"`
	// Activity says what the agent is doing when its latest step is a tool call no text came after ("running tests",
	// "reading tasks.go", "waiting for CI", transcript.Activity), and ActivityAt when that call started: while an agent only runs
	// tools its last_text stays put for minutes, and a task card showing it looked stuck. Empty once it writes text.
	Activity   string     `json:"activity,omitempty"`
	ActivityAt *time.Time `json:"activity_at,omitempty"`
	// Rev changes whenever the transcript does (its size): the app pulls new items for every session whose rev
	// differs from what it has, without opening it. Changes travel as the light `revs` event.
	Rev int64 `json:"rev,omitempty"`
	// WokeByTask: the latest turn was started by a background task reporting back, not by the user; finishing
	// it sends no push (the user did not ask for anything).
	WokeByTask          bool `json:"woke_by_task,omitempty"`
	PendingInteractions int  `json:"pending_interactions"`
	// Suggestion is the next message Claude Code proposes after a turn (grey in its empty input box), read off the
	// screen; gone once the agent works again or a message is sent.
	Suggestion string `json:"suggestion,omitempty"`
	// Starting: Persona is opening its tab and waiting for claude (node/starting.go); messages sent now go in once it is
	// ready. StartError: the start failed (shown in the chat for a while).
	Starting   bool   `json:"starting,omitempty"`
	StartError string `json:"start_error,omitempty"`
	// Todo is the active (uncompleted, unreleased) todo this session is linked to (assistant/todos.go).
	Todo *TodoRef `json:"todo,omitempty"`
	// Quiet: the agent works (or worked) for a quiet task, one the main conversation opened while the user wanted
	// those told only there (#1731): set by the hub, also once the task is done. Clients leave it out of task
	// notifications and global badges; the Agents page shows it as always.
	Quiet bool `json:"quiet,omitempty"`
	// SpawnFrom is persona_from reported by node for todo derivation (#762), stripped by hub before client push.
	SpawnFrom string `json:"spawn_from,omitempty"`
	// AttentionReason is why the session waits for the user, in the host's words (a botmux attention's reason).
	AttentionReason string `json:"attention_reason,omitempty"`
	// FailureReason: the host stopped the session's turn on an error it will not retry by itself (a botmux notice),
	// until a message goes to the session. The apps show it with a retry; the session is not waiting for the user.
	FailureReason string `json:"failure_reason,omitempty"`
	// ExternalURL opens the session where it lives outside Persona (a botmux session's Feishu topic).
	ExternalURL string `json:"external_url,omitempty"`
	// Runtime: an assistant conversation's runtime as it actually runs ("agentsdk"; "" is the CLI, #2455).
	Runtime string `json:"runtime,omitempty"`
	// RuntimeFallback: why an assistant conversation asked to run on the Agent SDK runs on the CLI instead (no
	// Node.js 22+, no bridge package, no claude); "" when it runs as asked.
	RuntimeFallback string `json:"runtime_fallback,omitempty"`
	// Chat, Topic, Dispatched: a botmux session's Feishu chat, its topic's root message and the topics it dispatched
	// (`botmux dispatch`), for the todos to follow (docs/assistant/todos.md "Following botmux"); stripped by hub like SpawnFrom.
	Chat       string   `json:"chat,omitempty"`
	Topic      string   `json:"topic,omitempty"`
	Dispatched []string `json:"dispatched,omitempty"`
	// AlsoOpenIn are the other hosts (host kinds: herdr, herdr-core, orca…) the session is live in at the same time
	// (#2576): it is listed and driven in the host that owns it (node/ownership.go); the apps warn of the conflict.
	AlsoOpenIn []string `json:"also_open_in,omitempty"`
	// Host is the engine host the session lives in (#2576): the kind of its pane (herdr, herdr-core, orca, botmux) or
	// paseo for the Paseo runtime's; empty for transcripts with no live host and for other runtimes. The apps label a
	// row with it when the machine enables more than one host.
	Host string `json:"host,omitempty"`
}

// Work phases (WorkState.Phase) and what a blocked session waits on (WorkState.On), docs/assistant/agent-work.md.
const (
	WorkWorking = "working"
	WorkBlocked = "blocked"
	WorkDone    = "done"

	WaitUser      = "user"      // a decision, an answer, a link to open: the hub makes a card for it
	WaitAgent     = "agent"     // another agent finishing: the hub wakes this one when it stops
	WaitExternal  = "external"  // a deploy, CI, a review, a time: the hub wakes it to check
	WaitTransient = "transient" // the model service failed for a moment: the hub sends "Continue"
)

// Session.TurnErrorKind values.
const (
	TurnErrorTransient = "transient" // the model service failed for a moment: worth a "Continue"
	TurnErrorUser      = "user"      // sign-in, usage limit, context window, the model…: the user must act
)

// Session.TurnErrorCode values (docs/assistant/onboarding.md 4.6).
const (
	TurnErrorNotLoggedIn = "not_logged_in" // Claude Code on that machine is not signed in
	TurnErrorAuthFailed  = "auth_failed"   // the key or token was refused or has expired
	TurnErrorModelRoute  = "model_route"   // the model name is not one the service or gateway knows
	TurnErrorRateLimited = "rate_limited"  // too many requests, or a usage limit
)

// Codes a call refuses with when the assistant's conversation cannot start on a machine (docs/assistant/onboarding.md 4.6, 5.2):
// rpc.Error codes the hub answers 409 with, the text in the request's language (apierr.Call).
const (
	CodeNoMachine       = "no_machine"       // no machine to run the main conversation on
	CodeMachinePending  = "machine_pending"  // the machine has not been confirmed as the user's yet
	CodeClaudeMissing   = "claude_missing"   // Claude Code is not installed (or not on the launcher's PATH) there
	CodeLauncherMissing = "launcher_missing" // the launcher (a ~/.zshrc function or alias) is not there
)

// WorkState is a session's work state (Session.Work).
type WorkState struct {
	Phase string `json:"phase"`
	On    string `json:"on,omitempty"`
	// Line is one line for the lists: the conclusion when done, what it waits for when blocked.
	Line string `json:"line,omitempty"`
	// Wait / Then: what it waits for and what it will do then (agent, external).
	Wait string `json:"wait,omitempty"`
	Then string `json:"then,omitempty"`
	// Every is how many minutes after the turn the hub first looks again for the wait (it doubles after each wake).
	Every int `json:"every_minutes,omitempty"`
	// WakeAt is when the hub acts next on its own: sends "Continue" (transient) or wakes it to check (external, agent).
	WakeAt *time.Time `json:"wake_at,omitempty"`
	// Attempts the hub made for this wait so far, of at most Max.
	Attempts int        `json:"attempts,omitempty"`
	Max      int        `json:"max,omitempty"`
	Agent    *WorkAgent `json:"agent,omitempty"` // the agent it waits for (on agent)
	Link     *WorkLink  `json:"link,omitempty"`  // a link the user must open in time (on user)
	// Artifacts are what the work produced (done): PRs, commits, documents, builds.
	Artifacts []WorkArtifact `json:"artifacts,omitempty"`
	// Questions are what the user is to decide (on user), as the card asks them.
	Questions []Question `json:"questions,omitempty"`
	// InteractionID is the card the hub made for a decision (on user), while it is pending.
	InteractionID string `json:"interaction_id,omitempty"`
	// Deferred: the user put the decision off on its card (Skip for now, Not now, Stop waiting, Hold off, or closed it): the agent is no
	// Awaiting Reply for it and is not reminded of (#5096).
	Deferred bool `json:"deferred,omitempty"`
	// TurnAt is the end of the turn this state is about (the session's updated_at then); At when it was found, By how:
	// "rule" or "model".
	TurnAt time.Time `json:"turn_at,omitzero"`
	At     time.Time `json:"at,omitzero"`
	By     string    `json:"by,omitempty"`
}

// WorkAgent names another agent (WorkState.Agent).
type WorkAgent struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Label     string `json:"label,omitempty"`
}

// WorkLink is a link to open before it expires (WorkState.Link).
type WorkLink struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// WorkArtifact is something a session's work produced: kind pr, commit, doc, build or url, and its reference.
type WorkArtifact struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// TodoRef is a session's link to an active todo (docs/assistant/todos.md).
type TodoRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SourceRuntime is the source_kind of a session an agent runtime lists (docs/integrations/runtimes.md): source_instance_id names
// the runtime ("dsh"); its capabilities come from what the runtime declared (no terminal, never attach).
const SourceRuntime = "runtime"

// Port is a listening TCP port on a machine (docs/operations/ports.md).
type Port struct {
	Port int `json:"port"`
	// Addr is what it is bound to: 127.0.0.1, 0.0.0.0, ::, ::1 or one of the machine's addresses.
	Addr    string `json:"addr,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"` // the command, as ps / lsof name it (node, python3.12…)
	Title   string `json:"title,omitempty"`   // the <title> of its page at /
}

// Item kinds.
const (
	KindText       = "text"
	KindThinking   = "thinking"
	KindToolUse    = "tool_use"
	KindToolResult = "tool_result"
	KindNotice     = "notice"
	KindImage      = "image"
	KindRecap      = "recap" // Claude Code's "while you were away" summary
	// KindFold stands for a run of the agent's work left out of a page (Item.Fold); only for callers that ask
	// (fold_work).
	KindFold = "fold"
)

// Fold is what a KindFold item stands for: a run of tool calls with their results and thoughts, from the item it is
// named after ("fold:" + its id, at its seq and time) through ThroughID. A chat shows the run folded into counts;
// opening it fetches the run (session.items with from_seq / through_seq), which replaces the fold.
type Fold struct {
	Items      int                `json:"items"`
	Tools      int                `json:"tools"`
	Thoughts   int                `json:"thoughts"` // thoughts with words (blank ones are not shown)
	Until      time.Time          `json:"until"`
	ThroughSeq int                `json:"through_seq"`
	ThroughID  string             `json:"through_id"`
	Tasks      []FoldTaskSnapshot `json:"tasks,omitempty"`
	// Images: the images the run's calls looked at, in order (FoldImage): a chat shows its turn's under the answer
	// without opening the run.
	Images []FoldImage `json:"images,omitempty"`
	// LatestThinking is a bounded preview of the run's newest thought with words. The original item stays in the fold.
	LatestThinking *Item `json:"latest_thinking,omitempty"`
}

// FoldImage is an image a tool call of a folded run looked at (docs/protocol/protocol.md "Images looked at this turn"): one its result
// carried (ItemID, fetched from GET …/items/{item id}/image), or a picture file Read opened whose result carried none
// (only Path; the file's thumbnail is GET /machines/{mid}/file/thumb).
type FoldImage struct {
	ItemID    string `json:"item_id,omitempty"`
	ToolUseID string `json:"tool_use_id"`
	Tool      string `json:"tool"`              // the call's tool as written (Read, mcp__persona__computer_state)
	Source    string `json:"source"`            // read, screenshot (computer_*), browser (browser_*), mcp (another MCP tool), tool
	Path      string `json:"path,omitempty"`    // the picture file Read opened (a Codex view_image too)
	Machine   string `json:"machine,omitempty"` // screenshot / browser: the machine the call named (an id or a name)
	Mime      string `json:"mime,omitempty"`
	Size      int    `json:"size,omitempty"` // bytes, about (the base64's length × 3 / 4, like Image.Placeholder)
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// FoldTaskSnapshot preserves a successful task result without its raw tool payload.
// Replacement boundaries stay ordered so partial updates can inherit older loaded tasks.
type FoldTaskSnapshot struct {
	Replace         string     `json:"replace,omitempty"`
	FallbackStatus  string     `json:"fallback_status,omitempty"`
	Entries         []FoldTask `json:"entries"`
	SourceItemID    string     `json:"source_item_id"`
	SourceToolUseID string     `json:"source_tool_use_id"`
	SourceSeq       int        `json:"source_seq"`
}

type FoldTask struct {
	ID          string  `json:"id"`
	Subject     *string `json:"subject,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
}

type Image struct {
	Mime string `json:"mime"`
	Data string `json:"data"`
	// Lazy: the hub left Data out (clients that asked for lazy_images); fetch it from
	// GET /api/v1/sessions/{mid}/{sid}/items/{item id}/image when the image is shown. Size is the byte count.
	// Width / Height (pixels) let the app reserve the image's space before the bytes arrive.
	Lazy   bool `json:"lazy,omitempty"`
	Size   int  `json:"size,omitempty"`
	Width  int  `json:"width,omitempty"`
	Height int  `json:"height,omitempty"`
}

// Placeholder is the image without its data: what a page carries, fetched on demand when shown.
func (img *Image) Placeholder() *Image {
	if img.Data == "" {
		return img
	}
	lazy := &Image{Mime: img.Mime, Lazy: true, Size: len(img.Data) * 3 / 4}
	// Only the header is read: the decoder stops once it knows the size.
	if c, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(img.Data))); err == nil {
		lazy.Width, lazy.Height = c.Width, c.Height
	}
	return lazy
}

// WithoutImageData returns items whose images carry no data, only a placeholder to fetch on demand: chat pages
// with screenshots inline ran to megabytes.
func WithoutImageData(items []Item) []Item {
	var out []Item
	for i, it := range items {
		if it.Image == nil || it.Image.Data == "" {
			continue
		}
		if out == nil {
			out = append([]Item(nil), items...)
		}
		out[i].Image = it.Image.Placeholder()
	}
	if out == nil {
		return items
	}
	return out
}

// Item is one content block of a Claude Code transcript.
type Item struct {
	ID        string          `json:"id"`
	Seq       int             `json:"seq"`
	TS        time.Time       `json:"ts"`
	Role      string          `json:"role"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Image     *Image          `json:"image,omitempty"`
	Queued    bool            `json:"queued,omitempty"` // user input typed while the agent worked, absorbed mid-turn
	// ClientMsgID: this item came from the message an app sent with this client_msg_id (session.send): its words,
	// its pictures, or the notice it became (/compact). Set by the node, which ties the transcript's line to the send
	// (node/echo.go); absent for anything else and from older nodes. docs/protocol/protocol.md "Matching pending messages".
	ClientMsgID string `json:"client_msg_id,omitempty"`
	// FullSize: Text was cut for pages and pushes (it ends in a "truncated, open for the full text" line, in Chinese); the whole of it, this many
	// bytes, comes from GET /api/v1/sessions/{mid}/{sid}/items/{item id}/full.
	FullSize int `json:"full_size,omitempty"`
	// Wake marks a background task's notice that started new work after the agent had finished its turn
	// (not one absorbed mid-turn): the exchange before it is over and its last words are an answer.
	Wake bool `json:"wake,omitempty"`
	// From names the Claude session that sent this message (cross-session messaging), not the user.
	From string `json:"from,omitempty"`
	// Injected: this user line is a message Persona put into the conversation for the model (a task's notice, a
	// scheduled run, a wake-up…), not something the user wrote. Set by the hub from the line's text
	// (prompts.ParseInjected) on its way to the apps; docs/protocol/protocol.md "Messages Persona injects".
	Injected *Injected `json:"injected,omitempty"`
	Fold     *Fold     `json:"fold,omitempty"`
	// Task: this notice is a background task reporting back (a command, a Monitor, a subagent, or a workflow).
	Task         *TaskNote     `json:"task,omitempty"`
	TurnDuration *TurnDuration `json:"turn_duration,omitempty"`
	// Compact: this notice is where the context was compacted (Claude Code's compact_boundary).
	Compact *Compact `json:"compact,omitempty"`
}

// Injected says what a message Persona wrote for the model is, for the apps to show one line instead of it.
type Injected struct {
	Kind    string `json:"kind"`              // InjectedTaskNotice | InjectedFollowup | InjectedSystem
	State   string `json:"state,omitempty"`   // task_notice: done|needs|interrupted|failed; followup: once|daily|interval|wake; system: lost_tasks
	Title   string `json:"title,omitempty"`   // the task's, the scheduled task's, or what a woken agent waits for
	Machine string `json:"machine,omitempty"` // task_notice: where its agent runs (a name)
	TodoID  string `json:"todo_id,omitempty"` // task_notice: the task, when the hub can tell
	Line    string `json:"line"`              // the one line to show
	Detail  string `json:"detail,omitempty"`  // followup once|daily|interval: what the task asks, as the user wrote it
}

// Injected kinds.
const (
	InjectedTaskNotice = "task_notice"
	InjectedFollowup   = "followup"
	InjectedSystem     = "system"
)

// Compact says how a compaction came about: Trigger "manual" (/compact) or "auto", and the context's size before it.
type Compact struct {
	Trigger   string `json:"trigger,omitempty"`
	PreTokens int    `json:"pre_tokens,omitempty"`
}

// Compaction states.
const (
	CompactionRunning = "running"
	CompactionDone    = "done"
	CompactionFailed  = "failed"
)

// Compaction is a compaction the user asked for: State running, done or failed (Error says why).
type Compaction struct {
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// TurnDuration preserves a recorded whole-turn duration and its native association.
type TurnDuration struct {
	DurationMS int64  `json:"duration_ms"`
	Source     string `json:"source"`
	TurnID     string `json:"turn_id,omitempty"`
	ParentUUID string `json:"parent_uuid,omitempty"`
	UserItemID string `json:"user_item_id,omitempty"`
}

// TaskNote is what a <task-notification> says, read from the notification alone (the call it came from is found by
// the app, through TaskID / ToolUseID): a command run in the background finishing, a Monitor's event or its end, a
// subagent finishing. Title, Detail and Text come decoded (Claude Code writes &gt; &lt; &amp; in them).
type TaskNote struct {
	Kind   string `json:"kind"`             // shell | monitor | agent | workflow | task (anything else)
	Status string `json:"status,omitempty"` // completed | failed | killed | stopped | event | ended | waiting
	// Title: the call's description, or the first line of its command when it had none.
	Title      string `json:"title"`
	TaskID     string `json:"task_id,omitempty"`
	ToolUseID  string `json:"tool_use_id,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	OutputFile string `json:"output_file,omitempty"`
	// Detail: a Monitor's event, a subagent's result, or a command's whole text when the title is only its first line.
	Detail string `json:"detail,omitempty"`
}

// Interaction kinds.
const (
	IxQuestion    = "question"
	IxPlan        = "plan"
	IxPermission  = "permission"
	IxScreen      = "screen"
	IxElicitation = "elicitation"
)

// AnsweredByAssistant marks a resolution the assistant gave for the user (Resolution.AnsweredBy).
const AnsweredByAssistant = "assistant"

// DismissedInChat is the Reason of a Dismiss for an assistant question taken back because the user wrote in the
// conversation instead of answering it (#9): channels say "replied in the chat" on its card, not "dismissed".
const DismissedInChat = "replied_in_chat"

// QuestionOptionDefer dismisses a triage question without sending an agent reply.
// Other question runtimes do not interpret this triage-only action.
const QuestionOptionDefer = "defer"

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Action      string `json:"action,omitempty"` // Optional triage intent; empty means a normal answer.
}

type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	MultiSelect bool             `json:"multi_select"`
	Options     []QuestionOption `json:"options"`
}

type Interaction struct {
	// Manager is the manager it belongs to (docs/assistant/managers.md, "Ownership"); the hub stamps it on what it sends the apps.
	Manager         string          `json:"manager,omitempty"`
	ID              string          `json:"id"`
	MachineID       string          `json:"machine_id"`
	SessionID       string          `json:"session_id"`
	SessionTitle    string          `json:"session_title,omitempty"`
	TaskTitle       string          `json:"task_title,omitempty"` // the task its agent works for (docs/assistant/unified-entry.md), set when sent to clients
	Cwd             string          `json:"cwd,omitempty"`
	Kind            string          `json:"kind"`
	CreatedAt       time.Time       `json:"created_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	ToolName        string          `json:"tool_name,omitempty"`
	ToolUseID       string          `json:"tool_use_id,omitempty"`
	Questions       []Question      `json:"questions,omitempty"`
	Plan            string          `json:"plan,omitempty"`
	ToolInput       json.RawMessage `json:"tool_input,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	Screen          string          `json:"screen,omitempty"`
	AgentID         string          `json:"agent_id,omitempty"`
	AgentType       string          `json:"agent_type,omitempty"`
	MCPServerName   string          `json:"mcp_server_name,omitempty"`
	MCPConfigSource string          `json:"mcp_config_source,omitempty"`
	Elicitation     *Elicitation    `json:"elicitation,omitempty"`
	// CanCancel is advertised only by runtimes supporting question-only cancellation.
	CanCancel bool `json:"can_cancel,omitempty"`
	// Dialog is the screen's dialog read on the hub (screen interactions); answers name an option and are checked
	// against Dialog.Rev before any key is pressed. Absent when the screen shows no dialog it could read.
	Dialog *dialogwire.Dialog `json:"dialog,omitempty"`
	// Secret: the screen asks for a password. The app takes it in a password field and keeps no copy; the hub never
	// stores what is typed into a terminal card anyway.
	Secret bool `json:"secret,omitempty"`
	// Fields are filled in on the phone before approving (a permission card that needs a password): the values come
	// back in Resolution.Values and never pass through the model. Quick approve (notification buttons, preview
	// cards, the watch, Siri) is off for such cards: they open the form.
	Fields []Field `json:"fields,omitempty"`
	// Resumable: a hook waits on it that reconnects when its node goes away (hub or node restart), so the hub keeps
	// it pending through the gap instead of cancelling it (docs/protocol/protocol.md "Interactions survive restarts").
	Resumable bool `json:"resumable,omitempty"`
	// Agent is who asks when it is not Claude Code: "codex" or "traex" (a card the node read off a Codex TUI,
	// docs/integrations/codex.md "Questions and approvals").
	Agent string `json:"agent,omitempty"`
	// Reason: why the agent asks for this permission, in its words (Codex's "Reason: …").
	Reason string `json:"reason,omitempty"`
	// AlwaysLabel names what "always allow" does for this request ("don't ask about this command again in this session", in Chinese); a permission card offers it
	// only when set or for Claude.
	AlwaysLabel string `json:"always_label,omitempty"`
	// AgentLabel is what to call Agent when the apps do not know it (a runtime's own name, "DSH"; docs/integrations/runtimes.md),
	// as proto.Session.AgentLabel.
	AgentLabel string `json:"agent_label,omitempty"`
	// AlwaysKeys: what "Always allow in this session" grants on an assistant conversation's permission card, set by the node (a tool,
	// a Codex command or file, a gated write's route). The hub keeps them for the conversation once answered so, and
	// answers a later card whose keys are all kept by itself: the agent's own session rule dies with its process
	// (docs/assistant/assistant.md "Always allow in this session"). Absent on Agents sessions' cards.
	AlwaysKeys []string `json:"always_keys,omitempty"`
	// Delegating: the assistant is deciding whether it may answer this question for the user (agents.delegate,
	// docs/assistant/assistant.md "The assistant answers questions first"); nothing was pushed yet. The card shows in the apps meanwhile, and an answer
	// given there wins.
	Delegating bool `json:"delegating,omitempty"`
	// Delegate is what the assistant found when it left the question to the user: the context it filled in.
	Delegate *Delegate `json:"delegate,omitempty"`
	// Muted says the user muted this agent or its space on the hub. The hub stamps it when answering GET
	// /api/v1/interactions so an answer-only device (the watch) can hide the card and not buzz for it; the phone
	// keeps its own mutes. Absent on older hubs' lists and on the manager's bus copy.
	Muted bool `json:"muted,omitempty"`
}

// Delegate is the assistant's context on a question it left to the user (Interaction.Delegate): it is a red line or
// about the goal itself, or the assistant could not tell.
type Delegate struct {
	What      string     `json:"what,omitempty"`      // what this is about, in a line
	WhyNow    string     `json:"why_now,omitempty"`   // why the agent asks now
	Recommend string     `json:"recommend,omitempty"` // the answer it recommends (an option's label when there is one)
	Cost      string     `json:"cost,omitempty"`      // what not taking the recommendation costs
	Reason    string     `json:"reason,omitempty"`    // why it is the user's to answer
	Deadline  *time.Time `json:"deadline,omitempty"`  // by when: 24 hours, or the card's expiry when that is sooner
}

// Field is one input of a card (Interaction.Fields).
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret,omitempty"` // a password: a secure field, redacted in the history
	Optional    bool   `json:"optional,omitempty"`
}

// Elicitation carries the hook's original schema or URL without interpreting its fields.
type Elicitation struct {
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
	URL             string          `json:"url,omitempty"`
	ID              string          `json:"elicitation_id,omitempty"`
}

type Resolution struct {
	// Cancel discards only this question, without supplying answers or interrupting the task.
	Cancel   bool              `json:"cancel,omitempty"`
	Action   string            `json:"action,omitempty"`
	Content  map[string]any    `json:"content,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Approve  *bool             `json:"approve,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	Feedback string            `json:"feedback,omitempty"`
	Allow    *bool             `json:"allow,omitempty"`
	Always   bool              `json:"always,omitempty"`
	Message  string            `json:"message,omitempty"`
	Keys     []string          `json:"keys,omitempty"`
	Text     string            `json:"text,omitempty"`
	// AnsweredBy is who answered when it was not the user in the app: AnsweredByAssistant for a question the
	// assistant answered on their behalf (docs/assistant/assistant.md). Absent when the app answered.
	AnsweredBy string `json:"answered_by,omitempty"`
	// Reason is why the assistant answered so (AnsweredByAssistant): the Y of "Answered by the assistant: X, because Y".
	// With Dismiss and no AnsweredBy it is why the card was dismissed (DismissedInChat).
	Reason string `json:"reason,omitempty"`
	// Remembered marks an allow the manager gave from an "Always allow in this session" it had kept (or that a card
	// answered meanwhile covers), not from an answer to this card: the node's sent-as-user ledger records such a write
	// as conversation_always, not as a card (docs/extensions/connectors.md "Sent as the user"). Only the manager sets it; an
	// answer from an app or a channel never carries it. An older manager does not send it.
	Remembered bool `json:"remembered,omitempty"`
	// PreKeys go in before Text (Keys after it): pick "Type something." by its number, type the answer, enter.
	PreKeys []string `json:"pre_keys,omitempty"`
	Dismiss bool     `json:"dismiss,omitempty"`
	// Option answers a screen dialog by its option's key (Interaction.Dialog): the keys are worked out where they are
	// pressed, from the screen as it is then (Text goes with a type_something option).
	Option string `json:"option,omitempty"`
	// Options answer a multi-select screen dialog (Dialog.Multi): the keys of the options to have checked when it is
	// sent, the rest unchecked; Text is its free-text field (empty: left unchecked).
	Options []string `json:"options,omitempty"`
	// Values are the card's Fields as filled in on the phone, by key.
	Values map[string]string `json:"values,omitempty"`
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Shell are the words that run a gated command's tool in a shell instead (its command, its skill's script): a
	// runtime refuses them in an assistant conversation's shell, where the gate and its credentials are not
	// (integrations/dsh), and points at the tool.
	Shell []string `json:"shell,omitempty"`
}

// Content is an MCP tool result content block.
type Content struct {
	Type     string `json:"type"` // text | image
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"is_error"`
	// ErrorKind is what kind of failure an error is, for a program to act on (personad tool's exit code), when the
	// hub, the node or the connector knows: one of the ToolError* below; empty is a plain failure, or an older hub's.
	ErrorKind string `json:"error_kind,omitempty"`
	// RetryAfter is how many seconds a rate limit lasts (ErrorKind rate_limited), when it is known.
	RetryAfter int `json:"retry_after,omitempty"`
}

// The kinds of a tool's failure (ToolResult.ErrorKind), as personad tool's exit codes have them.
const (
	ToolErrorDeclined    = "declined"     // the user (or connector_gate) did not allow the write
	ToolErrorUnavailable = "unavailable"  // the node, the hub or the connector cannot be reached or is not signed in
	ToolErrorRateLimited = "rate_limited" // the connector waits out a rate limit (RetryAfter seconds)
)

func TextResult(text string, isErr bool) ToolResult {
	return ToolResult{Content: []Content{{Type: "text", Text: text}}, IsError: isErr}
}

// ErrorResult is a failure of a known kind (ToolError*), retryAfter seconds for a rate limit (0: not known).
func ErrorResult(kind, text string, retryAfter int) ToolResult {
	res := TextResult(text, true)
	res.ErrorKind, res.RetryAfter = kind, max(retryAfter, 0)
	return res
}

// BackgroundTask is a Bash command, subagent, Monitor or workflow Claude runs in the background (run_in_background).
type BackgroundTask struct {
	ID          string    `json:"id"`   // task id from the tool result (e.g. "b85polnoj")
	Kind        string    `json:"kind"` // "shell" | "agent" | "monitor" | "workflow"
	Description string    `json:"description,omitempty"`
	Command     string    `json:"command,omitempty"`
	OutputFile  string    `json:"output_file,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	// ExpiresAt: when a Monitor stops by itself ("expires in 30m"); zero when it does not say.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// RunID: a workflow's run ("wf_…"), the key of its entry in Session.Workflows.
	RunID string `json:"run_id,omitempty"`
}

// Workflow is a Claude Code workflow run (the Workflow tool): a script that drives many agents in the background and
// reports back to the session with a task notification when it ends (docs/protocol/agent-states.md "Background workflows").
type Workflow struct {
	TaskID  string `json:"task_id"`          // the background task's id, as in Background and the task notification
	RunID   string `json:"run_id,omitempty"` // "wf_…": its directory under <session>/subagents/workflows/
	Name    string `json:"name,omitempty"`   // the script's meta.name ("parity-audit")
	Summary string `json:"summary,omitempty"`
	// Status is one of the Workflow* values. Running is Status == WorkflowRunning, for clients that only need that.
	Status  string `json:"status"`
	Running bool   `json:"running"`
	// Phases are the script's phase titles in order; Phase is the one its latest started agent is in, PhaseIndex its
	// 1-based place in Phases (0: not known).
	Phases     []string `json:"phases,omitempty"`
	Phase      string   `json:"phase,omitempty"`
	PhaseIndex int      `json:"phase_index,omitempty"`
	PhaseCount int      `json:"phase_count,omitempty"` // len(Phases)
	ToolUseID  string   `json:"tool_use_id,omitempty"` // the Workflow call that launched it
	// PhaseStats counts all agents, before Agents is capped, in Phases order then any undeclared phases.
	PhaseStats []WorkflowPhaseStats `json:"phase_stats,omitempty"`
	// AgentsStarted counts the agents it started so far (the total is not known before it ends: scripts fan out as
	// they go), AgentsDone those that returned, AgentsFailed those the journal says failed (not counted as done; absent
	// from older nodes, which counted them as done), AgentsRunning those still at it. Agents stopped when the run ended
	// are none of the three.
	AgentsStarted int        `json:"agents_started"`
	AgentsDone    int        `json:"agents_done"`
	AgentsFailed  int        `json:"agents_failed,omitempty"`
	AgentsRunning int        `json:"agents_running"`
	Tokens        int        `json:"tokens,omitempty"` // sum of each agent's latest usage, not cumulative usage
	Calls         int        `json:"calls,omitempty"`  // all agents' tool calls, before Agents is capped
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`   // nil while it runs
	UpdatedAt     time.Time  `json:"updated_at,omitempty"` // its last sign of life (journal or an agent's transcript)
	Error         string     `json:"error,omitempty"`      // a failed or killed run's error, first line
	// Agents are its agents in the order they started (at most WorkflowAgentsMax, the latest kept).
	Agents []WorkflowAgent `json:"agents,omitempty"`
}

// WorkflowAgentsMax caps Workflow.Agents.
const WorkflowAgentsMax = 60

// WorkflowPhaseStats counts agents in one phase, as Workflow counts them all. An empty title groups agents whose phase
// is unknown.
type WorkflowPhaseStats struct {
	Title   string `json:"title"`
	Started int    `json:"started"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed,omitempty"`
	Running int    `json:"running"`
}

// A workflow's statuses. Stalled: no notification and no state file, yet nothing written for a long while (its
// claude went away): not counted as running.
const (
	WorkflowRunning   = "running"
	WorkflowCompleted = "completed"
	WorkflowFailed    = "failed"
	WorkflowKilled    = "killed"
	WorkflowStalled   = "stalled"
)

// WorkflowAgent is one agent of a workflow run. Its transcript reads like a subagent's, as the session SessionID.
type WorkflowAgent struct {
	ID         string        `json:"id"`
	SessionID  string        `json:"session_id"` // "<parent>@<agent id>"
	Label      string        `json:"label,omitempty"`
	Phase      string        `json:"phase,omitempty"`
	Running    bool          `json:"running"`
	Status     string        `json:"status"` // running | done | failed (the journal says so) | stopped (its run ended before it returned)
	StartedAt  time.Time     `json:"started_at,omitempty"`
	EndedAt    *time.Time    `json:"ended_at,omitempty"` // nil while running; last exchange, or the run's end if stopped
	UpdatedAt  time.Time     `json:"updated_at,omitempty"`
	LastText   string        `json:"last_text,omitempty"`
	Model      string        `json:"model,omitempty"`
	LatestTool *WorkflowTool `json:"latest_tool,omitempty"`
	Tokens     int           `json:"tokens,omitempty"` // latest usage's input + cache read + cache creation + output
	Calls      int           `json:"calls,omitempty"`  // tool_use and server_tool_use blocks in its transcript
}

// WorkflowTool is an agent's most recent tool call, retained after its final text arrives. Summary is a short line on
// what it does ("Reading example.go"): the node writes it in Chinese, the hub gives it in the reader's language.
type WorkflowTool struct {
	ID      string `json:"id,omitempty"` // the call's tool_use id: a client keys what it plays for a new call by it
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// GoalInfo is a goal the agent keeps working toward until it holds (docs/integrations/agent-goal.md): Claude Code's /goal, whose
// Stop hook checks the condition each time the agent would stop, or a Codex thread goal.
type GoalInfo struct {
	Objective string `json:"objective"`
	// Status is one of the Goal* values: Claude Code's goals are active until met (complete); Codex has the others too.
	Status string `json:"status"`
	// Reason is Claude Code's latest check of the condition: why it holds, or what is still missing.
	Reason      string    `json:"reason,omitempty"`
	TokensUsed  int       `json:"tokens_used,omitempty"`
	TokenBudget int       `json:"token_budget,omitempty"` // Codex: the budget the goal may spend, 0 when there is none
	Seconds     int       `json:"seconds,omitempty"`      // time spent on it so far
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// PlanInfo is an agent's latest task list, counted (docs/integrations/agent-goal.md "Plan"): Done of Total items completed, and
// Current, the item in progress (its activeForm, else its subject) on one line, "" when none is. At is when the
// counts or Current last changed.
type PlanInfo struct {
	Done    int       `json:"done"`
	Total   int       `json:"total"`
	Current string    `json:"current,omitempty"`
	At      time.Time `json:"at"`
}

// A goal's statuses (Codex's thread_goals.status; Claude Code's goals are only active or complete).
const (
	GoalActive        = "active"
	GoalPaused        = "paused"
	GoalBlocked       = "blocked"
	GoalUsageLimited  = "usage_limited"
	GoalBudgetLimited = "budget_limited"
	GoalComplete      = "complete"
)

// LoopInfo describes a /loop: Claude re-runs Prompt on its own, either by scheduling its next wakeup each tick
// (ScheduleWakeup, kind "wakeup") or with a cron job (CronCreate, kind "cron").
type LoopInfo struct {
	Kind            string    `json:"kind"`
	Prompt          string    `json:"prompt,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	IntervalSeconds int       `json:"interval_seconds,omitempty"`
	NextAt          time.Time `json:"next_at,omitempty"`
	Cron            string    `json:"cron,omitempty"`
	Ticks           int       `json:"ticks,omitempty"` // wakeups scheduled so far
}

// Subagent is an agent the session launched (the Agent tool), with its own transcript.
type Subagent struct {
	ID          string    `json:"id"`         // agent id (agent-<id>.jsonl)
	SessionID   string    `json:"session_id"` // "<parent>@<id>": readable like any session
	Type        string    `json:"type,omitempty"`
	Description string    `json:"description,omitempty"`
	ToolUseID   string    `json:"tool_use_id,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"` // the launching subagent's id; empty: the session (an unlisted one: show at the top)
	Fork        bool      `json:"fork,omitempty"`      // a fork of its launcher's conversation (agentType "fork")
	Running     bool      `json:"running"`
	Background  bool      `json:"background,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	// UpdatedAt is when it last wrote a message or a tool result (its file's mtime only before it wrote one): it moves
	// when there is something new to show, not with every line the file gets (#5602).
	UpdatedAt time.Time     `json:"updated_at,omitempty"`
	LastText  string        `json:"last_text,omitempty"`
	Steps     int           `json:"steps,omitempty"`     // tool calls it made
	LastTool  *WorkflowTool `json:"last_tool,omitempty"` // its latest tool call: what it is doing now while it runs
}

// UsageBucket is one minute of one model's token usage on a machine, summed over every transcript there (node
// usage.go). A node reports a bucket's whole total each time it changes, so reporting it again replaces it.
type UsageBucket struct {
	Minute int64  `json:"minute"` // Unix seconds of the minute's start
	Model  string `json:"model"`
	// Launcher ran the replies: the session's launcher as Persona started it, "" when unknown (typed in a terminal, an
	// older node). A minute has one bucket per model and launcher.
	Launcher   string `json:"launcher,omitempty"`
	Input      int64  `json:"in,omitempty"` // not from the cache
	CacheWrite int64  `json:"cache_write,omitempty"`
	CacheRead  int64  `json:"cache_read,omitempty"`
	Output     int64  `json:"out,omitempty"`
	Requests   int    `json:"req,omitempty"` // model replies
}

// LarkRule is one rule of the assistant's Feishu write gate (node/larkgate.go): a Lark gateway agent API call matching
// Method ("*" for any), Path (segments; "*" one segment, "**" the rest) and, when set, the top-level fields of its
// JSON body, gets Action: "allow" (run it), "confirm" (the user approves it on the phone first) or "deny".
// hub.json's lark_gate rules come first, then the built-in ones; the first match decides.
type LarkRule struct {
	Method string         `json:"method"`
	Path   string         `json:"path"`
	Body   map[string]any `json:"body,omitempty"`
	Action string         `json:"action"`
}

// AssistantLark is what the assistant's machine needs to gate its skills' Feishu commands: the write gate's extra
// rules, lark-cli and the plugins' command gates. Sent by the hub to the assistant's node only; never written to disk
// or logs. (It no longer carries the Lark gateway's base and token: that is an extension's now, which supplies its own gate
// and credentials through Gates; an older hub's "account", "base" and "token" are unknown keys and ignored.)
type AssistantLark struct {
	Rules  []LarkRule `json:"rules,omitempty"`
	CLI    *LarkCLI   `json:"cli,omitempty"`    // lark-cli is configured (hub.json lark.cli, docs/plans/plan-lark-cli.md)
	Prefer string     `json:"prefer,omitempty"` // "cli", or the serving extension's own source: the primary one when both are there
	// Gates are the command gates of the assistant's skills (plugins' assistant.gate, docs/extensions/extensions.md "Assistant"):
	// a command calling an HTTP API as the user, its credentials and the rules its calls go by.
	Gates []CommandGate `json:"gates,omitempty"`
	// MCPServers are the extensions' MCP servers for the assistant (mcp_servers, docs/extensions/extensions.md "MCP servers").
	MCPServers []AssistantMCPServer `json:"mcp_servers,omitempty"`
}

// AssistantMCPServer is one extension's MCP server for the Claude assistant: a command line on its machine, its tools
// as mcp__<ID>__<tool> (node/assistant_mcp.go). Allow run without asking, Deny are never callable, the rest go by the
// conversation's permission mode.
type AssistantMCPServer struct {
	Plugin   string   `json:"plugin,omitempty"`
	ID       string   `json:"id"`
	Command  string   `json:"command"`
	Env      string   `json:"env,omitempty"`
	Args     []string `json:"args,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Allow    []string `json:"allow,omitempty"`
	Deny     []string `json:"deny,omitempty"`
}

// CommandGate is one skill command that calls an HTTP API as the user (`<command> METHOD PATH [BODY] [curl flags]`),
// as the assistant's machine gates it (node/larkgate.go): the command on the assistant's PATH runs the skill's
// script with Env; every Bash call of it is read, and its calls go by Rules (the first match; none matching is
// confirmed on the phone). With Auth the credentials stay in the node: BaseEnv points at its proxy, which adds them
// and holds every request to Rules (node/gateproxy.go, #1317).
type CommandGate struct {
	Plugin  string `json:"plugin,omitempty"`
	Command string `json:"command"` // the name on the assistant's PATH
	Script  string `json:"script"`  // the script it runs, relative to the skills directory ("<skill>/scripts/x.sh")
	Title   string `json:"title"`   // what the phone card says it acts on ("on <Title>…", in Chinese)
	// Env is put into the assistant's process; BaseEnv names the one holding the API's base URL (curl / wget at its
	// host is refused); Secret are names the assistant may never mention (the token); AllowedEnv may be set before
	// the command (VAR=x cmd …).
	Env        map[string]string `json:"env,omitempty"`
	BaseEnv    string            `json:"base_env,omitempty"`
	Secret     []string          `json:"secret,omitempty"`
	AllowedEnv []string          `json:"allowed_env,omitempty"`
	Headers    []string          `json:"headers,omitempty"`     // -H headers the command may be given (lowercase)
	PathPrefix string            `json:"path_prefix,omitempty"` // dropped from a call's path ("/agent/v1")
	Rules      []LarkRule        `json:"rules,omitempty"`
	// Actions name what a call does on the phone card: the first whose pattern (a regexp over "METHOD /path")
	// matches.
	Actions []GateAction `json:"actions,omitempty"`
	// Tool is the command as a tool for an assistant on a runtime without Bash ({args: [words after the command]}).
	Tool *GateTool `json:"tool,omitempty"`
	// Auth is how the node's proxy puts the credentials on a request; nil: Env goes into the assistant's process
	// whole, Secret included (a gate whose script needs its secret itself, or a hub from before #1317).
	Auth *GateAuth `json:"auth,omitempty"`
}

// GateAuth is a command gate's credentials as its API takes them: the header Header set to Value, each ${NAME} in it
// replaced by that one of the gate's Env.
type GateAuth struct {
	Header string `json:"header"`
	Value  string `json:"value"`
}

// GateAction names a kind of call for the phone card.
type GateAction struct {
	Pattern string `json:"pattern"`
	What    string `json:"what"`
}

// GateTool is a command gate's tool for runtimes.
type GateTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// LarkCLI is how the assistant's machine runs lark-cli.
type LarkCLI struct {
	Bin     string `json:"bin,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// NativeArchive is a session's state in its own tool's archive, as its manager last asked for it
// (docs/protocol/protocol.md "session.archive").
type NativeArchive struct {
	// Archived is what the tool was asked for: archived (true) or restored.
	Archived bool `json:"archived"`
	// State: done (the tool has it so), pending (not yet: the machine offline, the agent working, an error; tried
	// again), unsupported (the tool has no archive Persona can drive: the archive is Persona's alone).
	State string `json:"state"`
	// Error is why the last try did not finish, while pending.
	Error string `json:"error,omitempty"`
}
