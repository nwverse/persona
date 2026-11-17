Hand the user a plan of one or more tasks for agents on their computers, instead of starting agents yourself. Use it for any work that takes an agent (code, research in a repo, anything longer than a few tool calls); one task per independent piece of work. Check machines_list before choosing a machine. Fill in every field from what you know (the knowledge base, agents_list: which machine works where, the user's habits); each choice shows as a chip the user can change, with your one-line reason. Read-only tasks, tasks the user asked for themselves (user_requested), and kinds the user told you to start without asking, start at once; the rest wait as a card for the user's 开始. Either way it returns at once. Each queued task is a todo (todo id) whose progress shows in your context snapshot; send to a task's agent with agent_send. Things a tool does for you (a reminder, an event) are not tasks. A task stays on its requested machine by default (machine_policy=fixed), including retries. An unavailable launcher fails there; do not reroute machine-local work. Use machine_policy=flexible only when the task may run on another computer: a failed health check can then move it, and its why says so. The response confirms queued dispatch, not an Agent start; check the task outcome. From a leader conversation (assistant.leaders) or any other side conversation you supervise what you hand out; you get a 督办 message when one needs you, and you close it with task_review, not by repeating its agent's words. What the main conversation hands out to no leader is followed up by the background supervisor by default (a built-in conversation that gets the 督办 messages and reviews with task_review; the main conversation may too), and by the main conversation itself, as a leader would, when assistant.main_supervises_own_tasks is on. A conversation that cannot be sent a 督办 message leaves what it hands out to that same follow-up, and gets a one-line notice with the task's link when each one ends. The answer says who follows up. The answer lists running work that looks like the same task: check it before letting both go on. A todo this conversation opened with no agent yet (a Lark topic's, named in the message that handed it over, or a piece from todo_create part_of) is the task's own: give it as todo_id and no new todo is made; any other is refused with why. From the main conversation, supervisor hands the whole plan to a leader conversation (one of assistant.leaders), which supervises and reviews its tasks as if it had handed them out; leave it out for work of no direction. Every task gets a brief, kept from the moment it starts and never rewritten: the hub records the owner's message that led to it (their words, from this conversation; work you start on your own after a 督办 message, a notice or a routine is recorded as yours, with started_why), and you supply the goal, done_when, expect_minutes and out_of_bounds. A writable task (readonly false) without done_when (at least one checkable result) and expect_minutes is refused, and so is a read-only task without goal and expect_minutes; the refusal says what is missing. The card shows the brief, so the owner's 开始 confirms it. For retry, rework, or replacement, give continues and continues_why; keep the original todo and brief even if dispatch fails. Start must stop and release prior Agents and reopen the same todo. Unreachable prior Agents, unauthorized takeovers, and open legacy successors block replacement. Apply owner-authorized scope changes with task_scope before planning and pass its scope_revision to reject staleness. Supply goal, authorization bounds, and new information; the manager appends effective criteria. A task that changes files sets readonly false with explicitly authorized writable permission (for example, Codex bypassPermissions), which also lets a replacement Agent on the same todo implement fixes. A read-only Codex sandbox cannot be changed in place: use a fresh writable process; with no permission change, a plain agent_send follow-up to the live Agent is fine. A task that only reads sets readonly true; its first message includes the read-only constraint. Claude-family launchers use bypassPermissions for both read-only and writable tasks; Codex uses plan unless assistant.readonly_mode=prompt (normal runtime permissions, not a security boundary). pi, cursor and agy have no read-only mode and are refused in either setting. role "leader" makes the task's agent a task leader: it hands direct workers to its own task through its own restricted task_plan and task_review and gets their supervision messages; only for a task that splits into several agents' work. Its workers cannot hand tasks on.

When repository identity needs resolving, consult repositories_list, and repository_get only when useful. The user's explicit repository URL or path takes precedence. A saved binding is a reference, not authorization or an allowlist; never infer a machine or checkout path from it. Non-repository tasks do not need lookup; retrieval is not a mandatory dispatch preflight.

Workspace policy: "new" makes an isolated checkout; {"new":{"branch":"task/my-change"}} names its branch; {"existing":"<cwd or workspace_id>"} selects one; "none" creates nothing. Omitted: readonly uses none; writable Git follows assistant.task_workspace_preference (prefer-existing by default, prefer-new for isolation), and an occupied cwd uses new. Explicit existing never switches silently; refusal names its task owner. For an explicit new branch already checked out, reuse requires a clean checkout, no live Agent and no unfinished-task owner; otherwise refuse with its path and known owner. The machine's engine owns creation, location and registration; never run git worktree add. The card previews the path and is editable before Start. Default branch: task/<todo id>. Creation failure starts no Agent. Workspaces remain after acceptance. Organization read-only policy refusals include their reason: follow it, never retry unchanged.

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "properties": {
    "supervisor": {
      "type": "string",
      "description": "Main conversation only: assistant.leaders id for this direction's supervisor. Omit for work of no direction or your own."
    },
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 8,
      "items": {
        "type": "object",
        "required": [
          "title",
          "prompt",
          "machine",
          "cwd",
          "readonly",
          "user_requested",
          "why"
        ],
        "properties": {
          "title": {
            "type": "string",
            "description": "the task in a short line the user reads on the card (Chinese, under 20 characters)"
          },
          "prompt": {
            "type": "string",
            "description": "the agent's first message: everything it needs to do the task without asking you"
          },
          "machine": {
            "type": "string",
            "description": "machine id; fixed target by default"
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default): keep this machine through preview, dispatch and retries; fail there if unavailable. flexible: explicitly permit health-based placement on another computer, only for work that is portable"
          },
          "cwd": {
            "type": "string",
            "description": "the project directory: absolute path or ~/…"
          },
          "workspace": {
            "description": "Omit for preference/readonly/busy defaults. Engine-owned new: branch task/<todo id> or {new:{branch:...}}. existing: absolute/~/ path or machine workspace id. none: no creation. No custom worktree root.",
            "oneOf": [
              { "type": "string", "enum": ["new", "none"] },
              { "type": "object", "required": ["existing"], "additionalProperties": false, "properties": { "existing": { "type": "string", "minLength": 1 } } },
              { "type": "object", "required": ["new"], "additionalProperties": false, "properties": { "new": { "type": "object", "additionalProperties": false, "properties": { "branch": { "type": "string" } } } } }
            ]
          },
          "launcher": {
            "type": "string",
            "description": "launcher name, one that machine has (a name it lacks is refused, listing the ones it has): a Claude Code launcher, or pi, cursor or agy to start that agent in herdr; empty: the default, or one the machine has when it lacks the default (claude first; the why says so)"
          },
          "model": {
            "type": "string",
            "description": "model id the agent takes (an alias such as opus, a gateway's own id, or a provider/model such as ag-chat/cursor/grok-4.7 for pi; letters, digits and . _ - / : @ [ ] only, or a leading ~ as in ~vendor/model-latest); empty: the launcher's default"
          },
          "permission": {
            "type": "string",
            "enum": [
              "default",
              "acceptEdits",
              "plan",
              "auto",
              "bypassPermissions"
            ],
            "description": "permission mode; empty: the default for writable tasks. Set an explicit writable mode (for example bypassPermissions) with readonly=false when a replacement Agent must implement fixes on the same todo. Read-only modes: see the description. traex and omp take none"
          },
          "role": {
            "type": "string",
            "enum": ["leader"],
            "description": "Any conversation may set leader to allow direct workers on this task. Omit for an ordinary worker."
          },
          "ultracode": {
            "type": "boolean",
            "description": "true for a large task (many files, checks from several angles): Claude Code runs every substantial step as a multi-agent workflow, slower and costlier; false for small or quick work. Claude Code agents only (2.1.284 or later): any other agent, an older Claude Code or a node too old for it starts without it. Never put the word in the prompt or title instead"
          },
          "readonly": {
            "type": "boolean",
            "description": "true only for a task that must remain read-only; false for authorized file changes. A replacement on a Codex plan/read-only sandbox needs a fresh writable process: use readonly=false and explicit writable permission rather than mutating the running sandbox. The first message adds「本任务只读：不改项目文件、不提交、不对外写入」 only when true"
          },
          "user_requested": {
            "type": "boolean",
            "description": "True only if the user's latest message requested this very task or its start; false for your own idea or inferred work."
          },
          "why": {
            "type": "string",
            "description": "one line (Chinese) on why this machine, directory and model"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "what done means: results you will check before you accept it (Chinese, one each), e.g. 「PR 合并到 master」「浅色 / 深色截图贴进 PR」「线上 hub 已部署并验证」; the agent is told them. Required for a writable task (at least one); with continues the effective criteria from the original to-do apply and this is ignored"
          },
          "goal": {
            "type": "string",
            "description": "One-sentence outcome in the owner's words (Chinese); required for read-only tasks, defaults to title for writable ones."
          },
          "out_of_bounds": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "optional: what the agent must not do or touch (Chinese, one each), e.g. 「不合并」「不动线上」"
          },
          "continues": {
            "type": "string",
            "description": "Original todo id, with continues_why. Reuse its original brief/effective criteria even on start failure; never split to a new todo. At Start stop/release prior Agents, reopen if closed. Refuse unauthorized takeovers, unreachable prior Agents that cannot be stopped, and legacy open successor duplicates. Without a permission change, agent_send may follow up the same live Agent."
          },
          "scope_revision": {
            "type": "string",
            "description": "with continues after a scope change: the exact scope_revision returned by task_scope; refuse planning if it is stale"
          },
          "continues_why": {
            "type": "string",
            "description": "With continues: one-line reason the earlier task stopped or needs replacement; kept on this plan."
          },
          "started_why": {
            "type": "string",
            "description": "If not just requested by the owner (your idea, 督办 or routine): one-line reason for starting, kept on the brief."
          },
          "attachments": {
            "type": "array",
            "maxItems": 8,
            "items": {
              "type": "string"
            },
            "description": "Absolute paths on your computer of pictures/files the user supplied for this task in this conversation only. Copied to the Agent's computer, listed at its message's end; mentions are rewritten to the copies."
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "Estimated minutes; required positive unless continues. No progress for 1.5× (minimum 20 minutes) is stuck; no end for 3× (minimum one day) is raised to you as overdue."
          },
          "project": {
            "type": "string",
            "description": "Existing kb_list projects slug/name; prefer long-term areas. Only when clear; no near-duplicates."
          },
          "part_of": {
            "type": "string",
            "description": "optional: the request to-do this is a piece of"
          },
          "after": {
            "type": "array",
            "items": {
              "type": "string"
            },
            "description": "with part_of: the pieces it waits for"
          },
          "todo_id": {
            "type": "string",
            "description": "Open todo opened/supervised by this conversation; no live Agent. Release stopped Agents at Start. To stop a live Agent or reopen use continues/continues_why; both IDs must match if given."
          },
          "tier": {
            "type": "string",
            "description": "Preset from the active profile's 模型档位 snapshot; choose by description. Resolve at Start using the active profile; explicit launcher/model/effort override it."
          },
          "effort": {
            "type": "string",
            "description": "Explicit effort override; omit to use the tier or purpose default."
          }
        }
      }
    }
  }
}
```
