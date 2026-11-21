Read and change Persona's own settings for the user: their inbox sorting rules, how their inbox items link to their tasks, 关于我 (what you know about them), your default launcher (model), their mail accounts, which Claude Code config directories each machine keeps out of Agents, and each extension's settings (extension.<id>: what Settings › Extensions shows, like turning one of its features on). Start with action list (what there is, what each takes); get reads one. set / add / remove put a card on the user's phone that says what changes (before → after) and nothing changes until they approve; what the card needs filled in, like a mail password, the user types there, so never ask for passwords in the conversation. Change only what the user asked for.

Background model groups `model_config.kb`, `model_config.inbox.triage`, `model_config.agent.delegate`, and `model_config.translate` take `get`, `set`, and `test`. `get` returns the effective binding, revision, and compatible `purpose.provider_ids`. For `set` and `test`, value is one provider ID string from that list. `set` uses the same phone approval as other changes; a different provider clears the old model, effort, and permission mode. `test` reuses the bounded provider connection check, does not create a card, and saves nothing; saving an unverified or offline provider remains allowed.

`assistant.standing_authorizations`: get returns `{revision, authorizations}`. Only the main conversation may set the whole list, with its current revision and the user’s own approval on the phone. Each grant names one `machine_id`, a specific absolute `directory`, `actions` (merge, push, deploy-test, deploy-production, send, delete, spend), `conditions`, the user’s exact `quote`, `source`, and `reversible`. Keep existing grants unchanged by ID; to change a grant, remove it and add a new entry without an ID. The server assigns the new ID and grant time. Never invent a user quote or expand the scope. Conditions may retain the user’s words; only independently verifiable conditions can permit automatic answers. Revocation is available in Settings through the authorization list. Tool permissions are never automatically approved.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["list", "get", "set", "test", "add", "remove"],
      "description": "default list"
    },
    "key": {
      "type": "string",
      "description": "the setting, from list (inbox.rules, mail.accounts, extension.<id>…)"
    },
    "value": {
      "type": ["string", "number", "integer", "boolean", "object", "array", "null"],
      "description": "set: the new value; test: the provider ID to check; add: the item (each setting's list entry says what)"
    },
    "id": {
      "type": "string",
      "description": "remove: which item (from get)"
    }
  },
  "additionalProperties": false
}
```
