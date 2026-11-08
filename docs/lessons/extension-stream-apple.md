# Apple native extension streams: validation, routing and local acceptance

Scope: Apple native extension pages, private WebSocket sessions, delta reduction and receipt presentation. The contract is `schema/extension/stream.schema.json` and `schema/extension/README.md`.

## Private frames are not global broadcast events

`FrameMeta.read` recognizes frames with global event fields. Private snapshots do not have those fields; using it to identify `extension_ui_stream` misses snapshots while receipts with `event_id` may still be recognized. The apparent symptom is receiving a result without its page.

Identify the private frame's own `type`, then dispatch to the page observer on the owning connection. Do not add it to `ServerEvent`, the global cursor or replay. Missing `request_id` does not match a pending request: comparing two optional `nil` values returns equal. Route only by a nonempty correlation or an already-owned session.

## Original JSON is the reducer state

Generated Swift components also support legacy HTTP and sometimes decode permissively. They are a renderer projection, not canonical stream state. A decode/encode round trip does not preserve the original tree.

- Preserve omission, explicit `false`, `0`, empty strings, required empty arrays and numeric literals.
- Generate validation constraints from the same schemas. Validate before projecting to native views; do not create another wire envelope.
- Stage the entire operation batch and publish only after checking shared keys, topology, versions, budgets and the complete tree.
- An empty callback delta can advance exactly `+1`, fencing an accepted or uncertain callback without replacing the root.
- A non-event empty delta can retain its revision; a nonempty batch must advance exactly `+1`.

At interaction time, freeze the key, opaque action, session, epoch, displayed version, event ID and values. Do not borrow a newer action when a task or confirmation eventually executes. Receipts govern presentation only: commit their version first, then deduplicate by `(session, epoch, event_id)`. Closing, hiding or changing generation prevents asynchronous replies from reviving old pages or repeating effects.

Compare integers and bounds using the original decimal coefficient/exponent. `Decimal` and `Double` can round a long fraction into an integer. Neither accept unsafe versions through rounding nor reject large finite numbers permitted by the schema through an arbitrary precision cutoff.

## Global features do not imply every process supports streaming

Check both `hello.features` and explicit `context.stream`. Advertise only components the renderer actually draws; an empty component list means none. A renderer without media or HTML support must not advertise those tags.

A current hub may host an old process extension. Keep its full HTTP path when the initial read-only open is explicitly rejected. Once a native tree has mounted or an interaction has been sent, failure must not become a new HTTP write retry. This boundary survives native session closure and process replacement.

## Source-runtime acceptance on a Mac

Source runtime requires Linux. A macOS static mock is not Deno end-to-end acceptance. Use an isolated database, empty state directory, pinned and verified Deno, controlled package and formally issued full-access device credential. Do not copy production credentials, machines, push or data-source configuration.

Development fallback requires both the `persona_dev_resources` build tag and `PERSONA_DEV_SOURCE_RESOURCES=1` on the isolated manager only. It does not bypass namespace/mount checks or establish kernel-enforced aggregate RSS, CPU quotas or task limits; the production gate remains #5975.

Docker's default proc masks can make nested bubblewrap fail with `Can't mount proc on /newroot/proc`. Confirm the cause with the existing isolation validator, then adjust only the lab container's `systempaths`, not host or production policy. A namespace probe is not the full source validator.

An `--internal` Docker network does not provide ordinary published ingress. Keep the manager/database isolated and bind a separate client ingress locally. Docker-gateway requests may be classified as public, so `/api/v1/health` intentionally hides features. Read the authenticated WebSocket `hello`. The master token remains local-only: exchange a formal lab device credential instead of relaxing that restriction.

Use the official `jsx-runtime` and the same SDK module instance for page factories and Renderer. esbuild's default classic JSX introduces `React.createElement` and fails with `React is not defined`. Sync the official SDK and generate a bundle without runtime imports. Explicitly label an unpublished source's local page-registration adapter as an acceptance adapter, not a released package.

The isolated development source has a 300-second wall-clock limit. A long build/slot wait can overlap its restart even if an earlier probe passed. Build first, then check the authenticated source immediately before running the already-built suite. Keep the failed attempt and its timing; never relabel it as a pass or weaken assertions/resource limits to make the rerun succeed.

A task-wide `umask 077` protects logs/credentials but can also produce a Go executable with mode `0700`. `docker cp` preserves that mode and its file owner. The outer container process may execute it with privileges, yet the validator's user namespace and `--cap-drop ALL` cannot: the actual canonical launcher reported `bwrap: execvp /run/persona/personad: Permission denied`. Set the nonsecret executable artifact to `0755` before copying it; keep credentials/logs private. Re-running the unchanged canonical launcher then returned `ok`. Do not solve this by dropping isolation, forging its policy descriptor, or relaxing resource limits. A temporary diagnostic can call the existing `sandbox.ValidationProcess` and capture its stderr without changing the production validator.

## Keep frozen confirmation at page level

The page model owns frozen stream consent, not a row that may be replaced. In actual iOS settings navigation, a page-level `confirmationDialog` entered a waiting state without presenting and left the dangerous button spinning. A page-level native `alert` fixed the actual cancellation case. Legacy HTTP rows retain their existing presenter.

Check cancellation sends no callback, explicit approval executes exactly once, and version changes while awaiting consent reject stale authorization. A spinner or exit code alone is insufficient. Misconfigured lab pairing can skip XCTest cases yet return success; check executed and skipped counts.

An `alert` writes `isPresented = false` before invoking the selected button. Do not cancel the frozen continuation from that binding write, which would also turn explicit approval into cancellation. Buttons handle approve/cancel; lifecycle handles hiding/disposal.

Client confirmation does not replace the server gate. The manager separately sends an `extension.ui` permission card and rechecks contribution, authorization and revision. Only explicitly approved, current, owned frozen events may resolve a card whose saved action and confirmation text match exactly through the existing authenticated API. This never authorizes other tools, sessions or source-initiated permission requests.

A same-epoch snapshot cannot regress the committed version or change the root/view at the same version. Reject it, release the finished snapshot request fence, and request a complete tree rather than waiting indefinitely on a completed request.

## Visibility and positive readiness are distinct from retained data

Use one visibility source: `onTabVisibility` plus `scenePhase`. Do not mix mounted flags to activate a session. DEBUG readiness must distinguish a retained tree from an interactive current page: `stream:0` is not ready, and matching only the `stream:` prefix is insufficient.

The actual failure screenshot showed normal mounted/tab state but UIKit's Persona icon-change prompt leaving the scene inactive. Pausing was correct; it was not Form swallowing a mount callback. Do not relax background rules. In simulator tests only, acknowledge the known SpringBoard Persona icon/notification prompt, then wait for recovery. Never automatically answer an App or extension's own confirmation.

## Event requests can return full snapshots

Actual showcase segmented selection returns a full snapshot correlated to the event `request_id`, followed by its same-version receipt, rather than always producing a delta. Accepting only open/recovery snapshots drops this valid fallback: local selection changes while the tree remains at the old version and the button waits forever for its fence.

An event snapshot must match the pending event's connection, manager, session, epoch and generation. Validate and publish its complete original tree before processing receipts. Reject retired, hidden or recovering requests' replies. Preserve the revision assertions.

Initial UI readiness requires a positive committed version. During process reconstruction, wait for temporarily disabled page entries instead of blindly tapping. Transient snapshot failures get bounded read-only retries only. Never change an old write's event ID/protocol or resend it automatically.

## Snapshot parity is not callback registration

Shared legacy snapshots can reuse an action name across code-review diffs. A native SDK fixture must lower every action slot to a registered opaque callback, not only `action` and `load`: review discard, reply, comment, mark-viewed, context, draft, resolve and expansion controls also carry actions. Otherwise helper data parity can pass while the real manager rejects the page for a duplicate action ID. Check that all callbacks are registered, unique and preserve their metadata, then exercise the installed extension through the manager. Do not weaken manager validation or omit failing showcase pages to obtain a screenshot pass.

Exercise both direct contributions and state-selected pushed pages. A search fixture that always returns the static query-`1` snapshot can pass initial parity but fail a real query such as `02`. Filtering only when the factory's name is `list` still misses a `showcase` factory pushed with `{p: "list"}`. Use the selected page and submitted values for dynamic responses, preserving the unchanged initial snapshots; test both routes and an empty-result query.

## Native component negotiation also needs the correct UI version

The full native page renderer supports UI v5 review controls, but its open inherited the shared read-only context's v3 declaration. Against the same installed process-mode fixture, a v3 open of `code-review-v5` was rejected while a v5 open committed a snapshot and correlated close. A rendered page alone concealed the rejection because initial read-only negotiation can legitimately fall back to HTTP.

Declare v5 specifically on native full-page opens, alongside the actual supported component list. Keep the shared Widget/Intent read-only context unchanged. Pin the emitted version in a request test and require positive native commits during installed-extension screenshot acceptance; do not remove compatibility fallback or treat a legacy page as native evidence.

A positive native commit is not proof that every root-level control is rendered. The first v5 screenshots exposed an omitted `View.review`: the older HTTP fit had lowered it into ordinary components, masking the missing native bar. Mount the review bar outside the scrolling code content, preserve its named summary across native updates, and submit through the same frozen-action/confirmation path. Verify its visibility and actual submit/discard fences before accepting the full screenshot matrix.

A notification prompt's body also contains “icon”/“图标标记”. Classifying icon prompts first selected their OK button for the notification prompt, so no button matched and four real UI cases stayed inactive. Recognize the known notification prompt before the icon message, still requiring the tested app's brand and SpringBoard owner. Keep each failed route's diagnostic image distinct; never acknowledge an extension's own confirmation automatically.

Root review buttons use `submit`, but `review.discard` uses `press` (the canonical manager `EventAction` rule). Marking the whole review object as submit let cancellation appear healthy, yet confirmation was rejected before callback entry and never advanced its fence. Infer submit only for the review's button array, and test confirmed discard through the actual manager as well as a fake socket.

## Evidence boundaries

Unit tests use canonical valid/invalid fixtures and exact fence traces. Actual UI tests assert committed stream revisions, not just a loaded page that might be HTTP fallback. Screenshots contain synthetic data only; GUI captures are limited to the App window without other windows/desktop content. Clearly label offscreen rendering as such.

A tooling exit code is not an execution or UI verdict. An actual `idb-repl` run printed a Swift compilation error (missing the injected function's `Any` return) yet exited zero. Require an explicit executed-result marker and check diagnostics; then observe the resulting screen or XCTest state. An empty accessibility tree is not positive readiness, and successful HID dispatch alone does not prove the intended UI changed.

Use an independent build identity and machine-wide `xcode-slot` for every Xcode invocation. Own simulators follow the global count limit and are shut down afterward. Record the actual toolchain and that Xcode 26.0 was not verified when unavailable. Raw XCTest logs/xcresults can contain paths, device identifiers and launch credentials: publish only sanitized summaries and inspected images in the PR. Keep verification media outside the final source tree, following `docs/README.md`.
