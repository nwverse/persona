Send a message to an agent as the user would type it (answers its question, tells it to continue, gives new instructions). Queued if the agent is working. Only send what the user asked for. An agent that has stopped (a finished task's agent stops after a few idle minutes, an idle one after hours; its terminal stays) takes no plain messages: the send is refused and nothing is sent. Decide how to go on. Resume it (send again with resume true and resume_why) when the next work depends on its context: it has just investigated this and should now fix it, rework of its own work, or the user asked for the agent that has the context. That resumes the same session as the user's Resume on its terminal page does (its own launcher, directory and permission mode, in the terminal it left when that is still there) with your text as its first message; the user sees which agent you resumed and resume_why. A resumed agent must be supervised: from the main conversation give task (a new task of yours, its card marked resumed) unless it is still on an open task of yours. A resume is refused with the reason when its computer is offline, its context is nearly full, or its computer cannot resume it (session record gone, launcher unavailable, open elsewhere): then start a new agent. Start a new agent with agent_start in its directory (with the task when it had one, reading this one with agent_read for what the new one needs) for unrelated work, or when its context is nearly full or it went off track. When you cannot tell which, ask the user with ask_user: reuse this agent, or start a new one. A branch the user made that has not run yet is started by its first message. A text of only /exit or /quit stops a running agent (its terminal tab stays) and does nothing to one that has stopped; a finished task's agent is stopped for you, so there is no need to. An agent on a task another conversation supervises is not yours to stop: /exit to it is refused and nothing is sent (the user can close it in the app, or with agent_archive and close once it is idle).

When a dialog holds the agent's terminal the send is refused (dialog_open) with the dialog's first line, and whether it is only a notice that Enter closes ("Enter to continue", no options). Send again with dismiss_notice true to close such a notice first. Confirmations, permissions and pickers are never closed for you: tell the user, or answer them with interaction_answer.

When you hand new work to an agent that runs already (one the user opened, a long-lived one that passes work on), give task from the main conversation: once the text is sent the agent is your supervised task, as agent_supervise makes one (a todo with the text as its notes, the agent on it, done_when, a card in the conversation; 督办 messages, task_review), so the work is tracked instead of only said. Put the criteria in the text too: the agent is told only what the text says. An agent already on one of your tasks takes more of it under the same title (no second task; done_when is updated); a different title is refused, so other work is not filed under that task. An agent on the user's own todo or another conversation's task is refused. Refused means nothing was sent. If the send fails, no task is made. Leave task out, or null, for a reply, a nudge or a decision on work it already has; a task with nothing in it (a blank title, no non-blank done_when, no expect_minutes above 0, no other key) counts as left out too.

```json
{
  "type": "object",
  "required": ["machine", "session_id", "text"],
  "properties": {
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
      "description": "resume this stopped agent with its context, as the user's Resume does, and send text as its first message; needs resume_why. Ignored for an agent that runs"
    },
    "resume_why": {
      "type": "string",
      "description": "with resume: one line on why this agent's own context is needed (the user sees it), e.g. it just found the cause and should now fix it"
    },
    "dismiss_notice": {
      "type": "boolean",
      "description": "Press Enter first to close a notice that only says \"Enter to continue\" (no options). Any other dialog still refuses the send."
    },
    "attachments": {
      "type": "array",
      "maxItems": 8,
      "items": { "type": "string" },
      "description": "pictures and files the user gave you for it, by their absolute paths on your computer: a picture they sent (the path Claude Code gives it), a file the app put in their message. They are copied to the agent's computer and listed at the end of its message, and the text's mentions of them name the copies. Only those the user gave you in this conversation are accepted"
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
