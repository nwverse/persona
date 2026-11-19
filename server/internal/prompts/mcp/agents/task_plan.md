Plan code, repository research or work needing several tools as one task per independent piece. Check machines_list. Fill the editable choices from the knowledge base, agents_list and user preferences; give a one-line reason. Read-only, user_requested and preauthorized task kinds start immediately; other plans wait for the owner's 开始. Dispatch returns queued, not started. Check outcomes and possible duplicate work listed in the receipt. Each task appears as a todo in your context snapshot; reach its Agent with agent_send. Reminders and events are tool actions, not Agent tasks.

Machines are fixed by default, including retries. An unavailable launcher fails there. Select machine_policy=flexible only for portable work: a failed health check can move it and the receipt explains why. Use todo_id to adopt this conversation's existing Agent-less todo (Lark topic, todo_create part_of or failed start). For retries of a failed start, including on a different launcher, use task_retry.

Leaders and side conversations supervise their dispatched tasks via 督办 and task_review. Verify the work before accepting. Main-chat tasks without a leader go to the background supervisor; the main chat may review too and follows up directly when assistant.main_supervises_own_tasks is on. Conversations unable to receive 督办 use background supervision and receive a linked completion notice. The receipt names the supervisor. From main, supervisor delegates the whole plan to an assistant.leaders conversation for its own supervision and review.

Every task keeps its original brief. The hub records the owner's triggering message, or attributes self-initiated work to you with started_why. Supply goal, done_when, expect_minutes and out_of_bounds. Writable tasks need checkable done_when and expect_minutes; read-only tasks need goal and expect_minutes. The owner confirms the displayed brief with 开始. For rework, replacement or another machine, use continues and continues_why. Apply owner-authorized changes through task_scope, giving goal, authorization bounds and new information. The manager appends effective criteria; pass scope_revision to reject stale plans.

For authorized file changes set readonly=false with explicit writable permission, including replacements on the same todo. A read-only Codex sandbox needs a fresh process for writable work; use agent_send with a live Agent when permissions stay the same. Read-only tasks set readonly=true and receive the constraint in their first message. Claude-family launchers always use bypassPermissions. Codex read-only uses plan unless assistant.readonly_mode=prompt (normal permissions, no sandbox guarantee). pi, cursor and agy cannot run read-only in either setting. Use role leader only to split a task: its Agent plans and reviews direct workers through restricted task_plan/task_review and receives their supervision messages. Workers cannot delegate.

When repository identity needs resolving, consult repositories_list, and repository_get only when useful. The user's explicit repository URL or path takes precedence. A saved binding is a reference, not authorization or an allowlist; never infer a machine or checkout path from it. Non-repository tasks need no lookup; retrieval is not a mandatory dispatch preflight. Closing the terminal on acceptance is the owner's Start choice; this tool cannot set it.

Workspace policy: "new" makes an isolated checkout; {"new":{"branch":"task/my-change"}} names its branch; {"existing":"<cwd or workspace_id>"} selects one; "none" creates nothing. Omitted: readonly uses none; writable Git follows assistant.task_workspace_preference (prefer-existing by default, prefer-new for isolation), and an occupied cwd uses new. Explicit existing never switches silently; refusal names its task owner. For an explicit new branch already checked out, reuse requires a clean checkout, no live Agent and no unfinished-task owner; otherwise refuse with its path and known owner. The machine's engine owns creation, location and registration; never run git worktree add. The card previews the path and is editable before Start. Default branch: task/<todo id>. Creation failure starts no Agent. Workspaces remain after acceptance. Organization read-only policy refusals include their reason: follow it, never retry unchanged.

The receipt comes first. With wait_seconds, Inline observation then reports input/session references and completed (reply), question (interaction_id), timeout, cancelled, unavailable, interrupted or failed. Older nodes may time out without input echo. Waiting never resends, cancels, answers or accepts work. Supervise and task_review as usual. Only immediately dispatchable single-task plans wait.

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "properties": {
    "wait_seconds": {
      "type": "integer",
      "minimum": 0,
      "description": "Observe after dispatch for up to 50 seconds. Omit or use 0 for receipt only."
    },
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
            "enum": [
              "fixed",
              "flexible"
            ],
            "description": "fixed (default): same machine through preview/dispatch/retries; unavailable fails here. flexible: explicit health-based move for portable work only."
          },
          "cwd": {
            "type": "string",
            "description": "Absolute or ~/ project directory."
          },
          "workspace": {
            "type": [
              "string",
              "object"
            ],
            "description": "Task workspace policy, as the description says; omit for the default. existing takes an absolute/~/ path or a workspace id on that machine. No custom worktree root.",
            "oneOf": [
              {
                "type": "string",
                "enum": [
                  "new",
                  "none"
                ]
              },
              {
                "type": "object",
                "required": [
                  "existing"
                ],
                "additionalProperties": false,
                "properties": {
                  "existing": {
                    "type": "string",
                    "minLength": 1
                  }
                }
              },
              {
                "type": "object",
                "required": [
                  "new"
                ],
                "additionalProperties": false,
                "properties": {
                  "new": {
                    "type": "object",
                    "additionalProperties": false,
                    "properties": {
                      "branch": {
                        "type": "string"
                      }
                    }
                  }
                }
              }
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
            "enum": [
              "leader"
            ],
            "description": "Any chat may set leader for direct workers on this task; omit for an ordinary worker."
          },
          "ultracode": {
            "type": "boolean",
            "description": "Large tasks: every substantial step uses slower, costlier multi-agent work (Claude Code >=2.1.284 + supporting node only; otherwise ignored). False for quick work. Use flag, never prompt/title word."
          },
          "readonly": {
            "type": "boolean",
            "description": "true only for a task that must remain read-only; false for authorized file changes (a Codex read-only sandbox: see the description). The first message adds「本任务只读：不改项目文件、不提交、不对外写入」 only when true"
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
            "description": "Original todo id; needs continues_why. Reuse todo, original brief and effective criteria even on failed Start; never split. Start stops/releases active prior Agents and reopens if closed. Refuse unauthorized takeover, unreachable Agents that cannot be stopped, or open legacy successors. No permission change: agent_send may follow up its live Agent."
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
            "description": "This conversation's user-supplied task pictures/files: absolute paths here. Copy to Agent's computer; list copies at message end and use for mentions."
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
            "description": "Open or failed-start todo this chat opened/supervises; no live Agent. Release stopped Agents at Start. For stopping/reopening use continues/continues_why. Both IDs must match."
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
