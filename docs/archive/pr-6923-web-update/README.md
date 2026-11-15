# Web development update evidence for PR #6923

This change serves operators of the Web development UI in Settings > Hub and its
Hub/Node backend update chain. It retains the shared installer and three owned,
verified recovery snapshots. Native macOS, iOS, Android and other client adaptation,
packaging and acceptance are outside this change. A macOS Hub host or a computer's
service binary is a backend target, distinct from a native client.

## Visual evidence

These are real Chromium captures of the repository's full React App and HubSettings,
served by Vite against a loopback-only **synthetic API fixture**. They contain made-up
commits, computer/session names, status messages and history. The fixture records
requests; it never builds, installs, restarts a service or restores a database.
Its recovery messages demonstrate rendering, not successful recovery of a real Hub.
Backend recovery evidence is recorded separately below.

Environment: Linux amd64, Node 24.20.0, Chromium 149.0.7827.55, UTC, English,
device scale factor 1, reduced motion; 1440 × 1000 and 390 × 844 CSS pixels.
Both light and dark are asserted from the rendered root class. Standard and the
Web app's largest supported text setting, **Large (`14 / 13`)**, are asserted from
`data-text-size`. This is not an iOS accessibility-size or native macOS claim.
Browser clock is fixed to keep the synthetic dates stable.

The capture manifest identifies the source revision, Web source tree, settings
source checksum and every image checksum. The PR follow-up binds the final delivery
head and its fresh rerun to this same renderer. All 29 images were inspected by the
authoring agent; a person's visual approval is still requested before merge.

| Viewport | Scheme | Text | Source / dated commits | Automation / history |
| --- | --- | --- | --- | --- |
| Wide | Light | Standard | [Image](1440-light-standard-source.png) | [Image](1440-light-standard-options.png) |
| Wide | Light | Large | [Image](1440-light-large-source.png) | [Image](1440-light-large-options.png) |
| Wide | Dark | Standard | [Image](1440-dark-standard-source.png) | [Image](1440-dark-standard-options.png) |
| Wide | Dark | Large | [Image](1440-dark-large-source.png) | [Image](1440-dark-large-options.png) |
| Narrow | Light | Standard | [Image](390-light-standard-source.png) | [Image](390-light-standard-options.png) |
| Narrow | Light | Large | [Image](390-light-large-source.png) | [Image](390-light-large-options.png) |
| Narrow | Dark | Standard | [Image](390-dark-standard-source.png) | [Image](390-dark-standard-options.png) |
| Narrow | Dark | Large | [Image](390-dark-large-source.png) | [Image](390-dark-large-options.png) |

| State or interaction | Screenshot |
| --- | --- |
| Explicit automation opt-in | [Enabled](automatic-enabled.png) |
| Source change disables automation; release hides saved versions | [Release](release-source.png) |
| Program-only backup confirmation; cancel sends no apply request | [Wide](backup-confirm.png), [narrow dark Large](backup-confirm-narrow-dark-large.png) |
| Confirmed saved version waits for active sessions; source/selection disabled | [Waiting](backup-waiting.png) |
| Update now warns about interruption; cancel sends no forced request | [Confirmation](interrupt-confirm.png) |
| Missing writer coordination disables development selection | [Setup](setup-large.png) |
| Compilation and backup phases disable duplicate actions | [Building](building-large.png), [backing up](backing_up-large.png) |
| Recovery failure explanation | [Error](failed-large.png) |
| Previous programs restored, current data retained | [Rollback](rolled_back-large.png), [history](rollback-history-large.png) |
| Inventory failure offers Retry | [Backup list error](backup-error-large.png) |

The browser assertions verify dated hashes, automation off by default, opt-in,
source changes, hiding backup choices on release, backup cancel/confirm, disabled
controls while waiting, and Update now cancel/confirm with exactly one accepted
request each. They also check the actual scheme/text setting, page overflow and
uncaught browser errors. The browser check initially failed because a long version
squeezed the Version label at narrow Large text. Reusing Row's `stackOnNarrow` fixes
that regression without changing the desktop row. All buttons remain one line;
long native select values and history summaries use their existing truncation.

Reproduce with installed Web dependencies and an existing Chromium executable:

```sh
# Terminal 1, repository root. This process only serves synthetic data.
PORT=5996 node web/e2e/hub-development-fixture.mjs
# Terminal 2, repository root. The browser run has a five-minute deadline.
node web/e2e/hub-development.mjs http://127.0.0.1:5996 <artifact-directory> --executable <chromium>
# Stop only the fixture process started above when finished.
```

The driver refuses a non-loopback origin or a service missing the fixture marker,
blocks external page requests and cleans its owned browser context. No dependency
installation, production account, data or model is needed for these interactions.

## Focused recovery re-review

The three original findings were reread in full, then traced through the current
installer, helper, journal, boot, restoration, pruning and retry callers. This is
an **author review with isolated fault injection**, not independent reviewer approval.
No new backend defect was established in this bounded re-review. Existing corrections
were retained and rerun, rather than inferred to pass from their earlier results.

| Finding | Current mechanism | Isolated evidence |
| --- | --- | --- |
| [Crash before pending](https://github.com/nwverse/persona/pull/6923#discussion_r4575810562) | `PrepareDevelopmentAdoption` saves all program preimages before replacement. `DetachDevelopmentWeb` saves Web/WebPrev intent before rename. `boot` can restore a RecoveryReady fence without pending under either old or attempted build. | `TestDevelopmentCrashRecovery`: a separate process exits abruptly at prepared, hub, programs, Web intent and Web rename, each under both identities (10 cases); repeated restoration preserves the current history. `TestDevelopmentWebRecoveryPreservesConflictingOverride` retains both directories on conflict. |
| [Cancellation and writers](https://github.com/nwverse/persona/pull/6923#discussion_r4575810565) | SIGINT/SIGTERM unwind to bounded cleanup. Children receive group-wide INT, TERM, KILL escalation (2 seconds per phase). Repeated cancellation is deferred during cleanup/resume; resume has its own 60-second window. Go's WaitDelay is 90 seconds. | `test_cancellation_resumes_writers_and_reaps_child_group`: real helper subprocesses during quiesce, dump, resume, failed resume and stubborn dump. Checks repeated signals, graceful interruption, no surviving child group in these cancellation cases, resumed writers, or retained marker on failed resume, and refusal to retry incomplete work. |
| [Partial replacement cleanup](https://github.com/nwverse/persona/pull/6923#discussion_r4575810568) | All preimages precede live development replacements. Complete restore precedes fence/staging cleanup; failed restore, corrupt/unknown fence and refused retry retain evidence. The Hub preimage is restored independently of an empty companion Swapped record. | `TestDevelopmentUsesSharedInstallAndPreservesRecovery` exercises preimage, partial replacement and standby/restore failure, retained pending/fence/programs/Nodes and repaired-preimage retry. `TestDevelopmentFailedRestoreRetainsRecovery` adds pruning/refused-retry protection; `TestDevelopmentUnknownRecoveryIsRetained` retains unknown journals. `TestAFailedSwapLeavesTheProgram` checks the empty companion premise. |

Locatable code and tests:

- [Development journal/helper bridge](../../../server/internal/update/development.go): `PrepareDevelopmentAdoption`, `DetachDevelopmentWeb`, `DevelopmentRequest`.
- [Shared restore](../../../server/internal/update/install.go): `RestoreAll`; [helper](../../../server/internal/update/webdev.py): `interrupt`, `defer_cancellation`, `run`, `snapshot`.
- [Adoption and cleanup](../../../server/internal/hub/selfupdate_install.go): `installCandidate`, `pruneDownloads`; [startup](../../../server/internal/hub/selfupdate.go): `boot`, `rollback`.
- [Abrupt-exit/retention tests](../../../server/internal/hub/selfupdate_recovery_test.go), [shared-installer tests](../../../server/internal/hub/selfupdate_development_test.go), [program tests](../../../server/internal/update/programs_test.go), [Web conflict test](../../../server/internal/update/development_test.go).
- [Real helper/coordination fixtures](../../../scripts/test/test_web_development_update.py): cancellation, real synthetic writer, three-snapshot rotation, failed resume and program-only saved-version preparation.

## Commands and results

These commands passed in this follow-up. The PR reply records the exact final head
and final rerun; an empty hosted check list is not a passing CI run.

```sh
cd server
go test -v ./internal/hub ./internal/update -run 'Test(Development|AFailedSwap|SwapProgram)' -count=1 -timeout=3m
go test -race ./internal/hub ./internal/update -run 'Test(Development|AFailedSwap|SwapProgram)' -count=1 -timeout=3m
# Both packages: PASS, including all 10 abrupt-exit cases and failed-restore/retry cases.
# Tagged native Linux compile only, no execution of these programs:
go build -tags "$(cat build-tags.txt)" -trimpath -o <output>/personad ./cmd/personad
go build -tags "$(cat build-tags.txt)" -trimpath -o <output>/persona-hub ./cmd/persona-hub
go build -tags "$(cat build-tags.txt)" -trimpath -o <output>/persona-manager ./cmd/persona-manager
cd ..
python3 -B -m unittest -v scripts/test/test_web_development_update.py
# Ran 8 tests: OK (the cancellation test contains five real subprocess scenarios).
cd web
npm run build:development
npx vitest run src/views/settings/HubSettings.development.test.tsx src/views/settings/HubSettings.sourcebehind.test.tsx src/lib/hub-settings.test.ts
# Build: PASS; 3 files / 24 tests: PASS.
cd ..
python3 scripts/test/test_doc_paths.py
python3 scripts/test/test_doc_sections.py
python3 scripts/privacy_check.py --range origin/main..HEAD
git diff --check
# Docs: 12 + 4 tests OK; privacy and whitespace: clean.
```

Toolchains: Go 1.27.3 linux/amd64 and Python 3.12.3, plus the browser environment above.
The test build/download/database tools and service handover are synthetic boundaries;
this does not claim a real PostgreSQL restore or an installed-fleet acceptance test.

## Remaining acceptance boundaries

- Normal updates wait on the shared idle check; it is not an admission lock against
  new turns. Update now explicitly can interrupt sessions. No uninterrupted-session
  guarantee is added.
- Applying a backup restores programs and embedded Web only; it preserves current
  database/configuration/history and does not undo schema migrations. An incompatible
  older program or failed startup still needs the existing confirmation/rollback path.
- SIGKILL cannot run a resume hook. Paused writers, a failed resume, incomplete snapshots,
  conflicting Web overrides, corrupt or legacy/unready journals require operator
  inspection. Those markers must not be deleted blindly to force a retry.
- Abrupt process exits do not establish power-loss or filesystem-I/O-failure durability;
  shared directory syncing retains its existing best-effort contract. No such fault
  or native macOS host execution was performed.
- Hook correctness for every real database/file writer, real deployment, real-data
  restoration, production latency and native client acceptance remain unverified.
  No real services, private configuration/history, database restoration or model calls
  were used. No release or deployment was performed.
- The screenshots and targeted tests are evidence for a final-head review. The author
  does not mark the three P1 discussions approved or resolved. Maintainer review and
  visual sign-off remain required before merge.
