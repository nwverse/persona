Send a message to another Persona conversation (not this one). It waits for the current turn if running, or for 2 minutes of user quiet if they are talking; the result says which. Marked as from this one, it lets the other side answer with conversation_send. Forwarding allows a few requests per user message and a few messages per pair in ten minutes; your first reply to the conversation you are answering is its answer and adds no hop. Do not reply just to acknowledge. Policy refusals to main or a leader state why: follow it and do not retry. Owner-verbatim words to main stay allowed: start with them.

```json
{
  "type": "object",
  "required": ["id", "text"],
  "properties": {
    "id": {
      "type": "string",
      "description": "full id or its first 8 characters"
    },
    "text": {
      "type": "string"
    }
  }
}
```
