Plan Agent work (code, repo research, or more than a few tool calls) instead of starting agents yourself: one task per independent piece. Check machines_list before choosing resources. Fill every field from the knowledge base, agents_list and user preferences; each choice is an editable chip with your one-line reason. Read-only tasks, tasks the user asked for themselves (user_requested), and kinds the user told you to start without asking, start at once; the rest wait as a card for the user's 开始. Returns immediately: carry on. Each queued task becomes a todo in your context snapshot; use agent_send to reach its Agent. Things a tool does for you (a reminder, an event) are not tasks. A task stays on its requested machine by default (machine_policy=fixed), including retries. An unavailable launcher fails there; do not reroute machine-local work. Use machine_policy=flexible only when the task may run on another computer: a failed health check can then move it, and its why says so. The response confirms queued dispatch, not an Agent start; check the task outcome. From a leader conversation (assistant.leaders) or any other side conversation you supervise what you hand out; you get a 督办 message when one needs you, and you close it with task_review, not by repeating its agent's words. What the main conversation hands out to no leader is followed up by the background supervisor by default (a built-in conversation that gets the 督办 messages and reviews with task_review; the main conversation may too), and by the main conversation itself, as a leader would, when assistant.main_supervises_own_tasks is on. A conversation that cannot be sent a 督办 message leaves what it hands out to that same follow-up, and gets a one-line notice with the task's link when each one ends. The answer says who follows up. The answer lists running work that looks like the same task: check it before letting both go on. A todo this conversation opened with no agent yet (a Lark topic's, named in the message that handed it over, or a piece from todo_create part_of) is the task's own: give it as todo_id and no new todo is made; any other is refused with why. From the main conversation, supervisor hands the whole plan to a leader conversation (one of assistant.leaders), which supervises and reviews its tasks as if it had handed them out; leave it out for work of no direction. Every task gets a brief, kept from the moment it starts and never rewritten: the hub records the owner's message that led to it (their words, from this conversation; work you start on your own after a 督办 message, a notice or a routine is recorded as yours, with started_why), and you supply the goal, done_when, expect_minutes and out_of_bounds. A writable task (readonly false) without done_when (at least one checkable result) and expect_minutes is refused, and so is a read-only task without goal and expect_minutes; the refusal says what is missing. The card shows the brief, so the owner's 开始 confirms it. For retry, rework, or replacement, give continues and continues_why; keep the original todo and brief even if dispatch fails. Start must stop and release prior Agents and reopen the same todo. Unreachable prior Agents, unauthorized takeovers, and open legacy successors block replacement. Apply owner-authorized scope changes with task_scope before planning and pass its scope_revision to reject staleness. Supply goal, authorization bounds, and new information; the manager appends effective criteria. A task that changes files sets readonly false with explicitly authorized writable permission (for example, Codex bypassPermissions), which also lets a replacement Agent on the same todo implement fixes. A read-only Codex sandbox cannot be changed in place: use a fresh writable process; with no permission change, a plain agent_send follow-up to the live Agent is fine. A task that only reads sets readonly true; its first message includes the read-only constraint. Claude-family launchers use bypassPermissions for both read-only and writable tasks; Codex uses plan unless assistant.readonly_mode=prompt (normal runtime permissions, not a security boundary). pi, cursor and agy have no read-only mode and are refused in either setting. role "leader" makes the task's agent a task leader: it hands direct workers to its own task through its own restricted task_plan and task_review and gets their supervision messages; only for a task that splits into several agents' work. Its workers cannot hand tasks on.

When repository identity needs resolving, consult repositories_list, and repository_get only when useful. The user's explicit repository URL or path takes precedence. A saved binding is a reference, not authorization or an allowlist; never infer a machine or checkout path from it. Non-repository tasks need no lookup; retrieval is not a mandatory dispatch preflight.

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
      "description": "Main chat only: direction's supervisor (assistant.leaders id). Omit for no direction/your own."
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
            "description": "Card title in Chinese, under 20 characters."
          },
          "prompt": {
            "type": "string",
            "description": "Agent's first message: all it needs to work without asking you."
          },
          "machine": {
            "type": "string",
            "description": "Machine id; fixed by default."
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed default: this machine through preview/dispatch/retries, fail here if unavailable. flexible: explicit health-based placement elsewhere for portable work only."
          },
          "cwd": {
            "type": "string",
            "description": "Absolute or ~/ project directory."
          },
          "workspace": {
            "description": "Omit for preference/readonly/busy defaults. Engine new: task/<todo id> or {new:{branch:...}}. existing: absolute/~/ path or machine workspace id. none: no creation. No custom worktree root.",
            "oneOf": [
              { "type": "string", "enum": ["new", "none"] },
              { "type": "object", "required": ["existing"], "additionalProperties": false, "properties": { "existing": { "type": "string", "minLength": 1 } } },
              { "type": "object", "required": ["new"], "additionalProperties": false, "properties": { "new": { "type": "object", "additionalProperties": false, "properties": { "branch": { "type": "string" } } } } }
            ]
          },
          "launcher": {
            "type": "string",
            "description": "Installed launcher: Claude Code, or pi/cursor/agy in herdr. Unknown: refuse/list choices. Empty: default; if absent, installed fallback (claude first), explained in why."
          },
          "model": {
            "type": "string",
            "description": "Model alias/gateway id or pi provider/model (ag-chat/cursor/grok-4.7). Allowed: letters/digits . _ - / : @ [ ]; leading ~ allowed (~vendor/model-latest). Empty: launcher default."
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
            "description": "Empty: writable default. Replacement fixes: readonly=false plus explicit writable mode (e.g. bypassPermissions). Read-only: see tool policy. traex/omp take none."
          },
          "role": {
            "type": "string",
            "enum": ["leader"],
            "description": "Any chat may set leader for direct workers on this task; omit for an ordinary worker."
          },
          "ultracode": {
            "type": "boolean",
            "description": "Large multi-file/multi-check tasks: every substantial step uses a slower, costlier multi-agent workflow. Claude Code >=2.1.284 with a supporting node only; otherwise ignored. False for quick work. Use flag, never prompt/title keyword."
          },
          "readonly": {
            "type": "boolean",
            "description": "True: read-only, first message adds「本任务只读：不改项目文件、不提交、不对外写入」. False: authorized file edits. Codex plan/read-only replacement needs a fresh writable process and explicit writable permission; never mutate the running sandbox."
          },
          "user_requested": {
            "type": "boolean",
            "description": "True only for this task/start requested in the user's latest message; false for inferred work/your own idea."
          },
          "why": {
            "type": "string",
            "description": "Chinese one-line reason for machine, directory and model."
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "Chinese acceptance results told to Agent, one per entry. Nonempty for writable tasks; ignored with continues, which uses original todo's effective criteria."
          },
          "goal": {
            "type": "string",
            "description": "Owner's one-line Chinese outcome. Required read-only; writable defaults to title."
          },
          "out_of_bounds": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "Optional forbidden actions/areas, in Chinese, one per entry."
          },
          "continues": {
            "type": "string",
            "description": "Original todo id; requires continues_why. Reuse todo/brief/effective criteria even on failed Start. Follow the continuation stop/release/reopen and takeover checks above. Without a permission change, agent_send may follow up its live Agent."
          },
          "scope_revision": {
            "type": "string",
            "description": "With continues after a scope change: exact task_scope scope_revision; refuse stale plans."
          },
          "continues_why": {
            "type": "string",
            "description": "With continues: one-line stop/replacement reason saved on this plan."
          },
          "started_why": {
            "type": "string",
            "description": "If not just owner-requested (own idea, 督办 or routine): one-line start reason saved in brief."
          },
          "attachments": {
            "type": "array",
            "maxItems": 8,
            "items": {
              "type": "string"
            },
            "description": "User-supplied pictures/files for this task in this conversation only: absolute paths here. Copied to Agent's computer; copies listed at message end and used for mentions."
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "Estimated minutes; positive unless continues. Stuck after 1.5× without progress (minimum 20 minutes); overdue reported after 3× without end (minimum one day)."
          },
          "project": {
            "type": "string",
            "description": "Existing kb_list projects slug/name for a clear long-term area; no near-duplicates."
          },
          "part_of": {
            "type": "string",
            "description": "Optional request todo this is a piece of."
          },
          "after": {
            "type": "array",
            "items": {
              "type": "string"
            },
            "description": "With part_of: prerequisite pieces."
          },
          "todo_id": {
            "type": "string",
            "description": "Open todo this chat opened/supervises; no live Agent. Release stopped Agents at Start. For stopping/reopening use continues/continues_why. Both IDs must match."
          },
          "inbox_item": {
            "type": "string",
            "description": "Exact handed-off Inbox id for related work only. Checked against this conversation and manager; links the task without filing or replying."
          },
          "tier": {
            "type": "string",
            "description": "Preset from active profile's 模型档位; choose by description. Resolve with profile active at Start; explicit launcher/model/effort override."
          },
          "effort": {
            "type": "string",
            "description": "Explicit effort; omit for tier/purpose default."
          }
        }
      }
    }
  }
}
```
