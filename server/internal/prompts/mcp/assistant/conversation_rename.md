Rename a Persona conversation. For an active direction, give its plain project-supervisor `name`, without a date or task-kind prefix; this uses direction_name and is allowed from the main conversation only. For an ordinary conversation, give `kind` and `topic`: the title reads MMDD | kind | topic, using the day it started. A title or direction name the user chose stays unless they explicitly asked for this rename (then pass `user_asked`). The main conversation has no title to change.

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {
      "type": "string",
      "description": "full conversation id or its first 8 characters; this conversation's own id renames it"
    },
    "name": {
      "type": "string",
      "maxLength": 40,
      "description": "required for an active direction: its plain project-supervisor name, without a date or kind prefix"
    },
    "kind": {
      "type": "string",
      "enum": ["功能", "设计", "修复", "优化", "发布", "探索", "文档", "研究", "评审", "定时"],
      "description": "required for an ordinary conversation; not needed for a direction"
    },
    "topic": {
      "type": "string",
      "description": "required for an ordinary conversation: what it is about, at most 14 characters"
    },
    "user_asked": {
      "type": "boolean",
      "description": "the user explicitly asked for this rename; required to replace their chosen title or direction name"
    }
  }
}
```
