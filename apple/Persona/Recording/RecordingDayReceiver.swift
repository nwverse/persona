#if os(iOS) || os(visionOS)
import Foundation
import os
#if os(iOS)
import UIKit
#endif

/// The phone's end of all-day recording's "watch to phone" (docs/assistant/day-recap.md). A chunk arrives on
/// WatchConnectivity's queue, `RecordingDayIntake` stores it there (`RecordingDayStore`) and only then acknowledges it;
/// this, on the main actor, then lists the day among the recordings (`RecordingLibrary.adoptDay`) and asks for the
/// background catch-up. Transcription, the upload and the summaries follow the store's `changes`. Day chunks never go
/// through `onRecording` (so never through `MeetingRecorder.tagWatchRecording`).
@MainActor final class RecordingDayReceiver {
    static let shared = RecordingDayReceiver()

    /// Submits the background catch-up (transcribe and upload while charging); replacing the pending request is cheap.
    /// Tests swap it out.
    static var requestCatchUp: () -> Void = {
        #if os(iOS)
        RecordingDayCatchUp.schedule()
        #endif
    }

    /// At most one catch-up request per this long from arriving chunks (C9): a chunk arrives every ten seconds.
    static let catchUpInterval: TimeInterval = 15 * 60

    private let store: RecordingDayStore
    private let library: RecordingLibrary
    private let summaries: RecordingDaySummaries
    private let now: () -> Date
    private var started = false
    /// The intake WatchConnectivity calls; made by `start()`.
    private(set) var intake: RecordingDayIntake?
    /// Days whose index row to bring up to date, gathered while one is being adopted.
    private var rows: Set<UUID> = []
    private var adopting: Task<Void, Never>?
    private var lastCatchUp: Date?
    private var summariesRead = false
    #if os(iOS)
    private var flushTask = UIBackgroundTaskIdentifier.invalid
    #endif

    init(store: RecordingDayStore = .shared, library: RecordingLibrary = .shared, summaries: RecordingDaySummaries = .shared,
         now: @escaping () -> Date = { .now }) {
        self.store = store
        self.library = library
        self.summaries = summaries
        self.now = now
    }

    /// From `PhoneRecordingReceiver.start()`, before `WatchSync.activate()`: the system may launch the app in the
    /// background just to deliver a chunk, so the hooks are in place before the session can call them. Idempotent; the
    /// hooks are set once and never again (nothing synchronizes them).
    func start() {
        guard !started else { return }
        started = true
        RecordingDayStore.useTombstones(of: library)
        let sync = WatchSync.shared
        let intake = RecordingDayIntake(store: store,
                                        sendAck: { @Sendable ack in sync.sendDayAck(ack) },
                                        arrived: { @Sendable id in Task { @MainActor in self.arrived(id) } })
        self.intake = intake
        sync.onDayChunk = { @Sendable frame, reply in reply(intake.receive(frame: frame)) }
        sync.onDayFile = { @Sendable header, file in intake.receive(file: file, header: header) }
        sync.onDayUserInfo = { @Sendable info in intake.receive(userInfo: info) }
        sync.onDayWatchState = { [summaries] state in summaries.receive(watch: state) }
        // Following changes only: the full read waits for the app to be in front (`readSummaries`).
        summaries.start()
        Task { await recover() }
        #if os(iOS)
        followAppState()
        #endif
    }

    /// A chunk, marker or loss report was stored for `id` (any change the receiver made).
    func arrived(_ id: UUID) {
        // The transcriber and the upload follow `RecordingDayStore.shared.changes` on their own.
        requestCatchUpIfDue()
        rows.insert(id)
        guard adopting == nil else { return }
        adopting = Task {
            while let id = rows.popFirst() { await adoptRow(id) }
            adopting = nil
        }
    }

    /// Returns once every day that arrived so far has its library row (tests).
    func rowsAdopted() async {
        while let adopting { await adopting.value }
    }

    /// A crash can leave days whose chunks were acknowledged before the library's row for them was written (C14):
    /// the store rebuilds them from their sidecars, and each day without a row gets one.
    private func recover() async {
        let changed = Set(await store.recoverOnLaunch())
        for id in await store.ids() where changed.contains(id) || library.entry(id) == nil {
            await adoptRow(id)
        }
        readSummaries()
    }

    /// Every day's summary, read once per launch. A launch in the background to take a chunk shows nothing, so it
    /// doesn't decode fourteen days for them; the read waits until the app comes to the front (P9). Days that change in
    /// the meantime get theirs from the store's `changes` (`RecordingDaySummaries.start`, which reads nothing else).
    private func readSummaries() {
        #if os(iOS)
        guard UIApplication.shared.applicationState != .background else { return }
        #endif
        guard !summariesRead else { return }
        summariesRead = true
        summaries.readAll()
    }

    /// The library's small index row for the day (kind `day`, no segments); `adoptDay` writes only when it changes and
    /// refuses a deleted day.
    private func adoptRow(_ id: UUID) async {
        guard let record = await store.record(id), let start = record.hubStartedAt else { return }
        library.adoptDay(id: id, day: record.day, timeZone: record.timeZone, createdAt: start, closed: record.endedAt != nil)
    }

    /// The catch-up is requested from here too: on a day the app is only ever woken by WatchConnectivity it never
    /// passes through the background transition that also requests it (C9).
    private func requestCatchUpIfDue() {
        let at = now()
        if let last = lastCatchUp, at >= last, at.timeIntervalSince(last) < Self.catchUpInterval { return }
        lastCatchUp = at
        Self.requestCatchUp()
    }

    #if os(iOS)
    /// - Leaving the screen: iOS can suspend the app soon after and end it later without another word, so what changed
    ///   since the last coalesced write (transcripts, the upload's state) is written first, under a short background task.
    /// - Coming to the front: the summaries a background launch left unread (`readSummaries`).
    /// - A memory warning: the store lets go of the days it holds decoded (P11).
    private func followAppState() {
        let center = NotificationCenter.default
        center.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { @Sendable _ in
            MainActor.assumeIsolated { self.flushInBackground() }
        }
        center.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { @Sendable _ in
            MainActor.assumeIsolated { self.readSummaries() }
        }
        center.addObserver(forName: UIApplication.didReceiveMemoryWarningNotification, object: nil, queue: .main) { @Sendable [store] _ in
            store.trimMemory()
        }
    }

    private func flushInBackground() {
        guard flushTask == .invalid else { return }
        flushTask = UIApplication.shared.beginBackgroundTask(withName: "recording-day-flush") { @Sendable in
            // Ended at once when time runs out; the sidecars hold every chunk whatever happens to the write.
            MainActor.assumeIsolated { self.endFlushTask() }
        }
        Task {
            await store.flushAll()
            endFlushTask()
        }
    }

    private func endFlushTask() {
        guard flushTask != .invalid else { return }
        UIApplication.shared.endBackgroundTask(flushTask)
        flushTask = .invalid
    }
    #endif
}

/// What WatchConnectivity's queue runs for an all-day chunk, synchronously: store it, then acknowledge it (C26). It never
/// touches the main actor; what follows there goes through `arrived`. A chunk that could not be stored is not
/// acknowledged, so the watch keeps it and sends it again.
nonisolated final class RecordingDayIntake: Sendable {
    private let store: RecordingDayStore
    private let sendAck: @Sendable (RecordingDayAck) -> Void
    private let arrived: @Sendable (UUID) -> Void
    private let ackDelay: TimeInterval
    private let queue = DispatchQueue(label: "persona.recording-day-acks", qos: .utility)
    private let waiting = OSAllocatedUnfairLock(initialState: Waiting())

    /// Acknowledgements of chunks that came by file or user info, per recording, until their batch goes.
    private nonisolated struct Waiting {
        var seqs: [UUID: Set<Int>] = [:]
        var scheduled = false
    }

    /// - Parameters:
    ///   - sendAck: sends one recording's acknowledgements (a message while reachable, else user info).
    ///   - arrived: a day changed (stored or updated chunk, loss report); called on this queue.
    ///   - ackDelay: how long file and user-info acknowledgements gather (a backlog arrives as a burst of files).
    init(store: RecordingDayStore, sendAck: @escaping @Sendable (RecordingDayAck) -> Void,
         arrived: @escaping @Sendable (UUID) -> Void, ackDelay: TimeInterval = 1) {
        self.store = store
        self.sendAck = sendAck
        self.arrived = arrived
        self.ackDelay = ackDelay
    }

    /// A chunk or marker by `sendMessageData`: the reply is its acknowledgement (JSON `RecordingDayAck`), empty when it
    /// was not stored. It names this seq only: an acknowledgement the watch gets in reply to a message is for what that
    /// message carried, never for an older copy of the seq (the audio, say, of a chunk the user has erased since).
    func receive(frame: Data) -> Data {
        guard let unframed = RecordingDayHeader.unframe(frame) else { return Data() }
        let header = unframed.header
        guard let result = try? store.store(header: header, audio: header.isMarker ? nil : unframed.audio) else { return Data() }
        report(header.recording, result)
        return RecordingDayAck(recording: header.recording, seqs: [header.seq]).data
    }

    /// A chunk by `transferFile`: moved into the day before this returns. True when taken care of (stored, already held,
    /// or for a deleted day), false to leave it to the watch's resend.
    func receive(file: URL, header: RecordingDayHeader) -> Bool {
        guard let result = try? store.store(header: header, movingFile: file) else { return false }
        report(header.recording, result)
        acknowledge(header.recording, [header.seq])
        return true
    }

    /// User info (or a message) carrying a header-only marker (`chunkKey`) or a loss report (`lostKey`); true when one of
    /// them was taken care of.
    func receive(userInfo info: [String: Any]) -> Bool {
        var handled = false
        // Only markers travel without a file: a header that needs audio can't be stored from here.
        if let header = RecordingDayHeader(userInfo: info), header.isMarker,
           let result = try? store.store(header: header, audio: nil) {
            report(header.recording, result)
            acknowledge(header.recording, [header.seq])
            handled = true
        }
        if let lost = RecordingDayLost(data: info[RecordingDayContract.lostKey] as? Data) {
            do {
                try store.recordLost(lost)
                arrived(lost.recording)
                acknowledge(lost.recording, lost.seqs)
                handled = true
            } catch {
                // Not written down: not acknowledged, so the watch reports it again.
            }
        }
        return handled
    }

    /// Sends the waiting acknowledgements now, one per recording.
    func flushAcks() {
        let batch = waiting.withLock { (state: inout Waiting) -> [UUID: Set<Int>] in
            defer { state = Waiting() }
            return state.seqs
        }
        for (id, seqs) in batch where !seqs.isEmpty { sendAck(RecordingDayAck(recording: id, seqs: seqs)) }
    }

    private func report(_ id: UUID, _ result: RecordingDayStore.StoreResult) {
        if result == .stored || result == .updated { arrived(id) }
    }

    private func acknowledge(_ id: UUID, _ seqs: [Int]) {
        guard !seqs.isEmpty else { return }
        let first = waiting.withLock { (state: inout Waiting) -> Bool in
            state.seqs[id, default: []].formUnion(seqs)
            defer { state.scheduled = true }
            return !state.scheduled
        }
        guard first else { return }
        queue.asyncAfter(deadline: .now() + ackDelay) { [weak self] in self?.flushAcks() }
    }
}
#endif
