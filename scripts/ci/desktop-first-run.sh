#!/bin/bash
# Real production-package first-run acceptance, not the development-window smoke.
set -euo pipefail
source "$(dirname "$0")/common.sh"
ci_run 'Desktop packaged first-run acceptance' 1500 node desktop/scripts/first-run-test.mjs "$@"
