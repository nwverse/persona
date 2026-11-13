import Foundation
import Testing
@testable import Persona

// Hub transcription on the phone (#3862 phase 5; contract docs/assistant/meeting-minutes.md「Phase 5: hub transcription」,
// the phone's side docs/apple/recording.md「Hub transcription (phase 5)」): the audio socket's frames, the 60 seconds kept
// in memory and what falls out of them, the resend from `ready.from`, the codes the hub may end it with, the Wi-Fi rule,
// the timeline's limits, the mode in effect, what the page shows, the gap uploads and the settings. The hub is a
// `FakeHub` and its socket a `FakeVoiceSocket`; nothing reaches a network or a microphone.

private func json(_ text: String) -> [String: Any] {
    (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:]
}

private func samples(_ count: Int, from: Int = 0) -> [Int16] { (0..<count).map { Int16(truncatingIfNeeded: from + $0) } }

private func decoded(_ data: Data) -> [Int16] {
    stride(from: 0, to: data.count - 1, by: 2).map { Int16(littleEndian: Int16(data[$0]) | Int16(data[$0 + 1]) << 8) }
}

// MARK: - The wire

struct MeetingAudioWireTests {
    @Test func phoneFramesSayWhatTheContractSays() throws {
        let timeline = MeetingTimeline(roles: [.init(at: 0, attendance: "listener")], speaking: [.init(start: 4, end: 9, text: "好的")])
        let start = json(MeetingAudioOut.start(at: 12.00003, timeline: timeline))
        #expect(start["type"] as? String == "start")
        #expect(start["at"] as? Double == 12.0) // rounded to a sample
        let t = try #require(start["timeline"] as? [String: Any])
        #expect((t["roles"] as? [[String: Any]])?.first?["attendance"] as? String == "listener")
        #expect((t["speaking"] as? [[String: Any]])?.first?["text"] as? String == "好的")

        let on = json(MeetingAudioOut.speaking(on: true, at: 30, text: "答案"))
        #expect(on["type"] as? String == "speaking" && on["on"] as? Bool == true && on["text"] as? String == "答案")
        let off = json(MeetingAudioOut.speaking(on: false, at: 31, text: "ignored"))
        #expect(off["on"] as? Bool == false && off["text"] == nil, "an off frame carries no text")
        #expect(json(MeetingAudioOut.role(at: 5, attendance: .off))["attendance"] as? String == "off")
        #expect(json(MeetingAudioOut.end(at: 3733.5))["type"] as? String == "end")
        #expect(json(MeetingAudioOut.seek(at: 312.4))["at"] as? Double == 312.4)
    }

    @Test func gapUploadTimesArePlainDecimals() {
        #expect(MeetingAudioClock.query(0) == "0")
        #expect(MeetingAudioClock.query(312.4) == "312.4")
        #expect(MeetingAudioClock.query(1.0 / 16000) == "0.0000625")
        #expect(MeetingAudioClock.query(36000.25) == "36000.25")
        #expect(MeetingAudioClock.query(1e-9) == "0", "rounded to a sample")
        for s in [0.5, 7.0001, 123456.789] { #expect(!MeetingAudioClock.query(s).contains("e"), "no exponent: the hub takes digits and a dot") }
    }

    @Test func hubFramesReadLeniently() {
        #expect(MeetingAudioIn.decode(#"{"type":"ready","from":312.4,"gaps":[[0,4.2],[9,"x"]],"next":57}"#)
                == .ready(from: 312.4, gaps: [MeetingGap(from: 0, to: 4.2)], next: 57))
        #expect(MeetingAudioIn.decode(#"{"type":"ready","from":0,"gaps":[],"next":0}"#) == .ready(from: 0, gaps: [], next: 0))
        #expect(MeetingAudioIn.decode(#"{"type":"ack","at":330}"#) == .ack(at: 330))
        #expect(MeetingAudioIn.decode(#"{"type":"interim","start":328.1,"text":"我们先"}"#)
                == .interim(MeetingInterim(start: 328.1, text: "我们先")))
        #expect(MeetingAudioIn.decode(#"{"type":"line","n":57,"start":320.2,"end":326,"text":"Let's revisit pricing."}"#)
                == .line(MeetingHubLine(n: 57, start: 320.2, end: 326, text: "Let's revisit pricing.", source: "hub")))
        #expect(MeetingAudioIn.decode(#"{"type":"problem","code":"upstream_failed"}"#) == .problem("upstream_failed"))
        #expect(MeetingAudioIn.decode(#"{"type":"problem","code":null}"#) == .problem(nil))
        #expect(MeetingAudioIn.decode(#"{"type":"error","code":"replaced","fatal":true}"#) == .error(code: "replaced"))
        #expect(MeetingAudioIn.decode(#"{"type":"something_new"}"#) == .other, "an unknown type is ignored")
        #expect(MeetingAudioIn.decode("not json") == .other)
        #expect(MeetingAudioIn.decode(#"{"type":"line","n":-1,"start":0,"end":1,"text":"x"}"#) == .other)
        #expect(MeetingAudioIn.decode(#"{"type":"ready","from":"later"}"#) == .other)
    }

    @Test func pcmIsLittleEndianWholeSamples() {
        #expect(MeetingPCM.sample(2) == Int16.max && MeetingPCM.sample(-2) == -Int16.max && MeetingPCM.sample(.nan) == 0)
        let data = MeetingPCM.data([1, -2, 0x0102][...])
        #expect(data == Data([0x01, 0x00, 0xFE, 0xFF, 0x02, 0x01]))
    }

    @Test func transcriptionViewReadsLiveAndDetailMeta() throws {
        let meta = try JSON.decoder.decode(JSONValue.self, from: Data(#"""
        {"transcription": {"mode": "hub", "state": "finishing", "gaps": [[600, 642.5]], "problem": "upstream_failed", "fallback": true}}
        """#.utf8))
        let view = try #require(MeetingTranscriptionView(meta: meta))
        #expect(view.mode == .hub && view.known == .finishing && view.gaps == [MeetingGap(from: 600, to: 642.5)])
        #expect(view.problem == "upstream_failed" && view.fallback && !view.settled)
        let device = try #require(MeetingTranscriptionView(.object(["mode": .string("device")])))
        #expect(device.mode == .device && device.known == nil && device.gaps.isEmpty)
        #expect(MeetingTranscriptionView(.object(["mode": .string("hub"), "state": .string("final"), "gaps": .array([])]))?.settled == true)
        #expect(MeetingTranscriptionView(.object(["mode": .string("hub"), "state": .string("someday")]))?.known == .live)
        #expect(MeetingTranscriptionView(.object(["state": .string("live")])) == nil, "no mode, no transcription")
    }
}

// MARK: - The 60 seconds in memory

struct MeetingAudioRingTests {
    @Test func nothingGoesBeforeReadyThenWholeFramesFromReadyFrom() throws {
        var ring = MeetingAudioRing()
        ring.append(samples(4000), at: 0)
        #expect(ring.next() == nil, "no audio before ready")
        ring.ready(from: 0)
        var got: [Int16] = []
        while case .audio(let data, let at)? = ring.next() {
            #expect(data.count % 2 == 0 && data.count <= 64 << 10)
            #expect(data.count / 2 <= MeetingPCM.frameSamples, "about 100 ms a frame")
            #expect(at == Int64(got.count))
            got += decoded(data)
        }
        #expect(got == samples(4000))
        #expect(MeetingAudioRing().capacity == 60 * 16000)
    }

    @Test func aFrameNeverPassesTheBinaryLimit() {
        var ring = MeetingAudioRing()
        ring.append(samples(40000), at: 0)
        ring.ready(from: 0)
        guard case .audio(let data, _)? = ring.next(maxSamples: 1_000_000) else { Issue.record("no audio"); return }
        #expect(data.count == 64 << 10)
    }

    @Test func aReconnectResendsFromReadyFrom() {
        var ring = MeetingAudioRing()
        ring.append(samples(16000), at: 0)
        ring.ready(from: 0)
        while ring.next() != nil {}
        ring.stop()
        ring.append(samples(8000, from: 16000), at: 16000)
        #expect(ring.next() == nil, "down: nothing streams")
        // The hub held only the first half second: everything after it again.
        ring.ready(from: 8000)
        var got: [Int16] = [], first: Int64?
        while case .audio(let data, let at)? = ring.next() {
            if first == nil { first = at }
            got += decoded(data)
        }
        #expect(first == 8000 && got == samples(16000, from: 8000))
    }

    @Test func aHubBehindTheRingGetsASeekAndTheRestIsAGap() {
        var ring = MeetingAudioRing(seconds: 1)
        ring.append(samples(48000), at: 0) // 3 s: the ring holds the last one
        #expect(ring.start == 32000 && ring.end == 48000)
        #expect(ring.lostGaps == [MeetingGap(from: 0, to: 2)], "unacknowledged audio that fell out is a gap")
        ring.ready(from: 0)
        #expect(ring.next() == .seek(32000))
        guard case .audio(_, let at)? = ring.next() else { Issue.record("no audio after the seek"); return }
        #expect(at == 32000)
    }

    @Test func acknowledgedAudioThatFallsOutIsNoGap() {
        var ring = MeetingAudioRing(seconds: 1)
        ring.append(samples(16000), at: 0)
        ring.ack(16000)
        ring.append(samples(16000, from: 16000), at: 16000)
        #expect(ring.lostGaps.isEmpty)
        ring.append(samples(16000, from: 32000), at: 32000)
        #expect(ring.lostGaps == [MeetingGap(from: 1, to: 2)])
    }

    @Test func aSendThatFallsBehindSeeksToTheOldestHeld() {
        var ring = MeetingAudioRing(seconds: 1)
        ring.append(samples(1600), at: 0)
        ring.ready(from: 0)
        ring.append(samples(40000, from: 1600), at: 1600)
        #expect(ring.next() == .seek(ring.start))
    }

    @Test func aReadyAheadOfTheRingWaitsForTheAudio() {
        var ring = MeetingAudioRing()
        ring.append(samples(1600), at: 0)
        ring.ready(from: 3200)
        #expect(ring.next() == nil)
        ring.append(samples(3200, from: 1600), at: 1600)
        guard case .audio(_, let at)? = ring.next() else { Issue.record("no audio"); return }
        #expect(at == 3200)
    }

    @Test func aJumpInTheClockIsAGapAndASeek() {
        var ring = MeetingAudioRing()
        ring.append(samples(1600), at: 0)
        ring.ready(from: 0)
        _ = ring.next()
        ring.append(samples(1600), at: 16000)
        #expect(ring.next() == .seek(16000))
        // What it held went unacknowledged (sent, never acked), and the jump was never held: both gaps.
        #expect(ring.lostGaps == [MeetingGap(from: 0, to: 1)])
    }
}

// MARK: - The uplink on its socket

@MainActor struct MeetingAudioUplinkTests {
    private func open(_ uplink: MeetingAudioUplink, speaking: RecordingSpan? = nil) -> FakeVoiceSocket {
        let socket = FakeVoiceSocket(URLRequest(url: URL(string: "wss://hub.invalid/api/v1/recordings/x/audio")!))
        let timeline = MeetingTimeline(roles: [.init(at: 0, attendance: "listener")])
        uplink.open(socket, timeline: timeline, speaking: speaking, role: RecordingRoleChange(at: 0, attendance: .listener)) { _, _ in }
        uplink.drain()
        return socket
    }

    @Test func startGoesFirstThenTheOpenSpanAndTheRoleAndNoAudioBeforeReady() throws {
        let uplink = MeetingAudioUplink(recording: UUID())
        uplink.feed(samples(3200), at: 1600)
        let socket = open(uplink, speaking: RecordingSpan(start: 0.15, end: 0.15, text: "答案"))
        #expect(socket.frames.map { $0["type"] as? String } == ["start", "speaking", "role"])
        #expect(socket.start?["at"] as? Double == 0.1, "the oldest second held")
        #expect(socket.frames[1]["on"] as? Bool == true && socket.frames[1]["text"] as? String == "答案")
        uplink.feed(samples(1600, from: 4800), at: 4800)
        uplink.drain()
        #expect(socket.audio.isEmpty, "no binary frame before ready")
    }

    @Test func readyStreamsFromItsFromAndTheLiveAudioFollows() {
        let uplink = MeetingAudioUplink(recording: UUID())
        uplink.feed(samples(16000), at: 0)
        let socket = open(uplink)
        uplink.ready(from: 0.5, generation: uplink.currentGeneration)
        uplink.drain()
        #expect(decoded(socket.audio.reduce(Data(), +)) == samples(8000, from: 8000))
        uplink.feed(samples(1600, from: 16000), at: 16000)
        uplink.drain()
        #expect(decoded(socket.audio.reduce(Data(), +)) == samples(9600, from: 8000))
    }

    @Test func aNewSocketResendsWhatTheHubLacks() {
        let uplink = MeetingAudioUplink(recording: UUID())
        uplink.feed(samples(16000), at: 0)
        let first = open(uplink)
        uplink.ready(from: 0, generation: uplink.currentGeneration)
        uplink.drain()
        uplink.dropped(generation: uplink.currentGeneration)
        uplink.feed(samples(16000, from: 16000), at: 16000)
        let second = open(uplink)
        #expect(second.start?["at"] as? Double == 0)
        uplink.ready(from: 0.75, generation: uplink.currentGeneration)
        uplink.drain()
        #expect(decoded(second.audio.reduce(Data(), +)) == samples(20000, from: 12000))
        #expect(!first.audio.isEmpty)
    }

    @Test func aStaleReadyChangesNothing() {
        let uplink = MeetingAudioUplink(recording: UUID())
        uplink.feed(samples(1600), at: 0)
        let socket = open(uplink)
        uplink.ready(from: 0, generation: uplink.currentGeneration - 1)
        uplink.drain()
        #expect(socket.audio.isEmpty)
    }

    @Test func endFollowsTheLastAudio() {
        let uplink = MeetingAudioUplink(recording: UUID())
        uplink.feed(samples(3200), at: 0)
        let socket = open(uplink)
        uplink.ready(from: 0, generation: uplink.currentGeneration)
        uplink.feed(samples(1600, from: 3200), at: 3200)
        #expect(uplink.end(settle: 0))
        Thread.sleep(forTimeInterval: 0.05)
        uplink.drain()
        #expect(socket.frames.last?["type"] as? String == "end")
        #expect(socket.frames.last?["at"] as? Double == 0.3)
        #expect(decoded(socket.audio.reduce(Data(), +)).count == 4800)
    }
}

// MARK: - Rules

struct MeetingTranscriptionRuleTests {
    typealias Rule = MeetingTranscriptionRule

    @Test func theModeInEffectIsTheHubsWordButNeverHubUnasked() {
        #expect(Rule.decide(nil, asked: nil, answer: .init(mode: "hub")) == nil, "a meeting that named no mode")
        #expect(Rule.decide(nil, asked: .hub, answer: .init(mode: "hub")) == .init(mode: .hub))
        #expect(Rule.decide(nil, asked: .hub, answer: .init(mode: "device", problem: "unavailable"))
                == .init(mode: .device, problem: "unavailable"))
        #expect(Rule.decide(nil, asked: .device, answer: .init(mode: "hub"))?.mode == .device, "audio never leaves unasked")
        #expect(Rule.decide(nil, asked: .hub, answer: .init(mode: "later")) == .init(mode: .device))
    }

    @Test func anOlderManagerKeepsTheMeetingOnThePhoneForGood() {
        let rolled = Rule.decide(.init(mode: .hub), asked: .hub, answer: .init(mode: nil))
        #expect(rolled == .init(mode: .device, problem: nil, olderManager: true))
        #expect(Rule.decide(rolled, asked: .hub, answer: .init(mode: "hub")) == rolled, "a rollback mid-meeting is for the rest of it")
    }

    @Test func errorsAreActedOnByTheirCode() {
        #expect(Rule.reaction(toError: "recording_not_found") == .uploadFirst)
        #expect(Rule.reaction(toError: "bad_frame") == .retry)
        for code in ["replaced", "audio_full", "recording_deleted", "kb_read_only", "not_hub", "transcript_expired",
                     "transcription_final", "a_code_from_tomorrow", ""] {
            #expect(Rule.reaction(toError: code) == .stop, "\(code)")
        }
        #expect(Rule.reaction(toClose: 403) == .stop && Rule.reaction(toClose: 404) == .stop)
        #expect(Rule.reaction(toClose: nil) == .retry && Rule.reaction(toClose: 503) == .retry)
        #expect((1...8).map(Rule.backoff) == [1, 2, 4, 8, 16, 30, 30, 30])
    }

    @Test func cellularIsAllowedUnlessWiFiOnly() {
        #expect(Rule.mayStream(wifiOnly: false, network: .metered))
        #expect(!Rule.mayStream(wifiOnly: true, network: .metered))
        #expect(Rule.mayStream(wifiOnly: true, network: .unmetered))
        #expect(!Rule.mayStream(wifiOnly: false, network: .none))
    }

    @Test func thePageShowsTheHubWhileItIsUpAndHealthy() {
        func shown(_ hub: Bool = true, up: Bool = true, everUp: Bool = true, down: Bool = false, problem: String? = nil,
                   stopped: Bool = false, role: MeetingAttendance = .listener) -> Rule.Shown {
            Rule.shown(hub: hub, socketUp: up, everUp: everUp, down: down, problem: problem, stopped: stopped, role: role)
        }
        #expect(shown(false) == .init(hubInterim: false, hubLines: false, phoneLines: true, fellBack: false))
        #expect(shown() == .init(hubInterim: true, hubLines: true, phoneLines: false, fellBack: false))
        #expect(shown(problem: "no_interim").hubLines && !shown(problem: "no_interim").fellBack)
        #expect(shown(problem: "upstream_failed") == .init(hubInterim: false, hubLines: false, phoneLines: true, fellBack: true))
        #expect(shown(problem: "a_new_problem").fellBack)
        #expect(shown(role: .off) == .init(hubInterim: true, hubLines: false, phoneLines: true, fellBack: false),
                "role off: the hub's caption and the phone's own lines")
        #expect(shown(stopped: true).fellBack && shown(stopped: true).phoneLines)
        #expect(!shown(up: false, everUp: false).fellBack, "the first connect is no fallback yet")
        #expect(shown(up: false, everUp: true).fellBack)
        #expect(shown(up: false, everUp: false, down: true).fellBack, "never up and down: fallen back")
    }

    @Test func aStartAsksForTheDefaultOrThePickWhileTheHubIsReady() {
        #expect(MeetingAttendanceDefaults.startMode(listed: false, hubDefault: .hub, ready: true) == nil)
        #expect(MeetingAttendanceDefaults.startMode(listed: true, hubDefault: .hub, ready: true) == .hub)
        #expect(MeetingAttendanceDefaults.startMode(listed: true, hubDefault: .hub, ready: false) == .device)
        #expect(MeetingAttendanceDefaults.startMode(listed: true, hubDefault: nil, ready: true) == .device)
        #expect(MeetingAttendanceDefaults.startMode(listed: true, hubDefault: .device, ready: true, picked: .hub) == .hub)
        #expect(MeetingAttendanceDefaults.startMode(listed: true, hubDefault: .hub, ready: false, picked: .hub) == .device)
        #expect(MeetingAttendanceDefaults.startMode(listed: true, hubDefault: .hub, ready: true, picked: .device) == .device)
    }

    @Test func theFlagComesOnlyBesideTheConversationAndTheLiveView() {
        let store = UserDefaults(suiteName: "transcription-\(UUID())")!
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation", "meeting_transcription"], role: nil, store)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.transcriptionKey), "not without meeting_live_view")
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation", "meeting_live_view",
                                                   "meeting_transcription"], role: nil, store)
        #expect(store.bool(forKey: MeetingAttendanceDefaults.transcriptionKey))
    }
}

// MARK: - The timeline and the upload

struct MeetingTimelineTests {
    @Test func theTimelineStaysWithinTheHubsLimits() {
        let roles = (0..<150).map { RecordingRoleChange(at: Double($0), attendance: $0 % 2 == 0 ? .listener : .off) }
        let spans = (0..<1200).map { RecordingSpan(start: Double($0) * 2, end: Double($0) * 2 + 1, text: "x") }
        let t = MeetingTimeline(roles: roles, spoken: spans)
        #expect(t.roles.count == 100 && t.roles.first?.at == 0 && t.roles.last?.at == 99, "the first 100 role changes")
        #expect(t.speaking.count == 1000 && t.speaking.last?.start == 1998, "the first 1000 spans")
    }

    @Test func malformedEntriesAreLeftOutAndTextsCutOnACharacter() throws {
        let long = String(repeating: "会", count: 1500) // 4500 bytes
        let t = MeetingTimeline(roles: [RecordingRoleChange(at: -1, attendance: .listener), RecordingRoleChange(at: 2, attendance: .participant)],
                                spoken: [RecordingSpan(start: 9, end: 3, text: "backwards"), RecordingSpan(start: 1, end: 2, text: long),
                                         RecordingSpan(start: 0, end: 0.5, text: nil), RecordingSpan(start: .nan, end: 1)])
        #expect(t.roles == [.init(at: 2, attendance: "participant")])
        #expect(t.speaking.count == 2 && t.speaking[0].start == 0 && t.speaking[0].text == nil)
        let cut = try #require(t.speaking[1].text)
        #expect(cut.utf8.count == 3999 && cut.allSatisfy { $0 == "会" })
        let encoded = json(String(decoding: try JSONEncoder().encode(t), as: UTF8.self))
        #expect((encoded["speaking"] as? [[String: Any]])?.first?.keys.sorted() == ["end", "start"], "no text: none sent")
    }

    @Test func aMeetingThatAskedForAModeKeepsItsRoleChanges() {
        var a = RecordingAttendance.room(.listener, private: false, photos: false, liveView: true, transcription: .hub, at: 0)
        #expect(a.roles == [RecordingRoleChange(at: 0, attendance: .listener)])
        a.set(.off, at: 30)
        a.set(.off, at: 31)
        a.set(.participant, at: 60)
        #expect(a.roles.map(\.attendance) == [.listener, .off, .participant] && a.roles.map(\.at) == [0, 30, 60])
        var plain = RecordingAttendance.room(.listener, private: false, photos: false)
        plain.set(.off, at: 30)
        #expect(plain.roles.isEmpty, "a meeting that named no mode keeps none")
    }

    @MainActor @Test func aNoteTurnedIntoAMeetingStartsItsRolesThere() {
        let start = RecordingAttendance.room(.listener, private: false, photos: false, liveView: true, transcription: .hub)
        let a = MeetingAttendees.resumed(nil, start: start, at: 30)
        #expect(a.transcription == .hub && a.roles == [RecordingRoleChange(at: 30, attendance: .listener)])
    }

    @Test func theAttendanceRoundTripsAndAnUnknownModeNamesNone() throws {
        let a = RecordingAttendance.room(.participant, private: false, photos: false, liveView: true, transcription: .hub)
        #expect(try JSON.decoder.decode(RecordingAttendance.self, from: JSON.encoder.encode(a)) == a)
        let later = try JSON.decoder.decode(RecordingAttendance.self, from: Data(#"{"role":"listener","transcription":"quantum"}"#.utf8))
        #expect(later.transcription == nil)
    }

    private func meeting(_ attendance: RecordingAttendance) -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: Date(timeIntervalSince1970: 1_800_000_000), source: .phone, title: "Weekly", active: true)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = attendance
        return e
    }

    private func upload(_ e: RecordingEntry) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: JSON.encoder.encode(RecordingUpload(e))) as? [String: Any] ?? [:]
    }

    @Test func aHubModeUploadCarriesTheModeAndTheTimeline() throws {
        var a = RecordingAttendance.room(.listener, private: false, photos: false, liveView: true, transcription: .hub)
        a.spoken = [RecordingSpan(start: 4, end: 9, text: "答案")]
        let body = try upload(meeting(a))
        #expect(body["transcription"] as? String == "hub")
        let timeline = try #require(body["timeline"] as? [String: Any])
        #expect((timeline["roles"] as? [[String: Any]])?.count == 1)
        #expect((timeline["speaking"] as? [[String: Any]])?.first?["text"] as? String == "答案")
    }

    @Test func aDeviceModeUploadNamesItsModeOnlyAndOneWithoutTheFlagNeither() throws {
        let device = try upload(meeting(.room(.listener, private: false, photos: false, liveView: true, transcription: .device)))
        #expect(device["transcription"] as? String == "device" && device["timeline"] == nil)
        let none = try upload(meeting(.room(.listener, private: false, photos: false, liveView: true)))
        #expect(none["transcription"] == nil && none["timeline"] == nil)
        var note = meeting(.room(.listener, private: false, photos: false, liveView: true, transcription: .hub))
        note.meeting = nil
        #expect(try upload(note)["transcription"] == nil, "a note names no mode")
    }
}

// MARK: - Gap uploads

struct MeetingGapUploadTests {
    @Test func gapsBecomePiecesOfAtMostFiveMinutesWithinThePhonesAudio() {
        let plan = MeetingGapUpload.plan([MeetingGap(from: 0, to: 700), MeetingGap(from: 690, to: 720), MeetingGap(from: 900, to: 1000)], held: 950)
        #expect(plan == [.init(at: 0, seconds: 300), .init(at: 300, seconds: 300), .init(at: 600, seconds: 120), .init(at: 900, seconds: 50)])
        #expect(MeetingGapUpload.plan([MeetingGap(from: 1000, to: 1100)], held: 950).isEmpty, "past the phone's audio")
        #expect(300 * 16000 * 2 <= 10 << 20, "a piece fits a request")
    }

    @Test func refusalsInTheContractsOrderEachHaveTheirAnswer() {
        typealias R = MeetingGapUpload
        #expect(R.reaction(status: 409, code: "transcription_final") == .done)
        #expect(R.reaction(status: 409, code: "not_hub") == .done)
        #expect(R.reaction(status: 409, code: "transcript_expired") == .done)
        #expect(R.reaction(status: 410, code: "recording_deleted") == .done)
        #expect(R.reaction(status: 413, code: "audio_full") == .done)
        #expect(R.reaction(status: 413, code: "too_large") == .smaller)
        #expect(R.reaction(status: 404, code: "recording_not_found") == .uploadFirst)
        #expect(R.reaction(status: 403, code: "kb_read_only") == .later)
        #expect(R.reaction(status: 403, code: "forbidden") == .done)
        #expect(R.reaction(status: 503, code: "assistant_offline") == .later)
        #expect(R.reaction(status: 400, code: "bad_audio") == .done)
    }

    private func ended(_ transcription: MeetingTranscriptionMode?) -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: .now, source: .phone, active: false, expectedSegments: 1)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = .room(.listener, private: false, photos: false, liveView: true, transcription: transcription)
        e.segments = [RecordingSegment(index: 0, start: 0, duration: 60, sentences: [])]
        return e
    }

    @Test func gapsAreUploadedWithinTheRetentionUntilTheHubIsDone() {
        let e = ended(.hub)
        typealias T = MeetingTranscription
        #expect(T.needsGaps(e, meeting: nil, expired: false, synced: true, done: false))
        #expect(!T.needsGaps(e, meeting: nil, expired: true, synced: true, done: false), "never past `days`")
        #expect(!T.needsGaps(e, meeting: nil, expired: false, synced: false, done: false), "after the complete upload")
        #expect(!T.needsGaps(e, meeting: nil, expired: false, synced: true, done: true))
        var final = MeetingTranscription.Meeting()
        final.hub = MeetingTranscriptionView(mode: .hub, state: "final")
        #expect(!T.needsGaps(e, meeting: final, expired: false, synced: true, done: false), "the hub is done with it")
        var device = MeetingTranscription.Meeting()
        device.decided = .init(mode: .device)
        #expect(!T.needsGaps(e, meeting: device, expired: false, synced: true, done: false))
        #expect(!T.needsGaps(ended(.device), meeting: nil, expired: false, synced: true, done: false))
        var running = e
        running.active = true
        #expect(!T.needsGaps(running, meeting: nil, expired: false, synced: true, done: false))
    }
}

// MARK: - The store

@MainActor struct MeetingTranscriptionStoreTests {
    @MainActor private final class Rig {
        let hub = FakeHub()
        let library = RecordingLibrary(root: FileManager.default.temporaryDirectory.appending(path: "meeting-transcription-\(UUID())"))
        var sockets: [FakeVoiceSocket] = []
        var network: MeetingNetworkKind = .unmetered
        var listening = false
        var reuploads: [UUID] = []
        var audio: (at: Double, data: Data)? = (0, Data(repeating: 0, count: 64000))
        let store = UserDefaults(suiteName: "transcription-store-\(UUID())")!
    }

    private func make(_ rig: Rig) -> MeetingTranscription {
        let client = rig.hub.client
        let deps = MeetingTranscription.Deps(
            api: { client },
            socket: { request, _ in
                let socket = FakeVoiceSocket(request)
                rig.sockets.append(socket)
                return socket
            },
            network: { rig.network },
            listen: { sink in rig.listening = sink != nil },
            reupload: { rig.reuploads.append($0) },
            // A retry's second or two pass at once; the 20 seconds after `end` never do while a test runs.
            wait: { duration in if duration > .seconds(5) { try? await Task.sleep(for: .seconds(600)) } },
            store: rig.store,
            audio: { _, _ in rig.audio })
        return MeetingTranscription(deps: deps, library: rig.library)
    }

    private func meeting(_ rig: Rig, mode: MeetingTranscriptionMode? = .hub, role: MeetingAttendance = .listener) throws -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: .now, source: .phone, title: "Weekly", active: true)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = .room(role, private: false, photos: false, liveView: true, transcription: mode)
        try rig.library.save(e)
        return e
    }

    private func until(_ what: String, _ done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async {
        for _ in 0..<300 {
            if done() { return }
            try? await Task.sleep(for: .milliseconds(10))
        }
        #expect(done(), "timed out waiting: \(what)", sourceLocation: sourceLocation)
    }

    @Test func aHubMeetingKeepsItsAudioUntilTheModeIsInThenOpensTheSocket() throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.follow(e.id, paused: false, entry: e)
        #expect(rig.listening, "the tap's audio is kept from the start")
        #expect(t.meeting(e.id).socket == .waitingForMode && rig.sockets.isEmpty)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        let socket = try #require(rig.sockets.last)
        #expect(socket.request.url?.absoluteString.hasSuffix("/api/v1/recordings/\(e.id.uuidString.lowercased())/audio") == true)
        #expect(socket.request.url?.scheme == "wss" && socket.request.value(forHTTPHeaderField: "Authorization") == "Bearer t")
        #expect(t.meeting(e.id).socket == .connecting && t.hubTranscribes(e.id))
        let shown = t.shown(e.id, role: .listener)
        #expect(!shown.fellBack && shown.phoneLines, "connecting: the phone's lines, no notice yet")
    }

    @Test func theHubsLinesAndProblemsReachTheStore() async throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        let socket = try #require(rig.sockets.last)
        socket.deliver(#"{"type":"ready","from":0,"gaps":[],"next":0}"#)
        #expect(t.meeting(e.id).socket == .ready)
        socket.deliver(#"{"type":"interim","start":0,"text":"我们先把"}"#)
        #expect(t.meeting(e.id).interim?.text == "我们先把")
        socket.deliver(#"{"type":"line","n":0,"start":0,"end":4,"text":"我们先把 export cap 保持在 4 MB。"}"#)
        #expect(t.meeting(e.id).lines.map(\.n) == [0] && t.meeting(e.id).interim == nil)
        #expect(t.shown(e.id, role: .listener) == .init(hubInterim: true, hubLines: true, phoneLines: false, fellBack: false))
        socket.deliver(#"{"type":"problem","code":"upstream_failed"}"#)
        #expect(t.shown(e.id, role: .listener) == .init(hubInterim: false, hubLines: false, phoneLines: true, fellBack: true))
        socket.deliver(#"{"type":"problem","code":null}"#)
        #expect(t.shown(e.id, role: .listener).hubLines)
        socket.deliver(#"{"type":"ack","at":5}"#)
        #expect(t.meeting(e.id).acked == 5)
    }

    @Test func linesMissedWhileAwayAreReadFromTheLiveLog() async throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        let path = "/api/v1/recordings/\(e.id.uuidString.lowercased())/live"
        rig.hub.on("GET", path) { req in
            #expect(req.query["parts"] == "lines" && req.query["from"] == "0")
            return Data(#"{"lines":[{"n":0,"start":0,"end":4,"text":"a","source":"hub"},{"n":1,"start":4,"end":8,"text":"b","source":"device"}],"more":false}"#.utf8)
        }
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        try #require(rig.sockets.last).deliver(#"{"type":"ready","from":8,"gaps":[[0,2]],"next":2}"#)
        await until("the missed lines") { t.meeting(e.id).lines.count == 2 }
        #expect(t.meeting(e.id).lines.map(\.source) == ["hub", "device"])
        #expect(t.meeting(e.id).gaps == [MeetingGap(from: 0, to: 2)])
    }

    @Test func anUnknownErrorStopsHubTranscriptionForTheMeeting() throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        let socket = try #require(rig.sockets.last)
        socket.deliver(#"{"type":"ready","from":0,"gaps":[],"next":0}"#)
        socket.deliver(#"{"type":"error","code":"quota_exceeded_somewhere","fatal":true}"#)
        #expect(t.meeting(e.id).stopped == "quota_exceeded_somewhere" && t.meeting(e.id).socket == .off)
        #expect(!rig.listening && socket.closed)
        let shown = t.shown(e.id, role: .listener)
        #expect(shown.phoneLines && shown.fellBack, "the phone's own lines, with the notice")
        t.follow(e.id, paused: false, entry: e)
        #expect(rig.sockets.count == 1, "no other socket for this meeting")
    }

    @Test func aDropIsTriedAgainAndANotFoundUploadsFirst() async throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        try #require(rig.sockets.last).deliver(#"{"type":"error","code":"recording_not_found","fatal":true}"#)
        #expect(rig.reuploads == [e.id])
        await until("another socket") { rig.sockets.count == 2 }
        rig.sockets.last?.drop()
        await until("a third socket") { rig.sockets.count == 3 }
        #expect(t.meeting(e.id).stopped == nil)
        rig.sockets.last?.drop(status: 403)
        #expect(t.meeting(e.id).stopped == "http_403")
    }

    @Test func anOlderManagerOpensNoSocketAndStopsARunningUplink() throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        #expect(rig.sockets.count == 1)
        t.uploaded(e, answer: .init(mode: nil))
        t.follow(e.id, paused: false, entry: e)
        #expect(t.meeting(e.id).socket == .off && !rig.listening && rig.sockets.last?.closed == true)
        #expect(!t.hubTranscribes(e.id) && t.meeting(e.id).decided?.olderManager == true)
    }

    @Test func aDeviceMeetingNeverListensOrOpensASocket() throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig, mode: .device)
        t.uploaded(e, answer: .init(mode: "device"))
        t.follow(e.id, paused: false, entry: e)
        #expect(!rig.listening && rig.sockets.isEmpty)
        let unavailable = try meeting(rig)
        t.uploaded(unavailable, answer: .init(mode: "device", problem: "unavailable"))
        t.follow(unavailable.id, paused: false, entry: unavailable)
        #expect(rig.sockets.isEmpty && !rig.listening && t.meeting(unavailable.id).decided?.problem == "unavailable")
    }

    @Test func wifiOnlyKeepsTheAudioOnThePhoneOnCellular() throws {
        let rig = Rig()
        rig.store.set(true, forKey: MeetingAttendanceDefaults.wifiOnlyKey)
        rig.network = .metered
        let t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        #expect(t.meeting(e.id).socket == .waitingForWiFi && rig.sockets.isEmpty && rig.listening)
        #expect(t.shown(e.id, role: .listener).fellBack, "the socket counts as down")
        rig.network = .unmetered
        t.follow(e.id, paused: false, entry: e)
        #expect(rig.sockets.count == 1 && t.meeting(e.id).socket == .connecting)
        t.wifiOnlyChanged(false)
        rig.network = .metered
        t.follow(e.id, paused: false, entry: e)
        #expect(rig.sockets.count == 1 && t.meeting(e.id).socket == .connecting, "cellular is allowed without the switch")
    }

    @Test func aPauseClosesTheSocketAndContinueOpensAnother() throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        try #require(rig.sockets.last).deliver(#"{"type":"ready","from":0,"gaps":[],"next":0}"#)
        t.follow(e.id, paused: true, entry: e)
        #expect(t.meeting(e.id).socket == .paused && rig.sockets.last?.closed == true && rig.listening)
        #expect(!t.shown(e.id, role: .listener).hubLines)
        t.follow(e.id, paused: false, entry: e)
        #expect(rig.sockets.count == 2 && t.meeting(e.id).socket == .connecting)
    }

    @Test func theRoleAndPersonasSpeakingGoOnTheSocket() throws {
        let rig = Rig(), t = make(rig)
        var e = try meeting(rig)
        t.speaking(e.id, voice: .answer, on: true, at: 1, text: "答案")
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        let socket = try #require(rig.sockets.last)
        t.speaking(e.id, voice: .call, on: true, at: 2, text: nil)
        t.speaking(e.id, voice: .answer, on: false, at: 3, text: nil)
        t.speaking(e.id, voice: .call, on: false, at: 4, text: nil)
        e.attendance?.set(.off, at: 5)
        t.follow(e.id, paused: false, entry: e)
        Thread.sleep(forTimeInterval: 0.05)
        let frames = socket.frames
        #expect(frames.map { $0["type"] as? String } == ["start", "speaking", "role", "speaking", "role"])
        #expect(frames[1]["on"] as? Bool == true && frames[1]["text"] as? String == "答案", "the open stretch right after start")
        #expect(frames[3]["on"] as? Bool == false && frames[3]["at"] as? Double == 4, "off once the last voice stops")
        #expect(frames[4]["attendance"] as? String == "off" && frames[4]["at"] as? Double == 5)
    }

    @Test func stopSendsEndAndTheLastLinesStillCome() async throws {
        let rig = Rig(), t = make(rig)
        let e = try meeting(rig)
        t.uploaded(e, answer: .init(mode: "hub"))
        t.follow(e.id, paused: false, entry: e)
        let socket = try #require(rig.sockets.last)
        socket.deliver(#"{"type":"ready","from":0,"gaps":[],"next":0}"#)
        t.ended(e.id)
        #expect(t.meeting(e.id).socket == .ending)
        t.follow(nil, paused: false, entry: nil)
        #expect(t.meeting(e.id).socket == .ending, "a socket at its end is left to finish")
        await until("the end frame") { socket.frames.last?["type"] as? String == "end" }
        socket.deliver(#"{"type":"line","n":3,"start":10,"end":12,"text":"Bo will share the notes tomorrow."}"#)
        #expect(t.meeting(e.id).lines.last?.n == 3)
        socket.drop()
        #expect(t.meeting(e.id).socket == .ended && !rig.listening)
    }

    @Test func theLiveReadsTranscriptionIsKept() throws {
        let rig = Rig(), t = make(rig)
        let id = UUID()
        t.liveRead(id, transcription: MeetingTranscriptionView(mode: .hub, state: "live", fallback: true), interim: nil, read: ["transcription"])
        #expect(t.meeting(id).hub?.fallback == true)
        t.liveRead(id, transcription: nil, interim: nil, read: ["topics"])
        #expect(t.meeting(id).hub != nil, "a read that didn't ask leaves it")
        t.liveRead(id, transcription: nil, interim: nil, read: ["transcription", "interim"])
        #expect(t.meeting(id).hub == nil)
    }

    @Test func gapsGoUpAfterTheMeetingUntilNoneIsLeft() async throws {
        let rig = Rig(), t = make(rig)
        var e = try meeting(rig)
        e.active = false
        e.expectedSegments = 1
        e.segments = [RecordingSegment(index: 0, start: 0, duration: 60, sentences: [])]
        try rig.library.save(e)
        let base = "/api/v1/recordings/\(e.id.uuidString.lowercased())"
        rig.hub.on("GET", base + "/live", json: #"{"transcription":{"mode":"hub","state":"finishing","gaps":[[0,2]]}}"#)
        rig.hub.on("PUT", base + "/audio") { req in
            #expect(req.query["at"] == "0" && req.body?.count == 64000)
            return Data(#"{"gaps":[]}"#.utf8)
        }
        t.uploaded(e, answer: .init(mode: "hub"))
        await until("the gap pass") { t.meeting(e.id).gapUpload == .done }
        #expect(rig.hub.sent("PUT", base + "/audio").count == 1 && t.meeting(e.id).gaps.isEmpty)
    }

    @Test func theFinalPassEndsTheGapUploads() async throws {
        let rig = Rig(), t = make(rig)
        var e = try meeting(rig)
        e.active = false
        e.expectedSegments = 1
        e.segments = [RecordingSegment(index: 0, start: 0, duration: 900, sentences: [])]
        try rig.library.save(e)
        let base = "/api/v1/recordings/\(e.id.uuidString.lowercased())"
        rig.hub.on("GET", base + "/live", json: #"{"transcription":{"mode":"hub","state":"finishing","gaps":[[0,900]]}}"#)
        rig.hub.on("PUT", base + "/audio") { _ in throw APIError(status: 409, message: "final", fields: ["code": "transcription_final"]) }
        t.uploaded(e, answer: .init(mode: "hub"))
        await until("the gap pass") { t.meeting(e.id).gapUpload == .done }
        #expect(rig.hub.sent("PUT", base + "/audio").count == 1, "no more after transcription_final")
    }

    @Test func wifiOnlyHoldsTheGapUploadsOnCellular() async throws {
        let rig = Rig()
        rig.store.set(true, forKey: MeetingAttendanceDefaults.wifiOnlyKey)
        rig.network = .metered
        let t = make(rig)
        var e = try meeting(rig)
        e.active = false
        e.expectedSegments = 1
        e.segments = [RecordingSegment(index: 0, start: 0, duration: 60, sentences: [])]
        try rig.library.save(e)
        t.uploaded(e, answer: .init(mode: "hub"))
        await until("the gap pass") { t.meeting(e.id).gapUpload == .waitingForWiFi }
        #expect(rig.hub.requests.isEmpty)
    }

    @Test func liveLinesDontGoInHubMode() async throws {
        let library = RecordingLibrary(root: FileManager.default.temporaryDirectory.appending(path: "meeting-live-hub-\(UUID())"))
        var e = RecordingEntry(id: UUID(), createdAt: .now, source: .phone, title: "Weekly", active: true)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = .room(.listener, private: false, photos: false, liveView: true, transcription: .hub)
        e.segments = [RecordingSegment(index: 0, start: 0, duration: 15, sentences: [RecordingSentence(start: 1, end: 5, text: "hello")])]
        try library.save(e)
        var posts = 0
        let live = MeetingLive(deps: MeetingLive.Deps(post: { _, from, _ in posts += 1; return MeetingLive.Answer(next: from) },
                                                      settled: { _ in true }, upload: { _ in }, openSpans: { _ in [] }, wait: { _ in },
                                                      hubTranscribes: { _ in true }),
                               library: library)
        live.append(e.id)
        await live.send(e.id)
        #expect(posts == 0, "POST …/live is 409 hub_transcribes in hub mode")
    }
}

// MARK: - Settings

@MainActor struct MeetingTranscriptionSettingsTests {
    private let assistant = #"{"machine_id":"m","launcher":"claude","cwd":"/","conversations":[],"meeting_transcription_default":"device","meeting_glossary":["Persona"],"meeting_transcription_extension":"","meeting_transcription_ready":true}"#

    private func model(_ hub: FakeHub, store: UserDefaults, wifi: @escaping (Bool) -> Void = { _ in }) -> MeetingTranscriptionSettings {
        let client = hub.client
        return MeetingTranscriptionSettings(deps: .init(api: { client }, store: store, wifiOnlyChanged: wifi))
    }

    @Test func theGlossaryIsCleanedAsTheHubCleansIt() throws {
        #expect(try MeetingTranscriptionSettings.cleanGlossary(["  Persona ", "persona", "", "export\ncap", "Alice"]).get()
                == ["Persona", "export cap", "Alice"])
        #expect(MeetingTranscriptionSettings.cleanGlossary([String(repeating: "x", count: 41)]) == .failure(.termTooLong(String(repeating: "x", count: 41))))
        #expect((try? MeetingTranscriptionSettings.cleanGlossary([String(repeating: "会", count: 40)]).get())?.count == 1)
        #expect(MeetingTranscriptionSettings.cleanGlossary((0..<201).map { "t\($0)" }) == .failure(.tooManyTerms))
        #expect((try? MeetingTranscriptionSettings.cleanGlossary((0..<200).map { "t\($0)" }).get())?.count == 200)
    }

    @Test func theExtensionChoiceShowsOnlyWithMoreThanTheBuiltInOne() throws {
        func ext(_ id: String, _ transcription: [String]?) throws -> HubExtension {
            let contributes = transcription.map { #"{"transcription":\#(String(decoding: try! JSONEncoder().encode($0), as: UTF8.self))}"# } ?? "{}"
            return try JSON.decoder.decode(HubExtension.self, from: Data(#"{"id":"\#(id)","name":"\#(id.capitalized)","kind":"x","status":"running","contributes":\#(contributes)}"#.utf8))
        }
        let builtIn = MeetingTranscriptionSettings.choices([try ext("voice", ["gpt-live"]), try ext("telegram", nil)])
        #expect(builtIn.map(\.ref) == [""] && !MeetingTranscriptionSettings.showsChoice(builtIn))
        let more = MeetingTranscriptionSettings.choices([try ext("voice", ["gpt-live"]), try ext("example-asr", ["fast", "fast"])])
        #expect(more.map(\.ref) == ["", "example-asr/fast"] && MeetingTranscriptionSettings.showsChoice(more))
    }

    @Test func loadAndSaveGoThroughTheAssistantSettings() async throws {
        let hub = FakeHub()
        let json = assistant
        hub.on("GET", "/api/v1/assistant", json: json)
        hub.on("PUT", "/api/v1/assistant") { req in
            let body = (try JSONSerialization.jsonObject(with: req.body ?? Data())) as? [String: Any] ?? [:]
            #expect(body.keys.sorted() == ["meeting_transcription_default"] && body["meeting_transcription_default"] as? String == "hub")
            return Data(json.replacingOccurrences(of: #""meeting_transcription_default":"device""#, with: #""meeting_transcription_default":"hub""#).utf8)
        }
        let store = UserDefaults(suiteName: "transcription-settings-\(UUID())")!
        let settings = model(hub, store: store)
        await settings.load()
        #expect(settings.offered && settings.values == .init(defaultMode: .device, glossary: ["Persona"], extensionRef: "", ready: true))
        await settings.setDefault(.hub)
        #expect(settings.values?.defaultMode == .hub && settings.failure == nil)
        #expect(store.string(forKey: MeetingAttendanceDefaults.transcriptionDefaultKey) == "hub", "kept for a one-tap start")
    }

    @Test func aRefusedChangeGoesBack() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: assistant)
        hub.on("PUT", "/api/v1/assistant") { _ in throw APIError(status: 400, message: "bad", fields: ["code": "bad_glossary"]) }
        let settings = model(hub, store: UserDefaults(suiteName: "transcription-settings-\(UUID())")!)
        await settings.load()
        await settings.setGlossary(["Persona", "Bo"])
        #expect(settings.values?.glossary == ["Persona"])
        if case .hub? = settings.failure {} else { Issue.record("no failure: \(String(describing: settings.failure))") }
        await settings.setGlossary([String(repeating: "x", count: 41)])
        #expect(settings.failure == .termTooLong(String(repeating: "x", count: 41)) && hub.sent("PUT", "/api/v1/assistant").count == 1,
                "refused here, never sent")
    }

    @Test func aHubWithoutHubTranscriptionOffersNothing() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: #"{"machine_id":"m","launcher":"claude","cwd":"/","conversations":[]}"#)
        let settings = model(hub, store: UserDefaults(suiteName: "transcription-settings-\(UUID())")!)
        await settings.load()
        #expect(!settings.offered && settings.values == nil)
    }

    @Test func wifiOnlyIsKeptOnThePhoneOffByDefault() {
        let store = UserDefaults(suiteName: "transcription-settings-\(UUID())")!
        var told: [Bool] = []
        let settings = model(FakeHub(), store: store) { told.append($0) }
        #expect(!settings.wifiOnly)
        settings.setWiFiOnly(true)
        #expect(settings.wifiOnly && store.bool(forKey: MeetingAttendanceDefaults.wifiOnlyKey) && told == [true])
        #expect(model(FakeHub(), store: store).wifiOnly)
    }
}

// MARK: - What the phone shows and says

/// What the start, the settings, the meeting page, the Live Activity and the recording's page say about hub
/// transcription (#3862 phase 5): the privacy texts per mode (the contract's Wording, the extension named when it isn't
/// the built-in one), the fallback notice, the recording page's facts, the Lock Screen's line, `detail.meta.transcription`
/// and the speech service's choices. Tests run in zh-Hans, so texts are compared with each other, not with English.
@MainActor struct MeetingTranscriptionShownTests {
    private typealias Words = MeetingTranscriptionWords
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    @Test func theLineUnderRecordSaysWhatEachModeSends() {
        let hub = Words.startPrivacy(.hub, service: nil, talk: true, days: 14)
        let named = Words.startPrivacy(.hub, service: "Example ASR", talk: true, days: 14)
        let device = Words.startPrivacy(.device, service: "Example ASR", talk: true, days: 14)
        let quiet = Words.startPrivacy(.device, service: nil, talk: false, days: 14)
        #expect(named.contains("Example ASR") && !hub.contains("Example ASR"), "the extension named only when the owner chose it")
        #expect(Set([hub, named, device, quiet]).count == 4)
        #expect(!device.contains("Example ASR"), "privacy mode sends no meeting audio, whatever extension is chosen")
        #expect(Words.startPrivacy(nil, service: nil, talk: true, days: 14) == device, "a hub without the flag: as before")
        #expect(Words.startPrivacy(.hub, service: nil, talk: false, days: 14) == hub, "hub mode's words say Talk included")
        #expect(hub.contains("14") && quiet.contains("14"), "how long the text is kept")
        #expect(Words.modePrivacy(.hub, service: "Example ASR").contains("Example ASR"))
        #expect(Words.modePrivacy(.device, service: "Example ASR") == Words.modePrivacy(.device, service: nil))
        #expect(Words.title(.device) != Words.title(.hub))
    }

    @Test func thePagesTalkLineFollowsTheMode() {
        let device = Words.pagePrivacy(hub: false, service: "Example ASR")
        #expect(device == Words.modePrivacy(.device, service: nil), "privacy mode: phase 4's Wording, as before")
        #expect(Words.pagePrivacy(hub: true, service: "Example ASR").contains("Example ASR"))
        #expect(Set([device, Words.pagePrivacy(hub: true, service: nil), Words.pagePrivacy(hub: true, service: "Example ASR")]).count == 3)
    }

    @Test func theFallbackNoticeSaysWhy() {
        var m = MeetingTranscription.Meeting()
        m.decided = .init(mode: .hub)
        m.socket = .waitingForWiFi
        let wifi = Words.fallback(m)
        m.socket = .ready
        m.stopped = "replaced"
        let stopped = Words.fallback(m)
        m.stopped = nil
        m.problem = "upstream_failed"
        let problem = Words.fallback(m)
        #expect(Set([wifi, stopped, problem]).count == 3)
    }

    @Test func theRecordingPageSaysTheModeTheStateAndTheGaps() throws {
        #expect(Words.facts(nil, asked: .hub, gapUpload: .idle) == nil, "no mode decided: nothing")
        #expect(Words.facts(.init(mode: .device), asked: nil, gapUpload: .idle) == nil, "a meeting on the phone as always")
        let refused = try #require(Words.facts(.init(mode: .device), asked: .hub, gapUpload: .idle))
        #expect(refused.state != nil && !refused.busy, "asked for the hub and kept on the phone: said why")

        let finishing = try #require(Words.facts(.init(mode: .hub, state: "finishing"), asked: .hub, gapUpload: .uploading))
        #expect(finishing.busy && finishing.gaps == nil && finishing.upload != nil, "being finished, the missing audio going up")
        let live = try #require(Words.facts(.init(mode: .hub, state: "live"), asked: .hub, gapUpload: .waitingForWiFi))
        #expect(live.busy && live.state == finishing.state && live.upload != finishing.upload)
        let final = try #require(Words.facts(.init(mode: .hub, state: "final", gaps: [.init(from: 600, to: 642.5)]), asked: .hub,
                                             gapUpload: .uploading))
        #expect(!final.busy && final.state != finishing.state && final.upload == nil, "final: no more uploads said")
        #expect(final.gaps?.contains("10:00") == true && final.gaps?.contains("10:42") == true)
        let failed = try #require(Words.facts(.init(mode: .hub, state: "failed"), asked: .hub, gapUpload: .idle))
        #expect(!failed.busy && Set([failed.state, final.state, finishing.state]).count == 3)
        let unknown = try #require(Words.facts(.init(mode: .hub, state: "someday"), asked: .hub, gapUpload: .idle))
        #expect(unknown.busy, "a state the app doesn't know reads as still being finished")

        let many = (0..<5).map { MeetingGap(from: Double($0 * 100), to: Double($0 * 100 + 10)) }
        let spans = Words.spans(many)
        #expect(spans.contains("0:00") && spans.contains("3:30") && !spans.contains("6:40") && spans.contains("2"),
                "the first three, then how many more")
        #expect(Words.spans(Array(many.prefix(1))) == "0:00–0:10")
    }

    @Test func theDetailReadsTranscriptionFromItsMeta() throws {
        let answer = try JSON.decoder.decode(RecordingMinutesModel.Answer.self, from: Data(#"""
        {"detail": {"meta": {"minutes_state": "pending", "transcription": {"mode": "hub", "state": "finishing", "gaps": [[600, 642.5]], "fallback": true}}}}
        """#.utf8))
        let view = try #require(answer.detail.meta?.transcription)
        #expect(view.mode == .hub && view.known == .finishing && view.gaps == [MeetingGap(from: 600, to: 642.5)] && view.fallback)
        #expect(answer.detail.meta?.state == "pending")
        let odd = try JSON.decoder.decode(RecordingMinutesModel.Answer.self, from: Data(#"""
        {"detail": {"meta": {"minutes_state": "ready", "transcription": "hub"}}}
        """#.utf8))
        #expect(odd.detail.meta?.transcription == nil && odd.detail.meta?.state == "ready", "read leniently, the rest stays")
    }

    // MARK: The Live Activity

    private func meeting(lines: [String]) -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: now, source: .phone, title: "Weekly", locale: "zh-CN", active: true)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = RecordingAttendance.room(.listener, private: false, photos: false, liveView: true, transcription: .hub)
        for (n, text) in lines.enumerated() {
            e.attendance?.live.append(RecordingSentence(start: Double(n * 10), end: Double(n * 10 + 8), text: text))
        }
        return e
    }

    private func content(_ e: RecordingEntry, _ transcribed: RecordingActivity.Transcribed?, answer: String? = nil) -> RecordingActivity.Content {
        RecordingActivity.content(entry: e, recording: true, interrupted: false, elapsed: 100, previous: nil, now: now,
                                  answer: answer, answeredAt: answer == nil ? nil : now, transcribed: transcribed)
    }

    @Test func theLockScreenShowsTheHubsLineThenThePhonesOwnSaidSo() throws {
        let e = meeting(lines: ["手机自己的一句"])
        let healthy = MeetingTranscriptionRule.shown(hub: true, socketUp: true, everUp: true, down: false, problem: nil, stopped: false, role: .listener)
        let fell = MeetingTranscriptionRule.shown(hub: true, socketUp: true, everUp: true, down: false, problem: "upstream_failed", stopped: false, role: .listener)
        let up = content(e, .init(hubLine: "Bo will share the notes tomorrow.", shown: healthy))
        #expect(up.line == "Bo will share the notes tomorrow." && up.fellBack == nil)
        let back = content(e, .init(hubLine: "Bo will share the notes tomorrow.", shown: fell))
        #expect(back.line == "手机自己的一句" && back.fellBack == true, "the phone's own line, said so")
        #expect(content(e, nil).line == "手机自己的一句" && content(e, nil).fellBack == nil, "device mode: as before")
        let answered = content(e, .init(hubLine: nil, shown: fell), answer: "下周再定")
        #expect(answered.answer == "下周再定" && answered.line == nil && answered.fellBack == nil, "only beside a line")
        let empty = content(meeting(lines: []), .init(hubLine: nil, shown: fell))
        #expect(empty.line == nil && empty.fellBack == nil)
        // A state from before reads as it was; the new field keeps it small.
        let old = try JSON.decoder.decode(RecordingActivity.Content.self, from: JSON.encoder.encode(content(e, nil)))
        #expect(old.fellBack == nil)
        #expect(try JSON.encoder.encode(back).count < 1500)
    }

    // MARK: The speech service

    @Test func theSpeechServiceIsNamedOnlyWhenTheOwnerChoseAnother() throws {
        func ext(_ id: String, _ name: String, _ transcription: [String]?) throws -> HubExtension {
            let contributes = transcription.map { #"{"transcription":["\#($0.joined(separator: #"",""#))"]}"# } ?? "{}"
            return try JSON.decoder.decode(HubExtension.self, from: Data(#"{"id":"\#(id)","name":"\#(name)","kind":"x","status":"running","contributes":\#(contributes)}"#.utf8))
        }
        let choices = MeetingTranscriptionSettings.choices([try ext("voice", "Voice", ["gpt-live"]), try ext("example-asr", "Example ASR", ["fast", "accurate"])])
        #expect(choices.map(\.ref) == ["", "example-asr/fast", "example-asr/accurate"])
        #expect(MeetingTranscriptionSettings.service("", choices: choices) == nil)
        #expect(MeetingTranscriptionSettings.service("example-asr/fast", choices: choices) == "Example ASR")
        #expect(MeetingTranscriptionSettings.service("gone/x", choices: choices) == "gone", "one the hub doesn't list goes by its id")
        #expect(MeetingTranscriptionSettings.label(choices[1], in: choices).contains("fast"), "several from one extension: the contribution too")
        #expect(MeetingTranscriptionSettings.label(choices[0], in: choices) != MeetingTranscriptionSettings.label(choices[1], in: choices))
    }

    @Test func theSettingsReadTheExtensionsForTheChoice() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: #"{"machine_id":"m","launcher":"claude","cwd":"/","conversations":[],"meeting_transcription_default":"hub","meeting_glossary":[],"meeting_transcription_extension":"example-asr/fast","meeting_transcription_ready":true}"#)
        let client = hub.client
        let list = [try JSON.decoder.decode(HubExtension.self, from: Data(#"{"id":"example-asr","name":"Example ASR","kind":"x","status":"running","contributes":{"transcription":["fast"]}}"#.utf8))]
        let settings = MeetingTranscriptionSettings(deps: .init(api: { client }, store: UserDefaults(suiteName: "transcription-shown-\(UUID())")!,
                                                                plugins: { _ in list }))
        #expect(!MeetingTranscriptionSettings.showsChoice(settings.choices), "only the built-in one before the list is read")
        await settings.load()
        #expect(MeetingTranscriptionSettings.showsChoice(settings.choices) && settings.service == "Example ASR")
        let failing = MeetingTranscriptionSettings(deps: .init(api: { client }, store: UserDefaults(suiteName: "transcription-shown-\(UUID())")!,
                                                               plugins: { _ in throw APIError(status: 503, message: "down") }))
        await failing.load()
        #expect(failing.offered && !MeetingTranscriptionSettings.showsChoice(failing.choices), "a list that can't be read hides the choice")
        #expect(failing.service == "example-asr", "and the privacy text still names it")
    }
}
