# System calls on the iPhone (calling Persona, urgent calls)

Calling Persona is a phone call through the system's call UI (#9290): the main conversation's + › **Call Persona**, a
redial from the Phone app's Recents, Siri, Shortcuts or Spotlight (`docs/apple/siri.md`, "Calling Persona"). It shows on
the Lock Screen and in the Dynamic Island like any call, and it goes on when the owner switches apps, goes Home, opens
another page or locks the phone. Persona's urgent calls (`docs/assistant/urgent-call.md`) are the same kind of system call
and share everything below. The in-app voice chat (the chat's handset, `docs/assistant/voice.md` section 6) and CarPlay's
voice chat (`docs/apple/carplay.md`) stay what they were: calls inside the app, not system calls.

The page is a full-screen call screen that can be minimised to a "return to call" bar
(`docs/design/DESIGN_SYSTEM.md`, "Call screen" and "Return-to-call bar"). The call is the assistant's: the screen shows
its own look and name, and the system's call UI, the Dynamic Island, the Lock Screen and Recents show its name. Both the
screen and the bar live in a window of their own above the app's, so the bar is on every page and over every sheet,
and nothing in the app ends a live call by itself ("Nothing in the app ends a call" below).

## Architecture (`apple/Persona/Calls/`)

| Piece | What it does |
| --- | --- |
| `PersonaCalls` (`PersonaCalls.swift`, `OwnerCall.swift`) | The app's one call manager (`PersonaCalls.shared`, `@MainActor @Observable`). It owns the calls list the pages read (`calls`, `ownerCall`, `presented`, `refusal`, `route`), places calls to Persona (`callPersona`), rings urgent calls for `UrgentCallCenter` (`ring`, `answer`, `settle`), sends each system action to its call by conversation UUID, and carries out what the call's machine says. |
| `CallMachine` (`CallMachine.swift`) | One per call, both kinds: a pure `nonisolated` value type with no LiveCommunicationKit and no audio. Events in (the system's actions and callbacks, the page, the engine, timers), effects out (fulfil or fail, report, engine start/stop/mute/hold, ask the system to mute, unpause or end, activate the session once, schedule a timer). Every rule below is a unit test. |
| `CallSystem` (`CallSystem.swift`) | The seam to the system. `LCKCallSystem` on devices: the app's only `ConversationManager`, made the first time a call needs it (never at launch, #9264), and its delegate `PersonaCallsLCKDelegate`, whose methods are all `nonisolated` and hop to the main actor in order (`CallMain`; an unmarked delegate method traps when LiveCommunicationKit calls it from its own queue). `SimulatedCallSystem` in the Simulator. A fake in the tests. LiveCommunicationKit's action objects cannot be made by anyone else, so each action reaches `PersonaCalls` as a `CallSystemAction` with its own fulfil and fail. |
| `CallEngine` (`CallEngine.swift`) | What runs the conversation: `OwnerCallEngine` for a call to Persona (a `VoiceConversation(surface: .call)` on the system's audio session, `PhoneVoiceAudio(session: .system)`), `UrgentCallEngine` for an urgent call (`UrgentCallSession`, turn by turn). |
| `CallRoute` (`CallRoute.swift`) | Where the sound goes now (receiver, speaker, Bluetooth by name, headphones, car), read from the session on every route change while there is a call, never from what the app asked for. Speaker on is `overrideOutputAudioPort(.speaker)`, handed back when the last call ends. |
| `CallScreen`, `CallAvatar`, `CallControls`, `UrgentCallView` | The pages. The assistant in the middle of the call screen is `CallAvatar` (its look moving with the call, a ring of the voice's level). Who a call is with is the call's own `CallAssistant` (`PersonaCall.assistant`): the look and name taken together when the call is made and once more just before the system's Start, then fixed, so the face beside the name never follows an avatar chosen during the call; a package's frames and face are read and decoded off the main actor into it (`PersonaStore.decodedFrames`, `decodedFace`), and the pages (`CallAssistantDot`, `CallAssistantFace`) only arrange what is there, the built-in drawing meanwhile. Every control goes through `PersonaCalls`, never to the voice directly, so the system's call UI and AirPods say the same. |
| `CallWindow` (`CallWindow.swift`) | The call screen and the bar back to a minimised call, in a passthrough window at `.normal + 0.75` over the app's windows (the + panel's, the screen viewer's) and under the toasts', the system's alerts and the keyboard. It takes touches only where it shows something: the whole screen for the call screen and a refusal's alert, the bar alone while minimised. The pages under the bar get its room (`additionalSafeAreaInsets` on the app's root, and on every sheet or cover whose content would start under it; never on an alert or action sheet, `CallWindow.takesRoom`; the toasts' window above the call's gets it too, so a toast stays just below the top bars, `TakesCallBarRoom`), looked at again four times a second while the bar shows and the app is active; the watch is cancelled as the app leaves the front (Home, the lock, another app) and the room put right at once as it comes back (`CallWindow.watchesRoom`, docs/apple/performance.md P9). |
| `CallIcon` (`CallIcon.swift`) | The system call UI's template icon: Mochi's silhouette, 40 pt at 3×, drawn and encoded off the main actor before the manager is made (`PersonaCalls.readySystem`, the first call placed or rung), which only takes the finished PNG. |
| `CallGuards` (`CallGuards.swift`), `LiveCallGate` (`apple/Shared/Recording/LiveCallGate.swift`) | What keeps in-app actions from ending a live call: the manager answers whether a call is live (`guardsLive`), the assistant's name for the questions, the call's own conversation; web pages' media waits (`CallMediaGuard`). |

The voice side of a call to Persona (the `.call` surface, the system's audio session, `surface` in `start`, hold frames)
is `docs/assistant/voice.md` "6.12 System calls"; the urgent call's side is `docs/assistant/urgent-call.md`.

**Why a stand-in in the Simulator.** The Simulator has no call service. The real LiveCommunicationKit accepts the Start
there, the conversation joins, then callservicesd logs "Disconnecting call because there wont be a UI to host the call"
(disconnect reason 55) and ends it with an End action about 80–200 ms later; `didActivate` never comes. So
`SimulatedCallSystem` (`#if targetEnvironment(simulator)`) carries every request out at once, as the system would hand
it back, and activates and deactivates the audio session itself where the system would call `didActivate` /
`didDeactivate`. An incoming urgent call rings silently there. DEBUG `-showSystemCallService YES` takes the real
LiveCommunicationKit in the Simulator, to see and log what it does.

## LiveCommunicationKit configuration and the handle

`LCKCallSystem.configuration`, read-only once the manager exists:

- `maximumConversationGroups: 2`, one conversation per group: an urgent call during a call to Persona offers
  **Hold & Accept** (with one group the system offers only End & Accept and Decline, and a Start at capacity fails with
  `requesttransaction` code 7).
- `includesConversationInRecents: true` (the redial), `supportsVideo: false`, `supportedHandleTypes: [.generic]`, no
  ringtone. `iconTemplateImageData` is the assistant's mark as a template (`CallIcon`): an alpha silhouette of Mochi
  (its body and outline, its eyes cut out), 40 pt at 3× as Apple's guidance for a call app's icon asks, drawn with plain
  Core Graphics and encoded once, on a background thread, before the manager is made (`CallIcon.prepare`, awaited by
  `PersonaCalls.readySystem` before the first Start or ring; the manager's configuration is fixed for its life and is
  made on the main actor from the finished data, `CallIcon.ready`). Mochi stands for every look:
  a package's or an uploaded picture has no outline of its own to cut.
- Every conversation is pausable: `capabilities: [.pausing]` in the incoming report and in a `conversationUpdated`
  report for outgoing calls.

The handle is the assistant's name: `Handle(type: .generic, value: <name>, displayName: <name>)`, where the name is
`Persona.name` of the look on screen ("Persona" for the built-in Mochi, the package's name for another look), trimmed,
and "Persona" when it is empty (`CallHandle.name`). The call takes the look and its name together when it is made, and
once more just before the system's Start (after every wait, so a cold launch's hub and look count), then keeps both
(`PersonaCall.assistant`): the system, the call screen and the bar say the same name beside the same face for the whole
call, whatever avatar or hub is chosen meanwhile. The system UI, the Dynamic Island, the Lock Screen and Recents show
the handle's **value** and ignore its display name (FB24933340), and a redial gives back only the value. A redial with
any value (an old "Persona", a look's name since changed) calls the current hub's main conversation. An urgent call's
member is the same value with the call's name ("Urgent: <title>" when the Lock Screen may show details) only as its
display name: the call id no longer goes in the handle (it showed as "call_…"), and the title never reaches Recents.
The opt-in SiriKit extension still names its one contact "Persona" (it cannot read the app's look; `docs/apple/siri.md`).

LiveCommunicationKit reports errors in CallKit's domains (`com.apple.CallKit.error.requesttransaction`, sometimes under
`NSUnderlyingErrorKey`). `CallSystemError` compares them as strings. The app never imports CallKit
(`docs/operations/release.md`, "No CallKit").

## The state machine

A call has an id (the conversation UUID), a kind (`.owner(group:)` or `.urgent(callID:)`), a direction, a phase
(`dialing`, `ringing`, `connecting`, `connected(since:)`, `ended(reason)`), `muted`, a set of hold reasons (`.system`:
the system paused it; `.interruption`: the audio session is not the call's outside a pause, whether interrupted,
deactivated with no word of why, or not given back after the system resumed the call) and whether the system's audio is
the call's (`audioActive`). `held` is "any hold reason".

- **Start (outgoing).** `StartConversationAction` → fulfil, phase `connecting`, report `startedConnecting` and
  `pausable`, begin the engine (the voice starts), start the activation watchdog. Reports wait until the system knows
  the conversation (it is in `conversations`: checked right after the fulfil, else on the next `conversationChanged`):
  the system drops reports made before. The system giving up on the Start ends the call as failed.
- **Connected.** A call to Persona connects on the voice's first `ready` (report `connected`); an urgent call on Join.
- **Audio.** The engine's audio starts only on `didActivate`. `didDeactivate` stops it and never ends the call; outside
  a pause, with no interruption to say why, it holds the call (`.interruption`, either kind's engine holds) and the
  recovery below runs at once, so the call is never left connected and silent. No `didActivate` 5 s after the Start or
  Join: logged, `setActive(true)` tried once; if that fails too, the call ends as failed (both are device-checklist
  lines). The watchdog waits while the call is held (the system's pause or an interruption has the audio) and runs again
  from the system's resume; the result of an activation asked before a hold or an interruption counts for nothing. An
  activation that comes before the Start is handled is kept.
- **Mute.** `MuteConversationAction` is fulfilled and applied, idempotent by value: the system echoes the app's own mute
  8–17 ms later and sends an unmute after a muted call ends. The page's Mute asks the system (so its call UI and AirPods
  agree) and applies here if the system refuses (it does while dialling: the mute then reaches the system once it knows
  the call). From `didActivate` until 0.5 s after the engine runs, a system mute is the engine's own (turning voice
  processing on surfaces as one on iOS 17+): ignored, and the owner's mute asserted again once per engine (the first, one
  after a hold or an interruption, and one a route change rebuilt, which the audio says before it starts it).
- **Hold.** `PauseConversationAction` is fulfilled at once (a failed pause, or one left about 5 s, makes the system end
  the call). While held the microphone sends silence, what the voice says is dropped and nothing plays, the idle clock
  stops, and an extension that lists `hold` gets `{"type":"hold","on":true}` (again after a reconnect's `ready`, before
  any audio; what the microphone caught while waiting for `ready` is dropped, never sent). The call goes on only once
  every hold reason is gone and the audio is the call's again. The system's resume with no `didActivate` after it (the
  session was active already, our other call had it) keeps the call On Hold and runs the recovery below.
- **Interruption.** LiveCommunicationKit has no call observer, so a cellular call or another app's call may reach the
  app only as an audio-session interruption. Its `began` once the call started or was answered holds the call
  (`.interruption`), never ends it, and stops the engine. Its `ended`: nothing while the system still pauses the call (its
  resume starts the recovery); otherwise the recovery: after 1 s without `didActivate` the app asks the system to
  unpause, 0.5 s later activates the session once itself (not while the system pauses the call), and at 5 s the call
  stays On Hold with a **Resume** button (never silently ended). Resume asks again.
- **Our own second call.** When any of our calls ends, every other call the system held is unpaused by the app (the
  system does not resume a held call of the same app): an urgent call answered with Hold & Accept, then over, gives the
  call to Persona back.
- **End.** `EndConversationAction` → fulfil, end the engine (`VoiceCalls.end(.user, from: .call)`). The engine ending
  by itself reports `conversationEnded`: `failed` for failures (`VoiceEnd.failsCall`), `remoteEnded` otherwise. The page's
  Hang Up goes through the system, ending here if it refuses. A conversation that left without an End action, or a
  manager reset, ends the call without a report. Each side ends exactly once. An ended call stays in the list 2 s so the
  page can say why, then leaves.

## Placing a call to Persona (`callPersona`)

1. The call screen shows "Calling…" at once, before anything slow (a call already on is shown instead). Whatever page or
   sheet the app was on stays under it (the call's window goes over everything, Settings' sheet included). A cold launch
   from Siri, Shortcuts or Recents waits (at most 8 s) for the hub links and the main conversation.
2. Refused with the voice alert (and the page goes): an urgent call rings or is on, a recording or dictation has the
   microphone, no hub, no main conversation, no usable voice extension (the same checks and words as the chat's Voice
   Chat, after `VoiceSupport.refresh`), the microphone not allowed (asked in the app first: a locked phone mid-call
   cannot show the prompt). The urgent call and the recording are asked again after every wait and before each Start.
3. The selected hub's main conversation and its voice extension (`VoiceAvailability.pick`).
4. An in-app voice chat that is on ends first: its audio would end the call's.
5. The audio category, off the main actor: `.playAndRecord`, `.voiceChat`, `[.allowBluetoothHFP, .allowBluetoothA2DP]`
   (`PhoneVoiceAudio.callSessionOptions`, no `.defaultToSpeaker`: the earpiece by default, like a phone call). The app
   never activates the session; the system does.
6. `StartConversationAction`. From any origin but the app's own button the Start waits until the app is active (at most
   5 s): one started while the app is still coming to the front is refused (code 6), and is tried once more 0.5 s later.
   Any other refusal falls back to the in-app voice chat on the main conversation, with a toast saying why.
7. On the system's Start: the voice, `VoiceCalls.begin` of a `VoiceConversation(surface: .call)` with the chat's recent
   lines; its `start` frame carries `"surface":"call"`.

The urgent call sets the same category as it rings (`PersonaCalls.prepareAudio`), unless a call to Persona has set it
already (one still being placed has not), or an in-app voice chat is on: its loudspeaker stays while the call rings, and
answering ends the chat and sets the category before Join is fulfilled. So both kinds start on the earpiece; the pages'
**Speaker** (or **Audio**, the system's route picker, while Bluetooth, a car or headphones can take the call) and the
system's call UI move it.

A refusal that comes after its attempt was hung up (the main conversation or the microphone prompt answered late) is
dropped: a newer call's page and alert stay.

## Background and the Lock Screen

- Only the in-app `.phone` voice chat ends on background (`docs/assistant/voice.md` "6.5 Lifecycle"); a `.call` goes
  on. It does not keep the screen awake: the phone may auto-lock during it, as during a phone call.
- The tunnels of the call's hub group stay open in the background (`AppModel.callTunnelGroup`), and close once the call
  ends in the background.
- Hiding the page, another page, another app, Home and the Lock Screen never end the call. Coming back to the app with
  the call on (the Dynamic Island, the status bar's call, the app switcher) shows the call screen again
  (`returnedToApp`), over whatever page or sheet the app was on.
- A held call has no audio session, so iOS may suspend the app: its socket may be dead on resume, and the voice's
  usual reconnect (1, 4 and 10 s) brings it back and says the hold again.

## With voice chat, urgent calls and recording

- One call at a time, and nothing ends the call to Persona by itself. A voice chat in another conversation (its handset,
  + › Voice Chat, Continue on an ended voice chat) asks first: "End the call with <name> and start a voice chat here?"
  (**End Call and Start**, destructive, or **Cancel**, which keeps the call; `VoiceUnavailableNote.endCallFirst`). Only
  the yes ends the call and waits (at most 1 s) for the system to let its audio go before the voice chat opens its own
  (`VoiceCalls.start(endingCall: true)`, `VoiceCalls.endSystemCall`, which `PersonaCalls.endForVoiceChat` answers); it is
  refused if an urgent call rang meanwhile. Without the yes `VoiceCalls` refuses it, touching nothing
  (`VoiceUnavailableNote.systemCallOn`). The main conversation's own handset or Voice Chat shows the call screen (it is
  the same conversation).
- An urgent call rings during a call to Persona: it is reported as always (iOS ends an app that does not report a VoIP
  push's call), and the system offers Hold & Accept, End & Accept or Decline. Pause and End reach the call to Persona
  through its machine; after the urgent call, the app unpauses it.
- An urgent call answered during an in-app voice chat ends the voice chat first (`VoiceEnd.urgentCall`, "Ended for
  Persona's urgent call", Continue offered), so its audio cannot deactivate the session under the urgent call.
- While an urgent call rings or is on, a voice chat (any page, CarPlay, Continue) and a call to Persona are refused with
  "Finish the urgent call first" (`VoiceUnavailableNote.urgentCall`; `VoiceCalls.urgentCallOn`). One the system would
  not show (Focus or Do Not Disturb refused its ring, only its companion notification says so) refuses nothing until it
  is answered from that notification.
- Answered from its notification without the system (it refused the ring or Join): a call to Persona that is on goes on
  hold through the system first, as Hold & Accept does, and the urgent call talks once the system let that call's audio
  go; after it, the call to Persona is resumed. If the system will not hold the call to Persona, the urgent call is not
  answered here and its conversation opens instead.
- A recording or dictation in progress refuses a call to Persona; one asked for during a call is refused with a note and
  the call goes on (below). The call's voice keeps the microphone (`RecordingMicrophone.claim` without giving way), so
  even a path that asked nothing cannot take it.

## Nothing in the app ends a call

While a call to Persona (from "Calling…") or an urgent call (ringing, once the system showed it, or answered) is live,
an in-app action never silently ends it, takes its microphone or sets up the audio session under it
(`PersonaCalls.guardsLive`, `LiveCallGate`):

- **Dictation** (the composer's mic tapped or held, #9619; the to-do fields): refused at once with a toast, "On a call —
  dictation is off until it ends"; neither the dictation bar nor a hold opens (`Dictation.callGate`). A dictation that
  was waiting for its permission when the call went live is refused once it is answered, before the microphone or the
  session; its session is opened on the audio session's queue the call sets its category on (`LiveCallGate.sessionQueue`),
  and a block that finds a call live there touches nothing (`LiveCallGate.liveNow`). A dictation the call came during
  (an urgent call answered) does not deactivate the session as it ends.
- **A recording** (a meeting from the + list, the home screen's menu, Siri, the Control or the Recordings page; a note;
  all day): refused, "End the call first" (a toast where the start came from elsewhere, the Recordings page's line under
  its button); a recording a call paused stays paused, saying the same (`RecordingCapture.callGate`, checked before the
  permission, after it, after the Bluetooth microphone's search, at the resume's turn and just before the session is set
  up or the microphone opens: a start the call came during stops there). A recording that exists under a call (paused
  before an urgent call was answered) can still be stopped and is saved; its Stop (the Live Activity's, the system's)
  gives back only its microphone and leaves the call's session as it is, and Persona speaking or not (its category),
  echo cancellation and the meeting speaker's route wait for the call to end.
- **Voice chat**: asks first in another conversation, shows the call screen in the call's own (above). A refusal that
  comes after the yes (an urgent call rang while the call's audio went) is said by the same alert once it has gone
  (`VoiceEntry.proceed` awaits the start to its end); the yes after the call already ended by itself (AirPods, the
  system's call UI) hangs up nothing but still waits for the system to let its audio go.
- **Disconnecting the call's hub** (Settings' Disconnect All, Disconnect This Group or This Hub, Devices' Revoke and
  Unpair of this device): refused with a toast, "On a call with <name>. End the call before disconnecting.", before any
  credential is revoked or anything forgotten (`AppModel.disconnectRefusal`, `PersonaCalls.disconnectRefusal`); another
  group's hub disconnects as before. A call whose group is not known yet (an urgent call without its ring's details)
  counts for every group.
- **CarPlay**: the car's Talk to Persona (its screen, its Start, its Continue, Siri's Talk to Persona with the car
  connected) does not start while a call to Persona is on the phone: the car says "You're on a call with <name> on the
  phone" (`CarPlayVoiceSession.onPhoneCall`), and the call plays through the car when the car is its audio. The choice:
  the car's voice chat would have to end the call, and a question on the phone is the wrong thing while driving.
- **Web pages' media** (the in-app browser and its preloads, HTML cards and files, mail): suspended while a call is live
  and let go after (`CallMediaGuard`, `WKWebView.setAllMediaPlaybackSuspended`): WebKit sets up its own audio for a
  page's video, which would interrupt the call's session (the call would go on hold).
- **The screen viewer's picture in picture** sets up no playback session during a call (`VNCPiP.prepare`).

Every place that sets up the audio session has a verdict for a live call (`CallSourceTests.everyAudioSessionChangeHasAVerdictForALiveCall`
fails on a new one until it is given one):

| Where | Runs during a call? | Disturbs it? | Guard |
| --- | --- | --- | --- |
| `PersonaCalls.setCallCategory` (category before Start, as an urgent call rings) | yes, the call's own | no: the same category, before the system activates the session | none needed |
| `CallSystem` (`activateAudio` once after the 5 s watchdog; the Simulator's stand-in) | yes, the call's own | no | none needed |
| `CallRoute.setSpeaker` (`overrideOutputAudioPort`) | yes, on Speaker | no: the call's own route | none needed |
| `PhoneVoiceAudio` (`.phone`, `.carPlay`: category, activation, preferred input, speaker override) | only through `VoiceCalls` | would: its session replaces the call's | refused, or asked first (`VoiceCalls.systemCallName`); the system mode (a call's) never touches the session (`aSystemCallsAudioNeverTouchesTheSystemsSession`) |
| `Dictation` (`.record` category, activation, deactivation) | would, when tapped, or when its permission is answered | would: cuts the call's microphone, its deactivation stops the call's audio | refused before and after the permission (`LiveCallGate.dictationRefusal`); opened on `LiveCallGate.sessionQueue`, in order with the call's category; no deactivation while a call is live |
| `RecordingCapture.configureSession` (start, resume, the resume when the app comes back) | would | would | refused, and asked again after every wait and before the session or the microphone (`LiveCallGate.recordingRefusal`) |
| `RecordingCapture` stop, cancel, failed start (preferred input, deactivation), `setPlaysAloud` (category), `turnOnEchoCancellation` | yes: a recording paused before an urgent call was answered | would: the deactivation stops the call's audio, the category moves its route | under a live call only the microphone goes back; the category and voice processing wait for the resume |
| `MeetingSpeaker` (speaker override and its restore), `MeetingVoicePlayer`, `MeetingCallAudio` | only during a meeting recording (a paused one exists under a call) | the override would move the call's route | skipped during a call (`LiveCallGate.callIsLive`) |
| `VNCPiP.prepare` (`.playback` category, activation) | would, opening a Mac's screen | would | skipped during a call (`LiveCallGate.callIsLive`) |
| `UrgentCallVoice` (its recognizer's engine, `MeetingVoicePlayer`, the phone's voice) | yes, the urgent call's own | no: on the system's session, no category change | none needed |
| `AVPlayer` / `AVPlayerViewController` / `VideoPlayer` (audio messages, video files, link cards' video, Lark media) | can | no: they play through the session as it is and change nothing | none: the sound plays in the call's route |
| WebKit media (in-app browser, HTML cards and files, mail) | can | can: WebKit's own audio interrupts the call's session | suspended (`CallMediaGuard`) |
| Camera (`UIImagePickerController`, photos only) and the QR scanner (`DataScannerViewController`) | can | no: video only, no audio input | none needed |
| Haptics (`UIFeedbackGenerator`), voice cues (through the voice's own player) | can | no | none needed |
| Hub switch | can | no session change | none needed |
| Disconnect (Settings' Disconnect, Devices' Revoke and Unpair) | would | would: it revokes the credential and closes the tunnels the call talks through, and the voice ends as unreachable | refused while a call talks through that hub (`LiveCallGate.disconnectRefusal`) |

## Recents and intents

The app declares `NSUserActivityTypes` `INStartCallIntent` and `INStartAudioCallIntent` (`apple/project.yml`): Recents
may hand either. A redial reaches `PersonaCalls.continueActivity` from the scene's delegate (cold launch:
`connectionOptions.userActivities`; warm: `scene(_:continue:)`) or SwiftUI's `onContinueUserActivity`, whichever
fires; the same tap within 2 s is taken once. Any handle calls the current hub's main conversation. Siri, Shortcuts,
Spotlight and the opt-in SiriKit extension: `docs/apple/siri.md`, "Calling Persona".

## CarPlay

- CarPlay's own call screen is not used: CarPlay asks VoIP calling apps for CallKit and its communication entitlement.
  A call to Persona made on the phone plays through the car when the car is the route (`CallRoute` says `car`).
- Talk to Persona with no car places a call to Persona on the phone (or shows the one on); with CarPlay connected it is
  the car's voice chat, or asks to open Persona on the car's screen (`docs/apple/carplay.md`).
- The car's voice chat is refused while an urgent call rings or is on, and while a call to Persona is on the phone: the
  car says the call is on the phone, and never ends it ("Nothing in the app ends a call").

## No CallKit

No source imports CallKit and no binary in the app links it, weakly or through a package: App Review rejects a build
offered in China otherwise. The source guard and the binary check run in CI and in the release
(`docs/operations/release.md`, "No CallKit"); `CallSourceTests` also checks that only `CallSystem.swift` makes a
`ConversationManager`.

## DEBUG switches

All are listed in `scripts/release/release.sh`, which fails a Release binary that still has them.

- `-showCallSample <state>`: the call screen with no system, audio or hub: `dialing`, `connecting`, `connected`,
  `muted`, `held`, `interrupted`, `resume`, `speaker`, `ended`, `mutedSpeaker`, `reconnecting`, `audioRoute`, `speaking`
  (the assistant speaking, its ring swelling) (`PersonaCalls.showSample`). A sample guards nothing ("Nothing in the app
  ends a call").
- `-openOwnerCall YES`: calls Persona once the hub is there, as a redial would (in the Simulator through
  `SimulatedCallSystem`); `-openOwnerCallSeconds <n>` hangs up n seconds after it connected.
- `-showSystemCallService YES`: the Simulator on the real LiveCommunicationKit (it ends every call, see above).
- `-showUrgentCallSample YES`: the answered urgent call's page (Mute, Speaker, Hang Up); `held`: on hold after an
  interruption its audio did not come back from (On Hold, with Resume beside them).

## Tests

- Unit (`apple/PersonaTests/`): `CallMachineTests` (every rule above), `PersonaCallsTests` (the manager with
  `FakeCallSystem` and fake voices: start, reports, activation and the watchdog, mute, hold, interruptions, our own
  second call, end paths, refusals before Start, tunnels, the urgent exclusion), `CallSourceTests` (one manager, no
  CallKit, the system's session left alone), `CallScreenTests` (the screen's states and layout), `CallPersonaIntentTests`,
  the `UrgentCall*Tests` suites, `VoiceCallsTests`, `VoiceConversationTests` (`.call`, hold frames, `surface`),
  `LocalProxyRouteTests` and the CarPlay suites; `CallFollowUpTests` (the assistant's name on the system call and the
  ring, the guards following the live call, voice chat refused or asked, dictation and recordings refused, web media
  suspended, the call keeping its microphone, the assistant's look per state, the call's window, the bar's room, the
  template icon; and from the second review: a disconnect refused while the call talks through the hub, the call
  keeping the look and name it was made with, a package's pictures decoded off the main actor, an ended call's audio
  waited for, an urgent call during the hand-off said to the question, dictation asked again after its permission, a
  paused recording stopped under a call leaving its session, a start stopped by a call during the Bluetooth search or
  the microphone prompt, the icon drawn off the main actor before the manager, the bar's room watched only in front);
  `CallSourceTests` also checks every audio-session site's verdict, that every way in asks first, and those rules
  (`whatTheSecondReviewFoundStaysFixed`).
- UI (`apple/PersonaUITests/`, against `MOCK_VOICE=1` `scripts/mock/mock_hub.py`, `docs/operations/testing.md`):
  `CallScreenTests` (each sample, largest text, the app's text size, the bar, + › Call Persona, the urgent call's Mute
  and its page on hold with Resume, and the real path: a call that keeps sending audio on the Agents page, in an Agent's
  conversation, on the Home Screen, with another app in front and with the phone locked, the mock's audio count at each
  step written to `real-flow.json` beside the screenshots; `testNothingInTheAppEndsTheCall`, the walk through the app
  with the bar over the Agents page, a conversation, the + panel and Settings, dictation refused, Voice Chat asked and
  cancelled from the panel and the handset, a typed line, Home, another app and the lock, the counts in `walk.json`;
  `testTheAssistantOnTheCallScreen`; `testTheCallFollowsTheAssistant` with `MOCK_PERSONAS=builtin MOCK_LOOK=nwverse`) and
  `CallPersonaShortcutTests` (Call Persona and Talk to Persona from Spotlight, over Settings and an Agent's
  conversation, the microphone refused).
- The mock's `GET /mock/voice2` keeps each start's `surface` (`"call"` for a system call) and the app's `hold` frames
  (`holds`, oldest first); its voice extension lists `hold` and answers a hold as the extension does.
- The microphone on a fresh install: `xcrun simctl privacy <udid> grant microphone <bundle id>` before a run that should
  not see the prompt (the Simulator's microphone may record only silence; the frames still count).

## Only a device shows

- The system's call UI, the Lock Screen and Dynamic Island call, the Recents row and which activity type a redial
  delivers.
- When `didActivate` arrives for an outgoing call, and whether the 5 s watchdog ever fires.
- The engine-start system mute and its 0.5 s window; Mute and hang-up from AirPods and from the system's call UI.
- Hold & Accept with an urgent call; whether a cellular call arrives as a pause or as an interruption, and the resume.
- A held call suspended in the background and its reconnect.
- The earpiece by default for both kinds, Speaker on the page and in the system UI, real audio with echo cancellation,
  Bluetooth and the car.
- Siri's phrases (`docs/apple/siri.md`, "Verification on a phone").
