#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
ci_run 'Browser verification dependencies' 600 bash -c 'cd web && npm ci --no-audit --no-fund'
ci_run 'Receiver regression and retained behavior' 300 bash -c 'cd web && npx vitest run src/lib/api-dedupe.test.ts src/lib/session-list-abort.test.ts src/views/SidebarConcurrency.test.tsx --maxWorkers=2'
ci_run 'Receiver mutation must fail' 180 bash -c '
set -eu
file=web/src/lib/api.ts
cp "$file" "$file.receiver-backup"
trap '\''mv "$file.receiver-backup" "$file"'\'' EXIT
python3 - <<"PY"
from pathlib import Path
p=Path("web/src/lib/api.ts")
s=p.read_text()
a="entry.fetcher.call(globalThis, "
assert s.count(a)==1
p.write_text(s.replace(a,"entry.fetcher("))
PY
set +e
(cd web && npx vitest run src/lib/api-dedupe.test.ts -t "global fetch receiver" --maxWorkers=2) > receiver-mutation.log 2>&1
rc=$?
set -e
cat receiver-mutation.log
[ "$rc" -ne 0 ]
grep -q "Illegal invocation" receiver-mutation.log
echo "RECEIVER_MUTATION_DETECTED=1"
'
ci_run 'Production browser bundle' 600 bash -c 'cd web && npm run build'
ci_run 'Chrome settings native fetch smoke' 180 node web/e2e/native-fetch-settings.mjs /tmp/persona-fetch-settings-chrome
ci_run 'Retain Chrome evidence' 60 python3 - <<'PYCODE'
import base64
from pathlib import Path
for name in ["result.json", "settings-light.png", "settings-dark.png"]:
    print("CHROME_ARTIFACT " + name + " " + base64.b64encode((Path("/tmp/persona-fetch-settings-chrome") / name).read_bytes()).decode())
PYCODE
