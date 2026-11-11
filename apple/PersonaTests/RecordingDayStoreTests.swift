import Foundation
import os
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z

/// Tombstones a test controls, read from the store's queue. `delete(_:when:)` deletes a day the first time the store asks
/// after a condition holds: a delete on the main actor landing between the store's check and the end of its write.
private final class Tombstones: Sendable {
    private let deleted = OSAllocatedUnfairLock(initialState: Set<UUID>())
    private let pending = OSAllocatedUnfairLock<[UUID: @Sendable () -> Bool]>(initialState: [:])

    func delete(_ id: UUID) { deleted.withLock { _ = $0.insert(id) } }

    func delete(_ id: UUID, when condition: @escaping @Sendable () -> Bool) { pending.withLock { $0[id] = condition } }

    func contains(_ id: UUID) -> Bool {
        if deleted.withLock({ $0.contains(id) }) { return true }
        guard let condition = pending.withLock({ $0[id] }), condition() else { return false }
        delete(id)
        return true
    }
}

/// What a callback was given, from any queue.
private final class Collected<Element: Sendable>: Sendable {
    private let items = OSAllocatedUnfairLock(initialState: [Element]())
    func append(_ item: Element) { items.withLock { $0.append(item) } }
    var all: [Element] { items.withLock { $0 } }
}

/// A temporary recordings folder with one day in it, and stores over it whose coalesced write never comes during a test
/// unless asked for (only `flush` writes `day.json`): what is on disk is exactly what the test made happen. A second store
/// over the same folder is the app after a relaunch.
private struct Fixture {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("day-store-\(UUID().uuidString)", isDirectory: true)
    let id = UUID()
    let tombstones = Tombstones()

    var folder: URL { root.appendingPathComponent(id.uuidString, isDirectory: true) }

    func store(now: Date = anchor.addingTimeInterval(30), zone: String = "Asia/Shanghai",
               writeDelay: TimeInterval = 3600) -> RecordingDayStore {
        let tombstones = tombstones
        return RecordingDayStore(root: root, isDeleted: { tombstones.contains($0) }, zone: { TimeZone(identifier: zone)! },
                                 now: { now }, writeDelay: writeDelay)
    }

    /// Chunk `seq`, `offset` seconds after `start`, as the watch's header says it.
    func header(_ seq: Int, at offset: Double = 0, duration: Double = 10, erased: Bool = false, lost: Bool = false, end: Bool = false,
                after: String? = nil, id: UUID? = nil, day: String = "2027-01-15", start: Date = anchor) -> RecordingDayHeader {
        RecordingDayHeader(recording: id ?? self.id, day: day, seq: seq, startedAt: start.addingTimeInterval(offset), duration: duration,
                           level: -30, erased: erased, end: end, after: after, recordingStartedAt: start, lost: lost)
    }

    func audio(_ seq: Int) -> Data { Data(repeating: UInt8(seq % 256), count: 64) }
    func file(_ name: String) -> URL { folder.appendingPathComponent(name) }
    func exists(_ name: String) -> Bool { FileManager.default.fileExists(atPath: file(name).path) }

    func dayJSON() throws -> RecordingDayRecord {
        try JSONDecoder().decode(RecordingDayRecord.self, from: Data(contentsOf: file("day.json")))
    }

    func sidecar(_ seq: Int) throws -> RecordingDaySidecar {
        try JSONDecoder().decode(RecordingDaySidecar.self, from: Data(contentsOf: file(String(format: "seg-%03d.json", seq))))
    }

    func modified(_ name: String) throws -> Date? {
        try FileManager.default.attributesOfItem(atPath: file(name).path)[.modificationDate] as? Date
    }

    func cleanUp() { try? FileManager.default.removeItem(at: root) }
}

/// The phone's day store (docs/assistant/day-recap.md): on disk before the acknowledgement, one copy per seq with an
/// erase winning over audio, deleted days refused, rebuilt from its sidecars after a crash. Nonisolated: the store runs
/// on WatchConnectivity's queue and its own, never on the main actor.
struct RecordingDayStoreTests {
    @Test func aChunkIsOnDiskBeforeStoreReturns() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        var changes = store.changes.makeAsyncIterator()

        #expect(try store.store(header: f.header(0), audio: f.audio(0)) == .stored)
        #expect(try Data(contentsOf: f.file("seg-000.m4a")) == f.audio(0))
        #expect(f.exists("seg-000.json"), "its sidecar is what the acknowledgement stands on")
        #expect(!f.exists("day.json"), "day.json waits for the coalesced write")
        let changed = await changes.next()
        #expect(changed == f.id)

        let record = try #require(await store.record(f.id))
        let chunk = try #require(record.chunks[0])
        #expect(chunk.hasAudio && chunk.state == .waiting && chunk.level == -30 && chunk.needsTranscription)
        #expect(chunk.receivedAt == anchor.addingTimeInterval(30), "the latency clock reads the store's time")
        #expect(record.day == "2027-01-15" && record.timeZone == "Asia/Shanghai" && record.startedAt == anchor)
        #expect(try f.sidecar(0) == RecordingDaySidecar(header: f.header(0), receivedAt: anchor.addingTimeInterval(30),
                                                         timeZone: "Asia/Shanghai"))

        // By file: moved into the day before the call returns (the system deletes its copy right after).
        let incoming = f.root.appendingPathComponent("incoming.m4a")
        try f.audio(1).write(to: incoming)
        #expect(try store.store(header: f.header(1, at: 10), movingFile: incoming) == .stored)
        #expect(!FileManager.default.fileExists(atPath: incoming.path))
        #expect(try Data(contentsOf: f.file("seg-001.m4a")) == f.audio(1))

        // Not stored, so never acknowledged: a chunk without the audio it should carry, a header the watch can't write.
        #expect(throws: RecordingDayStore.Failure.missingAudio) { try store.store(header: f.header(2, at: 20), audio: Data()) }
        #expect(!f.exists("seg-002.json"))
        var broken = f.header(3, at: 30)
        broken.seq = -1
        #expect(throws: RecordingDayStore.Failure.invalidHeader) { try store.store(header: broken, audio: f.audio(3)) }
    }

    @Test func aChunkSentAgainIsStoredOnce() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        #expect(try store.store(header: f.header(0), audio: f.audio(0)) == .stored)
        // The acknowledgement went missing and the watch sends the chunk again, by message or by file.
        #expect(try store.store(header: f.header(0), audio: Data([9, 9])) == .duplicate)
        let incoming = f.root.appendingPathComponent("again.m4a")
        try Data([8]).write(to: incoming)
        #expect(try store.store(header: f.header(0), movingFile: incoming) == .duplicate)
        #expect(try Data(contentsOf: f.file("seg-000.m4a")) == f.audio(0), "the first copy stays")

        // Sent again after the day ended (the watch marks the last chunk then): the end is news, the audio stays.
        #expect(try store.store(header: f.header(0, end: true), audio: f.audio(0)) == .updated)
        #expect(try f.sidecar(0).header.end)
        let record = try #require(await store.record(f.id))
        #expect(record.chunks[0]?.end == true && record.chunks[0]?.hasAudio == true)
        #expect(record.expected == 1 && record.endedAt == anchor.addingTimeInterval(10))
        #expect(try store.store(header: f.header(0, end: true), audio: f.audio(0)) == .duplicate)
    }

    @Test func anEraseAfterTheAudioDeletesItAndItsText() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        let id = f.id
        _ = try store.store(header: f.header(0), audio: f.audio(0))
        _ = try store.store(header: f.header(1, at: 10), audio: f.audio(1))
        // Transcribed and on the hub already.
        await store.update(id) { record in
            record.chunks[0]?.state = .transcribed
            record.chunks[0]?.sentences = [RecordingSentence(start: 1, end: 4, text: "the export path")]
            record.chunks[0]?.positions = [RecordingDayPosition(part: 1, pos: 0)]
            record.parts = [RecordingDayPart(hubID: id, part: 1, startedAt: anchor, created: true, next: 1, count: 1)]
        }
        let synced = try #require(await store.record(id))
        #expect(RecordingDaySummary(record: synced, hubOffered: true).sync == .synced)

        // The user erased the last minutes on the watch.
        #expect(try store.store(header: f.header(0, erased: true), audio: nil) == .updated)
        #expect(!f.exists("seg-000.m4a"))
        #expect(try f.sidecar(0).header.erased, "the erase is as durable as the chunk was")
        let record = try #require(await store.record(id))
        let erased = try #require(record.chunks[0])
        #expect(erased.erased && !erased.hasAudio && erased.sentences == nil && erased.positions == nil)
        #expect(erased.startedAt == anchor && erased.duration == 10 && erased.receivedAt == anchor.addingTimeInterval(30), "its times stay")
        #expect(record.positionsHeld(in: 1) < record.parts[0].count, "the hub holds text the phone dropped: a repair is due")
        #expect(RecordingDaySummary(record: record, hubOffered: true).sync == .pending, "and the day's row says so")
        #expect(record.chunks[1]?.hasAudio == true && f.exists("seg-001.m4a"), "the next chunk is untouched")

        // The marker again, or the audio again (a resend from before the erase): nothing comes back.
        #expect(try store.store(header: f.header(0, erased: true), audio: nil) == .duplicate)
        #expect(try store.store(header: f.header(0), audio: f.audio(0)) == .duplicate)
        #expect(!f.exists("seg-000.m4a"))
        #expect(await store.record(id)?.chunks[0]?.erased == true)

        // And after a relaunch.
        let relaunched = f.store()
        _ = await relaunched.recoverOnLaunch()
        #expect(await relaunched.record(id)?.chunks[0]?.erased == true)
    }

    @Test func audioNeverReplacesAnErasedChunk() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        // The erase overtook the audio, which was still waiting on the watch's backlog.
        #expect(try store.store(header: f.header(0, erased: true), audio: nil) == .stored)
        #expect(try store.store(header: f.header(0), audio: f.audio(0)) == .duplicate)
        #expect(!f.exists("seg-000.m4a"))
        let chunk = try #require(await store.record(f.id)?.chunks[0])
        #expect(chunk.erased && !chunk.hasAudio && !chunk.needsTranscription)

        // An erase the app ended in the middle of (its sidecar written, the audio not deleted yet): the marker sent again
        // finishes it.
        try f.audio(0).write(to: f.file("seg-000.m4a"))
        #expect(try store.store(header: f.header(0, erased: true), audio: nil) == .duplicate)
        #expect(!f.exists("seg-000.m4a"))
    }

    @Test func anEditNeverBringsErasedTextBack() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        let id = f.id
        _ = try store.store(header: f.header(0), audio: f.audio(0))
        _ = try store.store(header: f.header(1, at: 10), audio: f.audio(1))
        // Chunk 0's transcription began before the erase came, and saves its text after it.
        _ = try store.store(header: f.header(0, erased: true), audio: nil)
        await store.update(id) { record in
            for seq in [0, 1] {
                record.chunks[seq]?.state = .transcribed
                record.chunks[seq]?.sentences = [RecordingSentence(start: 0, end: 2, text: "the export path")]
                record.chunks[seq]?.positions = [RecordingDayPosition(part: 1, pos: seq)]
            }
            record.chunks[0]?.erased = false
            record.chunks[0]?.hasAudio = true
        }
        let record = try #require(await store.record(id))
        let erased = try #require(record.chunks[0])
        #expect(erased.erased && erased.sentences == nil && erased.positions == nil && !erased.hasAudio)
        #expect(record.chunks[1]?.sentences?.count == 1, "the rest of the edit stands")
        #expect(record.chunks[1]?.positions == [RecordingDayPosition(part: 1, pos: 1)])

        // Dropping the erased chunk doesn't take: its seq stays accounted for.
        await store.update(id) { $0.chunks[0] = nil }
        #expect(await store.record(id)?.chunks[0]?.erased == true)
    }

    @Test func audioForAChunkReportedLostIsKept() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        // The watch reported seq 0 lost while the system was still delivering its file.
        #expect(try store.store(header: f.header(0, lost: true), audio: nil) == .stored)
        #expect(await store.record(f.id)?.chunks[0]?.needsTranscription == false)
        let incoming = f.root.appendingPathComponent("late.m4a")
        try f.audio(0).write(to: incoming)
        #expect(try store.store(header: f.header(0), movingFile: incoming) == .updated)
        #expect(try Data(contentsOf: f.file("seg-000.m4a")) == f.audio(0))
        #expect(try f.sidecar(0).header.lost == false)
        let chunk = try #require(await store.record(f.id)?.chunks[0])
        #expect(!chunk.lost && chunk.hasAudio && chunk.needsTranscription)
        #expect(chunk.receivedAt == anchor.addingTimeInterval(30), "it counts as received when the marker came")

        // The loss reported again changes nothing now, and the audio survives a relaunch.
        #expect(try store.store(header: f.header(0, lost: true), audio: nil) == .duplicate)
        #expect(f.exists("seg-000.m4a"))
        let relaunched = f.store()
        _ = await relaunched.recoverOnLaunch()
        #expect(await relaunched.record(f.id)?.chunks[0]?.hasAudio == true)
    }

    @Test func aDeletedDayIsRefusedButAcknowledged() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        f.tombstones.delete(f.id)
        #expect(try store.store(header: f.header(0), audio: f.audio(0)) == .refused)
        #expect(try store.store(header: f.header(1, at: 10, erased: true), audio: nil) == .refused)
        try store.recordLost(RecordingDayLost(recording: f.id, seqs: [2]))
        #expect(!FileManager.default.fileExists(atPath: f.folder.path), "nothing of it is kept")
        #expect(await store.record(f.id) == nil)
    }

    @Test func aDeleteLandingDuringTheWriteWins() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        // The user deletes the day after the store checked its tombstone, while it writes the chunk.
        let sidecar = f.file("seg-000.json")
        f.tombstones.delete(f.id, when: { FileManager.default.fileExists(atPath: sidecar.path) })
        #expect(try store.store(header: f.header(0), audio: f.audio(0)) == .refused)
        #expect(!FileManager.default.fileExists(atPath: f.folder.path), "what it wrote is gone again")
        #expect(await store.record(f.id) == nil)
        #expect(await store.ids().isEmpty)
    }

    @Test func aPendingWriteNeverBringsADeletedDayBack() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        _ = try store.store(header: f.header(0), audio: f.audio(0))
        // RecordingLibrary.remove: the tombstone first, then the folder; day.json is still waiting to be written.
        f.tombstones.delete(f.id)
        try FileManager.default.removeItem(at: f.folder)
        await store.flush(f.id)
        await store.flushAll()
        #expect(!FileManager.default.fileExists(atPath: f.folder.path))
        // A chunk the watch sends late is acknowledged and dropped.
        #expect(try store.store(header: f.header(1, at: 10), audio: f.audio(1)) == .refused)
        #expect(!FileManager.default.fileExists(atPath: f.folder.path))
        let relaunched = f.store()
        #expect(await relaunched.recoverOnLaunch().isEmpty)
        #expect(await relaunched.ids().isEmpty)
    }

    @Test func aDayIsRebuiltFromItsSidecarsAfterACrash() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let first = f.store()
        _ = try first.store(header: f.header(0), audio: f.audio(0))
        _ = try first.store(header: f.header(1, at: 10, erased: true), audio: nil)
        _ = try first.store(header: f.header(2, at: 60, after: RecordingDayContract.Reason.interrupted), audio: f.audio(2))
        _ = try first.store(header: f.header(3, at: 3600, duration: 0, end: true, after: RecordingDayContract.Reason.paused), audio: nil)
        // Audio whose sidecar was never written (the app ended between the two): never acknowledged.
        try Data([1]).write(to: f.file("seg-009.m4a"))
        #expect(!f.exists("day.json"), "the app ended before the first coalesced write")

        // Relaunched a day later, in another time zone.
        let second = f.store(now: anchor.addingTimeInterval(86_400), zone: "UTC")
        #expect(await second.recoverOnLaunch() == [f.id])
        #expect(f.exists("day.json"), "written before recovery returns")
        #expect(!f.exists("seg-009.m4a"), "the watch sends it again")
        let record = try #require(await second.record(f.id))
        #expect(record.chunks.keys.sorted() == [0, 1, 2, 3])
        #expect(record.chunks[0]?.hasAudio == true)
        #expect(record.chunks[0]?.receivedAt == anchor.addingTimeInterval(30), "the first arrival's time, not the relaunch's")
        #expect(record.chunks[1]?.erased == true && record.chunks[1]?.hasAudio == false)
        #expect(record.chunks[2]?.after == RecordingDayContract.Reason.interrupted)
        #expect(record.expected == 4 && record.endedAt == anchor.addingTimeInterval(3600))
        #expect(record.startedAt == anchor && record.day == "2027-01-15")
        #expect(record.timeZone == "Asia/Shanghai", "the zone the day was received in")
        #expect(await second.recoverOnLaunch().isEmpty, "a current day.json is left as it is")
    }

    @Test func aDayLeftWithSidecarsOnlyIsListedBeforeRecovery() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        _ = try f.store().store(header: f.header(0), audio: f.audio(0))
        #expect(!f.exists("day.json"), "the app ended before the first coalesced write")

        // Relaunched for a background task that lists the days before the launch's recovery ran.
        let relaunched = f.store()
        #expect(await relaunched.ids() == [f.id])
        #expect(f.exists("day.json"))
        #expect(await relaunched.recoverOnLaunch() == [f.id], "still reported, so the library gets its row")
        #expect(await relaunched.recoverOnLaunch().isEmpty)
    }

    @Test func aRemovedDayIsAnnouncedAndNeverWrittenAgain() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        _ = try store.store(header: f.header(0), audio: f.audio(0))
        var changes = store.changes.makeAsyncIterator()
        // RecordingLibrary.remove: the tombstone, then the folder; then the store is told.
        f.tombstones.delete(f.id)
        try FileManager.default.removeItem(at: f.folder)
        store.didRemove(f.id)
        let changed = await changes.next()
        #expect(changed == f.id, "followers drop it at once")
        #expect(await store.record(f.id) == nil)
        #expect(await store.flush(f.id) == false)
        #expect(!FileManager.default.fileExists(atPath: f.folder.path))
    }

    @Test func sidecarsNewerThanDayJSONAreMergedIn() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let id = f.id
        let first = f.store()
        _ = try first.store(header: f.header(0), audio: f.audio(0))
        _ = try first.store(header: f.header(1, at: 10), audio: f.audio(1))
        await first.update(id) { record in
            record.chunks[1]?.state = .transcribed
            record.chunks[1]?.sentences = [RecordingSentence(start: 0, end: 2, text: "hello")]
        }
        await first.flush(id)
        // After that write and before the next: one more chunk, and an erase of chunk 0 whose audio deletion the crash
        // interrupted.
        _ = try first.store(header: f.header(2, at: 20), audio: f.audio(2))
        _ = try first.store(header: f.header(0, erased: true), audio: nil)
        try f.audio(0).write(to: f.file("seg-000.m4a"))

        let second = f.store()
        #expect(await second.recoverOnLaunch() == [id])
        let record = try #require(await second.record(id))
        #expect(record.chunks[0]?.erased == true && !f.exists("seg-000.m4a"))
        #expect(record.chunks[1]?.sentences?.first?.text == "hello", "what only day.json held is kept")
        #expect(record.chunks[2]?.hasAudio == true)
    }

    @Test func whatArrivesBeforeRecoveryNeverBuriesANewerErase() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        // Two days, each transcribed and written, then erased after that write; the app ended before the next one.
        let chunkFirst = f.id, editFirst = UUID()
        let first = f.store()
        for id in [chunkFirst, editFirst] {
            _ = try first.store(header: f.header(0, id: id), audio: f.audio(0))
            await first.update(id) { record in
                record.chunks[0]?.state = .transcribed
                record.chunks[0]?.sentences = [RecordingSentence(start: 1, end: 4, text: "the export path")]
            }
            await first.flush(id)
            _ = try first.store(header: f.header(0, erased: true, id: id), audio: nil)
        }

        // Relaunched in the background for a chunk, before the launch's recovery ran: the watch sends chunk 0 again, now
        // marked as the day's last. It merges into the erased chunk, not the older day.json's.
        let second = f.store()
        #expect(try second.store(header: f.header(0, end: true, id: chunkFirst), audio: f.audio(0)) == .updated)
        let sidecar = try f.sidecar(0)
        #expect(sidecar.header.erased && sidecar.header.end, "the chunk's durable record still says erased")
        #expect(!f.exists("seg-000.m4a"))
        let chunk = try #require(await second.record(chunkFirst)?.chunks[0])
        #expect(chunk.erased && chunk.end && chunk.sentences == nil && !chunk.hasAudio)

        // The upload's edit and write, also before the recovery: written over the erased chunk, not the older text.
        await second.update(editFirst) { record in
            record.parts = [RecordingDayPart(hubID: editFirst, part: 1, startedAt: anchor)]
        }
        await second.flush(editFirst)
        let edited = try JSONDecoder().decode(RecordingDayRecord.self, from: Data(
            contentsOf: f.root.appendingPathComponent(editFirst.uuidString).appendingPathComponent("day.json")))
        #expect(edited.chunks[0]?.erased == true && edited.chunks[0]?.sentences == nil && edited.parts.count == 1)

        // The recovery reports both, and the day that only got a chunk is on disk when it returns.
        #expect(await second.recoverOnLaunch() == [chunkFirst, editFirst].sorted { $0.uuidString < $1.uuidString })
        let written = try #require(try f.dayJSON().chunks[0])
        #expect(written.erased && written.end)
        let third = f.store()
        #expect(await third.recoverOnLaunch().isEmpty, "nothing left behind")
        #expect(await third.record(chunkFirst)?.chunks[0]?.erased == true)
        #expect(await third.record(editFirst)?.chunks[0]?.erased == true)
    }

    @Test func recoveryFollowsTheJournalNotTheClock() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let id = f.id
        let first = f.store()
        _ = try first.store(header: f.header(0), audio: f.audio(0))
        await first.update(id) { record in
            record.chunks[0]?.state = .transcribed
            record.chunks[0]?.sentences = [RecordingSentence(start: 1, end: 4, text: "the export path")]
        }
        #expect(await first.flush(id))
        #expect(!f.exists("journal.log"), "day.json holds everything written so far")

        // The clock is set back a minute. Then an erase of chunk 0, a new chunk and a loss are stored and acknowledged,
        // and the app ends before the next write of day.json: their files are dated before it.
        _ = try first.store(header: f.header(0, erased: true), audio: nil)
        _ = try first.store(header: f.header(1, at: 10), audio: f.audio(1))
        try first.recordLost(RecordingDayLost(recording: id, seqs: [5]))
        let backdated = try #require(try f.modified("day.json")).addingTimeInterval(-60)
        for name in ["seg-000.json", "seg-001.json", "lost.json"] {
            try FileManager.default.setAttributes([.modificationDate: backdated], ofItemAtPath: f.file(name).path)
        }

        let second = f.store()
        #expect(await second.recoverOnLaunch() == [id])
        let record = try #require(await second.record(id))
        let erased = try #require(record.chunks[0])
        #expect(erased.erased && erased.sentences == nil && !erased.hasAudio, "an acknowledged erase is never undone")
        #expect(record.chunks[1]?.hasAudio == true)
        #expect(record.lost == [5])
        #expect(try f.dayJSON() == record, "written before recovery returns")
        #expect(!f.exists("journal.log"))
        #expect(await f.store().recoverOnLaunch().isEmpty, "nothing left behind")
    }

    @Test func anEraseIsAcknowledgedOnlyOnceItsAudioIsGone() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        _ = try store.store(header: f.header(0), audio: f.audio(0))
        await store.update(f.id) { record in
            record.chunks[0]?.state = .transcribed
            record.chunks[0]?.sentences = [RecordingSentence(start: 1, end: 4, text: "the export path")]
        }
        // Audio the phone fails to delete, as after a file system error: in its place, a folder holding a file it may not
        // remove.
        let audio = f.file("seg-000.m4a")
        let locked = audio.appendingPathComponent("locked", isDirectory: true)
        try FileManager.default.removeItem(at: audio)
        try FileManager.default.createDirectory(at: locked, withIntermediateDirectories: true)
        try f.audio(0).write(to: locked.appendingPathComponent("audio"))
        try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: locked.path)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: locked.path) }

        // Not stored as done, so not acknowledged: the watch sends the marker again. The text goes at once all the same,
        // and the erase is on disk.
        #expect(throws: (any Error).self) { try store.store(header: f.header(0, erased: true), audio: nil) }
        #expect(f.exists("seg-000.m4a"))
        #expect(try f.sidecar(0).header.erased)
        let chunk = try #require(await store.record(f.id)?.chunks[0])
        #expect(chunk.erased && chunk.sentences == nil && !chunk.hasAudio)
        #expect(throws: (any Error).self, "sent again while the audio still can't go: still not acknowledged") {
            try store.store(header: f.header(0, erased: true), audio: nil)
        }

        // Once it can go, the marker sent again deletes it and is acknowledged.
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: locked.path)
        #expect(try store.store(header: f.header(0, erased: true), audio: nil) == .duplicate)
        #expect(!f.exists("seg-000.m4a"))
    }

    @Test func lossesAreKeptAndNeverDropHeldAudio() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        // Reported before any chunk of the day arrived: written down, waiting for the day to be known.
        try store.recordLost(RecordingDayLost(recording: f.id, seqs: [0, 1]))
        #expect(f.exists("lost.json"))
        #expect(await store.record(f.id) == nil)

        _ = try store.store(header: f.header(2, at: 20), audio: f.audio(2))
        #expect(await store.record(f.id)?.lost == [0, 1])
        // One turns up after all.
        _ = try store.store(header: f.header(1, at: 10), audio: f.audio(1))
        #expect(await store.record(f.id)?.lost == [0])

        // A lost marker for a seq never held is kept like a chunk without audio.
        #expect(try store.store(header: f.header(3, at: 30, lost: true), audio: nil) == .stored)
        #expect(await store.record(f.id)?.chunks[3]?.lost == true)
        // A loss reported for a chunk the phone holds changes nothing: the phone's copy is the chunk.
        #expect(try store.store(header: f.header(2, at: 20, lost: true), audio: nil) == .duplicate)
        try store.recordLost(RecordingDayLost(recording: f.id, seqs: [2]))
        let record = try #require(await store.record(f.id))
        #expect(record.chunks[2]?.lost == false && record.chunks[2]?.hasAudio == true && f.exists("seg-002.m4a"))
        #expect(!record.lost.contains(2))

        // After a relaunch with no day.json written.
        let relaunched = f.store()
        _ = await relaunched.recoverOnLaunch()
        let recovered = try #require(await relaunched.record(f.id))
        #expect(recovered.lost == [0] && recovered.chunks[3]?.lost == true)
        // Reported as lost time, not as time the watch still holds.
        let gaps = RecordingDaySummary(record: recovered, hubOffered: true).gaps
        #expect(gaps == [RecordingDayGap(from: 0, to: 10, reason: RecordingDayGap.Reason.lost),
                         RecordingDayGap(from: 30, to: 40, reason: RecordingDayGap.Reason.lost)])
    }

    @Test func dayJSONIsWrittenCoalescedAndOnFlush() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        for seq in 0..<3 { _ = try store.store(header: f.header(seq, at: Double(seq) * 10), audio: f.audio(seq)) }
        #expect(!f.exists("day.json"), "three chunks, no write yet")
        #expect(await store.flush(f.id), "on disk when flush returns")
        let held = await store.record(f.id)
        #expect(try f.dayJSON() == held)
        #expect(try f.dayJSON().chunks.count == 3)

        // An edit that changes nothing writes nothing.
        let written = try f.modified("day.json")
        await store.update(f.id) { _ in }
        await store.flush(f.id)
        #expect(try f.modified("day.json") == written)

        // A real one goes with the next write.
        await store.update(f.id) { $0.chunks[0]?.state = .skipped }
        #expect(try f.dayJSON().chunks[0]?.state == .waiting)
        await store.flush(f.id)
        #expect(try f.dayJSON().chunks[0]?.state == .skipped)

        // A memory warning lets go of what is on disk as it is, never of an edit that isn't written yet.
        let onDisk = try f.dayJSON()
        store.trimMemory()
        #expect(await store.record(f.id) == onDisk)
        await store.update(f.id) { $0.chunks[1]?.state = .skipped }
        store.trimMemory()
        #expect(await store.record(f.id)?.chunks[1]?.state == .skipped)
        #expect(try f.dayJSON().chunks[1]?.state == .waiting)
    }

    @Test func theCoalescedWriteComesByItself() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store(writeDelay: 0.3)
        for seq in 0..<3 { _ = try store.store(header: f.header(seq, at: Double(seq) * 10), audio: f.audio(seq)) }
        // No flush: the burst reaches day.json by the store's own write a moment later.
        let deadline = Date.now.addingTimeInterval(10)
        while (try? f.dayJSON().chunks.count) != 3, Date.now < deadline { try await Task.sleep(for: .milliseconds(20)) }
        #expect(try f.dayJSON().chunks.count == 3)
    }

    @Test func aSummaryCountsTheDayAndNamesItsGaps() throws {
        typealias Reason = RecordingDayGap.Reason
        let id = UUID()
        func chunk(_ seq: Int, at: Double, duration: Double = 10, _ state: RecordingDayChunk.State = .transcribed,
                   erased: Bool = false, end: Bool = false, after: String? = nil) -> RecordingDayChunk {
            RecordingDayChunk(seq: seq, startedAt: anchor.addingTimeInterval(at), duration: duration, after: after, erased: erased,
                              end: end, receivedAt: anchor, state: state, hasAudio: !erased && duration > 0)
        }
        var record = RecordingDayRecord(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai")
        // Seq 2 hasn't arrived; chunk 4 was erased; the user was interrupted before chunk 5 and paused after chunk 6 until
        // the day ended.
        for c in [chunk(0, at: 0), chunk(1, at: 10), chunk(3, at: 40, .waiting), chunk(4, at: 50, erased: true),
                  chunk(5, at: 120, .skipped, after: RecordingDayContract.Reason.interrupted), chunk(6, at: 130, .failed),
                  chunk(7, at: 3600, duration: 0, .waiting, end: true, after: RecordingDayContract.Reason.paused)] {
            record.put(c, recordingStartedAt: anchor)
        }

        var summary = RecordingDaySummary(record: record, hubOffered: false)
        #expect(summary.firstAt == anchor && summary.lastAt == anchor.addingTimeInterval(140))
        #expect(summary.recordedSeconds == 50 && summary.chunks == 6)
        #expect(summary.waiting == 1 && summary.skipped == 1 && summary.failed == 1)
        #expect(summary.transcribedTo == anchor.addingTimeInterval(20), "up to the chunk still waiting")
        #expect(summary.gaps == [RecordingDayGap(from: 20, to: 40, reason: Reason.pending),
                                 RecordingDayGap(from: 50, to: 60, reason: Reason.erased),
                                 RecordingDayGap(from: 60, to: 120, reason: Reason.interrupted),
                                 RecordingDayGap(from: 130, to: 140, reason: Reason.untranscribed),
                                 RecordingDayGap(from: 140, to: 3600, reason: Reason.paused)])
        #expect(summary.sync == .notOffered && !summary.complete)

        // Seq 2 reported lost, chunk 3 transcribed: everything is in, but not on the hub yet.
        record.lost = [2]
        record.chunks[3]?.state = .transcribed
        summary = RecordingDaySummary(record: record, hubOffered: true)
        #expect(summary.gaps.first == RecordingDayGap(from: 20, to: 40, reason: Reason.lost))
        #expect(summary.transcribedTo == anchor.addingTimeInterval(140))
        #expect(summary.sync == .pending && !summary.complete)

        // The upload placed every sentence (positions 0…3 of part 1) and the hub took them all.
        func place(_ seq: Int, _ count: Int, from pos: Int?) {
            record.chunks[seq]?.sentences = (0..<count).map { RecordingSentence(start: Double($0), end: Double($0) + 1, text: "\(seq).\($0)") }
            record.chunks[seq]?.positions = pos.map { first in (0..<count).map { RecordingDayPosition(part: 1, pos: first + $0) } }
        }
        place(0, 2, from: 0)
        place(1, 1, from: 2)
        place(3, 1, from: 3)
        record.parts = [RecordingDayPart(hubID: id, part: 1, startedAt: anchor, created: true, next: 4, count: 4)]
        summary = RecordingDaySummary(record: record, hubOffered: true)
        #expect(summary.sync == .synced && summary.complete && summary.hubIDs == [id])
        record.parts[0].next = 3
        #expect(RecordingDaySummary(record: record, hubOffered: true).sync == .pending)

        // Chunk 3's text came after the last append, so it has no position yet: the counters alone look done.
        place(3, 1, from: nil)
        record.parts[0].next = 3
        record.parts[0].count = 3
        summary = RecordingDaySummary(record: record, hubOffered: true)
        #expect(summary.sync == .pending && !summary.complete)

        // Placed and sent; then chunk 1 is erased after its text reached the hub: the hub holds a sentence the phone
        // dropped, and the day isn't done until the repair PUT is.
        place(3, 1, from: 3)
        record.parts[0].next = 4
        record.parts[0].count = 4
        var erase = try #require(record.chunks[1]).header(recording: id, day: record.day, recordingStartedAt: anchor)
        erase.erased = true
        record.put(try #require(record.chunks[1]).merged(with: erase), recordingStartedAt: anchor)
        #expect(record.positionsHeld(in: 1) == 3)
        summary = RecordingDaySummary(record: record, hubOffered: true)
        #expect(summary.sync == .pending && !summary.complete)
        // The repair PUT renumbered what is left and the hub stored it.
        place(3, 1, from: 2)
        record.parts[0].next = 3
        record.parts[0].count = 3
        summary = RecordingDaySummary(record: record, hubOffered: true)
        #expect(summary.sync == .synced && summary.complete)

        record.unsupportedUntilHello = true
        #expect(RecordingDaySummary(record: record, hubOffered: true).sync == .unsupported)
    }
}

/// What the Recordings page reads.
@MainActor struct RecordingDaySummariesTests {
    @Test func todayIsEachDaysOwnDateAndADeletedDayLeaves() async throws {
        // 23:30 on 2026-11-01 in New York, the night its clocks fell back (a 25-hour day): already the 2nd in UTC.
        let now = try Date.ISO8601FormatStyle().parse("2026-11-02T04:30:00Z")
        let morning = try Date.ISO8601FormatStyle().parse("2026-11-01T13:00:00Z") // 08:00 in New York
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store(zone: "America/New_York")
        let newYork = f.id, utc = UUID()
        _ = try store.store(header: f.header(0, day: "2026-11-01", start: morning), audio: f.audio(0))
        _ = try store.store(header: f.header(0, id: utc, day: "2026-11-01", start: morning), audio: f.audio(0))
        await store.update(utc) { $0.timeZone = "UTC" }

        let summaries = RecordingDaySummaries(store: store, hubOffered: { true }, now: { now })
        await summaries.refresh([newYork, utc])
        #expect(summaries.byID.count == 2)
        #expect(summaries.today?.id == newYork)
        #expect(summaries.byID[newYork]?.sync == .pending, "not on the hub yet")
        // The watch's recording is today's only on its own date.
        summaries.receive(watch: RecordingDayWatchState(recording: utc, day: "2026-11-01", state: .recording))
        #expect(summaries.watch?.recording == utc)
        #expect(summaries.today?.id == newYork)

        // A deleted day leaves with its next pass.
        f.tombstones.delete(newYork)
        await summaries.refresh([newYork])
        #expect(summaries.byID[newYork] == nil && summaries.today == nil)
    }

    @Test func todayHoldsThroughTheShortSpringDay() async throws {
        // 2026-03-08 in New York has 23 hours (02:00 skips to 03:00). At 23:59 local it is 03:59 UTC on the 9th.
        let lateEvening = try Date.ISO8601FormatStyle().parse("2026-03-09T03:59:00Z")
        let midnight = try Date.ISO8601FormatStyle().parse("2026-03-09T04:00:00Z")
        let morning = try Date.ISO8601FormatStyle().parse("2026-03-08T13:00:00Z") // 09:00 EDT
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store(zone: "America/New_York")
        _ = try store.store(header: f.header(0, day: "2026-03-08", start: morning), audio: f.audio(0))

        let evening = RecordingDaySummaries(store: store, hubOffered: { false }, now: { lateEvening })
        await evening.refresh([f.id])
        #expect(evening.today?.id == f.id)
        #expect(evening.today?.sync == .notOffered && evening.today?.firstAt == morning)
        let next = RecordingDaySummaries(store: store, hubOffered: { false }, now: { midnight })
        await next.refresh([f.id])
        #expect(next.byID[f.id] != nil && next.today == nil, "a past day stays listed, not as today")
    }

    @Test func followingTheStoreReadsOnlyWhatChangesUntilAllIsAsked() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        let earlier = UUID()
        _ = try store.store(header: f.header(0, id: earlier), audio: f.audio(0))
        let offered = OSAllocatedUnfairLock(initialState: false)
        let summaries = RecordingDaySummaries(store: store, hubOffered: { offered.withLock { $0 } }, now: { anchor },
                                              coalesce: .milliseconds(10))

        // Launched in the background for a chunk: that day is summarized, and no other day is read for it (P9).
        summaries.start()
        _ = try store.store(header: f.header(0), audio: f.audio(0))
        #expect(await eventually { summaries.byID[f.id] != nil })
        #expect(Set(summaries.byID.keys) == [f.id])

        // In front: every day.
        summaries.readAll()
        #expect(await eventually { summaries.byID.count == 2 })
        #expect(summaries.byID[earlier]?.sync == .notOffered)

        // The hub's flag changed: the days shown are summarized again.
        offered.withLock { $0 = true }
        summaries.refreshAll()
        #expect(await eventually { summaries.byID.values.allSatisfy { $0.sync == .pending } })
    }
}

/// Waits up to ten seconds for `condition` (a coalesced pass that runs by itself); whether it came to hold.
@MainActor private func eventually(_ condition: () -> Bool) async -> Bool {
    let deadline = Date.now.addingTimeInterval(10)
    while !condition() {
        guard Date.now < deadline else { return false }
        try? await Task.sleep(for: .milliseconds(10))
    }
    return true
}

#if os(iOS) || os(visionOS)
/// What WatchConnectivity's queue runs for a chunk: acknowledged only once stored. Nonisolated, like that queue.
struct RecordingDayIntakeTests {
    @Test func theIntakeAcknowledgesOnlyWhatIsStored() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let sent = Collected<RecordingDayAck>()
        let arrivals = Collected<UUID>()
        let intake = RecordingDayIntake(store: f.store(), sendAck: { sent.append($0) }, arrived: { arrivals.append($0) }, ackDelay: 3600)

        // The live path: the reply is the acknowledgement, once the chunk is on disk.
        let reply = intake.receive(frame: f.header(0).frame(audio: f.audio(0)))
        #expect(RecordingDayAck(data: reply) == RecordingDayAck(recording: f.id, acks: [[0, 0]]))
        #expect(f.exists("seg-000.json"))
        // Not a frame, or a chunk without its audio: no acknowledgement, so the watch sends it again.
        #expect(intake.receive(frame: Data([0, 0, 0, 9, 1])).isEmpty)
        #expect(intake.receive(frame: f.header(5, at: 50).frame(audio: nil)).isEmpty)

        // By file, a marker by user info, a loss report: acknowledged together, one batch per recording.
        let incoming = f.root.appendingPathComponent("chunk.m4a")
        try f.audio(2).write(to: incoming)
        #expect(intake.receive(file: incoming, header: f.header(2, at: 20)))
        #expect(intake.receive(userInfo: f.header(1, at: 10, erased: true).userInfo))
        #expect(intake.receive(userInfo: [RecordingDayContract.lostKey: RecordingDayLost(recording: f.id, seqs: [3]).data]))
        #expect(!intake.receive(userInfo: ["unrelated": true]))
        #expect(sent.all.isEmpty, "the batch waits")
        // A message's reply names what that message carried, nothing older: a batched acknowledgement of a chunk's audio
        // must never read as acknowledging an erase of it sent since.
        let next = intake.receive(frame: f.header(4, at: 40).frame(audio: f.audio(4)))
        #expect(RecordingDayAck(data: next)?.seqs == [4])
        intake.flushAcks()
        #expect(sent.all == [RecordingDayAck(recording: f.id, seqs: [1, 2, 3])])
        #expect(arrivals.all == Array(repeating: f.id, count: 5), "every change reaches the main actor's side")

        // A chunk of a deleted day is still acknowledged, so the watch lets go of it; nothing follows.
        f.tombstones.delete(f.id)
        #expect(RecordingDayAck(data: intake.receive(frame: f.header(6, at: 60).frame(audio: f.audio(6))))?.seqs == [6])
        #expect(arrivals.all.count == 5)
    }
}

/// The receiver's main-actor side: each day's library row and the background catch-up (C9).
@MainActor struct RecordingDayReceiverTests {
    @Test func aDayGetsItsRowAndAskForTheCatchUpAtMostEveryQuarterHour() async throws {
        let f = Fixture()
        defer { f.cleanUp() }
        let store = f.store()
        let library = RecordingLibrary(root: f.root)
        var clock = anchor
        let receiver = RecordingDayReceiver(store: store, library: library,
                                            summaries: RecordingDaySummaries(store: store, hubOffered: { false }), now: { clock })
        var requests = 0
        let original = RecordingDayReceiver.requestCatchUp
        RecordingDayReceiver.requestCatchUp = { requests += 1 }
        defer { RecordingDayReceiver.requestCatchUp = original }

        _ = try store.store(header: f.header(0), audio: f.audio(0))
        receiver.arrived(f.id)
        await receiver.rowsAdopted()
        let row = try #require(library.entry(f.id))
        #expect(row.isDay && row.source == .watch && row.active && row.segments.isEmpty)
        #expect(row.day == "2027-01-15" && row.timeZone == "Asia/Shanghai" && row.createdAt == anchor)
        #expect(requests == 1)

        // A chunk every ten seconds asks once per quarter hour.
        clock = anchor.addingTimeInterval(14 * 60)
        _ = try store.store(header: f.header(1, at: 10), audio: f.audio(1))
        receiver.arrived(f.id)
        #expect(requests == 1)
        clock = anchor.addingTimeInterval(15 * 60)
        _ = try store.store(header: f.header(2, at: 20, duration: 0, end: true), audio: nil)
        receiver.arrived(f.id)
        await receiver.rowsAdopted()
        #expect(requests == 2)
        #expect(library.entry(f.id)?.active == false, "the end marker closes the row")

        // A day the user deleted never gets its row back from a late chunk.
        try library.remove(f.id)
        receiver.arrived(f.id)
        await receiver.rowsAdopted()
        #expect(library.entry(f.id) == nil)
    }
}
#endif
