Stage the recurring task's state note for this scheduled run. Call near the end of the run with the run key in the scheduled prompt and the full replacement state (up to 4096 UTF-8 bytes). Keep cursors, references, and what was already reported. The hub commits the note only after a successful run. An empty string clears the note.

```json
{
  "type": "object",
  "required": ["run_key", "text"],
  "properties": {
    "run_key": { "type": "string" },
    "text": { "type": "string" }
  }
}
```
