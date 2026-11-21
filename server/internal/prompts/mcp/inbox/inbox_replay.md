Only on explicit user request, recover 1-10 exact om_ message IDs from one oc_ chat in an RFC3339 window of at most one hour. Uses Inbox routing (including bot handoff to the main chat), never sends Lark messages or moves the global cursor. Use read_only=true after replay to read retained items by latest message ID without fetching or replaying history.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["chat_id", "start", "end", "message_ids"],
  "properties": {
    "chat_id": {
      "type": "string"
    },
    "start": {
      "type": "string",
      "format": "date-time"
    },
    "end": {
      "type": "string",
      "format": "date-time"
    },
    "message_ids": {
      "type": "array",
      "minItems": 1,
      "maxItems": 10,
      "uniqueItems": true,
      "items": {
        "type": "string"
      }
    },
    "read_only": {
      "type": "boolean",
      "default": false
    }
  },
  "additionalProperties": false
}
```
