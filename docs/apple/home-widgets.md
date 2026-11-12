# Home Screen and Lock Screen widgets (#5664)

Persona's own widgets for iPhone and iPad: the persona chosen in **Settings › Appearance › Avatar** at the center, and
what it watches for you around it. The Live Activity, the Control Center controls and the extension widget are
documented elsewhere (`docs/apple/live-activity.md`, `docs/apple/intents.md`, `docs/extensions/extensions.md`). The
Mac app is frozen and gets none of these widgets. The watch has its own complications (`docs/apple/persona.md`, "The
Milk Frog on the watch").

**Who it serves.** Everyday people first. **Persona**, **Today**, **Record**, **Health** and **Inbox** need no
knowledge of Agents and use plain words. **Agents**, **Usage** and **Computers** serve people who work with Agents.
They show titles and state words, never engine names, session IDs, paths or model IDs (DESIGN_SYSTEM.md 5.6).

## The widgets

| Widget | Home Screen | Lock Screen | Settings (Edit Widget) |
| --- | --- | --- | --- |
| **Persona** | small, medium, large, extra large (iPad) | circular, rectangular, inline | four places (Top Left, Top Right, Bottom Left, Bottom Right): a module or None, two to four in all. Defaults: Agents, Today, Inbox, Record. Also Agents (which kind) and Computer |
| **Agents** | small, medium, large | circular, rectangular, inline | Show: All agents · Agents on your computers · Persona's tasks. Computer: any, or one |
| **Today** | small, medium, large | circular, rectangular, inline | Show: events and to-dos · events · to-dos |
| **Usage** | small, medium, large | circular, rectangular, inline | Account: the fullest, or one |
| **Computers** | small, medium | circular, rectangular, inline | Computer: all, or one |
| **Record** | small, medium | circular, rectangular | none |
| **Health** | small, medium | circular, rectangular, inline | none |
| **Inbox** | small, medium, large | circular, rectangular, inline | none |

A new widget needs no editing: every default works on a fresh install (principle 1). Edit Widget holds every choice
and changes it back. Nothing about the widgets lives in the app's Settings (principles 2 and 3).

## The look

- **The stage.** Every widget stands on the app icon's paper (its dark in the dark), `HomeWidgetTokens.stageTop` /
  `stageBottom`. Words use the icon's ink. A row of Persona widgets reads as one family. The only strong colors are the
  agent states' (`StatusPalette`: Needs You blue, Working amber, Done green), a recording's red, and a soft glow of
  the persona's own color behind its face (Mochi's cheek, a package's `primary`).
- **The Persona widget is the persona between four tiles.** The tiles hold the chosen modules. The face sits where they
  meet, cut out of them (`destinationOut`), so it stands on the stage. The shape is the same at every size, and the
  tiles say more as they grow: one number in a small widget's corner (**corner**), a number and a line in the medium
  one (**strip**), a small widget's worth in the large one (**card**), and a medium widget's worth on iPad (**wide**).
  Each tile's content keeps to its outer corner, out of the face's clearing. With three modules the bottom tile spans
  the width. With two, the tiles stand side by side.
- **A module's own widget** has its name at the top left and the persona at the top right
  (`HomeWidgetTokens.faceBadge`), whose face tells how that module is. Agents waiting means needs you. A meeting within 15 minutes
  means working. A subscription window over 80 % means working, over 95 % needs you. Steps past the 7-day average
  means done. Messages that need you means needs you. A recording under way means working.
- **Numbers first, big and rounded** (`HomeWidgetTokens.numeral…`). Words come in the text styles and follow the text size.
  Widget sizes are the system's, so the numerals and faces are fixed sizes, the documented exception in
  DESIGN_SYSTEM.md 2.2, like `IslandMetrics` and `WatchFaceTokens`.
- **Tinted and clear (`accented`) and the Lock Screen (`vibrant`).** The system paints every opaque pixel in one
  color, two with the accent group. So:
  - tiles become a faint wash (`tileMono`), never a solid block;
  - Mochi becomes a line drawing (`MochiGlyph`: outline, eyes, cheek, the state's cue);
  - a package's picture is shown desaturated (`widgetAccentedRenderingMode(.desaturated)`);
  - every state color, the face's cue, Done's check and the Record button move to the accent group (`homeAccent()`).

  Marks that sit on a filled shape (the check, the Record waveform) are knocked out of it, so they still read in one
  color.
- **StandBy.** The system removes the background and scales a small widget up. The tiles and the face still read on
  black, and at night the system draws them in red.

## The persona

| Mood | When (the first that holds) | Mochi | A package (Milk Frog, uploads) |
| --- | --- | --- | --- |
| Offline | the snapshot is over 30 minutes old, or no hub is paired yet (no hub: Idle with "Open Persona to connect") | gray, eyes closed | its `offline` frame |
| Needs you | an agent waits for you | the needs-you ring | its `needs_you` frame |
| Working | an agent is working | the working arc, eyes aside | its `working` frame |
| Done | an agent finished and you have not looked | idle with the green check (happy eyes on the line drawing) | `idle` frame + the check |
| Thinking | only Persona is answering in the main chat | eyes up, the thought bubble | its `thinking` frame |
| Idle | otherwise | as the icon | its `idle` frame |

This is the Live Activity's order (`FleetPersona.state`) with Done added: the watch's Milk Frog has it, and the widgets
are where you look when something finished. Mochi is drawn in code at any size (`PersonaFace` in full color,
`MochiGlyph` otherwise). A package's pictures are written by the app into the App Group at the sizes the widgets draw.
A widget's largest face is 136 pt on iPad. Packages ship 144 px state frames and avatars up to 256 px. A face larger
than 48 pt therefore uses the state's frame scaled up, and the picture is only as sharp as the package allows. The
avatar picture is used only when the state has no frame.

## Modules

What each number counts. The same definition as the app, from the same code (principle 4):

| Module | Shows | Counts and their definitions | Source in the app |
| --- | --- | --- | --- |
| Agents | the most urgent category's count, the most urgent agent (its title, what it waits for, how long), the other counts | Needs You: sessions that wait for you, each once (DESIGN_SYSTEM.md 4.8, `AgentsNeedsYou`, the Agents tab's badge). Working, Done: the Agents list's states | `AppModel` sessions, the same rule as `TabBadges` |
| Today | the date, the next events, today's to-dos | to-dos: the To-dos page's Today filter (due today, plus overdue and open); overdue in red | `TodoStore`; events below |
| Usage | each subscription window's percent and reset time; tokens today; money spent today where known | windows: `GET /api/v1/usage/windows`, the Machines page's numbers. Tokens Today: the island's (`FleetState.stats.tokens`). Money: only what a gateway reports (`LauncherQuota.Usage.costUSD`); nothing is estimated from prices | `UsageWindowsStore`, `FleetState.stats` |
| Computers | online of all, each one's load and agents | online: `Machine.online`. Load: `FleetState.stats.loads`. Agents per computer: the sessions above | `AppModel.machines` |
| Record | a button that starts a meeting recording, named after the calendar event happening now; while recording, the running time and Stop | — | `StartMeetingRecordingIntent` / `StopRecordingIntent`, the Record control's |
| Health | steps today against the 7-day average, sleep last night, resting heart rate, active energy | Health's own numbers, read with the user's permission | `HealthTool.daily` / `.summary` |
| Inbox | how many need you, the newest messages | Needs You: open items the triage marks as yours (`InboxStore.actionCount`). Unread: the Inbox badge's number | `InboxStore` |

**Calendar events**: from the phone's calendar when Persona already has full access (it asks for that for recordings,
`MeetingCalendar`). Otherwise the hub's Today meetings, where Feishu provides them. Otherwise to-dos only. The widget
never asks for calendar access itself. Its empty state says to allow it in Persona.

**Inbox messages and health numbers are private**: `privacySensitive`. While the phone is locked the system blurs
them on the Lock Screen, in StandBy and in the Smart Stack.

## Data: the snapshot

The widget extension never talks to the hub and never reads the keychain (it cannot on a phone,
`docs/apple/live-activity.md`, "Why the avatar pack was still wrong in build 103"). The app writes one file into the
App Group, and the widgets draw only that:

```
Widgets/snapshot.json        HomeSnapshot: counts, the top rows, events, to-dos, windows, computers, health, inbox
Widgets/faces/<id>_<rev>/    the chosen package's pictures, one per state, at the widget sizes (as Fleet/faces/)
```

- **What it holds** (`apple/Shared/HomeWidgets/HomeSnapshot.swift`): only what a widget draws. Up to 40 agent rows
  (the rest counted), 5 events, 8 to-dos, 3 accounts, 8 computers and 5 messages. About 6 KB. Times are Unix seconds,
  and every field decodes leniently.
- **When the app writes it.** On every change the widgets would show, coalesced to at most one write every 5 seconds
  while in front (P7). Also when it goes to the background, after a background refresh (`BGAppRefreshTask`
  `persona.widgets.refresh`, asked for every 30 minutes while a widget is on a screen), and when a push the app handles
  in the background arrives. It writes only when the bytes changed. Then it reloads only the kinds whose part changed
  (`WidgetCenter.reloadTimelines(ofKind:)`), never all of them: WidgetKit budgets reloads.
- **Off the main thread** (P1): encoding and writing run on a background task, with the file protected until first
  unlock (`.completeFileProtectionUntilFirstUserAuthentication`, as `FleetLookStore`'s).
- **Stale**: the timeline adds an entry at `HomeSnapshot.staleAfter` (30 minutes after the write). From then on the face
  is offline and the tiles step back (`staleOpacity`). A module's own widget says "Updated … ago". Old numbers never
  pass for current ones (principle 7).
- **Before pairing**: every widget shows the persona and "Open Persona to connect".

## Taps

A tap on a medium or larger tile opens that module's page (`Link`). A small widget opens the most urgent thing
(`widgetURL`). Agents opens the most urgent agent's chat (`persona://open?machine=…&session=…`, the Live Activity's
link). The others go to the To-dos, Machines, Inbox and Health pages and the recordings. The Record button runs the
intent without opening the app, as the Record control does: the recording's Live Activity confirms it at once
(principle 7). Only the first recording asks for the microphone in the app.

## Smart Stack

Each entry carries a `TimelineEntryRelevance`. Highest when something needs you (agents or messages) or a recording
is running, high when a meeting starts within 15 minutes, low otherwise. The stack brings Persona up when there is
something for you.

## Screenshots

The gallery draws every widget at its size in light, dark, tinted, clear and Lock Screen, with made-up data
(`HomeSamples`): `-showHomeWidgets` in a Debug build (DEBUG launch argument, in `release.sh`'s list). A view outside a widget
gets no container background, family, margins or tint from the system, so the gallery supplies a stand-in for each.
Tinted and clear there are close to iOS, not exact. The Home Screen shots on each PR are the proof.

## Phases

1. **The snapshot and the Persona widget** (Home Screen sizes): `HomeSnapshot` and its writer in the app, the
   package pictures at widget sizes, the Persona widget, the gallery, tests (snapshot round trip, counts equal to the
   app's, staleness, moods).
2. **The module widgets**: Agents, Today, Usage, Computers, Record, Health, Inbox at their Home Screen sizes, with their
   settings.
3. **Lock Screen, settings and the Smart Stack**: the accessory families, the Persona widget's places, entities for
   computers and accounts (read from the snapshot), relevance, background refresh.
