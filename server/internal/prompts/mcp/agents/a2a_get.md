Read one A2A task with its messages and delivery receipts, or list the latest tasks you sent or received. The list returns a before cursor as next when more are available.

```json
{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string"
    },
    "before": {
      "type": "string"
    }
  }
}
```
