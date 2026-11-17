import Foundation
import Testing
@testable import Persona

/// Who owns the app's one call (docs/assistant/voice.md 6.3, 6.5, 6.10「VoiceCalls 的所有权」): one at a time, another
/// conversation's call ends the one before; the background and leaving the page end none on the iPhone (#9887).
@MainActor
struct VoiceCallsTests {
    @Test func oneCallAtATimeAndAnotherConversationEndsTheFirst() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let first = try #require(calls.current)
        #expect(calls.isBound(to: rig.key))

        // The same conversation again: the same call, no second socket.
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(calls.current === first && rig.sockets.count == 1)

        let other = SessionKey(group: rig.key.group, machineID: "m2", sessionID: "s9")
        await calls.start(other, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(first.phase == .ended(.replaced))
        #expect(calls.current !== first && calls.isBound(to: other) && !calls.isBound(to: rig.key))
        #expect(rig.sockets.count == 2 && rig.sockets[0].closed)
        #expect(rig.sockets[1].start?["target"] as? [String: String] == ["machine_id": "m2", "session_id": "s9"])
    }

    /// The background ends no voice chat on the iPhone (#9887 one button: switching away or locking never stops it): the
    /// phone's goes on with its socket, only its levels stop until the app is back; the car's goes on as before.
    @Test func theBackgroundEndsNoVoiceChat() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .carPlay, deps: rig.deps)
        calls.appDidEnterBackground()
        #expect(calls.current?.phase == .connecting, "a call from the car goes on")

        let phone = VoiceRig()
        let phoneCalls = VoiceCalls()
        await phoneCalls.start(phone.key, extensionID: "voice", surface: .phone, deps: phone.deps)
        let socket = try #require(phone.socket)
        socket.ready(phone.key)
        let chat = try #require(phoneCalls.current)
        phoneCalls.appDidEnterBackground()
        #expect(chat.phase.isLive && !socket.closed, "the phone's voice chat goes on in the background")
        #expect(phone.audio.uplink?.levelsPaused == true, "no levels where nobody sees them")
        phone.audio.capture(Data(repeating: 0x7F, count: 3200))
        #expect(chat.levels.input == 0)
        phoneCalls.appWillEnterForeground()
        #expect(phone.audio.uplink?.levelsPaused == false, "the levels again in front")
        #expect(phoneCalls.isBound(to: phone.key))
    }

    @Test func aSurfaceEndsOnlyItsOwnCallButTheUserEndsAny() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        calls.end(.left, from: .carPlay)
        #expect(calls.current?.phase == .connecting, "the car does not stop the phone's call")
        calls.end(.user, from: .carPlay)
        #expect(calls.current == nil, "结束 tapped ends it whoever started it, and nothing stays")
    }

    /// Leaving the page leaves the phone's voice chat on, with the bar back to it (#9887 one button); coming back takes
    /// the bar away; an ended one's Continue row goes with the page.
    @Test func leavingThePageKeepsTheVoiceChatWithTheBarBack() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        calls.leave(SessionKey(machineID: "m1", sessionID: "other"))
        #expect(calls.isBound(to: rig.key) && !calls.away, "another page leaving changes nothing")
        let call = try #require(calls.current)
        calls.leave(rig.key)
        #expect(!call.phase.isEnded && calls.away && calls.awayChat === call, "the chat goes on, the bar back to it shows")
        #expect(rig.socket?.closed == false)
        calls.returned(SessionKey(machineID: "m1", sessionID: "other"))
        #expect(calls.away, "another page coming back leaves the bar")
        calls.returned(rig.key)
        #expect(!calls.away && calls.awayChat == nil && calls.current === call)

        calls.leave(rig.key)
        call.end(.idle)
        #expect(calls.awayChat == nil, "an ended chat has no bar")
        calls.leave(rig.key)
        #expect(calls.current == nil, "its Continue row goes with the page")
        #expect(rig.microphone.claim(UUID()))
    }

    /// An interruption pauses the phone's voice chat (on hold: silence up, the extension told) and it goes on when the
    /// interruption ends with `shouldResume` (the audio brought back); without it, or when the audio will not come back,
    /// it ends as before, with Continue (#9887).
    @Test func anInterruptionPausesThePhonesVoiceChatAndItGoesOn() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let socket = try #require(rig.socket)
        socket.ready(rig.key)
        let chat = try #require(calls.current)
        rig.audio.emit(.interrupted)
        #expect(chat.held && chat.phase.isLive && chat.pausedByInterruption)
        rig.audio.emit(.interruptionEnded(shouldResume: true))
        #expect(!chat.held && chat.phase.isLive && rig.audio.resumes == 1)

        rig.audio.emit(.interrupted)
        rig.audio.resumeError = VoiceAudioError.session
        rig.audio.emit(.interruptionEnded(shouldResume: true))
        #expect(chat.phase == .ended(.interrupted), "the audio would not come back")

        let other = VoiceRig()
        let otherCalls = VoiceCalls()
        await otherCalls.start(other.key, extensionID: "voice", surface: .phone, deps: other.deps)
        let otherChat = try #require(otherCalls.current)
        other.audio.emit(.interrupted)
        other.audio.emit(.interruptionEnded(shouldResume: false))
        #expect(otherChat.phase == .ended(.interrupted) && other.audio.resumes == 0)

        // The car's still ends at an interruption.
        let car = VoiceRig()
        let carCalls = VoiceCalls()
        await carCalls.start(car.key, extensionID: "voice", surface: .carPlay, deps: car.deps)
        car.audio.emit(.interrupted)
        #expect(carCalls.current?.phase == .ended(.interrupted))
    }

    @Test func leavingAPageDoesNotEndTheCarsCall() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .carPlay, deps: rig.deps)
        calls.leave(rig.key)
        #expect(calls.current?.phase == .connecting)
    }

    /// A call to Persona (#9290) goes on in the background and off the page; only its manager ends it.
    @Test func aSystemCallGoesOnInTheBackgroundAndOffThePage() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        await calls.begin(call)
        calls.appDidEnterBackground()
        calls.leave(rig.key)
        calls.end(.left, from: .phone)
        #expect(calls.current === call && call.phase == .connecting)
        #expect(rig.socket?.closed == false)
    }

    /// The same conversation from another surface is a call of its own: a voice chat on the main conversation while a
    /// call to Persona is on it ends the call, not picks it up.
    @Test func anotherSurfaceOnTheSameConversationIsACallOfItsOwn() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let chat = try #require(calls.current)
        let call = rig.call(surface: .call)
        await calls.begin(call)
        #expect(chat.phase == .ended(.replaced) && calls.current === call)
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(call.phase == .ended(.replaced) && calls.current?.surface == .phone)
    }

    @Test func anEndedSystemCallIsNeverPickedUpAgain() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        await calls.begin(call)
        call.end(.lost)
        await calls.resume(rig.key)
        #expect(call.phase == .ended(.lost) && rig.sockets.count == 1)
        calls.release(call)
        #expect(calls.current == nil)
    }

    /// A system call goes on when its page goes (it is `PersonaCalls`'); once it ended by itself its line (「通话已结束 · 继续」)
    /// stays on the page and goes with it (#9290).
    @Test func aSystemCallOutlivesItsPageAndItsEndedLineGoesWithIt() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        await calls.begin(call)
        calls.leave(rig.key)
        #expect(calls.current === call && !call.phase.isEnded, "the page going leaves the system call on")
        call.end(.lost)
        #expect(calls.current === call, "its ended line stays while the page is there")
        calls.leave(rig.key, from: .carPlay)
        #expect(calls.current === call, "only the phone's page lets it go")
        calls.leave(rig.key)
        #expect(calls.current == nil)
    }

    /// A system call's voice ended before its start ran (the system ended the call at once): it never begins.
    @Test func aSystemCallsVoiceThatEndedBeforeItBeganNeverBegins() async {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        let call = rig.call(surface: .call)
        call.end(.user)
        await calls.begin(call)
        #expect(call.phase == .ended(.user) && calls.current == nil)
        #expect(rig.sockets.isEmpty && rig.audio.uplink == nil && rig.microphone.isFree)
    }

    /// While Persona's urgent call is on (#9290) nothing starts or is picked up again from any surface, and nothing is
    /// touched: the call before goes on (or stays ended), no audio opens.
    @Test func noVoiceChatUnderAnUrgentCall() async throws {
        let rig = VoiceRig()
        var urgent = false
        let calls = VoiceCalls()
        calls.urgentCallOn = { urgent }
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        let first = try #require(calls.current)
        first.end(.interrupted)
        urgent = true
        let stops = rig.audio.stops
        for surface in [VoiceSurface.phone, .carPlay] {
            let other = SessionKey(group: rig.key.group, machineID: "m2", sessionID: "s9")
            #expect(await calls.start(other, extensionID: "voice", surface: surface, deps: rig.deps) == .urgentCall)
        }
        #expect(await calls.resume(rig.key) == .urgentCall)
        #expect(calls.current === first && first.phase == .ended(.interrupted))
        #expect(rig.sockets.count == 1 && rig.audio.uplink == nil && rig.audio.stops == stops)
        urgent = false
        #expect(await calls.resume(rig.key) == nil)
        #expect(first.phase == .connecting && rig.sockets.count == 2)
    }

    @Test func continueOnAnEndedCallPicksItUp() async throws {
        let rig = VoiceRig()
        let calls = VoiceCalls()
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        rig.socket?.ready(rig.key)
        // An interruption the system says not to resume from ends it (one that may resume only pauses it, #9887).
        rig.audio.emit(.interrupted)
        rig.audio.emit(.interruptionEnded(shouldResume: false))
        let call = try #require(calls.current)
        #expect(call.phase == .ended(.interrupted))
        await calls.resume(rig.key)
        #expect(calls.current === call && call.phase == .connecting && rig.sockets.count == 2)
        // Starting the same conversation again does the same.
        call.end(.lost)
        await calls.start(rig.key, extensionID: "voice", surface: .phone, deps: rig.deps)
        #expect(calls.current === call && rig.sockets.count == 3)
    }
}
