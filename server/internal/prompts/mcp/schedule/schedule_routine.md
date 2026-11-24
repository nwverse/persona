Create a recurring routine, or pass `id` from list_followups to edit only the fields supplied. `enabled: false` pauses it. Use `time` with optional `weekdays`, or `cron` (5 fields in the user's zone, at least 5m apart, 0/7 = Sunday, restricted day fields match either; seconds, @every and TZ= are refused; check the reply's next runs). `until` cannot be a bare clock time.

Runs inherit Main chat's launcher/model/effort; permission mode stays the user's. `fresh_each_run: true` starts a new chat per run with its state note.

For approved work advancing a supervised to-do, pass its explicit `todo_id` (this conversation must supervise it; omit keeps, "" clears). From the second run Persona checks progress (Agent answers, tool calls, criterion changes, reports; not waits or repeats); a long stall asks Change approach / Keep waiting, even with `push: asked`. Get owner consent before creating or changing recurring work. For a result kept as an Overview card, use watch_draft.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "todo_id": {
      "type": "string",
      "description": "explicit supervised to-do this routine advances"
    },
    "fresh_each_run": { "type": "boolean" },
    "id": {
      "type": "string",
      "description": "to change an existing routine"
    },
    "title": {
      "type": "string",
      "description": "short name shown in the app, e.g. 早报"
    },
    "prompt": {
      "type": "string",
      "description": "what to do each time, self-contained"
    },
    "time": {
      "type": "string",
      "description": "HH:MM, 24-hour"
    },
    "weekdays": {
      "type": "array",
      "items": {
        "type": "integer",
        "minimum": 1,
        "maximum": 7
      },
      "description": "1 = Monday … 7 = Sunday; omit or empty = every day"
    },
    "cron": {
      "type": "string",
      "description": "instead of time / weekdays: minute hour day-of-month month day-of-week in the user's time zone, e.g. */30 9-18 * * 1-5 or 0 10 1 * *; or @hourly / @daily / @weekly / @monthly / @yearly"
    },
    "enabled": {
      "type": "boolean"
    },
    "report": {
      "type": "boolean",
      "description": "a daily report (as a leader's): each run opens with Persona's north-star lines (interruptions, availability, change since yesterday), which the report starts with. Omit to keep"
    },
    "push": {
      "type": "string",
      "enum": ["every", "asked"],
      "description": "every (default): each run's answer is pushed; asked: only what a run sends with notify_user, for watching something (runs in its own conversation). Omit to keep"
    },
    "inbox": {
      "type": "string",
      "enum": ["off", "worthy", "every"],
      "description": "Inbox rows, independent of push: off (default), worthy (the run uses inbox_post only for something worth a look), every (Persona adds a row after each run, for a daily briefing). Only when the user asked. Omit to keep"
    },
    "run_if": {
      "type": ["object", "null"],
      "description": "owner-approved shell check on the routine's computer before scheduled runs: exit 0 runs, 1 skips, other errors follow on_error. Omit keeps; null removes",
      "properties": {
        "command": {"type": "string", "maxLength": 4096},
        "timeout_s": {"type": "integer", "minimum": 1, "maximum": 60, "default": 30},
        "on_error": {"type": "string", "enum": ["run", "skip"], "default": "run"}
      },
      "required": ["command"],
      "additionalProperties": false
    },
    "until": {
      "type": ["string", "null"],
      "description": "optional end time (RFC 3339 or 30m / 2h / 1d); then Persona turns it off and records a skipped run. Omit keeps; null clears"
    },
    "launcher": {
      "type": "string",
      "description": "own launcher instead of Main chat's (a name on the run's computer); empty follows Main chat. Omit to keep"
    },
    "model": {
      "type": "string",
      "description": "model id for that launcher (without one, pins the current launcher); empty: its default. Omit to keep"
    },
    "effort": {
      "type": "string",
      "enum": ["low", "medium", "high", "xhigh", "max"],
      "description": "Claude thinking effort (without a launcher, pins the current one). Omit keeps; empty clears"
    }
  }
}
```
