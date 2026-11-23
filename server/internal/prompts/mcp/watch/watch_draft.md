Draft an Overview watch card when the user wants something checked regularly ("check X for me every day"). Show the preview, revise with `id`, and watch_confirm once they agree.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["question"],
  "properties": {
    "id": {"type": "string"},
    "title": {"type": "string"},
    "question": {"type": "string", "description": "what each run checks"},
    "schedule": {"type": "string", "description": "HH:MM daily (default 09:00), 30m/2h/1d or cron"},
    "display": {"type": "string", "enum": ["number", "status", "table", "trend"]},
    "unit": {"type": "string"},
    "alert": {
      "type": ["object", "null"],
      "properties": {
        "when": {"type": "string", "enum": ["above", "below", "bad", "change"]},
        "value": {"type": "number"}
      },
      "required": ["when"]
    },
    "project": {"type": "string", "description": "knowledge-base project"}
  }
}
```
