# Launcher health checks and automatic repair (#2873)

Launchers on your computers break silently: the gateway rejects a bare model name (`400 Bare Claude model name cannot be routed`, and after switching models `no account match`),
the gateway key expires, the CLI is too old, or starting an Agent keeps timing out (`context deadline exceeded`). Nobody used to notice, and a dispatched Agent failed on its first message.
Now a hub manager (`server/internal/assistant/launcher_health*.go`) periodically checks every launcher on every computer, routes work around broken ones, repairs known errors itself, and records what it cannot repair as blocked.
**Record only, never push**: no notifications, nothing on the Dynamic Island; the App and the board read it themselves.
**Off by default** (decided 2026-11-05): periodic checks and automatic repair only run when the manager's `manager.json` sets `agents.launcher_health.enabled: true` (`docs/operations/config.md` ("manager.json")); when it is off, failed Agent starts are still recorded and still routed around.

## Checks

- It reuses "Test availability" (`docs/integrations/launchers.md`): the node's `launchers.check` runs `claude -p … --max-turns 1` / `codex exec` once in an empty directory, with a 90-second timeout, spending a few tokens.
- When: it looks every 5 minutes (`healthTick`); on computers that are online (`Reachable`), any launcher last checked longer ago than the interval (default 30 minutes) is rerun.
  When a computer goes from offline to online, all its launchers are checked right away. Launchers on one computer run one after another (no burst of claude processes); computers run in parallel.
- What is checked: launchers this computer can start (`machineLaunchers`: those from ~/.zshrc minus hidden ones, plus Persona-managed ones; `kind: api` launchers do not run on a computer and are not checked).
  Runtime launchers (DSH and others, whose `source` is not `zshrc` / `persona`) are not checked.
- A failed Agent start is recorded too: when `agent_start`, a task plan starting an Agent (`startTaskAgent`) or a repair Agent's start gets an error from `session.start`, it is recorded as a failure (source `start`),
  and a check runs right away to confirm (one network blip should not leave a launcher unavailable; if the check passes, it recovers).
  The App's own starts count as well (#1642, `launcher_health_starts.go`): the hub tells the manager how a start it waited for went (`session.start_ended`: a failure, or a start the node ran before answering that worked),
  and a "New Agent" answered at once (`async`) is followed in the machine's session list, from `starting` to started or to `start_error` (counted once while the node lists it: the starts counted are kept with the records, so one is not counted again after the manager restarts; the failure's `updated_at` is the node's clock and is not compared with the record's).
  A start whose outcome the hub does not know (the computer offline, the call timed out) and a request the node refused before running anything are not recorded.
- Stored in the settings key `launchers.health` (Postgres), one entry per computer × launcher: success / failure, error text (redacted, truncated), error category,
  check time, consecutive failure count, repair log, state. It survives hub restarts.

## Error categories (`classifyLauncherError`)

| Category | Matches | Automatic repair |
| --- | --- | --- |
| `model_route` | `Bare Claude model name`, `cannot be routed`, `no account match`, model does not exist / not supported | Repair Agent (reconfigure the gateway's model) |
| `auth` | 401 / 403, `invalid … api key`, `authentication_error`, `Not logged in`, expired token | Repair Agent (reconfigure the gateway's key) |
| `cli_outdated` | `unknown option`, asks to upgrade / version too old / no longer supported | First the node upgrades itself (`claude update`); if that fails, a repair Agent |
| `timeout` | `context deadline exceeded`, no answer within 90 seconds | Counts only after two in a row; handed to a repair Agent |
| `other` | Everything else | Same: repaired only after two in a row |
| `not_in_terminal` | A start's new terminal tab could not run the launcher or its claude (`launcher_missing` / `claude_missing` with `where: "terminal"`, #1642, `docs/integrations/launchers.md` "The terminal's shell is another environment") | None: `blocked` at once, the reason says to define the launcher where the terminal's shell reads it or add it as a model provider |

## States

`ok` → on failure `failing` → while repairing `repairing` → back to `ok` when the retest after repair passes, `blocked` if the retest still fails. At any point, one passing check returns it to `ok` and clears the block.
Except `not_in_terminal`: the check runs in the node's login shell, where the launcher works, so neither the check right after the failed start nor the periodic rounds run for it, and a check asked for by hand does not clear it.
It clears when a start with that launcher on that computer works (any of the starts above: the user retrying from the App counts), or once it is older than 6 hours like any failure. It does not count against the main chat's readiness (`docs/assistant/onboarding.md`), which runs in the login shell.
`failing` / `repairing` / `blocked` all count as **unavailable** (when the latest result is within 6 hours).

- `agents_list`: gets an extra "unavailable launchers" section at the end, one line each: computer / launcher / state / category / error.
- The launcher list for creating an Agent (`GET /api/v1/machines/{mid}/launchers`) carries `health` on each item (`{state, error, kind, checked_at}`); the clients gray out the blocked ones ("In the App" below).
- `agent_start`, `task_plan`: when the launcher to use is unavailable on this computer, switch to another online computer where this launcher is healthy (a directory under the original computer's home becomes the new computer's home),
  and the "reason" in the answer / card says it switched and why; if no healthy one is found, start as originally planned and warn in the answer that the latest check failed. A task plan switches once when the card is produced and checks again when it actually starts.
  Healthy first: last check passed > never checked; never checked is not treated as broken.

## Automatic repair

- One repair round per computer at a time (several launchers on one computer usually share one cause: the gateway config is broken and every launcher returns 400); at most two repairs per round, then it is recorded as `blocked` and waits for a person.
  Blocked launchers are still retested at the normal interval and recover on their own when they pass.
- **Upgrade the CLI**: the node's `launchers.repair {name, action: "upgrade_cli"}` does only a fixed thing: for Claude launchers it runs `claude update` (login shell, 5-minute timeout);
  for anything else it answers "not supported". It does not accept arbitrary commands. Old nodes lack this method, which counts as "cannot upgrade".
- **Repair Agent**: when `agents.launcher_health.repair` in `manager.json` sets `skill`, it starts an Agent with a healthy launcher on `machine` (default: the computer the assistant runs on) in `cwd` (default home).
  The first message (`server/internal/prompts/agents/launcher_repair.md`) says which launchers on which computer are broken, the raw error and category, which skill to use for the repair, and to self-test with `claude -p` afterwards.
  It hangs off a **quiet to-do** (`Quiet`, no source chat): the hub sends no pushes for it, but it is visible on the Agents page and in to-dos. When it stops (neither working nor waiting for you) or after 45 minutes,
  the manager retests this round's launchers: pass means `ok`, fail means `blocked` (the reason includes the repair Agent's last message). Without `skill`, upgrading the CLI is the only repair; everything else goes straight to `blocked`.
  The skill name and the computer used for repair are configuration; the code names no specific tool or gateway.

The configuration, in the manager's `manager.json`:

```json
"agents": {
  "launcher_health": {
    "enabled": true,
    "interval_minutes": 30,
    "repair": { "skill": "<skill that repairs launchers>", "machine": "<machine id>", "cwd": "~", "launcher": "", "permission": "" }
  }
}
```

`enabled` missing or `false`: no periodic checks, no automatic repair (state can still be read; failed `agent_start` calls are still recorded, rechecked once, and routed around).
`permission` is the repair Agent's permission mode; missing means `bypassPermissions` (decided 2026-11-05: it has to run ssh to change config, and any other mode stops at an approval card waiting for a person);
set `default` if you want it to wait for your approval at every step. Member hubs in a mesh do not check (computers belong to the primary hub).

## API for the board

- `GET /api/v1/launchers/health?state=blocked&machine=`: all records (or filtered by state or computer), `{records: [{machine, launcher, state, kind, error, checked_at, source, fails, repair?, blocked_reason?}]}`.
- `POST /api/v1/launchers/health/check?machine=&launcher=`: check right now (without parameters, all online computers), returns this run's results.
- A state change broadcasts a `launcher_health` event (`{type, record}`, only to connected clients, not a push).
  The "decisions / blocked / progress" board reads `state=blocked` records, or subscribes to this event, to list what is blocked.

## In the App (#1643)

Decided with the user: gray out with the reason, never hide.

- **New Agent** (iOS / Mac / visionOS `LauncherPicker` in `NewChatView`, web `NewAgent.tsx`): a launcher whose `health.state` is `blocked` on the picked computer stays listed, dimmed, with one line saying why
  (`not_in_terminal`: "Not available in this computer's terminal"; the other classes a line each: key or sign-in rejected, gateway can't use the model, CLI too old, didn't answer, failed its last check).
  Choosing it does not pick it: it opens how to fix it (iOS / Mac a sheet, web a panel under the launchers): why, what to do (define it where the terminal's shell reads it, or add it as a model provider,
  with **Add Provider** going to the provider wizard; other classes: fix its key, gateway or CLI), the record's `error` under Details, and **Try Anyway**, which picks it after all:
  a start with it that works is what clears a `not_in_terminal` record, so the user must be able to start it once it is fixed. `failing` / `repairing` launchers are offered as before (the hub is still on them); an older hub sends no `health` and nothing changes.
- The first pick when nothing is configured to follow (an older hub without model configuration) skips blocked launchers: the one used last on that computer unless blocked, else the first that is not (`LauncherChoices.initial`, web `initialLauncher`).
  When the launcher that will run is blocked anyway (Model Settings' default, or Try Anyway) the form says so under the launcher row; Start stays enabled.
- **Run Agent here** (a shell pane is a terminal too): web's menu shows a blocked launcher disabled with the same line; iOS / Mac's sheet picks a usable launcher first and says the line under the picker.
- The assistant's launcher menus (headless `claude -p` in the login shell) are unchanged: `not_in_terminal` does not apply there, and the main chat's readiness covers its model.
- A failed start whose session has `start_error_code` (#1642) shows the hub's whole sentence under "Didn't Start" in the chat (iOS `StartingNote`, web chat page) instead of clipping it after three lines; without a code it is shown as before.

## Not done

- An Agent that starts but fails on its first turn (the gateway rejecting the model name; `session.start` succeeded) is not recorded from the session; periodic checks find it.
- Android does not read `health` yet.
- **New Agent** in the App (`agents.start_params`, on a computer the user picked) does not switch computers; Agents started by the Lark bot are not wired in yet.
