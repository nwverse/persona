Plan code, repository research or work beyond a few calls. Split independent tasks. Check machines_list, then use knowledge, agents_list and preferences for editable choices and brief reasons. Read-only, user_requested and preapproved work starts at once. Other work awaits 开始. A queued receipt confirms dispatch. Check the outcome and use agent_send. Reminders and events are separate.

machine_policy=fixed keeps the machine on retries and rejects unavailable launchers. For portable tasks, flexible can move after a failed health check and explains why. Side chats receive 督办 and close with task_review. Main may pick a supervisor from assistant.leaders. Otherwise background supervises unless assistant.main_supervises_own_tasks. If the caller cannot receive 督办, follow-up handles it and sends a final notice and link. Check the returned supervisor and similar work. Use todo_id for an existing unassigned to-do. A repository issue/PR URL in the title, goal or prompt blocks duplicates of open work. Reuse via continues or todo_id, or explain each conflict with distinct_from. Other hints are advisory.

Started tasks keep a brief (owner request or started_why). Give goal, done_when, out_of_bounds. Writable work needs done_when (PR/Issue defaults to PR merged + issue closed unless owner asks for PR-only/draft-only); read-only needs goal. Omit expect_minutes for automatic estimation; positive overrides. Manual Agents have no estimate_due. Continue via continues+continues_why; use task_retry for failed starts and task_scope+scope_revision for scope changes.

Writes require readonly=false and explicit write authorization. Replace a read-only Codex to make fixes. Use agent_send when permissions stay the same. readonly=true constrains the assignment. Claude uses bypassPermissions. Codex uses plan unless assistant.readonly_mode=prompt. pi, cursor and agy reject readonly. role=leader can start and review direct role=worker tasks via restricted task_plan/task_review. Workers cannot delegate.

Use the user's explicit repository URL or path first. For unresolved identity use repositories_list, then repository_get if needed. A saved binding is a reference. Check authorization, machine and checkout independently. Terminal cleanup on acceptance is the owner's Start choice.

For a to-do with a saved workspace plan, select {"backend":{"operation_id":"<saved operation ID>"}}. Every selected repository must be ready. Start uses the saved root and its owning computer. Optional backend.host selects a supported engine.

Workspace policy: "new" makes an isolated checkout; {"new":{"branch":"task/my-change"}} names its branch; {"existing":"<cwd or workspace_id>"} selects one; "none" creates nothing. Omitted: readonly uses none; writable Git follows assistant.task_workspace_preference (prefer-existing by default, prefer-new for isolation), and an occupied cwd uses new. Explicit existing never switches silently; refusal names its task owner. For an explicit new branch already checked out, reuse requires a clean checkout, no live Agent and no unfinished-task owner; otherwise refuse with its path and known owner. The machine's engine owns creation, location and registration; never run git worktree add. The card previews the path and is editable before Start. Default branch: task/<todo id>. Creation failure starts no Agent. Workspaces remain after acceptance. Organization read-only policy refusals include their reason: follow it, never retry unchanged.

`wait_seconds` observes up to 50 seconds after dispatch, appending `Inline observation` JSON: `completed`(this input's `reply`), `question`(pending `interaction_id`), `timeout`/`cancelled`/`unavailable`/`interrupted`/`failed`, with known input/session ids. Observation never answers questions, accepts tasks, resends or cancels work; normal supervision/`task_review` applies. Older nodes may time out rather than return an unrelated reply. Only an immediately dispatchable single-task plan waits; approval cards and multi-task plans return immediately.{{template "shared/mcp_language" .}}

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
      "description": "0/omitted: normal receipt"
    },
    "supervisor": {
      "type": "string",
      "description": "main only: leader conversation id (assistant.leaders); it supervises; omit for no direction or your own work"
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
          "role",
          "machine",
          "readonly",
          "user_requested",
          "why"
        ],
        "anyOf": [
          {"required": ["cwd"]},
          {"type": "object", "required": ["workspace", "todo_id"], "properties": {"workspace": {"type": "object", "required": ["backend"]}}}
        ],
        "properties": {
          "title": {
            "type": "string",
            "description": "{{if eq .Lang "en"}}Card title in English, under 20 characters.{{else if eq .Lang "ja"}}カードに表示する日本語のタイトル（20文字未満）。{{else}}Card title in Chinese, under 20 characters.{{end}}"
          },
          "context": {"type": "string", "description": "facts the manager cannot resolve; omit project background"},
          "prompt": {
            "type": "string",
            "description": "goal, authorization bounds and new facts; the manager adds project context, criteria and framework rules once"
          },
          "machine": {
            "type": "string",
            "description": "Machine id; fixed by default."
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default): keep this machine; flexible: permit health-based moves only for portable work"
          },
          "cwd": {
            "type": "string",
            "description": "Absolute or ~/ project directory, unless workspace.backend supplies it."
          },
          "workspace": {
            "type": ["string", "object"],
            "description": "workspace policy; omit for default; existing: absolute/~/path or workspace id on that machine; no custom root",
            "oneOf": [
              { "type": "string", "enum": ["new", "none"] },
              { "type": "object", "required": ["backend"], "additionalProperties": false, "properties": { "backend": { "type": "object", "required": ["operation_id"], "additionalProperties": false, "properties": { "operation_id": { "type": "string", "minLength": 1 }, "host": { "type": "string" } } } } },
              { "type": "object", "required": ["existing"], "additionalProperties": false, "properties": { "existing": { "type": "string", "minLength": 1 } } },
              { "type": "object", "required": ["new"], "additionalProperties": false, "properties": { "new": { "type": "object", "additionalProperties": false, "properties": { "branch": { "type": "string" } } } } }
            ]
          },
          "launcher": {
            "type": "string",
            "description": "listed launcher (Claude Code, pi, cursor or agy in herdr); unknown names refused with choices; empty: default/fallback, claude first, why says which"
          },
          "model": {
            "type": "string",
            "description": "model id or alias the agent takes; empty: the launcher's default"
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
            "description": "mode; empty: writable default; readonly/replacement/Codex rules: see description; traex and omp take none"
          },
          "role": {
            "type": "string",
            "enum": ["worker", "leader"],
            "description": "worker: ordinary; leader (any conversation): starts/reviews direct workers"
          },
          "ultracode": {
            "type": "boolean",
            "description": "large tasks (many files, multi-angle checks): Claude Code multi-agent workflows for substantial steps, slower/costlier; small tasks: false; Claude Code 2.1.284+ only, otherwise starts without it; never put it in prompt/title"
          },
          "readonly": {
            "type": "boolean",
            "description": "true only for a task that must remain read-only; false for explicitly authorized file changes (a Codex read-only sandbox: see the description). The first message adds「本任务只读：不改项目文件、不提交、不对外写入」 only when true"
          },
          "user_requested": {
            "type": "boolean",
            "description": "True only for this task/start requested in the user's latest message; false for inferred work/your own idea."
          },
          "why": {
            "type": "string",
            "description": "{{if eq .Lang "en"}}One-line reason in English for the machine, directory and model.{{else if eq .Lang "ja"}}コンピュータ、ディレクトリ、モデルを選ぶ理由を日本語で一行に記述します。{{else}}Chinese one-line reason for machine, directory and model.{{end}}"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "{{if eq .Lang "en"}}Checkable acceptance results in English, one per entry, shared with the Agent. Nonempty for writable tasks; ignored with continues.{{else if eq .Lang "ja"}}Agent に伝える確認可能な完了条件を日本語で一項目ずつ記述します。書き込み可能なタスクは一つ以上必須です。continues 指定時は無視されます。{{else}}Chinese checkable acceptance results told to Agent, one per entry. Nonempty for writable tasks; ignored with continues.{{end}}"
          },
          "goal": {
            "type": "string",
            "description": "{{if eq .Lang "en"}}Owner's one-line outcome in English. Required for read-only tasks; writable tasks default to the title.{{else if eq .Lang "ja"}}ユーザーが得る結果を日本語で一行に記述します。読み取り専用タスクでは必須です。書き込み可能なタスクで省略した場合はタイトルを使います。{{else}}Owner's one-line Chinese outcome. Required read-only; writable defaults to title.{{end}}"
          },
          "out_of_bounds": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "{{if eq .Lang "en"}}Optional forbidden actions or areas in English, one per entry.{{else if eq .Lang "ja"}}任意の禁止事項や対象外の範囲を日本語で一項目ずつ記述します。{{else}}Optional forbidden actions/areas, in Chinese, one per entry.{{end}}"
          },
          "continues": {
            "type": "string",
            "description": "Original todo id; requires continues_why. Reuse todo, original brief and effective criteria even on failed Start; never split. Start stops/releases active prior Agents and reopens if closed. Refuse unauthorized takeover, unreachable Agents that cannot be stopped, or open legacy successors."
          },
          "scope_revision": {
            "type": "string",
            "description": "With continues after a scope change: exact task_scope scope_revision; refuse stale plans."
          },
          "continues_why": {
            "type": "string",
            "description": "With continues: one-line stop/replacement reason saved on this plan."
          },
          "distinct_from": {
            "type": "array",
            "items": {
              "type": "object",
              "required": [
                "todo_id",
                "reason"
              ],
              "additionalProperties": false,
              "properties": {
                "todo_id": {
                  "type": "string"
                },
                "reason": {
                  "type": "string",
                  "minLength": 1
                }
              }
            },
            "description": "new tasks only: give each conflicting open todo ID and why the work is separate. Reasons stay in the brief. Reuse the todo for the same work"
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
            "description": "absolute paths of pictures/files the user gave in this conversation (Claude Code path or app attachment); copied to the agent, appended to its message with mentions updated; others refused"
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "Optional minutes override. Omit/0: comparable history, or the 60-minute system_default; positive: override. Review at 1×; stuck at 1.5× (min 20m); overdue at 3× (min 24h)."
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
            "description": "Open or failed-start todo this chat opened/supervises; no live Agent. Release stopped Agents at Start. Equals continues if both are given."
          },
          "inbox_item": {
            "type": "string",
            "description": "Inbox item id; omit for the conversation's sole eligible open handoff. Several require an id; none leaves work unlinked. Never files or replies."
          },
          "tier": {
            "type": "string",
            "description": "Agent preset from the snapshot's 模型档位 line; choose by purpose; resolved at task start with its profile; explicit launcher/model/effort/speed overrides"
          },
          "speed": {
            "type": "string",
            "enum": ["standard", "fast", "ultrafast"],
            "description": "new-start speed; standard/omitted: launcher default. Codex: fast, ultrafast; Claude Code: fast only"
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
