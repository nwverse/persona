# Apple reference measurements (#6871, part of #6793)

This document records test-only phase-1 measurements, not production core integration. PersonaCore is linked into the
iOS App but is constructed only in its binding tests. The shipping chat remains the native `ChatStore` and native UI.
No core is created on the launch path, and no screen, continuity behavior or release configuration changes here.

## Reproduce

Create an owned simulator through `scripts/app/sim-boot.sh` (never `simctl create/boot` directly). Record its exact
model, runtime and UDID. All Xcode invocations go through `~/.local/bin/xcode-slot`.

```sh
SIM=$(SIM_RUNTIME=27.0 scripts/app/sim-boot.sh "Persona 6871 Reference Measurements")
PERF_SIM="$SIM" PERF_ITERATIONS=3 PERF_DD=/tmp/persona-6871-ui-dd PERF_OUT=/tmp/persona-6871-ui-memory \
  PERF_ONLY='PerfReferenceTests/testNativeChatTenOpenCloseMemory' scripts/app/perf-test.sh reference
# Use PERF_SKIP_BUILD=1 with the same optimized build for the other PerfReferenceTests cases.
PERF_SIM="$SIM" PERF_ITERATIONS=3 PERF_DD=/tmp/persona-6871-ui-dd PERF_OUT=/tmp/persona-6871-ui-stream \
  PERF_SKIP_BUILD=1 PERF_ONLY='PerfReferenceTests/testNativeStreamingTrace' scripts/app/perf-test.sh reference
PERF_SIM="$SIM" PERF_ITERATIONS=3 PERF_DD=/tmp/persona-6871-ui-dd PERF_OUT=/tmp/persona-6871-ui-glide \
  PERF_SKIP_BUILD=1 PERF_ONLY='PerfReferenceTests/testNativeMainConversationGlide' scripts/app/perf-test.sh reference
PERF_SIM="$SIM" PERF_ITERATIONS=3 PERF_DD=/tmp/persona-6871-ui-dd PERF_OUT=/tmp/persona-6871-ui-launch \
  PERF_SKIP_BUILD=1 PERF_ONLY='PerfReferenceTests/testNativeFirstFrame' scripts/app/perf-test.sh reference
```

The harness uses optimized Debug (`-O`, wholemodule, `PERF_SIGNPOST`), not a store Release. First frame uses
`XCTApplicationLaunchMetric` and a separately labelled didFinishLaunching-to-root-view-appearance observation; these
are not the same interval. Streaming uses the recorded Markdown trace, with one warmup and three measured runs. The
scroll probe drives the real UIScrollView at a constant speed without accessibility gestures in the measured window.
CADisplayLink callback cadence is not GPU-presented FPS. The custom hitch value is not Instruments Animation Hitches.

For the real C binding, build and run only the optimized benchmark target, with no concurrent UI-test runner:

```sh
eval "$(python3 scripts/persona_config.py env)"
~/.local/bin/xcode-slot native/app-core/scripts/build-apple.sh
~/.local/bin/xcode-slot timeout 1800 xcodebuild test -project apple/Persona.xcodeproj -scheme Persona \
  -destination id="$SIM" -derivedDataPath /tmp/persona-6871-core-dd \
  SWIFT_OPTIMIZATION_LEVEL=-O SWIFT_COMPILATION_MODE=wholemodule \
  'SWIFT_ACTIVE_COMPILATION_CONDITIONS=$(inherited) PERF_SIGNPOST' -parallel-testing-enabled NO \
  -resultBundlePath /tmp/persona-6871-core.xcresult \
  -only-testing:PersonaTests/PersonaCoreReferencePerformanceTests
```

Dispatch includes generated Swift bincode encoding, the handle lock and the C queue call; worker execution and flush
are outside its span. Drain + bincode decode + string-list apply is measured after the worker is ready; the 1 ms budget
is not evaluated on empty patches. A separate patch-JSON-to-native-Item metric exists but is not a ChatStore adapter:
`ChatPatch` does not preserve original event spans, replace/older/unfold semantics or opened-fold state. The native
JSON decode + `ChatStore.apply` column is a native input diagnostic, not an equivalent core speedup/slowdown proof.
Every metric has 100 warmups and 1,000 raw samples. p99 uses `floor((n - 1) * .99)`, matching the Android reference.

The no-core generator removes only the App and test target's PersonaCore dependencies. It does not edit the normal
project. For a local unsigned Release comparison, run `native/app-core/scripts/apple-size-reference.sh
/tmp/persona-6871-package-output`. It writes both build logs, a link command containing `-lpersona_core`, file-level
hashes, Mach-O accounting, logical payload bytes and locally zipped bytes. The ZIP is not App Store download size.
No App Store Connect processing, per-device thinning, distribution signing, archive export or upload is performed.
Xcode 26.0, physical devices and actual store-download sizes remain separate open cells.

Stop the owned mock by its recorded port and the owned simulator after each suite. Keep small JSON evidence, not large
DerivedData or compiled libraries, in the PR.

## Recorded evidence

The selected raw arrays, original XCTest export manifests, exact build provenance and verification commands are in
`docs/archive/apple/reference-measurements-6871/README.md`. It distinguishes the original simulator
`FD14ED31-119A-4415-BF8D-5DBE25967622` from the recreated `58B70B19-21DA-4051-B017-0D828579958A`; they are not
pooled into one launch median. Current build SHA, dirty-harness state and toolchain are explicit for every record.
The archived JSON files are unmodified exports, not hand-entered statistics. Both variants in the package comparison
use Release + PERF_SIGNPOST; this is a performance-configuration linkage diagnostic, not a default Release/store build.

The ordinary Release compilation failure at the measurement base was `AssistantProfileView.swift:49`: an unguarded
`screenProbe` call whose implementation requires DEBUG or PERF_SIGNPOST. Both size variants therefore used the same
PERF_SIGNPOST condition; this PR did not silently patch production code to obtain a comparison. Origin/main later
addressed that call site independently in `745617e273`.

Physical-device acceptance is blocked by the maintainer's 2026-11-14 decision; it is not inferred from a simulator.
Xcode 26.0 is not installed here (only Xcode 27.1 is), and store-processed/thinned/download bytes were not measured.
The HarmonyOS inventory found no `devecocli`, `hdc`, `ohpm` or `hvigorw` on PATH and no DevEco/API26 SDK at the
ordinary `/Applications/DevEco-Studio.app`, `~/Library/Huawei` or `~/Library/OpenHarmony` locations. No credentials,
installation, sign-in, license acceptance or device run was attempted.
