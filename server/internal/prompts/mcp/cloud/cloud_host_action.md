Act on the cloud server a computer is bound to; `action` must be one its host lists in cloud_hosts. `read` actions run at once (`snapshot_list` answers the snapshots); others first ask the user to approve on the phone, and a declined or unanswered card does nothing. `reconnect` with `start` reconnects the computer once its server runs. Linking a computer to a server is the user's to do in the app.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["machine", "action"],
  "properties": {
    "machine": {
      "type": "string",
      "description": "computer id or name"
    },
    "action": {
      "type": "string",
      "enum": ["start", "stop", "reboot", "hard_reset", "force_stop", "sync_ip", "snapshot_list", "snapshot_create", "console", "custom"]
    },
    "id": {
      "type": "string",
      "description": "the custom action's id (action custom)"
    },
    "name": {
      "type": "string",
      "description": "the new snapshot's name (snapshot_create)"
    },
    "reconnect": {
      "type": "boolean",
      "description": "start only: reconnect the computer once it runs"
    }
  }
}
```
