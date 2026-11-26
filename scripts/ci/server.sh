#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
GOFLAGS=$(sh scripts/test/go_trimpath.sh)
export GOFLAGS
ci_run 'Lark loose alignment and large-group names race' 600 bash -c 'cd server && go test -race -v -count=1 -timeout=5m ./internal/kb/... ./internal/modules/lark/... ./internal/larksyncwire/...'
ci_run 'Lark one-time backfill and space access race' 600 bash -c 'cd server && go test -race -v -count=1 -timeout=5m ./internal/extmgr -run "TestLarkSync|TestSpaceConversationsMayOpenOnlyTheConversationHandoff"'
