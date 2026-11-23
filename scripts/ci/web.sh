#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
ci_run 'Web install' 600 bash -c 'cd web && npm ci --no-audit --no-fund'
ci_run 'Web lint' 300 bash -c 'cd web && npm run lint'
ci_run 'Web typecheck' 300 bash -c 'cd web && npm run typecheck'
ci_run 'Web tests' 600 bash -c 'cd web && npm test'
ci_run 'Web build and privacy' 600 bash -c 'cd web && npm run build'
# Persona Core alone (#13932, docs/web/web.md "Modules"): built and tested with zero feature modules. Fails when a file
# outside web/src/modules reaches a module's files, or a core test only passes with a module registered.
ci_run 'Web core-only build' 600 bash -c 'cd web && npm run build:core-only'
ci_run 'Web core-only tests' 900 bash -c 'cd web && npm run test:core-only'
