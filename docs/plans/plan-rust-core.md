# Plan: one Rust core for the iOS, Android and HarmonyOS clients (#6793)

Move the client logic that has no UI (sync and continuity, list state, counts, protocol decoding, and later storage)
out of Swift, Kotlin and ArkTS into one Rust library, and keep each client's UI native: SwiftUI, Jetpack Compose and
ArkUI. **This document is a proposal awaiting the maintainer's decisions** ("To decide" at the end). Nothing here is
built yet; the first code is a vertical slice whose measurements decide whether the migration goes on.

**Who it serves**: everyone who uses the phone apps, indirectly. Nothing the user sees changes. They get the same
counts and the same chat on every phone, fixes that reach every client at once, and less per-chunk work while a
reply streams. Contributors carry the cost: a Rust toolchain in every mobile build.

## Why now, and what it replaces

Decision D1 of the client redesign (`docs/archive/redesign/README.md`) chose native clients that share **specifications
and fixtures**, not code. It deferred a shared core (Kotlin Multiplatform at the time) for these reasons:

1. It would replace iOS's tested Swift data layer.
2. Swift export from Kotlin was alpha, and the types it exported were not `Sendable` under Swift 6.
3. The watch and Live Activity extensions would have to link the shared framework within tight memory limits.

Since then, the picture has changed:

- **A third native client.** The HarmonyOS client (`harmony/`, #5147) is ArkTS. Kotlin Multiplatform cannot target it;
  Rust can, and the client already ships a Rust library: the SSH core in `harmony/native/ssh`.
- **Drift is measurable.** `spec/fixtures/` holds 23 domains, and most are replayed by only some of iOS, Android,
  HarmonyOS, the web app and Go. For example, `continuity` is replayed by Android,
  HarmonyOS and Go but not iOS, and `needs-you` by iOS and Android but not HarmonyOS. Each fix still lands up to three
  times.
- **Duplicated performance work.** Streaming chunk assembly is optimised per client: #4044 on Android, and `StreamChunks`,
  `StreamRepair` and `StreamReveal` in iOS's `PersonaKit`.

How the reasons for D1 change with Rust:

| D1 reason | With Rust |
| --- | --- |
| Replacing tested Swift code | Still true. The migration goes one domain at a time, each behind its fixtures, with a shadow run before the old code is deleted ("Migration rules"). |
| Swift interop maturity | UniFFI generates plain Swift; the slice must prove it compiles under Swift 6.2 strict concurrency with Xcode 26.0 ("Apple"). |
| Extensions' memory | Still true. The watch, widgets, Live Activity and notification extensions do not link the core in this plan ("Apple"). |

If this plan is adopted, D1 is superseded for the domains that move. Domains that have not moved keep D1's rule:
fixtures first, then each implementation.

## Scope

**In scope**: the iOS app (and iPadOS), the Android app, the HarmonyOS app.

**Out of scope**:

- **The web app** keeps its own implementations and its fixture replays. A WebAssembly build of the same core is
  possible later and needs its own issue.
- **The Go hub** keeps its implementations (`attention`, `triage`, `fold-changes`, the continuity corpus) and keeps
  replaying the fixtures.
- **The frozen Mac app** links the core only where shared Swift code requires it, to keep building (`docs/apple/mac.md`).
- **Persona Link and the watch.**

## Architecture

### Layout

```text
native/app-core/                 Cargo workspace, its own Cargo.lock and rust-toolchain.toml
  crates/persona-core/           pure logic: domains, stores, view states, patches. No IO, no async, no FFI, no unsafe
  crates/persona-core-uniffi/    Swift and Kotlin bindings (UniFFI)
  crates/persona-core-ohos/      ArkTS binding (decision 1)
  tests/                         replays spec/fixtures/<domain> through persona-core
  scripts/                       build-apple.sh, build-android.sh, build-ohos.sh
```

Each client gets one adapter module, the only code that imports the bindings:

- **Apple**: a `PersonaCore` Swift package beside `PersonaKit` and `PersonaSSH` under apple/Packages. It holds an
  XCFramework binary target, the generated Swift, and thin Swift adapters.
- **Android**: a `:core:rustcore` module with the generated Kotlin. The `.so` libraries go to the app; a host build of
  the library serves the JVM unit tests.
- **HarmonyOS**: a folder under the entry module's `ets/core` with the generated `.d.ts` and adapters, packaged the way
  the SSH core is.

Generated binding sources are committed, with a check that regenerating them changes nothing. This follows the
repository's precedent for generated code: `apple/Shared/Generated/ExtensionWire.swift`. Compiled libraries are never
committed (the SSH core's rule). One pinned toolchain serves the whole workspace, starting from the SSH core's 1.98.0.

### A deterministic core: actions in, effects and patches out

The core never performs IO and never reads a clock. Everything it needs comes in as an `Action`. Everything it wants
done goes out as an `Effect`, and the client runs that effect through its `PlatformHost`.

```rust
pub struct Core { /* state per hub scope */ }

impl Core {
    pub fn new(config: CoreConfig, host: Arc<dyn PlatformHost>) -> Arc<Self>;
    /// Applies one action; O(changed state). Never blocks on IO.
    pub fn dispatch(&self, action: Action);
    /// Patches for one view since `version`, pulled by the UI on its next frame.
    pub fn drain(&self, view: ViewId, since: u64) -> PatchBatch;
}

pub trait PlatformHost: Send + Sync {
    /// A cheap wake-up with no payload: something changed at `version`.
    fn changed(&self, version: u64);
    /// Network requests, timers, storage writes. Results come back as actions.
    fn run(&self, effect: Effect);
}
```

Why it is shaped this way:

- **Fixtures replay with no fakes.** A fixture's events are actions, and its expected requests are effects. The
  continuity fixtures' `delay_ms: 1500` becomes a `Schedule` effect followed by a `TimerFired` action carrying the
  time, so no test waits on a clock.
- **Effects map onto `PlatformHost`.** Each effect kind is one capability. Adding a capability is an API change that
  is visible in review.
- **Pull, not push, for view state.** `changed` only wakes the client. The UI pulls patches on its next frame through
  CADisplayLink, Choreographer or ArkUI's frame callback, so bursts of changes coalesce into at most one patch batch per
  frame. That gives the 16–32 ms streaming coalescing without timers in the core, and keeps the burst off the main
  thread (`docs/apple/performance.md`, P7).
- **Keyed list diffs.** List patches are insert, update, remove and move by a stable id. They map directly onto
  SwiftUI's `ForEach` identity (P6), Compose's `LazyColumn` keys and ArkUI's `LazyForEach` data-source notifications.
  A full screen's state never crosses the boundary on every change.
- **Threading.** `dispatch` and `drain` are called off the main thread: a serial queue or actor on Apple, a
  coroutine dispatcher on Android, a worker on HarmonyOS. The core guards its state with one lock per hub scope.

What never crosses the boundary:

- **User-facing text.** The core returns codes and enums. Words, dates and numbers are formatted from each client's
  localization resources (`docs/protocol/i18n.md`).
- **Pixels.** Images, video and audio are decoded natively; only URLs, paths and metadata cross.
- **Haptics.** The UI decides them from the state it shows.

### PlatformHost capabilities by phase

| Capability | Phase | Notes |
| --- | --- | --- |
| Timers | 1 | `Schedule { id, delay_ms }` / `TimerFired { id, now_ms }` |
| Transport (HTTP and WebSocket) | 2 | Requests and frames as effects and actions while transport stays native (decision 2) |
| Storage | 3 | Files in each client's per-hub scope directories (D6). SQLite only if decision 4 says so |
| Secure storage | 3 | Keychain, Keystore + Tink, HarmonyOS Asset Store (what the SSH core uses). The core sees opaque references, never key bytes, unless a domain needs them |
| Push registration, Live Activities / live views | never in the core | The core produces the state; the native code delivers it. The Live Activity extension cannot link the core |

### Errors, panics and symbols

- **Panics at the boundary.** Panics never cross FFI. UniFFI turns them into errors on the calling side; the ArkTS
  binding catches them, as the SSH core's `catch_unwind` does.
- **Panic reports.** A `std::panic::set_hook` hook writes a short report, with a backtrace and no user data, into a
  bounded buffer. The client's diagnostics read that buffer.
- **Symbols.** Release builds keep debug information:
  - Apple: the XCFramework's debug symbols go into the app's dSYM.
  - Android: the unstripped `.so` goes to the Play Console's native debug symbols.
  - HarmonyOS: the unstripped `.so` is archived beside the HAP.
  `docs/operations/release.md` gains these steps once the core ships.

## Per-platform notes

### Apple

- **Swift 6 isolation.** App targets default to MainActor isolation. Generated bindings therefore live in their own
  package, `PersonaCore`, which is not MainActor by default. Only that package's adapters touch them.
- **Compiler.** The slice is built with Xcode 26.0 (Swift 6.2), the minimum compiler. The UniFFI version is chosen for
  the Swift it generates under strict concurrency.
- **Where it is linked.** `apple/Shared` is compiled by the iOS, Mac, Vision, watch and widget targets, so `Shared/`
  never imports `PersonaCore`. The core is called from the iOS app's own code and from packages the watch does not
  link, as `PersonaKit` already is.
- **Rust targets.** The iOS device, iOS simulator and macOS targets are standard Rust targets; macOS is needed for
  `swift test`. The watchOS (`arm64_32`, `aarch64`) and visionOS targets were tier 3 in Rust's platform support list
  when this was written, so building them needs a nightly toolchain with `build-std`. Until that changes:
  - The watch keeps its Swift code.
  - Vision Pro keeps its Swift implementation of any domain that moves, behind the same adapter protocol.
  - This is a known cost of the plan (decision 5).
- **Performance.** `docs/apple/performance.md` applies unchanged:
  - No main-thread call into the core does work proportional to a chat or list.
  - Core creation is lazy and off the launch path (P10).
  - The slice attaches before and after measurements from an optimized build (P12).
- **Build entry points.** XcodeGen needs the XCFramework before `xcodebuild` runs, so `scripts/app/sim.sh` and
  `scripts/ci/apple.sh` build it first. The build is cached by the hash of Cargo.lock and the core's sources, so an
  unchanged core costs nothing.

### Android

- **Libraries.** `cargo-ndk` builds `.so` files for the ABIs the app ships: at least `arm64-v8a`, and `x86_64` for
  emulators. They are linked for 16 KB pages, which Google Play requires for apps targeting Android 15 and later.
- **Kotlin bindings.** UniFFI's Kotlin bindings call through JNA, which on Android comes from JNA's `aar`. D1 made
  `:core:domain` pure JVM, so its unit tests load a host build of the library through `jna.library.path`.
- **Size.** Binary size is measured per ABI ("Go / no-go").

### HarmonyOS

- **Precedent.** The SSH core already shows the build: Rust 1.98.0 for `aarch64-unknown-linux-ohos`, the API 26 native
  SDK and offline locked builds. The core reuses its toolchain setup and its license inventory scripts.
- **Binding.** Decision 1 chooses between napi-ohos (ohos-rs) generating the NAPI module and `.d.ts` types, or the SSH
  core's pattern: a C ABI with a small C++ NAPI bridge.
- **Tests.** `harmony/tests` run in Node on the host and cannot load the device library. The fixtures are replayed in
  `cargo test`, and the ArkTS adapters are tested in Node against a fake core. One on-device suite replays the slice's
  fixtures through the real library; the slice must show it runs, following `docs/harmony/device-validation.md`.
- **Two Rust libraries.** The SSH core and the app core each carry a copy of std. Merging them is possible later; it is
  not part of this plan.

## Verification

- **`cargo test`** replays `spec/fixtures/<domain>` for every domain the core implements. From then on, that domain's
  reference implementation is the core. `spec/README.md` currently says "behavior follows the current iOS
  implementation"; its domain table gains a column naming the reference, and moved domains name the core.
- **Each client** replays the moved domain's fixtures through its real binding during the slice, to prove the
  binding. After that, a smoke subset per client is enough, since the logic is one implementation.
- **The web app and Go** keep replaying the fixtures of their own implementations, so all implementations of a rule
  are still checked against one source.
- **A new core lane in `scripts/ci/`** (core.sh) runs `cargo fmt --check`, `clippy -D warnings`, `cargo test --locked`, the
  binding-drift check and the license inventory check. `scripts/ci/apple.sh`, `scripts/ci/android.sh` and HarmonyOS's
  build install the pinned toolchain. `docs/operations/testing.md` ("Hosted CI") gains the lane.
- **Licenses.** Each app ships the notices for the core's Cargo closure, generated from Cargo.lock the way
  `harmony/native/ssh/collect-licenses.py` does.

## Phases

| Phase | Work | Exit |
| --- | --- | --- |
| 0 | This plan; the decisions below | Decisions recorded here |
| 1 | Workspace, toolchain, three bindings, CI lane, and the first domain (decision 3) end to end on all three clients | Every client replays that domain's fixtures through its binding; the go / no-go table is filled in |
| 2 | A stateful domain with view-state patches: `continuity` with the chat's item list, then `sync` | Patches drive one real list on each client within the performance budgets |
| 3 | The domains with the most drift, one issue each: `needs-you` / `attention` / `entries` once #5974 settles the definition, `lines` and `answer` (turn folding, the answer across wakes), `sort` / `recents`, `wire` (tolerant decoding, D5), streaming assembly (#4044) | Each domain: shadow run, then old code deleted |
| 4 | Storage and the snapshot that launch draws from (D6), if decision 4 says so | Cold launch draws from the snapshot before any sync |
| 5 | Transport in Rust, only if decision 2 chooses it | Its own plan |

Every phase after 0 opens its own issue under #6793. Markdown parsing is not on the list: iOS renders markdown text with
MarkdownUI, and moving a syntax tree across FFI for every streamed chunk is a cost to measure before proposing it.

### Migration rules (each domain)

1. **Fixtures first.** The domain's fixtures cover the rules each client implements today. Gaps found while porting
   become new cases, checked against the current implementations. Expectations are never generated from the code under
   test.
2. **Core next.** The core passes them in `cargo test`.
3. **Adapter.** Each client puts its current implementation and the core behind one protocol or interface.
4. **Shadow run.** For one TestFlight and internal-testing cycle, DEBUG and test builds run both implementations and
   report mismatches to diagnostics. A report holds the domain, the rule and the shape of the input, never content.
   Release builds use the core only after the shadow run is clean.
5. **Delete.** The old implementations are deleted, and `spec/README.md` names the core as the domain's reference.

## Go / no-go after phase 1

The slice measures these. The proposed limits are for the maintainer to confirm (decision 6). If any limit is
exceeded and cannot be fixed within the slice, the plan stops: D1 stays, and the result is recorded here.

| Measure | Proposed limit |
| --- | --- |
| Download size added, per platform (per ABI on Android) | ≤ 2 MB for phase 1, reviewed again before tokio or SQLite join |
| Launch to first frame | no change beyond run-to-run noise (`docs/apple/performance.md`, "Budgets") |
| One `dispatch` + `drain` on the slice's data | p99 ≤ 1 ms, and nothing on the main thread over 16 ms while scrolling or streaming |
| Incremental app build with the core unchanged | no Rust rebuild |
| Clean build time added | reported per platform; no fixed limit |
| Contributor setup | `scripts/app/sim.sh`, `scripts/app/android.sh` and HarmonyOS's build still need one command after installing rustup |

## To decide

1. **Bindings.**
   - Recommended: UniFFI for Swift and Kotlin plus napi-ohos for ArkTS. The core's public types derive both through
     `cfg_attr`, so all three typed APIs come from one set of Rust types.
   - Alternative: one C ABI carrying serialized actions, effects and patches, with hand-written Swift, JNI and C++ NAPI
     wrappers in the SSH core's style. That gives one contract and no generator updates, but the glue code is written
     by hand and every call serializes.
2. **Transport.**
   - Recommended: transport stays native, and the core owns protocol decoding and the sync state machines.
     Background URLSession, certificate pinning (HarmonyOS's SPKI pin in ArkTS), system proxies and the iOS SSH tunnel
     (`PersonaSSH`) stay where the OS supports them.
   - Alternative: the core owns HTTP and WebSocket with tokio and rustls, as the issue proposes.
3. **First domain.**
   - Recommended: `continuity`. It is one of the issue's high-drift domains, and Android, HarmonyOS and Go already
     replay it; iOS gains fixture coverage it lacks today. It exercises actions, timer effects and the item store.
   - Alternative: `needs-you`, which is stateless and smaller. #5974 is redefining it right now, though, so porting it
     first would chase a moving rule.
4. **Storage.**
   - Recommended: decide after phase 2, with launch measurements.
   - Alternatives: SQLite (WAL) in the core for every client, or native storage behind `PlatformHost` permanently.
5. **watchOS and visionOS.**
   - Recommended: accept that the watch and Vision Pro keep Swift implementations while their Rust targets are tier 3,
     and revisit when they reach tier 2.
   - Alternative: build them with nightly `build-std`.
6. **Go / no-go limits.** Confirm or change the table above.
