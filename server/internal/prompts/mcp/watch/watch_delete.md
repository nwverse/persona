Delete a watch card and its scheduled task; confirm first unless the user asked.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {"type": "string"}
  }
}
```
