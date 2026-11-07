# Apple tab-switch measurements and retained-sync follow-up (#739, #1159)

Date: 2026-11-08. **Acceptance remains open.** This report contains synthetic-fixture evidence, not production data. Original comparison sources were not changed to conceal the hidden HTTP synchronization failure.

## Sources and measurement boundaries

- Before: `34f263fad99e886e54de0f05beac37411ec01b14`.
- After: `abfae41b0f5b2ec764219c10591e31088a158383`.
- Both: Xcode 27.1, optimized Debug (`-O`, whole-module), identical temporary app-side probe, stock iOS 27.0 / iPhone 17 Pro simulator. **Xcode 26.0 is not installed locally and is not verified here.**
- Same running 8,356-row synthetic fixture: SHA-256 `4f4fe5555a0654bd6f4108b418b4286dd1c92efb4c773783fbc960ca1e3d060d`.
- Each source: 120 samples, ten cold and twenty warm for each direction and quiet/continuously streaming condition. The cold destination had not been built; both warm pages were retained. Streaming continued throughout the measured transitions.
- Start: the real shell selection handler. First callback: first observed CADisplayLink callback. Settled: actual pager animation completion. These are **not physical touch-to-first-presented-pixel timings or GPU FPS**. Script sleeps, accessibility polling and screenshot waits are outside these intervals.
- When a blocked transition settled before its first callback, the probe froze the settled boundary and waited for the actual callback. Slow samples were retained, not replaced with zeros or discarded.
- Quantiles use nearest rank. No confidence interval or claim of statistically established speedup is made.

The full numeric aggregates are retained outside the checkout for PR attachment. Raw runloop-busy fields are deliberately excluded: the existing instrument did not reset `passStart` between windows, producing some values longer than the whole transition. Those fields cannot establish in-window CPU duration.

## First callback and settled time

Milliseconds, p50 / p95. A→G is Assistant→Agents; G→A is Agents→Assistant.

| Condition | Lifecycle | Direction | n/source | First callback before | First callback after | Settled before | Settled after |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| Quiet | Cold | A→G | 10 | 2970.6 / 3091.4 | 2988.6 / 3159.6 | 3500.8 / 3617.4 | 3517.1 / 3694.6 |
| Quiet | Cold | G→A | 10 | 1469.5 / 1731.4 | 993.5 / 1950.6 | 2428.4 / 2568.2 | 2482.6 / 2741.3 |
| Quiet | Warm | A→G | 20 | 915.6 / 973.4 | 939.5 / 971.2 | 1433.6 / 1486.9 | 1459.4 / 1489.5 |
| Quiet | Warm | G→A | 20 | 545.7 / 578.2 | 553.1 / 566.7 | 1069.6 / 1106.9 | 1068.0 / 1101.1 |
| Streaming | Cold | A→G | 10 | 3114.4 / 3874.1 | 3074.8 / 3436.9 | 3725.3 / 3945.2 | 3661.6 / 3890.2 |
| Streaming | Cold | G→A | 10 | 1673.9 / 1754.9 | 1666.6 / 1753.2 | 2511.4 / 2552.4 | 2500.8 / 2823.9 |
| Streaming | Warm | A→G | 20 | 979.0 / 1027.1 | 983.9 / 1047.7 | 1506.2 / 1560.9 | 1499.1 / 1589.0 |
| Streaming | Warm | G→A | 20 | 592.2 / 614.4 | 565.6 / 602.1 | 1122.3 / 1167.1 | 1119.0 / 1142.6 |

**These samples do not establish a material overall improvement in settling time.**

## Observed cadence and coverage

Cadence is the interval between display callbacks, not a complete presented-frame measurement. Windows with fewer than two ticks are unavailable, not zero. The maximum below is conditional on available windows. `Activation/cadence gap` is the larger of the first-callback delay and observed cadence gap; it is not GPU frame duration.

| Condition / lifecycle / direction | Cadence available before / after | Max cadence before / after (ms) | Max activation/cadence gap before / after (ms) |
| --- | ---: | ---: | ---: |
| Quiet / cold / A→G | 10/10 / 10/10 | 451.2 / 459.5 | 3091.4 / 3159.6 |
| Quiet / cold / G→A | 10/10 / 10/10 | 1920.1 / 1892.8 | 1920.1 / 1950.6 |
| Quiet / warm / A→G | 20/20 / 20/20 | 61.3 / 45.3 | 979.1 / 978.6 |
| Quiet / warm / G→A | 20/20 / 20/20 | 600.3 / 56.2 | 600.3 / 576.5 |
| Streaming / cold / A→G | 2/10 / 5/10 | 526.8 / 3100.3 | 3874.1 / 3436.9 |
| Streaming / cold / G→A | 10/10 / 10/10 | 1763.8 / 1959.4 | 1763.8 / 1959.4 |
| Streaming / warm / A→G | 20/20 / 20/20 | 591.3 / 624.5 | 1104.4 / 1122.3 |
| Streaming / warm / G→A | 20/20 / 20/20 | 595.4 / 619.2 | 624.9 / 619.2 |

## Exact observed response payloads

These totals cover each group's measurement observation windows. HTTP counts are **completed responses**, not every request start. Byte counts are encoded JSON bodies; headers, TLS and compression are excluded. Different callback/settling windows can encompass different traffic; these are observations, not a normalized bandwidth estimate.

| Condition / lifecycle / direction | HTTP completed before / after | HTTP body bytes before / after | WS messages before / after | WS JSON bytes before / after |
| --- | ---: | ---: | ---: | ---: |
| Quiet / cold / A→G | 40 / 40 | 66,337,900 / 66,337,900 | 0 / 0 | 0 / 0 |
| Quiet / cold / G→A | 63 / 60 | 91,168 / 88,030 | 10 / 10 | 1,121,610 / 1,121,610 |
| Quiet / warm / A→G | 60 / 60 | 3,120 / 3,120 | 0 / 0 | 0 / 0 |
| Quiet / warm / G→A | 1 / 1 | 43 / 43 | 0 / 20 | 0 / 3,600 |
| Streaming / cold / A→G | 36 / 40 | 46,436,988 / 66,337,960 | 418 / 156 | 1,154,020 / 430,641 |
| Streaming / cold / G→A | 60 / 60 | 88,090 / 88,090 | 200 / 201 | 1,646,200 / 1,648,961 |
| Streaming / warm / A→G | 60 / 60 | 3,120 / 3,120 | 351 / 99 | 971,524 / 274,021 |
| Streaming / warm / G→A | 1 / 1 | 43 / 43 | 263 / 269 | 727,958 / 692,807 |

## Hidden transcript and actual replay failure

The same natural 60-update Assistant stream, including final stream-clear delta, ran while that chat was hidden. Both transports remained online on one unchanged connection, with no reconnect.

| Observation | Before | After |
| --- | ---: | ---: |
| Target WS deltas | 61 | 0 |
| Hidden StreamingText body evaluations | 24 | 0 |
| Hidden ChatLiveText body evaluations | 25 | 0 |
| Marked seen while hidden | Yes | No |
| Server unread while hidden | No: hidden seen | Yes |
| Seen after return | Not a new visibility-owned transition | Yes |
| Missed items caught up through return since_seq | No | No |

The fixed source performed hidden HTTP `items?after_seq=1254&lazy_images=1`, applied new items 1255/1256 into its retained store and advanced coverage to 1256. Return subscribed with `since_seq=1256` and an empty backlog. Hidden `AssistantText` evaluated once and `ChatView` four times. This is the real #1159 gap, **not** a duplicate-sequence fixture bug and **not** successful missed-sequence replay. Before, the chat simply stayed subscribed and read while hidden.

The independent follow-up in PR #1192 defers retained hidden stores and fences asynchronous fill/reload responses. Its local 117 PersonaKit tests and eight targeted tests passed, including both real-app hidden HTTP/WS regressions and the original visible stalled-stream recovery. The fresh large-fixture follow-up observation remains pending; those results must not replace the original before/after data.

## Main-thread stacks: heavy work remains

Real Time Profiler captures were recorded and exported, not inferred from source or trace-directory existence. Values below are inclusive **sample weights**, not stopwatch CPU durations. Categories overlap and cannot be added together.

| Capture | Main-thread samples | Agent-list weight (ms) | Chat-render weight (ms) | Chat-store weight (ms) |
| --- | ---: | ---: | ---: | ---: |
| Before: hidden stream completion, 20 s | 16,702 | 891 | 17 | 3 |
| After: hidden stream completion, 20 s | 16,100 | 700 | 1 | 1 |
| After: alternating tabs, 20 s | 17,929 | 4,460 | 17 | 2 |

Named after-alternation stacks include `AgentsList.body.getter` (2,092 ms), `AgentsList.repoSection(_:)` (1,170 ms), `RepoTree.build(_:machines:)` (1,012 ms), and list ordering/grouping work. Accessibility/runtime control accounts for 638 ms of inclusive weight in that capture. A claim of **no heavy main-thread work** would be incorrect.

The matched before-alternation recording timed out and its partial trace could not export a Time Profiler table. It is not presented as a successful symmetric capture. Raw traces/XML are not published because they include environment metadata; only anonymous function/category summaries belong in public evidence.

## Screenshots actually inspected

Light is on the left and dark on the right in each source-labeled pair. These are 24 synthetic simulator screenshots presented in twelve readable pairs; PNG metadata was removed without changing RGB pixels. System appearance was used instead of triggering alternate-icon confirmation. Earlier overlay-obstructed frames are excluded. The media stays outside the checkout, as required by the archive policy; **public PR attachment links remain pending**.

These snapshots document the existing styles and visible content, **not pixel-identical layout parity or a complete visual acceptance pass**. Chat scroll/header states can differ between captures; one before BeautifulUI dark frame includes overlapping header content and is not hidden or presented as a clean-header success.

| Style / screen | Before captured and inspected | After captured and inspected |
| --- | --- | --- |
| Glass / Assistant | Light + dark | Light + dark |
| Glass / Agents | Light + dark | Light + dark |
| Glass / Agent chat | Light + dark | Light + dark |
| BeautifulUI / Assistant | Light + dark | Light + dark |
| BeautifulUI / Agents | Light + dark | Light + dark |
| BeautifulUI / Agent chat | Light + dark | Light + dark |

## Supplemental scenarios and remaining gates

- Twenty rapid real accessibility activations completed in each source and returned to Assistant. The callbacks are app-side observations, not physical touch timing.
- Open Agent returns were driven through the existing in-place app route because the pushed chat has no top switch. The shown transcript was checked; the original source retained incorrect global visible ownership. This does not establish preservation of the same pushed navigation view.
- A real composer was focused and an unsent synthetic draft entered. Reinspection of the exact after-source return image shows a clean software keyboard and retained draft, with no slide-typing tutorial; the earlier review note had misassociated the capture state. The before-source image also has a keyboard but includes a previously retained draft. A fresh exact-draft equality check will distinguish test setup contamination from a product regression.
- Programmatic scroll offsets returned toward the end in both sources. That control does not necessarily mark real user reading intent; it is **not** a passed user-gesture reading-restoration check.
- The complete selected unit/shell/chat regression suite, Mac/Vision builds, fresh retained-sync streaming/replay observation, real gesture reading/keyboard checks and CI-dependent merges remain open at this report's checkpoint.
- No new TestFlight build has been reserved, archived or uploaded. Build/upload must use current main containing `8ac9cd023` and the accepted fixes. Internal availability and **actual approved external availability** remain unverified; `VALID` processing or pending Beta App Review is not external availability.

Original checklist: [tab-switch-performance-739.md](tab-switch-performance-739.md). Stage data comment: https://github.com/nwverse/persona/pull/1063#issuecomment-6713575326. Test-fixture follow-up: PR #1191. Retained-sync follow-up: PR #1192. Apple CI runtime baseline: PR #1125 (not duplicated here).
