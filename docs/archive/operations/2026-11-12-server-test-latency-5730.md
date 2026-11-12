# Shared server test latency: fixed-revision verification (#5730)

Audience: contributors and maintainers running bounded server verification. This record concerns shared test cost,
independently of the Web skill picker (#5473) and ZIP importer (#5581). It does not establish either feature's merge readiness.

## Revisions and environment

- Main baseline: `f1468ef3d2ae9566430d2357e7d5bb9d3a973b81`, fetched before creating the independent optimization branch.
- Initial implementation: `522192ca9a5e576fd5203decc5acfe13b321d89d`.
- Implementation used by the completed main full-suite measurements, including the Codex polling correction: `be189594939b3294f07c5e634c8c74cc89b1524c`.
- Go 1.27.1, Linux/amd64, 32 allowed CPUs. Historical reports used Go 1.27.3; these measurements do not reproduce that toolchain.
- Independent baseline/head source copies; no other large Go verification jobs launched by this task during the final matched queue.
  The host is shared, not exclusively reserved. Each run records load, exact command, revision, exit code and wall time.
- Runtime/profile/proxy variables removed with `server_test_env.clean_environment`. Go settings/cache paths resolved before
  replacing HOME. HOME, USERPROFILE, temporary directories and XDG_RUNTIME_DIR point at a fresh owned fixture for each run.
  No test database URL is set. Existing database/platform/opt-in skips remain visible; database coverage is unavailable locally.
- Cold means an empty Go build cache, with the module cache already populated. OS caches were not flushed.
  Both variants were compiled into their warm cache before matched execution; preparation is excluded from test timings.
- A local supervisor applies the unchanged 600-second outer deadline, terminates the owned process group, and kills it after
  five seconds if needed. It also checks for detached processes carrying that run's unique temporary-directory marker.
  Cleanup and remaining owned processes are recorded separately. Raw logs remain private; only derived measurements are published.

An exploratory eight-package sample lost its outer supervisor when its queue coordinator was stopped. It is excluded from
all timing conclusions. The final queue keeps its supervisor alive throughout every sample.

## What changed

`testNode` previously paused 300 ms on every scripted-shell start even though that fixture's shell was already ready.
The existing test seam now declares the shell settled. Shell-readiness tests still call `watchShell`, exercising the real
observation path, startup questions, changing screens and slow shells.

Scripted Claude and Codex hosts also use a 20 ms readiness poll instead of paying the normal 500 ms between synchronously
produced frames. Timing-sensitive tests keep the production cadence through `withStartWaits`; normal nodes remain
at 500 ms. Virtual-time tests exercise real captured screens, a working frame resetting readiness, cancellation
before a read and the start deadline. They do not wrap real sockets.

`herdrNode` previously joined unfinished first-message checks only after their recovery window expired. The fixture now owns
a cancellation context, stops its starts, cancels and joins its checks, then joins engine lists before restoring shared state
or closing its socket. Delivery/recovery assertions still wait for `firstWait` themselves. Normal nodes retain the detached
background context, production deadlines and retry counts. No test, assertion, security fingerprint or opt-in boundary was removed.

The Codex app-server test fixture also polled for each approval response. It now registers a waiter before sending
and wakes it from the existing JSON-RPC reader, matched by request ID. Its five-second budget and all security
classifications remain unchanged. Cancellation, deadlines, EOF, write failures, unrelated replies and JSON-RPC errors
are exercised through real pipes and held node/hub approvals. The fixture reader is joined at cleanup.

The same validation exposed a race in `TestCodexStartAndTurns`: it reassigned the node directory while an earlier
turn-completed notification was still listing sessions. Fixed main reproduces the identical write/read race.
Configuring the test directory before starting its transport preserves the image/turn assertions and removes that race.

This reuses the partition runner already merged in #5615/#5675; those changes are not part of this delivery. Direct `go test`
still does not select partitions automatically. See `docs/operations/testing.md` ("Bounded server package verification") for adoption.

## Before/after measurements

The first completed comparison below uses the initial `522192ca9a5e` implementation; it is retained after the Codex correction.

| Measurement | Main baseline | Implementation | Outcome |
| --- | --- | --- | --- |
| Cold eight-package test compilation | 46.153 s | 47.022 s | Both exit 0; no compilation speedup |
| Five-package direct command, warm cache | 600.108 s | 600.118 s | Both outer timeout, exit 124 |
| Eight-package direct command, warm cache | 600.107 s | 600.108 s | Both outer timeout, exit 124 |

The direct calls remain over the external budget. They must not be reported as passing. Their supervisor cleanup
took 0.089–0.090 s, with zero remaining owned processes; compilation and supervisor cleanup do not explain the
600-second execution limit. The head's completed package summaries include assistant at 210.906/210.832 s and
hub at 133.337/132.411 s in the five/eight-package calls respectively; node has no package terminal before termination.
Normal Go output does not provide a complete test-terminal inventory for a terminated package.

The separate warm compilation preparations took 3.364 s (base) and 28.981 s (head). These are preparation costs,
excluded from the execution comparison. A source change invalidates its own build artifacts even when dependencies
are already cached; the cold-cache comparison above measures that separately.

The initial complete eight-package partitioned comparison took **439.601 s → 322.021 s** (26.7% lower wall time),
both exit 1, with the same single Chrome failure. The 7,282 common roots consumed 1,125.060 s → 971.600 s summed
elapsed time, saving 153.460 s. Node workers ended in 348.859/435.917 s before and 313.027/318.405 s after.
These overlapping worker times must not be added to the package-set wall time. Adding regressions reshuffles the
existing round-robin partitions, so common-root elapsed is also reported to avoid crediting scheduling luck.

The measured `be189594939b` source's cold eight-package compilation took 45.541 s (baseline 46.153 s); warm preparations took
3.292 s before and 25.334 s after. There is no meaningful cold-compilation improvement to attribute to fixture
changes. These preparations are excluded from test execution comparisons.

Largest observed reductions in the initial comparison (top-level elapsed includes fixture cleanup):

| Test | Baseline | Initial head | Saved |
| --- | --- | --- | --- |
| `TestStartSubmitsImageAndLongPaste` | 23.970 s | 4.330 s | 19.640 s |
| `TestEngineSwitchHoldsTheEarlierSessionWhileItResumesIt` | 10.680 s | 3.350 s | 7.330 s |
| `TestEngineSwitchFallsBackToAFreshSession` | 6.150 s | 1.370 s | 4.780 s |
| `TestEngineSwitchLooksAgainBeforeTheQuit` | 4.780 s | 0.280 s | 4.500 s |
| `TestEngineSwitchLeavesAnEarlierSessionBeingResumed` | 4.780 s | 0.280 s | 4.500 s |
| `TestEngineSwitchBackResumesTheEarlierSession` | 4.840 s | 0.350 s | 4.490 s |

At the final source `be189594939b`, the original five-package command still times out at **600.084 s**, and the
eight-package command at **600.083 s** (both exit 124). Supervisor cleanup is 0.068/0.067 s respectively, with zero
remaining owned processes. These outcomes remain failures to meet the original direct-command budget.

The final complete partitioned comparison is **439.601 s → 356.385 s**, saving **83.216 s (18.9%)** in observed
wall time. Both exit 1. Common-root elapsed falls **1,125.060 s → 954.020 s**, saving **171.040 s**. This reduces
common test cost more than the initial version, but its additional three regression roots reshuffle the existing
round-robin assignments: final node workers take 261.841/352.818 s, compared with the initial head's
313.027/318.405 s. The later observed wall time, rather than the faster initial trial, is the full-suite measurement at `be189594939b`.

Warm time before the first JSON event is 2.456/2.410 s before/after. Final node workers spend 0.027–0.028 s
between process start and the first test, and 0.009–0.010 s after the last test terminal. Baseline workers spend
0.028–0.029 s and 0.010–0.012 s respectively. Supervisor cleanup is 0.026 s for both, with zero remaining owned
processes. Fixture teardown occurs inside each test's elapsed time; these phases separate it from process cleanup.

The original commands, without diagnostic or internal-timeout flags, are:

```sh
go -C server test -count=1 \
  ./internal/assistant ./internal/assistantskills ./internal/assistantwire ./internal/hub ./internal/node
go -C server test -count=1 \
  ./internal/apierr ./internal/assistant ./internal/assistantskills ./internal/assistantwire \
  ./internal/hub ./internal/i18n ./internal/node ./internal/prompts
```

Complete event accounting uses the existing runner separately, from `server/`, under the same 600-second outer limit:

```sh
python3 ../scripts/test/go_test_partitioned.py go test -json -count=1 \
  ./internal/apierr ./internal/assistant ./internal/assistantskills ./internal/assistantwire \
  ./internal/hub ./internal/i18n ./internal/node ./internal/prompts
```

That diagnostic adds `-json` and the runner's discovered node `-run` patterns. It is not an original direct command, and a
completed partitioned run is not a direct-command pass. Parent/subtest durations overlap; summed elapsed time uses top-level tests only.

## Additional response-event optimization

This follow-up is test-only. Its source commit is `be8e80bbdb6d3452621be9227fe8a11e26167a07`. The completed main full-suite measurements
above remain attributed to `be189594939b`; they are not relabelled as measurements of the later source. Completed
five/eight-package commands were not repeated. The following comparison selects the entire existing security matrix:

```sh
go -C server test -json -count=1 -run '^TestAssistantBypassShellNames$' ./internal/node
```

| Warm-cache measurement | `be189594939b` | Reply-event fixture | Result |
| --- | --- | --- | --- |
| Command wall time | 26.684 s | 3.442 s | Both exit 0; 23.242 s (87.1%) saved |
| Root test elapsed | 24.930 s | 1.670 s | 23.260 s (93.3%) saved |
| Terminal identities, including nested cases | 3,517 pass | 3,517 pass | Zero missing, added or changed states |

The matrix still evaluates all 1,172 commands through both approval adapters. The observed saving matches the
removed 20 ms polling tick per Codex response; no command, expected decision or phone-interaction assertion changes.
Before the fix, a real held approval response advanced the virtual clock by 20 ms and failed
`TestFakeCodexRequestWakesOnItsReply`. Afterward it wakes without advancing time. Preparation/compilation is excluded
from the matched warm-cache comparison; first green compilation is recorded separately in the receipt artifact.

Four new regression roots passed 20 repetitions (0.125 s execution, 1.788 s wall). All 44 roots in the five existing
fixture-consumer files plus those four regressions passed three repetitions: 48 roots and 4,577 identities, exactly
three pass terminals each (13,731 events; 17.723 s wall). Filtering the earlier full `be189` JSON to those 44 existing
roots also reconciles all 4,568 common identities to identical states in each repetition, with no missing or added
identity inside an existing root.

The first consumer race run failed (65.867 s wall, 35.567 s execution) at the concurrent directory reassignment
in `TestCodexStartAndTurns`. The identical isolated `-race -count=20` command reproduces that race on fixed main
(3.290 s wall, exit 1). Initializing the directory before transport startup then passes the same 20 repetitions
(32.358 s wall including recompilation, 2.059 s execution) and the complete 48-root consumer selection with
`-race -count=3` (38.508 s wall, 36.292 s execution, exit 0). The broader `codexWaitTick` race described below remains
an independent retained failure. Neither race is attributed to the external plain-test FAIL reports.

## Terminal-event reconciliation

The baseline partitioned run completed all 7,282 inventoried top-level tests exactly once: 6,889 passed,
392 retained existing skips, and one failed (`TestPreviewDrivesARealBrowser`: Chrome closed before answering).
Including subtests, it recorded 16,948 terminal identities (16,352 pass, 595 skip, one fail), with zero duplicate
run/terminal events and zero unfinished tests. No inventoried root was missing or unexpected.

The initial head has all 16,948 common terminal identities with identical pass/fail/skip states, plus nine
terminal identities belonging to its six added regression roots. There are zero removed identities, changed
states, duplicate runs/terminals, unfinished tests, or missing/unexpected inventoried roots.

The final inventory contains 7,291 roots: all 7,282 baseline roots plus nine regressions, with no deletion.
The final JSON has 6,898 passed roots, 392 existing skipped roots and the same single browser failure. Including
subtests it has 16,368 pass, 595 skip and one fail (16,964 identities): all 16,948 common identities preserve their
terminal state, and 16 added identities belong to the nine regressions. There are zero missing roots, unexpected
roots, duplicate run/terminal events, unfinished identities, removed identities or changed common states.

The accompanying [root terminal table](2026-11-12-server-test-latency-5730.tsv) lists every inventoried root in
the baseline, measured `be189` main source and both patched feature snapshots. `absent` means that snapshot lacks
the root, `not_selected` means its package is outside that command, and `no_terminal` means an inventoried root did
not complete. An elapsed value of `n/a` means that main snapshot has no measured terminal for the root.
The main optimized column explicitly names `be189`; it does not represent a new full-main run at
the later reply-event source. Dynamic subtest names and raw output are withheld. Full private nested manifests
(`.terminals.json` schema) have SHA-256 `76f4517c1d6e95b88b5026ae5f51415e58a4910a569793cf89c12f9a855d6a06`
for main and `7d78d5591b72bbb315d9ea54ef1b98890efc48ab578b32c95eadb789bb5333b7` for `be189`.

[Derived receipts](2026-11-12-server-test-latency-5730-results.json) retain exact argv, source revisions, exit status,
wall time, load samples and owned-process cleanup counts. Checkout/output paths use explicit placeholders. They
include the 48-root normal/race selection, failed race reproductions and the first unsuccessful feature-runner
invocations, so those failures cannot be mistaken for completed feature suites.

## Feature-head integration

Both reported heads were exported independently, without modifying their branches. The startup/teardown patch applies cleanly.
The Claude readiness patch needs context-only adaptation in `waitReady`: those snapshots predate main's additional pane-disappearance
handling beside the polling select. The same polling seam is applied without porting or changing that existing pane handling:

- #5473: `cb2ba1521e01ec956dd367d3f84e87a01a5ee677`.
- #5581: `cc575818ac55207696518a2674c3fc5b0505d7ab`.

The subsequent shared Codex polling patch and the test-only reply-event patch `be8e80bbdb6d` apply cleanly to both
patched exports. Older snapshots lack main's later `fakeHub.onContext` seam; that unrelated version difference is
preserved. The helper implementation and new regression file match the optimization source.

Both feature snapshots predate the existing repository partition entrypoint. Initial relative-path invocations
returned exit 2 before tests (0.037/0.036 s); those receipts provide no complete-suite credit. The corrected invocation
runs the already merged fixed-main runner by absolute path from each feature's `server/` directory. It discovers and
executes that feature snapshot's own tests, without copying or redesigning the scheduler:

```sh
# Set RUNNER_CHECKOUT to the fixed-main runner checkout.
# Run from the isolated feature export's server directory.
python3 "$RUNNER_CHECKOUT/scripts/test/go_test_partitioned.py" go test -json -count=1 \
  ./internal/apierr ./internal/assistant ./internal/assistantskills ./internal/assistantwire \
  ./internal/hub ./internal/i18n ./internal/node ./internal/prompts
# For #5581, select assistant, assistantskills, assistantwire, hub and node instead.
```

The runner checkout is the frozen `be189594939b` main-based source; its existing scheduling code is unchanged from
the fixed baseline. Each invocation retains the 600-second outer deadline. These are the first actual partitioned
executions for the exported feature sources; their earlier completed direct calls are not repeated.

| Export and selected set | Earlier direct call with `be189` patches | Complete partitioned call with `be8e` patches | Root terminals |
| --- | --- | --- | --- |
| #5473, eight packages | 600.122 s, exit 124 (outer timeout) | 300.666 s, exit 1 (failure, no outer timeout) | 6,866 pass / 392 existing skip / 1 fail = 7,259 |
| #5581, five packages | 600.121 s, exit 124 (outer timeout) | 312.675 s, exit 1 (failure, no outer timeout) | 6,797 pass / 392 existing skip / 1 fail = 7,190 |

Both corrected runs have zero missing/unexpected inventoried roots, duplicate run/terminal events, unfinished
identities or remaining owned processes. Their private audits include 16,897 and 16,845 nested identities respectively.
The sole failure is `TestPreviewDrivesARealBrowser` at `preview_live_test.go:149`, with the same Chrome-closed assertion
seen on fixed main (1.620 s in #5473, 0.070 s in #5581). No different failure is hidden behind the completed runner.
These exports have no complete unpatched same-feature timing sample; comparison to main is not a feature speedup claim.
Their earlier direct invocations use the preceding implementation and are not measurements of `be8e`.

Relative to each feature's earlier patched inventory, the reply-event change adds exactly four roots and deletes none:
7,255 → 7,259 for #5473 and 7,186 → 7,190 for #5581. The combined public root table contains 7,334 distinct roots across
all four source inventories, SHA-256 `d0e0ad8110e211473cebf820fa840dc734c6ae9f720de055237294c759c3c764`.
This union includes version-specific tests; an older snapshot's `absent` entries are not deleted tests in this PR.

Their source and test inventories differ from the fixed current-main baseline. Results are kept separate, with the feature
SHA and optimization patch identified; they do not resolve #5473's merge conflict or establish either feature as fully green.

## Regression and repository checks

- Before the fix, `TestHerdrFixtureCancelsUnfinishedPromptChecks` failed after 3.41 s because teardown waited for a recovery
  failure instead of cancelling the check. `TestFixtureShellIsAlreadySettled` failed because its virtual clock advanced 300 ms.
- The initial six fixture/lifetime/readiness regressions passed 20 repetitions (0.902 s test execution). The detached-lifetime regression
  confirms that a normal check can receive its transcript receipt after its submitting request is cancelled.
- Before the Codex correction, three additional regressions failed at the expected 500 ms fixture poll, including
  matching-rollout success, launcher failure and the fixture deadline. All nine regressions then passed 20 repetitions
  (0.956 s test execution). Cancellation still ends the pending poll without reading the host, and a normal node
  still waits 500 ms. The success case reads real rollout files and rejects a different first message.
- Initial risk-focused `-race -count=3` passed: the six regressions, dropped-message recovery, retained/never-delivered prompts,
  image/long-paste submission, unrelated drafts/dialogs, startup questions, real shell settling, unknown engine state and a disappeared Orca pane (68.491 s test execution).
- `go -C server build ./...`, `go -C server vet ./...`, the Windows/amd64 build, and the full `herdrlive` node test compile passed.
  Live agents were not started. The repo-wide lazy-regexp, architecture, timezone and plugin guards passed.
- Existing partition discovery (6 tests), partition runner (9 tests), and isolated server launcher/environment (6 tests) passed.
  The sleep ratchet passed without baseline changes. No shell or workflow file changed; additional shell/workflow lint is not applicable.
- The expanded Codex `-race -count=3` run exited 1 at `TestCodexStartWithoutAMessage`: restoring
  `codexWaitTick` in `withCodexWaits` races with the detached `lookForCodexThreads` loop. Neither waiting-path file
  changes in this PR. The identical isolated command below also failed with the same race on fixed main
  (46.078 s wall, including race compilation) and final head (5.095 s wall). This is retained as an existing failure;
  a passing focused race run does not establish that the broader race suite is green.

  ```sh
  go -C server test -race -count=20 -run '^TestCodexStartWithoutAMessage$' ./internal/node
  ```

- Final focused `-race -count=3` passed (68.558 s test execution, 70.808 s wall). Its exact selection covers the
  nine new regressions, real Codex start/permission/launcher/trust paths, concurrent starts, stalled-start failure,
  cancellation and abandoned-pane cleanup/retry. The preceding broad failure and both baseline/head race
  reproductions remain above; no test or failure was removed from the normal suite.
- Final build, vet, Windows/amd64 build, complete `herdrlive` test compile and the four repository guard packages
  passed together (55.179 s wall) at `be189594939b`.
- Final document-path, Go-path, section-reference and wire-document checks are recorded in the derived receipts.
  Privacy checks cover tracked/staged/range contents and commit/reply text before publication. Only two private
  values are available from local configuration; a clean scan is bounded by that available configuration. Public
  artifacts contain derived measurements and static root names, with private checkout paths replaced by placeholders.

The local `scripts/ci/server.sh node` failed identically on baseline and implementation at the Postgres 18 fixture (exit 1,
Docker socket permission denied); tests did not start through that lane. No permission or global configuration was changed.
A manual Server CI dispatch on the authorized fork returned HTTP 404: its default branch has no registered workflows.
No hosted CI result is claimed, and that default branch was not changed. A maintainer must provide an available CI ref/environment.

Remote automated verifier comments on [PR #5845](https://github.com/nwverse/persona/pull/5845) report node-package
failures at 540.403 s for `be1895949`, 539.305 s for the documentation-only `98e5d33bd`, and 504.553 s for
the reply-event implementation `be8e80bbd` (2026-11-12T15:37:35Z). All three report package FAIL, not timeout.
The complete comment bodies supply only a short output tail, without named failing assertions, the full command or
environment. Ordinary refusal/resume/close messages in that tail are not identified failed assertions. The package
elapsed values do not establish that the complete external verifier met its deadline or passed, and do not attribute
its failure to the local Chrome result.

The first two SHAs were checked through GitHub's check-runs, check-suites, combined commit-status and Actions-run APIs:
all contain zero associated entries; the combined status is `pending` with an empty statuses array. PR reviews and
inline review comments are empty. Recent repository Actions runs precede this PR and use other SHAs. The repository
contains no matching automated-verifier implementation or full-log link. Issue #5730 contains only its original claim.
Consequently no complete watcher log or named remote failure is available through these public sources. The new `be8e80bbd` comment likewise contains only the short normal-log tail and package FAIL, with no named
failed assertion. Its lower package elapsed time is not a controlled before/after sample or proof that the complete
external verifier met its deadline. The detailed external failure cause remains unknown; no additional logs are requested or awaited.
Local failures and measured hotspots are investigated independently. Local direct calls at `be189594939b` still time out as recorded above.

- [Verifier report, 2026-11-12T14:42:57Z](https://github.com/nwverse/persona/pull/5845#issuecomment-6826718109).
- [Verifier report, 2026-11-12T14:52:24Z](https://github.com/nwverse/persona/pull/5845#issuecomment-6826959409).
- [Verifier report at the reply-event source, 2026-11-12T15:37:35Z](https://github.com/nwverse/persona/pull/5845#issuecomment-6828106659).

No UI or native code changed: screenshots, native builds and online deployment verification are not applicable. Nothing was merged or deployed.
