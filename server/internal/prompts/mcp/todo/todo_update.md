Apply supplied fields now, no confirmation. "" clears notes/due/remind/project/part_of/audience/direction_id. [] clears after. Use todo_complete or todo_reopen. outward_reply records the original HTTPS topic, permalink, exact text and RFC3339 provider time. Never sends. promise overrides detection, due_at overrides 24h.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {"type": "string"},
    "title": {"type": "string"},
    "notes": {"type": "string", "description": "Replace notes, keep needed text."},
    "due": {"type": "string", "description": "Date or local/RFC3339 time."},
    "remind": {"type": "string", "description": "Future local/RFC3339 time or 30m/2h/1d. Notifies again."},
    "priority": {"type": "string", "enum": ["high", "normal", "low"]},
    "project": {"type": "string", "description": "Existing KB project, no duplicates."},
    "audience": {"type": "string", "description": "human or autonomous."},
    "direction_id": {"type": "string"},
    "part_of": {"type": "string", "description": "Request ID. Changes clear after."},
    "after": {"type": "array", "items": {"type": "string"}, "description": "Replace sibling dependencies."},
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
