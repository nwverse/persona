Search Lark through lark-cli. The `messages` kind searches messages; `chats`, `docs`, `people`, `minutes`, `todos`, and `mail` use the corresponding read-only CLI shortcuts where available.

```json
{
  "type": "object",
  "required": ["query"],
  "properties": {
    "query": {
      "type": "string"
    },
    "kind": {
      "type": "string",
      "enum": ["messages", "docs", "people", "chats", "calendar", "minutes", "todos", "mail"],
      "description": "messages by default"
    }
  }
}
```
