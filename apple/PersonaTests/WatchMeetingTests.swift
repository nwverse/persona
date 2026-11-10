import Foundation
import Testing
@testable import Persona

/// Meeting recordings on the Apple Watch (docs/apple/recording.md「Apple Watch」, #2720): the meeting mark crosses to the
/// phone with the recording and goes up with its upload; the minutes come back to the watch short.
@MainActor struct WatchMeetingTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func library() -> RecordingLibrary {
        RecordingLibrary(root: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
    }

    /// Started as a meeting on the watch: the mark rides with every segment and the end message, the phone keeps it, and
    /// the upload says `kind: meeting`.
    @Test func meetingMarkReachesThePhoneAndItsUpload() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        var watch = RecordingEntry(id: UUID(), createdAt: now, source: .watch)
        watch.meeting = RecordingMeeting()
        let segment = RecordingTransfer(entry: watch, segment: RecordingSegment(index: 0, start: 0, duration: 60))
        #expect(segment.meeting == true)
        // Through WatchConnectivity's dictionary and back, as the phone receives it.
        let received = try #require(RecordingTransfer(dictionary: segment.dictionary))
        try lib.receive(received)
        var phone = try #require(lib.entry(watch.id))
        #expect(phone.isMeeting)
        #expect(phone.source == .watch)
        phone.segments[0].sentences = [RecordingSentence(start: 1, end: 3, text: "开会")]
        phone.active = false
        phone.expectedSegments = 1
        let upload = RecordingUpload(phone)
        #expect(upload.kind == "meeting")
        #expect(upload.device == "watch")
        #expect(upload.calendar == nil) // no event matched: a meeting all the same
    }

    /// A note from the watch stays a note; a watch with an older app sends no mark at all, and the phone reads it.
    @Test func noteAndOlderWatchStayNotes() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let note = RecordingEntry(id: UUID(), createdAt: now, source: .watch)
        let transfer = RecordingTransfer(entry: note, segment: RecordingSegment(index: 0, start: 0, duration: 30))
        #expect(transfer.meeting == nil)
        // The older watch's JSON: no "meeting" key.
        var json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(transfer)) as! [String: Any]
        json.removeValue(forKey: "meeting")
        let old = try JSONDecoder().decode(RecordingTransfer.self, from: JSONSerialization.data(withJSONObject: json))
        try lib.receive(old)
        #expect(lib.entry(note.id)?.isMeeting == false)
    }

    /// A later segment or the end message never takes a meeting's mark away or renames it.
    @Test func laterMessagesKeepTheMeeting() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        var watch = RecordingEntry(id: UUID(), createdAt: now, source: .watch)
        watch.meeting = RecordingMeeting()
        try lib.receive(RecordingTransfer(entry: watch, segment: RecordingSegment(index: 0, start: 0, duration: 60)))
        lib.change(watch.id) { $0.title = "Standup"; $0.meeting = RecordingMeeting(eventID: "e1", title: "Standup") }
        watch.expectedSegments = 1
        try lib.receive(RecordingTransfer(entry: watch)) // the end message
        #expect(lib.entry(watch.id)?.meeting?.eventID == "e1")
        #expect(lib.entry(watch.id)?.title == "Standup")
    }

    // MARK: Minutes on the watch

    private let minutes = RecordingMinutes(
        title: "Design review", when: "2026-11-10 14:00–15:10", attendees: ["Ada", "Bo"],
        points: (1...6).map { .init(text: "Point \($0)", at: Double($0) * 60) },
        decisions: [.init(text: String(repeating: "长", count: 300), at: 10)],
        actions: [.init(text: "Add the regression test.", at: 2100, who: "Bo", by: "Friday"), .init(text: "Ship it")],
        openQuestions: [.init(text: "Per entry or per recording?")])

    /// A few of each, cut to fit a wrist: points, decisions and action items (who, what, by when); open questions stay
    /// on the phone.
    @Test func shortMinutesForTheWrist() {
        let m = WatchMinutes.meeting(id: UUID(), title: "Recording · today", date: now, minutes: minutes)
        #expect(m.state == .ready)
        #expect(m.title == "Design review")
        #expect(m.points.count == WatchMinutes.maxItems)
        #expect(m.decisions.first?.count == WatchMinutes.maxChars)
        #expect(m.decisions.first?.hasSuffix("…") == true)
        #expect(m.actions == ["Bo: Add the regression test. · Friday", "Ship it"])
        let writing = WatchMinutes.meeting(id: UUID(), title: "Weekly", date: now, minutes: nil)
        #expect(writing.state == .writing)
        #expect(writing.title == "Weekly")
        #expect(WatchMinutes.meeting(id: UUID(), title: "Weekly", date: now, minutes: nil, failed: true).state == .failed)
    }

    /// The newest meetings first, a bounded few, small enough for the application context; read back as sent.
    @Test func relayStaysSmallAndRoundTrips() throws {
        let many = (0..<20).map { i in
            WatchMinutes.meeting(id: UUID(), title: "Meeting \(i)", date: now.addingTimeInterval(Double(i) * 3600), minutes: minutes)
        }
        let relay = WatchMinutes(meetings: many)
        #expect(relay.meetings.count == WatchMinutes.maxMeetings)
        #expect(relay.meetings.first?.title == "Design review")
        #expect(relay.meetings.first?.date == now.addingTimeInterval(19 * 3600))
        let data = try #require(relay.data)
        #expect(data.count < 16_000)
        #expect(WatchMinutes(data: data) == relay)
    }

    /// The phone's view of one recording for the watch: written, being written, failed; ready with nothing to note
    /// still counts as ready.
    @Test func relayStateFromTheHub() {
        let entry = RecordingEntry(id: UUID(), createdAt: now, source: .phone, title: "Weekly")
        #expect(MinutesRelay.short(entry, minutes: minutes, status: "ready").state == .ready)
        #expect(MinutesRelay.short(entry, minutes: nil, status: "pending").state == .writing)
        #expect(MinutesRelay.short(entry, minutes: nil, status: "failed").state == .failed)
        #expect(MinutesRelay.short(entry, minutes: RecordingMinutes(), status: "ready").state == .ready)
    }
}
