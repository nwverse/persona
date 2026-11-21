Send Lark text as configured (default bot), not via CLI/ask_user. Server: approved, revocable channel/chat/scenario/identity grant or owner confirmation; not repository send grants. Bot inaccessible: report, never switch to user. Retries reuse idempotency_key; check the destination after uncertain delivery. Other sends keep their approvals.

```json
{
  "type": "object",
  "properties": {
    "channel": { "type": "string", "enum": ["larkcli"] },
    "target": { "type": "string", "description": "oc_ chat ID; resolve read-only." },
    "text": { "type": "string", "minLength": 1, "maxLength": 16000 },
    "idempotency_key": { "type": "string", "minLength": 1, "maxLength": 200 }
  },
  "required": ["channel", "target", "text", "idempotency_key"],
  "additionalProperties": false
}
```
