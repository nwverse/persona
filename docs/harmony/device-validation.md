# HarmonyOS device and integration validation

Use an isolated Hub, synthetic tasks and dedicated Agent profiles. A passed portable test, an SDK probe and a screenshot establish different facts; retain the command output and exact HAP hash for each run. Never use a personal workspace's messages, credentials or external recipients as fixtures.

## Device matrix

|Category|Required cases|Layout decision|
|---|---|---|
|Straight phone|Root list, chat, decision, file preview, keyboard, 200% text, background/foreground|Use the actual native compact window class|
|Mate X-style conventional foldable|Cover, expanded portrait, expanded landscape, half fold, rotation with an unsent draft|An expanded MD window remains a native stack|
|Pura X Max-style wide foldable|Native split, drag the master width, collapse/restore, select/filter/scroll the current Agent, child navigation|LG/XL use native Navigation split; title follows whether the selected row identifies the detail|

The repository's [native screenshots](screenshots/README.md) record measured pixels, density and window class. An emulator lacking a valid crease region cannot establish hover or hinge avoidance. Verify that capability separately on an image or device that supplies real geometry.

Across the matrix, inspect Glass/Brief/Board, light/dark, English/Chinese/Japanese, system font scale and the keyboard. Reopen the application after an SDK runner exits before collecting UI evidence. The runner may close the main Ability. Restore the saved main HAP after building a test HAP if the build replaced its default output artifact.

## Environments to prepare

These integrations need their own environment before their end-to-end behavior can be accepted. Their unavailability is not a passing result and does not justify omitting a client implementation.

|Environment|Preparation|Acceptance observation|
|Other Agent runtimes|A supported CLI runtime on an owned trusted computer, configured launcher/model, disposable repository|Create a session, stream a response, answer its question/permission/plan, stop, disconnect and recover without replay|
|Voice extension|A configured compatible voice service and V1/V2 endpoint, microphone permission and audio output|Negotiate the implemented version and target; record only after ready; interrupt/background/reconnect without changing the admitted run or silently resuming capture|
|External Inbox channels|Dedicated test connector/account and explicit synthetic recipient/thread, including attachments|Read acknowledgement covers visible content; confirmed reply sends once; failure preserves the draft; child updates refresh the parent|
|Mac screen sharing|An owned Mac with screen-sharing permission/TCC and a supported Hub/node screen route|Read-only viewing, explicit control, coordinate mapping, disconnect/revoke releases all input|
|System photo editor|A device/image with a registered `photoEditor` handler and a synthetic image|Read-only URI grant, original unchanged, returned new copy staged only in the captured conversation|
|Camera and sensors|Physical camera/location/sensor hardware and permission combinations|Grant, deny, revoke, background and cancel; no action continues after its target or permission retires|
|Fold geometry and accessibility|A real conventional/wide foldable with crease geometry, screen reader and system input methods|Native hover keeps actions away from the hinge; draft/focus/selection survive; all actions are reachable and spoken correctly|
|Background notifications|Device-qualified notification capability with grant/deny, lock screen and process termination|Open the captured route and fetch current facts; expired requests never execute from stale notification content|
|Release performance|An optimized signed build on a named physical phone and foldable|Record the measurements below; emulator speed is not a physical-device performance result|

Local ChatGPT-authenticated Codex, native storage/transport/SSH, recording and Mandarin on-device recognition, and native contact/calendar writes can be tested independently of those external services. Use the [client guide](harmony.md#verification) and checked-in fixtures. Contact/calendar mutations must use owned temporary entries and remove only the IDs created by the run.

## Physical performance procedure

Record device, OS/API, build mode and hash, refresh rate, temperature, fixture size and profiler version. Use 200 sessions and 10,000 messages for the retained-list/transcript workload. Run at least 30 measured samples after a separate warm-up; report the distribution and p95, not only a best run.

- Cold local interactivity: process stopped, saved local state present; target p95 at most 2 seconds. Record network readiness separately.
- Warm local interactivity: background then foreground; target p95 at most 1 second.
- Local send echo: input submission to the optimistic native message row; target p95 at most 100 ms. Record server first-token time separately.
- Scroll/typing: collect App, RenderService and GPU frame timings. At 60 Hz target p95 at most 16.7 ms and fewer than 5% of frames exceeding two frame intervals; use 8.3 ms for a 120 Hz target.
- Repeat split drag, fold/rotation, large-text IME and a streaming update while an input is focused. Retain memory and dropped-frame evidence alongside screenshots.

These are qualification targets. Do not present them as measured results without the recorded run. Store signing identity and service credentials outside the repository. AppGallery upload, submission and optional system-service entitlements require a separate release workflow.
