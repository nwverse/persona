Change one of the user's Persona to-dos: its title, notes, due date, reminder or priority. Only what you give changes; "" clears notes, due or remind. A new reminder time is pushed again even when the old one already was. Done at once, nothing to confirm, also for a task's to-do. To mark it done or open it again use todo_complete / todo_reopen.

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {
      "type": "string",
      "description": "the to-do's id (todo_…), as todo_list gives it"
    },
    "title": {
      "type": "string",
      "description": "the new title"
    },
    "notes": {
      "type": "string",
      "description": "the new notes (they replace the old ones: keep what should stay); \"\" clears them"
    },
    "due": {
      "type": "string",
      "description": "the new due date: a day (2026-11-15) or a moment (2026-11-15 18:00 in the user's time zone, RFC 3339); \"\" clears it"
    },
    "remind": {
      "type": "string",
      "description": "the new reminder: a moment (2026-11-14 09:00, RFC 3339) or 30m / 2h / 1d from now, not in the past; \"\" clears it"
    },
    "priority": {
      "type": "string",
      "enum": ["high", "normal", "low"]
    }
  }
}
```
