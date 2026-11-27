Change a watch card: `id` plus the watch_draft fields to change; `paused` stops or resumes it.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {"type": "string"},
    "paused": {"type": "boolean"}
  }
}
```
