Read one entity: its metadata (links to people / chats / projects, tags), compressed context (summary.md), a project's timeline, a chat's messages of the last 7 days, a recording's meeting minutes, or an all-day recording's day recap (the day's timeline, meetings, suggested to-dos), with each action item's state (suggested, or its to-do's) and its timestamped transcript while still retained (phone / watch recordings, text only), a meeting's / event's recordings, a topic's knowledge points by subtopic (each with its kind, reason and the entries it came from). Transcripts expire after recording_days (default 14 days); then meetings keep only minutes and all-day recordings their recap, per minutes_days (default forever). id from kb_list / kb_search (a project also by name). A project lists its 20 most relevant people and the count (all: meta.json); results over ~20K tokens are cut, saying where the rest is.

```json
{
  "type": "object",
  "required": ["kind", "id"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["projects", "chats", "people", "docs", "meetings", "events", "recordings", "topics"]
    },
    "id": {
      "type": "string"
    }
  }
}
```
