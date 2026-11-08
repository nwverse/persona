Show or change which chats a source's history import reads and how: `full` (every message), `anchor` (only the user's own messages and mentions of the user, with 50 messages on each side) or `off`. Without `set` it refreshes the plan from the source's chat list and lists it grouped by mode, largest first, with each chat's id; the user's choices survive a refresh, new chats get the defaults (direct messages full, groups anchor, muted / official / bot / system chats off). Chats the source's search finds that the list does not have are added as "not in the list", and new chats to import take the approval back, so the plan needs kb_history_resume again. Listing or changing the plan never starts the import: show the user the list, apply the exceptions they name with `set`, show the result again, and only when they say to start call kb_history_resume. Needs `kb.history.enabled` in manager.json.

```json
{
  "type": "object",
  "properties": {
    "source": {
      "type": "string",
      "description": "connector id from kb.history.sources; omitted: every configured source (required with set)"
    },
    "set": {
      "type": "array",
      "description": "mode changes; mode null goes back to the default",
      "items": {
        "type": "object",
        "required": ["id", "mode"],
        "properties": {
          "id": {
            "type": "string"
          },
          "mode": {
            "type": ["string", "null"],
            "enum": ["full", "anchor", "off", null]
          }
        }
      }
    }
  }
}
```
