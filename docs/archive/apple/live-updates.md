# Live updates must not interrupt user actions (2026-10-31)

User: "There is another issue with refreshes caused by dynamically fetched data: they can interrupt what the user is doing. For example, in Agents, I tap More and am about to choose a machine, but a refresh
makes the list jump to the top. While viewing an image from the execution process, an update to the latest Agent progress can also interrupt it and close the image. This rerendering must have a performance cost too, right?"

## Test method

`LiveUpdateTests` (`docs/operations/testing.md`): the mock hub's `/mock/churn` simulates a busy hub, pushing 12 rounds of updates every 400 ms:

- Each machine's session list: working Agents get a new latest message, with two taking turns as the most recent.
- Machine list: `last_seen` changes.
- Each open chat gains new steps, with a message every 4 rounds.

Each round has 5 pushes total. Assertions check that ongoing user actions are not interrupted. The App launches with `-perfCounts YES` and records two things:

- How often each view's body is evaluated.
- Time from each push until the main thread is idle (also emitted as os_signpost, visible in Instruments' Points of Interest).

All numbers are from a Debug build on a simulator; the focus is the before / after ratio. Before-fix numbers use the same test and counters on origin/master.

## Results

| Ongoing action | Before | After |
| --- | --- | --- |
| ⋯ menu open, scrolled to machines | Push resets the menu to the top (option jumps from 467 pt to 807 pt) | Stays put |
| Row's long-press menu open | Stays open, but menu content is rebuilt about 100 times per round (across every row's menu) | No rebuild |
| Viewing a process screenshot full-screen while the Agent keeps working until the turn ends | Stays open while new steps arrive, but **closes when the turn ends (the final reply arrives)** | Stays open throughout |
| Process card expanded, list scrolled midway, typing in search, row swiped left to show Close | Not interrupted | Not interrupted |

Main-thread time and body evaluations per push round:

| Scenario | Main thread / round (before → after) | Extra body evaluations per round (before → after) |
| --- | --- | --- |
| Agents list, long-press menu open | 157 → 45 ms | List 7 → 3, long-press menu content 96 → 0, ⋯ menu content 7 → 0, filter bar 7 → 0, both tabs 3 → 0 each |
| Agents list, scrolled midway | 194 → 76 ms | Long-press menu content 185 → 0, others as above |
| Agents list, searching | 154 → 35 ms | Long-press menu content 103 → 0, others as above |
| Chat, process card expanded | 182 → 50 ms | Process steps 133 → 1.2, chat page 7 → 3.6, Agents list behind it 7 → 3 |
| Chat, image full-screen (6 steps per round, turn ends in the last round) | 223 → 56 ms | Process steps 67 → 6, thumbnails 8 → 0, chat page 8 → 4 |

## Causes and fixes

- **⋯ menu resets to the top:** the menu is in the Agents list toolbar. Every push reevaluates the list, resubmitting its toolbar to UIKit; the open menu is laid out again and its scroll position resets.
  Keeping menu content unchanged is not enough: even after content stopped rebuilding, the menu still jumped.
  - `ChatsView` now handles only navigation, search, and the toolbar, without reading session data. The list is `AgentsList`.
  - The menu's machines use `AppModel.machineChoices`, containing only names and icons. It changes only when machines are added, removed, or renamed; pushed `last_seen` changes do not count.
  - After the split, search sits on `AgentsList`, immediately after the filter bar, preserving the original order. `ChatsView`'s body must read `searching`:
    opening a chat from search results closes search first and pushes on the next cycle, so navigation must reevaluate too. Without that read, the opened chat retains the system navigation bar and `SearchOpenTests` fails.
- **Long-press menus:** each list reevaluation evaluates `.contextMenu` content for every row. This is now `AgentRowMenu`, considered equal while its displayed values (pinned, muted, looping, online) are unchanged.
  Menu actions fetch the "current session" when invoked. The space header's ⋯ works the same way (`SpaceHeaderMenu`).
- **Image viewer closes:** the full-screen viewer was originally presented by the tapped thumbnail itself.
  - The thumbnail lives in a lazy-list row. As the turn progresses, the list follows the latest step and drops distant rows; when the turn ends, work rows are reordered and rebuilt. Once the row disappears, so does its viewer.
  - The chat page now presents the viewer (environment value `viewImages`), with zoom-transition sources distinguished per image.
  - Images in user messages (`SentPhotos`, `AttachedImages`) use the same approach.
  - Tool-row expansion also originally lived in the row (`ToolCallRow.expanded`), collapsing when rebuilt. It now lives on the page (`openTools`), like process-card `openWork`.
- **All process steps reevaluate on every push:** the chat page passed actions down with patterns such as `.environment(\.openFile) { … }`, producing a new closure on each reevaluation.
  SwiftUI cannot tell that it is unchanged, so every row reading these environment values reevaluates too. A `ChatActions` instance is now created once per chat page; its closures forward to the handlers set for the current render.
- **One session's page reevaluates for all sessions:** `model.session(key)` originally read the entire index, so a session-list update from any machine counted as a change.
  Each session now has an observable cell (`SessionCell`) that notifies only when that session actually changes.
- Identical pushed `machines` lists are no longer assigned again.

## Unchanged

- Lists scrolled midway, expanded process cards, search, and left swipes were not interrupted by these pushes and were left alone.
- A newly pinned "Needs you" card pushes content below it downward. It needs immediate attention, so it is not buffered.

## It came back (2026-11-01, TestFlight 70)

User: "The machine-switch refresh issue and refreshes while scrolling in chats that we fixed yesterday seem to be back."

### Why tests missed it

- The pushes above covered only session lists, machine lists, and new chat steps. A real hub pushes two other kinds all day:
  - Inbox changes (new Lark messages).
  - Pending interactions (one Agent asks a question, another gets answered).
- "Badges" read these: the Agents and Inbox counts on the old tab bar, and the red dot on ☰ / count on "Agents" in the new shell. The mock never pushed either, so tests stayed green.
- At the time of this investigation, the shell (fd9c99e) was the default iPhone navigation, but the UI-test helper forced tab navigation. These tests had never run under the shell. Current helpers must exercise default navigation instead.

### Cause

Both layouts share the cause: badges live on the view containing the page, so a badge change rebuilds the page or menu.

| Layout | Badge reader | On badge change | Result |
| --- | --- | --- | --- |
| Historical tab navigation | Each `Tab` | `Tab` reevaluates and recreates its page: `ChatsView` and `AssistantView` bodies run again (`Tab` content is not compared by value) | Agents toolbar is resubmitted to UIKit; ⋯ menu resets to the top. The open Persona chat reevaluates in full (27 times in 8 seconds) |
| New shell | Bodies of both top bars | Entire top bar reevaluates | The "Agents" button is the machine menu (`Menu`), with the count in its label. A count change changes the label, relaying out the open menu and resetting it to the top. The Persona top bar and "Assistant" menu rebuild on every push |

### Fix

- Historical tab navigation: wrap each `Tab` page in `TabPage { … }.equatable()`.
  - The page takes nothing from `Tab`, so a `Tab` reevaluation need not rerun its body.
  - State, environment, and models read by the page itself still update it normally.
- New shell:
  - Move the count into a separate `WaitingBadge` view that reads `agentsWaiting` itself. It sits beside the menu in a ZStack, not in the menu label, so `ShellSwitch` no longer receives the count.
  - Make `ShellCircleButton(inboxDot: true)` read the Inbox for ☰'s red dot itself; the top bar no longer reads it.
  - Make `AgentsTopBar` `Equatable`: each `ChatsView` reevaluation gives it a new closure, but that no longer rebuilds it.

### Results

Tests: the mock's `/mock/churn` gained two parameters.

- `&inbox=1`: change an Inbox item each round, vary the "Needs you" count every other round, and repush pending interactions. Every `LiveUpdateTests` case includes it.
- `&waiting=1`: add an Agent waiting for you each round, then answer it the next round. The count changes every round, and the top card appears / disappears. Menu cases include it.

The results below record both historical navigation implementations. Current UI tests launch the default shell at compact iPhone width and the sidebar at regular iPad width, without a navigation override.

"Before" below is origin/master, using the same tests.

| Scenario | Layout | Before | After |
| --- | --- | --- | --- |
| ⋯ menu open, scrolled to machines, 12 push rounds | New shell | Menu resets to top (469 → 809 pt); menu content rebuilds 1.1 times per round | Stays put, 0 |
| ⋯ menu open, scrolled to machines, 12 push rounds | Historical tab navigation | Menu resets to top (467 → 807 pt); `ChatsView` and menu content rebuild 2.5 times per round; main thread 535 ms per round | Stays put, 0, 121 ms |
| Persona chat open, 14 push rounds in 8 seconds | Historical tab navigation | `AssistantView` / `ChatView` / top bar each 27 times; total push main-thread time 158 ms | Page and top bar 0 (`ChatView` still 9, for steps the mock adds to this chat), 45 ms |
| Persona chat open, 14 push rounds in 8 seconds | New shell | Top bar 40 times, `ShellSwitch` (containing the "Assistant" menu) 39 times, 41 ms | 0, 14 ms |

### Not reproduced

Recorded the new shell and inspected frame by frame (`simctl io recordVideo`, frames extracted with custom AVFoundation code):

- Selecting a machine and switching back to all machines with these pushes showed no flicker or jumps.
- Scrolling back in Agent and Persona chats, then stopping, left the view completely still under pushes.

The historical tab navigation's whole-chat reevaluation from badges is fixed. The new shell's chat page itself did not reevaluate with badges, and "refreshes while scrolling" did not reproduce with the new-shell mock. If it persists after the fix, record against the real hub.

### Test pitfalls

- The new shell keeps both pages mounted. The hidden page remains in the accessibility tree, yielding two "Agents" buttons and two `chat.scrollState` probes.
  - Tests pick the tappable button.
  - Probe labels end with `key=<machine>/<session>`, allowing selection by session.
- While a menu is open, XCUITest reading the `perf.counts` probe itself resets the menu to the top. Measure menu position before reading counters.

## Forced refreshes while scrolling (2026-11-01, TestFlight 77)

User: "Forced refreshes while scrolling the list hurt the experience." TestFlight 77 already included the "It came back" fixes above. Those tests had not reproduced the scrolling issue with the mock,
so investigation started against the real hub.

### What the real hub pushed

A read-only proxy in front of hub recorded pushes. GETs were forwarded; other requests returned `{}` without being sent; WebSocket forwarded only subscribe, unsubscribe, and pong upstream.
This changed none of the user's state and did not register the simulator as a phone. A 10-minute recording contained 554 messages:

| Push | Per minute | Notes |
| --- | --- | --- |
| `sessions` | 25.5 | Median interval 1.85 seconds; each carries new `rev` / `updated_at`; about one fifth change the last message |
| `revs` | 15.6 | |
| `fleet` | 6.9 | |
| `lark` | 5.7 | |
| `hook_event`, `inbox` | About 0.7 each | |
| `machines` | 0.1 | Only the initial connection message in 10 minutes |

The Agents list orders spaces by "state + last update." Applying the App's ordering rules to the 255 recorded `sessions` messages, 93 (36%) changed space positions.

### Measurement

- With the App launched using `-perfCounts YES`, `PerfScroll` inspects dragging / decelerating scroll views frame by frame (`docs/operations/testing.md`, "Pushes during scrolling").
  - It records several anomalies: whole-content jumps, replaced / redrawn rows, relative movement between rows, and interrupted inertia.
  - Each anomaly is attributed to the push type received within the preceding 0.6 seconds.
- On real data, drag and flick each of five lists for 60 seconds: Agents list, Agent chat, Persona chat, knowledge base, Machines page.
  Run once with pushes enabled, then again with the proxy holding pushes (allowing only new steps in the open chat). Only the difference is caused by pushes.
- Jumps under 100 pt occur in both runs, caused by lazy rows determining their heights and UI-test-generated drags; they do not count.

Before the fix, real hub, new shell:

| List | Pushes enabled | Pushes held |
| --- | --- | --- |
| Agents list | Maximum jump 558 pt, visible rows replaced 5 times, list reevaluated 9 times while scrolling | Maximum 45 pt, 0, 0 |
| Agent chat, Persona chat, knowledge base | Same as with pushes held | — |
| Machines page | High `moved`, equally high with pushes held (the glass cards' effect layers move on their own) | — |

The user's issue was the Agents list, the default page in the new shell.

### Causes

Working through the layers revealed five causes:

1. **Spaces ordered by last update.** Every new message from a working Agent can move its space ahead of another. Reordering during scrolling makes the entire screen's content jump.
2. **Every push still triggers reevaluation and reordering during scrolling.** "Needs you" cards appear / disappear at the top; last messages vary between one and two lines, changing row height.
   Reevaluation continues behind a pushed chat or on another page: while scrolling an Agent chat, the list behind it reevaluates (47–51 times in 20 seconds on the mock).
3. **After introducing snapshots during scrolling, pushes still reached the list through three missed paths:**
   - `AgentSync` (background transcript fetches when a push brings a new `rev`) wrote progress to UserDefaults after every fetch. Writing anything to UserDefaults
     makes every `@AppStorage` view in the App treat it as a change and rerun its body. The Agents list has four `@AppStorage` properties; on a busy hub, writes happen every few seconds.
   - Snapshots read the model in `onChange` / `onScrollPhaseChange`. SwiftUI attributes that read to the body, so subsequent pushes still rerun it.
   - The snapshot lived in the list's own `@State`; setting it at scroll start redrew the list (twice at every scroll start on real data).
   - Each `List` row separately tracks its last reads. Rows asked the model for machine names (`model.machine`), and question cards asked for sessions (`model.session`),
     so pushes during a snapshot still redrew those rows independently.
4. **Any `List` update remeasures rows using estimated heights,** even with identical data. These rows are about 70 pt taller than the estimate.
   An update halfway through deceleration suddenly changes content height by one or two rows, shifting visible content by hundreds of points and replacing / redrawing several rows.
5. **Applying updates between scrolls also causes jumps.** Releasing the snapshot after 0.5 seconds at rest applies data while still, but `List` waits until the next touch to remeasure rows, causing a jump then.
   Consecutive flicks pause only about 1 second between gestures.

### Fix

- Order spaces by `sortDate` (`SpaceGroup.sortDate`): working Agents use their work-start time; others use the last update, just like rows within a space.
  In the same recording, position changes fell from 93/255 to 18/255. The remainder were real state changes (23 in those 10 minutes).
- The list reads only an intermediary `AgentsFeed` (sessions, pending interactions, machines). It tracks the model with `withObservationTracking` and forwards changes to the list.
  Updates are held in the following cases and applied together once the condition clears:
  - Finger dragging, deceleration, and the first 1.5 seconds after stopping: consecutive flicks count as one scroll (later changed to 0.5 seconds; see "Follow-up" below).
  - Covered by a pushed page (not when the list remains visible beside it in split view).
  - On the new shell's other page or the historical tab navigation's other tab (released while swiping between the new shell's two pages).
  Holding changes only an unobserved switch, so starting a scroll does not redraw the list; scroll phase also lives in an unobserved object.
- Close the leaks:
  - Store `AgentSync` progress in `StateCache` (a file in Caches, alongside the transcript cache it describes), not UserDefaults.
  - The list body no longer reads the model, only `AgentsFeed`; there are no model reads in `onChange` either.
  - Rows no longer read the model. Each body evaluation populates an unobserved lookup table (`AgentsList.Lookup`) with machines and sessions; rows read from it.
    The list passes machine and Agent names into question cards (`InteractionPreviewCard(machine:agentLabel:)`).
    A row's "Muted" state uses the session's own space name (`model.isMuted(session)`), not `model.session(key)`.

### Results

`ScrollUnderPushTests`: the mock pushes every 2 seconds (`/mock/churn?ms=2000&real=1&inbox=1&waiting=1`), with each drag / flick run lasting about 20 seconds.
"Before" uses the same test with `ChatsView.swift`, `AgentSync.swift`, and `InteractionViews.swift` reverted to origin/master.

| Scenario | Layout | Before | After |
| --- | --- | --- | --- |
| Agents list | New shell | 10 jumps ≥ 100 pt (maximum 1349 pt), 5 relative row movements, 2 row replacements, 5 UserDefaults writes during scrolling; list reevaluated 52 times and rows 129 times during scrolling | All 0; list reevaluated 0 times during scrolling, rows 45–51 times (rows entering the screen) |
| Agents list | Historical tab navigation | 8 jumps (maximum 917 pt), 4 movements, 1 replacement, 2 writes; list 47 times, rows 144 times | All 0; list 0 times, rows 46–52 times |
| Agent chat | Both | Agents list behind it reevaluated 47–50 times; 2 UserDefaults writes | 0, 0 |
| Persona chat | Both | 3–4 UserDefaults writes | 0 |

After the fix, each layout ran 3 times, and all 6 Agents-list runs recorded 0. One new-shell Agent-chat run started scrolling before the chat opened (the test waited a fixed number of seconds).
The test was changed to wait for that chat's `chat.scrollState` probe; both layouts then passed.

Real hub, new shell, same gestures (60 seconds each, pushes enabled / held):

| List | Before: enabled | Before: held | After: enabled | After: held |
| --- | --- | --- | --- | --- |
| Agents list | Maximum jump 558 pt, 5 row replacements, 9 list reevaluations during scrolling | Maximum 45 pt, 0, 0 | Maximum 58 pt, 0, 0 | Maximum 67 pt, 0, 0 |
| Agent chat, Persona chat, knowledge base | Same as held (maximum 40–67 pt, no row replacements) | — | Same as held | — |

The hub happened to be slightly quieter after the fix (2 session pushes during the Agents-list minute, versus 4 before), with more Inbox and Lark pushes.


### Unchanged

- Both new-shell pages remain mounted. While scrolling the Agents list, the other page's assistant chat still reevaluates with its own pushes (about 7 times in 20 seconds on the mock).
  It is invisible, so jumps cannot be measured; left unchanged.
- After scrolling stops, buffered changes apply together. Rows above the screen may grow taller or spaces change position, shifting stationary content slightly (like the original "Needs you" card).

### Follow-up (2026-11-02, accuracy and timeliness)

User: fewer refreshes must not sacrifice correctness; what the screen says must be true and timely. Three changes were made to the buffering above; standards and tests are in `docs/apple/ios-state.md`:

- Critical changes are not held: an Agent starting / ceasing to need you, arriving / answered questions or approvals, Agent offline, machine offline or error. These apply immediately even while scrolling (measured on-screen 0.03–0.04 s after a push).
  - Updating only that row in place is not possible: a state change reorders spaces by priority and inserts a new card at the very top.
  - Buffered changes therefore apply with it, and the list may shift under the finger. That is the cost of immediate display.
- Apply 0.5 seconds after stopping (previously 1.5 seconds). Applying between scrolls can cause a jump on the next touch; updates intentionally land while stationary, recorded as `landed` by `PerfScroll`.
- Even continuous scrolling holds updates for at most 5 seconds, then applies them forcibly (measured 5.08 seconds).
- Chat pages: no difference between pushes enabled / held, so unchanged. New chat messages follow only at the latest end; scrolling back freezes content (`ChatView.Frozen`), as before.
- Jumps under 100 pt also occur without pushes, from lazy rows determining their heights and UI-test-generated drags; they do not count.
