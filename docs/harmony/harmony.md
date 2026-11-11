# HarmonyOS client

Persona's HarmonyOS client uses ArkTS and native ArkUI on HarmonyOS 7 / API 26. It runs on straight phones, conventional foldables and wide foldables; watches are outside this client. The Hub remains the only application backend. Agents run on the user's computers, with their existing runtime and host capabilities.

## Product and information architecture

The four primary destinations are Persona, Agents, Inbox and Settings. Each keeps an independent navigation stack in its Hub and manager scope.

- Persona contains the unique main chat, recent conversations, new chat, Todos, Knowledge and the separate Health space.
- Agents starts with a flat session list. Rows retain the computer, repository and worktree identity; the native grouping menu exposes computer, repository and worktree groups. Search, state, computer, unread and history filters are explicit user actions. Reading freezes membership and order while live status can update in place.
- Inbox owns its filters, contextual source viewer, handled state, drafts and confirmed outbound actions. A Todo source reference opens that source conversation rather than fabricating an Inbox item.
- Settings separates device preferences from Hub/manager configuration. Management actions retain their captured owner, connection and target while asynchronous work completes.

The phone is a controller and reader, not another Agent runtime. A lack of a trusted online computer, a read-only source or a missing server feature must be represented before an action becomes available. An unavailable integration is distinct from an empty successful result.

Health always resolves the roster's default manager and explicitly uses `space=health`. Its conversations and records stay outside ordinary recent chats and the main chat. Reports go to that manager's trusted online assistant computer with `dest=health`; upload stages a path in a draft and never sends a chat turn by itself. HarmonyOS does not claim to provide Apple's HealthKit.

## Native layout and design contract

The structure comes from `Navigation`, `NavDestination`, `Tabs`, system title bars, lists, selection controls, menus, sheets and dialogs. Reusable business views compose those controls; they do not draw replacement navigation, tabs, system pickers or dialogs. Persona supplies semantic colors, typography and spacing through resources and `Tokens`.

Use the actual window class. XS, SM and MD use a stack; LG and XL use native split navigation. An expanded foldable can still have an MD window. Do not infer a split from a model name, screen pixels or a fixed percentage.

The expanded master pane starts at 320 vp, has a native drag range of 280–440 vp and can collapse to 64 vp. Its four primary tabs live at its bottom. Collapsing hides those tabs and uses the native toolbar to restore the pane. It does not add a second navigation rail.

Agent detail hides its title only when the matching left-side selected row is visible and identifies it. A compact window, collapsed pane, filter exclusion, offscreen selection or different owner restores the native title. File, history and other child destinations retain their own title and back action. Selecting another Agent replaces the Agent destination instead of creating a back stack of every selected row.

The floating bottom tabs use the native 56 vp bar with a 12 vp bottom margin. ArkUI owns the system inset. Do not append another safe-area spacer or copy the system gesture-area height into content padding. List tail clearance accommodates the floating control once.

The conversation uses `FoldSplitContainer` for horizontal half-fold hover. Reading stays in its primary region; the current decision or composer stays in its secondary region. The native component owns crease avoidance. It is instantiated only when the system supplies valid fold-crease geometry. A foldable emulator image that omits that geometry uses the ordinary native Column and Scroll layout; it cannot validate hinge avoidance or hover behavior. The capability is sampled when a conversation view is created; configuration callbacks do not switch that view between native subtrees, preserving input composition, focus and transcript identity. This capability check prevents the framework from dereferencing an absent crease region and does not invent a hinge or emulate hover. Outside hover, the measured content height determines the ordinary split, so a phone does not inherit a fixed 50/50 division. The interaction region scrolls when large text, expanded choices or the keyboard reduce its available height. The transcript scroller and draft owner remain the same across posture changes.

Glass, Brief and Board retain the same fields and actions and the native structural layout. Their own-message treatments use separate semantic light/dark resources. Device appearance persists separately from Hub preferences and is propagated to already-open manager contexts.

Avatar and Accent are independent device preferences. Native list groups, Radio and Select controls expose the built-in avatar, authenticated Hub packages and accent choices. Initialization restores the saved preference before enabling changes, including a retry after storage failure. Each package choice retains its source Hub and revision, so identical package IDs from different Hubs remain distinct. The selected image is an intentional device preference and survives Forget Hub; other cached images from that Hub are purged. A new preference is published only after storage acknowledges it.

Native `WithTheme` supplies light and dark brand/emphasis tokens and readable own-message tokens without tracking system color mode in a second observer. Package PNGs have separate encoded-size and decoded-dimension limits, must pass native image decoding, and are installed atomically in private storage. Semantic text and bubble contrast is at least 4.5:1. The application opts in to the platform's UIMaterial behavior; supported native controls own the material effect and its system fallback.

Do not add descriptive subtitles, explanatory child controls or a second action for the same purpose. Target identity belongs where a decision needs it. Operational errors and confirmations state the actual target or consequence. A title already supplied by the destination is not repeated in its content. Long actions move to native menus; controls must remain usable with large text and the keyboard visible. The application configuration explicitly follows the system font scale, including its 3.2× maximum; ArkUI uses `fp` for text.

## Conversation and operation lifecycle

A detail context captures its Hub, manager, machine and session. Opening a manager-owned resource does not change the workspace's selected manager. Global Settings remain workspace-owned; settings opened from a chat retain that chat's target.

Each chat draft has a revision. A submitted message is persisted in the local outbox before network admission and appears immediately. Its stable `client_msg_id`, rather than matching text, reconciles a server echo. The outbox uses an index plus individual bounded rows, not one growing database cursor row. The operation journal contains identity and outcome, not another copy of message text or photo Base64.

A known acknowledgement clears only the captured draft revision. Newer edits survive, including identical text typed again. An unknown result blocks that destination until it is reconciled; another chat can still send. Reconnection never silently creates a replacement operation. Explicit confirmation of a local-only crash window may release it only after the server's receipt lookup proves there is no recorded operation.

Attachments remain local until explicit Send. Native photo/document pickers grant access to the user's selected files; sandbox copies have bounded size and validated decoded image content. A system photo editor receives read permission and returns a new selected attachment, preserving the original. System preview provides image zoom and supported PDF, Office, HTML and media viewing.

Received images open an authenticated conversation-scoped system gallery. Editing one stages a new outgoing image in that conversation's draft. A native editor may pause the application and cause a reconnect; the stable pairing revision, Hub, manager and draft identity determine whether its result still belongs there. Forgetting, re-pairing or changing the destination rejects the result. An acknowledged draft keeps its referenced file even if navigation occurs while storage acknowledgement is returning.

An outbox message carries only the selected attachment IDs and revisions for cleanup. HTTP acknowledgement, exact echo or a later receipt suppresses those revisions before fallible I/O. Failed cleanup keeps a durable obligation and blocks reuse; ordinary terminal-history pruning cannot discard it. Local storage epochs fence queued work across Forget Hub and immediate re-pairing. Successful cleanup preserves newer unsent attachment revisions.

Native picker pauses and same-owner reconnects do not detach the local attachment selection. Hidden pages stop their subscriptions and release terminal/audio/screen control. Foregrounding rebinds the captured resource to the new authenticated repository and re-fetches current facts; it does not replay mutations.

## Workspace, source and permission boundaries

Files and Git use the existing machine routes. Writes, branch switches, stash operations and push require a native confirmation with computer, directory, branch and operation. The client does not automatically force-push, clean or reset. Acknowledgement remains success if a follow-up read fails. Conflicts keep the user's draft.

Terminal viewing begins read-only. Taking control is explicit and revocable; hidden, disconnected or replaced contexts cannot replay queued keys. The desktop RFB screen is a separate capability from a terminal's text screen. It uses the authenticated native binary WebSocket transport to the Hub's existing port relay, with read-only viewing and separately confirmed control.

Source-specific Inbox actions preserve manager, source, account, conversation and message identity. Reading acknowledges only visible content. Outbound replies show their actual account and recipient before confirmation. Native Todo references use the source-message endpoint to locate a conversation position; a topic binding is not treated as a message ID.

The current Todo contract can contain a Feishu `omt_*` thread binding without a root `om_*` message. The messaging API opens a thread by root message ID and exposes no verified mapping from that binding. Such a binding remains readable metadata with no fabricated read/reply operation. An explicit verified root message can use the existing thread API. This is a server/provider contract boundary, not a client-side ID conversion.

Phone tools request their own platform permission when used. Permission to read is not permission to write; writes require a phone-side confirmation and expire without approval. Unsupported platform data capabilities have an explicit result. Voice uses its negotiated extension protocol and releases audio resources on interruption/background; lack of a configured voice service does not prevent text use.

Contact writes require separate read and write permissions, show the before/after values, and recheck the original contact immediately before updating only requested fields. Calendar operations use calendars accessible to the application. Updating or deleting a recurring occurrence or splitting a series is unavailable with the current atomic Calendar Kit contract; the client does not substitute a whole-series mutation. Access to all system calendars requires the separately qualified whole-calendar permissions. Place-name search uses geocoding and does not read the phone's physical position.

Voice chooses the highest protocol version this client implements from the extension's advertised maximum. Legacy V1 is restricted to the manager's unique main conversation; it cannot target an Agent or a secondary conversation. V2 binds its ready frame and audio stream to the selected machine/session. Permission dialogs, reconnects and pending capability discovery cannot change the protocol or target of an admitted call.

Knowledge recordings keep bounded PCM audio in the application sandbox. Capture and transcription have separate states; stopping does not automatically transcribe or upload. The current on-device Core Speech recognizer supports Mandarin (`zh-CN`) PCM16 mono at 16 kHz; other languages are unavailable in this recording flow. Audio feeding starts only after the matching native recognition session reports `onStart`, and stops on cancellation, error or premature completion. Sync uses the existing idempotent recording PUT with transcript segments only. Audio never enters that request. An uncertain acknowledgement retains the exact recording ID, revision and body for explicit retry. Backgrounding stops capture/cancels transcription, and Forget Hub purges identifiable persisted recordings even when no recording view was opened during the current launch.

Scheduled task templates populate an editable draft and do not create a task until confirmation. Run Now opens only the acknowledged response's actual machine/session; failed or uncertain new attempts cannot reuse an earlier result. History entries without both destination IDs remain readable without an invented conversation link.

## Architecture

| Layer | Responsibility |
| --- | --- |
| `harmony/entry/src/main/ets/core` | Pure protocol reducers, transcript continuity, capability policy, bounded content parsing and frame/selection logic |
| `models/ClientModel.ets` | Workspace and detached manager contexts, catalog, selected chat, draft/outbox and lifecycle coordination |
| `models/HubRepository.ets` | Existing REST contracts and durable mutation admission/receipts |
| Dedicated workspace models | Todo, Knowledge, Health, Inbox, Agent list, file/Git/terminal and management state with captured owner guards |
| `platform/HubTransport.ets` | Native HTTPS/TLS and WebSocket transport, endpoint validation, pin verification and bounded frames/bodies |
| `platform/LocalStore.ets` | App-sandbox relational storage, serialized writes and per-Hub retirement epochs |
| `platform/CredentialVault.ets` | Platform Asset Store credentials, separate from application data |
| `models/SshTunnel.ets`, `platform/SshCredentialVault.ets`, `native/ssh` | Conditional pinned SSH connection, authenticated local proxies, bounded device-only private-key storage and native cancellation |
| `components` and `pages/Index.ets` | Native business views and navigation, localized resources and platform controls |

The secure endpoint and identity are fixed before sending credentials. A supplied pin does not fall back to ordinary CA trust after a mismatch. Paths cannot escape the authenticated origin/prefix. Redirects do not transfer authorization to another origin. Binary downloads, JSON bodies, frame assembly, decoded images and local cached content have separate bounds.

Forgetting a Hub retires its operation contexts before removing credentials, scoped database rows and owned file/cache copies. A durable cleanup record is acknowledged before physical erasure starts. Failed cleanup keeps that identity after the profile disappears, blocks reconnection/re-pairing to it and retries at startup, foregrounding or the explicit Retry cleanup action. Clearing the record requires every cleanup step and the profile update to succeed. Retrying another Hub's cleanup does not stop the active Hub's SSH lease. Device appearance remains local. Runtime secrets, paired URLs, generated signing material and private fixture records never enter the repository.

Phone-tool registration and incoming calls belong to the same captured client context. Changing the active phone/voice settings owner removes the old subscription and advertises an empty tool list on its old connection before registering the new owner. Late events cannot be assigned to a different Hub. Returning from contextual settings restores the workspace owner. Old audio resources detach their captured listeners before asynchronous release, so their cleanup cannot consume or clear a newer call.

HTTP `Accept-Language` and WebSocket `client_hello.locale` use the effective application language, resolved to `en`, `zh-Hans` or `ja`. A successful explicit language change re-announces capabilities on every live workspace and detached manager connection. Native Follow system takes effect after a cold application launch; selecting it keeps the current launch's UI and wire language aligned until then. Recreating an Ability in the same process does not apply that pending change. Matching-system mode re-announces after a system configuration change; unsupported system languages resolve to English. A failed preference write restores the prior visible and wire language.

## Build and identity

Open `harmony/` in DevEco Studio with the HarmonyOS 7 SDK, or use DevEco CLI from that directory. API 26 is the supported minimum and target; older API compatibility requires a separate component and device matrix. The repository supplies a development example bundle identity. Maintainers supply the registered identity and signing configuration privately before distributing a release; this guide does not authorize store upload or submission.

Build the locked ARM64 SSH core before the HAP, following [the native build and license instructions](../../harmony/native/ssh/README.md). It requires Rust 1.98.0 with the OHOS target and the API 26 native SDK. CMake packages both the NAPI bridge and its core library; generated binaries stay outside version control. Private keys use bounded 1 KiB Asset Store chunks and a final manifest, with device-unlocked access and synchronization disabled. The database stores metadata and a random key reference only.

SSH accepts only the captured SHA-256 host-key fingerprint before authentication. Its local authenticated proxies admit the captured Hub host/port only. Device DNS answers can admit a TLS socket's resolved IP to SOCKS, but the SSH channel still opens the logical Hub hostname; HTTPS Host, SNI, SPKI and base-path checks retain that original identity. Backgrounding, owner changes and cancellation retire the same lease for HTTP and child streams. Asset removal queries bounded attribute-only pages by profile/reference, then deletes exact aliases; it never retrieves secret values for cleanup. This also accommodates API 26 runtimes that reject critical labels in `asset.remove`.

The locked dependency notice inventory is bundled as a local application resource. Privacy → Open source licenses opens its complete texts offline in a native sheet, using ArkWeb with JavaScript, DOM storage and file access disabled. `harmony/native/ssh/package-notices.py` verifies every inventory checksum against the retained notice and the locked package identities before generating that resource.

The app and entry Ability refer to the same layered image. The 1024-square source foreground reuses Persona's approved artwork, the background is opaque and flat, and the startup image uses the approved flat icon. `harmony/scripts/export-icons.py` generates the resources and `harmony/icon-manifest.json`. SDK resource compilation can legitimately convert the icon layers to 512-square RGBA PNGs; inspect the packaged content and references rather than treating that size as a source-art rule.

## Verification

Run portable behavior tests with Node.js 24 or newer from `harmony/`:

```sh
node --test tests/*.test.mjs
node tests/run-domain.mjs
```

Build the application and the native SDK test target separately:

```sh
devecocli build
devecocli build --modules entry@ohosTest
```

Install the test HAP on an owned emulator and run its Stage test runner:

```sh
hdc -t '<device serial>' install entry/build/default/outputs/ohosTest/entry-ohosTest-unsigned.hap
hdc -t '<device serial>' shell aa test -b '<development bundle>' -m entry_test -s unittest OpenHarmonyTestRunner -w 60
```

The tests use unique database names and owned aliases/copies. They must not erase a normal profile. Native transport tests additionally use the local fixture in `harmony/tests/native-transport-fixture.py`; its last case rotates its own certificate, so restart only that fixture before another run. Do not rotate a paired Hub certificate as test setup. The `previewCheck` test argument enables the image preview suite. Reopen the main Ability after a test run before taking UI evidence. The optional `fontScale` argument (`2` or `3.2`, with `aa test -s timeout 60000 -w 90`) opens the actual application at that verified native scale for a 45-second layout inspection, then restores its original application scale. This probe is confined to the test HAP and does not change system font preferences.

Keep one owner and one active run per build/test. A worker writing a module does not run a competing build. Portable tests verify behavior with platform boundaries stubbed; they do not replace native SDK results or device screenshots.

The device matrix includes a straight phone, a conventional Mate X-style foldable (cover, expanded, rotation) and a Pura X Max-style wide foldable. For each, inspect actual window class and native split boundaries, title restoration, back stack, draft continuity, IME resize, system insets, light/dark, English/Chinese/Japanese and 200% text. Also inspect read-only/expired decisions, offline/reconnect and foreground routing. Use synthetic work and an isolated Hub; other runtime/voice/screen-sharing environments are listed separately when unavailable. A screenshot of a prototype is not proof that the HAP behaves correctly.

No AppGallery push, service card or live-window entitlement is implied by native foreground notifications. These system enhancements require their own qualification and device evidence; the core application does not rely on background WebSocket survival.

## Platform references

- [ArkUI Navigation](https://developer.huawei.com/consumer/cn/doc/harmonyos-references/ts-basic-components-navigation) and [Tabs](https://developer.huawei.com/consumer/cn/doc/harmonyos-references/ts-container-tabs): native structure and system-owned behavior.
- [FoldSplitContainer](https://developer.huawei.com/consumer/cn/doc/harmonyos-references/ts-container-foldsplitcontainer): native hover regions and crease avoidance.
- [System photo editor](https://developer.huawei.com/consumer/cn/doc/harmonyos-guides/photoeditorextensionability): `startAbilityByType("photoEditor")`, read URI grant and returned result URI.
- [Scan Kit default UI](https://developer.huawei.com/consumer/cn/doc/harmonyos-references/scan-scanbarcode-api): system scan interface rather than a custom camera overlay.
- [Application icon configuration](https://developer.huawei.com/consumer/cn/doc/best-practices/bpta-app-icon-configuration): layered resources, entry references and system composition.

The local API 26 SDK and the corresponding Huawei documentation are the implementation authority. An existing ArkUI capability is not described as newly introduced in HarmonyOS 7 merely because this client uses it.
