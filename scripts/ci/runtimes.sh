#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
ci_run 'Authoring Go format patch on sj-wsl' 120 gofmt -d server/internal/db/logical_conversations.go server/internal/db/conversation_delivery.go server/internal/db/conversation_delivery_test.go server/internal/assistant/logical_devices.go server/internal/assistant/logical_switch.go server/internal/assistant/logical_delivery.go server/internal/assistant/conversations_switch.go server/internal/assistant/logical_zero_turn_placeholder_test.go
ci_run 'TypeScript SDK install' 600 bash -c 'cd sdk/ts && npm ci --no-audit --no-fund'
ci_run 'TypeScript SDK source authoring, portability, types and tests' 600 bash -c 'cd sdk/ts && npm run test:portable && npm run typecheck && bun test'
for package in integrations/*/package.json; do
  directory="${package%/package.json}"
  ci_run "$directory tests" 300 bash -c 'cd "$1" && npm test' bash "$directory"
done
ci_run 'Pinned OpenCode MCP isolation contract (fake model)' 300 node scripts/ci/opencode-contract.mjs
