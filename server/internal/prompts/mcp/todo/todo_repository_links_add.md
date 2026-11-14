Add issue/PR URLs to an existing to-do. Use this after finding or opening related work, without asking the user to edit metadata manually. The manager derives and groups repository identities, preserving existing aliases, refs and source snapshots. A repository need not be preconfigured. Duplicate URLs do not duplicate links; an explicit call may restore a previously removed link. The batch is atomic. Read todo_get or todo_repositories_get for current associations; use the revision-checked set tools for removal or replacement. Links are context, never evidence by themselves that work is merged, deployed or complete.

```json
{
  "type": "object",
  "required": ["id", "urls"],
  "properties": {
    "id": {"type": "string"},
    "urls": {"type": "array", "minItems": 1, "maxItems": 50, "items": {"type": "string"}}
  },
  "additionalProperties": false
}
```
