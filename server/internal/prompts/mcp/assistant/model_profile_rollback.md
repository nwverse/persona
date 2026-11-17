Undo the last model-profile switch using its saved before-image. Restores only touched model settings, not launcher credentials or the main chat's profile for new conversations. Inspect the rollback preview with model_profiles_list first; a second rollback redoes the switch.

```json
{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
```
