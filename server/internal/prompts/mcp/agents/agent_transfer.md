Move an existing open Agent task to a current direction, or back to the main conversation, from the main conversation only. This is the App's Move to action: the same task, brief, criteria, estimate, origin and Agent session stay; supervision and the task card move to the destination. The Agent keeps running without a message, interruption or restart. Use agents_list for the Agent and conversations_list for the exact destination conversation id. To undo, transfer back to the previous supervisor. An Agent on the owner's own to-do, an assistant conversation, or an Agent without an open task is refused; use agent_supervise to adopt untracked work first. This does not hand new work to the Agent or change its scope.

```json
{
  "type": "object",
  "required": ["machine", "session_id", "supervisor"],
  "properties": {
    "machine": {"type": "string", "description": "the machine id or name from agents_list"},
    "session_id": {"type": "string", "description": "the Agent session id, or its first 8 characters"},
    "supervisor": {"type": "string", "description": "the exact id of a current direction or the main conversation from conversations_list"}
  }
}
```
