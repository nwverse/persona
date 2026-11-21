Update supplied fields immediately. "" clears notes/due/remind/project/part_of; [] clears after. New reminders notify again. Use todo_complete or todo_reopen. outward_reply records a sent reply's original HTTPS topic, permalink, exact text and RFC3339 provider time. Retry recording only. promise overrides detection; due_at overrides 24h.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {"type": "string"},
    "title": {"type": "string"},
    "notes": {"type": "string", "description": "Replacement; preserve still-needed notes."},
    "due": {"type": "string", "description": "YYYY-MM-DD or RFC3339."},
    "remind": {"type": "string", "description": "Future datetime or 30m/2h/1d; new times notify again."},
    "priority": {"type": "string", "enum": ["high", "normal", "low"]},
    "project": {"type": "string", "description": "Existing KB project slug/name; avoid duplicates."},
    "part_of": {"type": "string", "description": "Request id; changing clears after."},
    "after": {"type": "array", "items": {"type": "string"}, "description": "Sibling piece ids."},
    "outward_reply": {
      "type": "object",
      "required": ["topic_url", "sent_url", "text", "sent_at"],
      "properties": {
        "topic_url": {"type": "string"},
        "sent_url": {"type": "string"},
        "text": {"type": "string"},
        "sent_at": {"type": "string"},
        "due_at": {"type": "string"},
        "promise": {"type": "boolean"}
      }
    }
  }
}
```
