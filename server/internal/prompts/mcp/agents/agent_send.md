Send what the user asked for to an Agent. A busy Agent may queue it. A stopped Agent needs resume and resume_why if this work needs its context. Only a recorded idle, task completion or restart stop permits that. For user and unknown stops use the user's Resume. It must be supervised (give task unless on an open task of yours). A nearly full Agent is compacted first. For unrelated work use agent_start. Unsure: ask_user. A branch that never ran starts on its first message. Only /exit or /quit stops a running Agent and keeps the tab. The owner's controls close another conversation's Agent.

For dialog_open, retry with dismiss_notice only when Enter alone closes the notice. For permissions and pickers, tell the user or use interaction_answer.

From the main conversation, give task to supervise new work, including done_when in text. More on your existing task keeps its title and updates its criteria. A different title, the user's own to-do or another conversation's task is refused. A failed send creates no task. Omit task for a reply, nudge or decision.

Use handoff to ask an existing Agent to check a candidate for a different, stopped to-do you supervise. The node's acknowledgement starts the wait. Optional wait_seconds adds this input's completion, question or observation status to the send receipt. It never resends, accepts a task or answers a question. Follow task_review as usual.

```json
{
  "type": "object",
  "required": ["machine", "session_id", "text"],
  "properties": {
    "wait_seconds": {
      "type": "integer",
      "minimum": 0,
      "description": "Observe this input for up to 50 seconds after dispatch; omit or 0 for only the send receipt."
    },
    "machine": {
      "type": "string"
    },
    "session_id": {
      "type": "string"
    },
    "text": {
      "type": "string"
    },
    "resume": {
      "type": "boolean",
      "description": "resume this stopped agent with its context, text as its first message"
    },
    "resume_why": {
      "type": "string",
      "description": "why its context is needed (shown to the user)"
    },
    "dismiss_notice": {
      "type": "boolean",
      "description": "Press Enter first to close a notice that only says \"Enter to continue\" (no options). Any other dialog still refuses the send."
    },
    "attachments": {
      "type": "array",
      "maxItems": 8,
      "items": { "type": "string" },
      "description": "Absolute paths of files the user attached in this conversation. Copies are sent to the Agent’s computer."
    },
    "handoff": {
      "type": "object",
      "required": ["todo_id", "receiving_todo_id", "head", "check", "until"],
      "additionalProperties": false,
      "properties": {
        "todo_id": { "type": "string", "description": "Originating to-do" },
        "receiving_todo_id": { "type": "string", "description": "The target Agent’s to-do" },
        "head": { "type": "string", "description": "Full commit ID to verify" },
        "check": { "type": "string", "description": "What to verify" },
        "until": { "type": "string", "description": "Duration (e.g. 2h) or RFC 3339 deadline" }
      }
    },
    "task": {
      "type": ["object", "null"],
      "description": "new work: make it your supervised task first (main conversation only); null for a reply",
      "additionalProperties": false,
      "properties": {
        "title": {
          "type": "string",
          "description": "the task in a short line (Chinese, under 20 characters)"
        },
        "done_when": {
          "type": "array",
          "maxItems": 6,
          "items": { "type": "string" },
          "description": "results you can check when it says it is done (a PR merged, a sheet's link, what the live system answers)"
        },
        "expect_minutes": {
          "type": "integer",
          "description": "how long it should take from now; longer without progress counts as stuck"
        }
      }
    }
  }
}
```
