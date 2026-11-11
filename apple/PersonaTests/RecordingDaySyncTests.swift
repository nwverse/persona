import Foundation
import os
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z, 16:00 in Shanghai

/// The phone's clock, readable from the store's queue.
private final class Clock: Sendable {
    private let value = OSAllocatedUnfairLock(initialState: anchor.addingTimeInterval(60))
    var now: Date { value.withLock { $0 } }
    func advance(_ seconds: TimeInterval) { value.withLock { $0 = $0.addingTimeInterval(seconds) } }
}

private func refusal(_ status: Int, _ code: String? = nil, _ extra: [String: String] = [:]) -> APIError {
    var fields = extra
    if let code { fields["code"] = code }
    return APIError(status: status, message: "refused", fields: fields)
}

/// A hub that keeps day recordings as the contract says (docs/assistant/day-recap.md "Phone to hub"): a `PUT` replaces
/// every segment and sets the base, an append stores by position on that base (`base_changed`, `segments_gap`,
/// `day_full` past `capacity`, `too_large` past `maxPerAppend`), the detail carries the recap. Any request can be made
/// to fail once (`fail`). Every request is kept, its body as JSON.
private final class DayHub: @unchecked Sendable {
    struct Request {
        var method: String
        var path: String
        var body: [String: Any]
    }

    private struct Held {
        var base: Int64
        var revision: Int64
        var segments: [[String: Any]]
    }

    private let lock = NSLock()
    private var watcher: (@Sendable (Request) -> Void)?
    private var held: [String: Held] = [:]
    private var failures: [String: [APIError]] = [:]
    private var log: [Request] = []
    private var capacity = Int.max
    private var maxPerAppend = Int.max
    private var status: [String: String] = [:]
    private var recap: [String: Any]?

    var requests: [Request] { lock.withLock { log } }
    func calls(_ method: String) -> [Request] { requests.filter { $0.method == method } }
    func texts(_ id: UUID) -> [String] { lock.withLock { held[key(id)]?.segments.map { $0["text"] as? String ?? "" } ?? [] } }
    func base(_ id: UUID) -> Int64? { lock.withLock { held[key(id)]?.base } }
    func count(_ id: UUID) -> Int? { lock.withLock { held[key(id)]?.segments.count } }

    /// The next `method` request fails with `error`.
    func fail(_ method: String, _ error: APIError) { lock.withLock { failures[method, default: []].append(error) } }
    func set(capacity: Int? = nil, maxPerAppend: Int? = nil) {
        lock.withLock {
            if let capacity { self.capacity = capacity }
            if let maxPerAppend { self.maxPerAppend = maxPerAppend }
        }
    }
    /// Every `method` request answers `200` with this status (`expired`).
    func answer(_ method: String, status: String) { lock.withLock { self.status[method] = status } }
    func setRecap(final: Bool, generatedAt: String) {
        lock.withLock { recap = ["final": final, "generated_at": generatedAt, "summary": "A day."] }
    }
    /// The hub lost its last segments (a restore), or the whole recording (a new hub).
    func truncate(_ id: UUID, to count: Int) {
        lock.withLock {
            guard var rec = held[key(id)] else { return }
            rec.segments = Array(rec.segments.prefix(count))
            held[key(id)] = rec
        }
    }
    func forget(_ id: UUID) { lock.withLock { held[key(id)] = nil } }
    /// Called with every request as it arrives, before it is answered.
    func watch(_ body: @escaping @Sendable (Request) -> Void) { lock.withLock { watcher = body } }

    func answer(_ request: LocalRequest) throws -> Data {
        let body = (request.body.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]) ?? [:]
        if let watcher = lock.withLock({ watcher }) { watcher(Request(method: request.method, path: request.path, body: body)) }
        return try lock.withLock {
            log.append(Request(method: request.method, path: request.path, body: body))
            if var queued = failures[request.method], !queued.isEmpty {
                let error = queued.removeFirst()
                failures[request.method] = queued
                throw error
            }
            let parts = request.path.split(separator: "/").map(String.init)
            switch request.method {
            case "GET":
                var detail: [String: Any] = ["meta": ["kind": "day"]]
                if let recap { detail["recap"] = recap }
                return try json(["detail": detail])
            case "PUT": return try put(parts[3], body)
            default: return try append(parts[3], body)
            }
        }
    }

    private func put(_ id: String, _ body: [String: Any]) throws -> Data {
        let revision = (body["revision"] as? NSNumber)?.int64Value ?? 0
        if let status = status["PUT"] { return try json(["id": id, "revision": revision, "status": status]) }
        if let have = held[id], have.revision > revision { return try json(["id": id, "revision": have.revision, "status": "stale"]) }
        held[id] = Held(base: revision, revision: revision, segments: body["segments"] as? [[String: Any]] ?? [])
        return try json(["id": id, "revision": revision, "status": "stored"])
    }

    private func append(_ id: String, _ body: [String: Any]) throws -> Data {
        guard var rec = held[id] else { throw refusal(404, "recording_not_found") }
        if let status = status["POST"] { return try json(["id": id, "revision": rec.revision, "next": rec.segments.count, "status": status]) }
        let base = (body["base"] as? NSNumber)?.int64Value ?? 0
        let revision = (body["revision"] as? NSNumber)?.int64Value ?? 0
        let from = body["from"] as? Int ?? 0
        let segments = body["segments"] as? [[String: Any]] ?? []
        guard revision > base else { throw refusal(400) }
        guard segments.count <= maxPerAppend else { throw refusal(413, "too_large") }
        guard base == rec.base else { throw refusal(409, "base_changed", ["base": "\(rec.base)", "next": "\(rec.segments.count)"]) }
        guard from <= rec.segments.count else { throw refusal(409, "segments_gap", ["next": "\(rec.segments.count)"]) }
        let new = Array(segments.dropFirst(rec.segments.count - from))
        guard rec.segments.count + new.count <= capacity else { throw refusal(413, "day_full", ["next": "\(rec.segments.count)"]) }
        rec.segments += new
        rec.revision = max(rec.revision, revision)
        held[id] = rec
        return try json(["id": id, "revision": rec.revision, "next": rec.segments.count, "status": "stored"])
    }

    private func json(_ object: [String: Any]) throws -> Data { try JSONSerialization.data(withJSONObject: object) }
    private func key(_ id: UUID) -> String { id.uuidString.lowercased() }
}

/// Requests that wait while it is held, and fail as cancelled once their task is: a request caught in flight.
private final class Gate: Sendable {
    private let state = OSAllocatedUnfairLock(initialState: (held: false, arrived: 0))

    var arrived: Int { state.withLock { $0.arrived } }
    func hold() { state.withLock { $0.held = true } }
    func release() { state.withLock { $0.held = false } }

    func pass() async throws {
        state.withLock { $0.arrived += 1 }
        while state.withLock({ $0.held }) {
            try Task.checkCancellation()
            try await Task.sleep(for: .milliseconds(2))
        }
        try Task.checkCancellation()
    }
}

/// The app's state as the upload sees it: in front or not, whether the system gives background time, and the grants it
/// gave, each of which can be made to run out.
@MainActor private final class FakeAppState: RecordingDayAppState {
    final class Grant: RecordingDayGrant {
        let onExpiry: @MainActor () -> Void
        private(set) var ended = false
        init(onExpiry: @escaping @MainActor () -> Void) { self.onExpiry = onExpiry }
        func end() { ended = true }
        func expire() {
            guard !ended else { return }
            onExpiry()
            ended = true
        }
    }

    var inForeground = true
    var givesGrants = true
    private(set) var grants: [Grant] = []

    func beginGrant(named name: String, onExpiry: @escaping @MainActor () -> Void) -> RecordingDayGrant? {
        guard givesGrants else { return nil }
        let grant = Grant(onExpiry: onExpiry)
        grants.append(grant)
        return grant
    }
}

/// Waits (briefly) until `condition` holds: a request reaching the hub happens on the upload's own task.
@MainActor private func until(_ condition: () -> Bool) async {
    var tries = 0
    while !condition(), tries < 2000 {
        tries += 1
        try? await Task.sleep(for: .milliseconds(1))
    }
    #expect(condition())
}

/// Lets the store's change stream reach a started upload, then waits for any pass that started.
@MainActor private func settle(_ sync: RecordingDaySync) async {
    for _ in 0..<50 { await Task.yield() }
    await sync.whenIdle()
}

/// A temporary recordings folder with one day in it (`id`, Shanghai, recorded from `anchor`), its library row in English,
/// a store whose coalesced write never comes during a test (only `flush` writes), and the fake hub.
@MainActor private final class Fixture {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("day-sync-\(UUID().uuidString)", isDirectory: true)
    let id = UUID()
    let clock = Clock()
    let hub = DayHub()
    let gate = Gate()
    let app = FakeAppState()
    let library: RecordingLibrary
    let store: RecordingDayStore
    var features: Set<String> = [RecordingDayContract.feature]
    var removed: [UUID] = []
    var noted: [UUID: [UUID]] = [:]
    var finals = 0

    init() {
        library = RecordingLibrary(root: root)
        let lib = library, clock = clock
        store = RecordingDayStore(root: root, isDeleted: { lib.isDeletedSnapshot($0) }, zone: { TimeZone(identifier: "Asia/Shanghai")! },
                                  now: { clock.now }, writeDelay: 3600)
        library.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor)
        library.change(id) { $0.locale = "en-US" }
    }

    func sync() -> RecordingDaySync {
        let hub = hub, clock = clock, gate = gate
        let api = APIClient(config: ServerConfig(url: URL(string: "https://hub.invalid")!, token: "t"), transport: { request in
            try await gate.pass()
            return try hub.answer(request)
        })
        return RecordingDaySync(store: store, library: library, api: { api }, features: { [unowned self] in self.features },
                                now: { clock.now }, days: { 14 }, appState: app,
                                noteParts: { [unowned self] day, ids in self.noted[day] = ids },
                                removed: { [unowned self] in self.removed.append($0) },
                                recapFinal: { [unowned self] in self.finals += 1 }, debounce: .zero)
    }

    /// Chunk `seq`, `offset` seconds after `anchor`, stored as the watch sent it and transcribed: `texts` are its sentences,
    /// the first starting 1 s in, a second apart.
    func chunk(_ seq: Int, at offset: Double, texts: [String], marks: [RecordingDayHeader.Mark]? = nil) async throws {
        let header = RecordingDayHeader(recording: id, day: "2027-01-15", seq: seq, startedAt: anchor.addingTimeInterval(offset),
                                        duration: 10, level: -30, marks: marks, recordingStartedAt: anchor)
        _ = try store.store(header: header, audio: Data([1, 2, 3]))
        let sentences = texts.enumerated().map { RecordingSentence(start: Double($0.offset) + 1, end: Double($0.offset) + 1.5, text: $0.element) }
        await store.update(id) { record in
            record.chunks[seq]?.state = .transcribed
            record.chunks[seq]?.sentences = sentences
        }
    }

    func erase(_ seq: Int, at offset: Double) throws {
        let header = RecordingDayHeader(recording: id, day: "2027-01-15", seq: seq, startedAt: anchor.addingTimeInterval(offset),
                                        duration: 10, erased: true, recordingStartedAt: anchor)
        #expect(try store.store(header: header, audio: nil) == .updated)
    }

    func end(_ seq: Int, at offset: Double) throws {
        let header = RecordingDayHeader(recording: id, day: "2027-01-15", seq: seq, startedAt: anchor.addingTimeInterval(offset),
                                        duration: 0, end: true, after: RecordingDayContract.Reason.stopped, recordingStartedAt: anchor)
        _ = try store.store(header: header, audio: nil)
    }

    func record() async throws -> RecordingDayRecord { try #require(await store.record(id)) }
    func part(_ number: Int = 1) async throws -> RecordingDayPart { try #require(try await record().parts.first { $0.part == number }) }
    /// The day as `day.json` holds it now: what a crash would leave.
    nonisolated static func onDisk(_ root: URL, _ id: UUID) -> RecordingDayRecord? {
        let url = root.appendingPathComponent(id.uuidString).appendingPathComponent("day.json")
        return (try? Data(contentsOf: url)).flatMap { try? JSONDecoder().decode(RecordingDayRecord.self, from: $0) }
    }
    func cleanUp() { try? FileManager.default.removeItem(at: root) }
}

private extension DayHub.Request {
    var revision: Int64 { (body["revision"] as? NSNumber)?.int64Value ?? 0 }
    var segments: [[String: Any]] { body["segments"] as? [[String: Any]] ?? [] }
    var texts: [String] { segments.map { $0["text"] as? String ?? "" } }
    var coverage: [String: Any] { body["coverage"] as? [String: Any] ?? [:] }
}

/// All-day recording's upload (docs/assistant/day-recap.md "Phone to hub"): only behind the flag, created with a `PUT`,
/// grown by appends on wall-clock offsets, every contract error handled by its code (C22), parts on `day_full` (C18),
/// repairs crash-safe and revisions only going up (C17).
@MainActor struct RecordingDaySyncTests {
    @Test func nothingGoesWithoutTheHubFlag() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["Morning."])
        f.features = ["sessions_list"]
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .blocked)
        #expect(f.hub.requests.isEmpty)
        #expect(try await f.record().parts.isEmpty, "not even a part is made for a hub that doesn't take days")

        f.features = [RecordingDayContract.feature]
        #expect(await sync.syncNow(f.id) == .done)
        #expect(f.hub.requests.map(\.method) == ["PUT", "POST"])
    }

    @Test func theCreatePutCarriesTheDayAndAppendsGoOnTheWallClock() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["Morning."], marks: [RecordingDayHeader.Mark(at: 4, label: RecordingDayMark.meeting)])
        try await f.chunk(1, at: 10, texts: ["Second.", "Third."])
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .done)

        let put = try #require(f.hub.calls("PUT").first)
        #expect(put.path == "/api/v1/recordings/\(f.id.uuidString.lowercased())")
        #expect(put.body["kind"] as? String == "day")
        #expect(put.body["day"] as? String == "2027-01-15")
        #expect(put.body["part"] as? Int == 1)
        let started = try #require(put.body["started_at"] as? String)
        #expect(try Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(started) == anchor)
        #expect(put.body["ended_at"] == nil, "still recording")
        #expect(put.body["duration"] as? Double == 20)
        #expect(put.body["language"] as? String == "en-US")
        #expect(put.body["device"] as? String == "watch")
        #expect(put.body["title"] as? String == "")
        #expect(put.texts.isEmpty)
        #expect(put.body["complete"] as? Bool == false)
        let bookmarks = put.body["bookmarks"] as? [[String: Any]] ?? []
        #expect(bookmarks.count == 1 && bookmarks[0]["at"] as? Double == 4 && bookmarks[0]["label"] as? String == "meeting")
        #expect(put.coverage["to"] as? Double == 0, "nothing transcribed is on the hub yet")
        #expect(put.revision > 0)

        let append = try #require(f.hub.calls("POST").first)
        #expect(append.path.hasSuffix("/segments"))
        #expect((append.body["base"] as? NSNumber)?.int64Value == put.revision)
        #expect(append.revision > put.revision)
        #expect(append.body["from"] as? Int == 0)
        #expect(append.texts == ["Morning.", "Second.", "Third."], "seq order, then sentence order")
        // Seconds since started_at, pauses included: the chunk's start plus the sentence's offset in it.
        #expect(append.segments.map { $0["start"] as? Double } == [1, 11, 12])
        #expect(append.coverage["to"] as? Double == 20)

        var part = try await f.part()
        #expect(part.created && part.base == put.revision && part.next == 3 && part.count == 3)
        #expect(f.hub.texts(f.id) == ["Morning.", "Second.", "Third."])

        // New text while recording waits out `appendInterval` after the last append.
        try await f.chunk(2, at: 20, texts: ["Fourth."])
        f.clock.advance(5)
        guard case .later(let at) = await sync.syncNow(f.id) else { Issue.record("expected the throttle"); return }
        #expect(at == f.clock.now.addingTimeInterval(RecordingDayContract.appendInterval - 5))
        #expect(f.hub.calls("POST").count == 1)
        f.clock.advance(RecordingDayContract.appendInterval)
        #expect(await sync.syncNow(f.id) == .done)
        let next = try #require(f.hub.calls("POST").last)
        #expect(next.body["from"] as? Int == 3 && next.texts == ["Fourth."])
        part = try await f.part()
        #expect(part.next == 4 && part.count == 4)
    }

    @Test func theEndGoesAtOnceWithCompleteAndEndedAt() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["Morning."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        try await f.chunk(1, at: 10, texts: ["Evening."])
        try f.end(2, at: 3600)
        #expect(await sync.syncNow(f.id) == .done, "the day ended: no throttle")
        let last = try #require(f.hub.calls("POST").last)
        #expect(last.texts == ["Evening."])
        #expect(last.body["complete"] as? Bool == true)
        let ended = try #require(last.body["ended_at"] as? String)
        #expect(try Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(ended) == anchor.addingTimeInterval(3600))
        #expect(last.coverage["to"] as? Double == 3600)
        let gaps = last.coverage["gaps"] as? [[String: Any]] ?? []
        #expect(gaps.map { $0["reason"] as? String } == ["stopped"])
        #expect(try await f.part().complete)
    }

    @Test func baseChangedOnABaseThePhoneDidNotPutRepairs() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One.", "Two."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        let first = try await f.part()
        try await f.chunk(1, at: 10, texts: ["Three."])
        f.clock.advance(60)
        f.hub.fail("POST", refusal(409, "base_changed", ["base": "999", "next": "2"]))
        #expect(await sync.syncNow(f.id) == .done)
        let repair = try #require(f.hub.calls("PUT").last)
        #expect(f.hub.calls("PUT").count == 2)
        #expect(repair.texts == ["One.", "Two.", "Three."], "every positioned sentence")
        #expect(repair.revision > first.maxSentRevision)
        let part = try await f.part()
        #expect(part.base == repair.revision && part.next == 3 && part.count == 3 && part.pendingPut == nil)
        #expect(f.hub.texts(f.id) == ["One.", "Two.", "Three."])
    }

    @Test func segmentsGapSendsFromTheHubsCount() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One.", "Two."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        f.hub.truncate(f.id, to: 1)
        try await f.chunk(1, at: 10, texts: ["Three."])
        f.clock.advance(60)
        #expect(await sync.syncNow(f.id) == .done)
        let posts = f.hub.calls("POST")
        #expect(posts.map { $0.body["from"] as? Int } == [0, 2, 1], "refused at 2 (the hub holds 1), sent again from 1")
        #expect(f.hub.texts(f.id) == ["One.", "Two.", "Three."])
        #expect(f.hub.calls("PUT").count == 1, "no repair needed")
    }

    @Test func recordingNotFoundCreatesItAgainWithItsText() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        f.hub.forget(f.id)
        try await f.chunk(1, at: 10, texts: ["Two."])
        f.clock.advance(60)
        #expect(await sync.syncNow(f.id) == .done)
        #expect(f.hub.requests.map(\.method) == ["PUT", "POST", "POST", "PUT"])
        #expect(f.hub.calls("PUT").last?.texts == ["One.", "Two."], "every sentence that has a position, the one refused too")
        #expect(f.hub.texts(f.id) == ["One.", "Two."])
        #expect(try await f.part().next == 2)
    }

    @Test func anOlderManagerIsLeftAloneUntilItsNextHello() async throws {
        for error in [refusal(404), refusal(400)] {
            let f = Fixture()
            defer { f.cleanUp() }
            try await f.chunk(0, at: 0, texts: ["One."])
            let sync = f.sync()
            f.hub.fail("PUT", error)
            #expect(await sync.syncNow(f.id) == .blocked)
            #expect(try await f.record().unsupportedUntilHello)
            // New chunks don't bring a storm of refused requests (C22).
            try await f.chunk(1, at: 10, texts: ["Two."])
            f.clock.advance(60)
            #expect(await sync.syncNow(f.id) == .blocked)
            #expect(f.hub.requests.count == 1)
            sync.helloReceived(offered: true)
            #expect(await sync.syncNow(f.id) == .done)
            #expect(f.hub.texts(f.id) == ["One.", "Two."])
            #expect(try await f.record().unsupportedUntilHello == false)
        }
    }

    @Test func tooLargeHalvesTheBatchAndADaySizedSentenceStops() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: (1...10).map { "S\($0)." })
        f.hub.set(maxPerAppend: 4)
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .done)
        let sizes = f.hub.calls("POST").map(\.segments.count)
        #expect(sizes.prefix(7) == [10, 10, 10, 10, 10, 10, 7], "halved from 500 until a batch fits")
        #expect(sizes.dropFirst(7).allSatisfy { $0 <= 4 })
        #expect(f.hub.texts(f.id) == (1...10).map { "S\($0)." }, "in order, none twice")
        #expect(try await f.record().parts.count == 1, "too_large never starts a part")

        let g = Fixture()
        defer { g.cleanUp() }
        try await g.chunk(0, at: 0, texts: ["Long."])
        g.hub.set(maxPerAppend: 0)
        #expect(await g.sync().syncNow(g.id) == .blocked)
        #expect(try await g.record().problem == "too_large")
    }

    @Test func dayFullContinuesInTheNextPart() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One.", "Two."])
        f.hub.set(capacity: 2)
        let sync = f.sync()
        await sync.syncNow(f.id)
        try await f.chunk(1, at: 10, texts: ["Three."])
        f.clock.advance(60)
        #expect(await sync.syncNow(f.id) == .done)

        let record = try await f.record()
        #expect(record.parts.count == 2)
        let first = try #require(record.parts.first { $0.part == 1 })
        let second = try #require(record.parts.first { $0.part == 2 })
        // Part 2 starts at the first sentence that didn't fit: chunk 1 (10 s in) plus its 1 s.
        let boundary = anchor.addingTimeInterval(11)
        #expect(second.hubID != f.id && second.startedAt == boundary && second.created && second.next == 1)
        #expect(first.endedAt == boundary && first.count == 2 && first.complete)
        #expect(f.noted[f.id] == [second.hubID], "kept for deleting every part")
        // The new part was on disk before its PUT, so a crash can't make a second one.
        let disk = try JSONDecoder().decode(RecordingDayRecord.self,
                                            from: Data(contentsOf: f.root.appendingPathComponent(f.id.uuidString).appendingPathComponent("day.json")))
        #expect(disk.parts.map(\.hubID) == record.parts.map(\.hubID))

        let calls = f.hub.requests.map { "\($0.method) \($0.path.contains(second.hubID.uuidString.lowercased()) ? 2 : 1)" }
        #expect(calls == ["PUT 1", "POST 1", "POST 1", "POST 1", "PUT 2", "POST 2"])
        let close = f.hub.calls("POST")[2]
        #expect(close.texts.isEmpty && close.body["complete"] as? Bool == true && close.body["ended_at"] != nil,
                "part 1 is told it ended, and complete")
        let create = try #require(f.hub.calls("PUT").last)
        #expect(create.body["part"] as? Int == 2 && create.body["day"] as? String == "2027-01-15")
        #expect(f.hub.texts(second.hubID) == ["Three."])
        #expect(f.hub.calls("POST").last?.segments.first?["start"] as? Double == 0, "on part 2's own clock")
    }

    @Test func notDayIsAProblemAndNothingIsWrittenOverIt() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        f.hub.fail("POST", refusal(409, "not_day"))
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .blocked)
        #expect(try await f.record().problem == "not_day")
        let sent = f.hub.requests.count
        try await f.chunk(1, at: 10, texts: ["Two."])
        f.clock.advance(60)
        #expect(await sync.syncNow(f.id) == .blocked)
        #expect(f.hub.requests.count == sent)
    }

    @Test func forbiddenPausesUntilTheAppComesBack() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        f.hub.fail("PUT", refusal(403, "kb_read_only"))
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .blocked)
        #expect(sync.paused)
        #expect(await sync.syncNow(f.id) == .blocked)
        #expect(f.hub.requests.count == 1)
        sync.foreground()
        #expect(await sync.syncNow(f.id) == .done)
        #expect(f.hub.texts(f.id) == ["One."])
    }

    @Test func deletedOnTheHubGoesHereToo() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        try await f.chunk(1, at: 10, texts: ["Two."])
        f.clock.advance(60)
        f.hub.fail("POST", refusal(410, "recording_deleted"))
        #expect(await sync.syncNow(f.id) == .done)
        #expect(f.library.entry(f.id) == nil && f.library.isDeleted(f.id))
        #expect(f.removed == [f.id])
        #expect(await f.store.record(f.id) == nil)
    }

    @Test func expiredStopsTheDayForGood() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        f.hub.answer("PUT", status: "expired")
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .done)
        #expect(try await f.part().expired)
        try await f.chunk(1, at: 10, texts: ["Two."])
        f.clock.advance(60)
        await sync.syncNow(f.id)
        #expect(f.hub.requests.count == 1)
    }

    @Test func unavailableBacksOffAndSendsTheSamePositionsAgain() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One.", "Two."])
        f.hub.fail("POST", refusal(503, "kb_unavailable"))
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .failed)
        let part = try await f.part()
        #expect(part.count == 2 && part.next == 0, "positions kept, nothing confirmed")
        #expect(await sync.syncNow(f.id) == .done)
        let posts = f.hub.calls("POST")
        #expect(posts.count == 2)
        #expect(posts[1].body["from"] as? Int == 0 && posts[1].texts == ["One.", "Two."], "the same sentences at the same positions")
        #expect(f.hub.texts(f.id) == ["One.", "Two."])
    }

    @Test func anEraseRepairsTheHubWithAPut() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["Keep one."])
        try await f.chunk(1, at: 10, texts: ["Erase me."])
        try await f.chunk(2, at: 20, texts: ["Keep two."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        let before = try await f.part()
        try f.erase(1, at: 10)
        #expect(await sync.syncNow(f.id) == .done)
        let repair = try #require(f.hub.calls("PUT").last)
        #expect(f.hub.calls("PUT").count == 2)
        #expect(repair.revision > before.maxSentRevision)
        #expect(repair.texts == ["Keep one.", "Keep two."])
        let gaps = repair.coverage["gaps"] as? [[String: Any]] ?? []
        #expect(gaps.contains { $0["reason"] as? String == "erased" && $0["from"] as? Double == 10 && $0["to"] as? Double == 20 })
        let after = try await f.part()
        #expect(after.base == repair.revision && after.next == 2 && after.count == 2 && after.pendingPut == nil)
        #expect(f.hub.texts(f.id) == ["Keep one.", "Keep two."])
    }

    @Test func aRepairDecidedBeforeACrashGoesAgainWithItsRevision() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        await f.sync().syncNow(f.id)
        let decided: Int64 = 9_000_000_000_000
        await f.store.update(f.id) { record in
            record.parts[0].pendingPut = decided
            record.parts[0].maxSentRevision = decided
        }
        #expect(await f.store.flush(f.id))
        let relaunched = f.sync()
        #expect(await relaunched.syncNow(f.id) == .done)
        let again = try #require(f.hub.calls("PUT").last)
        #expect(f.hub.requests.count == 3)
        #expect(again.revision == decided && again.texts == ["One."])
        let part = try await f.part()
        #expect(part.pendingPut == nil && part.base == decided)
    }

    @Test func revisionsOnlyGoUpEvenWhenTheClockGoesBack() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        f.clock.advance(-3600)
        try await f.chunk(1, at: 10, texts: ["Two."])
        try f.end(2, at: 20)
        await sync.syncNow(f.id)
        let revisions = f.hub.requests.map(\.revision)
        #expect(revisions.count >= 3)
        #expect(zip(revisions, revisions.dropFirst()).allSatisfy { $0 < $1 })
        let base = f.hub.calls("PUT")[0].revision
        #expect(f.hub.calls("POST").allSatisfy { $0.revision > base && ($0.body["base"] as? NSNumber)?.int64Value == base })
        #expect(try await f.part().maxSentRevision == revisions.last)
    }

    @Test func reconciliationResendsWhatTheHubLostAndReplacesAnotherBase() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One.", "Two."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        let base = try #require(try await f.part().base)
        // Same base, fewer positions than sent: from there again.
        f.hub.truncate(f.id, to: 1)
        f.clock.advance(120)
        sync.reconciled([f.id: [RecordingDayHubRow(id: f.id, revision: base, part: 1, next: 1, base: base)]])
        #expect(await sync.syncNow(f.id) == .done)
        #expect(f.hub.calls("POST").last?.body["from"] as? Int == 1)
        #expect(f.hub.texts(f.id) == ["One.", "Two."])
        // Another base: replaced.
        f.clock.advance(120)
        sync.reconciled([f.id: [RecordingDayHubRow(id: f.id, revision: base + 5, part: 1, next: 2, base: base + 5)]])
        #expect(await sync.syncNow(f.id) == .done)
        let repair = try #require(f.hub.requests.last)
        #expect(repair.method == "PUT" && repair.texts == ["One.", "Two."] && repair.revision > base + 5)
        // A list made before the phone's last request says nothing about it.
        let current = try await f.part()
        sync.reconciled([f.id: [RecordingDayHubRow(id: f.id, revision: base, part: 1, next: 0, base: base)]])
        #expect(await sync.syncNow(f.id) == .done)
        #expect(try await f.part() == current)
    }

    @Test func aReconciledBaseEqualToThePendingRepairIsAdopted() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        // A repair was stored on the hub, but its answer was lost before the phone wrote it down.
        let decided: Int64 = 9_000_000_000_000
        await f.store.update(f.id) { $0.parts[0].pendingPut = decided }
        f.clock.advance(120)
        let sent = f.hub.requests.count
        sync.reconciled([f.id: [RecordingDayHubRow(id: f.id, revision: decided, part: 1, next: 1, base: decided)]])
        #expect(await sync.syncNow(f.id) == .done)
        #expect(f.hub.requests.count == sent, "nothing sent again")
        let part = try await f.part()
        #expect(part.base == decided && part.pendingPut == nil && part.next == 1)
    }

    @Test func aReadyRecapOnACompleteDayIsReadForItsFinalTime() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        try f.end(1, at: 10)
        let sync = f.sync()
        await sync.syncNow(f.id)
        #expect(try await f.part().complete)
        f.hub.setRecap(final: true, generatedAt: "2027-01-15T13:00:41Z")
        f.clock.advance(120)
        let base = try await f.part().base
        sync.reconciled([f.id: [RecordingDayHubRow(id: f.id, part: 1, next: 1, base: base, recapState: "ready")]])
        await sync.syncNow(f.id)
        let part = try await f.part()
        #expect(part.recapState == "ready")
        #expect(part.recapFinalAt == JSON.parseDate("2027-01-15T13:00:41Z"))
        #expect(f.finals == 1, "the keeper's 3-day clock starts")
        #expect(f.hub.calls("GET").count == 1)
        await sync.syncNow(f.id)
        #expect(f.hub.calls("GET").count == 1, "read once")
    }

    @Test func aDayPastTheHubsWindowIsNotUploaded() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        f.clock.advance(15 * 86_400)
        #expect(await f.sync().syncNow(f.id) == .done)
        #expect(f.hub.requests.isEmpty)
    }

    @Test func aFailingRequestWaitsOutItsBackoffWhateverChangesMeanwhile() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        await sync.syncNow(f.id)
        // The hub fails the next two appends. The upload follows the store's changes as in the app, the ones its own
        // bookkeeping makes with every request among them (C22).
        f.hub.fail("POST", refusal(503, "kb_unavailable"))
        f.hub.fail("POST", refusal(503, "kb_unavailable"))
        sync.start()
        f.clock.advance(60)
        try await f.chunk(1, at: 10, texts: ["Two."])
        await settle(sync)
        #expect(f.hub.calls("POST").count == 2, "one failed append, not one for every change it made")
        // New text during the backoff waits for it too.
        try await f.chunk(2, at: 20, texts: ["Three."])
        await settle(sync)
        #expect(f.hub.calls("POST").count == 2)
        // Once the first wait is over it goes again, fails again, and the next wait is twice as long.
        f.clock.advance(RecordingDaySync.backoff(after: 1))
        sync.changed(f.id)
        await settle(sync)
        #expect(f.hub.calls("POST").count == 3)
        f.clock.advance(RecordingDaySync.backoff(after: 1))
        sync.changed(f.id)
        await settle(sync)
        #expect(f.hub.calls("POST").count == 3, "10 s after the second failure, not 5")
        f.clock.advance(RecordingDaySync.backoff(after: 2))
        sync.changed(f.id)
        await settle(sync)
        #expect(f.hub.calls("POST").count == 4)
        #expect(f.hub.texts(f.id).prefix(2) == ["One.", "Two."], "through at last, from the same position")
    }

    @Test func everyRequestsRevisionIsOnDiskBeforeItGoes() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        // What a crash at the moment each request reaches the hub would leave: a revision at least as high (C17).
        let seen = OSAllocatedUnfairLock(initialState: [(sent: Int64, onDisk: Int64)]())
        let root = f.root, id = f.id
        f.hub.watch { request in
            let sent = request.revision
            let onDisk = Fixture.onDisk(root, id)?.parts.first?.maxSentRevision ?? 0
            seen.withLock { $0.append((sent, onDisk)) }
        }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        #expect(await sync.syncNow(f.id) == .done)
        try await f.chunk(1, at: 10, texts: ["Two."])
        f.clock.advance(60)
        f.hub.fail("POST", refusal(503, "kb_unavailable"))
        #expect(await sync.syncNow(f.id) == .failed)
        #expect(await sync.syncNow(f.id) == .done, "the retry")
        try f.end(2, at: 20)
        #expect(await sync.syncNow(f.id) == .done, "an append with no segments: the end")
        let requests = seen.withLock { $0 }
        #expect(f.hub.requests.map(\.method) == ["PUT", "POST", "POST", "POST", "POST"])
        #expect(f.hub.calls("POST").last?.texts.isEmpty == true)
        #expect(requests.count == 5 && requests.allSatisfy { $0.onDisk >= $0.sent })
    }

    @Test func aNewLanguageRepairsTheHubEvenWhenNoTextChanged() async throws {
        for silent in [false, true] {
            let f = Fixture()
            defer { f.cleanUp() }
            try await f.chunk(0, at: 0, texts: silent ? [] : ["Un."])
            try f.end(1, at: 10)
            let sync = f.sync()
            await sync.syncNow(f.id)
            let before = try await f.part()
            #expect(before.complete)
            // Retention took the audio (or there was no speech): nothing is read again, yet the hub's language changes.
            await f.store.removeAudio(f.id, seqs: [0])
            f.library.change(f.id) { $0.locale = "fr-FR" }
            f.clock.advance(60)
            await sync.languageChanged(f.id)
            let decided = try #require(Fixture.onDisk(f.root, f.id)?.parts.first?.pendingPut, "decided, and on disk before it goes")
            #expect(decided > before.maxSentRevision)
            await sync.whenIdle()
            let repair = try #require(f.hub.calls("PUT").last)
            #expect(f.hub.calls("PUT").count == 2)
            #expect(repair.body["language"] as? String == "fr-FR" && repair.revision == decided)
            #expect(repair.texts == (silent ? [] : ["Un."]))
            let after = try await f.part()
            #expect(after.pendingPut == nil && after.base == decided && after.complete)
        }
    }

    @Test func aRecapThatIsNotFinalYetIsReadAgainLater() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        try f.end(1, at: 10)
        let sync = f.sync()
        await sync.syncNow(f.id)
        let base = try await f.part().base
        let rows = [f.id: [RecordingDayHubRow(id: f.id, part: 1, next: 1, base: base, recapState: "ready")]]
        f.hub.setRecap(final: false, generatedAt: "2027-01-15T09:00:00Z")
        f.clock.advance(120)
        sync.reconciled(rows)
        await sync.whenIdle()
        #expect(f.hub.calls("GET").count == 1)
        #expect(try await f.part().recapFinalAt == nil)
        // The final recap is written between two lists that both say `ready` (C21).
        f.hub.setRecap(final: true, generatedAt: "2027-01-15T22:00:00Z")
        f.clock.advance(600)
        sync.reconciled(rows)
        await sync.whenIdle()
        #expect(f.hub.calls("GET").count == 1, "not read again before the interval")
        f.clock.advance(RecordingDaySync.recapProbeInterval)
        sync.reconciled(rows)
        await sync.whenIdle()
        #expect(f.hub.calls("GET").count == 2)
        #expect(try await f.part().recapFinalAt == JSON.parseDate("2027-01-15T22:00:00Z"))
        #expect(f.finals == 1, "the keeper's 3-day clock starts")
        f.clock.advance(RecordingDaySync.recapProbeInterval)
        sync.reconciled(rows)
        await sync.whenIdle()
        #expect(f.hub.calls("GET").count == 2, "final: not read again")
    }

    @Test func theCatchUpRunningOutLeavesNothingRunning() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        f.app.inForeground = false
        f.app.givesGrants = false
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        f.gate.hold()
        let catchUp = Task { await sync.drain() }
        await until { f.gate.arrived == 1 }
        // The task's expiration handler cancels it: the request in flight with it.
        catchUp.cancel()
        #expect(await catchUp.value, "work is left for the next catch-up")
        for _ in 0..<20 { await Task.yield() }
        await sync.whenIdle()
        // A change in the background with no time given starts nothing either.
        sync.changed(f.id)
        await sync.whenIdle()
        #expect(f.gate.arrived == 1 && f.hub.requests.isEmpty, "nothing goes after the task's time ran out")
        #expect(f.app.grants.isEmpty, "and no background time was taken in its place")
        f.gate.release()
        f.app.inForeground = true
        sync.foreground()
        await sync.whenIdle()
        #expect(f.hub.texts(f.id) == ["One."])
    }

    @Test func leavingTheFrontWithoutAGrantStopsThePass() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        try await f.chunk(0, at: 0, texts: ["One."])
        let sync = f.sync()
        f.gate.hold()
        sync.changed(f.id)
        await until { f.gate.arrived == 1 }
        f.app.inForeground = false
        f.app.givesGrants = false
        sync.background()
        await sync.whenIdle()
        sync.changed(f.id)
        await sync.whenIdle()
        #expect(f.gate.arrived == 1 && f.hub.requests.isEmpty)
        // A chunk arriving in the background brings a grant of its own; the day goes inside it, which then ends.
        f.app.givesGrants = true
        f.gate.release()
        sync.changed(f.id)
        await sync.whenIdle()
        #expect(f.hub.texts(f.id) == ["One."])
        #expect(f.app.grants.count == 1 && f.app.grants[0].ended)
    }
}
