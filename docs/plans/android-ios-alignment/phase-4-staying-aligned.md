# Phase 4: staying aligned (#4212)

Part of the [Android alignment plan](README.md) (#4194). Status: plan, 2026-11-11.

## Requirements

### Goal

After the one-time work in phases 1 to 3, every change keeps Android and iOS together, or says why not. A reviewer sees both apps side by side without setting anything up, and a behavior change on iOS fails a test before Android drifts.

### R1. Paired screenshots from one command

- One command renders the same scenes from the same mock hub data on the iOS simulator and the Android emulator, in the Glass style, light and dark, with the locale and the clock pinned.
- It writes a gallery: one row per scene, with the columns iOS light, Android light, iOS dark, Android dark, and each row links to its parity-list row.
- The first ten scenes: Persona home, the Agents list, an Agent chat, the assistant chat, the Inbox, Settings, Machines, a pending request, Managers and Model configuration. Next in line: pairing, the terminal, a Lark chat and a mail item.
- It runs on a Mac with Xcode and an Android emulator. It is not part of hosted CI.

### R2. iOS reads the shared fixtures

- Every `spec/fixtures` set that describes client behavior has an iOS test, so an iOS change that breaks a shared rule fails on iOS first.
- Where iOS and a fixture disagree, iOS is the reference (`spec/README.md`): the fixture and Android follow, unless iOS has a bug.

### R3. A self-check line for the other platforms

- The "UI change self-check" in `docs/design/DESIGN_SYSTEM.md` and the self-check in `.github/pull_request_template.md` get one line: "Other platforms: done / issue #N / not applicable (reason)", plus "Parity list row updated".

### R4. Intentional differences in the spec

- `DESIGN_SYSTEM.md` gets an intentional-differences section right after the section 7 matrix. Each entry gives the difference, the platforms, the reason and the decision (date and issue). The matrix's Verdict cells link to it.
- It starts from the list in the [README](README.md) and takes owner decisions 3 and 8.

### Acceptance criteria

- One command produces the paired gallery for at least ten primary screens, light and dark (principle 6).
- An iOS behavior change that breaks a shared fixture fails an iOS test (principle 6).
- The self-check and the PR template carry the other-platforms line.
- `DESIGN_SYSTEM.md` lists the intentional differences, each with its reason (principle 4).

## Technical design

### What exists

**iOS screenshots.**

- `scripts/app/personactl.py`, the DEBUG runtime CLI, can navigate to seven scenes, set the style and the appearance, freeze motion, wait for the page to settle and capture through `simctl io screenshot`.
- `scripts/app/personactl-scenes/id-cli.py` with `scripts/app/personactl-scenes/scenes-id1.txt` shoots those seven scenes (home, Agents, chat, Inbox, Settings, Machines, error) for each style and appearance.
- DEBUG launch arguments in `apple/Persona/App/PersonaApp.swift` open pages directly: `-pairURL`, `-tab`, `-openSession`, `-openAssistant` and others. `docs/operations/testing.md` has the mock hub recipe (pair with `-pairURL …token=mock`, status bar override, dark appearance through `simctl ui`).
- `scripts/app/sim-boot.sh` gives each Agent its own simulator.
- UI tests save images when `PERSONA_SHOTS` is set, but keep no reference images.

**Android screenshots.**

- `scripts/app/android.sh shots` installs a separate package, starts the mock hub through a wrapper that renames contacts and can freeze the clock (`SHOTS_FROZEN`), pairs through `adb reverse` and `debug.mockAddress`, and captures about twenty pages in light and dark, one style per run (`SHOTS_STYLE`).
- `scripts/app/android_shots_diff.py` compares two captures of the same platform pixel by pixel.
- Robolectric tests named `*ShotsTest.kt` draw Compose with fake data and write PNGs, again without reference images.
- Demo mode (`android/core/demo/src/main/kotlin/persona/core/demo/DemoHub.kt`) reads a copy of the iOS demo export.

**The mock hub.** `scripts/mock/mock_hub.py` listens on 127.0.0.1:18760 and takes any token; `scripts/mock/mock_scenarios.py` adds named scenarios. Its clock is the time it started.

**What both can render from the same data today.** From the mock: home, Agents, an Agent chat, the Inbox, Settings and Machines. The assistant chat, a pending request, the terminal, Lark, mail, Managers, Model configuration and pairing have hooks on both but no shared scene list. Two things break equality: the Android wrapper renames contacts, and the two demo exports have drifted (only iOS has Lark conversations, tasks and to-dos; only Android has follow-ups and routines).

**Shared fixtures today.**

| Set | Android | iOS | Hub (Go) | Web |
| --- | --- | --- | --- | --- |
| answer, approval | yes | only through an opt-in Go test that compiles iOS sources | yes | no |
| attention | no | no | yes | yes |
| comments | no | yes | no | yes |
| continuity | yes | no | yes | no |
| entries | yes | yes | no | yes |
| inbox | yes | hand-off memory only | no | yes |
| injected | yes | yes | yes | yes |
| lines | yes | no | no | no |
| needs-you | yes | yes | no | no |
| recents | yes | yes | no | yes |
| sort | yes | no | no | no |
| sync | yes | yes | yes | yes |
| terminal | yes | no | no | no |
| triage | no | no | yes | yes |
| wire | yes | `interactions.json` only | yes | no |
| agent-sharing | a copy in test resources | no | yes | no |

Android finds the fixtures through the `persona.repo.root` property set by the Gradle convention plugins; iOS tests derive the path from `#filePath`, as in `apple/PersonaTests/AgentsNeedsYouFixtureTests.swift`.

**Process.** The PR template asks for light and dark screenshots and a principles self-check, and points UI changes to the "UI change self-check" in `DESIGN_SYSTEM.md`. Neither mentions the other platforms. Section 7 of `DESIGN_SYSTEM.md` is one table (`Topic | Apple | Web | Android | Verdict`); section 8 lists the unintended differences, DS-01 to DS-35.

### Design

**R1, the paired screenshots.**

1. *One data source.* Move the mock wrapper out of `scripts/app/android.sh` (contact renaming, frozen clock, seeded ids) into a launcher both platforms use, and give the mock hub a frozen clock. iOS pairs with `-pairURL`; Android with `adb reverse` and `debug.mockAddress`. Demo mode stays out until the two demo exports match.
2. *A scene manifest.* A new file under `scripts/app/` maps each scene to its iOS hook (a `personactl` navigation or launch arguments) and its Android hook (debug extras such as `debug.openTab`, `debug.openSession`, `debug.openPage`, `debug.openRequest`). It replaces `scenes-id1.txt` and the hard-coded page loop in `android.sh shots`.
3. *The iOS runner* does what `id-cli.py` does: boot through `sim-boot.sh`, override the status bar, launch with the locale pinned (`-AppleLanguages`), navigate, set the Glass style and the appearance, freeze motion, wait to settle, capture.
4. *The Android runner* reuses the capture step of `android.sh shots` with `SHOTS_STYLE=glass` and `SHOTS_FROZEN`, reads the scenes from the manifest and pins the app's locale.
5. *Device sizes.* An emulator profile close to the reference iPhone's width in points (the iPhone 17 Pro that `sim-boot.sh` creates by default), so layouts compare at the same width.
6. *The gallery* is a static HTML page in the output folder (not committed): one row per scene, four images, the scene name and its parity-list link.
7. *The command* is a new script under `scripts/app/` that takes the simulator, the emulator serial and the output folder, runs both sides and writes the gallery. Like every waiting script here, it has a timeout and ends with a one-line verdict.

`android_shots_diff.py` stays for regressions within one platform.

**R2, iOS fixture tests.** Add XCTests for the sets iOS doesn't read: continuity, lines, sort, terminal, answer and approval (replacing the opt-in Go check), all of wire, and the inbox list. Locate files the way `AgentsNeedsYouFixtureTests.swift` does. Where the logic sits inside a view, move it into a plain function in `apple/Shared` first. Each failure is decided case by case: iOS is the reference, so the fixture and Android usually move. attention and triage describe hub and web behavior, and agent-sharing is a hub contract, so they stay where they are.

**R3, the self-check line.** One item in the "UI change self-check" section of `DESIGN_SYSTEM.md` and one checkbox in `.github/pull_request_template.md`.

**R4, the intentional-differences section.** A subsection right after the section 7 table, seeded from the README. The Verdict cells for styles, the 48dp floor, toasts and icons link to it.

### Order of work

1. R3 and R4: small documentation PRs.
2. The shared mock launcher and the scene manifest, with the Android runner on top.
3. The iOS runner and the gallery.
4. The iOS fixture tests, one set per PR.

### Risks

- Simulators and emulators are slow and flaky. Keep the run local, wait for settled pages rather than fixed sleeps, and allow re-running one scene.
- Text differs where the data differs. Use the mock only, with the same renaming on both sides.
- Two toolchains on one Mac: `sim-boot.sh` and its own emulator per Agent, as the testing docs require.
- A small doc drift found on the way: `docs/operations/testing.md` calls the Android styles "Glass / Morning / Board", but the code says Brief. Fix it in the first PR here.
