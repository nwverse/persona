Start one bounded subtask of your current supervised task in a separate coding Agent, on your current computer and directory. Define the boundary, what not to do, and the evidence to return; never copy the parent assignment verbatim or hand off the whole parent task. There is one supervised handoff layer: a session linked with source spawned cannot call task_spawn. The child joins the SAME to-do with the SAME supervisor; this does not make you a task leader or create a new task. Use this instead of a runtime's standalone new-conversation tool (for example Codex create_thread). Ask your supervisor for an unrelated task. The server chooses the task, parent, launcher and permission from your verified assignment, never from arguments. Unknown permission stays read-only. The result names the child session, to-do and supervisor. Do not retry a start with an uncertain result.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "description": "Short title for this part of the existing task"
    },
    "prompt": {
      "type": "string",
      "description": "Bounded subtask within the current task: state its scope, what not to do, and the evidence to return; do not copy the parent assignment"
    }
  },
  "required": ["title", "prompt"],
  "additionalProperties": false
}
```
