# Apple phase-1 reference evidence (#6871)

This is a point-in-time record, not physical-device or production-core acceptance. The native app still uses `ChatStore`;
PersonaCore is constructed only in the binding benchmarks. No production continuity behavior, screen appearance or
release configuration is migrated by this work. The maintained reproduction instructions and metric boundaries are in
`docs/apple/reference-measurements.md`.

## Toolchain and builds

All measured app runs used Xcode 27.1 (27A9275), Swift 6.4 and Rust 1.98.0 on an arm64 Apple M5 Pro Mac. The app
configuration was optimized Debug (`SWIFT_OPTIMIZATION_LEVEL=-O`, `SWIFT_COMPILATION_MODE=wholemodule`) with
`PERF_SIGNPOST`, not a store Release. Every Xcode invocation used `~/.local/bin/xcode-slot`. Xcode 26.0 is not installed
and remains unverified. Simulator results do not satisfy physical-device gates.

The UI stream, glide and native-memory records use the named iPhone 17 Pro / iOS 27.0
simulator `FD14ED31-119A-4415-BF8D-5DBE25967622`. Their measurement tree began with
`cf119132e9be23c6921424c93d72ce88401de2e7` and `64130151ec49ab7264a467abed55629ee331bc66` and then had harness
corrections through `f75271ea9c5b9ed835cc777125600cad09934e4c`, `bdc575e809d1915178441f8c441231fbe421372e`
and later merges into `84a5a314c55340118980eb43067084ef8f0eff2b`; it was a dirty tree, not one immutable build.
The newer launch and final serial binding/lifecycle run use recreated simulator
`58B70B19-21DA-4051-B017-0D828579958A`, source base `84a5a314c55340118980eb43067084ef8f0eff2b` and the
then-uncommitted per-run launch identity changes. These are separate shared-machine observations, not comparisons
against no-core builds. Raw arrays are retained exactly as exported; local DerivedData is not committed.

## Native UI raw evidence

| File | Source and exact observation |
| --- | --- |
| `raw/native-launch.json` | Unmodified first-frame attachment exported from `/tmp/persona-6871-final-launch-second/reference.xcresult`, file `4D228984-DDC0-4440-86CC-1BB89BEAF0C7.json`. Four distinct run IDs, one warmup and three measured launches. XCTApplicationLaunchMetric values: 5.26995975 / 5.611702416 / 6.161564292 s. Separate root-view appearance: warmup 1479.259 ms, measured 635.704 / 1328.504 / 1689.306 ms. View appearance is not a presented-frame timestamp. |
| `raw/native-stream.json` | Unmodified `/tmp/persona-6871-final-stream-seventh/reference.xcresult` attachment `65EC2E20-8B8F-4F9E-8303-4F1F869C60E3.json`; one warmup + three 24-second recorded Markdown streams. Measured cadence 57.085357 / 56.706046 / 56.789437; custom hitch 48.217557 / 53.996751 / 53.945530 ms/s; longest frame 82.465875 / 97.304333 / 104.829125 ms. CADisplayLink callback cadence is not GPU-presented FPS; custom hitch is not Instruments Animation Hitches. |
| `raw/native-glide.json` | Unmodified `/tmp/persona-6871-final-glide-third/reference.xcresult` attachment `0EE74120-F1FB-44A8-868F-F411D65486E4.json`; one warmup + three actual six-second UIScrollView glides. Measured cadence 56.187845 each; custom hitch 59.436727 / 60.547445 / 61.006830 ms/s; longest frame 58.612875 / 53.400292 / 57.230000 ms; actual offset 6000 / 5999.333 / 6000 pt. |
| `raw/native-memory.json` | Unmodified `/tmp/persona-6871-final-memory/reference.xcresult` attachment `C14E929E-26EC-4B32-98EC-9947E93D6760.json`. One warmed baseline, ten real native chat/list round trips and a final settling observation, sequences 1–12. `phys_footprint` 84,118,528 → 84,724,736 B (delta 606,208 B); `resident_size` 628,326,400 → 632,455,168 B (delta 4,128,768 B). These are distinct metrics; neither is a PersonaCore lifecycle result. |

The glide screenshot was inspected: it shows the synthetic long native conversation and its real scroll content. No
visual or navigation changes are part of the harness. Performance diagnostics are guarded by DEBUG/PERF_SIGNPOST.

The retained launch predates `process_id`, the retained memory predates its single run/process identity, and the
retained stream predates explicit trace-completion metadata. They are historical raw observations, not eligible final
acceptance under the strengthened reporter. The current harness requires those identities and validates the recorded
Markdown subscription, terminal transcript and clear signal; any new measured result must replace the corresponding
old row below before it can be called verified. No historical value is silently edited to look current.

An earlier valid launch on the original simulator recorded XCTApplicationLaunchMetric 2.54269325 / 2.599955042 /
2.529024083 s, with view appearance 454.290667 / 381.413000 / 420.302792 / 390.034625 ms including warmup. Those
numbers remain an explicitly historical observation and are not pooled with the newer recreated-simulator launch.

## Real C binding and lifecycle

`raw/core-boundary.json`, `raw/core-large-page.json` and `raw/core-lifecycle.json` are the unmodified XCTest
attachments exported from `/tmp/persona-6871-final-binding-current.xcresult`. Their original names were respectively
`B2631CA6-A7F1-44D2-985E-2604AC49E25E.json`, `4C86F9B9-DD5E-4709-9313-21CDE326432B.json` and
`FDE5DD35-999C-4CB6-A72A-369EF3F190D0.json`. The export manifest identifies the recreated named simulator and each
benchmark test. All three tests passed serially, with 1,000 raw samples after 100 warmups per timing metric.

| Real C binding metric | p50 / p99 / max (µs) |
| --- | --- |
| Dispatch: generated Swift bincode encoding + handle lock + C queue | 3.625 / 6.750 / 15.500 |
| One-item drain + bincode decode + string-list apply | 3.125 / 6.958 / 15.791 |
| Patch JSON to native Item apply (separate consumer) | 6.625 / 19.375 / 73.167 |
| 100-item drain + bincode decode + string-list apply | 83.667 / 183.667 / 563.333 |
| Native JSON decode + `ChatStore.apply` (non-equivalent diagnostic) | 7.334 / 8.209 / 12.709 |

Worker execution and flush are outside the dispatch/drain timing spans. `loadOlder` is dispatched to an opened chat.
The drain workload has a nonempty patch, not an empty drain. Dispatch measures bincode encoding and queueing only;
the fixture's `hasMore: nil` makes `loadOlder` a normal no-request worker event, not a claimed FetchOlder effect
workload. Worker effects and reducer time are outside the measured span. The patch consumer and `ChatStore.apply`
have different output contracts and asynchronous row/block rebuilding, so this table does not claim an equivalent
core speedup or slowdown. `ChatPatch` lacks original event-span/backlog/moreAfter/opened-fold semantics; an adapter
is phase-2 work.

`raw/core-lifecycle.json` contains one warmup and ten create/open/drain/background-close cycles with weak-release
assertions. Baseline `phys_footprint` is 62,851,952 B and final is 62,868,336 B (delta **16,384 B**). The final
independent report is `/tmp/persona-6871-binding-current-report-second/report.json`: all three tests have complete raw
samples. Final acceptance remains glide-only; these binding/lifecycle reports do not replace that gate. The earlier
failed disk-full build and old lifecycle export are not substituted for this passing serial run.

## Package payload and compressed transport

The unsigned generic-iOS Release + PERF_SIGNPOST comparison used the dirty measurement tree based on
`cf119132e9be23c6921424c93d72ce88401de2e7` plus harness `64130151ec49ab7264a467abed55629ee331bc66`, Xcode 27.1
and the two PersonaCore target dependencies as its intended difference. The with-core link command contains
`-lpersona_core`; the actual no-core link does not. `package-with-core-build.log.gz` and
`package-without-core-build.log.gz` retain both complete original build logs byte for byte after decompression, and
`package-no-core-project.yml.gz` plus `package-no-core-manifest.json.gz` retain the exact generated spec and its
source-project hash. The manifest's source SHA
matches the measurement commit's `apple/project.yml`. The complete dirty source diff and original DerivedData were not
retained, so this is a preserved binary/linkage observation, not a claim of complete source-snapshot reproducibility.
This is not a default Release/store measurement because PERF_SIGNPOST includes diagnostic paths.

The original report is `/tmp/persona-6871-package-report/package-size.json`; the original link-command excerpt and
no-core source manifest are sibling files. Its retained with-core and without-core ZIPs have SHA-256 respectively
`29df55283674ada57586576bee80acdda16f1eee0f7418f33991de123f7a7cc0` and
`aae48553d05749052092f133cc458be71cdbe9e8ea6e47a29bbe5c06622ceef3`. Compressed original-log SHA-256 values are
`363f0f8a5d324b7b4640152633a4b296b9076ad3857fe27b16cc4181ef5870aa` (with core) and
`f4f18995aaa944b1c0eb1eb0d176f7ef638136c52eb6d35c120de4edfc97c70c` (without core). The no-core spec and manifest
GZIP hashes are `c544c98877d875f927fe06318548bc54187dd2fa8c80c77bf5f6bb7197b2354e` and
`77d2dd5c362091f2644893757fea359abe1e76ed6f4f62041b811ac0ce9cd47d`.

| Local quantity | With core | Without core | Delta |
| --- | ---: | ---: | ---: |
| Logical .app payload (B) | 127,845,039 | 127,081,693 | 763,346 |
| Allocated payload (B) | 128,061,440 | 127,299,584 | 761,856 |
| Executable (B) | 75,247,376 | 74,484,032 | 763,344 |
| Local ditto ZIP (B) | 60,856,673 | 60,500,929 | 355,744 |

Independent recomputation used `apple-package-report.py` on payload directories extracted from those intact original
ZIPs. Every archived file entry, executable SHA-256 and every numeric delta matched the original report. The new report
is `/tmp/persona-6871-package-verified-third/package-size.json`. Recompression is not expected to reproduce ZIP bytes
because archive metadata differs; the original ZIP hashes themselves match the report. This verifies the preserved
comparison without rebuilding the deleted DerivedData.

ZIP bytes are a local compressed-transport diagnostic, not App Store processed, thinned or download size. The store
download gate and physical-device acceptance remain open. An earlier ordinary Release attempt at this base failed at
`AssistantProfileView.swift:49` because `screenProbe` was unguarded while its implementation requires DEBUG or
PERF_SIGNPOST. It was not silently fixed here; origin/main later fixed it in `745617e273`.

## Verification commands

```sh
python3 -I scripts/app/test_perf_report.py
python3 -I scripts/app/test_reference_evidence.py  # 6 checks: original hashes and all reported raw statistics
python3 -I native/app-core/scripts/test_apple_measurements.py
python3 -I scripts/test/test_doc_paths.py
python3 -I scripts/test/test_doc_go_paths.py
python3 -I scripts/test/test_doc_sections.py
python3 -I scripts/test/test_doc_wire.py
python3 -I scripts/privacy_check.py

xcrun xcresulttool export attachments \
  --path /tmp/persona-6871-final-launch-second/reference.xcresult \
  --output-path /tmp/persona-6871-launch-raw-export
# The other preserved .xcresult bundles are listed in the raw table above.

python3 -I native/app-core/scripts/apple-package-report.py \
  --with-core /tmp/persona-6871-verified-with-payload/Persona.app \
  --without-core /tmp/persona-6871-verified-without-payload/Persona.app \
  --xcframework apple/Packages/PersonaCore/build/PersonaCoreC.xcframework \
  --output /tmp/persona-6871-package-verified-third
```

A real first-frame run was:

```sh
PERF_SIM=58B70B19-21DA-4051-B017-0D828579958A PERF_ITERATIONS=3 \
PERF_DD=/tmp/persona-6871-launch-ui-dd PERF_OUT=/tmp/persona-6871-final-launch-second \
PERF_SKIP_BUILD=1 PERF_ONLY=PerfReferenceTests/testNativeFirstFrame \
scripts/app/perf-test.sh reference
```

It passed one warmup + three measured launches. `/tmp/persona-6871-final-launch-second/report.json` records the XCTest
metric, device and attachment provenance. The first attempt failed at the old `beginRun` nil application reference;
its crash evidence was retained rather than counted as a sample. The optimized UI build ended in
`** TEST BUILD SUCCEEDED **` in `/tmp/persona-6871-launch-ui-build.log`.

Hosted CI was not dispatched: this repository requires manual CI dispatch. The 38-case/94-step accepted continuity
matrix was not rerun as a substitute for measurement evidence. No TestFlight, App Store, distribution signing or online
release configuration was used.

## HarmonyOS inventory

On 2026-11-14, PATH had no `devecocli`, `hdc`, `ohpm` or `hvigorw`. The ordinary locations
`/Applications/DevEco-Studio.app`, `~/Library/Huawei` and `~/Library/OpenHarmony` had no DevEco/API26 SDK. No
credentials were read and no installation, sign-in, license acceptance or device run was attempted. This inventory
does not establish that no SDK exists anywhere else, and it is not a device acceptance result.
