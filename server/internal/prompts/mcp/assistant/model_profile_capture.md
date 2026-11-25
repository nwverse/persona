Capture current purposes/launcher model fields into an existing profile with exact before/after phone approval. Preserves tiers, rechecks revision and reads back. Does not change current configuration; switch to apply.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string",
      "description": "Existing profile id from model_profiles_list"
    }
  },
  "additionalProperties": false,
  "required": ["id"]
}
```
