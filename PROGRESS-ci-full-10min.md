# PR #13414 progress (temporary: delete this file before merge)

Branch task/ci-full-10min. Paused for a CI machine restart, 2026-11-23.

## Done on the branch

- web.sh: typecheck and build beside the tests.
- go_partitions.py: internal/voice in 4 partitions (server-managers 388 s -> 273-285 s on the gate).
- full.sh: PERSONA_CI_FULL_INCLUDE / EXCLUDE / JOBS, with scripts/ci/test_full.py.
- server-shards.py: partitioned packages listed at once (locally 161 s -> 113 s for five packages).
- test_apple_readiness.py: iPad cases moved to test_apple_readiness_ipad.py (59 s + 44 s locally; was a 120 s timeout).
- Main reds fixed here: MOCK_MEETING_FINISHING moved to scripts/mock/meeting_finishing_mock.py (#13519); extmgr bot
  identity-read race; feature-map line refs; web launch delivery (below).
- Last commit: web/src/lib/new-agent.ts keeps the lazily loaded delivery module, so a start after the first goes out in
  the same turn. #7063 made every delivery await import("./new-agent-delivery"), and 12 tests in four view files that
  wait a fixed number of turns failed on main (StartingAgent, Todos.handover-launchers, NewWorktree,
  SidebarLaunchHandoff). The four files load the module once in beforeAll; no assertion changed. Locally: those files
  44/44 three times, 59 nearby files 555/555, typecheck and lint pass. NOT yet checked: npm run build (the local run
  failed only because go was not on PATH; rerun with the Go toolchain on PATH).

## Gate on 33f7cc5fe (proposed 3-slot split, three runs started together, two probes on the fourth slot)

| Part | Lanes (s) | Total | Result |
| --- | --- | --- | --- |
| X | server-managers 405, server-node 298 | 703 | FAIL: assistant/repository TestCheckUsesEnvironmentProxy, node TestDesktopSessionsAreInTheirProject |
| Y | server-other 186, scripts 353 | 539 | FAIL: other (extension container isolation); scripts (engine_switch, meeting_highlights, voice mocks, compatibility identifiers) |
| Z | server-build 344, web 500 | 844 | FAIL: web, the #7063 tests above (fixed in the last commit) |

All four slots were busy and the build cache was cold after the main merge, so every lane ran 30-160% slower than in
the previous round (build 131 -> 344 s, web 338 -> 500 s). Not a clean number; rerun when the machine is quiet.
Probes (old base b6e942352, failures are that base's main reds): office 267 s submit->result (244 s run, no queue);
link 568 s (532 s run, no queue). The faster listing reaches probes only once this PR is on main.

## Next

1. Triage the X and Y failures: run each alone on this head and on origin/main; fix what is main's, report the rest.
2. Rerun the three parts on a quiet machine for the clean full-suite time; then the probes after merge.
3. Split plan for infra (CI machine's full-lanes.tsv, not in this repository): 3-slot line
   `server-managers,server-node | server-other,scripts | server-build,web` (estimate from quiet-machine lane times
   508 / 518 / 469 s, about 8.6 min, against 753 s for the current line). Two slots cannot reach 10 min (about 750 s).
4. Top-20 before/after table, PR body, web vitest overhead issue draft.
