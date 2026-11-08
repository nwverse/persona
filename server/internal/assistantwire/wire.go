// Package assistantwire is what the hub and the assistant (internal/assistant, its own process) say to each other
// over the bus (docs/protocol/protocol.md "Bus", docs/assistant/assistant.md "The boundary with the hub"): each method's and event's name, params
// and result, the error codes that cross it, and the routes the hub relays. Both sides import it; neither imports
// the other.
package assistantwire

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Hello is manager.hello's answer, and manager.updated's params (the answer again when it changed): what the hub
// knows that the manager needs (its machines, the cards' switches, the push timings, the user's language). The
// manager's own sections (Owner, OwnerNotes, Agents, KB, the models but Launchers, a card's wait) are not sent by a hub
// that knows config.own: the manager lays its manager.json over the hello (assistant/ownconfig.go); an older hub still
// sends its hub.json's, which a manager.json section replaces. Its own settings are its own (Profile).
type Hello struct {
	Owner string `json:"owner"`
	// OwnerNotes is manager.json's assistant.notes: what the assistant knows about the user until "About me" is edited.
	OwnerNotes string `json:"owner_notes"`
	// Assistant is where the hub would run the assistant's conversations by default.
	Assistant Placement `json:"assistant"`
	// DoneDelayMS is how long a finished turn stays still before it counts as the end (hub.json notify): a task's
	// agents are followed with the same patience as the hub's own pushes.
	DoneDelayMS int64 `json:"done_delay_ms"`
	// PersonaView is hub.json's assistant.persona_view: teach ```persona-view rather than ```persona-card (#1850).
	PersonaView bool `json:"persona_view,omitempty"`
	// HTMLViews is hub.json's assistant.html_views (default on) while PersonaView is on: the HTML bridge
	// (…/views/{key}/html-actions) answers. An older hub never sends it, so the bridge stays off there.
	HTMLViews bool `json:"html_views,omitempty"`
	// Agents is manager.json's agents section: the triage's limits and default switches (agents.go).
	Agents AgentsConfig `json:"agents,omitzero"`
	// KB is manager.json's kb section: the knowledge base the assistant keeps (kb.go).
	KB *KBSettings `json:"kb,omitempty"`
	// Models is what hub.json says about models (models.go).
	Models HubModels `json:"models,omitzero"`
	// Locale is the user's language (zh-Hans / en / ja, docs/protocol/i18n.md): the assistant answers in it. "" (an older hub)
	// is zh-Hans.
	Locale string `json:"locale,omitempty"`
	// Mutes are the muted spaces / agents (the hub's settings mutes) of the manager's own machines only (as
	// fleet.mutes), so a manager has them before anything it pushes; nil from an older hub or while the hub cannot
	// read them (fleet.mutes brings them later).
	Mutes *Mutes `json:"mutes,omitempty"`
	// LegacyConfig is what of the hub's hub.json the hello carried before config.own (managerconf.HelloSections:
	// owner, notes, Agents, kb, ai, a card's wait, the inbox's models and AI twin machine), raw. Only in the handshake's
	// answer to a built-in manager the hub did not start (its hello's deploy is not "child"), which takes them over into
	// its manager.json once (managerconf.MoveFrom); the hub's child took them from this hub.json itself.
	LegacyConfig json.RawMessage `json:"legacy_config,omitempty"`
	// Notify is hub.json's notify: the finished push's timings; HookTimeoutMS, how long a hook's card waits by default,
	// is manager.json's hook_timeout_minutes, over the hello (the interactions and the pushes are the manager's).
	Notify NotifyTimings `json:"notify,omitzero"`
}

// NotifyTimings are hub.json's notify section, and manager.json's hook_timeout_minutes, in milliseconds (0: the
// default).
type NotifyTimings struct {
	DoneDelayMS      int64 `json:"done_delay_ms,omitempty"`
	LongTurnMS       int64 `json:"long_turn_ms,omitempty"`
	BackgroundHoldMS int64 `json:"background_hold_ms,omitempty"`
	HookTimeoutMS    int64 `json:"hook_timeout_ms,omitempty"`
}

// Placement is the hub's defaults for where the assistant runs.
type Placement struct {
	// MachineID is manager.json's assistant.machine when the hub has it, else the hub's own machine, else the first one;
	// "" when the hub has no machine.
	MachineID string `json:"machine_id"`
	// Launcher is manager.json's assistant.launcher ("" is claude).
	Launcher string `json:"launcher,omitempty"`
	// Machines are the machines the hub has: a machine the assistant saved that is none of them is not used.
	Machines []string `json:"machines,omitempty"`
	// WorkMachines are configured work computers, including aliases; none may host the assistant.
	WorkMachines []string `json:"work_machines,omitempty"`
	// Standby: this hub is a member of a mesh group, whose center runs the scheduled tasks (docs/server/mesh.md).
	Standby bool `json:"standby,omitempty"`
}

// DeviceCall is device.call's params. Device is a machine id, or DeviceAll for DeviceDevicesList.
type DeviceCall struct {
	Device string          `json:"device"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// The machine methods the assistant calls. The node's own methods go to the machine's node as they are; the hub
// answers the others from what it holds about the machine.
const (
	// The node's (docs/protocol/protocol.md "hub ↔ node").
	DeviceSessionItems  = "session.items"  // {session_id, turns} → {items}
	DeviceSessionRename = "session.rename" // {session_id, title} → {ok}
	DeviceAssistantSend = "assistant.send" // {session_id, text, resume_only?, permission_mode?, …model} → {session_id}
	DeviceSessionSend   = "session.send"   // {session_id, text} → {}
	DeviceSessionStart  = "session.start"  // {cwd, prompt, launcher?, model?, permission_mode?, tab_name?, workspace_id?} → {session}
	// DeviceWorktreePrepare / DeviceWorktreeCreate: "New worktree…" (docs/integrations/terminal.md), the workspace named by a directory
	// in it: {cwd} → WorktreePrepare; {cwd, branch, path} → WorktreeCreateResult.
	DeviceWorktreePrepare = "worktree.prepare"
	DeviceWorktreeCreate  = "worktree.create"

	// The hub's.
	// DeviceDevicesList (on DeviceAll): {} → {devices: [Device], phone_online}, the user's machines.
	DeviceDevicesList = "devices.list"
	// DeviceInteractionsList (on DeviceAll): {} → {interactions}, what the machines' agents wait on the user for.
	DeviceInteractionsList = "interactions.list"
	// DeviceSessionsList: {} → {sessions}, the machine's sessions as the apps get them (state, titles).
	DeviceSessionsList = "sessions.list"
	// DeviceSessionGet: {session_id} → {session, ok}, one session as the node listed it.
	DeviceSessionGet = "session.get"
)

// DeviceAll is the device of the calls about every machine.
const DeviceAll = "*"

// Device is one of the user's machines as devices.list has it.
type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`     // local (the hub's own) | ssh | link …
	SSHHost string `json:"ssh_host,omitempty"` // the hub machine's ssh alias for it
	Home    string `json:"home,omitempty"`     // "" while it has not reported one
	DataDir string `json:"data_dir,omitempty"` // its node's data directory ("" while not reported, or the default)
	Online  bool   `json:"online"`
	// Reachable: a call to it would go out now (its link is up; Online can lag a reconnect).
	Reachable bool `json:"reachable"`
	// Version is its node's build ("" while not reported); ModelConfiguration: that node takes a launcher's model
	// (--model) and effort, which "Models" needs of the machine a launcher purpose runs on.
	Version            string `json:"version,omitempty"`
	ModelConfiguration bool   `json:"model_configuration,omitempty"`
	// ClaudeVersion is Claude Code's version there ("" when it has none or never said): 设置 › 模型供应商's built-in
	// claude lists the machines that have it.
	ClaudeVersion string `json:"claude_version,omitempty"`
}

// ErrNoDevice: the hub has no machine by the id a DeviceCall gave.
var ErrNoDevice = errors.New("no such machine")

// AppCall is app.call's params (appkit.RelayParams on the bus).
type AppCall struct {
	AppID  string          `json:"app_id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	// TimeoutMS is how long the caller waits for the answer (its context's deadline); the hub stops there too. 0: none.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// The apps the hub runs for the assistant and their methods.
const (
	// AppConnectors is the hub's connectors (docs/extensions/connectors.md).
	AppConnectors = "connectors"
	// ConnectorsSnapshots: {tz} → {snapshots: [connectorwire.SnapshotData]}, the live states of the signed-in connectors
	// that keep one (the chats waiting on the user, today's meetings), those that answered within a few seconds.
	ConnectorsSnapshots = "connectors.snapshots"

	// AppInteractions is the hub's interaction queue (InteractionsGet, InteractionsAnswer).
	AppInteractions = "interactions"

	// AppInbox is the inbox's sources (inbox.go), answered in the manager's process by the extension manager
	// (internal/extmgr), not the hub; AppSettings is Persona's own settings (persona_settings).
	AppInbox    = "inbox"
	AppSettings = "settings"

	// AppNotify is the hub's push settings: NotifyQuietGet tells whether the user is in their quiet hours now.
	AppNotify = "notify"
	// NotifyQuietGet: {} → {quiet}: true while any of the user's devices is inside its quiet hours (#5806), read in
	// the hub's clock and the user's time zone. The Lark bot's nudges (#6084) wait for it to turn false.
	NotifyQuietGet = "notify.quiet"

	// AppTool: {name, arguments, machine_id, session_id} → proto.ToolResult, one of the assistant's tools that is the
	// app's to run (persona_settings).
	AppTool = "app.tool"
)

// What the interaction queue offers the assistant: data in, data out. The tools on it (interaction_answer) are the
// assistant's, with their rules and wording.
const (
	// InteractionsGet: {interaction_id} → {interaction, ok}, the interaction while it is pending; when it is not (ok
	// false), status says how it ended (resolved, dismissed, expired, cancelled; "" when there is no such interaction).
	InteractionsGet = "interactions.get"
	// InteractionsAnswer: {machine_id, session_id, interaction_id, answers: {question: answer}} → {ok, status}: the
	// pending question of that session resolved with answers, answered by the assistant; ok false (with status as
	// InteractionsGet's) when it is no longer pending. An interaction that is not a question, or not that session's,
	// is an error.
	InteractionsAnswer = "interactions.answer"
)

// ModelBinding is what a purpose runs on: a provider (launcher.<name> for a launcher) with a model and effort.
type ModelBinding struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Runtime  string `json:"runtime,omitempty"` // main chat only: cli (default) or agentsdk; kept across a provider change
}

// NotifyRequest is notify.request's params.
type NotifyRequest struct {
	Notification Notification `json:"notification"`
}

// Notification is one notification intent: a push (kind assistant) to every device whose settings take it, like an
// agent's. The hub delivers it as it is: it does not derive what it means.
type Notification struct {
	ID        string `json:"id"` // stable, for deduplication
	Kind      string `json:"kind"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Subtitle  string `json:"subtitle,omitempty"`  // the line under the title: where it comes from
	DeviceID  string `json:"device_id,omitempty"` // the machine of the session it opens
	SessionID string `json:"session_id,omitempty"`
	// Asked: the conversation asked for this push itself (notify_user): the conversation's own finished push is left
	// out. (The assistant sends it to the Feishu bot and the channels with pushes on itself.)
	Asked bool `json:"asked,omitempty"`
	// Interaction is a NotifyInput push's card.
	Interaction *proto.Interaction `json:"interaction,omitempty"`
	// TookMS and LongMS are a NotifyDone push's turn: how long it took, and from how long a turn counts as long (the
	// devices' "long turns only").
	TookMS int64 `json:"took_ms,omitempty"`
	LongMS int64 `json:"long_ms,omitempty"`
}

// NotifyPush is the kind of the assistant's pushes.
const NotifyPush = "assistant"

// Broadcast is ws.broadcast's params: a JSON object with a string "type", sent to the apps unchanged.
type Broadcast struct {
	Message json.RawMessage `json:"message"`
}

// Snapshot is manager.snapshot's params: the whole of what the assistant shows, every time (the hub keeps the
// newest by Seq and shows nothing it did not report).
type Snapshot struct {
	Epoch string    `json:"epoch"` // this run of the assistant
	Seq   uint64    `json:"seq"`   // increasing within Epoch
	At    time.Time `json:"at"`
	// Partial: some of the state could not be read (the database did not answer); the hub asks again (Resync) the
	// next time it shows it.
	Partial bool `json:"partial,omitempty"`
	// Sessions are the assistant's conversations it has something to say about.
	Sessions []SessionView `json:"sessions"`
	// Interactions are the questions it asks on the phone (ask_user: headless Claude has no AskUserQuestion), and its
	// cards about agents' turns (WorkCardTool). The hub shows each like any agent's and sends the answer back
	// (Handler.Action); one that leaves the list is withdrawn.
	Interactions []proto.Interaction `json:"interactions"`
	// Tasks are its scheduled tasks, as the hub's other parts need them (the model configuration's "Scheduled tasks"); null
	// while they could not be read.
	Tasks []TaskView `json:"tasks"`
	// Todos is which todo each agent works for (TodoReport); null while the todos could not be read.
	Todos *TodoReport `json:"todos"`
	// Profile is its settings as the hub's own features need them; null while they could not be read.
	Profile *Profile `json:"profile"`
	// Work is where the agents stand after their turns (agents.go); null while it could not be read.
	Work *WorkReport `json:"work"`
	// Channels is what the hub shows of the channels (ChannelsReport); null while it could not be read.
	Channels *ChannelsReport `json:"channels"`
	// Inbox is what the hub shows of the inbox (InboxReport, inbox.go); null while it could not be read.
	Inbox *InboxReport `json:"inbox,omitempty"`
}

// TaskView is one scheduled task in the snapshot.
type TaskView struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`  // its title, or what stands for one
	Repeat       string    `json:"repeat"` // once / daily / interval
	Enabled      bool      `json:"enabled"`
	At           time.Time `json:"at,omitempty"` // once: when; interval: the next run
	EveryMinutes int       `json:"every_minutes,omitempty"`
	MachineID    string    `json:"machine_id,omitempty"` // of the conversation it came from
	RunMachineID string    `json:"run_machine_id,omitempty"`
	RunSessionID string    `json:"run_session_id,omitempty"` // its own conversation
	// Push is what of a run reaches the phone: "" every run's answer, PushAsked only what the run sends with
	// notify_user (#2396). Such a task always runs in its own conversation, whose finished turns are not pushed.
	Push string `json:"push,omitempty"`
}

// PushAsked is a scheduled task's push that sends only what its runs push themselves (TaskView.Push).
const PushAsked = "asked"

// FinishedText is the words the finished push carries for s's turn, chosen as hub/machine.go notifyFinished chooses
// them: the work line it ended on (done or stuck), else its last words; "" when it said nothing (the push then says a
// localised "done", which is never a cut-off). NormalFinish judges a cut-off on them.
func FinishedText(s proto.Session) string {
	if w := s.Work; w != nil && w.Line != "" && (w.Phase == proto.WorkDone || w.Phase == proto.WorkBlocked) {
		return w.Line
	}
	return s.LastText
}

// NormalFinish: s's turn ended normally, as the finished push would tell it: no model error, no cut-off answer, not
// stuck on something. A quiet task's conversation (PushAsked) pushes nothing then; an error, a cut-off or a stuck
// turn is told as ever.
func NormalFinish(s proto.Session) bool {
	return strings.TrimSpace(s.TurnError) == "" && !CutOff(FinishedText(s)) && !(s.Work != nil && s.Work.Phase == proto.WorkBlocked)
}

// QuietTaskFinish: s, the own conversation of a task that pushes only what its runs send, may go unpushed now. Only
// when it runs on an agent whose failed turn is told on the session (reportsFailures), so that NormalFinish can tell
// "nothing new" from an error. Never the main conversation (main: its session id), which the user may have made of
// the task's. And only a normal finish (NormalFinish).
func QuietTaskFinish(s proto.Session, main string) bool {
	return reportsFailures(s) && (main == "" || s.SessionID != main) && NormalFinish(s)
}

// SupervisedFinish: s, a conversation whose current turn a supervision message started (SessionView.Supervising), may go
// unpushed now: a normal finish (NormalFinish); an error, a cut-off or a stuck turn is pushed as ever.
func SupervisedFinish(s proto.Session, supervising bool) bool {
	return supervising && reportsFailures(s) && NormalFinish(s)
}

// reportsFailures: s's agent tells a turn that failed on the model service. Claude Code (Agent "") writes the error
// in its last words (CutOff); a headless Codex's node reports the failed turn/completed as TurnError (#2571). A
// runtime's (Agent: its id, DSH…) is not known to, so its quiet task is pushed as ever.
func reportsFailures(s proto.Session) bool {
	return s.Agent == "" || s.Agent == "codex"
}

// SessionView is what the assistant reports about one of its conversations, put on the session lists the apps get.
type SessionView struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	// Title is "MMDD | kind | topic" or the user's own (titles.go); the main conversation's is always "Persona".
	Title    string   `json:"title,omitempty"`
	Archived bool     `json:"archived,omitempty"`
	Queued   []string `json:"queued,omitempty"` // messages from other conversations waiting for its turn to end
	// Origin is who opened the conversation (docs/protocol/protocol.md "Where assistant chats come from": user, channel, inbox, task, spawned,
	// lark_agent, extension) and OriginRef what of (the inbox item, the task, the conversation, the channel, the
	// extension); both "" while it is not known.
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`
	// Supervising: its current turn was started by a supervision message about the tasks it handed out (#3124): the assistant
	// does not push its normal finish (SupervisedFinish); it pushes what the user needs to know (notify_user).
	Supervising bool `json:"supervising,omitempty"`
}

// ErrStaleSnapshot is manager.snapshot's stale_snapshot: a newer one came in first (two changes reported at once).
var ErrStaleSnapshot = errors.New("stale_snapshot: a newer snapshot came in")

// SnapshotResult is manager.snapshot's answer.
type SnapshotResult struct {
	OK  bool   `json:"ok"`
	Seq uint64 `json:"seq"`
}

// ---- what the hub sends the assistant (Handler) ----

// RoleHeader carries the role of the device a relayed request came from (the hub's auth decides it and sets the
// header, replacing any the client sent). Absent (a test's own request) means a full device.
const RoleHeader = "X-Persona-Role"

// ExtensionHeader carries the extension a relayed request came from when RoleHeader is "extension" (set by the hub
// like RoleHeader; never the client's).
const ExtensionHeader = "X-Persona-Extension"

// AdminHeader is "1" on a relayed request that came through the hub's admin UI with a signed-in admin session (the
// hub's admin routes it relays, devicewire.AdminUsageRoutes); set by the hub like RoleHeader, never the client's.
// Its value is the one extwire.HeaderAdmin carries on the extensions' relay.
const AdminHeader = "X-Persona-Admin"

// DeviceNameHeader carries the name of the device a relayed request came from ("" for the hub's own token), so a
// manager can say who did something (a model profile's switch, docs/assistant/model-profiles.md); set by the hub like
// RoleHeader, never the client's.
const DeviceNameHeader = "X-Persona-Device-Name"

// ToolCall is assistant.tool's params.
type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	MachineID string          `json:"machine_id"` // the machine of the conversation that called it
	SessionID string          `json:"session_id"` // the conversation; "" when it is not known
}

// SessionRef names one session on one machine.
type SessionRef struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

// ContextParams is assistant.context's params. MachineID is the asking node's machine: the hub sets it from the
// node's connection, never from the node's params. With Ack the call only confirms that a message carrying those
// view errors (ContextResult.ViewErrors) was delivered, and answers nothing.
type ContextParams struct {
	SessionID string             `json:"session_id"`
	MachineID string             `json:"machine_id,omitempty"`
	Ack       []ViewErrorReceipt `json:"ack,omitempty"`
}

// ContextResult is assistant.context's answer: the <persona-context> text, and the <persona-view-errors> block of the
// conversation's rich views that failed their check and were not reported yet (docs/assistant/assistant-views.md "Feedback next turn").
// Reading it consumes nothing: the node acks Receipts once the message is delivered.
type ContextResult struct {
	Text       string             `json:"text"`
	ViewErrors string             `json:"view_errors,omitempty"`
	Receipts   []ViewErrorReceipt `json:"view_error_receipts,omitempty"`
}

// ViewErrorReceipt names the reported errors of one reply as they were read: an ack whose digest no longer matches
// (the errors changed since) confirms nothing.
type ViewErrorReceipt struct {
	ItemID string `json:"item_id"`
	Digest string `json:"digest"`
}

// Action is manager.action's params (appkit.Action): op answer, with the user's answer (a proto.Resolution).
type Action struct {
	ActionID           string          `json:"action_id"`
	SessionID          string          `json:"session_id,omitempty"`
	Op                 string          `json:"op"` // answer
	TurnID             string          `json:"turn_id,omitempty"`
	InteractionID      string          `json:"interaction_id"`
	InteractionVersion uint64          `json:"interaction_version,omitempty"`
	Answer             json.RawMessage `json:"answer,omitempty"`
}

// ActionResult is manager.action's answer.
type ActionResult struct {
	OK  bool   `json:"ok"`
	Seq uint64 `json:"seq"`
}

// InteractionResolved is interaction.resolved's params: the card, the answer, and resolved / dismissed / failed.
type InteractionResolved struct {
	Interaction proto.Interaction `json:"interaction"`
	Resolution  proto.Resolution  `json:"resolution"`
	Status      string            `json:"status"`
}

// InteractionRaised is interaction.raised's params: an agent of a quiet task (#1731) asks the user something; the
// hub sends no push for it, the main conversation is told instead. The card carries its task's title.
type InteractionRaised struct {
	Interaction proto.Interaction `json:"interaction"`
}

// MessageCard is a message the inbox's AI twin drafted a reply to, about one of the user's tasks: what the card in the
// task's conversation shows until the item is sent or done (the app sends the draft through the inbox, by the item's
// id).
type MessageCard struct {
	TodoID  string `json:"todo_id"`
	Source  string `json:"source"`            // lark | mail | …
	Account string `json:"account,omitempty"` // Lark / the mail address
	Chat    string `json:"chat"`              // the chat's name / the mail's subject
	Sender  string `json:"sender,omitempty"`
	Text    string `json:"text"` // the message, clipped
	Draft   string `json:"draft"`
	// SentAt: the reply to the item went out (from this card or from the inbox, on any device) and Sent is what it
	// said; set when the inbox sent the reply (the inbox is the assistant's too), never cleared, so the card stays sent
	// when the item opens again. A card sent before that was recorded has no Sent (the draft is what went out).
	SentAt *time.Time `json:"sent_at,omitempty"`
	Sent   string     `json:"sent,omitempty"`
}

// APIErrorMarkers start what Claude Code answers when the model call itself failed (an error card in the app). Both
// sides read a turn by them: the hub's session lists say an agent's turn was cut off (proto.Session.TurnError), the
// assistant's scheduled runs and tasks act on it. "Not logged in · Please run /login" is what Claude Code answers when
// it has no credentials (a model of its own, "<synthetic>"): the turn did not run, the user must sign in
// (docs/assistant/onboarding.md 4.4). Its separator is in the marker, so a reply that talks about being logged out is a reply.
var APIErrorMarkers = []string{"API Error:", "There's an issue with the selected model", "Failed to authenticate",
	"OAuth token has expired", "Invalid API key", "Not logged in ·"}

// InterruptedSummary is what is said of a turn the model service cut off, instead of its last words (the error is no
// conclusion, #1516): an interrupted task's receipt, the push.
const InterruptedSummary = "模型服务出错，中断了"

// CutOff: an agent's answer is the model API error that ended its turn (it starts with one).
func CutOff(answer string) bool {
	answer = strings.TrimSpace(answer)
	for _, marker := range APIErrorMarkers {
		if strings.HasPrefix(answer, marker) {
			return true
		}
	}
	return false
}

// DeviceSessions is sessions.changed's params: the list as the node sent it, and when the node's connection to the hub
// began (a new one: the node may have restarted and forgotten what it was told, e.g. the herdr todo marks).
type DeviceSessions struct {
	MachineID string          `json:"machine_id"`
	Sessions  []proto.Session `json:"sessions"`
	Connected time.Time       `json:"connected,omitzero"`
}

// ---- the todos (the assistant's, docs/assistant/todos.md) ----

// TodoReport is what the hub shows of the todos: the todo each agent works for now (the open todos' active links: the
// session lists' todo, the task named on its agents' cards) and the agents kept quiet (#1731: no pushes, badges or Live
// Activity; those of quiet tasks open or done in the last week). Small, whole every time.
type TodoReport struct {
	Links []TodoLink   `json:"links"`
	Quiet []SessionRef `json:"quiet,omitempty"`
}

// TodoLink is one agent that works for an open todo.
type TodoLink struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	TodoID    string `json:"todo_id"`
	Title     string `json:"title"` // the todo's
	// Task: the todo is a task an assistant conversation opened (its agents' cards name it: proto.Interaction.TaskTitle).
	Task bool `json:"task,omitempty"`
}

// TodoHandoff is assistant.todo_handoff's params: an agent is handed work for a todo (POST /api/v1/sessions or
// …/messages with todo_id); Text is what it is handed ("" when only the todo is wanted).
type TodoHandoff struct {
	TodoID string `json:"todo_id"`
	Text   string `json:"text,omitempty"`
	// New: Text is a new agent's first prompt; the background of the project its directory (Cwd on MachineID) belongs
	// to goes before it too (#2820). An older assistant ignores these.
	New       bool   `json:"new,omitempty"`
	MachineID string `json:"machine_id,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
}

// TodoHandoffResult is assistant.todo_handoff's answer: the todo (Found false: no such todo) and Text with the todo's
// decisions in effect before it (as it was without any, or when they could not be read). Status and Error, when set,
// say the hand-off may not go (404: no such todo, 409: it is done); the hub answers the app with them as they are.
type TodoHandoffResult struct {
	Found  bool       `json:"found"`
	Title  string     `json:"title,omitempty"`
	DoneAt *time.Time `json:"done_at,omitempty"`
	Text   string     `json:"text,omitempty"`
	Status int        `json:"status,omitempty"`
	Error  string     `json:"error,omitempty"`
}

// TodoLinkParams is assistant.todo_link's params: the agent handed work for a todo works for it from now on (a link
// by hand; title, cwd and agent as the session has them). Prompt is what the agent was started with: without a
// title, the link's comes from it (its first line past a context block).
type TodoLinkParams struct {
	TodoID    string `json:"todo_id"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
}

// TodoLinkResult is assistant.todo_link's answer: the todo's title (the session the start answers names it).
type TodoLinkResult struct {
	Title string `json:"title,omitempty"`
}

// TodoSpaceRenamed is todos.space_renamed's params: the hub renamed a workspace (PATCH …/spaces/{wid}); a todo linked
// to it by its name follows.
type TodoSpaceRenamed struct {
	MachineID string `json:"machine_id"`
	Old       string `json:"old"`
	New       string `json:"new"`
}

const (
	// MethodTodoHandoff: TodoHandoff → TodoHandoffResult (the hub calls it before an agent is handed work for a todo).
	MethodTodoHandoff = "assistant.todo_handoff"
	// MethodTodoLink: TodoLinkParams → TodoLinkResult (after the hand-off went through); an error says why it was not linked
	// (db.ErrTodoNotFound / ErrTodoCompleted's words).
	MethodTodoLink = "assistant.todo_link"
	// EventTodoSpaceRenamed: TodoSpaceRenamed, one way.
	EventTodoSpaceRenamed = "todos.space_renamed"
)

// ---- the bus ----

// AssistantID is the bus identity the assistant connects as (a manager's token, hub/bustokens.go).
const AssistantID = "assistant"

// Capabilities a manager names in manager.hello's capabilities (stable names: never renamed or reused).
const (
	// CapabilityConfigOwn: the manager reads its own sections of the configuration from its manager.json (owner,
	// notes, Agents, the knowledge base, the models, a card's wait): the hello does not carry them any more, and a
	// manager without it runs on its built-in defaults (docs/assistant/managers.md "Configuration").
	CapabilityConfigOwn = "config.own"
	// CapabilityAgentSessionArchives: the manager owns recoverable Codex/Claude archives.
	CapabilityAgentSessionArchives = "agent_session_archives"
	// CapabilityAgentSessionArchiveClose: the manager's archive also takes ?close=1, "Archive and close", which closes
	// the agent on its computer (session.retire) once archived (docs/protocol/protocol.md "Agent session archives").
	CapabilityAgentSessionArchiveClose = "agent_session_archive_close"
	// CapabilityMachinesOwn: the manager keeps its own list of computers in its manager.json and takes the hub's edits
	// of it (devicewire MethodMachinesPut / Remove / Get) once the hub hands it MachinesOwn
	// (docs/assistant/managers.md "Each manager's computers").
	CapabilityMachinesOwn = "machines.own"
)

// The methods and events, by direction. The assistant's handshake is manager.hello {id, host, os, arch, deploy,
// version, capabilities}; its answer is Hello (with the hub's name, role and managers besides).
const (
	// The assistant calls the hub (Hub).
	MethodHello    = "manager.hello"
	MethodDevice   = "device.call"
	MethodApp      = "app.call"
	MethodNotify   = "notify.request"
	MethodSnapshot = "manager.snapshot"
	EventBroadcast = "ws.broadcast" // one way

	// The hub calls the assistant (Handler).
	MethodHTTP    = "http.request" // HTTPRequest → HTTPResponse
	MethodTools   = "assistant.tools"
	MethodTool    = "assistant.tool"
	MethodContext = "assistant.context"
	MethodAction  = "manager.action"

	// The hub tells the assistant (one way).
	EventUserSpoke           = "session.user_spoke"
	EventUserStopped         = "session.user_stopped" // the user stopped an assistant conversation's turn in an app
	EventTurnStarted         = "session.turn_started"
	EventTurnEnded           = "session.turn_ended"
	EventSessionsChanged     = "sessions.changed"
	EventInteractionResolved = "interaction.resolved"
	EventInteractionRaised   = "interaction.raised"
	EventUpdated             = "manager.updated" // Hello again: hub.json's assistant section or the machines changed
	EventResync              = "manager.resync"  // {}: report the whole snapshot again
)

// Error codes that cross the bus where a Go error would lose its kind (rpc.Error.Code).
const (
	CodeNoDevice         = "no_device"         // ErrNoDevice
	CodeMachineOffline   = "machine_offline"   // the machine's node is not connected (apierr.ErrOffline)
	CodeNodeClosed       = "node_closed"       // the link to the machine's node dropped during the call (rpc.ErrClosed)
	CodeStaleSnapshot    = "stale_snapshot"    // ErrStaleSnapshot
	CodeAssistantOffline = "assistant_offline" // the hub has no connection from the assistant
	// CodeFeishuAPI: Feishu answered the bot's call with an error code (Data: {"code": n}): a refusal or a passing
	// failure the assistant tells apart (the agent messages, feishu_cot.go).
	CodeFeishuAPI = "feishu_api"
	// CodeChatDenied: the hub refused to send into a chat or to reply to a message in one, because the bot may only use
	// the owner's private chat and the group chats the owner opted in. Nothing was sent.
	CodeChatDenied = "chat_denied"
)

// HTTPRequest is http.request's params: an app's request on one of Routes, with an escaped URL path (header without
// the hub's credentials and hop-by-hop headers, the asking device's role in RoleHeader; body as base64).
type HTTPRequest struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

// HTTPResponse is http.request's answer, written back to the app unchanged.
type HTTPResponse struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

// Routes are the routes the assistant answers (net/http patterns): the hub relays the apps' requests on them as
// http.request. The assistant registers exactly these (assistant.TestRoutesAreTheWireOnes).
var Routes = []string{
	"GET /api/v1/sessions/archived",
	"POST /api/v1/sessions/{mid}/{sid}/archive",
	"POST /api/v1/sessions/{mid}/{sid}/unarchive",
	"POST /api/v1/assistant/conversations/{id}/archive",
	"GET /api/v1/assistant/conversations/{id}/always",
	"DELETE /api/v1/assistant/conversations/{id}/always/{hash}",
	"POST /api/v1/assistant/conversations/{id}/unarchive",
	"POST /api/v1/assistant/conversations/{id}/stop",
	"GET /api/v1/assistant/reporters",
	"GET /api/v1/assistant/conversations/{id}/feed",
	"POST /api/v1/assistant/conversations/{id}/route",
	"POST /api/v1/assistant/conversations/{id}/today",
	"GET /api/v1/assistant/tasks",
	"POST /api/v1/assistant/tasks",
	"PUT /api/v1/assistant/tasks/{id}",
	"DELETE /api/v1/assistant/tasks/{id}",
	"POST /api/v1/assistant/tasks/{id}/run",
	"POST /api/v1/assistant/tasks/{id}/fresh",
	"GET /api/v1/assistant/runs",
	"GET /api/v1/assistant/routines",
	"PUT /api/v1/assistant/routines",
	"POST /api/v1/assistant/routines/{id}/run",
	"GET /api/v1/assistant/followups",
	"POST /api/v1/assistant/followups/{id}/run",
	"DELETE /api/v1/assistant/followups/{id}",
	"GET /api/v1/task-plans/{id}",
	"POST /api/v1/task-plans/{id}/start",
	"POST /api/v1/task-plans/{id}/dismiss",
	"GET /api/v1/task-trust",
	"DELETE /api/v1/task-trust",
	// The todos (the assistant's todos.go, docs/assistant/todos.md).
	"GET /api/v1/todos",
	"POST /api/v1/todos",
	"PATCH /api/v1/todos/{id}",
	"DELETE /api/v1/todos/{id}",
	"PUT /api/v1/todos/{id}/spaces/{mid}/{space...}",
	"DELETE /api/v1/todos/{id}/spaces/{mid}/{space...}",
	"PUT /api/v1/todos/{id}/agents/{mid}/{sid}",
	"DELETE /api/v1/todos/{id}/agents/{mid}/{sid}",
	"GET /api/v1/todos/{id}/decisions",
	"POST /api/v1/todos/{id}/decisions",
	"PATCH /api/v1/todos/{id}/decisions/{did}",
	"PUT /api/v1/todos/{id}/thread",
	"DELETE /api/v1/todos/{id}/thread",
	"POST /api/v1/todos/{id}/accept",
	"POST /api/v1/later/{id}/handled", // a question that can wait, marked handled (later.go)
	"POST /api/v1/todos/{id}/done-check",
	"POST /api/v1/todos/{id}/retry",
	"POST /api/v1/todos/retitle",
	// Its settings and its main conversation (profile.go, start.go).
	"GET /api/v1/assistant",
	"PUT /api/v1/assistant",
	"POST /api/v1/assistant/warm",
	"GET /api/v1/assistant/readiness",
	"POST /api/v1/assistant/readiness/dismiss",
	"GET /api/v1/assistant/memory",
	"PUT /api/v1/assistant/memory/{name}",
	"DELETE /api/v1/assistant/memory/{name}",
	"GET /api/v1/assistant/capabilities",
	"POST /api/v1/assistant/conversations",
	"POST /api/v1/assistant/conversations/{id}/compact",
	"POST /api/v1/assistant/main/new",
	"POST /api/v1/assistant/origins/backfill",
	// The channels (channels.go there, docs/extensions/channels.md "API").
	"GET /api/v1/channels",
	"GET /api/v1/channels/{id}",
	"PUT /api/v1/channels/{id}",
	"DELETE /api/v1/channels/{id}/binding",
	"POST /api/v1/assistant/conversations/{id}/views/{key}/actions",
	"POST /api/v1/assistant/conversations/{id}/views/{key}/html-actions",
	"POST /api/v1/channels/{id}/test",
	// The knowledge base (the assistant's kb_routes.go, docs/assistant/knowledge.md "App API").
	"GET /api/v1/kb/{kind}",
	"GET /api/v1/kb/{kind}/{id}",
	"PUT /api/v1/kb/{kind}/{id}",
	"GET /api/v1/kb/moments/{id}",
	"GET /api/v1/kb/chats/{id}/context",
	"GET /api/v1/kb/chats/{id}/days/{day}",
	"GET /api/v1/kb-search",
	// The phone's recordings (kb_recordings.go there, docs/protocol/protocol.md "Recording sync").
	"PUT /api/v1/recordings/{id}",
	"DELETE /api/v1/recordings/{id}",
	"GET /api/v1/recordings",
}

// ---- the assistant's settings, and the conversations the hub's features start (docs/assistant/assistant.md "The boundary with the hub") ----

// Profile is the assistant's settings (settings `assistant`, its own) as the hub's own features need them, in every
// snapshot: a phone's message to the main conversation is told apart, the model configuration's main chat runs on its
// machine, the hub reads times in the user's zone, and the nodes keep listing Keep (the Feishu bot and the channels are
// the assistant's own). The hub keeps the last
// one; before one came in it goes by hub.json's assistant section, its own time zone and no main conversation.
type Profile struct {
	MachineID string `json:"machine_id"` // where its conversations run
	Main      string `json:"main,omitempty"`
	// Launcher is the one it runs on when "Models" › main chat names none (its own setting, else hub.json's, else claude):
	// that purpose's default.
	Launcher string `json:"launcher"`
	Timezone string `json:"timezone,omitempty"` // the user's (the phone reports it), IANA; "" while not known
	// Keep are the conversations their machines list however long they are quiet: the main one, the main ones it
	// replaced, scheduled tasks' own (node.configure keep_sessions: a busy machine's newest transcripts crowd them out).
	Keep []SessionRef `json:"keep"`
}

// The hub asks the assistant (Handler) for what its own features need of the assistant's conversations.
const (
	// MethodStart: StartParams → {session}: a conversation the hub's features start (the inbox's AI twin).
	MethodStart = "assistant.start"
	// MethodSendParams: SendParams → {params}: a message for one of its conversations (the phone's, one the node sends
	// by itself) with the launcher and model it runs on, and what
	// the old main conversation left before the text of the first one to a new main conversation started empty.
	MethodSendParams = "assistant.send_params"
	// MethodRename: RenameParams → the node's answer: the user renames one of its conversations in the app (the main
	// one has no title to change, a Codex one only its tab); the new title is the user's from now on.
	MethodRename = "assistant.rename"
)

// The node's methods the assistant calls for its directory, its conversations and its settings
// (docs/protocol/protocol.md "hub ↔ node"), passed through by the hub's device.call like the others.
const (
	// DeviceAssistantPrepare: {dir, claude_md, connectors, clis, plugin_skills} → {cwd, plugin_skills_need}.
	DeviceAssistantPrepare = "assistant.prepare"
	// DeviceAssistantGuestPrepare: {dir, claude_md} → {cwd}: a guest conversation's directory (assistant/guest.go), only
	// its CLAUDE.md / AGENTS.md written; the assistant's connectors, CLIs and skills are left as they are. A node without
	// it answers "unknown method": no guest conversation starts there.
	DeviceAssistantGuestPrepare = "assistant.guest_prepare"
	// DeviceAssistantWarm: {cwd, launcher, …model} → {}: a process ready for the next new conversation.
	DeviceAssistantWarm = "assistant.warm"
	// DeviceAssistantMemory: {op, cwd, name?, content?} → the memory files (op list) or {}.
	DeviceAssistantMemory = "assistant.memory"
	// DeviceAssistantCompact: {session_id} → {}.
	DeviceAssistantCompact = "assistant.compact"
	// DeviceAssistantHandoff: {session_id} → {summary, recent}: what a main conversation leaves for the next one.
	DeviceAssistantHandoff = "assistant.handoff"
	// DeviceLaunchersList: {} → {launchers}, the node's own.
	DeviceLaunchersList = "launchers.list"
	// DeviceLaunchersProbe: {name} → {detail}.
	DeviceLaunchersProbe = "launchers.probe"
)

// StartParams is assistant.start's params.
type StartParams struct {
	Text           string        `json:"text"`
	Images         []proto.Image `json:"images,omitempty"`
	Launcher       string        `json:"launcher,omitempty"`
	PermissionMode string        `json:"permission_mode,omitempty"`
	Effort         string        `json:"effort,omitempty"`
	Purpose        string        `json:"purpose,omitempty"`    // the model configuration's purpose; "" is the main chat's
	MachineID      string        `json:"machine_id,omitempty"` // "" is the assistant's machine
	// Space: "health" starts it in the health space (its own directory, instructions and memory, docs/assistant/health.md);
	// "" is the assistant's own.
	Space string `json:"space,omitempty"`
	// Main: it becomes the main conversation (what the old one left goes before Text).
	Main bool `json:"main,omitempty"`
	// Untitled: not one of the conversations it names and lists as its own (the inbox's AI twin: its result is the
	// draft in the inbox).
	Untitled bool `json:"untitled,omitempty"`
	// Origin and OriginRef: who opens it (SessionView.Origin); "" is the user's.
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`
	// GuestTools: a guest conversation (assistant/guest.go) that may use only these connector tools; nil is an
	// ordinary one.
	GuestTools []string `json:"guest_tools,omitempty"`
}

// SendParams is assistant.send_params's params: Params are the node's assistant.send / session.send params.
type SendParams struct {
	MachineID string         `json:"machine_id"`
	SessionID string         `json:"session_id"`
	Params    map[string]any `json:"params"`
}

// RenameParams is assistant.rename's params: Params are the app's ({title?, tab_name?}).
type RenameParams struct {
	MachineID string         `json:"machine_id"`
	SessionID string         `json:"session_id"`
	Params    map[string]any `json:"params"`
}

// CodeBadRequest is what the assistant refuses a request of the hub's with when the app's request itself is wrong
// (the hub answers it 400 with the message).
const CodeBadRequest = "bad_request"

// What the hub's apps tell the assistant for its directory and its settings.
const (
	// ConnectorsSignedIn: {} → {connectors, clis, lark_cli}: the connectors switched on and signed in (rate limited
	// counts), their command lines (name → connector id), and whether hub.json sets up lark-cli.
	ConnectorsSignedIn = "connectors.signed_in"

	// AppPlugins is the hub's plugins (docs/extensions/extensions.md "Assistant").
	AppPlugins = "plugins"
	// PluginsAssistant: {machine_id} → {instructions, skills}: the plugins' parts of the assistant's instructions, and
	// their skills for that machine (files only with those it was not sent yet).
	PluginsAssistant = "plugins.assistant"
	// PluginsSkillsNeed: {machine_id, names} → {}: the machine's node lacks these skills (its assistant.prepare said
	// plugin_skills_need): their files go next time.
	PluginsSkillsNeed = "plugins.skills_need"

	// SettingsList: {} → {settings: [Setting]}: the hub's own settings persona_settings offers; app.tool persona_settings
	// runs get / set / add / remove on them (a change waits for the user's approval on the phone).
	SettingsList = "settings.list"
)

// Setting is one of the hub's settings persona_settings offers.
type Setting struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	About   string   `json:"about"`
	Value   string   `json:"value,omitempty"` // what set / add take, for the model
	Actions []string `json:"actions"`
}

// ---- the channels: the Feishu bot and the chat app bots (docs/extensions/channels.md, docs/extensions/bots.md) ----

// Which conversation a channel talks to, following the turn and sending its answer back, what is asked there and its
// answer, the commands, the pushes and the sync mirror are the assistant's; the hub keeps the transport — the Feishu
// open platform's long connection and REST (pairing among them), the plugins' channel programs — and hands the
// assistant what comes in. The channels' routes are the assistant's (Routes) but binding (POST …/bind: credentials,
// sign-in fields, pairing): no secret of a channel or the bot passes through the assistant. /api/v1/bots/feishu* stays
// the hub's. What answers a card in a channel carries the owner's action (OwnerAction): the hub issued it when it
// handed the owner's press or message over, and takes it once — the assistant can't approve anything on its own.
const (
	// MethodChannelReceive: ChannelReceive → {}: the owner's message from a plugin's channel, taken (not its turn).
	MethodChannelReceive = "channels.receive"
	// MethodFeishuMessage: FeishuMessage → {}: the owner's message in the Feishu bot's private chat, taken.
	MethodFeishuMessage = "feishu.message"
	// MethodFeishuAction: FeishuAction → the card callback's answer (feishubot.CardResponse): the owner pressed one
	// of the bot's cards. Feishu waits 3 seconds for it.
	MethodFeishuAction = "feishu.card_action"
	// MethodFeishuView: {} → FeishuView, what GET /api/v1/bots/feishu shows of the bot's chat.
	MethodFeishuView = "feishu.view"

	// EventInteractionAnnounced: InteractionAnnounced, one way: a new card (not a quiet task's).
	EventInteractionAnnounced = "interaction.announced"
	// EventInteractionEnded: InteractionEnded, one way: a card is over — answered (with the resolution), withdrawn,
	// expired.
	EventInteractionEnded = "interaction.ended"
	// EventSessionItems and EventSessionDelta: SessionItems / SessionDelta, one way: a conversation the assistant
	// follows (DeviceSessionFollow) has new items, or is writing text.
	EventSessionItems = "session.items"
	EventSessionDelta = "session.delta"
	// EventFeishuOwner: FeishuOwner, one way: the Feishu bot was paired with its owner, or unpaired.
	EventFeishuOwner = "feishu.owner"
	// EventFeishuRecalled: FeishuRecalled, one way: the owner recalled a message in the bot's private chat.
	EventFeishuRecalled = "feishu.recalled"
	// EventChannelNoted: ChannelNoted, one way: a channel's message went into a conversation through the apps' routes
	// (a voice conversation's errand: client_msg_id voice-…).
	EventChannelNoted = "channels.noted"

	// The hub's device methods for the channels' turns.
	// DeviceSessionFollow: {session_id} → {}: the conversation's new items and the text it writes come as
	// EventSessionItems / EventSessionDelta until DeviceSessionUnfollow, or until the assistant disconnects.
	DeviceSessionFollow   = "session.follow"
	DeviceSessionUnfollow = "session.unfollow"
	// DeviceSessionSendOnce: the node's session.send params (with client_msg_id) → {}: sent under the apps' once-only
	// guard — a message id already delivered is not sent again, one whose send was cut off is refused.
	DeviceSessionSendOnce = "session.send_once"

	// AppFeishu is the Feishu bot's transport (the open platform, as the paired owner's bot).
	AppFeishu = "feishu"
	// FeishuStatusGet: {} → FeishuStatus.
	FeishuStatusGet = "feishu.status"
	// FeishuPost: {reply_to?, card, fallback?, uuid?, no_detour?} → {message_id}: a card (JSON 2.0) as a reply, else
	// into the owner's chat; one Feishu refuses goes again as rich text (fallback, Markdown). uuid is the caller's key
	// for a send that may be made again (a retried morning brief): the same key lands once, and a reply under it is
	// not dropped as a repeat of another with the same words. It may be made of letters, digits, ":", "_" and "-" only
	// (anything else is ignored, as if none was given) and is cut to 46 characters; without one a reply's key is made
	// of its parent and content. no_detour? makes a reply whose parent was withdrawn or deleted fail (feishubot.MessageGone
	// tells why) in place of going into the chat as a message of its own.
	// chat_id? names another chat to send into (and to fall back to when the parent was withdrawn) and in_thread? puts
	// a reply in the parent's thread; the hub refuses any chat but the owner's private chat and the group chats the
	// owner opted in, and with neither field the call is what it always was.
	FeishuPost = "feishu.post"
	// FeishuText: {reply_to?, text, chat_id?, in_thread?, uuid?, no_detour?} → {message_id}: plain text as a reply,
	// else into the owner's chat; chat_id, in_thread, uuid (cut to 50 characters here) and no_detour are FeishuPost's.
	// One more place is open to it (#6146): a reply to a message the hub took as the owner's "@bot /ai …" in a group that
	// is not opted in (FeishuFromElsewhere), which lands under that message and nowhere else in the group (at most
	// 2000 characters, never a detour into the chat); a send into that group, or a reply to any other message in it,
	// is refused as ever.
	FeishuText = "feishu.text"
	// FeishuUpdateCard: {message_id, card} → {}.
	FeishuUpdateCard = "feishu.update_card"
	// FeishuCotCreate: {reply_to?} → {cot_id, message_id}: an agent message (the open platform's message_cot, a
	// collapsible process) under the message replied to, else into the owner's chat.
	FeishuCotCreate = "feishu.cot_create"
	// FeishuCotAppend: {cot_id, message_id, events: [feishubot.CotEvent]} → {}.
	FeishuCotAppend = "feishu.cot_append"
	// FeishuCotComplete: {cot_id, message_id, reason: done | error} → {}: one that will not get its RUN_FINISHED.
	FeishuCotComplete = "feishu.cot_complete"
	// FeishuReact: {message_id, emoji} → {reaction_id}; FeishuUnreact: {message_id, reaction_id} → {}.
	FeishuReact   = "feishu.react"
	FeishuUnreact = "feishu.unreact"
	// FeishuMessages: {message_id} → {items: [feishubot.Item]}: a message read back (a quote; a merged forward with
	// the messages in it).
	FeishuMessages = "feishu.messages"
	// FeishuResource: {message_id, key, kind} → {data, mime}: an image of a message the bot received (at most 3 MB).
	FeishuResource = "feishu.resource"
	// FeishuSave: {message_id, key, kind, name, machine_id} → {path}: a file (or a large image) of a message saved on
	// a machine (its temp directory, like the apps' uploads), at most 50 MB.
	FeishuSave = "feishu.save"
	// FeishuUserName: {open_id} → {name}: a user's name (the Chinese for "someone" when it can't be read; the owner's falls back to hub.json's).
	FeishuUserName = "feishu.user_name"
	// FeishuConfigure: {enabled?, features?: {id: on}} → {}: the bot switched, its feature switches changed (only
	// those given).
	FeishuConfigure = "feishu.configure"
	// FeishuTest: {} → {}: the test card to the owner.
	FeishuTest = "feishu.test"

	// AppChannels is the plugins' channel programs, and the list of every channel as the transport has it.
	AppChannels = "channels"
	// ChannelsList: {} → {channels: [channel.View]}: every channel without what is the assistant's (targets, the
	// plugin channels' features); the Feishu bot's features are its switches.
	ChannelsList = "channels.list"
	// ChannelsSend: {channel, ref, text, kind, reply_to?} → {message_id?}: one piece to the owner (retried with the same
	// ref); CodeCannotSend when there is nowhere to send to yet.
	ChannelsSend = "channels.send"
	// ChannelsTyping: {channel, on} → {}.
	ChannelsTyping = "channels.typing"
	// ChannelsEnable: {channel, on} → {}: the plugin (and its connector), or the Feishu bot, switched.
	ChannelsEnable = "channels.enable"
	// ChannelsUnbind: {channel} → {}: signed out, unpaired.
	ChannelsUnbind = "channels.unbind"

	// InteractionsResolve: {interaction_id, resolution, action, channel} → {ok, status}: the card answered as the user
	// answered it in channel. action is the owner's action the hub handed over with that press or message there
	// (single use; a card's is for that card only, a message's for a kind of card the channel takes a typed answer
	// for); without one, or with another channel's, the hub refuses. ok false (status as InteractionsGet's) when it is no
	// longer pending, an error when the answer does not fit it.
	InteractionsResolve = "interactions.resolve"

	// CodeCannotSend: a channel has nowhere to send to yet (channel.CodeCannotSend); CodeNeedsTriageModel: a plugin
	// can't be switched on before the inbox's triage model is set (409 to the app).
	CodeCannotSend       = "cannot_send"
	CodeNeedsTriageModel = "needs_triage_model"
	// CodeTryAgain: the assistant could not take an owner's message now (what it needs of the hub did not answer):
	// the hub hands it over again, as after a cut-off call.
	CodeTryAgain = "try_again"
)

// ChannelReceive is channels.receive's params.
type ChannelReceive struct {
	Channel string          `json:"channel"`
	Message json.RawMessage `json:"message"` // channel.Message
	// Action is the owner's action: what answers a card with this message passes it (InteractionsResolve).
	Action string `json:"action,omitempty"`
}

// FeishuMessage is feishu.message's params: im.message.receive_v1's event as Feishu sent it (the owner's, in the
// bot's private chat), and the owner's action for what this message answers (InteractionsResolve).
type FeishuMessage struct {
	Event  json.RawMessage `json:"event"`
	Action string          `json:"action,omitempty"`
	// From says where the hub took the message from when it is not the owner's private chat or a topic of a group the
	// owner opted in: FeishuFromElsewhere is the owner's "@bot /ai …" in a group that is not opted in (#6146), the only
	// thing the hub lets through from one. "" for the usual two.
	From string `json:"from,omitempty"`
}

// FeishuFromElsewhere is FeishuMessage.From of a message the hub took from a group chat that is not opted in: the owner's
// "@bot /ai …" and nothing else. The assistant opens a to-do for it in the home topic group, or in the owner's private chat when there is none (assistant/feishu_elsewhere.go).
const FeishuFromElsewhere = "elsewhere"

// FeishuAction is feishu.card_action's params: card.action.trigger's event as Feishu sent it (the owner's press), and
// the owner's action for the card it presses (InteractionsResolve).
type FeishuAction struct {
	Event  json.RawMessage `json:"event"`
	Action string          `json:"action,omitempty"`
}

// FeishuView is feishu.view's answer.
type FeishuView struct {
	Target string        `json:"target,omitempty"` // where the chat's messages go now
	Agents []FeishuAgent `json:"agents,omitempty"` // agents bound to threads of the chat
}

// FeishuAgent is an agent bound to a thread of the bot's chat.
type FeishuAgent struct {
	Title     string `json:"title"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

// ChannelNoted is channels.noted's params.
type ChannelNoted struct {
	Channel   string `json:"channel"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

// FeishuOwner is feishu.owner's params.
type FeishuOwner struct {
	Paired bool `json:"paired"`
}

// FeishuRecalled is feishu.recalled's params: the message the owner recalled.
type FeishuRecalled struct {
	MessageID string `json:"message_id"`
}

// FeishuFeatures are the Feishu bot's switches (settings bots.feishu, the hub's with its credentials).
type FeishuFeatures struct {
	Main     bool `json:"main"`
	Cards    bool `json:"cards"`
	AllCards bool `json:"all_cards"`
	Pushes   bool `json:"pushes"`
	Sync     bool `json:"sync"`
	Agents   bool `json:"agents"`
	Reports  bool `json:"reports"` // the main conversation's tasks are told in the chat (#2768)
	Board    bool `json:"board"`   // /tasks keeps one standing "waiting for you" card, edited in place (#6052)
}

// FeishuStatus is feishu.status's answer.
type FeishuStatus struct {
	// Ready: switched on, connected and paired: the bot can post now.
	Ready    bool           `json:"ready"`
	Enabled  bool           `json:"enabled"`
	Features FeishuFeatures `json:"features"`
	Owner    string         `json:"owner,omitempty"` // the owner's open id ("" before pairing)
	// PairedAt is when the owner paired: with Owner, which pairing the chat's state belongs to (a new one, or none,
	// starts it afresh).
	PairedAt time.Time `json:"paired_at,omitzero"`
	// PublicURL is the hub's web app (a card's "Open web app").
	PublicURL string `json:"public_url,omitempty"`
	// Chats are the chats the bot may send into for the assistant (the hub's own rule for feishu.post / feishu.text):
	// the owner's private chat and the group chats the owner opted in. A thread the assistant posts into is checked
	// against it before the call, so nothing goes to a chat an agent made up.
	Chats []string `json:"chats,omitempty"`
	// HomeChat is the home topic group (#6146): the opted-in group the owner marked with "/topics home", else the first
	// one opted in; "" while none is. It is one of Chats. Where a request from a group that is not opted in opens its topic.
	HomeChat string `json:"home_chat,omitempty"`
	// NudgeAfter is the bot's setting nudge_after (#6084): minutes a card waits unanswered before the one nudge under it,
	// 0 off. nil (a status from before the setting) is NudgeAfterDefault.
	NudgeAfter *int `json:"nudge_after,omitempty"`
}

// NudgeAfterDefault is nudge_after when never set: minutes a card of the Lark bot waits before it is nudged.
const NudgeAfterDefault = 60

// InteractionAnnounced is interaction.announced's params.
type InteractionAnnounced struct {
	Interaction proto.Interaction `json:"interaction"`
}

// InteractionEnded is interaction.ended's params: Resolution when it was answered (how, for its cards), nil when it
// ended otherwise (withdrawn, expired, answered and gone).
type InteractionEnded struct {
	Interaction proto.Interaction `json:"interaction"`
	Resolution  *proto.Resolution `json:"resolution,omitempty"`
}

// SessionItems is session.items's params.
type SessionItems struct {
	MachineID string       `json:"machine_id"`
	SessionID string       `json:"session_id"`
	Items     []proto.Item `json:"items"`
}

// SessionDelta is session.delta's params: the text the conversation is writing now.
type SessionDelta struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// ChannelsReport is what the hub shows of the channels, in the snapshot (Snapshot.Channels): which channels each
// conversation's messages came from (the session lists' channels), and the conversations whose turn a channel follows
// now or answered lately (the hub's finished push leaves those out: the answer is in the channel). Small, whole every
// time; null while the channels' state could not be read (the hub keeps the last).
type ChannelsReport struct {
	Marks []ChannelMark `json:"marks"`
	Turns []ChannelTurn `json:"turns"`
}

// ChannelMark is one conversation's channels, newest first.
type ChannelMark struct {
	MachineID string   `json:"machine_id"`
	SessionID string   `json:"session_id"`
	Channels  []string `json:"channels"`
}

// ChannelTurn is a conversation a channel's turn is followed in (Answered zero) or was answered in at Answered.
type ChannelTurn struct {
	MachineID string    `json:"machine_id"`
	SessionID string    `json:"session_id"`
	Answered  time.Time `json:"answered,omitzero"`
}
