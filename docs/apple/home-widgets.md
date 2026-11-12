# Home Screen and Lock Screen widgets (#5664)

Persona's own widgets for iPhone and iPad. Each widget does one job, in the manner of the system's own (Calendar,
Reminders, Fitness, Batteries). The persona chosen in **Settings › Appearance › Avatar** is only an accent. The Live
Activity, the Control Center controls and the extension widget are documented elsewhere (`docs/apple/live-activity.md`,
`docs/apple/intents.md`, `docs/extensions/extensions.md`). The Mac app is frozen and gets none of these widgets. The
watch has its own complications (`docs/apple/persona.md`, "The Milk Frog on the watch").

**Who it serves.** Everyday people first: **Today**, **To-dos**, **Record**, **Health** and **Inbox** need no
knowledge of Agents and use plain words. **Agents**, **Usage** and **Computers** serve people who work with Agents.
They show titles and state words, never engine names, session IDs, paths or model IDs (DESIGN_SYSTEM.md 5.6).

## The widgets

Each widget has one hero (a number, a ring, a chart, a button) and one color that says which widget it is.

| Widget | Its color | Small | Medium | Large | Lock Screen | Edit Widget |
| --- | --- | --- | --- | --- | --- | --- |
| **Agents** | Needs You's blue (the lead state's) | how many need you, big; the other counts in a line | + the one waiting longest | the three counts side by side, then five agents | circular (count, or the face), rectangular, inline | Show: All Agents · Agents on Your Computers · Persona's Tasks; Computer |
| **Today** | the icon's coral | weekday, the day big, the next event | + the next three events | the rest of the day as a day view, a red line at now | circular (the date), rectangular, inline | none |
| **To-dos** | orange | the list's disc and how many, "1 overdue" | + four to-dos to tick off | eight to-dos to tick off | circular, rectangular, inline | none |
| **Usage** | by how full: green, orange ≥ 80 %, red ≥ 95 % | the fullest window as a ring | each account as two rings, one inside the other | today's tokens, money spent, tokens by the hour, the accounts' rings | circular gauge, rectangular, inline | Account |
| **Computers** | green; a computer's agents color its ring | four rings, a computer each | four rings with names and states | how many online, then each computer with its load | circular, rectangular, inline | Computer |
| **Record** | the system's recording red | one big button | the button and what the recording is named | — (one action does not fill it) | circular and rectangular buttons | none |
| **Health** | pink | today's steps, big, against the 7-day average | + sleep and resting heart rate | + the week's steps as bars | circular gauge, rectangular, inline | none |
| **Inbox** | Needs You's blue | how many need you, big, and who sent the newest | + the newest message | the five newest | circular, rectangular, inline | none |

A small widget never holds a list; lists start at medium. A new widget needs no editing: every default works on a fresh
install (principle 1). Edit Widget holds every choice and changes it back. Nothing about the widgets lives in the
app's Settings (principles 2 and 3).

## The look

- **Surface**: the system widgets' white and their dark gray (`HomeWidgetTokens.surface`). No card inside a card.
  Words use the system's primary, secondary and tertiary levels.
- **Name line**: the widget's name at the top left in its color (as Calendar's weekday), the persona at the top right.
- **Hero**: big rounded numerals (`HomeWidgetTokens.hero…`), rings with a track (`HomeRing`), bar charts. Words are
  text styles and follow the text size. Widget sizes are the system's, so heroes, rings and faces are fixed sizes, the
  documented exception in DESIGN_SYSTEM.md 2.2, like `IslandMetrics` and `WatchFaceTokens`.
- **Tinted and clear (`accented`) and the Lock Screen (`vibrant`)**: the system paints every opaque pixel in one
  color, two with the accent group. Every colored part (heroes, rings, the event bars, the Record button, the To-dos
  disc) goes in the accent group (`homeTone` / `homeAccent()`). Marks on a filled disc are knocked out of it, so they
  still read in one color. Mochi becomes a line drawing (`MochiGlyph`), and a package's picture is shown desaturated.
- **StandBy**: the system removes the background and scales a small widget up. Every widget reads on black, and at
  night the system draws it in red.

## The persona, as an accent

The persona never takes the hero's place. It appears in three places:

- **The corner**: a 22 pt face at the top right (`HomeWidgetTokens.faceCorner`) whose mood says how this widget's
  subject is. Agents: needs you, working, done (an agent finished, unseen). Today: working when an event starts within
  15 minutes. To-dos: needs you when one is overdue, done when all are done. Usage: working over 80 %, needs you over
  95 %. Computers: what their agents do, offline when all are. Health: done once the steps pass the average. Inbox:
  needs you when a message does.
- **Empty states**: the face, calm, and one plain line ("Nothing needs you", "All done for today").
- **Before pairing**: the face and "Open Persona to connect".

| Mood | Mochi | A package (Milk Frog, uploads) |
| --- | --- | --- |
| Needs you | the needs-you ring | its `needs_you` frame |
| Working | the working arc, eyes aside | its `working` frame |
| Done | idle with the green check (happy eyes on the line drawing) | `idle` frame + the check |
| Thinking | eyes up, the thought bubble | its `thinking` frame |
| Idle | as the icon | its `idle` frame |
| Offline | gray, eyes closed: the snapshot is over 30 minutes old | its `offline` frame |

Mochi is drawn in code (`PersonaFace` in full color, `MochiGlyph` otherwise). A package's pictures are written by the
app into the App Group at the widget sizes, as the Live Activity's are.

## What each number counts

The same definition as the app, from the same code (principle 4):

| Widget | Number | Definition | Source in the app |
| --- | --- | --- | --- |
| Agents | Needs You | sessions that wait for you, each once (DESIGN_SYSTEM.md 4.8, `AgentsNeedsYou`, the Agents tab's badge) | `AppModel` sessions, the rule of `TabBadges` |
| Agents | Working, Just Finished | the Agents list's states (finished = unread) | the same |
| Today | events | the rest of today's events: the phone's calendar when Persona already has full access (`MeetingCalendar`'s); else the hub's Today meetings; else none, and the widget says to allow Calendars in Persona. The widget never asks itself | EventKit / `TodayBody.meetings` |
| To-dos | the count | the To-dos page's Today filter: due today, plus open overdue ones | `TodoStore`, `TodoListLogic` |
| Usage | a window's percent, reset time | `GET /api/v1/usage/windows`, the Machines page's numbers | `UsageWindowsStore` |
| Usage | tokens today, by the hour | the island's Tokens Today; the hours from `GET /api/v1/usage`'s `hourly` (#5721) | `FleetState.stats`, the usage summary |
| Usage | money spent today | only what a gateway reports (`LauncherQuota.Usage.costUSD`); never estimated from prices | launcher quotas |
| Computers | online, load | `Machine.online`; load from `FleetState.stats.loads` | `AppModel.machines` |
| Health | steps, sleep, resting heart rate | Health's own numbers, with the user's permission | `HealthTool.daily` / `.summary` |
| Inbox | Need You, Unread | open items the triage marks as yours (`InboxStore.actionCount`); the Inbox badge's number | `InboxStore` |

**Private content**: Inbox and Health are `privacySensitive`. While the phone is locked the system blurs them on the
Lock Screen, in StandBy and in the Smart Stack.

## Ticking off a to-do

The box beside a to-do is a button (`CompleteTodoIntent`). The intent runs in the app, never in the extension, as the
Live Activity's buttons do. The app marks the to-do `ticking` in the snapshot and reloads the widget, so it shows
done at once (principle 7). Then it tells the hub (the To-dos page's own call). On success the to-do leaves the list.
On a refusal or no answer it becomes `failed`: a red mark and "Couldn't complete — tap to try again", and a tap tries
again. Nothing fails silently. On the Lock Screen the intent asks for Face ID first.

## Data: the snapshot

The widget extension never talks to the hub and never reads the keychain (it cannot on a phone,
`docs/apple/live-activity.md`, "Why the avatar pack was still wrong in build 103"). The app writes one file into the
App Group, and the widgets draw only that:

```
Widgets/snapshot.json        HomeSnapshot: counts, the top rows, events, to-dos, windows, computers, health, inbox
Widgets/faces/<id>_<rev>/    the chosen package's pictures, one per state, at the widget sizes (as Fleet/faces/)
```

- **What it holds** (`apple/Shared/HomeWidgets/HomeSnapshot.swift`): only what a widget draws. Up to 40 agent rows
  (the rest counted), today's events, 8 to-dos, the accounts, 8 computers, 5 messages and a week of steps. About
  6 KB. Times are Unix seconds, and every field decodes leniently.
- **When the app writes it**: on every change a widget shows, coalesced to at most one write every 5 seconds in the
  foreground (P7); when it goes to the background; after a background refresh (`BGAppRefreshTask`
  `persona.widgets.refresh`, about every 30 minutes while a widget is on a screen); and when the hub's widget push
  wakes it (#5721). That push is sent within seconds when Needs You changes; other changes are coalesced to at most one
  every 15 minutes per device. The app writes only when the bytes changed, then reloads only the kinds whose part
  changed (`WidgetCenter.reloadTimelines(ofKind:)`): WidgetKit budgets reloads.
- **Off the main thread** (P1): encoding and writing run in the background, with the file protected until first
  unlock, as `FleetLookStore`'s.
- **Stale**: the timeline adds an entry at `HomeSnapshot.staleAfter` (30 minutes after the write). From then on the
  content steps back (`staleOpacity`), the face is offline and the widget says "Updated … ago" (principle 7).

## Taps

A widget opens its page (`persona://widget/<kind>`): the Agents list, To-dos, Machines, Recordings, Health, Inbox,
and the calendar for Today. Agents opens the most urgent agent's chat (`persona://open?machine=…&session=…`, the Live
Activity's link). Each row of the large Agents widget opens its own agent. The Record button runs the Record control's
intent without opening the app, and the recording's Live Activity confirms it at once.

## Smart Stack

Each entry carries a `TimelineEntryRelevance`: highest when something needs you (agents, messages, an overdue to-do)
or a recording is running, high when a meeting starts within 15 minutes, low otherwise.

## Screenshots

The gallery draws every widget at its size in light, dark, tinted, clear and Lock Screen, with made-up data
(`HomeSamples`): `-showHomeWidgets` in a Debug build (a DEBUG launch argument, in `release.sh`'s list). A view outside a
widget gets no container background, family, margins or tint from the system, so the gallery supplies stand-ins for
them. Tinted and clear there are close to iOS, not exact. The Home Screen shots on each PR are the proof.

## Phases

1. **The snapshot and the first widgets**: `HomeSnapshot` and its writer in the app, the package pictures, the
   Agents, Inbox and Record widgets (Home Screen), the gallery, tests (snapshot round trip, counts equal to the app's,
   staleness, moods).
2. **The rest of the Home Screen**: Today, To-dos (with ticking off), Usage, Computers, Health.
3. **Lock Screen, settings and the Smart Stack**: the accessory families, Edit Widget (entities for computers and
   accounts read from the snapshot), relevance, background refresh, and the hub's push (#5721, its own PR).
