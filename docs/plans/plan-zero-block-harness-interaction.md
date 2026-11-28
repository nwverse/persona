# Zero-Block Harness Interaction Architecture

## Executive Summary

Persona interacts with three primary coding agent harnesses: **Claude Code**, **Codex**, and **Antigravity (AGY)**. Across all three harnesses, agents run in diverse execution environments—managed terminal engines (`herdr`, `herdr-core`, `orca`, `tern`, `rex`), external `tmux` sessions, standalone CLI terminals, background processes, and desktop applications.

The **Zero-Block Architecture** guarantees that user interactions (viewing progress, sending prompts, answering structured questions, approving tool permissions, and reviewing plans) never deadlock, never silently drop approvals, never freeze external terminals, and never leave sessions stuck in false `blocked` states.

---

## Architectural Principles & Key Design Decisions

### 1. Claude Code: Seamless Plugin & Terminal Bridging

#### 1.1 Plugin Permission Bridge with Strict Mutual Exclusion
- **Local IPC**: The `persona@persona` plugin connects to the local node's Unix domain socket (`node.sock`) via HTTP long polling (`POST /claude-plugin/v1/poll`, `/permission`, `/permission-done`).
- **Mutual Exclusion Guarantee**: When a session is launched with Persona's managed hooks (`--plugin-dir ~/.persona/claude-hooks`), the node records `notePersonaHook`. This immediately revokes plugin permission authorization and withdraws any active plugin cards for that session, strictly preventing double cards or competing approvals.
- **Broadened Authorization Scope**: Manually started Claude sessions inside `herdr`, `herdr-core`, `orca`, `tern`, `rex`, and external `tmux` without `persona-hooks` now receive plugin service authorization (`ccPluginServes = true` and `ccPluginServesPermissions = true`).

#### 1.2 Peer Inbox Decoupling
- **Independent Send Capability**: An active plugin input connection (`ccPluginInput`) independently grants `Capabilities.Send = true` and `Capabilities.PluginInput = true`, clearing `ReadOnlyReason`. Sessions without a peer inbox are no longer marked read-only or refused with `open_elsewhere`.
- **Send Precedence**: In message dispatching (`sendOutsidePriority`), owner prompts are dispatched via `ccPluginSend` first. Peer inbox delivery is only used as a fallback when the plugin is unavailable or inactive.
- **Native Interaction Priority**: When the plugin permission bridge is active (`ccPluginPermissionsCapable = true`), `AskUserQuestion` calls are handled as native `ix_` interaction cards (`proto.IxQuestion`) via `classic.PermissionRequest`. Duplicate fallback `clq_` cards are suppressed, falling back to peer inbox only when no plugin bridge exists.

#### 1.3 Plugin Resilience & Grace Alignment
- **Soft Downgrade for `classic_passed_over`**: Claude Code's managed-settings guard (`cc-plugin-sec-default`) can bypass user-tier `classic.*` hooks. Instead of permanent lockout, the plugin requires two consecutive turns with hookable tool calls and zero `PreToolUse` before declaring `passedOver`. Built-in hookless tools (`View`, `GlobTool`, `GrepTool`, `ToolSearch`) are explicitly excluded. Any subsequent `classic.PreToolUse` immediately resets the observation and restores bridging.
- **Poll Reconnect Grace Window**: A single transient failure on `/poll` (e.g. during a node restart) preserves `permissionAuth` for a 2-minute grace period (`AUTH_GRACE_MS = 120s`). New `classic.PermissionRequest` calls perform independent reconnects using the cached socket path without dropping approvals.
- **Grace Window Alignment**: `PERMISSION_GRACE_MS` is aligned to 1,000 ms (1s) to match the node's `decided.grace` (`permissionGrace = 1s`), preventing local fast allow hooks (e.g., `bypass-allow`) from causing ghost pushes and card retractions.

#### 1.4 State Blindspot Self-Healing
- **Herdr-Core Resting Inspection**: `restingClaude` includes `host.HerdrCore`, ensuring that dialogs on herdr-core screens are actively inspected when turns end.
- **Tmux Live Lines**: `liveHosts` includes `host.Tmux`, exposing live status lines for tmux sessions.
- **Transcript Advancing Self-Healing (`claudeTranscriptWorkingOverBlocked`)**: When a host reports `StatusBlocked`, the node verifies whether the transcript has advancing turn activity after the blocked transition (`claudeBlockedStaleGrace = 10s`, `claudeFreshWorking = 45s`). If transcript activity is advancing and no modal dialog, active ask, or screen prompt exists, the session self-heals from `StatusBlocked` to `StatusWorking`.

---

### 2. Codex: Non-Intrusive Non-Blocking Coordination

#### 2.1 The Non-Intrusive Principle
- Codex sessions must never be frozen or arbitrarily locked by Persona.
- Persona coordinates via the native `app-server` daemon, subscribing to threads, broadcasting `ServerRequest` approvals, and observing rollout streams without disrupting interactive TUI users.

#### 2.2 Transparent Daemon Join
- External Codex CLI sessions started without `--no-daemon` share the managed `app-server` daemon. The node auto-discovers active threads via UDS and synchronizes permissions.
- In `herdr-core` or `--no-daemon` environments, the node transparently pairs rollout journals and screens, ensuring prompt approvals and plan confirmations remain non-blocking.

#### 2.3 Rollout-Based Sticky Blocked Self-Healing
- `codexRolloutWorkingOverBlocked` checks if rollout activity has advanced past the blocked timestamp without open dialogs, automatically clearing stale blocked flags.

---

### 3. Antigravity (AGY): Verified Presences & Non-Destructive ACP

#### 3.1 OSC 7501 Fake Blocked Disambiguation
- When terminal output sends OSC 7501 `blocked`, the node cross-checks `agyhooks` (`Stop` settled), the conversation summaries database (`IDLE`/`DONE`), and a clean screen prompt to heal sticky false blocks into `StatusIdle` / `StatusDone`.

#### 3.2 Non-Invasive Presence Detection
- On Darwin, the node inspects `.lock` file holders using non-blocking system process inspection (`lsof -Fn` / `procinfo`) to prevent false acquisitions, 60s timeout rejections, or concurrent ACP write collisions.

#### 3.3 Non-Destructive ACP Suspend & Plan Integration
- `runtime.js` handles `res.pending` by non-destructively suspending the turn rather than cancelling it with `acp.cancel(rawId)`.
- `exit_plan_mode` and Artifact `RequestFeedback` map directly to native `proto.IxPlan` cards.

---

## Multi-Wave Roadmap & Status

```
+---------------------------------------------------------------------------------+
| Wave 0: Infrastructure & Deploy Baseline (Completed)                            |
| - NAS Hub blue-green deployment & Postgres backup                               |
| - Multi-platform binary cross-compilation & clean codesign                      |
| - Node service restart and live link verification                              |
+---------------------------------------------------------------------------------+
                                      |
                                      v
+---------------------------------------------------------------------------------+
| Wave 1: Core Harness Reliability & Zero-Block (Current)                         |
| - Claude: Plugin scope broadening, peer inbox decoupling, plugin stability,     |
|           herdr-core/tmux status self-healing (Completed in #17407)             |
| - Codex:  App-server thread join, non-blocking approvals, herdr-core fixes     |
| - AGY:    OSC 7501 self-healing, Darwin presence detection, ACP plan mapping    |
+---------------------------------------------------------------------------------+
                                      |
                                      v
+---------------------------------------------------------------------------------+
| Wave 2: Unified Cross-Harness Capabilities (Planned)                            |
| - Unified Elicitation across Claude, Codex, and AGY                             |
| - Consistent plan approval and modification feedback                            |
| - Deep terminal input and queue resilience                                      |
+---------------------------------------------------------------------------------+
                                      |
                                      v
+---------------------------------------------------------------------------------+
| Wave 3: Governance, Ratchets & Client Parity (Planned)                          |
| - Automated zero-block regression ratchets in CI                                |
| - Continuous multi-harness audit suites                                         |
| - Multi-platform client parity (iOS, Android, Web, Mac)                        |
+---------------------------------------------------------------------------------+
```

---

## Exclusive File Boundaries & Per-Package Ownership

To prevent merge conflicts and cross-cutting regressions across concurrent worker streams, file ownership is partitioned by harness:

| Package / Boundary | Exclusive Scope | Responsible Agent / Track |
| :--- | :--- | :--- |
| **Claude Bridge & Outside** | `server/internal/node/ccplugin.go`<br>`server/internal/node/ccplugin_perm.go`<br>`server/internal/node/claudebg.go`<br>`server/internal/node/claudeoutside_ask.go`<br>`server/internal/node/agent_status.go`<br>`server/internal/node/claudestatus.go`<br>`integrations/claude-code-plugin/**` | Claude Track (`w1-claude-bridge`) |
| **Codex Harness** | `server/internal/node/codex*.go`<br>`server/internal/node/appserver*.go`<br>`integrations/codex/**` | Codex Track (`w1-codex`) |
| **Antigravity (AGY)** | `server/internal/node/hostherdrcore.go`<br>`server/internal/node/agypresence*.go`<br>`server/internal/node/agytranscript.go`<br>`server/internal/node/agybridge.go`<br>`integrations/antigravity/**` | AGY Track (`w1-agy`) |
| **Shared Composition** | `server/internal/node/sessions.go` (isolated sections only) | Handled with minimal surgical diffs |

---

## Sanitization Verification

This architecture document adheres strictly to public repository guidelines:
- **No private IP addresses, hostnames, or intranet domains**: All network references use generic terms (`node.sock`, `HTTP/1.1`, local UDS).
- **No proprietary account names or personal emails**: All examples use generic placeholders (`<uuid>`, `<session_id>`, `user`).
- **No secrets or tokens**: Zero cryptographic credentials, auth tokens, or passwords are contained within this specification.
