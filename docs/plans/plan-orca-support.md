# Plan: Orca support

Status: in progress. Steps 0 and 1 are complete. The first Orca and herdr-core phases of step 2 are both merged (2026-11-01; "Orca source", "herdr-core host"). Completed steps are checked under "Implementation order" below. Once all steps are done, merge the design into
`docs/server/architecture.md` / `docs/server/server.md` and delete this plan.

## Background

Persona currently drives only Agents running in herdr. Claude Code running elsewhere (an Orca terminal, an ordinary terminal) appears as "Not running" in the app:
history is visible, status is not, and sending a message starts another `claude --resume` in herdr, with both processes writing the same session.

We want Orca integration without scattering `if orca` everywhere: first extract a "host" layer, implemented separately by herdr and Orca. Adding tmux later becomes another implementation.

## Two concepts

Use these terms consistently in this document and code:

- **Host**: where the terminal runs, such as herdr or Orca. The host lists panes, reads screens, sends input, and opens/closes panes.
- **Agent**: what runs in the pane, such as Claude Code, Codex, or TRAE CLI. The Agent determines transcript location, session-ID recognition, state detection, and dialog appearance.

These are currently mixed: TRAE CLI was integrated in herdr code paths because herdr did not recognize it (`node/traex.go`).
After separation, "Codex in Orca" and "TRAE CLI in herdr" are just combinations from two tables: hosts and Agents.

## Current state: tightly coupled to herdr

- node holds `*herdr.Client` directly (`node/node.go`), and its internal session model is `map[string]herdr.Agent`, with no interface.
- Every feature calls herdr directly: snapshots and event streams (`placement.go`, `herdrevents.go`), session recognition (`setAgents`, `sessionFromProcess`),
  launch (`start` in `control.go`, `waitReady` in `claudeready.go`), sending and pre-send checks (`prompt`, `checkPane`, `ensureSubmitted`),
  mode / thinking-effort changes (`modes.go`), rename/close, and terminal observe / control / scroll (`terminal.go`).
- Pane IDs are untyped: in-memory bindings `n.panes`, hub database `node_sessions.pane`, and hook `HERDR_PANE_ID` all assume herdr panes.
- `proto.Session.SpaceKind` is reserved already, but node writes only `"herdr"`; the app decodes but never reads it, grouping only by "machine + space ID".
- Many app strings hardcode herdr (new chats, spaces, terminal "about 1000 lines", etc.).

## Host interface

Code lives in `server/internal/host` (interfaces, `PaneRef`, kind-keyed host `Registry`), with the herdr implementation in `server/internal/node/hostherdr.go`
(the only node file referencing the herdr client). Every host implements the core; optional capabilities use Go interface assertions. Methods follow actual node call sites,
and each herdr implementation method wraps one herdr call (`Close` first counts panes in the tab, then closes).

```go
type Host interface {
    Kind() string                                        // "herdr" | "orca"
    Version(ctx) string
    Snapshot(ctx) (*Layout, error)                       // spaces, slots, agent panes, every pane
    Agents(ctx) ([]Agent, error)                         // agent panes only (herdr without snapshot)
    Agent(ctx, PaneRef) (*Agent, error)                  // one pane's agent + state
    Read(ctx, PaneRef, ReadOpts) (string, error)         // visible | recent | recent-unwrapped, plain | ANSI
    SendText(ctx, PaneRef, text string) error
    SendKeys(ctx, PaneRef, keys ...string) error         // Persona-wide key names; each host translates
    Prompt(ctx, PaneRef, text string) error              // host's own submit; ErrNoAgent → caller types + enter
    PaneProcesses(ctx, PaneRef) ([]int, error)           // foreground pids, claude first
    PaneEnv() string                                     // env var naming the pane: "HERDR_PANE_ID" / "ORCA_PANE_KEY"
}

// Optional capabilities.
type Watcher    interface { Watch(ctx, panes []PaneRef, changed func()) error }
type Launcher   interface { Spaces(ctx) ([]Space, error); CreateSpace(ctx, cwd, label) (*Created, error)
                            CreateSlot(ctx, space, cwd, label) (*Created, error); Run(ctx, PaneRef, cmd) error
                            NameAgent(ctx, PaneRef, name) error }
type Organizer  interface { RenameSpace(ctx, space, name) error; RenameSlot(ctx, slot, name) error
                            Close(ctx, PaneRef, space, slot string) error } // the whole slot when it is the last pane
type Streamer   interface { CanObserve() bool; PaneSize(ctx, PaneRef) (cols, rows int, err error)
                            Observe(ctx, PaneRef, cols, rows int, fn func(Frame)) error }
type Controller interface { CanControl() bool
                            Control(ctx, PaneRef, cols, rows int, takeover bool, fn func(Frame)) (Control, error) }
```

- **Pane reference `PaneRef`**: host kind + host-local ID. Stored and transmitted as `<kind>:<id>`; herdr panes have no prefix (herdr IDs already contain colons,
  such as `w1:p2`). Prefixes not in `host.Kinds` (fixed known kinds: herdr, orca, herdr-core, botmux, independent of local registrations) are treated as herdr. A known kind with no local host reports "no <kind> host on this machine" on lookup, never sends to herdr, so hub database, `proto.Session`, and hook pane values need no migration.
  node always stores this string (`n.panes`, `Agent.PaneID`), and `Registry.Pane` resolves host and ID at call time.
- **Session model**: node uses `map[string]host.Agent` internally (same fields as the old herdr Agent; `PaneID` is a `PaneRef` string). Panes in host-returned
  `Agent` and `Created` already use that host's `PaneRef`.
- **Key names**: retain the app's current herdr notation (`shift+tab`, `ctrl+c`, `enter`, etc.) as Persona-wide names. herdr forwards unchanged;
  Orca converts to bytes (for example `shift+tab` → `\x1b[Z`).
- **Errors**: hosts annotate their errors with `ErrNoAgent` / `ErrAttachBusy` / `ErrTakenOver` using `host.Mark` (tested with `errors.Is`), preserving error text. Missing host capabilities return `ErrUnsupported` from node, with text beginning `unsupported` (hub uses this prefix to fall back to screen reads).
- **Layout `Layout`**: spaces, slots, recognized Agent panes, and all panes. When known, hosts also supply Agent type, state, and session ID (herdr-integrated `agent_session`).
- **Still herdr-specific in step 1**: new sessions, space renaming, and tab renames recorded in `starts` use the default host (herdr), since space IDs do not yet carry host kind
  (step 3). Event streams subscribe only to herdr (`watchHerdr`, one `herdrEvents`); `node.info` gets `herdr_version` from the herdr host version.
  Listing takes a snapshot from each host and combines them, retaining a host's last Agents if it does not respond; only total failure counts as failure.

## Shared layer above hosts

These parts currently sit in herdr code paths; after extraction all hosts share them.

| Shared layer | Responsibility |
| --- | --- |
| Session recognition | Prefer a session ID supplied by the host; otherwise scan claude / codex process environments, find the pane via each host's `PaneEnv()`, then read `sessions/<pid>.json` (Codex uses thread names, TRAE CLI uses an owner file). `personad hook` reads pane variables for every host |
| State detection | Use host-reported state when available; otherwise infer from the screen (the current approach for TRAE CLI), retain existing corrections such as `codexTurnOver`, then let hub map to unified states (`docs/protocol/agent-states.md`) |
| Pre-send checks | If the host reads colored screens: full draft / gray-text detection and extra Enter (`checkPane`, `ensureSubmitted`); plain-text only: degrade like current Codex handling, checking dialogs only |
| Capability reporting | Each session carries supported host capabilities (live terminal, takeover, create, rename, close); the app hides unavailable buttons |

Replace node's `n.herdr` with kind-keyed hosts (`n.hosts`): listing combines their panes, and each operation dispatches by `PaneRef` kind.

Changes required in hub and app:

- Write host kind to `SpaceKind`; include host kind in app space-group keys and rename APIs (otherwise identical space IDs merge into one group and rename targets herdr).
- Hide buttons by capability; generalize herdr-specific wording, showing truly herdr-only text (such as about 1000 history lines) only for that host.
- Store prefixed `PaneRef` in `node_sessions.pane`.

## Orca source

Based on and checked against Orca 1.4.245 source.

### Connection

- Read `~/Library/Application Support/orca/orca-runtime.json` (0600): Unix socket path and `authToken` (Windows Orca writes `named-pipe` transport,
  `\\.\pipe\orca-<pid>-…`, not `unix`; see [windows.md](../operations/windows.md), "Orca"). Orca changes tokens on every launch;
  reread on connection failure or `unauthorized`. When Orca is not running, the file is absent and the host reports unavailable.
- Protocol: one JSON per line, request `{id, authToken, method, params}`, response `{id, ok, result | error}`. Slow calls may first send `{"_keepalive":true}`; skip it.
- Any process running as the same user can connect, without `ORCA_*` environment variables, including nodes started over ssh or kept running by launchd.
- This socket protocol has no public documentation, only an internal version (`RUNTIME_PROTOCOL_VERSION`, currently 3). Check capabilities with `status.get` at startup; disable if incompatible.
  Connect directly to the socket, not a new `orca` CLI process each time (starting a Node process every second while polling is too heavy).

### Phase 1 (merged 2026-11-01)

Orca is this machine's third "engine" ("One engine per machine" below): engine name `orca`, display name "Orca". Code: client `server/internal/orcart`,
host `server/internal/node/hostorca.go`, fake test runtime `server/internal/orcart/orcarttest`.

- **Pane ID**: Orca pane key `<tabId>:<leafId>` (`worktree.ps` reports Agents by it; terminal process `ORCA_PANE_KEY` also contains it), stored as
  `orca:<tabId>:<leafId>`. The host resolves handles required by `terminal.*` (`term_<uuid>`) by pane key from the latest `terminal.list`.
  On expired handles (`terminal_handle_stale`), relist: reads follow normally; writes (send, keys, close) retry only if it is still the same terminal process (`incarnationId` unchanged).
  If the process changed, reject without input. Reads, writes, and closes all carry `expectedIncarnationId`; Orca rejects a handle whose process changed, preventing writes to its replacement.
- **Connection**: open a connection per call (the socket supports multiple requests per connection, but we do not need that). Before the first call and after Orca changes runtime (`runtimeId` changes), call
  `status.get`: `runtimeProtocolVersion` below 3 or `minCompatibleRuntimeClientVersion` above 3 is incompatible, all calls error,
  and `node.info` marks Orca unavailable with a `note`. Missing metadata, no local transport for the platform (macOS / Linux `unix`; Windows `named-pipe`, then `unix`), or missing/refused socket / pipe means "not running" (`host.ErrNotRunning`):
  immediately answer "No terminal", with no logs if never connected. On connection failure or `unauthorized`, reread metadata and retry once (restart changes socket and token).
  If reply `_meta.runtimeId` differs from metadata, reread next time. Orca responds to unreadable requests (bad JSON, too large) with ID `unknown`; treat as rejection of this request.
  Metadata lookup: if `$ORCA_USER_DATA_PATH` is set, use only that. Otherwise check two locations and read the most recently written when both exist (an old runtime may leave a stale file):
  desktop app directory (macOS `~/Library/Application Support/orca/`, Windows `%APPDATA%\orca\`, Linux `$XDG_CONFIG_HOME/orca/`),
  and orcad directory (remote runtime started here over SSH by Orca on another computer, platform-independent: `$ORCA_USER_DATA`, otherwise `$XDG_DATA_HOME/Orca`, otherwise `~/.orca`;
  Orca 1.4.246's orcad, #2070). If unreadable, rediscover next call so Orca can start after node.
- **Timeouts**: `terminal.list`, `worktree.ps` 3 seconds (each refresh); `status.get`, `terminal.read`, `terminal.agentStatus` 5 seconds; `terminal.send` 30 seconds
  (Agent prompts use Orca-side long polling with keepalives); `terminal.close` 10 seconds.
- **No push**: does not implement `Watcher`. Orca machines have no event stream; node polls a snapshot every second, not the slow 10-second snapshot used with herdr event streams.
  Keep it lightweight: normally omit layouts in `terminal.list` (`includeVisualLayouts:false`; Orca says this is expensive), requesting them only when the terminal set or a worktree's `topologyRevisions` changes,
  or after 30 seconds. Pass the previous `afterSnapshotId` to `worktree.ps`, which returns only `unchanged` if unchanged. Scan processes at most every 1.5 seconds,
  in two steps (`ps -o pid=,stat=,etime=,args=` to find Claude Code, Codex, TRAE CLI, and the Antigravity CLI (`agy`), then `ps -E` only for them). `Agents` after a failed snapshot reuses that attempt's result.
  Local measurement: about 0.2 seconds per refresh (10 terminals).

### Mapping

| Persona requirement | Orca implementation | Phase 1 |
| --- | --- | --- |
| List panes | `terminal.list {includeVisualLayouts, includePreview:false}` (layouts requested only under the "No push" rules above): local only (`executionHostId` is `local`; terminals on SSH targets have no node there to manage them), excluding `orphaned` ones except a terminal this host created (#5547, "New sessions" below) | ✅ |
| Identify Agents | Requires a **foreground** Claude Code, Codex, native TRAE CLI, or Antigravity CLI (`agy`) process (the last two on Linux and macOS) in the pane (`ORCA_PANE_KEY` matches the pane key; fall back to `ORCA_AGENT_PANE`): not suspended (`ps` stat does not start with `T`), in the terminal foreground process group (stat contains `+`; Linux compares pgrp and tpgid in `/proc/<pid>/stat`), without `HERDR_PANE_ID` / `HC_TERMINAL_ID` (claude in herdr / herdr-core nested inside Orca inherits the outer `ORCA_PANE_KEY` but belongs to its own host). Orca's `agentIdentity` and Agent rows can remain after exit, so neither alone suffices. Only if process scanning fails does listing fall back to both `agentIdentity` and a matching Agent row with `hasLivePty` (all writes then reject) | ✅ |
| State | Agent row for this pane key in `worktree.ps` (matching type, `updatedAt` no earlier than 2 seconds before process start, or it may belong to the previous Agent): prefer `mainAgent.state` (main Agent's own state; row `state` includes children, remaining working if the main turn ended but background children run). Keep working / blocked / done, map waiting to blocked. Without a usable row, ask `terminal.agentStatus` (3-second cache): working unchanged, permission → blocked, idle → done; null `status` → unknown | ✅ Polling |
| Map pane to session | Foreground claude pid for the pane → `sessions/<pid>.json` (shared "process identifies session" path). An `agy` pane's conversation comes from Persona's agy hooks (`ORCA_PANE_KEY`) or, on Linux, from the `presence/<conversation>.lock` its foreground process holds open, as for a classic herdr pane (#16531, [engines.md](../integrations/engines.md), "Capabilities by engine"). macOS uses two `ps` calls (above), Linux reads `/proc`, Windows reads process PEB ([windows.md](../operations/windows.md), "Orca"). Extract only `ORCA_PANE_KEY` / `ORCA_AGENT_PANE` and the two nested-host exclusion variables; never retain, log, or send environments to hub (they also contain Orca's hook token) | ✅ |
| Spaces / slots | Space is worktree (`worktreeId`), named from branch (strip `refs/heads/`), falling back to path basename, ordered by `worktree.ps` (Orca sidebar order); a worktree on an SSH target (`hostId` not `local`) is no space of this computer's, in the layout or the workspace catalog (#17050). Slots are tabs in `visualLayouts`, in layout-tree reading order (`first`, then `second` for splits), named by tab title or index if empty | ✅ |
| Read screen | `terminal.read {terminal, screen:true}`: plain text, current screen only; rejoin soft wraps using `tailRows[].wrapped`; error on `source: screen-unavailable`. Host declares `host.PlainScreen`. The host's `recent` read also returns only the current screen, which the node's own checks read; what a client shows above the screen (`session.screen.before`, `session.screen` `recent`) comes from Orca's output tail and, where it agrees, its log by cursor (`host.Scrollbacker`, #17048, [terminal.md](../integrations/terminal.md) "31. Orca terminals: scrolling up, and a quicker plain view (#17048)"); the screen read is cheap, so a page polling it may read it four times a second while it changes (`host.QuickScreen`). Orca 1.4.277 lifts the text of an Agent's input box out of the rows into `draft`, leaving the prompt row as its bare glyph (`❯`, `›`): the read puts it back in that row (`orcart.Screen.Text`), or the node would take every box for empty and type `/effort` into a draft. The draft has no colors either: checked live on an isolated runtime with Claude Code 2.1.331, a typed draft and Claude's grey suggestion come back alike (Orca's own emulator knows which cells are dim, but no method on the local socket says so), so effort and speed changes are refused while the suggestion shows (#17042) | ✅ Plain text, scrollback |
| Send message | Do not use Orca's `agentPrompt`: it bracket-pastes only to Agents it has "seen working". Newly launched claude receives raw text + `\r`, submitting a multiline message at its first newline (tested 2026-11-01). Rejections with `accepted:false` (`no-agent` / `permission`) require `requireAgentStatus:"sendable"`, which Orca always rejects alongside "text + Enter", so it is unusable here too. Follow herdr's `agent.prompt`: first verify a foreground Agent (process table has a recognized foreground Agent in the pane, not inside a host nested in it; `terminal.agentStatus` is asked too: permission status returns `dialog_open`, an error means not foreground; its `isRunningAgent` is not required since #5463, because Orca reports false while Claude Code has background tasks and still takes input; failed process scan means not foreground; refusals return `not_delivered` with no input and log the check that refused), then send bracketed paste ourselves: `terminal.send {text: ESC[200~ + text + ESC[201~}` (no `enter`; replace ESC in text with `<ESC>`, like Orca, to prevent premature paste termination). Wait 150 ms plus 10 ms per line, capped at 1 second; verify foreground Agent again, then send `\r` separately. Any failure after paste returns `delivered:unknown` (text may remain in input). Always pass `client: {id:"persona", type:"desktop"}` (`client.id` required; absent yields `invalid_argument`). Pre-send checks only detect dialogs (no color to distinguish drafts from gray text). With no supported Agent in front but another program `terminal.inspectProcess` names (a CLI the scan does not know), `Prompt` answers `ErrNoAgent` and the node pastes the text and presses Enter itself while that program is still in front (send.go `typeable`, #17040); a shell in front stays refused | ✅ |
| Session not running | When a session is no longer an Agent pane (exited, suspended, backgrounded), herdr resumes it in a new pane before sending (herdr-core too, since #2532). A message to it is refused with no input on Orca: if its last pane still exists, `agent_not_in_front` (the Agent is not in front; it may be suspended); if gone and Orca cannot continue it, `not_running` ("…reopen it on the computer"). Previously this looked up a launcher and returned the confusing `unsupported: orca cannot do this`. A stopped Agent's Resume opens a new Orca terminal for Claude Code, and since #17044 for Codex, TRAE CLI and pi (`docs/integrations/engines.md`, "Capabilities by engine") | ✅ |
| After a reboot | Orca 1.4.277 keeps the tabs but lists none until a client opens one, then resumes the Agent with its own `claude --plugin-dir <agent view> --resume <id>` from what its Claude hooks reported; `terminal.create`'s `launchAgent` / `launchToken` / `launchConfig` are not used for that over the local socket (checked on an isolated runtime, #17038). So no resume command is given: the session is `interrupted` with Continue until the tab is opened, then `off_launcher` in it, and a switch back to its launcher restarts it there ([engines.md](../integrations/engines.md), "Orca after a reboot (#17038)") | ⚠️ Shown, not restored |
| Type without Enter | `terminal.send {text}` without `enter`: raw bytes, no paste, after the typing gate (`typeGate`, #17040): a foreground Agent from the scan (not permission: free-form dialog input needs it), or a program other than a shell `terminal.inspectProcess` names in front, or the owner's answer to a failed start's kept-tab question in a terminal this host opened under the same incarnation (`withStartTyping`). A shell in front of anything else, an unverifiable answer and an Orca without `inspectProcess` refuse with nothing typed. Orca does not declare `TextTyper` | ✅ |
| Special keys | Key names → xterm bytes: `enter` `\r`, `esc` `\x1b`, `shift+tab` `\x1b[Z`, `backspace` `\x7f`, `ctrl+<letter>` control characters, `ctrl+space` `\x00`, `alt+<character>` prefixed ESC. Modified arrows / Home / End / Insert / Delete / PageUp / PageDown / F1–F12 use xterm parameters (1 + shift 1 + alt 2 + ctrl 4, e.g. `alt+left` `\x1b[1;3D`, `ctrl+left` `\x1b[1;5D`). Send each key separately via `terminal.send`, 40 ms apart (cancel via caller ctx; concatenating `esc` with a following sequence can make an alt key). Reject unknown keys before sending any. Printable keys (letters, digits, space) pass the same typing gate as text; Enter, esc, arrows, ctrl+c do not (stuck panes must remain interruptible/closable) | ✅ |
| No input delivered | Foreground check fails; `terminal.send` returns `accepted:false` (permission → `dialog_open`, others → `not_delivered`); pre-write errors such as `terminal_handle_stale` (including incarnation mismatch), `terminal_not_writable`, `terminal_gone`, `terminal_exited`, `invalid_argument`, `runtime_busy`, `request_too_large` also return `not_delivered` (all `delivered:none`, safe to resend). Uncertain-write errors such as `runtime_error` remain errors | ✅ |
| Close | Like herdr, send ctrl+c (`\x03`) twice, then `terminal.close` (Orca closes the pane, or tab if it was the last pane). An already absent terminal counts as closed | ✅ |
| What runs in a terminal | `terminal.inspectProcess {terminal, expectedIncarnationId}` (`host.ForegroundNamer`, #17039): `foregroundProcess` names the program in front, `null` with `hasChildProcesses:false` is the shell itself (named from the evidence fence's `shellPid`); `idleShellProven` (1.4.277: shell in front, alone, no job behind it or stopped, no output since) is what the grace close needs. An answer whose `foregroundProcessEvidence.verdict` is not `live` (an incarnation that changed: `incarnation_mismatch`), or `childProcessEvidence:"unverifiable"`, says nothing. An Orca without the method (`method_not_found`) leaves the agent process scan, which names only a supported agent: anything else is "can't tell", which closes and types nothing. Orca 1.4.277's terminal daemon marks every answer on Windows unverifiable (`windows_ssh_foreground_unavailable`), so there nothing is closed or typed on its word | ✅ |
| Rename | `RenameSpace` sets a local worktree's display name (`worktree.set {displayName}`); `RenameSlot` renames the tab (`terminal.rename` on one of its terminals). On a runtime with no window the layout keeps the tab's first title (Orca 1.4.277, checked live): slot labels take the tab's custom name from `session.tabs.list` (the phone's tab model, which follows renames) whenever the layouts are read, once per worktree with tabs (a rename of the node's own has the next listing read them), and the guarded rename of an assistant-named tab (#15674) checks the name there; an Orca without it keeps the layouts' titles (#17043) | ✅ |
| Split, close tab | `host.Splitter`: `terminal.split {terminal, expectedIncarnationId, direction}` (`right` → `vertical`, `down` → `horizontal`); the local socket's split takes no folder (only its layout-addressed form does, which it refuses with `host_not_owned`), so a pane working outside the worktree root gets `cd <folder>` as the new shell's `command`. `host.SlotCloser`: `terminal.closeTab` on one of the tab's terminals. Neither holds `expectedIncarnationId` on 1.4.277, so the terminal is looked up afresh first. `method_not_found` → `unsupported`, nothing done, and the panes listing stops offering it for that runtime (#17043) | ✅ |
| Recognize hook pane | With no `HERDR_PANE_ID` or `HC_TERMINAL_ID`, `personad hook` reads `ORCA_PANE_KEY` (fallback `ORCA_AGENT_PANE`) and reports `orca:<tabId>:<leafId>`. node records these only on Orca-engine machines (herdr machines continue ignoring Orca panes). claude launched in Orca usually lacks Persona hooks (only Persona's own launches add them through `--settings`) | ✅ |
| New sessions, mode / thinking-effort changes | Implements `host.Launcher`; `terminal.create` supports creation in registered worktrees or their subdirectories and attempts to register unregistered Git repositories automatically. A directory in no worktree (`~/.persona`, say) gets its terminal in the worktree Orca is showing (`isActive`), else the first local one, and the start `cd`s there: Orca's UI does not adopt a terminal made in a worktree it is not showing, which about 2 s later becomes `orphaned` with its `tabId` / `leafId` rewritten to `pty:<worktreeId>@@…`, the same handle and incarnation (#5547). The node keeps the pane key creation gave, and the host finds a terminal it made (a start's, or the one Orca opens in a new worktree) by its handle and incarnation: one Orca orphaned and re-keyed stays listed under that key, its processes and agent row looked up under both keys, so the start's typing, readiness wait, tab rename and closing still reach it; one the user moved to another tab is listed where it is now. `Run` accepts an orphaned terminal only when it is that re-keyed one; a terminal whose handle changed is not ours (#972). A start whose pane is gone for 6 looks in a row (3 s) fails saying so. A start's terminal is created with the start environment as `terminal.create`'s `env` (`PERSONA_AGENT=1` and the startup switches, `host.EnvLauncher`; an Orca that refuses the field gets the create again without it, #17036). `terminal.create`'s `cwd` is not honoured for this create (Orca 1.4.277), so the start `cd`s through its launch grant, and New Terminal / Open Terminal Here for a folder asks for the terminal with `command: "cd <folder>"` and `startupCommandDelivery: "shell-ready"` (Orca types it into that new shell once it is ready; no launch grant for a user's shell). Mode menu needs further expansion | ✅ New sessions supported |
| Live terminal, colored screen reads, takeover dimensions | Only over paired encrypted WebSocket; hub falls back to screen reads ("Streaming transport" below: no-go until Orca adds a method) | ❌ |
| Push changes | Not on the socket (all `*.subscribe` methods are streaming) | — |

### Streaming transport (#17048)

Investigated on Orca 1.4.277 (its runtime bundle, `orcad-server.js`, and an isolated headless runtime with its own HOME
and profile): can Persona use the transport that `terminal.subscribe` and `agentSession.subscribeStatus` need, without
weakening security and without pairing the user has to repeat? **No-go for now**; the node keeps polling, and the plain
view reads faster instead (`docs/integrations/terminal.md`, "31. Orca terminals: scrolling up, and a quicker plain view (#17048)").

- **The local socket refuses streaming methods**: the dispatcher answers `method_not_supported` "Method terminal.subscribe
  requires a streaming transport" (measured), whatever the token.
- **The WebSocket transport** (`ws://127.0.0.1:<port>`, listed in `orca-runtime.json` next to the socket) starts with
  `e2ee_hello`: v1 sends the client's X25519 key and both sides seal with a NaCl box (the server's key is
  `publicKeyB64` in `orca-e2ee-keypair.json`, 0600), v2 adds a transcript hash; within 10 s the client sends a sealed
  `e2ee_auth` with a **paired device's** token (or, v2, a device-key proof). `handleAuth` resolves the token in Orca's
  device registry (`orca-devices.json`) only. Measured: the handshake completes with the key Persona can read, and
  `e2ee_auth` with the local socket's own token answers `e2ee_error {code: "unauthorized"}` and closes 4001.
- **Getting a device token** without a person: `pairing.createOffer` is callable on the local socket (permission
  `pairing-admin`) and returns a pairing URL carrying a device token, but for both scopes it first calls
  `ensureNetworkExposure()`, rebinding the WebSocket listener to every interface. Measured on the isolated runtime
  (`--refuse-public`): `{available: false, reason: "network_exposure_failed"}`. On a desktop Orca it would expose a
  full-control endpoint to the network, and a device seen with `pairingReach: "network"` makes Orca bind all interfaces
  again at every start unless `--bind` pins it (`resolveInitialWebSocketBindHost`). It also lists a paired device in
  Orca. That weakens security: not used. A loopback-only offer (`reach: "this-computer"`) exists inside Orca but no RPC
  parameter reaches it.
- **With a person**: `orca serve` prints a pairing offer at start (none with `--no-pairing`: `disabled_by_operator`), and
  the desktop app pairs phones by QR code. Persona would have to ask for that pairing on every computer, keep a device
  credential per Orca, and pair again whenever the user revokes or resets it: the churn this was to avoid.
- **Writing a device into `orca-devices.json`** (Orca adopts external edits) would grant access behind Orca's back: not
  considered.

What Orca would need to add, any one of which Persona would adopt behind a capability probe (`status.get`
capabilities, or falling back on `method_not_supported`), with a `host.Watcher` and a `host.Streamer`:

1. **A streaming subscription on the local socket** (preferred): `terminal.subscribe` and `agentSession.subscribeStatus`
   (or one `events.subscribe`) accepted on the authenticated local socket, as notifications on the same connection, as
   herdr-core's `events.subscribe` does. The socket already grants full control to its token's holder, so this adds no
   new trust.
2. **A colored screen read**: `terminal.read {screen: true, format: "ansi"}` with SGR rows, the cursor and the grid size,
   plus a long-poll for change (`terminal.wait {for: "output", afterSequence}`), so the plain view gets colors, a cursor
   and push refresh without a stream.
3. **Loopback pairing for a local client**: `pairing.createOffer {scope: "runtime", reach: "this-computer"}` over the
   local socket without network exposure, so a same-account client can pair itself once, silently and revocably.

### Findings

- **When state rows disappear**: after an Agent exits, Orca removes its row from `worktree.ps` (retaining only recovery rows, which `worktree.ps` does not list). But `agentIdentity`
  and row removal need not synchronize with the process, so Agent identification is process-based.
- **A start does not take Orca's word alone** (#10588): a start once failed as a Claude stall in a tab Orca named claude in, where only empty prompts
  showed and the start line never arrived. A start in an Orca tab now counts claude as come only with Claude Code's screen or its start file run, and a
  line that never showed and never ran ends as `terminal_not_ready` (`node/startevidence.go`, `docs/integrations/claude-integration.md`).
- **`terminal.send` rejection is not an error**: it returns `{send:{accepted:false, bytesWritten:0, refusedReason}}`; inspect `accepted`. However, `no-agent` / `permission`
  appear only with `requireAgentStatus:"sendable"`; without it, Orca writes anyway. Orca directly rejects `requireAgentStatus` together with "text + Enter"
  (requiring two steps: text, then Enter), but that loses bracketed paste, so we must verify foreground Agent via `terminal.agentStatus` before sending.
- **Bracketed-paste conditions**: requires `agentPrompt`, text, `enter`, `client.type: desktop`, and a foreground Agent recognized by Orca **and previously seen working**.
  The reply includes a `send.prompt` receipt. Newly launched claude that has not worked yet does not qualify; Orca writes raw text followed by `\r`. Persona therefore performs bracketed paste itself (see "Send message").
  Text without `enter` is written raw. `client` requires `id` (Persona uses `"persona"`, as Orca CLI does); `type` alone returns `invalid_argument`.
- **Default screen reads switch sources**: `terminal.read` without `screen` reads the output-stream tail (`source: stream`); always pass `screen:true` for the screen.
- **Two kinds of kept output** (Orca 1.4.277, #17048): the output tail (`terminal.read {limit}`) keeps rows a program redrew in place as they ended, as a scrollback does (2000 lines and 256 KiB kept, at most 32 KiB a read, the screen instead on the alternate screen); the log by cursor (`terminal.read {cursor, limit}`, `oldestCursor` / `latestCursor`) is every completed line, each redraw of an agent's live rows included, so it is shown only where it agrees with the tail. `cursor` together with `screen:true` is refused.

### Cautions

- The token in `orca-runtime.json` grants full Orca control; claude process environments also contain Orca's hook token (`ORCA_AGENT_HOOK_TOKEN`). node reads only required variables, never logs or sends them to hub.
  The client never prints tokens; tests check logs contain no token.
- `ORCA_TERMINAL_HANDLE` may be absent (set only when a handle was preallocated). Recognize panes only by `ORCA_PANE_KEY`, falling back to `ORCA_AGENT_PANE`, whose name Claude Code does not strip.
- Orca installs its hooks globally in `~/.claude/settings.json`. claude outside Orca also runs them, but scripts exit when Orca variables are absent.
  Persona leaves these hooks alone.
- `~/Library/Application Support/orca/agent-hooks/last-status.json` also has per-pane session IDs (`entries[paneKey].providerSession.id`),
  but is an internal Orca file; do not use it.
- TRAE CLI on Linux and macOS uses the same foreground send check as Claude Code and Codex. The `traecli` command and its older alias identify the same agent. TRAE CLI history identifies the session, and the send check requires the matching foreground process in the pane.
- Live integration checks are read-only: `go test -tags orcalive -run OrcaLive ./internal/node/ -v` uses a read-only client (allowing only `status.get`, `terminal.list`,
  `worktree.ps`, `terminal.read`, `terminal.agentStatus`; all other methods reject before sending) to list the active Orca once, print panes, status, pid, and session ID, and compare against
  `sessions/<pid>.json`, without printing screen content. Orca-mutating calls (send, close, create) are tested only against fake runtimes: `orcarttest` validates every request against Orca's
  parameter schema (`src/shared/rpc-contract`), returning and recording `invalid_argument` for invalid requests; tests verify none occur.
  node's test package points `ORCA_USER_DATA_PATH` at a nonexistent directory, preventing every test from touching real Orca.

### Structured sessions (#2072)

Since Orca 1.4.24x, "structured sessions" wrap Claude Code / Codex in Orca's own chat UI, without terminals. `terminal.list` omits them;
their rows in `worktree.ps` have pane keys `structured-agent-session-<session-id>:<uuid>`. Phase 1 recognized only terminals, leaving these invisible in Persona.
They are now integrated by the Orca bridge in node (`server/internal/node/orcabridge.go`, client `server/internal/orcart/session.go`) through the
runtime protocol (`docs/integrations/runtimes.md`), like the Paseo bridge but inside node (`net.Pipe` connects to `RuntimeHost`):

- **Activation**: enabled for the Orca engine, closed when switching away. Lists every structured session in local worktrees (`hostId: local`), whatever its provider (`agentType`).
- **Which chats**: the agent rows of `worktree.ps`, and the chat tabs of `session.tabs.listAll` (`type: agent-session`) that `worktree.ps` does not list: Orca reports a
  chat's row only once it has had a turn, so a chat opened and not written to yet is found only among the tabs. The tabs are read every 5 seconds; an Orca without the method
  (`method_not_found`) adds none. Checked on Orca 1.4.277.
- **Content**: a Claude chat's is its Claude transcript (`transcript`, `providerSession.id` from `agentSession.history`), read by the node as usual. A Codex chat is its Codex thread
  (`codex_thread`, the same field), so the node does not list the thread a second time from Codex's history; its content, like every other provider's chat's and a Claude chat's
  before its transcript is written, is Orca's own journal (`agentSession.history`, `orcabridge_items.go`): an item's `seq` is its journal sequence, a message's blocks its items
  `<seq>#<index>` (the user's and the agent's words, reasoning as thinking, tool calls and their results, Orca's warnings and errors as notices; turns, prompts and Orca's
  bookkeeping left out), `history` the journal's epoch (`orca:<epoch>`, which a rewind changes), `before_seq` / `after_seq` page with Orca's own `before` / `after` cursors.
  A provider the apps do not know is named in `agent_label` (Antigravity, Gemini, OpenCode…). Rows carry `host: "orca"`, so the apps ask the Orca engine about their worktree.
- **Session IDs**:
  - A Claude chat whose Claude session the bridge knows when it first lists it: that Claude session id, the id the same conversation has in a terminal or "Not running"
    (as before this version, so every link, pin and read state stays).
  - A chat Persona started (#17047) is `persona-<id>` in Orca and `<id>` in Persona: the id the hub named for it.
  - Any other chat (Codex, another provider, a Claude chat seen before its first message): Orca's own session id. A Claude chat listed so keeps that id once its Claude
    session appears; its row names the transcript from then on, and the node lists the transcript under no second id but names it in the row's `aliases`
    (`docs/integrations/runtimes.md`, "Runtimes that run Claude Code").
  - The bridge keeps the id it gave each chat in its **book** (`orcabridge_book.go`): `<user cache>/persona/orca/chats.json` (0600, in the node's private cache folder, the 300
    chats seen last), so a node that restarts gives each chat the same id. A book that is lost loses only the third rule's memory: such a Claude chat comes back under its
    Claude session id. Space is worktree (`worktreeId`), named from branch as for terminal sessions; child worktrees carry `parent_space_id` and collapse under their parent on
    the web, like terminal sessions (#2073).
- **Closed chats**: a chat closed in Orca (its tab gone; its record and journal stay) is listed from the book, not running (`offline`), the latest 80 of the last 7 days, as Paseo
  lists its archived agents. Its content is read as when it was open. A message to it opens it in Orca again (`agentSession.reveal`) and goes there, never to a terminal;
  modes are refused until then (`orca_closed`), and closing it in Persona takes it off the list. One Orca no longer has (deleted) answers `session_gone` and leaves the book.
  While Orca is not running every chat of the book is listed this way. So no chat's Claude transcript is left to the node as an ordinary session not running, which a Resume
  would open in an Orca terminal: the bridge sends `unlisted_transcripts: []` (it lists its closed chats itself and names none).
- **State**: poll `worktree.ps` every 1.5 seconds, using row `mainAgent.state` (working / blocked / done); a tab without a turn is idle. Push to hub only when the list changes.
- **Actions**: send text/images (messages sent while working queue after this turn, `delivery: queue-if-active`), interrupt (`agentSession.cancel`),
  close (`agentSession.close`, like Orca's close button; records remain). No terminal, screen reads, keys, or live terminal.
- **Controls** (#17046, `orcabridge_controls.go`; the bridge declares `caps.modes`, `model`, `rename`, `rewind` and `seen`):
  - Permission mode, effort and model are `agentSession.setOption`, in that order: model, then the effort chosen with it, then the mode. Persona's `default` /
    `acceptEdits` / `auto` / `bypassPermissions` are Orca's `ask` / `accept-edits` / `auto` / `full-access`. **Plan** is not a permission mode in Orca but its
    interaction mode (`setOption interactionMode plan`, Orca 1.4.277): a row in it reads `permission_mode: plan`, and any other mode picked on it sets
    `interactionMode default` first. A Claude chat takes Claude Code's chips, full access included at any time (`bypass_available`). Another provider's chat lists
    the modes Orca takes for it (`capabilities.permission_modes`, its approval as `default`, from Orca's own table: Codex ask / auto / full-access, Antigravity
    and Hermes ask / accept-edits / full-access, most providers ask / full-access, pi, aug and a provider outside the table none; plus `plan` where its options
    report an interaction mode, as Codex's do) and the efforts its model has that Persona can send (`efforts`, from `agentSession.options`' models); a mode Orca
    does not take for it is refused (`orca_mode_unsupported`) with nothing changed. A model change is offered on every open chat (`capabilities.model`), from
    the models of its provider's launcher (below) where it has one, else the computer's launcher models.
  - Rename is `agentSession.rename {sessionId, name}` (`agent-session.structured.rename.v1`; an Orca without it is refused as `orca_rename_unsupported`).
  - Rewind is `agentSession.rewind {envelope, itemId, expectedEpoch}` (`agent-session.rewind.v1`; a row says `capabilities.rewind` only while Orca has it and
    `agentSession.options` says the chat supports it). The bridge finds the user's message in Orca's journal (the newest 2,000 rows) by its text (white space
    aside), its occurrence counting from the end and how many of the user's messages came after it, and refuses (`rewind_mismatch`) a message not where the app
    saw it and a journal whose epoch moved on (Orca's `stale-epoch`); Orca's `busy` is `rewind_busy`. It rewinds the conversation only (no `rewind_code`), and
    the app puts the message back in its own input box (`input_cleared: true`). A Claude chat that Orca rewinds goes on in a fork of its Claude session: the
    bridge reads the chat's provider session again at once (and at most every 10 seconds while its row changes), so the row names the new transcript and keeps
    its id.
  - Seen is `agentSession.acknowledgeAttention {sessionId, observedCursor}` up to the journal's end (`agent-session.attention-ack.v1`; nothing on an Orca
    without it), so a chat read in Persona is not unread in Orca.
  - A closed chat (above) takes none of these until a message opens it again.
- **Starting a chat** (#17047, `orcabridge_start.go`; `caps.start`):
  - Launchers: one per provider Orca runs as a chat (its structured agents) and found installed (`preflight.installedAgents`, read at most once a minute),
    on an Orca whose `agent.launch` takes the chat surface (`agent.launch.surface.v1`): `orca-<provider>` (`orca-claude`, `orca-codex`…), display name
    "Orca chat · <provider>", pushed with `runtime.launchers` as they change. A provider other than Claude Code lists the permission modes a start takes
    (Orca's table, plan aside: the bridge promises no read-only start, `caps.readonly`, so the node refuses a plan start), and each lists the models its
    provider's open chats name in `agentSession.options` (with the efforts among Persona's levels). None while Orca is not running or cannot start chats,
    and none on a computer whose Orca host is off (the bridge does not run there). Every open chat's row names its provider's launcher (`launcher`), so the
    apps' model picker offers Orca's models; a chat Persona started says `started_by_persona`.
  - `start({cwd, prompt, client_msg_id, launcher, model, permission_mode, effort, session_id})`: the chat goes into the deepest local Orca worktree holding
    `cwd`, else, for a folder in a Git repository Orca does not have (not the home folder, the root or a temporary folder), the repository added with
    `repo.add` as a terminal start adds it; a folder no worktree holds is refused (`orca_start_folder`), never started in an unrelated workspace. The chat
    starts at its worktree's root (Orca's chat surface takes no start directory). It is `agent.launch {agent, operationId, target: {kind: "existing",
    worktree}, prompt: {text, delivery: "submit"}, chatOptions: {permissionMode, model, effort}, sessionId, surface: "chat", presentation: "background"}`,
    as Orca's own New chat, so Orca's window keeps what it shows; an Orca whose launch has no chat surface gets `agentSession.create` (envelope with no
    fence, `firstMessage`, `options`); one with neither is refused (`orca_start_unsupported`). Pictures reach it as for any runtime start: the node saves
    them and names their files in the prompt.
  - Never doubled: the Orca session id is `persona-<the id the hub named>` (the book maps it back, above). A start asked for again finds the chat it made
    (the row it answered with stands until Orca lists the chat, at most a minute, else Orca's `agentSession.history` knows the id) and launches nothing.
  - A later per-computer default ("New Agent starts Orca chats") would route New Agent's Claude Code and Codex starts on that computer to `orca-claude` /
    `orca-codex` in the node; it is not built (an open product decision). The assistant's task Agents stay in terminals either way: an Orca chat carries
    none of Persona's launcher, hooks or MCP config.
- **Cards**: when blocked, read `agentSession.currentRequests`, mapping pending approvals to permission cards (if `detail` is tool-input JSON,
  show commands/files like hook cards), plan approvals to plan cards, and questions to question cards. Return answers with Orca option IDs and `expectedRevision`.
  Cards disappear from the phone if answered on the computer first. Every answer of a question goes: Orca 1.4.277 takes at most four in one reply and refuses more, which
  then counts as an answer not taken. An answer Orca did not take (refused, or its outcome unknown) is never dropped: while Orca still waits on the prompt it goes on the phone
  again as a new card (its own id), a permission card with Orca's words on why in its `reason`.
- **Errors**: the bridge's refusals keep their codes (`runtime_away`, `refused` and `session_gone` with `delivered: none`, `unknown` with `delivered: unknown`) and carry a
  `reason` (`orca_away`, `orca_refused`, `orca_unknown`, `orca_session_gone`, `orca_closed`) the hub words in the request's language (`rpc-errors.<reason>`), Orca's own
  words as the detail.
- **Write envelope**: every write carries `envelope {sessionId, clientOperationId, expectedRuntimeFence, payloadFingerprint}`.
  `clientOperationId` is `<13-digit-milliseconds>-<32-hex-digits>`; the same message (same `client_msg_id`) reuses it, so Orca returns the original result on resend, never sends twice.
  `payloadFingerprint` is SHA-256 of canonical JSON `{method, sessionId, fields}` (sorted keys, no spaces, escaping as JavaScript `JSON.stringify`).
  Approvals and questions use `agentSession.respondTo:approval` / `respondTo:question`. `expectedRuntimeFence` comes from `page.fence` in `history`
  (no longer compared since 1.4.246, but must be a positive integer). `refusal` with `ok:false` means nothing was done; `agent_session_operation_unknown` means completion is unknown.
- **Socket has request/response methods only**: `agentSession.subscribe*` are streaming methods available only on paired WebSocket, so this uses polling.
- **Live checks** (2026-11-05, Orca 1.4.246, newly created test sessions closed afterward): `setOption`, `send` (same operation ID resend does not send twice),
  Bash approval card content and deny (file not created), answering an already answered approval, `cancel` (working → stopped), and `close` all matched the behavior above.

## Registered bot endpoints (#12610)

Phase 1 exposes only explicit manager-owned registrations in **Agents → Bots**. It does not infer bots from structured sessions, create a second transcript store, or add a `/api/v1/bots` chat API. Registration is a `bots.endpoints` entry in the manager's 0600 `manager.json` (`docs/operations/config.md`, "manager.json"); there is deliberately no browser link/add form in this phase. A registration has a stable public endpoint ID and title plus an exact machine and Orca session binding. Its public session ID is `bot_<endpoint id>:endpoint`, never Orca's structured-session identity.

Each registration is an independent `agents.Source`. The manager resolves the bound computer and uses `Machine.call`; the node alone calls Orca through `orcart.Client`. The internal `agentbot.describe`, `agentbot.history` and `agentbot.send` RPCs require an exact, unambiguous local structured-session binding. Missing, stale or duplicate bindings fail closed rather than falling back to a default bot or another session. Existing `/api/v1/agents...` routes, `agents.read` / `agents.control`, full / answer device rules, outbox, invalidation and polling are unchanged.

History comes from `agentSession.history`: stable `itemId`, `sequence`, `observedAt` and message `role` / `blocks`, with `page.window.nextCursor {epoch, sequence}` encoded as the existing API's opaque older-page cursor. `rankTimeMs` is the bound row's update time or the newest history message, whichever is later. Assistant replies are read from this history, never synthesized from a send receipt; acceptance does not mean completion. Text and known tool blocks are projected without forwarding unknown raw block payloads.

For writes, the manager persists only idempotency metadata (endpoint binding, SHA-256 of the exact text, operation ID and creation time), not message text or a transcript. It does so before calling the node, and passes that operation ID as Orca's `clientOperationId`; `agentSession.send` computes the exact payload fingerprint. The same client ID and payload therefore replay after a manager/node restart, while a changed payload or binding conflicts before another write. `retryUnknown` is never set and unknown outcomes never auto-repost. A retry is always the user's explicit retry with the same client ID. The book keeps the newest 2,000 operations (four times the outbox's 500), so a client ID outlives its outbox row; only a retry after that many newer sends would get a fresh operation ID.

Registering an endpoint makes it reachable by everything that already holds personal-Agent access: full devices, and extensions granted `agents.read` (history, including tool inputs and results) or `agents.control` (sending). An Orca session is usually a coding agent with tools on that computer, so register only a session you would let those extensions instruct.

## herdr-core host

herdr-core (command `hc`) is the terminal runtime behind the Herdr desktop app. It has the same author as herdr, but is a rewrite and incompatible: different socket, `terminal.*` methods,
and environment variables. It is therefore a separate host, kind `herdr-core`, storing panes as `herdr-core:<address>` (such as `herdr-core:t7`).
Code: client `server/internal/herdrcore`, host `server/internal/node/hostherdrcore.go` (starting and renaming: `hostherdrcore_launch.go`).
Checked against herdr-core 0.1.16 source and `hc schema`; starting and renaming (#2532) against 0.1.19's `hc schema` and the app's own terminals.

### One engine per machine

Each machine selects one "engine" (a set of hosts): herdr (default, one host), herdr-core, orca ("Orca source" above), and
`herdr-both`, added 2026-11-03 (herdr + herdr-core side by side, "Combined engine" below). Users choose per machine in the app (during pairing, **Settings › Machines**).
hub persists the choice and sends `engine.use` when node connects (API/storage in `docs/protocol/protocol.md`, "Engines"; node code `server/internal/node/engine.go`).

- **Why not combine by default**: initially both hosts registered together. Review found this slowed herdr: slow herdr-core delayed herdr refresh/send too (up to about 4 seconds);
  if herdr died while herdr-core remained, stale herdr Agents stayed online; herdr-core events triggered unnecessary herdr snapshots; TRAE CLI in herdr-core
  forced herdr to poll every second. Selecting only one registers only that host and avoids all four. The combined engine is explicit and addresses them individually with parallelism and per-host isolation.
- **herdr selected**: node's host table, refresh, polling, and event stream remain exactly as before; no herdr-core client or subscription. The sole exception is `node.info`,
  which checks herdr-core availability once to fill `engines`.
- **On connection**: for a machine with a saved choice, hub's first call is `engine.use`. node waits for the engine to be settled (`engine.use`, hub listing sessions directly, or at most 3 seconds)
  before polling/subscribing, so herdr-core machines never briefly flash herdr sessions. Machines using herdr (no saved choice) receive no `engine.use`.
  With herdr-core selected, `node.info` does not query herdr either; the herdr version comes from `herdr --version`.
- **Runtime switching**: `engine.use` replaces the host table, discards old Agents, space names, and process caches (invalidating refreshes begun before the switch), ends every terminal stream
  (pushes `terminal.end`), stops old event streams and starts new ones, then pushes the session list. Each event stream has its own live marker; stopped old streams cannot clear new-stream state.
- **Operations without a host**: operations not deriving a host from a pane, such as creating sessions or renaming spaces, use `defaultHost` / `defaultHostKind`: herdr-core when selected,
  returning `unsupported` for unavailable capabilities (worktrees), not "No herdr on this machine". Combined mode always defaults to classic herdr. herdr-selected machines are unchanged.
- **Nested herdr**: with herdr-core selected, a terminal whose foreground is classic herdr is not an Agent, even if inner claude OSC 7501 escapes through.
- **Nested ptys** (#5463): processes in a pty a program in the pane made (tmux, zellij, screen, `script`, a node-pty) inherit its `ORCA_PANE_KEY` and are each "in front" of that pty while the pane itself may show a shell. They are not the pane's: the scan walks up from each candidate to the first ancestor on another terminal (the pty's owner) and drops the candidate when that owner carries the same pane key; in an Orca pane the owner is Orca's terminal host, which has none (seen on a Mac 2026-11-06: `claude → bash → bash → login → orca-terminal-host`, versus `claude → bash → bash → tmux server` with the key). No owner found: not taken. Not by `TMUX=`: Orca's own Claude agent teams set it in a real pane. Without this, an agent's `tmux new-session -d` workers running `claude -p` took the pane from the claude that started them, which was then listed as interrupted.
  Combined mode registers such terminals under herdr-core with no Agent; the inner claude belongs to a herdr pane and is not counted twice.

### Combined engine (herdr-both, 2026-11-03)

Reason: Herdr App can connect remotely to another machine's terminals (`herdr remote-client-bridge`), so classic herdr (local terminals) and
herdr-core (Herdr App remote access) can both run on one machine. Selecting either hides the other's sessions. `herdr-both` registers both,
isolating the four issues in "Why not combine by default":

- **Refreshes do not wait for each other**: `refreshHosts` changes from sequential to per-host parallel, with each call bounded by its client timeout (herdr-core list: 2 seconds).
  One stuck host does not block the other; partial refresh replaces only that host's Agent cache, retaining the other's last result.
- **Events do not trigger each other**: each watcher host has its own `watcherSignals` (live marker + kick channel, `herdrevents.go`). The polling loop selects all kicks,
  adding only the emitting host to that round's due list; hosts without events retain their own slow-snapshot cadence (10 seconds). herdr-core events no longer trigger herdr snapshots.
- **Online state is per-host**: Agent cache is `map[hostKind][]Agent`; a host returning `not_running` clears only its own group.
- **TRAE CLI does not slow the other host**: `hostHasTraex(kind)` counts its sessions only in the given host's panes and decides whether that host needs a snapshot every second.
- **Default host is herdr**: create / continue / rename use herdr; pane actions still route by PaneRef kind. herdr-core pane restrictions (no color, no text-only typing,
  no live terminal) are exactly the same as when herdr-core alone is selected.
- Orca is not combined: no event stream, one-second polling, heaviest model. Combining it would be separate work later.

### Connection

- Socket location follows `hc` rules: `$HC_SOCKET`, otherwise top-level `socket` in `$XDG_CONFIG_HOME/herdr-core/config.toml`, otherwise `<session>.sock` in the runtime directory
  (`$XDG_RUNTIME_DIR/herdr-core`, or `/run/user/<uid>/herdr-core` if it exists), otherwise state directory
  `$XDG_STATE_HOME/herdr-core/<session>/<session>.sock` (usually `~/.local/state/herdr-core/default/default.sock` on macOS). Session name is `$HC_SESSION`, default `default`.
  On Windows herdr-core has no socket file: it serves the named pipe `\\.\pipe\herdr-core-<user>-<session>.sock` (`%USERNAME%`, then the session; the holder beside it is `…-<session>.holder.<id>.sock`), which the node checks and dials instead (#11189); `$HC_SOCKET` and `config.toml` are not read there unless `$HC_SOCKET` is already a pipe name.
- Protocol: JSON-RPC 2.0, one message per line. Ordinary calls open a fresh connection; `terminal.list` and `doc.get` have 2-second limits (every refresh), sending 20 seconds
  (`terminal.send` waits for 3 seconds without typing on the computer), keys 8 seconds, others 5 seconds. Errors carry stable `data.kind` (`not_found`, `blocked`, etc.).
- `events.subscribe` does not use framed mode: after its response, the ordinary connection sends only notifications (`event`, `heartbeat` every 15 seconds, `missed` plus a new snapshot when behind).
  No message for 45 seconds means disconnected; reconnect with backoff. Only `terminal.attach` needs `hello {framed:true}`.
- Only a missing socket file or refused connection (no listener) means not running (`host.ErrNotRunning`): immediately answer "No terminal", clear sessions, no logs.
  Connection timeouts and similar failures mean it is still there but busy; retain the last sessions. If previously connected, log one line at the start of each outage and one on recovery.
  Opening Herdr later requires no node restart.

### Mapping

| Persona requirement | herdr-core implementation | Phase 1 |
| --- | --- | --- |
| List panes | `terminal.list`: local terminals only (address has no `/`) with `state: live` | ✅ |
| Agent type | `status.app` (program's own OSC 7501 value, unchanged): `claude-code` → claude, `codex` → codex, others are not Agents (left to internal-variant detection). If no status yet, check foreground `argv[0]` for `claude` / `codex`. Foreground shell, classic herdr, or no foreground process is not an Agent: herdr-core retains an exited Agent's last status until the terminal's own program ends | ✅ |
| State | `status.state`: working / blocked / done / idle unchanged; `error` and claude without status → unknown (error says an error occurred, not what it is doing now) | ✅ |
| Map pane to session | Foreground `foreground.pid` is claude's own pid; read `sessions/<pid>.json`; herdr-core reports no session ID | ✅ |
| Spaces / slots | Herdr-created terminals have labels `herdr.space`, `herdr.tab` (space/tab IDs in layout document `doc.get {name:"herdr-app"}`), and `where` (space / tab display name). Use document names, then `where`, then directory basename / index; indices follow document order. Non-Herdr terminals (`hc run`) group by directory into space `dir:<directory>`, with empty slot | ✅ |
| Read screen | `terminal.read {terminal, lines:0}`: plain text only, soft wraps already joined, current screen only. History reads make herdr-core send wheel events to full-screen programs and withhold user keys, so `recent` also returns only the current screen, without scrolling back | ✅ Plain text, no history |
| Send message | First `terminal.get` verifies an Agent remains (reject after exit so the shell cannot execute the message as a command), then `terminal.send {terminal, text}`: paste then Enter (bracketed paste if enabled), waiting for 3 seconds without computer typing. Rejection delivers no input: `blocked` → `dialog_open`; `status_mismatch` / `unavailable` / `finished` / `not_found` → `not_delivered` (all `delivered:none`, safe to resend) | ✅ |
| Special keys | `terminal.keys`: names match Persona (`enter`, `esc`, `shift+tab`, `ctrl+u`, `up`, etc.), forwarded unchanged | ✅ |
| Type without Enter | Not possible; see below. "Text + Enter" and `/effort` use `terminal.send`; other text input (including dialog options requiring keys to open an input field first) rejects before any keys (`cannot_type`, Chinese explanation) | Partial |
| Recognize hook pane | All terminal processes have `HC_TERMINAL_ID` (the address); without `HERDR_PANE_ID`, `personad hook` reports `herdr-core:<address>` | ✅ |
| Push changes | One `events.subscribe` connection covers all terminals; any change (except size, keep, and other-machine changes) immediately requests a snapshot | ✅ |
| Live terminal | Not implemented; see below. hub falls back to screen reads | ❌ |
| Close | Like herdr, send ctrl+c twice, then `terminal.kill` (hangup → terminate → force kill, returning terminal state after at most about 3 seconds). Herdr App also closes panes with only `terminal.kill`; when the terminal ends as `killed`, the app removes the pane (or tab if last), without `terminal.rm`. We likewise only kill, retaining the ended record. Ended or absent (`not_found`) counts as closed; still `live` after kill is an error | ✅ |
| Rename | Space and tab names live in Herdr App's layout document: `RenameSpace` / `RenameSlot` set `name` there (see "Layout document"). A directory group (`dir:`) has no name to change (`unsupported`). Session titles (`/rename`) work normally | ✅ (#2532) |
| Create | `Launcher` (#2532): `terminal.run` with no argv (the machine's shell, as the app opens one), `keep`, `no_parent`, the app's terminal identity (`TERM=xterm-256color`, `COLORTERM=truecolor`, `TERM_PROGRAM=ghostty`) and labels (`herdr.space`, `herdr.tab`, `where`, `herdr.seen: 0`), then a tab added to the layout document; the launcher goes in through the start file, as on herdr (`terminal.send` pastes into the same canonical tty). `Run` refuses unless a shell is in front. `NameAgent` does nothing (a terminal's name is set only as it starts) | ✅ |
| Takeover | No `Controller` (see "Live terminal" below) | ❌ |

### Findings

- **Are addresses reused?** No. Addresses use an ever-increasing counter (`t1`…`t9`, `t10`, `t1b`, etc.), never recycled when terminals are deleted, persisted in the state directory across restarts.
  Only clearing the state directory (or detecting a cloned disk) restarts from `t1`. Pane IDs therefore use the address directly, without `created_ms`: clearing state is a rare manual operation,
  and session recognition checks `sessions/<pid>.json` first, using old bindings only when process identification fails.
- **Can it read colors?** No. `terminal.read` is plain text; only the first `terminal.attach` frame has color. The host declares `host.PlainScreen`:
  colored reads return `unsupported`; when the app requests color, node sends plain text instead (line endings converted to `\r\n`). Claude pre-send checks only dialogs
  (`screen.Choosing`), not drafts, since draft text cannot be distinguished from gray hints without color. herdr's path remains unchanged (it does not declare `PlainScreen`).
  Extra Enter (`ensureSubmitted`) and suggested replies (`suggestion.go`) are skipped without color, and unnecessary: `terminal.send` adds Enter itself.
- **Can it paste without Enter?** No. `terminal.send` always adds Enter; `terminal.keys` accepts key names only (single letters/digits and some punctuation, not arbitrary text).
  Raw input exists only in the framed `terminal.attach` stream, and attach without `observe` resizes the terminal to our dimensions. `SendText` therefore returns `unsupported`,
  and the host declares `host.TextTyper` with `CanTypeText: false`. node sends "text + Enter" and `/effort` as one `terminal.send`; other key requests requiring text
  reject before any keys (including free-form dialog options, which previously opened the input first and failed when typing, leaving a half-open field).
- **What format is the stream?** `terminal.attach {observe:true}` first sends a complete redraw (`0x18` followed by VT sequences), then raw program PTY output, not delta frames;
  it redraws only when backlog exceeds 4 MB. node's current frames are a subset of herdr's grid redraws, and the app's custom VT subset may not handle arbitrary raw program output,
  so phase 1 has no `Streamer`; hub falls back to screen reads.
- **Can keep terminals close?** Yes, but ctrl+c alone is insufficient. Herdr App Agent terminals run `sh -ic '"$@"; exec "$0" -i' sh <agent>` with `keep`:
  the shell continues after Agent exit and the terminal remains `live`. `keep` starts a replacement shell only if a program is "lost" (killed by signal, holder gone, etc.); normal exit with an exit code and
  `terminal.kill` are final (herdr-core `terminals.rs` tests `keep_preserves_losses…`, `terminal.keep` documentation). Always follow two ctrl+c sends
  with `terminal.kill`. Tested on an owned instance: after ctrl+c terminal stayed `live` with a foreground shell; after kill it was `finished`,
  with no revival after the keep grace period (`TestHerdrCoreLiveCloseKeep`).

### Layout document

Herdr App keeps its spaces and tabs in the core's `herdr-app` document (`doc.get` / `doc.set`, checked 2026-11-09 on 0.1.19):
`{spaces: [{id, name, folder, tabs: [{id, name, root}]}]}`, where `root` is a tree of `row` / `column` nodes (`children`, a `size.weight`
per child) whose `block` leaves hold `content: {terminal: "t7"}`. Ids are a kind prefix and 32 hex digits (`sp…`, `tb…`, `p…`, `s…`); a tab's
root shares the tab's id. A space for a folder has `folder` set and an empty `name` (the app shows the folder's name); a named space may
have no folder.

- **Edited, never replaced**: decoded as generic JSON (numbers kept as written), only a space, a tab or a `name` is added or changed, so
  whatever the app keeps that Persona does not know is written back as it was. `doc.set` writes at the version read; `conflict` (the app wrote
  meanwhile) reads it again and redoes the change, at most 4 times.
- **Only a known form is written**: no document (the app never ran on this core), `spaces` not a list, a space or tab without an id or `tabs`
  / `root` of another type: nothing is written, and an automatic or directory-group start opens its terminal unplaced (its directory's `dir:`
  group, like `hc run`). An explicit app-space target is refused before opening a terminal if that layout cannot resolve it.
- **Folder identity**: automatic starts match the space's `folder` to the cleaned directory path, not its mutable display name. Each conflict
  retry repeats that match so concurrent first starts reuse the same space; `terminal.label` keeps the terminal's space and `where` labels
  consistent with the selected space. Explicit targets keep their space id even if another space matches the directory.
- **Order**: the terminal starts first (its address goes in the block), then the document; a document the core refuses ends that terminal
  rather than leave it running where the app does not show it. Cleanup errors, including a terminal still live after kill, accompany the
  placement error. A space that disappeared meanwhile refuses the start (`not delivered`).
- **The app removes closed tabs itself**: `Close` stays `terminal.kill`; Persona does not delete blocks.

### Cautions

- herdr-core has no hooks; status comes only from the program's own OSC 7501 (Claude Code sends it). Programs that do not emit it have no status.
- herdr-core does not discard `terminal.send` / `terminal.keys` abandoned by the caller: they wait for 3 seconds without computer typing and may arrive after Persona times out.
  Key timeout was shortened to 8 seconds to reduce this window; dialog answers do not recheck revision just before delivery.
- Known minor differences (not addressed yet): our parser and `hc` may differ on malformed `config.toml`; a snapshot can take about 4 seconds (`terminal.list` + `doc.get`,
  affecting only herdr-core-selected machines); pane IDs omit core identity, so clearing state restarts addresses at `t1` (process-first recognition limits impact).
- `terminal.list` also lists other machines connected to this core (addresses contain `/`); they have their own node, so skip them here.
- Live checks use only owned instances: point `HC_SOCKET`, `HC_SESSION`, `XDG_STATE_HOME`, and `XDG_CONFIG_HOME` under `/tmp`, then `hc server stop` afterward.
  On the default instance, use read-only calls only (`terminal.list`, `terminal.get`, `doc.get`); terminal-ending actions such as close are tested only on owned instances. See `server/internal/node/hostherdrcore_live_test.go` (`-tags hclive`).

## Implementation order

0. [x] **Add tests** (completed 2026-11-01, `server/internal/node/herdr_*_test.go`, `herdrfake_test.go`): characterization tests for node/herdr interactions. One shared fake herdr socket records every call from node in order;
   each test asserts "given this herdr state, what sessions are listed and what is the herdr call sequence?" Drive tests through node's request entry point, not internal fields.
   Rerun unchanged after step 1 to prove behavior stayed the same. Tests only, no business-code changes.
1. [x] **Move only herdr, preserve behavior** (merged 2026-11-01, `server/internal/host` + `node/hostherdr.go`; unit tests and `scripts/test/test-herdr.sh` live integration passed;
   there were no Claude panes in herdr at deployment, so `live_panes_check.py` real-pane comparison remains unrun; rerun once panes exist, observe for a day or two before step 2): define interfaces, move all herdr calls behind them, implement `PaneRef` and prefixed storage together. Existing and corpus tests all pass;
   step 0 tests may change only the "Node adapters" block in `herdrfake_test.go`; not one line of the three `herdr_*_test.go` files, or behavior preservation is not proven.
   After deployment, compare real panes with `scripts/diag/live_panes_check.py` to confirm session recognition is unchanged. Merge and deploy separately, run a day or two before proceeding.
2. [x] **Orca phase 1** (merged 2026-11-01; temporary Orca terminals tested for listing, single/multiline sends, rejection while suspended, and closing): discovery, status, sends, keys, plain-text screen reads, screen-based dialog answers, close, as the third engine ("Orca source" above).
   Test with a fake Orca socket (`orcarttest`, validating Orca parameter rules); read-only checks against active Orca confirmed all session IDs matched `sessions/<pid>.json`.
   Another host: [x] **herdr-core phase 1** (merged 2026-11-01, one engine per machine): "herdr-core host" above. Close supported (2026-11-01); rename omitted because Herdr App owns it. Create and rename through the app's layout document since #2532. Later: paste without Enter (await upstream method), live terminal.
   [x] **herdr + herdr-core combined** (2026-11-03, `herdr-both`, "Combined engine" above): multiple hosts in the table, parallel refresh no longer waiting for the slowest.
3. [ ] **App**: engine selection is done (pairing, **Settings › Machines**, options from hub `engines`, 2026-11-01); capability-based button hiding and generic wording are not.
4. [ ] Later: Orca phase 2 (create, modes), phase 3 (live terminal).

## Fixes deferred until after step 1 merges

Issues discovered while adding tests, with tests pinning current behavior. Fixing now would change the before/after-refactor baseline, so address separately after step 1, updating corresponding tests too:

- Switching to `bypassPermissions`, `tuiSetMode` gives up after 6 shift+tab presses, leaving Claude in a different mode (not the original). Restore the original on failure, or verify the target exists in the cycle first (`node/modes.go`).
- Fixed (#4873): when `pane.list` fails, `close()` closes only the requested pane (herdr host `Close`, `node/hostherdr_launch.go`). herdr closes the tab itself if that was its last pane, preserving other split panes otherwise.
- Four existing `catchup_test.go` tests fail with `CLAUDE_CONFIG_DIR` set: they assume Claude config under HOME. Clear this variable in tests (new `herdrNode` already does).
- `TestTerminalControlEndsWhenTakenOrForgotten` in `terminal_test.go` changes global `termLease` while a still-running `controlLoop` reads it: occasional `-race` failures (seen once by the engine-selection worker with `-count=10`; not reproduced in 30 runs each on branch and master). Wait for `controlLoop` before test completion, or make the lease an instance field.
- Other suspicious behavior (not necessarily needing fixes): fallback from failed herdr snapshot to `agent.list` retains old space / slot names; `editQueue` clears lines based on transcript queue, not what the TUI actually restores.

## Open decisions

- Report capabilities per session or per host on each machine (leaning toward the latter, with sessions carrying only host kind).
- ~~Displaying one session in two hosts simultaneously~~: settled in #2576. It is listed once, owned by one host, and its row carries `also_open_in` (`docs/integrations/engines.md` "Host sets").
- Sessions outside any host but with a live process (ordinary terminal): mark "Running elsewhere" and disable sending? `sessions/<pid>.json` alone can detect this, independent of host abstraction, so it can be done first.
