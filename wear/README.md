# Persona for Wear OS

A standalone Samsung Galaxy Watch / Wear OS client for the core Apple Watch workflow: see what needs you, answer a question, review a plan, approve an action once, inspect recent conversations, send a short reply, and check the Inbox. Its audience is someone away from their phone who needs to read or answer a small task on a round watch screen.

The app connects directly over Wi-Fi to a primary Persona hub and up to three additional hubs. Each uses its own revocable `kind: watch`, `role: answer` credential and the existing REST API. It does not need an Android phone companion or a new server protocol. Foreground refresh is polling because the hub deliberately denies WebSocket access to watch credentials. Background push and continuous background polling are not implemented.

## Scope and platform differences

- Assistant and Agents open existing conversations; requests stay associated with their owning task. The pending count is the visible unresolved request list, including requests which need another device to complete.
- Question choices, plans, ordinary one-time permissions, and supported computer-dialog options use the Apple Watch wire format. Secret input and unrestricted terminal typing stay on the phone or computer. The app never offers an always-allow permission.
- Messages use stable client message IDs. Action submission is separate from successful execution; an acknowledgment means the hub received the action.
- Conversations also provide Continue and mark an opened conversation as seen. Known numbered choices from older hubs use the same restricted terminal-key contract as Apple Watch; Esc, confirmed Enter, and Handle on Computer remain explicit actions.
- Text entry uses the watch keyboard. The earlier optional system-dictation entry remains unchanged; voice-input expansion, microphone recording and transcription were explicitly deferred. No recording service or microphone permission is shipped.
- Inbox handling, short replies and AI-generated reply drafts are available only when the hub exposes those capabilities. Generating a draft never sends it, and replacing an unsent draft requires confirmation. Missing Inbox support does not hide successfully loaded tasks.
- Connections in Settings manages additional hubs. Session/request identifiers, drafts and operation receipts remain scoped to their owning connection. Failed groups retain read-only cached tasks; healthy groups remain usable. Inbox belongs to the primary hub.
- The Milk Frog page uses the original hub-supplied, hash-verified assets, with static snapshots for its optional Wear OS complication. It shows pending work, progress, finished work, rest and connection failure; it never calls an unreachable hub idle. The entry appears after a complete supported asset pack is available.
- Extension widgets expose only the existing read-only projection: text, metrics, progress, rows, statistics and charts. Provider buttons, fields, actions and arbitrary web links are not executed. Optional extension complications read cached projections and visibly expire; the app refreshes them when opened.
- Credentials remain in app-private storage protected with Android Keystore; the application disables backup. HTTPS supports the hub public-key fingerprint supplied at pairing. HTTP is restricted by the client to local-network addresses for existing LAN hubs. HTTP remains plaintext on that network.

This version does not port iPhone-dependent WatchConnectivity setup, phone push delivery, audio recording/transcription, voice calls, workout/health integration, recording complications, or a phone companion. It adds complication data sources, not a replacement watch face, and does not change the user's selected face. Tool activity remains collapsed until opened for inspection. The [capability comparison](PARITY.md) distinguishes implemented features from platform-dependent and deferred work; the [verification report](QA.md) records what was actually tested.

## Build

Use JDK 17 or 21 and Android SDK Platform 35. The included Gradle wrapper downloads its pinned distribution and dependencies when they are not already cached. Set `JAVA_HOME` and `ANDROID_HOME` for your machine; no personal paths belong in checked-in files.

From `wear/`:

```powershell
.\gradlew.bat :app:assembleDebug :app:testDebugUnitTest :app:lintDebug --console=plain
```

The installable APK is `wear/app/build/outputs/apk/debug/app-debug.apk`. The package is `dev.persona.watch`. Debug signing is for local installation; retain the signing key for subsequent updates.

```powershell
adb pair <watch-pairing-address>
adb connect <watch-debug-address>
adb -s <watch-debug-address> install -r wear/app/build/outputs/apk/debug/app-debug.apk
adb -s <watch-debug-address> shell am start -n dev.persona.watch/.MainActivity
```

Wireless pairing and connection use different ports. Existing ADB pairing can be reused. Installing this package does not replace any existing watch face or unrelated watch app.

## Pairing and private debug provisioning

The app's connection screen accepts a hub address and pairing credential, then exchanges it for its own watch credential. Do not distribute a full-access device token or embed a token in the APK. The hub's device-management screen can revoke the watch credential.

It also accepts a copied `persona://pair` link. A link's explicit TLS port and public-key pin are preserved; alternate origins are not followed. Settings → Connections adds or removes extra connections without changing the primary hub or other groups' drafts.

Adding an extra connection journals its metadata before exchanging the pairing token. The issued watch credential is encrypted before the directory entry is committed. If that commit fails, opening Connections or choosing Retry finishes the saved registration, including after an app restart, without exchanging the one-time token again. An unreadable saved credential is retained for recovery rather than silently discarded. A crash, lost response or storage failure before the issued credential becomes durable cannot be recovered locally; a new pairing link may be needed in that case.

Pending registrations recover independently: a missing-credential record is discarded, while a different Hub can be added without publishing an older pending Hub first. Connection removal keeps a cleanup journal until its encrypted credential is deleted, so a storage failure remains retryable after its row disappears. Changing the primary origin requires a new pairing token; the previous primary connection remains active until the candidate passes an authenticated refresh and its current UI completion commits it. The same open pairing form can reuse an issued candidate after a refresh/save failure; that primary candidate is held in memory, so a process restart can require a new pairing link.

For device development, a debug build additionally accepts `files/debug-pairing.json` in its private directory. The file contains `baseUrl`, `token`, and optional `fingerprint` and `deviceId`; use a watch-specific answer credential. The debug import is launched with `--ez import_pairing true`. It is not an exported token-reading service or a production onboarding mechanism. The app consumes the file and protects the stored credential.

Keep the source configuration in a private local file outside version control. The repository's ignored `tmp/` is an option for temporary testing, but a private directory outside cloud-synced projects is preferable. Never paste its contents into logs, screenshots, issues, or chat.

```powershell
node wear/tools/provision-watch.mjs --adb <adb-executable> --device <watch-debug-address> --config <private-json-file>
```

The helper transfers JSON on ADB stdin into a fixed app-private path using `run-as`; the token never appears in process arguments or helper output. It starts the app and reports delivery, not verified authentication. Confirm the resulting screen and hub response separately. Remove temporary source credential files after testing.

## Synthetic LAN acceptance fixture

The fixture is a Node.js built-in HTTP server with no dependencies and no connection to a real hub, account, mail service, or agent. Every conversation and request is explicitly labeled as sample data. Its fixed test-only credential is `persona-wear-fixture-only`; never use it as a production secret.

From the repository root:

```powershell
node wear/tools/fixture-hub.mjs --host 0.0.0.0 --port 18765 --events tmp/wear-fixture-events.jsonl
```

Binding to all interfaces makes the fixture reachable by a watch on the same network. The default, without `--host`, is loopback only. Use your computer's reachable LAN address in the watch configuration; there is no ADB reverse tunnel in this LAN test. Stop the process when testing is finished.

A fixture configuration uses these fields, replacing the address placeholder before use:

```json
{
  "baseUrl": "http://<computer-LAN-address>:18765",
  "token": "persona-wear-fixture-only",
  "deviceId": "fixture-watch"
}
```

The initial state has five requests: question, plan, ordinary permission, computer choice, and a protected permission that cannot be allowed on the watch. It also has Assistant, a working Agent, and one replyable Inbox item. The server validates the supported answer shapes and rejects the free-input screen option and secret approval. Submitted messages and operation IDs are deduplicated; conflicts return HTTP 409. No endpoint forwards an action to the real hub.

The following controls always connect to loopback. `/__test` requires the fixture credential and rejects non-loopback peers, regardless of their Host header.

```powershell
node wear/tools/fixture-control.mjs status
node wear/tools/fixture-control.mjs reset
node wear/tools/fixture-control.mjs offline
node wear/tools/fixture-control.mjs online
node wear/tools/fixture-control.mjs error
node wear/tools/fixture-control.mjs stale
node wear/tools/fixture-control.mjs slow
```

Use `--port <port>` when changing the listener. `offline` drops API connections, `error` returns 503, `stale` returns 410 when an outstanding request is answered, and `slow` delays API responses by four seconds. `online` removes delay. `reset` restores initial synthetic state and clears in-memory events. The optional JSONL audit remains append-only and contains route, status, action kind, synthetic IDs, and message length/hash, never authorization headers, query values, or submitted message text. The local control API also accepts `{ "failNext": { "path": "/api/v1/inbox", "status": 503 } }` and `{ "expire": ["fixture-plan"] }` for focused cases.

## Acceptance procedure and evidence boundaries

Build, unit tests, and lint establish code-level checks. A fixture run establishes rendering and client transport behavior with synthetic data. Neither proves successful communication with a real Persona hub or execution by a real agent.

For physical-watch acceptance, save screenshots, package version, device model/API, and scrubbed request outcomes outside committed source. Check the round-screen top and bottom, scrolling, large text, loading, failure, and empty states. A supported debug build can open a known screen using `--es screen home|agents|inbox|settings|session|interaction`, with `session_id` or `interaction_id` for details; this changes only navigation, not server data.

```powershell
node wear/tools/capture-watch.mjs --adb <adb-executable> --device <watch-debug-address> --out tmp/wear-qa/home
```

The capture helper saves a binary-safe PNG and the Android accessibility XML, without opening another screen. It records the explicit user rotation preference before UI automation and restores/verifies it after the dump process exits, including failure. A device-side timeout ends the dump before host cleanup; the PNG remains available if the hierarchy is unavailable. Use `--screenshot-only` when XML is unnecessary: it never connects UI automation. Screenshots and accessibility dumps can contain private conversation text: keep real-hub captures private and publish only synthetic fixture evidence.

1. Install the APK and connect the watch to the synthetic fixture over ordinary LAN Wi-Fi. Verify five pending requests appear and the protected request has no enabled approval path.
2. Submit each supported choice; confirm the fixture audit records one accepted resolution, the card disappears, and the count updates. Cancel a confirmation and verify no write was received.
3. Send a sample message, inspect its acknowledgment and resulting transcript, then verify retry does not duplicate it. Exercise interrupt only against the synthetic working task.
4. Reply to the synthetic Inbox item and mark it handled; confirm each action in the fixture audit.
5. Use offline, 503, delayed, and stale modes. Check cached data is identified as old, recoverable drafts survive, and stale cards do not appear successfully executed.
6. Restore the real watch credential and connect to the user's real hub. Verify authentication, current session/request lists, recent chat history, and Inbox availability without manipulating unrelated live work. Only send or resolve a real test task when that task is explicitly designated for testing.
7. End with the real connection restored and fixture services stopped. Record any untested dictation, background, phone-network, or real-agent execution boundary honestly.

This README describes reproducible checks. The task's actual verification report, rather than this checklist, determines which checks were completed.

## Physical-device test runner

For an already paired watch, `connect-watch.mjs` resolves its changing wireless-debugging port from its hardware serial:

```powershell
$watchTarget = node wear/tools/connect-watch.mjs --serial "<hardware-serial>"
if ($LASTEXITCODE -ne 0) { throw 'Watch discovery failed' }
node wear/tools/run-watch-check.mjs --device $watchTarget
```

Add `--adb <adb-executable>` to both commands if ADB is not on PATH. The helper reuses a matching online TLS transport or connects to the watch's `_adb-tls-connect` advertisement, then verifies `ro.serialno` before returning the target. It handles stale-port advertisements and collision-renamed service instances without querying unrelated phones, restarting ADB, or enabling legacy TCP debugging. The watch must be awake with wireless debugging enabled on the same Wi-Fi; unavailable mDNS produces an explicit error instead of guessing an address. Android's [ADB Wi-Fi architecture](https://android.googlesource.com/platform/packages/modules/adb/+/HEAD/docs/dev/adb_wifi.md) documents the randomly chosen TLS port and mDNS discovery; a fixed legacy TCP port is a different, unencrypted transport.

The additional test APK drives the production app's controls using Android instrumentation. It is not shipped inside the app. Build it with `:app:assembleDebugAndroidTest`, install `app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk`, then run:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address>
```

Use this host wrapper for every mode below; add `--adb <adb-executable>` if ADB is not on PATH. It restores and verifies the original rotation preference only after instrumentation and UiAutomation teardown are confirmed. Timeout/interruption first stops `dev.persona.watch.test`. If the system still identifies this exact runner in `dev.persona.watch`, the wrapper may then stop that target and wait for both sessions to end. Unknown dump formats or a cleanup timeout fail without writing rotation settings. Host SIGINT/SIGTERM and timeout failures return a nonzero exit code. Direct `uiautomator dump` or `am instrument` calls can persist a temporary charging orientation; do not use them outside the rotation guard. An unreadable rotation baseline stops automation instead of guessing a default. The cleanup logic has host-side mocked tests; actual device results are recorded separately in [QA](QA.md).

The default smoke check uses the saved connection and only reads: credential encryption, authenticated session/machine/Inbox refresh, and recent chat retrieval. It reports counts and TLS/pinning flags without printing credentials or conversations.

The optional `live_ping` check sends through the visible composer in one explicitly designated test conversation. First create a dedicated conversation whose initial message includes `Samsung Watch Persona`, `PING_WATCH`, and `PONG_WATCH`, asks for an initial `WATCH_READY`, and asks the agent to answer `PING_WATCH` with only `PONG_WATCH` without tools or other actions. After `WATCH_READY` appears, use that conversation's exact session ID:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode live_ping --session-id <dedicated-test-session-id>
```

This check requires the real saved primary credential, pinned HTTPS, no additional connections, the exact unique primary session and the designated transcript markers. Connection identity and the absence of extra Hubs are rechecked throughout the run, including immediately before editing or sending. It refuses unrelated messages or drafts, clicks Send at most once, and checks for exactly one outgoing ping plus a visible `PONG_WATCH`. Re-running after an accepted ping reuses it without sending again. If its `PONG_WATCH` is still pending, the probe polls for up to 45 seconds, bounded by the 110-second total run deadline. `live_ping_agent_response_timeout` means a ping was observed but no agent reply arrived; `live_ping_ping_not_observed` means the UI attempt produced no observed ping. Read failures and an unconfirmed Send click remain separate errors; none triggers an automatic resend. A passing instrumentation run ends with `INSTRUMENTATION_CODE: -1` and a `PASS` result; the ADB process exit code alone is not evidence of success.

With the synthetic fixture paired, the following checks click the actual question, plan, permission, screen, protected-request, chat and Inbox controls. Every scenario requires the test-only credential, fixture health marker, synthetic machine identity and no additional connections. The guard rechecks connection identity before UI actions and wait steps, including Inbox/recovery. The optional `form_accessibility` scenario checks native input labels and minimum touch heights; compiling it alone does not establish device or TalkBack acceptance.

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode fixture
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode fixture --scenario recovery,cancel_stale
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode fixture --scenario nonvoice
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode fixture --scenario matrix --timeout-ms 240000
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode fixture --scenario credential_loss
```

If a development computer's inbound firewall blocks the fixture, a temporary `adb reverse tcp:18765 tcp:18765` tunnel with fixture URL `http://127.0.0.1:18765` is an alternative. Recreate the tunnel after an ADB reconnect and remove it with `adb reverse --remove tcp:18765` when done. Record these as tunneled synthetic tests, separately from a real Hub's direct LAN connection. Fault-injection scenarios require this loopback fixture URL. Do not weaken the computer's firewall for the test.

The optional `matrix` scenario uses the loopback fixture to exercise the production Home, Agents, Inbox/detail, unsent AI suggestion, chat composer/Sending/receipt, protected requests, Settings, Connections/add form, and failure/recovery screens. It checks form labels and minimum 48 dp input heights. Repeat it at the device's supported default and maximum font scales under both system light and dark appearance; record `fixture_font_scale` and `fixture_ui_mode_night` rather than assuming a configuration took effect. The current acceptance device exposes a maximum scale of `1.3`; this is device-specific, not a universal Wear OS limit. The app deliberately retains its dark appearance under both system settings; this does not add or validate a separate light theme. Review complete round-screen viewports, including scrolled content, and attach the synthetic captures with build/configuration labels to the PR. `matrix_passed=true` and a successful instrumentation result establish the scripted checks, not visual acceptance of every viewport.

Matrix `matrix_*_ms` values are observed upper bounds from production UI actions to the harness detecting their outcomes. They include accessibility polling/settling and, for the Sending/receipt case, an injected fixture delay. `matrix_timings_scope=synthetic_production_ui` identifies this loopback-fixture path: these measurements are not real-LAN latency or agent/model execution performance.

The optional `credential_loss` scenario also requires the guarded loopback fixture. It exercises the production `CredentialStore` AES-GCM save/load logic with an actual Android Keystore key in a disposable alias and a separate private directory. It removes only that alias, verifies that the remaining envelope cannot decrypt, then saves and reads replacement synthetic credentials. It checks the saved primary envelope is unchanged and cleans up its isolated resources; it never deletes the production alias or real credentials. Require `credential_loss_passed=true`, `production_alias_untouched=true` and a successful instrumentation result. This tests isolated key-loss recovery, not account re-pairing after destruction of the user's real key.

Test screenshots are written to the app-private `files/fixture-qa/` directory. `capture-watch.mjs` can also capture the current screen and accessibility tree to a local output basename. Uninstall the test package `dev.persona.watch.test` after acceptance; the Persona app and its pairing remain.

The guarded multi-hub check requires two independent fixture processes, separate loopback ports/reverse tunnels, and no existing additional hub on the watch. It pairs a temporary test group through the real connection form, checks routing when server IDs collide, and removes only that test group afterward:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode multihub --secondary-url http://127.0.0.1:18766
```

The guarded `surfaces` check opens the production Milk Frog and extension-widget pages. It requires a debug build, a saved HTTP loopback fixture connection with the test-only credential, the fixture health marker and synthetic machine/session identities, and no additional hubs. Start the fixture, configure the watch with `http://127.0.0.1:18765`, and establish the reverse tunnel before running it:

```powershell
adb -s <watch-debug-address> reverse tcp:18765 tcp:18765
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode surfaces
```

This mode resets the synthetic fixture to its surfaces scenario. It verifies hashes for all 13 original Milk Frog assets, decoded rendering, and at least three distinct animation frames before and after a tap. When system animation is disabled, it checks the static rendering and reports `pet_motion_enabled=false` instead of claiming animation passed. It also checks all six supported widget item types and all six chart kinds (line, area, bar, pie, donut and candlestick), chart values, omission of executable provider controls, a 503 response that preserves the still-fresh cached projection with a retry error followed by recovery, and a 404 catalog followed by recovery. A retryable error alone must not label an unexpired projection stale or extend its original expiry. The fixture audit must show the injected failures and no unrelated action submissions.

Success requires `surfaces_passed=true` and `INSTRUMENTATION_CODE: -1`. Screenshots use the `surface-` prefix in `files/fixture-qa/`; the result includes asset and observed-frame counts. Record this as a tunneled synthetic app-page test. The runner does not install or select a watch face or configure complication slots, and explicitly reports `system_complication_tested=false`. It clears its injected faults afterward; restore the real connection and remove the reverse tunnel separately when finished.

Passing application-page tests do not by themselves establish that a complication has been selected on the user's physical watch face. Report those checks separately.

## System complication acceptance

Use app version `0.2.1` or later for the cached-pet expiry check below. The [independent QA watch face](qa/watchface/README.md) provides separate `SHORT_TEXT`, `LONG_TEXT` and `SMALL_IMAGE` slots without modifying the user's existing face. It is a temporary test host, not a required part of Persona. Its README contains the build command, package identity and slot locations.

Before starting, record the current face's actual package/instance identity, appearance and selected complications. A shared system runtime component alone may not identify the selected face. Also record the exact original values of any system settings changed temporarily for testing, such as display timeout or animation scale. Save the real Persona connection privately before switching to the loopback fixture. Keep the watch unlocked and charged, and distinguish its visible face from a lock screen or charging overlay.

1. Install the separate QA face and select it through the watch's system face picker. In its real editor, select Persona's Milk Frog data source for each supported slot type. Record the selection, the rendered text/image and a tap that opens the actual Milk Frog page.
2. For extension short- and long-text slots, use the real system picker and the production configuration page. Check cancellation, then explicitly choose a widget and tap Use. Verify each selected slot renders its own cached projection and opens the corresponding widget when tapped. Choosing a widget in one slot must not rewrite another slot's binding.
3. Observe extension cache expiry on the face: stale values should become the refresh prompt after the next face redraw. For this minute-based QA face, wait through the next minute redraw after expiry; do not require a change at the exact expiry timestamp. Opening the widget page and refreshing should update the selected slot. Save the before/after evidence and the actual timing separately from application-page tests.
4. Run the guarded pet-cache injection below only after the system picker has already bound the pet slots. Observe the actual face both before and after the reported expiry, then restore the saved synthetic cache. Do not open Persona or the Milk Frog page during this observation, because a foreground refresh can replace the injected state.
5. Restore the original face and its configuration before uninstalling the QA face. Restore every temporarily changed system setting to its recorded value, restore the real Persona connection, remove the reverse tunnel and test APK, and verify direct-Hub reads separately. Only record outcomes actually observed in [QA](QA.md).

If the real Hub has no published widget, the owner can review the [optional Hub Status provider](qa/hub-status-extension/README.md). It supplies actual aggregate counts with two read permissions and requires a separate owner installation; preparing its package does not complete real extension acceptance.

For a real-Hub run, `real_surfaces` reads the watch's saved primary connection and primes its private caches with actual Hub data. It requires a debug build, a paired non-loopback HTTPS connection with a public-key pin and device identity, and no additional hubs. It fetches the real session snapshot, verifies all 13 pet assets, discovers the widget catalogue, and requests a read-only projection for the first available widget of each accessory type. It does not change pairing, select a face, inject synthetic state, send messages or approve requests:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode real_surfaces
```

`real_surfaces_probed=true` with `INSTRUMENTATION_CODE: -1` means the probe completed; inspect each surface's availability result separately. Output contains only counts, state/family labels and HTTP statuses. `extension_catalog_raw_widgets` distinguishes an empty server catalogue from entries filtered by the watch parser. HTTP 200 with zero raw widgets is a valid empty response, but cannot establish that a populated real extension complication works. The separate extension inventory request may return 403 with the watch credential; this means inventory access was denied, not that no extensions are installed. After the probe, use the real system picker and observe actual face rendering and taps; the runner reports `system_complication_tested=false` and does not replace those checks. Keep real-Hub evidence separate from the fixture results below.

The probe reports `raw_machines`, `raw_sessions` and `raw_interactions` from the original successful JSON responses, without additional requests or logging their contents. The existing `machines`, `sessions` and `interactions` fields describe the parsed watch snapshot: sessions omit quiet, archived and non-assistant sessions outside the supported live states; interactions omit muted, delegated, quiet-owner and elicitation entries. Compare Hub Status's aggregate counts with the raw fields, not the filtered watch list. The extension credential's server scope and the projection's cache timestamp can still differ from the watch read, so record those boundaries when comparing values.

The read-only `complication_parcel` mode checks production timelines through AndroidX's wire conversion and an Android `Parcel` round trip. Prepare the guarded loopback fixture connection with no additional hubs, all 13 verified pet assets and a fresh pet cache, plus real system-selected short- and long-text extension bindings with fresh cached projections:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode complication_parcel
```

Success requires `complication_parcel_passed=true`, `complication_timelines=5`, a positive `parcel_bytes` count and `INSTRUMENTATION_CODE: -1`. It verifies distinct default/entry data objects, validity ranges, data types, tap intents and serialization for three pet types and two extension types. It does not select slots, execute taps or establish visible face rendering; retain the separate physical observations above.

The `pet_expiry` mode requires a debug build, the test-only fixture credential at an HTTP loopback origin, no additional hubs, verified fixture health/data, and all 13 hash-verified pet assets. It backs up only the current synthetic pet cache in app-private storage, writes the synthetic needs-you count `42` with **45 seconds of remaining cache validity**, and requests an update from the already-selected data source. It neither selects a face/slot nor watches the screen:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode pet_expiry
```

`pet_expiry_seeded=true`, `pet_expiry_count=42`, `expires_at_ms` and `observation_window_ms=45000` describe the injection. `SEEDED` and `INSTRUMENTATION_CODE: -1` establish only that setup completed. Physical acceptance additionally requires seeing `42`/the corresponding pet state change to Offline on the already configured face after `expires_at_ms` and the next face redraw, without reopening the app or manually refreshing the provider. The minute-based QA face is assessed after its next minute redraw, not at the exact expiry timestamp. Record each tested slot type; one type does not establish the other types. If screenshots disagree with the physical display, verify the display before declaring success or failure.

Always restore the original synthetic cache before changing the saved connection or uninstalling the test APK, including after an interrupted observation:

```powershell
node wear/tools/run-watch-check.mjs --device <watch-debug-address> --mode pet_expiry_restore
```

`pet_expiry_restored=true` confirms the cache was restored and its temporary backup removed. Restore can run while the fixture server is stopped, but still requires the same guarded fixture connection and refuses a changed identity. A leftover backup blocks another injection until restoration. These modes supply controlled test state; they do not claim system rendering, tap navigation or expiry passed on their own.
