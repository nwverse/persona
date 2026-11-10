Hand part of your own task to workers: each task you give this starts a new coding agent on a computer, beneath the task you lead, at once, with no card for the user to start it. You get this tool only because your task was handed out as a leader task. Workers belong to this task only: you cannot pick another to-do (todo_id and role are refused), a worker cannot hand work on, and at most 8 workers of this task run at a time. A task that changes files (readonly false) must set permission "bypassPermissions", since nobody is there to answer a permission prompt; a task that only reads sets readonly true. Leave machine and cwd out to start the worker where you run. Each worker that has report_to_supervisor reports to you as a message that starts with "Persona · worker report"; your own reports still go to whoever handed your task to you. Returns at once, with each worker's to-do; it does not confirm the agent started.

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "additionalProperties": false,
  "properties": {
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 8,
      "items": {
        "type": "object",
        "required": [
          "title",
          "prompt",
          "readonly"
        ],
        "additionalProperties": false,
        "properties": {
          "title": {
            "type": "string",
            "description": "the worker's task in a short line"
          },
          "prompt": {
            "type": "string",
            "description": "the worker's first message: everything it needs to do the work without asking you"
          },
          "machine": {
            "type": "string",
            "description": "machine id; empty: the computer you run on"
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default): keep this machine; flexible: a failed launcher health check may move it to another computer"
          },
          "cwd": {
            "type": "string",
            "description": "the project directory: absolute path or ~/…; empty: your own directory when the worker runs on your computer"
          },
          "launcher": {
            "type": "string",
            "description": "launcher name, one that machine has; empty: the default"
          },
          "model": {
            "type": "string",
            "description": "model id; empty: the launcher's default"
          },
          "tier": {
            "type": "string",
            "description": "an Agent preset (a tier of the model profile in use); explicit launcher, model and effort override it"
          },
          "effort": {
            "type": "string",
            "description": "explicit effort override; omit to use the tier's"
          },
          "permission": {
            "type": "string",
            "enum": [
              "bypassPermissions",
              "plan"
            ],
            "description": "bypassPermissions is required when readonly is false; readonly true always runs in plan mode"
          },
          "readonly": {
            "type": "boolean",
            "description": "true when the worker only reads (looks things up, reads code, changes no project files); false when it changes files"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "what done means: results the worker must show, one each; the worker is told them"
          },
          "why": {
            "type": "string",
            "description": "optional: one line on why this machine and directory"
          }
        }
      }
    }
  }
}
```
