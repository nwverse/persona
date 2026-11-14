Prepare or manage an explicitly requested backend-created workspace for one to-do. Existing engine-created and reused folders are unchanged. inspect is read-only and returns persisted state plus a live observation. prepare needs a stable operation_id; the first request also supplies machine_id, layout and selected_repo_ids from todo_repositories_get. All selected repositories must be ready. Retry the same operation after a transient error; inspect an unknown outcome first. To correct branch/path choices, cancel the old operation, edit repository associations or naming rules, and prepare with a new operation_id on the same to-do. Successful unchanged repositories and local work are preserved; conflicting resources require explicit cleanup. Cleanup removes working trees, retaining branches by default. force can discard uncommitted work and delete_branch can remove branches: use those only when the user explicitly requested that effect. No action starts an Agent or changes the to-do stage. Preparation after cleanup requires a new operation_id.

```json
{
  "type": "object",
  "required": ["id", "action"],
  "properties": {
    "id": {"type": "string"},
    "action": {"type": "string", "enum": ["inspect", "prepare", "cancel", "cleanup"]},
    "operation_id": {"type": "string"},
    "machine_id": {"type": "string"},
    "layout": {"type": "string", "enum": ["root", "container"]},
    "selected_repo_ids": {"type": "array", "items": {"type": "string"}},
    "force": {"type": "boolean"},
    "delete_branch": {"type": "boolean"}
  },
  "additionalProperties": false
}
```
