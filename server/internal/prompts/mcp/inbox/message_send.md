Send a plain-text Lark message using the owner's configured outward-message identity. Use this instead of invoking lark-cli directly or asking ask_user to approve a send. The server either matches an explicitly approved, revocable standing authorization for this exact channel/chat/scenario/identity, or shows the owner a permission card. Never interpret a repository's send permission as permission to send chat messages. A bot cannot send to chats it cannot access; report that error and never retry as the owner. Keep idempotency_key stable across retries of the same intended message; after an uncertain outcome, inspect the original destination before considering a new send. Other connectors and CLI writes keep their existing approval rules.

```json
{
  "type": "object",
  "properties": {
    "channel": { "type": "string", "enum": ["larkcli"] },
    "target": { "type": "string", "description": "Exact Lark chat ID, starting with oc_. Resolve it with read-only tools first." },
    "text": { "type": "string", "minLength": 1, "maxLength": 16000 },
    "idempotency_key": { "type": "string", "minLength": 1, "maxLength": 200 }
  },
  "required": ["channel", "target", "text", "idempotency_key"],
  "additionalProperties": false
}
```
