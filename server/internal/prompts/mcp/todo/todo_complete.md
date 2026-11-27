Mark a Persona to-do done when the user says it is done or asks to close it. A plain to-do closes at once; a task's, or one agents work on, is confirmed on the phone first, since closing it also closes its agents once idle; declined, nothing changes. A concern is not closed with this: report its outcome with concern_report.{{template "shared/mcp_language" .}}

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
