Read the subtasks you handed to other computers with dispatch_start. With no arguments: the user's other connected computers (id, name, whether they are online, home directory) and the tasks you dispatched with their states. With `session_id`: that task's state and its agent's last turn, which ends with its final reply when it has stopped. Give `wait_seconds` to wait for a working task to stop before answering (at most 50; call again while it still works, and do other work in between when you can). Only tasks you dispatched yourself can be read.

```json
{
  "type": "object",
  "properties": {
    "session_id": {
      "type": "string",
      "description": "the session id dispatch_start returned (or its first 8 characters)"
    },
    "wait_seconds": {
      "type": "integer",
      "description": "wait up to this long for a working task to stop; default 0, at most 50"
    }
  }
}
```
