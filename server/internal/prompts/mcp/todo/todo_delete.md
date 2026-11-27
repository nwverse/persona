Delete a Persona to-do for good (with its notes, agent links and decisions), only when the user asks; a finished one is completed instead (todo_complete). Always confirmed on the phone first; declined, answered in writing or unanswered, nothing is deleted. Its agents are left as they are.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {
      "type": "string",
      "description": "To-do id from todo_list."
    }
  }
}
```
