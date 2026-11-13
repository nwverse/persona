# Handoff: #4206 One sidebar (branch `feat/4206-one-sidebar`)

> **Temporary.** Delete this file — and `docs/handoff/4206-one-sidebar.md` and `docs/handoff/4206-one-sidebar-prototype.html`,
> left by an earlier handoff — in a commit on this branch **before opening the PR**. The doc-path check fails while they exist.

Written for an Agent picking this up on another computer. Everything it needs is in this file and the repository; nothing
refers to files outside it.

## 1. Goal and scope

On the iPad and the open iPhone Duo (both postures) one sidebar replaces the Assistant | Agents switch: Persona's places,
Pinned, All Chats and the Agents list in one column, as in the web app. Design and behavior: `docs/apple/ios.md`,
"One sidebar".

- The PR closes: **Fixes #4206 #4852 #4853 #4854 #4855**, and **Refs #3641** (do not close #3641; its reporter has not replied).
- **#5795** (the open agent shows no selected row when it sits in a folded Idle Repositories card) was opened and claimed
  while testing this branch. It is **not fixed here** and not part of this PR.
- Claims: #4206 and #4852–#4855 have `Progress:` comments, and #5795 a `Claim:`, all with **ETA 2026-11-15**. Renew with a
  `Progress:` comment before it lapses (ask the owner first, see section 8).

## 2. Done on the branch

Branch state: merged with `origin/main` up to commit `f81398879` (2026-11-13; merge only, never rebased or force-pushed).
If `main` moves again before the PR, merge it once more (section 6).

The commits of the last stretch (`git log --first-parent`):

| Commit | What |
|---|---|
| `f2187b05c` | One Show Sidebar button at a time: on the open Duo held on its side Persona's page shows the system's bar (New Chat in the side column, #4854), and the split adds its own Show Sidebar there; the page no longer draws a second one. Test `OneSidebarTests.testOneButtonForTheColumnAtATime` (fails before the fix, passes after). |
| `a625755e0` | Merge `origin/main` (kept main's split of `AssistantView`'s stack for Xcode 26.0, #5333; the one sidebar's system bar is its own property `barredRoot`). |
| `e8c18b896` | An open agent is no longer lost when a second agent opens over it: agent rows in the one sidebar were `NavigationLink(value:)`, which in a split's sidebar drives a selection of its own and emptied the page's path; they are buttons now (`AgentsList.openLink`), as the Mac's repo rows. Messages leaves its top corner to the system's Show Sidebar on the Duo's side, like Persona's page. Test fixes. |
| `c250954f7` | Merge `origin/main`: the Recordings row follows #5517 (named Recordings, opens the page); the drawer's `collapse` is left out (the one sidebar folds itself). |
| `06f22a6b3` | Tests after #5587 (the list shows only the current assistant's computers; tests open an agent of the current assistant), the iPad top-strip check samples under the top bar, ReturnToChat scrolls until the row can be tapped and times its tree swap after the pick. |
| `3570fe23a` | **One color down the sidebar, and a foot without a band** (below). |
| `4878dafb3` | Handoff. |
| `ad1f80d94` | Merge `origin/main` (110 commits). Conflicts in `ChatsView` and `RootView` against #5641 (selecting Agents returns to the list): `ChatsView` keeps main's `agentNavigationLink` and folds the one sidebar's `openLink` into it (under the one sidebar a row is always a button that sets the bound path); `RootView` keeps the one sidebar on iOS and main's `selectHomeTab` tab binding elsewhere. `AgentsTabNavigationTests.testNativeTabReselectionAndSwitchBackShowList` skips under the one sidebar (no tabs there); `test_apple_rename.py` expects main's tab binding; `DESIGN_SYSTEM.md` 3.5 names the one sidebar. |
| `011dca2f6`, `72c6d0b43` | Merge `origin/main` again (6 commits, no conflicts; the second brings main's own build fix #5899). |
| `5eaff049d` | **The open Duo's sidebar is 320 pt** (below). |
| `64b5b90ad` | Handoff after the merge and the width change. |
| `234ba0b59` | **The column follows the handle while it is dragged** on the Duo too (below). Refs #2624. |
| `d2b2bf465` | Handoff: the branch quit at launch on a real iPhone Duo. |
| `ea58226d4` | **The Agents list no longer overflows a device's main-thread stack** ("On a real iPhone Duo" below). |
| `fed07d1bd` | **One width, slide only, beside the page on its side** ("The column's one width" below): the handle and its drag-to-resize are gone, `SplitTilePin`. |

### The color fix (`3570fe23a`)

The owner saw three bands down the column in the dark screenshots of the open Duo. Cause, measured from screenshots:

| Part | Where its color came from | Dark |
|---|---|---|
| Rows | the system sidebar's background (`OneSidebarMetrics.padColumn` was iPad-only) | 56,59,63 grey-blue |
| Header (mark, name, fold button) | `.background(OneSidebarMetrics.chromeBackground)` = `Theme.pageBackground` on the Duo | 0,0,0 on its side; 28,28,30 upright (the overlaid column is elevated) |
| Foot (New Chat, Search · Settings) | the same `chromeBackground` | as the header |
| Status-bar strip | `SplitNavigation.topFillColor`, nil on the Duo | as the header |

In light the system's sidebar and the page are both white, so it did not show; the iPad was already one color (#4855).

Now: `OneSidebarMetrics.columnColor` (renamed from `padColumn`) is `Theme.Shell.drawer` on the iPad **and** the open Duo
in the glass styles — **dark 23,23,23, light 249,249,249** — behind the rows, the header and the status-bar strip. The foot
has no background: its buttons float on the column and the rows scroll on behind them (as ChatGPT's iPad sidebar).
Files: `apple/Persona/Views/OneSidebar.swift`, `apple/Persona/Views/SplitNavigation.swift`, `docs/apple/ios.md` (#4855 entry).
The other styles keep their column as it was (the foot is transparent in all styles).

### The width (`5eaff049d`)

The owner found the column too narrow on the open Duo and asked for ChatGPT's width there: ChatGPT's sidebar on the open Duo
upright (669 × 951 pt) is **320 pt** wide (48 % of the screen). Ours had no width of its own and took the system's, about
199 pt upright and 239 pt on its side. Now `Theme.Size.sidebarPhone` = **320 pt** is the open Duo's column in both postures
(`SidebarWidth`); a width dragged on the handle still wins, and a double tap on it gives 320 back. The iPad keeps its
340–480 pt (400 measured), already wider. The narrow-column fallbacks (`ViewThatFits`) stay for a column dragged narrower.
`OneSidebarTests.testTheColumnHasThePlacesPinnedAndAgents` checks the width (320 ± 4 pt on the Duo, at least 320 on the
iPad) and the tests launch with `-agents.list.width 0`. Docs: `docs/apple/ios.md`, "One sidebar" ("The column's width").

### The drag follows the finger (`234ba0b59`)

The owner saw only the handle move while dragging it on the open Duo; the column jumped to the new width on release.
`SplitDividerHandle.applied` held back the live width on a phone until something had been dragged once (`!(phone && width == 0)`,
#2932: the Duo's column then had no width of its own, and one arriving mid-drag built the list again). Since `5eaff049d` the
Duo's column always takes its width through one expression, so the condition is gone and the column follows the finger as on
the iPad. Checked from recordings of a slow drag held 3 s on the Duo on its side: before, the column stayed at 320 pt for the
whole drag and jumped to 420 on release; after, 320 → 349 → 396 → 420 pt while held. The list kept its scroll, its Needs You
filter and More open. The iPad already followed (400 → 421 → 458 → 480 pt).

Following live exposed a misread posture: while the list narrows, the page's edge moves a frame before the list's, and
`notePosture`'s "page starts before the list ends" read those frames as the list laid over the page, so a list dragged closed
on the Duo on its side was not remembered (`SplitDividerTests.testDraggedPastItsLeastTheListClosesAndComesBackFromTheEdge`
failed twice). Laid over now means the page starts in the list's first half.

`SplitDividerTests` got no new assertion for "wider while still held": XCUITest's `press(forDuration:thenDragTo:…thenHoldForDuration:)`
returns only after the finger lifts, so a test cannot read the width mid-drag. The evidence is the recordings' frames above.

## 3. Test status (mock hub with `MOCK_LEADERS=1`, simulators, Xcode 27.1; 26.0 not verified)

On `5eaff049d`; one `build-for-testing`, then `test-without-building` per device and posture.

One `build-for-testing`, then `test-without-building` per device and posture.

| Where | Groups | Result |
|---|---|---|
| iPad (iPad Pro 13-inch) | `OneSidebarTests`, `DuoOpenChromeTests`, `SplitDividerTests`, `IPadLayoutTests`, `IPadSidebarTests`, `TreeSwapFoldTests`, `ReturnToChatTests` | 26 run: **18 passed, 7 skipped, 1 failed** |
| iPhone Duo, Open, upright | `OneSidebarTests`, `DuoOpenChromeTests`, `SplitDividerTests` | 17 run: **16 passed, 1 skipped, 0 failed** |
| iPhone Duo, Open, on its side (`TEST_RUNNER_PERSONA_ORIENTATION=landscape`) | same three | 17 run: **15 passed, 2 skipped, 0 failed** |

Before the width change, on the merge `72c6d0b43`, the Duo gave the same counts upright and on its side.

- Skips are by design: Duo-only tests skip on the iPad; upright-only and beside-the-page-only tests skip in the other posture.
- The one failure, `TreeSwapFoldTests.testHealthReportsStillGoUpAfterAFold`, **also fails on `main`**; not this PR's.

On `234ba0b59` (the drag fix, `SplitNavigation.swift` only), the tests it touches:

| Where | Tests | Result |
|---|---|---|
| iPad | `SplitDividerTests` | 2 run: **2 passed** |
| iPhone Duo, Open, on its side | `SplitDividerTests` | 2 run: **2 passed** (before the posture fix: 1 failed, the closing not kept; failed again alone) |
| iPhone Duo, Open, upright | `SplitDividerTests` and the two upright `OneSidebarTests` (they depend on the posture reading) | 4 run: **4 passed** |

## Known issue, not fixed: on its side, a drag puts the page under the column

> **2026-11-13: addressed in `fed07d1bd`**: there is no drag any more, the column has one width, and `SplitTilePin` keeps a
> phone's split side by side while it is wider than tall. Checked on the iPad on its side; **not yet on the open Duo on its
> side** (its posture needs the Device Hub window, and the Mac was locked). Kept below as the record of the problem.

On the open Duo **on its side**, after the handle is dragged once, the split shows the column **over** the page: the page goes
full width under the column and is dimmed, and stays so (folding and unfolding the column brings it back beside the page until
the next drag). The iPad keeps the page beside the column while dragging.

Steps (mock hub, the Duo opened in Device Hub and turned on its side):

1. Launch on Persona's page with the column out (`-agents.list.closed NO`); the page sits beside the column.
2. Drag the handle on the column's edge 100 pt right and let go; drag it back.
3. The page is now under the column: Persona's face sits where it is with the column folded (x 334 pt instead of 494),
   and the page is dimmed (see `duo-side.mp4`, 50–60 s, with the owner's videos).

Compared on the same steps with a local probe (the face's x after each step): it happens on `234ba0b59`, on `5eaff049d` and
on `72c6d0b43` (before the 320 pt width), so it is **not** from the drag fix or the width change; on `72c6d0b43` the column
had a width kept from an earlier drag. Likely the system's split choosing its overlay display mode when the column's width
changes through `navigationSplitViewColumnWidth`. `SplitNavigation.notePosture` then also reads it as laid over, so a later
closing is not remembered. Left for discussion with the owner.

**The same on a real iPhone Duo (2026-11-13), and why it looked like a device difference.** On the phone the column lay over
the dimmed page on its side from launch, not only after a drag: the app had kept `agents.list.width = 480` (the most,
`Theme.Size.sidebarMax`) from an earlier drag. The simulator screenshots launch with `-agents.list.width 0`, the default.
Relaunched on the phone with `-agents.list.width 0 -agents.list.closed NO` and turned on its side, the column sat beside the
page, as in the simulator. So the phone shows this issue, not a difference of its own; a double tap on the handle gives the
default width back until it is fixed.

## Experiment, not kept: the system's own divider on the iPad

iPadOS 26 lets a split's divider be dragged by itself (WWDC25 session 282). With `SplitDividerHandle` turned off locally and only
`SidebarWidth`'s `navigationSplitViewColumnWidth(min: 340, ideal: 400, max: 480)` left, a slow synthetic drag on the column's edge:

- **iPad**: the divider drags and the column follows the finger (400 → 420 → 441 → 461 → 471 pt while moving), stays where it is
  let go (471, then 351), and stops at the min (340) when dragged to the far left: it does **not** fold the column. Not checked:
  whether the system keeps the width across launches.
- **iPhone Duo on its side**: nothing moves; the column stays at 320 pt (the phone ignores the column's width range).

So on the iPad the system's divider could replace our handle's resizing, but not its closing past the least or its remembered
width, and the Duo still needs our handle. The change was reverted; nothing of it is on the branch.

## On a real iPhone Duo

Tried on 2026-11-13: `58051c95b` and `d2b2bf465` installed on a real iPhone Duo **quit as soon as they open**. **Fixed in
`ea58226d4`**: the branch now launches on the phone, the one sidebar and its Agents list show, upright and on its side.

### Getting a Debug build onto the phone

1. **Signing certificate.** A newly set up Mac has no Apple Development certificate. If the account already has one for that
   machine whose private key is gone (for example after a reinstall), automatic signing stops and asks to revoke it; instead,
   make a new one in Xcode > Settings > Apple Accounts > the team > Manage Certificates > `+` > Apple Development.
2. **Intermediate certificate.** If `security find-identity -v -p codesigning` still shows 0 valid identities, the keychain
   is missing Apple's WWDR G3 intermediate (an old expired WWDR certificate doesn't count). Add it to the login keychain
   (Apple publishes it on its certificate authority page); the identity then shows as valid.
3. **Bundle id and team.** Fill `app.bundle_id` and `app.team_id` in `~/.config/persona/config.json` on that Mac with the
   release app's values, then run `python3 scripts/persona_config.py xcconfig`. This stays on the machine; it never goes
   into the repository. A placeholder bundle id fails to sign: its App IDs have no App Groups, and the profiles it gets
   don't match the entitlements.
4. **Build and install.** `xcodebuild -scheme Persona -configuration Debug -destination 'generic/platform=iOS'
   -allowProvisioningUpdates build`, then `xcrun devicectl device install app --device <UDID> <Persona.app>`. Over Wi-Fi this
   works once the phone has been paired and has Developer Mode on; a cable works too. The build replaces the release app on
   the phone (its data stays). A locked or sleeping phone shows as `unavailable` and can't be installed to.

### The crash on launch and its cause

Every crash report is the same: `EXC_BAD_ACCESS (SIGSEGV)`, `KERN_PROTECTION_FAILURE`, "Could not determine thread index
for stack guard region", about 260 frames, the top under `AgentsList.body` → `observedAgentList` → `styledAgentList` and
whatever that frame happened to call (`StoredValueBox.value`'s KeyPath, or KeyPath metadata). The main thread ran past its
stack: 1 MB on a device, 8 MB in the simulator, which is why no simulator showed it. The `@StoredValue` read at the top is
only the last straw, not the cause.

Not depth but frame size. Measured with lldb on the phone (each frame's own size is the next frame's SP minus its SP), at
the crash:

| Frame | Branch before the fix | `main` (`5474a23c6`) | After `ea58226d4` |
|---|---|---|---|
| `AgentsList.observedAgentList` | 602,752 B | 533,600 B | 113,968 B |
| `AgentsList.styledAgentList` | 105,056 B | 73,488 B | 17,472 B |
| `AgentsList.body` | 57,840 B | 55,840 B | 57,840 B |
| Main-thread stack in use on entering `styledAgentList` | ~1,031,000 B (crash) | 911,072 B | 303,936 B |

`observedAgentList` and `styledAgentList` were generic over the list's full `some View` type and chain about 55 modifiers
(sheets, tasks, `onChange`s, alerts). An unoptimized build gives every layer of that chain a stack slot holding all the
layers under it. `main` already used 87% of the stack at that point (it does not crash: a Debug build of `main` opened with
`-tab dev` stayed up); the one sidebar's section in the list and its conditions made the list's type larger and crossed
the limit. The `SWIFT_OPTIMIZATION_LEVEL=-O` try on the Debug build did not help either, as recorded before; the fix is
in the code, not the build settings.

**The fix (`ea58226d4`, `ChatsView.swift`):** `body` passes the list as one `AnyView`; `styledAgentList` and
`observedAgentList` take `AnyView` (no longer generic); `observedAgentList` erases again before its sheets and before its
tasks. The whole list once, not a row, so `docs/apple/performance.md` P6 still holds.

### How it was checked on the phone

- Device: a real iPhone Duo (`<UDID>`), Debug build signed with the Mac's Apple Development certificate and the
  release bundle id (from a config outside the repository, `PERSONA_CONFIG=<path>`).
- Measuring a frame: launch stopped, attach, break in `styledAgentList`, read the thread's SPs:
  ```sh
  xcrun devicectl device process launch --device <UDID> --terminate-existing --start-stopped <bundle id> \
    --json-output /tmp/launch.json -- -tab dev
  # lldb: device select <UDID>; device process attach --pid <pid>; breakpoint set -r 'AgentsList\.styledAgentList'; continue
  ```
- Crash reports: `xcrun devicectl device info files --device <UDID> --domain-type systemCrashLogs`, then
  `xcrun devicectl device copy from … --domain-type systemCrashLogs --source <name>.ips --destination <file>`. Match a
  report to a build by the `Persona.debug.dylib` UUID (`dwarfdump --uuid`).
- **Screenshots of the Duo's inner display:** `devicectl device capture screenshot` takes the cover display by default,
  which is dark while the phone is open (a black 1398 × 2034 picture). Add `--display-unique-id <inner display id>` from
  `xcrun devicectl device info displays` (the active `LCD-1`, 2007 × 2853).
- `xcrun devicectl device orientation set` does not turn a real phone's interface; rotating means turning the phone.
- Result after `ea58226d4`: a cold launch (the sidebar out, the Agents list's rows drawn), then the phone turned on its
  side and upright again by the owner, twice: the process stayed up and no new crash report came. After the last turn the
  column was folded (not looked into). On its side the column was laid over the dimmed page, not beside it: see "Known
  issue, not fixed: on its side, a drag puts the page under the column".
- Simulator after the fix, iPad Pro 13-inch: `OneSidebarTests`, `SplitDividerTests`, `IPadLayoutTests`, `IPadSidebarTests`,
  `TreeSwapFoldTests`, `ReturnToChatTests`: 21 run, 18 passed, 2 skipped, 1 failed
  (`TreeSwapFoldTests.testHealthReportsStillGoUpAfterAFold`, which fails on `main` too). The Duo's groups were not rerun
  (its posture needs the Device Hub window).

**The phone now** has a Debug build of this branch, which runs; reinstall the release app from TestFlight to go back.

## The column's one width (`fed07d1bd`, 2026-11-13)

The owner's call: no drag-to-resize. The column is shown or hidden, at one width, and only slides; Feishu and ChatGPT never
resize theirs. Why: dragging laid every row out again on every frame.

- **Widths**: iPad `Theme.Size.sidebar` (400 pt), open Duo `Theme.Size.sidebarPhone` (320 pt, the existing value that fits the
  Duo on its side: 951 pt across, the page keeps most of it). A Mac window keeps its range (frozen).
- **Removed**: `SplitDividerHandle` (handle, drags, double tap, VoiceOver resize), the `splitListWidth` environment value,
  `Theme.Split`, `Theme.Size.splitDetailMin`. `<key>.width` is no longer read (old values are simply ignored).
- **Kept**: `<key>.closed` through `SplitListMemory` (the closed state's rules, overlay posture included, are unchanged), and
  `SplitTilePin` (`.tile` while a phone's split is wider than tall; SwiftUI's own behavior back upright, where the overlay is
  by design, #4852).
- **Measured** (iPad Pro 13-inch simulator, iPadOS 27.2, on its side; optimized build `-O`, whole module; mock with
  `MOCK_AGENTS=1 MOCK_LONG_ASSISTANT=1 MOCK_ASSISTANT_SIDEBAR=1 MOCK_TASKS=1`; 5 measured runs, medians, from `FrameStats`,
  as `XCTHitchMetric` gives nothing on a simulator):

  | | fps | hitch ms/s | p95 frame | worst frame | main thread busy p95 | busy frames over 16 ms |
  |---|---|---|---|---|---|---|
  | Before, dragging the handle 100 pt and back ×3 | 28.8 | 519 | 63.3 ms | 711 ms | 54.4 ms | 17 |
  | Before, Hide / Show Sidebar ×3 | 42.2 | 294 | 26.9 ms | 317 ms | 8.6 ms | 30 |
  | After, Hide / Show Sidebar ×3 | 42.5 | 291 | 25.8 ms | 361 ms | 6.7 ms | 29 |

  Hiding and showing was never the slow part and measures the same; the drag, which was, is gone. The simulator's hitch
  numbers are high in every row (its renderer); the main thread's busy time is the app's own share. Not measured on a device.
- **Tests on `fed07d1bd`** (iPad, on its side where it matters): `SplitListTests` 2/2, `PerfSidebarTests` 1/1,
  `OneSidebarTests` 8 passed and 2 skipped (upright Duo only), `IPadSidebarTests` 1/1, `IPadLayoutTests` 3/3,
  `StoredValueTests` passed. `DuoSplitTests` fails 4 of 5 **on `77bf246d8` too** (it expects the system's close-list button
  and search field, which the one sidebar replaced); not from this change, left for whoever owns it.
- **Pending**: the open Duo on its side (Device Hub: Open, Rotate Right), then `SplitListTests` and `OneSidebarTests` with
  `TEST_RUNNER_PERSONA_ORIENTATION=landscape`, and a look at the real phone.

## Right-edge tabs and "Separate sidebars": HTML demo only (2026-11-13)

Not built in Swift yet; an HTML demo for the owner's review is kept outside the repository. Decided so far: icon-only tabs
(助手 = the Persona mark, Agents = `terminal`, 消息 = `bubble.left.and.bubble.right`; badges; tooltips), on the right edge on
its side and on the iPad, a vertical strip in the folded shell (today's `ShellSideRail`, with the face and the page's action),
none upright; tapping a tab on its side opens the column if needed, scrolls to that section and pins it magnetically (80 pt
threshold), and shows that tab's remembered page; the selected tab follows the scroll. 消息 follows Settings › Lark › Show
Messages on Home (`LarkHomePreference.enabled`, `integrations.lark.enabled`). Folded, an opened session keeps Back in the side
column (`ChatView.sideBarItems`), as it already does.

Proposed for review: Settings › Appearance › **Sidebar: Merged / Separate** (default Merged; `AppearanceSection` in
`apple/Persona/Views/Settings/AppearanceSettingsView.swift`, after Agents List Layout, only where there is a sidebar).
Separate = main's layout: each tab its own sidebar, remembered shown or hidden per tab.

## 4. Status

The six screenshots and the montage were retaken on `5eaff049d` (2026-11-13) and checked: the size, one top-left button,
one color down the column (dark 23,23,23, light 249,249,249), and the column's width (iPad 400 pt, the Duo 320 pt upright
and on its side). Three videos of the one sidebar in use (iPad, the Duo upright, the Duo on its side; dark, demo data) were
recorded on `234ba0b59`. All of it lives outside the repository with the owner and is **waiting for the owner's review**;
whether to merge is to be discussed. Nothing has been posted on GitHub; no PR yet.

Putting the Duo into the Open posture needs the Device Hub window (Xcode's Devices window). No command line
way was found: `simctl` has no posture command, `simctl io … screenConfig power` changes a panel's power but not the
posture, `devicectl` only reads the hinge angle, and the device type profile has no posture field (the Duo always boots
Closed). Rotation does have a command (`xcrun devicectl device orientation set --device <UDID> landscapeLeft`), but only
matters once the Duo is Open. So this part needs a logged-in, unlocked session.

## 5. How to finish

Run everything from the repository root unless a step says otherwise.

### Build

```sh
python3 scripts/persona_config.py xcconfig
eval "$(python3 scripts/persona_config.py env)"      # PERSONA_BUNDLE_ID etc., from your build config
(cd apple && xcodegen generate -q)                   # apple/Persona.xcodeproj is generated, never committed
(cd apple && xcodebuild -project Persona.xcodeproj -scheme Persona \
  -destination "generic/platform=iOS Simulator" -derivedDataPath ../build/DerivedData build-for-testing)
```

`build-for-testing` does not install the app; tests install it themselves, screenshots need
`xcrun simctl install <UDID> build/DerivedData/Build/Products/Debug-iphonesimulator/Persona.app` first.

### Mock hub (demo data only)

```sh
MOCK_LEADERS=1 PORT=18842 python3 scripts/mock/mock_hub.py     # any free port; check with lsof first
```

Stop it by its port (`lsof -nP -iTCP:18842 -sTCP:LISTEN -t | xargs kill`), never `pkill -f mock_hub.py` (other Agents).

### Simulators

Only through `scripts/app/sim-boot.sh` (one simulator at a time; the machine is busy):

```sh
DUO=$(scripts/app/sim-boot.sh --device-type "iPhone Duo" "Persona 4206 Duo")
IPAD=$(scripts/app/sim-boot.sh --device-type "iPad Pro 13-inch (M5)" "Persona 4206 iPad")
xcrun simctl shutdown <the other one>
```

### Duo: Open, on its side (needs the screen)

1. Xcode › Window › Devices and Simulators (Device Hub). Select **iPhone Duo** in the list; check the window's title names the
   Duo before every click (another session may be using the same window).
2. Click **Open** (posture), then **Rotate Right** until the inner display is landscape. Verify:
   `xcrun simctl io $DUO screenshot --display=3 /tmp/check.png && sips -g pixelWidth -g pixelHeight /tmp/check.png` →
   2853 × 2007 is on its side, 2007 × 2853 upright (`--display=3` is the inner display, `--display=1` the cover).
3. When done, select again whatever was selected before.

### UI tests

```sh
cd apple
TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18842 TEST_RUNNER_PERSONA_ORIENTATION=landscape \
xcodebuild -project Persona.xcodeproj -scheme Persona -destination "platform=iOS Simulator,id=$DUO" \
  -derivedDataPath ../build/DerivedData test-without-building \
  -only-testing:PersonaUITests/OneSidebarTests -only-testing:PersonaUITests/DuoOpenChromeTests \
  -only-testing:PersonaUITests/SplitDividerTests
```

- `TEST_RUNNER_PERSONA_MOCK_HUB` must be a URL. Upright: drop `TEST_RUNNER_PERSONA_ORIENTATION`.
- A Duo back in Closed makes these tests skip: put it in Open first.
- iPad: same command with `-destination "platform=iOS Simulator,id=$IPAD"` and also
  `-only-testing:PersonaUITests/IPadLayoutTests -only-testing:PersonaUITests/IPadSidebarTests
  -only-testing:PersonaUITests/TreeSwapFoldTests -only-testing:PersonaUITests/ReturnToChatTests`.

### Screenshots (six, demo data only)

Launch Persona against the mock hub in zh-Hans, glass style, sidebar out, then capture:

```sh
U=$DUO; MODE=dark; OUT=shots/duo-landscape-$MODE.png; DISPLAY_N=3     # iPad: U=$IPAD and no --display
xcrun simctl terminate $U "$PERSONA_BUNDLE_ID"
xcrun simctl ui $U appearance $MODE
xcrun simctl spawn $U defaults write "$PERSONA_BUNDLE_ID" agents.list.closed -bool NO
xcrun simctl launch $U "$PERSONA_BUNDLE_ID" -AppleLanguages '(zh-Hans)' -AppleLocale zh_CN \
  -pairURL 'persona://pair?url=http%3A%2F%2F127.0.0.1%3A18842&token=mock' -tab assistant -app.tab assistant \
  -dev.openChat '' -dev.grouping repo -agents.list.closed NO -agents.list.width 0 -ui.style glass
sleep 12
xcrun simctl io $U screenshot --display=$DISPLAY_N "$OUT"
```

Files: `ipad-light.png`, `ipad-dark.png`, `duo-portrait-light.png`, `duo-portrait-dark.png`, `duo-landscape-light.png`,
`duo-landscape-dark.png`. Check each before using it: the size (Duo 2853 × 2007 on its side, 2007 × 2853 upright), the
column's width (the Duo's screenshots are 3×, so 320 pt is 960 px; the iPad's 2×, 400 pt is 800 px), one
top-left button, and **one color down the column** (status strip, header and rows the same: dark 23,23,23, light
249,249,249; between the foot's buttons you see whatever row scrolled behind them). Set the appearance back to light after.
Never commit screenshots or mockups.

### Montage

Save as `m.swift` (outside the repository), run `swift m.swift <screenshot directory> <output.jpg>`. The labels are for the
owner, who reads Chinese.

```swift
import AppKit
let dir = CommandLine.arguments[1], out = CommandLine.arguments[2]
func img(_ n: String) -> NSImage { NSImage(contentsOfFile: "\(dir)/\(n).png")! }
func px(_ i: NSImage) -> NSSize { let r = i.representations[0]; return NSSize(width: r.pixelsWide, height: r.pixelsHigh) }
let gap: CGFloat = 40, label: CGFloat = 70, pad: CGFloat = 50
let row1: [(String,String)] = [("ipad-light","iPad 浅色"),("ipad-dark","iPad 深色"),("duo-portrait-light","Duo 竖握 浅色"),("duo-portrait-dark","Duo 竖握 深色")] // i18n-ignore
let row2: [(String,String)] = [("duo-landscape-light","Duo 横握 浅色"),("duo-landscape-dark","Duo 横握 深色")] // i18n-ignore
let h1: CGFloat = 1100
let w1 = row1.map { let s = px(img($0.0)); return s.width * h1 / s.height }
let W = w1.reduce(0,+) + gap * CGFloat(row1.count - 1)
let w2 = (W - gap) / 2
let s2 = px(img(row2[0].0)); let h2 = w2 * s2.height / s2.width
let totalW = W + pad*2, totalH = pad + label + h1 + gap + label + h2 + pad
let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(totalW), pixelsHigh: Int(totalH), bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
rep.size = NSSize(width: totalW, height: totalH)
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
NSColor(white: 0.93, alpha: 1).setFill(); NSRect(x: 0, y: 0, width: totalW, height: totalH).fill()
let attrs: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: 44, weight: .semibold), .foregroundColor: NSColor.black]
func draw(_ n: String, _ t: String, _ x: CGFloat, _ yTop: CGFloat, _ w: CGFloat, _ h: CGFloat) {
  (t as NSString).draw(at: NSPoint(x: x, y: totalH - yTop - label + 12), withAttributes: attrs)
  img(n).draw(in: NSRect(x: x, y: totalH - yTop - label - h, width: w, height: h))
}
var x = pad
for (i, r) in row1.enumerated() { draw(r.0, r.1, x, pad, w1[i], h1); x += w1[i] + gap }
x = pad
for r in row2 { draw(r.0, r.1, x, pad + label + h1 + gap, w2, h2); x += w2 + gap }
NSGraphicsContext.current = nil
let jpg = rep.representation(using: .jpeg, properties: [.compressionFactor: 0.85])!
try! jpg.write(to: URL(fileURLWithPath: out))
print(Int(totalW), Int(totalH))
```

Row one: iPad light, iPad dark, Duo upright light, Duo upright dark; row two: Duo on its side light and dark, each half the width.

Send the six paths (and the montage) to the owner and **stop** until they approve.

## 6. Before merging (after the owner approves)

1. Delete this file and `docs/handoff/4206-one-sidebar*` in a commit.
2. `git fetch && git merge origin/main` (merge only; never rebase, never force-push), if `main` moved since `f81398879`.
   Resolve conflicts keeping main's structure; rebuild; rerun the three groups on the iPad, the Duo upright and the Duo on
   its side.
3. Local checks: `python3 scripts/test/test_doc_paths.py`, `test_doc_go_paths.py`, `test_doc_sections.py`,
   `test_doc_wire.py`, `scripts/test/test_apple_rename.py`, `python3 scripts/privacy_check.py`; `scripts/ci/apple.sh` where
   it runs. CI on the PR: hosted CI does not run on PRs here; follow `docs/operations/testing.md` ("Hosted CI").
4. With the owner's go-ahead, post the screenshots to #4206, then open the PR from `.github/pull_request_template.md`:

   ```
   Title: feat(apple): one sidebar on the iPad and the open iPhone Duo

   Fixes #4206 #4852 #4853 #4854 #4855
   Refs #3641

   ## What changed and why
   (one sidebar replaces the Assistant | Agents switch; #4852 upright overlay; #4853 top margin on its side;
   #4854 New Chat in the system's side column; #4855 the column's color, now one color on the Duo too; the open Duo's
   column 320 pt, as ChatGPT's there; the one-button, lost-agent and three-band fixes from section 2)

   ## Principles self-check (docs/design/PRINCIPLES.md) and UI change self-check (docs/design/DESIGN_SYSTEM.md)
   ## Performance rules followed (docs/apple/performance.md)
   ## Verification (tests per device and posture, screenshots)
   ## Xcode used (and "26.0 not verified" if not 26.0)
   ```

5. Merge only with `scripts/hooks/merge-pr.sh <PR number>`, then check the issues closed (not #3641).

## 7. Commits

Identity: the GitHub account doing the work and its noreply address. Messages in English, ending with
`Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. `git config core.hooksPath scripts/hooks` runs the
privacy scan on commit and push. Stage only your files; `apple/Persona.xcodeproj/` is generated, never committed.

## 8. Rules for this work

- **Ask the owner before** opening the PR, merging, or posting anything on GitHub (comments, screenshots, closing issues).
- Do not close #3641.
- Claims are comments only (`Claim:` / `Progress:`), never GitHub's assign; current claims run to 2026-11-15.
- Mockups and screenshots use demo data only (the mock hub) and are never committed or pushed.
- Nothing machine-specific in the repository: no tokens, IPs, intranet addresses, host names, user names or local paths.
  Run `python3 scripts/privacy_check.py` before every push.
- Only run the tests the change touches; UI tests in simulators only.
