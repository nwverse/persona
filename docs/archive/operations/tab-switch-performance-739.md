# Persona / Agents switching: measurements and acceptance boundary (#739)

Date: 2026-11-08. Baseline: `34f263fad`. All production diagnostics were GET requests; no main conversation was created, archived, sent to or marked seen. Browser interaction used only the synthetic mock hub.

## Root causes and before / after evidence

### Assistant overview

The production runtime inventory contained 8,356 sessions. The query-free full list was 7,991,471 response bytes, although the assistant overview returned only 15,683 bytes. Its local `sessions.list` path decorated every runtime row, then encoded and decoded the entire inventory before selecting the few headless conversations.

The fix selects headless conversations from typed device data first, then applies the existing decoration pipeline. Partial decoration fills its own state timestamps but cannot prune unrelated state marks or hook flags. Legacy managers without a local fleet retain their existing RPC path. Health-space, archived-conversation, pane exclusion and wire-value parity have regression coverage.

`BenchmarkAssistantViewLargeInventory` uses 8,200 synthetic sessions, including 14 assistant conversations, and measures the complete overview plus response encoding:

| Implementation | Three runs (ms/op) | Allocated bytes/op |
| --- | --- | --- |
| Before | 457.98, 449.97, 464.04 | 218,727,370; 204,779,514; 204,805,202 |
| Removing JSON round trip only | 342.95, 372.94, 362.36 | 67,929,349; 67,879,458; 67,887,389 |
| Select before decoration | 5.84, 5.86, 6.67 | 12,254,078; 12,254,569; 12,254,985 |

The intermediate experiment matters: merely removing serialization still decorated thousands of unrelated coding sessions. These are Linux CPU/allocation measurements, not iPhone frame timings or retained-memory measurements.

### Web full-list handoff

On narrow screens the sidebar list hands over to the Agents-page list. Saved grouping or pins select the query-free full-list fallback. A hook-local snapshot, pending request and cooldown were discarded on each component handoff.

The fix shares them within the current credential partition. A one-second last-consumer grace preserves same-commit handoff and StrictMode remounts, then frees the snapshot, subscriptions and timers. Reset invalidates synchronously, including logout followed by the same login in one React commit. Normal hints keep the existing three-second post-completion throttle; reconnect and failure recovery semantics remain covered.

The same 8,356-row synthetic fixture, initial mount plus 20 same-commit handoffs:

| | GETs | Sum of uncompressed response bytes |
| --- | --- | --- |
| Original source | 21 | 159,636,834 |
| Fixed source | 1 | 7,601,754 |

This is actual test execution, not an estimate of production compressed traffic. Coverage includes simultaneous consumers, pending handoffs, grace expiry, credential/reset fencing, late replies and local-edit overlays. The default bounded list remains unchanged.

A second comparison drove actual production bundles in Chrome at 390 by 844 against the same small synthetic mock hub, clicking the real switch 20 times per theme. Original light/dark runs each made 20 additional query-free full-list GETs; fixed light/dark runs each made zero. Both retained 10 bounded/exact-detail GETs per run and had zero runtime exceptions. Screenshots were inspected. The small fixture's click timing is not evidence of large-inventory frame improvement or iPhone performance.

### iPhone retained chat

The shell retains both pages after first display; switching changes `tabIsActive`, not physical mounting. `ChatView` previously subscribed and acquired visible/read ownership in `onAppear`, releasing only in `onDisappear`. Its hidden-page release existed only in the Mac branch. iPhone therefore kept the hidden Persona transcript stream active.

Incoming deltas are applied on the main actor and published through the existing pacing layer, potentially five display updates per second. Each update can invalidate the retained view's streaming Markdown and layout even though the page is invisible. The fix binds subscription and visible/read ownership to paired `onTabVisibility` edges on iPhone as well as Mac. It does not interrupt the Agent on the hub. Returning uses the retained `ChatStore` and its `since_seq`; no page-identity change was added.

Added Apple tests cover an initially hidden mount, 20 repeated visibility cycles, retained store identity and another visible owner's subscription. **Apple tests, actual delivery-rate counts, frame-time comparisons and screenshots were not run on Linux.** Five-to-zero hidden display updates is the intended mechanism, not a measured iPhone result.

## Read-only production baseline

Seven samples per endpoint, measured on the hub machine over loopback without a proxy. These exclude mobile internet/TLS latency. The steady history requests below use five turns; Apple initial history uses two turns and limit 80, so they are also useful payload-size diagnostics, not an exact capture of every iPhone request.

| GET request | p50 (ms) | Maximum (ms) | Response bytes |
| --- | --- | --- | --- |
| `/api/v1/assistant` | 393.73 | 538.09 | 15,683 |
| `/api/v1/sessions?limit=50&summary=1` | 1.20 | 4.75 | 48,127 |
| `/api/v1/machines` | 0.63 | 0.88 | 6,239 |
| `/api/v1/fleet` | 0.36 | 0.60 | 1,389 |
| Persona items, five turns, folded work, lazy images | 2.24 | 3.78 | 20,114 |
| Persona items after newest covered sequence | 0.68 | 0.81 | 93 |
| Agent items, five turns, folded work, lazy images | 2.06 | 18.84 | 10,992 |
| Agent items after newest covered sequence | 1.34 | 49.32 | 89 |

A separate initial Persona-history read was 442.30 ms before the node's transcript cache warmed; repeated reads were 1.14–2.36 ms. Do not average cold initialization into a warm-cache claim.

### Client request paths

- Apple: first assistant mount reads `/api/v1/assistant`. A retained warm tab switch does not by itself rerun that mount task.
- Apple chat subscription: WebSocket `subscribe`, lazy images and folded work, with `since_seq` when coverage is known. HTTP fallback/history uses `/items?turns=2&limit=80&lazy_images=1&fold_work=1`; catch-up uses `after_seq`. Paged chat detail uses exact `/sessions` lookup.
- Web: the socket connection itself survives navigation, but chat mount subscribes again, reads the latest two turns, exact detail, incremental feed and readiness. Only the grouped/pinned full-list handoff was changed here; those other bounded requests remain.
- iPhone source also contains synchronous cold `ChatCache` loading/decoding, feed catch-up sorting and rich-Markdown parsing. These are profiling candidates, not additional measured causes or unverified cache rewrites in this patch.

## Verification and Mac follow-up

Linux verification: relevant assistant/hub/devices packages passed against isolated test databases; web full-list tests 37 passed; complete web tests 5,329 passed and one pre-existing test skipped; lint, typecheck, production build, privacy and documentation-path checks passed. Light/dark 390-by-844 browser screenshots were inspected using synthetic data. Production deployment and after measurements are recorded in the linked PR's final verification comment.

For the separate Mac task:

1. Fetch the merged main commit and use an issue-scoped worktree. Do not change registered app identities or signing resources as part of this performance work.
2. Generate private xcconfig with `python3 scripts/persona_config.py xcconfig`. Before manual Xcode invocation, load the environment using the config script. Verify Xcode 26.0 compatibility when available; otherwise record the Xcode used and "26.0 not verified".
3. Build iPhone, Mac and other targets compiling `ChatView` / `TabActivity`; run `TabActivityTests`, `ShellStateTests`, `ChatStoreTests`, plus shell/chat UI regressions. Use the simulator allocation and pinned-launch helpers from the testing guide, never create or boot someone else's simulator.
4. Compare baseline and merged code with the same synthetic quiet/streaming conversations and large inventory. Measure each direction separately, cold and warm, rapid alternate taps, an open Agent session, keyboard shown, scroll restoration, hidden-stream delivery, unread/notification behavior, and missed-sequence catch-up. Do not include artificial sleeps in tap-to-visible timings.
5. Record tap-to-first-frame and settled p50/p95, worst frame, main-thread stacks, hidden delta delivery/render counts, and exact request counts/bytes. Inspect light/dark simulator screenshots in the relevant existing styles; preserve the prior look. A source-level subscription change alone does not establish smooth iPhone switching.
6. After those checks pass, reserve a unique build number, release through the repository release script, then run the TestFlight wait command from the release guide. Report the build number, internal-group availability and external Beta App Review state only after both groups are handled.

The hub/web fixes can be verified and deployed independently. The iPhone performance and TestFlight acceptance remains open until the Mac evidence is supplied.
