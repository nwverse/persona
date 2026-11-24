File to-dos under kb_list projects or mark them temporary (one-off), as one undoable batch. suggest: places for unsorted to-dos, how sure and why; moves nothing, show the user. apply: what the user accepted or asked. undo: a batch back; no batch lists them.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["action"],
  "properties": {
    "action": {"type": "string", "enum": ["suggest", "apply", "undo"]},
    "items": {"type": "array", "description": "[{id, project or temporary}]"},
    "batch": {"type": "string"}
  }
}
```
