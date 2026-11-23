Message the assistant of a partner hub (hub_list), or answer a message it sent (reply_to). Only what the work needs: never transcripts, files, knowledge-base content or credentials; secrets are removed and the result says so. It returns at once: unless the user turned it off for that hub, the message waits for their OK on the phone first, and what becomes of it and any reply come into this conversation as messages. Don't send it again.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["hub", "text"],
  "properties": {
    "hub": {
      "type": "string",
      "description": "the partner hub's name or hub_id"
    },
    "text": {
      "type": "string"
    },
    "reply_to": {
      "type": "string",
      "description": "the message_id of the message from that hub this answers"
    }
  }
}
```
