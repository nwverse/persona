import Foundation
import ImageIO
import SwiftUI
import Testing
import UIKit
import WebKit
@testable import Persona

/// The call is the assistant's, and nothing in the app ends it (#9290 follow-up, docs/apple/calls.md): the system call
/// and the pages carry the assistant's name, the call screen shows its look, the call's window shows the bar and the
/// screen over everything, the system call UI gets a template icon, and every in-app action that would take the call's
/// microphone or audio asks first or is refused, the call untouched.
@MainActor
struct CallFollowUpTests {
    // MARK: The assistant's name

    /// The handle's value (what the system's call UI, the Lock Screen and Recents show) is the assistant's name, for a
    /// call to Persona and for an urgent call (whose own name stays the display name only); "Persona" when it has none.
    @Test func theSystemCallCarriesTheAssistantsName() async throws {
        let rig = CallRig()
        rig.assistant = "奶蛙"
        let call = await rig.dial()
        #expect(call.name == "奶蛙" && rig.system.startNames == ["奶蛙"])
        let urgent = UUID()
        try await rig.calls.ring(urgent, callID: "call_1", name: "Urgent: the build broke")
        let ring = try #require(rig.system.rings.last)
        #expect(ring.name == "奶蛙" && ring.title == "Urgent: the build broke")

        let unnamed = CallRig()
        unnamed.assistant = "  "
        _ = await unnamed.dial()
        #expect(unnamed.system.startNames == ["Persona"])
        #expect(CallHandle.name(nil) == "Persona" && CallHandle.name(" Mochi ") == "Mochi")
    }

    /// A redial with any value calls Persona: the activity is a call whatever its handle says.
    @Test func aRedialWithAnyNameIsACall() {
        let activity = NSUserActivity(activityType: "INStartCallIntent")
        #expect(PersonaCalls.isCall(activity))
        #expect(PersonaCalls.handle(activity) == nil)
    }

    // MARK: The guards

    /// The app's guards follow the live call: from "Calling…" to its end (not a DEBUG sample), the assistant's name for
    /// the questions, and the call's own conversation (its handset shows the call screen).
    @Test func theGuardsFollowTheLiveCall() async {
        let rig = CallRig()
        rig.assistant = "Mochi"
        var changes: [Bool] = []
        rig.calls.onLiveChange = { changes.append($0) }
        #expect(!rig.calls.guardsLive && rig.calls.liveOwnerCallName == nil)
        let (call, _) = await rig.connected()
        #expect(rig.calls.guardsLive && rig.calls.liveOwnerCallName == "Mochi" && changes == [true])
        #expect(rig.calls.isOwnerCallConversation(rig.voice.key))
        #expect(!rig.calls.isOwnerCallConversation(SessionKey(group: rig.voice.key.group, machineID: "m2", sessionID: "s2")))
        rig.calls.hangUp()
        rig.system.act(.end, call.id)
        #expect(call.phase.isEnded && !rig.calls.guardsLive && rig.calls.liveOwnerCallName == nil && changes == [true, false])
        #expect(!rig.calls.isOwnerCallConversation(rig.voice.key))
    }

    /// An urgent call ringing counts once the system shows it; one Focus refused counts only once answered.
    @Test func anUrgentCallGuardsOnceItRings() async throws {
        let rig = CallRig()
        try await rig.calls.ring(UUID(), callID: "call_1", name: "Urgent")
        #expect(rig.calls.guardsLive && rig.calls.liveOwnerCallName == nil, "an urgent call asks no voice chat question: it refuses")
        let quiet = CallRig()
        quiet.system.ringError = CallSystemStandIn.refused
        await #expect(throws: CallSystemStandIn.self) { try await quiet.calls.ring(UUID(), callID: "call_2", name: "Urgent") }
        #expect(!quiet.calls.guardsLive)
    }

    /// A voice chat elsewhere is refused while a call to Persona is on, touching nothing, until the user says to end the
    /// call (`endingCall`); Continue the same. CarPlay's never says it: it is told the call is on the phone.
    @Test func noVoiceChatOverACallToPersonaUnlessTheUserEndsIt() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        var name: String? = nil
        var ended = 0
        calls.systemCallName = { name }
        calls.endSystemCall = { ended += 1; name = nil }
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let first = try #require(calls.current)
        first.end(.interrupted)
        name = "Mochi"
        ended = 0 // the first start asked for no call to end: there was none
        let other = SessionKey(group: rig.key.group, machineID: "m2", sessionID: "s9")
        for surface in [VoiceSurface.phone, .carPlay] {
            #expect(await calls.start(other, extensionID: "voice", surface: surface, deps: rig.deps) == .systemCallOn("Mochi"))
        }
        #expect(await calls.resume(rig.key) == .systemCallOn("Mochi"))
        #expect(ended == 0 && calls.current === first && rig.sockets.count == 1, "nothing was touched")
        #expect(await calls.resume(rig.key, endingCall: true) == nil)
        #expect(ended == 1 && first.phase == .connecting && rig.sockets.count == 2)
    }

    /// The question's words and what its yes does.
    @Test func theQuestionNamesTheAssistantAndWhatItsYesDoes() {
        let key = SessionKey(group: "g", machineID: "m", sessionID: "s")
        let start = VoiceUnavailableNote.endCallFirst("Mochi", key: key)
        #expect(start.title.contains("Mochi") && start.proceed == .startVoice(key) && start.settings == nil)
        #expect(VoiceUnavailableNote.endCallFirst("Mochi", key: key, again: true).proceed == .resumeVoice(key))
        #expect(VoiceUnavailableNote.systemCallOn("Mochi").proceed == nil)
        #expect(VoiceUnavailableNote.urgentCall.proceed == nil, "a refusal stays a refusal")
    }

    /// Dictation is refused at once while a call is on: it says why, and nothing opens (the tap's bar, the hold, the
    /// microphone); the call manager's answer is what the gate asks.
    @Test func dictationIsRefusedOnACall() async {
        let note = "On a call"
        let dictation = Dictation()
        dictation.callGate = { note }
        var failures: [String] = []
        dictation.start(update: { _ in }, failed: { failures.append($0) })
        #expect(failures == [note] && !dictation.isListening)

        let bar = DictationBarModel()
        bar.start(dictation, hints: [], reduceMotion: true)
        #expect(!bar.on && AppModel.shared.toast?.message == note, "the bar opened")
        let hold = ComposerHold()
        var text = "draft"
        hold.begin(dictation, text: Binding(get: { text }, set: { text = $0 }), reduceMotion: true)
        #expect(!hold.holding && text == "draft")

        let free = Dictation()
        free.callGate = { nil }
        #expect(free.callRefusal == nil)
    }

    /// A recording is refused while a call is on, before anything is asked or opened; the call manager answers.
    @Test func aRecordingIsRefusedOnACall() async {
        let capture = RecordingCapture.shared
        guard capture.state == .idle else { return }
        let gate = capture.callGate
        capture.callGate = { "End the call first" }
        defer { capture.callGate = gate }
        await capture.start(locale: "en-US")
        #expect(capture.state == .idle && capture.error == "End the call first")
        let outcome = await MeetingRecorder.shared.startMeeting(inForeground: false, openPage: false)
        #expect(outcome == .failed("End the call first") && capture.state == .idle)
        capture.error = nil
    }

    /// The gate's words, while the call manager says a call is on.
    @Test func theGateSaysWhyWhileACallIsOn() {
        let live = LiveCallGate.isLive
        LiveCallGate.isLive = { true }
        let dictation = LiveCallGate.dictationRefusal, recording = LiveCallGate.recordingRefusal, on = LiveCallGate.callIsLive
        LiveCallGate.isLive = live
        #expect(on && dictation == String(localized: "On a call — dictation is off until it ends"))
        #expect(recording == String(localized: "End the call first"))
    }

    /// Web pages' media is suspended while a call is live, also in a page made during it, and let go after.
    @Test func webMediaWaitsForTheCall() {
        let guardian = CallMediaGuard()
        let before = WKWebView()
        guardian.register(before)
        guardian.callLive(true)
        #expect(guardian.suspended)
        guardian.register(WKWebView())
        guardian.callLive(false)
        #expect(!guardian.suspended)
    }

    /// A system call keeps its microphone: a recording or dictation that asks while it is on is refused and the call goes
    /// on (an in-app voice chat still gives way: `VoiceConversationTests.aRecordingTakesTheMicrophoneFromTheCall`).
    @Test func aSystemCallKeepsItsMicrophone() async {
        let rig = CallRig()
        let (call, socket) = await rig.connected()
        #expect(!rig.voice.microphone.claim(UUID()), "something took the call's microphone")
        #expect(call.phase.isLive && !socket.closed && call.voice?.phase.isEnded == false)
    }

    // MARK: The call screen and its window

    /// The assistant's look follows the call: moving while it speaks or works, idling while it listens, still on hold,
    /// muted and over, the ring and a mark saying which.
    @Test func theAssistantsLookFollowsTheCall() {
        #expect(CallAvatarLook.make(.speaking) == CallAvatarLook(state: .working, still: false, ring: .voice, mark: nil))
        #expect(CallAvatarLook.make(.working).state == .working)
        #expect(CallAvatarLook.make(.listening) == CallAvatarLook(state: .idle, still: false, ring: .voice, mark: nil))
        #expect(CallAvatarLook.make(.hearing).followsLevel)
        #expect(CallAvatarLook.make(.connecting) == CallAvatarLook(state: .idle, still: false, ring: .lap, mark: nil))
        #expect(CallAvatarLook.make(.paused) == CallAvatarLook(state: .idle, still: true, ring: .held, mark: .hold))
        #expect(CallAvatarLook.make(.muted) == CallAvatarLook(state: .idle, still: true, ring: .quiet, mark: .muted))
        #expect(CallAvatarLook.make(.ended) == CallAvatarLook(state: .offline, still: true, ring: .ended, mark: nil))
        for phase in VoiceOrb.Phase.allCases where CallAvatarLook.make(phase).still {
            #expect(!CallAvatarLook.make(phase).ringMoves, "\(phase): a still look's ring moves")
        }
    }

    /// The window shows the call screen while it is presented, the bar while the call is live and minimised, a refusal's
    /// alert, and nothing under an answered urgent call's page or with no call.
    @Test func theCallWindowShowsWhatTheCallIs() async {
        let rig = CallRig()
        #expect(CallWindowMode.make(presented: false, owner: nil, refusal: false, urgentUp: false) == .none)
        #expect(CallWindowMode.make(presented: false, owner: nil, refusal: true, urgentUp: false) == .refusal)
        let call = await rig.dial()
        #expect(CallWindowMode.make(presented: true, owner: call, refusal: false, urgentUp: false) == .screen)
        #expect(CallWindowMode.make(presented: false, owner: call, refusal: false, urgentUp: false) == .bar)
        #expect(CallWindowMode.make(presented: true, owner: call, refusal: false, urgentUp: true) == .none)
        #expect(CallWindowMode.make(presented: false, owner: call, refusal: false, urgentUp: true) == .none)
        rig.calls.hangUp()
        rig.system.act(.end, call.id)
        #expect(CallWindowMode.make(presented: false, owner: call, refusal: false, urgentUp: false) == .none, "no bar for an ended call")
    }

    /// The room a page or sheet gets under the bar: what reaches past its content's top, with a small gap; none for a
    /// sheet lower down.
    @Test func theBarsRoomIsWhatThePageUnderItNeeds() {
        let status: CGFloat = 62, bar: CGFloat = 48
        #expect(CallWindow.room(contentTop: status, barBottom: status + bar) == bar + Theme.Space.xs)
        #expect(CallWindow.room(contentTop: status + 10, barBottom: status + bar) == bar - 10 + Theme.Space.xs, "a page sheet")
        #expect(CallWindow.room(contentTop: 300, barBottom: status + bar) == 0, "a sheet at half height")
    }

    /// The call screen's middle sizes the assistant as it sized the orb: its share of the page, then what the words
    /// leave, down to the voice bar's orb's size.
    @Test func theAssistantGivesWayToTheWords() throws {
        #expect(CallScreen.avatarSize(page: 900, free: .infinity, scale: 1) == Theme.Size.callAvatar)
        #expect(CallScreen.avatarSize(page: 900, free: 90, scale: 1) == 90)
        #expect(CallScreen.avatarSize(page: 900, free: Theme.Size.voiceOrb - 1, scale: 1) == nil)
    }

    // MARK: The system call UI's icon

    /// A valid PNG with an alpha channel, 40 pt at 3×: clear around Mochi and in its eyes, solid in its body.
    @Test func theTemplateIconIsAnAlphaSilhouette() throws {
        let data = try #require(CallIcon.templatePNG())
        #expect(data.prefix(8) == Data([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]), "not a PNG")
        let source = try #require(CGImageSourceCreateWithData(data as CFData, nil))
        let image = try #require(CGImageSourceCreateImageAtIndex(source, 0, nil))
        #expect(image.width == 120 && image.height == 120)
        #expect(![.none, .noneSkipFirst, .noneSkipLast].contains(image.alphaInfo), "no alpha")
        let alpha = try Self.alpha(image)
        func at(_ x: Double, _ y: Double) -> UInt8 { alpha[Int(y * 119) * 120 + Int(x * 119)] }
        #expect(at(0, 0) == 0 && at(1, 1) == 0, "the corners are not clear")
        #expect(at(0.5, 0.62) == 255, "Mochi's body is not solid")
        let eye = Mochi.eyes[0]
        #expect(at(eye.x, eye.y) == 0, "the eye is not cut out")
        #expect(alpha.contains(0) && alpha.contains(255))
    }

    /// The image's alpha, row by row from the top.
    private static func alpha(_ image: CGImage) throws -> [UInt8] {
        var out = [UInt8](repeating: 0, count: image.width * image.height)
        let ctx = try #require(CGContext(data: &out, width: image.width, height: image.height, bitsPerComponent: 8,
                                         bytesPerRow: image.width, space: CGColorSpaceCreateDeviceGray(),
                                         bitmapInfo: CGImageAlphaInfo.alphaOnly.rawValue))
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        return out
    }
}
