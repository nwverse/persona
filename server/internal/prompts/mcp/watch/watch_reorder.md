Reorder Overview's watch cards: ids first; the rest follow in their order.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["ids"],
  "properties": {
    "ids": {"type": "array", "items": {"type": "string"}}
  }
}
```
