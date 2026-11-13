#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
ci_run 'Python script suites and repository source/doc guards' 900 python3 scripts/ci/python-tests.py
ci_run 'Issue-link workflow tests' 120 node scripts/test/pr_needs_issue_test.mjs
ci_run 'Apple catalogs and localization source' 180 python3 scripts/i18n/apple_l10n.py check
ci_run 'Android translations' 180 python3 scripts/app/android_i18n_check.py
ci_run 'Android token ratchet' 180 python3 scripts/app/android_token_baseline.py
ci_run 'Android design-system slot order' 180 python3 scripts/app/android_slot_order.py
ci_run 'Workflow syntax and shell lint' 180 actionlint
ci_run 'Release language-check shell syntax' 30 bash -n scripts/release/site_language_check.sh scripts/release/publish.sh
ci_run 'Website worker, release and bilingual page tests' 180 node --test site/worker.test.mjs site/releases.test.mjs site/i18n.test.mjs
