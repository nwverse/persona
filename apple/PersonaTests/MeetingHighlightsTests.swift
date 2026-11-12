import Foundation
import Testing
@testable import Persona

/// The highlights on the phone (#3862 phase 2b; contract docs/assistant/meeting-minutes.md「Phase 2b: highlights」 and
/// 「The meeting card」, the phone's side docs/apple/recording.md「Highlights (phase 2b)」): the flag kept only beside the
/// conversation and the live view and snapshotted at the start, the lenient decoding, the merge by what a read asked for
/// with the cap and the order, the parts read (never by the background topics read), the card's mode, its newest
/// highlight and rows, and which transcript lines a highlight's time lights. The hub is a `FakeHub`; nothing reaches a
/// network.
@MainActor struct MeetingHighlightsTests {
    // MARK: Fixtures

    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func meeting(_ attendance: RecordingAttendance?) -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: now, source: .phone, title: "Weekly", locale: "zh-CN", active: true)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = attendance
        return e
    }

    private func room(_ role: MeetingAttendance = .participant, liveView: Bool = true, highlights: Bool = true) -> RecordingAttendance {
        RecordingAttendance.room(role, private: false, photos: false, liveView: liveView, highlights: highlights)
    }

    private func path(_ id: UUID, _ route: String) -> String { "/api/v1/recordings/\(id.uuidString.lowercased())/\(route)" }

    private func until(_ what: String, _ done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async {
        for _ in 0..<500 {
            if done() { return }
            try? await Task.sleep(for: .milliseconds(10))
        }
        #expect(done(), "timed out waiting: \(what)", sourceLocation: sourceLocation)
    }

    private func highlight(_ text: String, _ at: Double, _ end: Double? = nil) -> MeetingHighlight {
        MeetingHighlight(text: text, at: at, end: end)
    }

    // MARK: The flag

    /// `meeting_highlights` is kept from every hello only beside the conversation and the live view, and a start
    /// snapshots it on the recording for life: a meeting keeps what it started with, and one without the live view never
    /// has highlights.
    @Test func theFlagIsKeptOnlyBesideTheLiveViewAndSnapshotted() throws {
        let store = UserDefaults(suiteName: "meeting-highlights-\(UUID())")!
        #expect(MeetingAttendanceDefaults.highlightsFeature == "meeting_highlights")
        let all: Set<String> = ["meeting_attendance", "meeting_conversation", "meeting_live_view", "meeting_highlights"]
        MeetingAttendanceDefaults.remember(hello: all, role: nil, store)
        #expect(store.bool(forKey: MeetingAttendanceDefaults.highlightsKey))
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation", "meeting_highlights"], role: nil, store)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.highlightsKey), "only with meeting_live_view")
        MeetingAttendanceDefaults.remember(hello: all, role: nil, store)
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_live_view", "meeting_highlights"], role: nil, store)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.highlightsKey), "only with meeting_conversation")
        MeetingAttendanceDefaults.remember(hello: all, role: nil, store)
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation", "meeting_live_view"], role: nil, store)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.highlightsKey))
        #expect(store.bool(forKey: MeetingAttendanceDefaults.liveViewKey), "the live view stays")

        let start = RecordingStart.make(.meeting, offered: true, conversation: true, photos: false, liveView: true, highlights: true,
                                        role: .listener, privateMark: false, attendee: nil)
        guard case .meeting(let attendance?) = start else { Issue.record("no room meeting: \(start)"); return }
        #expect(attendance.room && attendance.liveView && attendance.highlights)
        let noLiveView = RecordingStart.make(.meeting, offered: true, conversation: true, photos: false, liveView: false, highlights: true,
                                             role: .listener, privateMark: false, attendee: nil)
        if case .meeting(let a) = noLiveView { #expect(a?.highlights == false, "highlights come only with the live view") }
        let old = RecordingStart.make(.meeting, offered: true, conversation: false, photos: false, liveView: true, highlights: true,
                                      role: .listener, privateMark: false, attendee: nil)
        if case .meeting(let a) = old { #expect(a?.highlights != true, "a #3746 meeting has no highlights") }
        #expect(!RecordingAttendance.room(.participant, private: false, photos: false, liveView: false, highlights: true).highlights)

        // Kept with the recording, and read back; a file from before has none, and a value of another shape is none.
        let data = try JSON.encoder.encode(room())
        let back = try JSON.decoder.decode(RecordingAttendance.self, from: data)
        #expect(back.highlights && back.liveView)
        let before = try JSON.decoder.decode(RecordingAttendance.self, from: Data(#"{"role":"listener","room":true,"liveView":true}"#.utf8))
        #expect(!before.highlights && before.liveView)
        let odd = try JSON.decoder.decode(RecordingAttendance.self, from: Data(#"{"role":"listener","room":true,"highlights":"yes"}"#.utf8))
        #expect(!odd.highlights)
    }

    /// A one-tap start (the drawer, Siri, the Control) reads the last hello's flags: highlights only with all four, so an
    /// always-on and an always-off snapshot both fail here. No hub in this model, so the remembered flags decide; the user
    /// defaults it reads are put back after.
    @Test func aStartTakesTheHighlightsOnlyBesideTheLiveView() {
        let store = UserDefaults.standard
        let keys = [MeetingAttendanceDefaults.offeredKey, MeetingAttendanceDefaults.roleKey, MeetingAttendanceDefaults.conversationKey,
                    MeetingAttendanceDefaults.photosKey, MeetingAttendanceDefaults.liveViewKey, MeetingAttendanceDefaults.highlightsKey]
        let before = keys.map { store.object(forKey: $0) }
        defer { for (key, value) in zip(keys, before) { store.set(value, forKey: key) } }
        let model = AppModel()
        guard model.hubFeatures.isEmpty else { return } // a hello already in: the flags are the hub's, not this case's
        let all: Set<String> = ["meeting_attendance", "meeting_conversation", "meeting_live_view", "meeting_highlights"]
        MeetingAttendanceDefaults.remember(hello: all, role: .listener, store)
        let with = MeetingAttendees.startAttendance(model)
        #expect(with.room && with.liveView && with.highlights, "all four flags: a room meeting with the highlights")
        #expect(MeetingAttendees.highlights(model))

        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation", "meeting_live_view"], role: .listener, store)
        let without = MeetingAttendees.startAttendance(model)
        #expect(without.liveView && !without.highlights, "no meeting_highlights: the live view alone")
        #expect(!MeetingAttendees.highlights(model))

        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation", "meeting_highlights"], role: .listener, store)
        #expect(!MeetingAttendees.startAttendance(model).highlights, "highlights come only with the live view")

        MeetingAttendanceDefaults.remember(hello: all, role: .listener, store)
        #expect(MeetingAttendees.startAttendance(model).highlights, "back on with the next hello that lists them")
    }

    // MARK: Decoding

    /// `highlights` reads leniently: words are trimmed and a highlight without words, or without a time it can go back
    /// to, leaves the list (not the others); an `end` that is missing, not a number or before `at` is `at`;
    /// `highlights_at` comes with them, and the rest of the answer still reads.
    @Test func highlightsDecodeLeniently() throws {
        let text = #"""
        {"room":{"state":"ready"},"topics":[{"title":"Export cap","state":"done"}],"highlights":[
          {"text":"  Keep the export cap for this release. ","at":312,"end":358.4},
          {"text":"The vendor stays through Q3.","at":1120.5},
          {"text":"Open: who owns the hiring plan?","at":1385,"end":1300},
          {"text":"Pricing by seat","at":1500,"end":"later"},
          {"text":"   ","at":20},
          {"text":"No time"},
          {"text":"Bad time","at":-4},
          {"text":"Text time","at":"12:30"},
          {"at":40},
          7
        ],"highlights_at":"2026-11-13T06:23:10Z","revs":{"highlights":3}}
        """#
        let read = try JSON.decoder.decode(MeetingConversation.LiveRead.self, from: Data(text.utf8))
        let highlights = try #require(read.highlights)
        #expect(highlights.map(\.text) == ["Keep the export cap for this release.", "The vendor stays through Q3.",
                                           "Open: who owns the hiring plan?", "Pricing by seat"])
        #expect(highlights.map(\.at) == [312, 1120.5, 1385, 1500])
        #expect(highlights.map(\.end) == [358.4, 1120.5, 1385, 1500], "a missing, odd or earlier end is the start")
        #expect(read.highlightsAt == "2026-11-13T06:23:10Z")
        #expect(read.room?.known == .ready && read.topics?.count == 1, "the rest of the answer still reads")
        #expect(read.revs?["highlights"] == 3)

        let none = try JSON.decoder.decode(MeetingConversation.LiveRead.self, from: Data(#"{"highlights":{"text":"x"}}"#.utf8))
        #expect(none.highlights == nil && none.highlightsAt == nil)
    }

    // MARK: Merging

    /// A read that asks for highlights (or reads everything) takes the hub's list, an absent one meaning none; a read of
    /// other parts keeps them; a meeting started without them never holds any. At most four, earliest first, those of the
    /// same time in the order given.
    @Test func highlightsMergeByWhatWasAsked() {
        let a = highlight("A", 300), b = highlight("B", 60), c = highlight("C", 60, 90), d = highlight("D", 900)
        var m = MeetingConversation.Meeting()
        MeetingConversation.applyHighlights(&m, highlights: [a, b, c], at: "t1", replace: true, shown: true)
        #expect(m.highlights.map(\.text) == ["B", "C", "A"], "earliest first, ties in the order given")
        #expect(m.highlightsAt == "t1")
        MeetingConversation.applyHighlights(&m, highlights: nil, at: nil, replace: false, shown: true)
        #expect(m.highlights.map(\.text) == ["B", "C", "A"], "a read of other parts keeps them")
        MeetingConversation.applyHighlights(&m, highlights: [d], at: "t2", replace: false, shown: true)
        #expect(m.highlights == [d] && m.highlightsAt == "t2", "present without being asked for: the hub's word")
        MeetingConversation.applyHighlights(&m, highlights: nil, at: nil, replace: true, shown: true)
        #expect(m.highlights.isEmpty && m.highlightsAt == nil, "asked for and absent: none")
        MeetingConversation.applyHighlights(&m, highlights: [], at: "t3", replace: true, shown: true)
        #expect(m.highlights.isEmpty && m.highlightsAt == "t3", "cleared by the room")

        let many = (0..<7).map { highlight("H\($0)", Double(700 - $0 * 100)) }
        MeetingConversation.applyHighlights(&m, highlights: many, at: "t4", replace: true, shown: true)
        #expect(MeetingConversation.maxHighlights == 4)
        #expect(m.highlights.map(\.text) == ["H6", "H5", "H4", "H3"], "the earliest four")
        MeetingConversation.applyHighlights(&m, highlights: [a], at: "t5", replace: true, shown: false)
        #expect(m.highlights.isEmpty && m.highlightsAt == nil, "no highlights flag: none")
        MeetingConversation.applyHighlights(&m, highlights: nil, at: nil, replace: false, shown: false)
        #expect(m.highlights.isEmpty)
    }

    // MARK: Parts

    /// A meeting started with the highlights follows `highlights` in `meeting_live` and reads them with
    /// `?parts=highlights`; one started without them reads none. The background read after an accepted batch stays
    /// `?parts=topics` alone: the Live Activity never shows highlights.
    @Test func aHighlightsMeetingReadsThemOnTheirEvent() async throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "meeting-highlights-\(UUID())")
        defer { try? FileManager.default.removeItem(at: root) }
        let lib = RecordingLibrary(root: root)
        let made = meeting(room()), plain = meeting(room(highlights: false))
        try lib.save(made)
        try lib.save(plain)
        let hub = FakeHub()
        let full = #"{"room":{"state":"ready","machine_id":"server","session_id":"room-1"},"asks":[],"topics":[],"highlights":[{"text":"Keep the cap.","at":312,"end":330}],"highlights_at":"2026-11-13T06:00:00Z","revs":{"room":2,"topics":1,"highlights":1}}"#
        for id in [made.id, plain.id] {
            hub.on("GET", path(id, "live")) { req in
                switch req.query["parts"] {
                case "highlights":
                    return Data(#"{"highlights":[{"text":"The vendor stays through Q3.","at":1120,"end":1171},{"text":"Keep the cap.","at":312,"end":330}],"highlights_at":"2026-11-13T06:05:00Z","revs":{"highlights":2}}"#.utf8)
                case "topics":
                    return Data(#"{"topics":[{"title":"Pricing","state":"current"}],"revs":{"topics":2}}"#.utf8)
                default:
                    return Data(full.utf8)
                }
            }
        }
        let client = hub.client
        let c = MeetingConversation(deps: MeetingConversation.Deps(api: { client }, group: { "" }, now: { .now }, wait: { _ in },
                                                                  readPhotos: { _ in true }),
                                    library: lib, sync: RecordingSync(library: lib, api: { nil }))
        #expect(c.partsRead(for: made.id).contains("highlights") && c.partsRead(for: made.id).contains("topics"))
        #expect(!c.partsRead(for: plain.id).contains("highlights"))
        #expect(c.partsRead(for: plain.id).contains("topics"), "the live view stays as phase 2 has it")

        let owner = UUID()
        c.watch(made.id, by: owner)
        await until("the first read") { c.meeting(made.id).loaded }
        #expect(c.meeting(made.id).highlights.map(\.text) == ["Keep the cap."])
        #expect(c.meeting(made.id).revs["highlights"] == 1)

        c.event(recordingID: made.id.uuidString.lowercased(), changed: ["highlights"], revs: ["highlights": 2])
        await until("the highlights read") { c.meeting(made.id).highlights.count == 2 }
        #expect(hub.sent("GET", path(made.id, "live")).last?.query["parts"] == "highlights")
        #expect(c.meeting(made.id).highlights.map(\.at) == [312, 1120], "earliest first")
        #expect(c.meeting(made.id).highlightsAt == "2026-11-13T06:05:00Z")
        #expect(c.meeting(made.id).room?.known == .ready, "the room stays as read")
        let reads = hub.sent("GET", path(made.id, "live")).count
        c.event(recordingID: made.id.uuidString.lowercased(), changed: ["highlights"], revs: ["highlights": 2])
        try await Task.sleep(for: .milliseconds(100))
        #expect(hub.sent("GET", path(made.id, "live")).count == reads, "a revision already read reads nothing")

        c.watch(plain.id, by: owner)
        await until("the plain meeting's read") { c.meeting(plain.id).loaded }
        #expect(c.meeting(plain.id).highlights.isEmpty, "started without the highlights: none, whatever the hub sends")
        c.event(recordingID: plain.id.uuidString.lowercased(), changed: ["highlights"], revs: ["highlights": 2])
        try await Task.sleep(for: .milliseconds(100))
        #expect(hub.sent("GET", path(plain.id, "live")).count == 1, "not followed")

        // In the background: after an accepted batch, only the topics.
        let behind = MeetingConversation(deps: MeetingConversation.Deps(api: { client }, group: { "" }, now: { .now }, wait: { _ in },
                                                                       readPhotos: { _ in true }, streaming: { false }),
                                         library: lib, sync: RecordingSync(library: lib, api: { nil }))
        let before = hub.sent("GET", path(made.id, "live")).count
        behind.liveAccepted(made.id)
        await until("the topics read") { hub.sent("GET", path(made.id, "live")).count > before }
        #expect(hub.sent("GET", path(made.id, "live")).last?.query["parts"] == "topics")
    }

    // MARK: The card

    /// The top card: phase 2's Live box for a meeting started without the highlights and before the first; Highlights
    /// once there are any; with the keyboard up (the regular sizes) the folded Live box either way, and at the
    /// accessibility sizes, where nothing folds, as without it.
    @Test func theCardsMode() {
        typealias H = MeetingHighlights
        #expect(H.mode(shows: false, hasHighlights: true, writing: false, accessibilitySize: false) == .live(folded: false), "the flag is off")
        #expect(H.mode(shows: true, hasHighlights: false, writing: false, accessibilitySize: false) == .live(folded: false), "none yet")
        #expect(H.mode(shows: true, hasHighlights: true, writing: false, accessibilitySize: false) == .highlights)
        #expect(H.mode(shows: true, hasHighlights: true, writing: true, accessibilitySize: false) == .live(folded: true), "the keyboard is up")
        #expect(H.mode(shows: false, hasHighlights: false, writing: true, accessibilitySize: false) == .live(folded: true), "as phase 2")
        #expect(H.mode(shows: true, hasHighlights: true, writing: true, accessibilitySize: true) == .highlights, "nothing folds")
        #expect(H.mode(shows: true, hasHighlights: false, writing: true, accessibilitySize: true) == .live(folded: false))
    }

    /// The rows keep the order given, say their time from the recording's start, and keep their identity when the list
    /// comes again with another highlight in it (unique when the room lists one twice); the newest is the latest `at`,
    /// the last given of those at the same time.
    @Test func theRowsAndTheNewestHighlight() {
        let first = MeetingHighlights.rows([highlight("Keep the cap.", 312, 358), highlight("Vendor through Q3.", 1120.5, 1171)])
        #expect(first.map(\.clock) == ["5:12", "18:40"])
        #expect(first.map(\.end) == [358, 1171])
        let again = MeetingHighlights.rows([highlight("Keep the cap.", 312, 358), highlight("Pricing by seat.", 900),
                                            highlight("Vendor through Q3.", 1120.5, 1171)])
        #expect(again[0].id == first[0].id && again[2].id == first[1].id, "an insert moves no other row's identity")
        let twice = MeetingHighlights.rows([highlight("Same.", 60), highlight("Same.", 60)])
        #expect(Set(twice.map(\.id)).count == 2)
        #expect(MeetingHighlights.rows([highlight("Long", 3725)]).first?.clock == "1:02:05")

        #expect(MeetingHighlights.newest(again)?.text == "Vendor through Q3.")
        let tie = MeetingHighlights.rows([highlight("Earlier", 60), highlight("First at 300", 300), highlight("Second at 300", 300)])
        #expect(MeetingHighlights.newest(tie)?.text == "Second at 300")
        #expect(MeetingHighlights.newest([]) == nil)

        #expect(MeetingHighlights.shown(again, open: false, accessibilitySize: false) == again, "closed: all of them")
        #expect(MeetingHighlights.shown(again, open: true, accessibilitySize: false).map(\.text) == ["Vendor through Q3."],
                "open in the regular layout: the newest alone")
        #expect(MeetingHighlights.shown(again, open: true, accessibilitySize: true) == again, "open at the accessibility sizes: all")
        #expect(MeetingHighlights.shown([], open: true, accessibilitySize: false).isEmpty)
    }

    // MARK: The transcript at a highlight's minute

    private func items(_ starts: [Double], marks: [Double] = []) -> [RecordingTimeline.Item] {
        RecordingTimeline.make(sentences: starts.enumerated().map { .init(segment: $0.offset / 2, start: $0.element, text: "s\($0.offset)") },
                               marks: marks, asks: [], transcribedUntil: (starts.last ?? 0) + 10, now: (starts.last ?? 0) + 10)
    }

    private func ids(_ items: [RecordingTimeline.Item], at starts: [Double]) -> Set<RecordingTimeline.Item.ID> {
        Set(items.filter { item in
            if case .sentence = item.kind { starts.contains(item.at) } else { false }
        }.map(\.id))
    }

    /// Every one of the phone's lines that starts in the highlight's minute (`at` to `end`) is lit and the first one is
    /// scrolled to; bookmarks and other rows never are. A time that went through the hub and back finds its line.
    @Test func theTranscriptLightsTheHighlightsMinute() throws {
        let built = items([250, 300, 315, 330, 345.5, 400], marks: [310])
        let focus = try #require(RecordingTimeline.focus(at: 300, end: 345.5, in: built))
        #expect(focus.lit == ids(built, at: [300, 315, 330, 345.5]))
        #expect(focus.scroll == ids(built, at: [300]).first)
        let drift = try #require(RecordingTimeline.focus(at: 300.0004, end: 300.0004, in: built))
        #expect(drift.lit == ids(built, at: [300]), "the same millisecond")
        let backwards = try #require(RecordingTimeline.focus(at: 315, end: 200, in: built))
        #expect(backwards.lit == ids(built, at: [315]), "an end before the start is the start")
    }

    /// When none of the phone's lines starts in the minute (the hub's lines aren't the phone's), the line
    /// `RecordingMinutes.sentenceIndex` picks for `at` is the one lit and scrolled to; with no line at all, nothing.
    @Test func withoutALineInTheMinuteTheLineSaidThen() throws {
        let built = items([10, 100, 200], marks: [150])
        let between = try #require(RecordingTimeline.focus(at: 150, end: 160, in: built))
        #expect(between.lit == ids(built, at: [100]) && between.scroll == ids(built, at: [100]).first, "the last that started by then")
        let early = try #require(RecordingTimeline.focus(at: 2, end: 5, in: built))
        #expect(early.lit == ids(built, at: [10]), "before the first line: the first")
        let grace = try #require(RecordingTimeline.focus(at: 99.5, end: 99.6, in: built))
        #expect(grace.lit == ids(built, at: [100]), "a second's grace, as sentenceIndex has")
        #expect(RecordingTimeline.focus(at: 10, end: 20, in: items([], marks: [10])) == nil, "an empty transcript")
        #expect(RecordingTimeline.focus(at: .nan, end: 20, in: built) == nil)
    }
}
