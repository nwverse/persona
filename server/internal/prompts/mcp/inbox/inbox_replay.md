Recover specific Lark messages into Persona's Inbox only when the user explicitly asks you to recover those messages. Give one chat ID, an RFC3339 start and end spanning no more than one hour, and 1-10 exact message IDs. This replay reads only those selected source messages and does not move the global Lark cursor. It follows the normal Inbox path and may hand an explicitly addressed bot message to Persona's main conversation; it never sends a Lark message. After replay, use `read_only: true` to read the retained Inbox receipt without replaying again. `read_only: true` does not fetch Lark history; it only returns already retained items whose latest message ID is selected.

```json
{
  "type": "object",
  "required": ["chat_id", "start", "end", "message_ids"],
  "properties": {
    "chat_id": {
      "type": "string",
      "description": "One Lark chat ID beginning with oc_."
    },
    "start": {
      "type": "string",
      "format": "date-time",
      "description": "Start of the bounded replay window."
    },
    "end": {
      "type": "string",
      "format": "date-time",
      "description": "End of the bounded replay window, at most one hour after start."
    },
    "message_ids": {
      "type": "array",
      "minItems": 1,
      "maxItems": 10,
      "uniqueItems": true,
      "items": {
        "type": "string",
        "description": "An exact Lark message ID beginning with om_."
      }
    },
    "read_only": {
      "type": "boolean",
      "default": false,
      "description": "When true, only read matching items already retained in the Inbox; do not replay or fetch messages."
    }
  },
  "additionalProperties": false
}
```
