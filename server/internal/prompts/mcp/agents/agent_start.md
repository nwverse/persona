Start a new agent in herdr or Orca on one of the user's computers, in a project directory, with a first prompt. Use for real development work instead of doing it here; check machines_list first. Returns the new session id. On a failed launcher health check, starts on a healthy machine (moving ~/ paths to its home) and says so. Claude-family and Paseo Auto Accept use bypassPermissions; TRAE CLI and omp take no effort.

The launcher decides the agent (Claude Code, or pi/cursor/agy in a herdr pane with the given model). pi's first message uses its Persona extension; cursor/agy use herdr once recognised. Those three take no permission mode or ultracode, and use their own effort.

For a dedicated branch/checkout, pass `worktree.branch`: Persona creates the worktree via the engine (visible in Orca/app) under cwd's workspace and starts the agent there; never run `git worktree add`. Existing local branches check out; new ones branch from workspace HEAD; branches checked out elsewhere are refused.

For secret-provider credentials (e.g. 1Password), pass `secret_env` mapping env names to references (`op://vault/item/field`), never values. The user approves on a card first; values go only to that Claude Code agent's env on a connected machine (never shown to you), and the answer reports granted/declined/expired/failed. Tell the agent the variable names; never ask it to print them.

Pass `ultracode: true` for large multi-file/multi-angle tasks (Claude Code 2.1.284+ multi-agent workflows; slower/costlier; off for small work; never put "ultracode" in prompt; `agents_list` shows `ultracode=on`). Set `skip_project_memory: true` (default false) to omit automatic project/direction memory (summary, outcomes, decisions, pitfalls, lessons) for this start only, keeping the prompt unchanged. Organization read-only policies refuse calls with a reason; do not retry.{{template "shared/mcp_language" .}}

`wait_seconds` observes up to 50 seconds after dispatch, appending `Inline observation` JSON: `completed`(this input's `reply`), `question`(pending `interaction_id`), `timeout`/`cancelled`/`unavailable`/`interrupted`/`failed`, with known input/session ids. Observation never answers questions, accepts tasks, resends or cancels work; normal supervision/`task_review` applies. Older nodes may time out rather than return an unrelated reply.

```json
{
  "type": "object",
  "required": [
    "machine",
    "cwd",
    "prompt"
  ],
  "properties": {
    "wait_seconds": {
      "type": "integer",
      "minimum": 0,
      "description": "0/omitted: normal receipt"
    },
    "machine": {
      "type": "string"
    },
    "cwd": {
      "type": "string",
      "description": "absolute path or ~/…; with worktree, a directory of the repository to branch from"
    },
    "prompt": {
      "type": "string"
    },
    "skip_project_memory": {
      "type": "boolean",
      "description": "default false; true skips project/direction memory; prompt unchanged"
    },
    "attachments": {
      "type": "array",
      "maxItems": 8,
      "items": { "type": "string" },
      "description": "absolute paths of pictures/files the user gave in this conversation (Claude Code path or app attachment); copied to the agent, appended to its message with mentions updated; others refused"
    },
    "launcher": {
      "type": "string",
      "description": "listed launcher (Claude Code, pi, cursor or agy); unknown names refused with choices; empty: default/fallback, claude first, answer says which"
    },
    "secret_env": {
      "type": "object",
      "maxProperties": 8,
      "additionalProperties": { "type": "string" },
      "description": "NAME → secret-provider reference (op://vault/item/field), never a value; card approval first"
    },
    "ultracode": {
      "type": "boolean",
      "description": "multi-agent workflows; default false"
    },
    "worktree": {
      "type": "object",
      "description": "engine-made worktree of cwd's repository",
      "required": [
        "branch"
      ],
      "properties": {
        "branch": {
          "type": "string",
          "description": "the branch to check out (made from the workspace's HEAD when it does not exist)"
        }
      }
    },
    "tier": {
      "type": "string",
      "description": "Agent preset from the snapshot's 模型档位 line; choose by purpose; resolved before approval and shown on the card; explicit launcher/model/effort/speed overrides"
    },
    "speed": {
      "type": "string",
      "enum": ["standard", "fast", "ultrafast"],
      "description": "new-start speed; explicit choice overrides the tier. standard/omitted: launcher default. Codex: fast, ultrafast; Claude Code: fast only"
    },
    "effort": {
      "type": "string",
      "description": "Explicit effort; omit for tier/purpose default."
    },
    "model": {
      "type": "string",
      "description": "Explicit model id or alias the agent takes; omit for the tier or purpose default."
    }
  }
}
```
