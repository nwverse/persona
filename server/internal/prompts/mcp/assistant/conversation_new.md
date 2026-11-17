Start a Persona conversation with an opening message; return its id now. Use for user-requested separate threads. The message is marked as coming from this one; the new conversation is named after its first turn and told to report with conversation_send. Failure or no report leaves one notice per conversation. An organization read-only policy may refuse; follow its reason instead of retrying. Add `tags` only if asked.

```json
{
  "type": "object",
  "required": ["text"],
  "properties": {
    "text": {
      "type": "string",
      "description": "everything the new conversation needs; it does not see this one"
    },
    "tags": {
      "type": "array",
      "items": { "type": "string" },
      "description": "initial tags; trimmed and de-duplicated, at most 10 of 32 characters each"
    }
  }
}
```
