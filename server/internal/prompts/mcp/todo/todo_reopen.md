Reopen a done Persona to-do when the user says it is not done after all. A plain to-do reopens at once; a task's, or one agents worked on, is confirmed on the phone first.{{template "shared/mcp_language" .}}

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
