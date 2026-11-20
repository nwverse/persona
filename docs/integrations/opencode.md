# opencode

[opencode](https://opencode.ai) integrates with Persona in two complementary ways:

- **Starting new Agent sessions and Persona Assistant conversations** from Persona (Web / iOS / Android): `integrations/opencode` advertises the `opencode` launcher, its configured models (`launchers.models`, including `provider/model` IDs such as `opencode-go/...`), `session.start`, `session.modes`, and `assistant: true`.
- **Controlling and mirroring existing opencode sessions** (including opencode TUI panes in herdr): opencode in a herdr pane remains a single herdr session (state, terminal, keys and tabs belong to herdr) enriched with chat content, streaming, sending, interrupt, rename, and phone permission cards from the runtime. Without the plugin/companion, herdr panes are still listed as terminal-only rows.

Supported since 2026-11-05 (issue #3070, related #1178); Agent creation and Assistant support added for issue #12000. opencode is built on `integrations/runtime-sdk/` (`docs/integrations/runtimes.md`); the node side is `server/internal/node/opencode.go`.

| Where opencode runs | What it is in Persona | Who handles what |
| --- | --- | --- |
| **Standalone ACP companion** (`integrations/opencode/companion.mjs`, no resident `opencode serve` needed) | Launcher `opencode` (Agent & Assistant); live ACP sessions (`source_kind: "runtime"`), plus disk-only sessions from `opencode.db` listed as `offline` | Companion spawns `opencode acp` on demand, serves Persona MCP tools over a private loopback HTTP proxy, discovers models via `opencode models --verbose`, reads both 1.x (`session`/`message`/`part`) and 2.x (`session_v2`/`session_message`) `opencode.db` |
| **In a herdr pane** (opencode's TUI, connected to the shared `opencode serve`) | A herdr session, same as Claude in herdr | Pane, space, tab, state, terminal view, keys: herdr; chat content, sending, interrupt, rename, cards, streaming: the Persona plugin in the server process |
| Not in herdr (web, `opencode run`, another terminal connected to `opencode serve` with the plugin) | A session of runtime `opencode-<pid>` (`source_kind: "runtime"`) | The plugin (`integrations/opencode/index.js`); no terminal view; also supports `session.start`, `launchers.models`, `session.modes`, and `assistant: true` when the server exposes v2 plugin APIs |
| In a herdr pane, plugin not installed | A herdr session, terminal only | The chat is a single explanatory note, sending is rejected (`opencode_no_extension`), interrupt presses Esc |

## Structure

```
 App ── hub ── node ── ~/.persona/runtime.sock ◄── opencode ACP companion (runtime `opencode`, `companion.mjs`)
                │                                      · `opencode models --verbose` for catalog
                │                                      · `opencode acp` for `session.start`, Agent & Assistant turns, `session/load`
                │                                      · loopback HTTP MCP proxy (`127.0.0.1:0` + per-process Bearer token)
                │                                      · read-only `opencode.db` (1.x + 2.x) for offline/historical sessions
                │
                ├── ~/.persona/runtime.sock ◄── Persona plugin in `opencode serve` (runtime `opencode-<pid>`, `index.js`)
                │                                      · list, chat: read-only `opencode.db` (`session_v2`/`session_message`)
                │                                      · live: server-wide event stream
                │                                      · control: `session.create`/`prompt`/`interrupt`/`update`/`switchModel`, `permission.reply`
                │
                └─ herdr ── opencode TUI in the pane (herdr's own opencode plugin reports state, session id, title)
```

- **Companion and plugin side by side**: the standalone companion (`runtime: "opencode"`) lists disk-only sessions as `offline` and declares `requires: ["prefer_running"]`. When a live `opencode serve` plugin (`runtime: "opencode-<pid>"`) is also connected, node routes live sessions to the running plugin while both coexist without colliding on runtime IDs.
- **One connection per server process (plugin)**: opencode attaches every TUI to the same `opencode serve --service`. The plugin runs in that process and **is loaded once per project directory (location)** (only when the first request for that directory arrives). The plugin keeps a host in `globalThis`, and each directory's instance registers its context there. The event stream is server-wide, so only one instance (the primary) reads it; when it goes away the next one takes over, and events are deduplicated by id. For opencode 1.x servers that expect a `server()` hook on default-exported plugins, `index.js` also provides a no-op `server()` alongside `setup()`.
- **How a herdr row matches a runtime row**: herdr's opencode plugin reports `agent_session` in the pane (opencode's session id, `ses_…`), with the title `OC | <title>`.
  node merges the herdr row and the runtime's row into one by session id (`mergeOpencodeRow`): title (the runtime's is complete, herdr's gets truncated), last line, model, context usage and revision come from the runtime;
  state comes from herdr (when the runtime says `blocked`, it also counts as "Needs you"). Panes for which herdr reports no session id do not disappear: they are listed as terminal-only rows `opencode-<pane>`.
  Calls are routed by type: `session.keys` / `screen` / terminal / tabs go to herdr, everything else (content, sending, interrupt, rename, cards) goes to the runtime.
- **Title**: herdr's `OC | ` prefix is stripped; before a title is generated, opencode's terminal title is just `OpenCode`, which is not treated as a title.
- **Notifications**: state comes from herdr for TUI sessions and from the runtime for standalone sessions; deciding "Needs you" / "done" and pushing use the hub's existing logic.

## Starting Agents and Persona Assistant

### Launcher and models

Both `companion.mjs` and `index.js` advertise:

- `launcher: "opencode"` and a `launchers` entry `{ name: "opencode", display_name, agent: "opencode", model, models }`
- `caps`: `start`, `models`, `modes`, `model` (`modelSwitch`), `send`, `interrupt`, `rename`, `assistant` (plus `close` and `readonly` on the ACP companion)

Model discovery (`integrations/opencode/lib/models.js`):

- **ACP companion**: runs `opencode models --verbose` (parsing `providerID`, `id`, `name`, `limit.context`, and `variants` → Persona `efforts` `low` / `medium` / `high` / `xhigh` / `max`), reads the configured default `model` from `<opencode config dir>/opencode.jsonc` / `opencode.json`, and merges `configOptions` returned by ACP `session/new`.
- **v2 plugin**: reads `ctx.model.list()` and normalizes entries into `provider/model` IDs.
- **Model resolution**: supports `provider/model` (including `opencode-go/...` and nested slashes such as `openrouter/anthropic/...`), `provider:model`, and unique bare model IDs matching the discovered catalog; unknown bare IDs are rejected with `bad_request`.

### Agent sessions (`session.start`)

- `session.start` accepts `cwd`, optional `prompt`, `images`, `model`, `permission_mode` / `mode`, `client_msg_id`, and `assistant`.
- In the ACP companion, `session/new` starts the session in `cwd`, applies `mode` (`plan` for read-only, failing closed with `readonly_unsupported` if opencode rejects switching to `plan`) and `model` (`session/set_config_option`) before sending the initial prompt/images, and attaches a per-session loopback HTTP MCP server with ordinary Agent tools (`assistant: false`).
- In the v2 plugin, `ctx.session.create` (with `ctx.session.switchModel` fallback) creates the session in the matching directory context and records it in `createdSessions` so it is visible before `opencode.db` flushes.
- Started sessions report `started_by_persona: true` and `launcher: "opencode"`.

### Persona Assistant (`assistant: true`) and safety boundary (#11220)

- **Workspace instructions (`AGENTS.md`)**: when Persona starts an Assistant chat in `~/.persona/assistant`, opencode natively loads `<cwd>/AGENTS.md` into its system prompt.
- **Persona MCP tools (`integrations/opencode/lib/mcp.js`)**: a private loopback HTTP MCP server (`127.0.0.1:0`, 32-byte random Bearer token checked with `timingSafeEqual`, one route per session) proxies `tools/list` and `tools/call` through `rt.tools` / `rt.callTool` over `~/.persona/runtime.sock`. Hub credentials are never passed to opencode. During ACP `session/new` (before `session.start` returns to node), `tools/list` calls `rt.tools({ assistant })` without `session_id`; once created and bound, `tools/call` supplies `session_id` and `assistant`.
- **Live context snapshot**: on `session.send` with `context`, the runtime prepends `<persona-context>\n...\n</persona-context>\n\n` for the model and strips attribute-less `<persona-context>` / `<jarvis-context>` blocks when mapping user messages back to Persona (`visibleUserText`), while keeping `<persona-context kind="forward">` blocks visible.
- **Permissions, memory writes, and command gates**: Assistant chats route approvals to the phone (`answerFirst` overridden for assistant sessions), auto-allow `write`/`edit` inside `memory_dir` outside `plan` mode, reject writes/shell in `plan` mode, and always reject shell commands matching Persona tool command gates (`tool.shell`) even under `bypassPermissions`.
- **No `caps.supervision_policy` (fail-closed per #11220)**: `opencode acp` advertises `{ close, fork, list, resume }` without `additionalDirectories`, so opencode cannot load an isolated per-session rule file outside `~/.persona/assistant/AGENTS.md`. Therefore `integrations/opencode` **deliberately omits `caps.supervision_policy`**, and both the node (`server/internal/node/assistant_runtime.go`) and the runtime itself fail closed with `assistant_supervision_runtime` if any non-empty `supervision_instructions` is supplied. Shared `~/.persona/assistant/AGENTS.md` is never rewritten and supervision rules are never concatenated into user prompts.

## Terminal rows for other agents

node used to drop every agent herdr reported that it did not recognize (`gemini`, `amp`…) (`if a.Agent != "claude" { continue }`), so the pane was invisible on the phone.
Now, when herdr reports an agent but Persona has no way to read its chat, it is **listed as a terminal-only row** (`otherAgent`): pane, space, tab, state and title are herdr's,
the session id is `<agent>-<pane>` (the agent's own session id is of no use; this row is the pane), the chat is a single note "Persona can't read <agent>'s chat yet", sending is rejected (`terminal_only`; no blind typing into an unknown TUI),
and the terminal view and keys work as usual (herdr's).
Panes for which herdr reports no agent (`agent: ""`) and botmux panes are still not listed. This row cannot be closed (`session.retire` refuses).

## Where the chat comes from

opencode's plugin API provides the session's context (what remains after compaction) and the event stream, **but no session list and no full message read**. opencode's own database `opencode.db` (SQLite, WAL) holds the full history:

- **2.x schema**: `session_v2` and `session_message` (read by both `index.js` and `companion.mjs`).
- **1.x schema**: `session`, `message`, and `part` (auto-detected and read by `companion.mjs` via `v1SessionItems` / `v1ImageOf`).
- Both runtimes **only read** `opencode.db` (`file:…?mode=ro`, `PRAGMA query_only`, 300 ms busy timeout): `bun:sqlite` inside opencode's process, `node:sqlite` in Node / companion / tests. Location is `$XDG_DATA_HOME/opencode/opencode.db` (default `~/.local/share/opencode`); `PERSONA_OPENCODE_DB` can point elsewhere.
- Pages for sessions not known to opencode return `not_found`, so node does not misattribute DSH, pi, or Antigravity sessions to opencode.

### seq and "a message still being written"

In `opencode.db` v2, `session_message.seq` is the sequence number in opencode's event log: unique within a session, only increasing, **with gaps**, so it is used directly as the item's seq.

- An assistant message is **rewritten in place** during its step (tool calls complete one by one) and its seq does not change, while the SDK's `append` skips batches with `toSeq <= already pushed`.
  So in the plugin, **only finalized messages are pushed**: a message counts once `time.completed` is set (or it has `finish`); in-between state is covered by streaming text and "working".
- In the ACP companion, live turns emit incremental `session.items` batches as tool calls, thinking, and text settle, plus `session.delta` for streaming text.
- An item's id is `<seq>#<part index×2>` and a tool result's is `<seq>#<part index×2+1>`, so when a result completes, the ids of later parts in the same message do not shift.
- `history` = `opencode:<session id>`; when the user reverts in opencode (`revert` non-empty), its hash is appended and the app refetches the whole page.

### Mapping

`integrations/opencode/lib/map.js`, `session_message` / v1 `message`+`part` / ACP updates → Persona item:

| opencode | Persona |
| --- | --- |
| `user`: `text`, `files` (attribute-less `<persona-context>` stripped; `<persona-context kind="forward">` kept) | user `text`; images are placeholders (`lazy`, data via `session.image`), other files become one line `［附件 名字］` ("[attachment name]") |
| `assistant`'s `reasoning` / `text` / `tool` | `thinking` / `text` / `tool_use` (+ `tool_result` once completed or failed, with `tool_use_id`, `is_error`) |
| Tool names and arguments (drawn the Claude way) | `shell`/`bash`→`Bash` (`command`, `description`), `read`→`Read` (`path`/`filePath`→`file_path`), `write`→`Write`, `edit`→`Edit` (`oldString`/`newString`→`old_string`/`new_string`), `grep`→`Grep`, `glob`→`Glob`, `subagent`/`task`→`Agent`, `webfetch`→`WebFetch`, `websearch`→`WebSearch`, `skill`→`Skill`; others (`patch`, `execute`…) keep their names |
| `finish: error` / interrupted (`error.type: aborted`) | notice `出错了：…` ("Error: …") / `已中断` ("Interrupted") |
| `finish: length` / `content-filter` | notice `输出达到上限，这一轮停了` ("Output hit the limit; this turn stopped") / `这一轮被拦下了` ("This turn was blocked") |
| `idle` (`outcome: interrupted`) | notice `已中断` ("Interrupted") |
| `compaction` | notice `已压缩上下文` ("Context compacted") |
| Commands the user runs with `!` (`shell`) | A `Bash` `tool_use` + `tool_result` pair |
| `system`, `synthetic`, model / agent / directory switches | No item (seq still advances) |

Sub-agents (child sessions with non-empty `parent_id`) are not listed as separate sessions; their permission requests count toward the root session.

## Permissions and questions

- **Permissions** (`permission.asked` in the plugin, `session/request_permission` in ACP):
  for TUI sessions, `answerFirst` (`auto` / `phone` / `local`) decides whether the phone or TUI answers first, and whoever answers first wins. For ACP companion sessions and Assistant chats, permission cards always go to the phone (unless auto-allowed by `memory_dir` / `bypassPermissions` or auto-rejected by `plan` mode / command gates).
- **Questions** (`form.created`, opencode's question tool in the plugin): the plugin API cannot answer forms, so it is handed back to the terminal (`blocked` / "Needs you").

## Installation

On the computer that runs opencode (it must be in the hub's machine list, and node must be able to start).

### Option A: Standalone ACP companion (recommended for starting Agents & Assistant without `opencode serve`)

Requires Node 22+ and `opencode` on `PATH` (or `PERSONA_OPENCODE_BIN` / `--bin`):

```sh
node <Persona repo>/integrations/opencode/companion.mjs
```

Options: `--bin <path>`, `--db <path>`, `--socket <path>`, `--config-dir <dir>`, `--launcher <name>`, `--name <label>`, `--model <provider/model>`, `--no-assistant`.

### Option B: opencode server plugin (for `opencode serve` and herdr TUI sessions)

1. `node <Persona repo>/integrations/opencode/install.mjs`: writes `<opencode config dir>/plugins/persona.js` (`export { default } from "<Persona repo>/integrations/opencode/index.js"`). Refuses to overwrite foreign files; `--force` overwrites, `--uninstall` removes.
2. Restart `opencode serve --service` once so it loads the plugin.
3. Keep herdr's opencode integration (`herdr integration install opencode`) installed if you also use opencode TUI panes in herdr.

Both Option A and Option B can run at the same time on the same machine.

### Configuration (optional)

`<opencode config directory>/persona.json`:

| Field | Default | Description |
| --- | --- | --- |
| `answerFirst` | `auto` | Who gets the permission card first for non-assistant server sessions |
| `launcher` | `opencode` | Launcher name advertised to Persona (empty string disables launcher registration) |
| `displayName` | `opencode` | Launcher display name in Persona |
| `model` | from `opencode.jsonc` / `opencode.json` | Default `provider/model` for the launcher |
| `assistant` | `true` | Advertise `caps.assistant` so Persona can start Assistant conversations on opencode |
| `listDays` | `7` | How many recent days of non-running sessions to list |
| `maxSessions` | `80` | Maximum list length |
| `socket` | `~/.persona/runtime.sock` | node's runtime socket; `PERSONA_RUNTIME_SOCKET` also works |

Environment variables: `PERSONA_OPENCODE_BIN` (companion `opencode` binary path), `PERSONA_OPENCODE_DB` (location of `opencode.db`), `PERSONA_OPENCODE_DEBUG=1` (debug logs to stderr).

## Token usage

The node meters opencode's token usage from its database (the same file this runtime reads, `PERSONA_OPENCODE_DB` included), with the system `sqlite3`: each finished step and compaction, reasoning counted as output, under the agent `opencode` (`docs/server/machines.md`, "Token usage").

## Limits and later

- **No assistant supervision (`caps.supervision_policy`)**: opencode does not support isolated per-session rule loading (`additionalDirectories`), so supervised tasks cannot use opencode as supervisor; Persona fails closed with `assistant_supervision_runtime` (#11220).
- **No herdr pane opening / resume_argv**: `session.start` creates a runtime session (`source_kind: "runtime"`), not a new herdr terminal pane; stopped herdr panes still cannot be resumed in-place (`no resume_argv`).
- **Plugin `session.start` requires opencode v2 plugin APIs** (`ctx.session.create`, `ctx.mcp.add`): on opencode 1.x servers, use the standalone ACP companion (`companion.mjs`) to start new Agents and Assistant chats.
- **Tool calls in the plugin** only appear in the chat once the step ends (see "seq" above); in the ACP companion they stream as `tool_call` / `tool_call_update` arrive.
- **Questions (`form.created`)** in the plugin cannot be answered on the phone and are handed back to the terminal.
- **Auto-update bundling** (bundling the package into `personad` via `runtimepkg.Packages`, as pi does) is not done yet; `opencode` runs directly from the checkout.

## Pitfalls

- **`setup` must return immediately.** opencode makes requests wait on "Location plugins did not finish activating"; `await`ing the event loop inside `setup` makes every request for that directory return 503. Run the event loop in the background (`void`).
- **`setup` must return a cleanup function**, and the event subscription must abort with it (`ctx.event.subscribe({signal})`).
- **The event stream is server-wide; every directory's instance receives every event**: dedupe by event `id`.
- **MCP `tools/list` during ACP `session/new`**: opencode connects to `mcpServers` and calls `tools/list` before `session/new` returns a `sessionId` (and therefore before node registers ownership of the new session). The runtime calls `rt.tools({ assistant })` without `session_id`, and passes `session_id` on `tools/call` after binding.
- The default public models return 403 in some regions (`This model is not available in your country`): end-to-end tests use a scripted fake model (`e2e/mock-llm.mjs`), configured as a custom provider.

## Verification

- `cd integrations/opencode && npm test`:
  - `test/models.test.js`: JSONC config parsing, `opencode models --verbose` & plain CLI parsing, ACP `configOptions`, catalog normalization/efforts/windows/cap, `resolveModel` (`opencode-go/...`, nested slashes, `provider:model`, bare matching, error branches), `buildLauncher`.
  - `test/mcp.test.js`: loopback HTTP MCP proxy Bearer auth, route isolation, `allocate` + `bind`, `tools/list`, `tools/call` (text + image), `extractShellGates` / `matchShellGate`.
  - `test/map.test.js`: tool names & inputs, user/assistant items, hidden attribute-less `<persona-context>` vs visible `<persona-context kind="forward">`, 1.x `v1SessionItems` / `v1ImageOf`, `acpPermissionCard`.
  - `test/opencode.test.js`: v2 plugin listing, pagination, blob images, streaming, permissions, subagents, `hello` capabilities (without `supervision_policy`), `launchers.models`, `session.start` (Agent & Assistant), MCP registration, `memory_dir`, `plan`, command gates, `modes`, fail-closed `supervision_instructions`.
  - `test/companion.test.js`: standalone ACP companion `hello` (without `supervision_policy`), 1.x DB `offline` listing + `session/load` resume, `launchers.models`, Agent `session.start` + streaming + tool calls + permissions + `modes` + `rename` + `close` + `interrupt`, Assistant `session.start` + end-to-end HTTP MCP round-trip + context hiding + `memory_dir` + `plan` (`readonly_unsupported`) + command gates + fail-closed `supervision_instructions`.
  - `test/install.test.js`: shim installation and uninstall.
- node: `server/internal/node/opencode_test.go`: herdr TUI merge, blocked state, plugin-less & unknown-agent terminal rows, `launchers.list` (`Assistant: true`) & `launchers.models` & `session.start` alongside herdr merge, and Assistant `assistant.send` + MCP authorization + fail-closed `assistant_supervision_runtime` without `caps.supervision_policy`.
- End to end (v2 server plugin): `PERSONAD=<personad> node integrations/opencode/e2e/run.mjs` (previously verified 16/16 against opencode 2.0.32).
- Web: fake hub with `MOCK_OPENCODE=1` + `scripts/test/web_opencode_e2e.py`.
