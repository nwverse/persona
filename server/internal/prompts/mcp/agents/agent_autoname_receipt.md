Read-only: this assistant's agent_autoname confirmation cards and one coding agent's current naming snapshot. It cannot prove an earlier call started or succeeded (no correlation with the tool-call id; the snapshot may be older or another caller's; background failures are not attributed), so never retry from it: unknown means unsettled. Use the exact machine and full session id from agents_list; only the calling assistant's cards are read. since/before: an RFC3339 window of at most 24 hours, given together; pages are bounded and incomplete ones reported. It never calls the naming model, writes settings, resolves a card or renames. Not for Persona conversations, ordinary coding Agents or extensions.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["machine", "session_id"],
  "properties": {
    "machine": {
      "type": "string",
      "description": "the machine's exact id, as agents_list shows it"
    },
    "session_id": {
      "type": "string",
      "description": "the agent's exact full session id, never a prefix"
    },
    "since": {
      "type": "string",
      "format": "date-time",
      "description": "inclusive window start; requires before"
    },
    "before": {
      "type": "string",
      "format": "date-time",
      "description": "exclusive window end, at most 24 hours after since"
    }
  }
}
```
