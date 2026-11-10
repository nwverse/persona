# Lessons: Paseo engine (`integrations/paseo`)

Read before changing the bridge (`integrations/paseo`), its node runner (`node/paseobridge.go`), or testing real Paseo. Design: docs/integrations/paseo.md.

- **Connect only to the address in home's `paseo.pid`, never default-port fallback** (2026-11-04). Paseo writes PID file immediately with listen:null; fills address after listening. Earlier fallback `PASEO_LISTEN` / `127.0.0.1:6767` connected a temporary-daemon prototype to the live local Paseo and sent agent.create. Missing provider made it fail, leaving only failed `creations/` record.
  - Bridge / node detection now trust PID-file address only, like Paseo CLI.
  - Before temporary-daemon requests, verify `daemonTarget(home)` port equals assigned port.
- **Claude Code `ExitPlanMode` permission passed to Paseo lacks plan body** (tested 2.1.294). Input {}, no metadata.planText. Plan in corresponding timeline tool call `detail.type: "plan"` and transcript tool_use.input.plan. Bridge finds via metadata.toolUseId.
- **Pending requests in Agent snapshots are trimmed**: `sanitizePendingPermissions` drops empty input. `agent_permission_request` event more complete, but either arrives first; never assume input present.
- **Read-only Bash does not request Claude permission**, e.g. echo / ls. E2E approval card needs writing command, e.g. touch.
- **New Paseo 0.14 Claude Agent `runtimeInfo.sessionId` null**, Claude ID appears only after first turn starts. Persona-created sessions use `paseo-<agent id>` stored `persona.session` label; waiting for Claude ID would change identity mid-run.
- **Provider models await warmup**: `get_providers_snapshot` immediately loading; `providers_snapshot_update` after ~1 second. First hello launchers may lack models; bridge rechecks while providers loading.
- **Send reply waits until turn actually starts**, Paseo max 60 seconds, node runtime timeout 30. Loading closed Agent may take longer; bridge waits max 25 then treats as handed to Paseo. Creation same.
- **Subscription event can arrive in same read as reply**: `fetch_agents` reply immediately followed by directory update. Register synchronously while handling reply, not after await, or lose events. Reply also `subscriptionId`, so route `requestId` replies before `subscriptionId` events.
- **Reading closed-Agent timeline loads it** from persistence. Skip closed Agents when fetching list last-text; read only when phone opens chat.
- **Paseo's `config.mcpServers` entries need `type: "stdio"`** (0.13.2 schema), which Claude-style `mcp.json` entries omit. The bridge adds it and sets each server's `PERSONA_SESSION_ID` to the Agent's `persona.session` label (#3655); the provider's own session id would break the task link.
- **A provider that takes no MCP servers fails `agent.create` when `config.mcpServers` is set** (0.13.2 `requireExternalMcpSupport`: `Provider '<name>' does not support MCP servers`, an `error` on the response). Paseo stores a failed creation under its agent ID, so the bridge's one retry without `mcpServers` uses a new agent ID, and only for that message; the fake daemon (`test/fake-paseo.js`) refuses it for `noMcpProviders` and checks the stdio shape Paseo's schema needs.
