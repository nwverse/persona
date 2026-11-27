Main conversation only. File each owner message you handle, follow-ups included, under one matter (a concern, todo_list kind concern): an existing one (id) or a new one (title). Only the owner's own text is filed, never tool results, notices or relayed messages. Give a new matter about (what, why, what counts as done; owner's language) and update it when the goal changes. Hand its tasks out with part_of the concern id; concern_report once they finish. now is one status line; ask (at most four options) is a decision shown as buttons and answered as a message; an empty ask clears it. Filing under a done concern reopens it; a task id is refused: file under its concern.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "an existing concern's id (todo_…), from todo_list"},
    "title": {"type": "string", "description": "a new matter's short title, owner's language; a matching open concern is reused"},
    "quote": {"type": "string", "description": "words of an earlier owner message to file or move here; the newest matching message is used"},
    "message": {"type": "string", "enum": ["latest", "none"], "description": "latest (default): file the newest unfiled owner message; none: only set about / now / ask"},
    "about": {"type": "string", "description": "what the matter is, why, what counts as done; 2-4 sentences, owner's language; empty clears"},
    "now": {"type": "string", "description": "one status line: what is being done or waited for"},
    "ask": {"type": "string", "description": "the decision the owner must make, shown as a card; empty clears"},
    "options": {"type": "array", "items": {"type": "string"}, "description": "answers offered as buttons, at most four"}
  }
}
```
