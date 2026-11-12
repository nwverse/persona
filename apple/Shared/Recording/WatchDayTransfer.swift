#if os(watchOS)
import Foundation
import Observation
import WatchConnectivity

/// Sends the watch's all-day chunks and control events to the phone and keeps each until the phone acknowledges it
/// (docs/assistant/day-recap.md「Watch to phone」). `RecordingDayOutbox` decides what goes and which way, and
/// `RecordingDayWatchLedger` what the watch keeps; this carries both out over WatchConnectivity and hands the phone's
/// erase and delete requests to the capture. Event driven (a library change, a reply, an acknowledgement, a file
/// transfer finishing, a reachability or Low Power change) plus one timer for the outbox's next deadline: a backoff or
/// hold ending, a reply or an acknowledgement overdue, and, while an event waits with the system as user info, the next
/// look whether it still does (the end of a user info transfer doesn't reach this yet). Nothing polls otherwise.
@Observable
final class WatchDayTransfer {
    static let shared = WatchDayTransfer()

    /// Chunks the phone has not acknowledged yet, over every day recording.
    private(set) var pending = 0
    /// The session says the phone's app can be reached for live messages right now.
    private(set) var reachable = false

    private typealias Key = RecordingDayOutbox.Key

    /// A chunk or event about to go, with what its envelope needs.
    private nonisolated struct Work {
        nonisolated enum Payload {
            case chunk(RecordingSegment)
            case event(RecordingDayEventEnvelope)
        }

        var entry: RecordingEntry
        var payload: Payload
        var item: RecordingDayOutbox.Item
    }

    /// Reads of a chunk's audio that may fail before the chunk is reported lost: a file that can't be read is a loss
    /// the watch could not avoid, but one failed read is not.
    private static let maxReadFailures = RecordingDayContract.maxAttempts
    /// The phone hears how far the watch has got (`next`, `pending`) at most this often; a new state goes at once.
    private static let progressInterval: TimeInterval = 60
    /// After a hand-off, chunks saved this soon after still go the queued way: the pause's last chunk can reach the
    /// library a moment after the pause itself, from the writer's queue.
    private static let handOffWindow: TimeInterval = 5
    /// The wake timer never fires sooner than this, so times that round to now don't spin.
    private static let minimumWake: TimeInterval = 1

    @ObservationIgnored private var started = false
    @ObservationIgnored private var outbox = RecordingDayOutbox()
    /// Chunk messages being framed or awaiting their reply, by the send they belong to: a reply's error from an older
    /// send of the same chunk (handed off as a file since) must not count against the newer one.
    @ObservationIgnored private var inFlight: [Key: Int] = [:]
    @ObservationIgnored private var generation = 0
    /// Envelope sizes, measured once per chunk from its file (a lookup, not a read), exact once framed.
    @ObservationIgnored private var frameBytes: [Key: Int] = [:]
    @ObservationIgnored private var readFailures: [Key: Int] = [:]
    @ObservationIgnored private var handOffUntil = Date.distantPast
    @ObservationIgnored private var pumpScheduled = false
    @ObservationIgnored private var wake: Task<Void, Never>?
    @ObservationIgnored private var published: RecordingDayWatchState?
    @ObservationIgnored private var publishedAt = Date.distantPast
    @ObservationIgnored private var stateSince: (state: RecordingDayWatchState.State, since: Date)?
    @ObservationIgnored private var observers: [NSObjectProtocol] = []

    private init() {}

    /// Idempotent; `WatchRecordingTransfer.start()` calls it at launch, before WatchConnectivity activates, so a request
    /// the phone queued finds its handler. The day slots of `WatchSync` it takes are this transfer's own; the recording
    /// slots stay with `WatchRecordingTransfer`, which fans their events out to `pump()`.
    func start() {
        guard !started else { return }
        started = true
        let sync = WatchSync.shared
        sync.onDayAck = { [weak self] ack in self?.received(ack) }
        sync.onDayEraseRequest = { [weak self] request in self?.eraseRequested(request) }
        sync.onDayDelete = { [weak self] delete in self?.deleteRequested(delete) }
        // Posted on a global queue; delivered on main here.
        observers.append(NotificationCenter.default.addObserver(forName: .NSProcessInfoPowerStateDidChange, object: nil,
                                                                queue: .main) { @Sendable _ in
            MainActor.assumeIsolated { WatchDayTransfer.shared.pump() }
        })
        observeLibrary()
        pump()
    }

    /// Plans and sends what is due. Coalesced to the end of the current main-actor turn, so a burst of events (a chunk
    /// saved, then the erase that follows it in the same tap) makes one pass that sees their outcome.
    func pump() {
        guard !pumpScheduled else { return }
        pumpScheduled = true
        Task { [weak self] in
            guard let self else { return }
            self.pumpScheduled = false
            self.run(handingOff: false)
        }
    }

    /// The app is about to stop running (a pause, a stop, a halt, a background refresh ending): everything
    /// unacknowledged goes to the system now, chunks as files and events as user info, messages still awaiting their
    /// reply included: a message dies with the app, a queued transfer reaches the phone anyway. The phone drops what
    /// it already holds.
    func handOff() {
        handOffUntil = Date.now.addingTimeInterval(Self.handOffWindow)
        run(handingOff: true)
    }

    /// Ends the open day recordings whose own midnight has passed and that the microphone doesn't record into, a day
    /// all-day recording is paused in included (`RecordingDayWatchLedger.closeStaleDays`), so the phone and the hub learn
    /// the day is over without waiting for the next start. Run from the background refresh after midnight, and in every
    /// pass. The `closed` events are handed off at once, since the app may not run again soon.
    func closeStaleDays() {
        guard !currentLedger().closeStaleDays(now: .now).isEmpty else { return }
        handOff()
    }

    /// The all-day state, into the watch's application context for the phone's Today row. A new state goes at once;
    /// progress alone (`next`, `pending`) at most every `progressInterval`.
    func publishState() {
        guard Self.phoneTakesDays, WCSession.isSupported(), WCSession.default.activationState == .activated else { return }
        let now = Date.now
        let state = currentState(now: now)
        if let last = published {
            let sameState = last.state == state.state && last.recording == state.recording && last.day == state.day
                && last.since == state.since
            if sameState, last == state || now.timeIntervalSince(publishedAt) < Self.progressInterval { return }
        }
        published = state
        publishedAt = now
        WatchSync.shared.publishDayWatchState(state)
    }

    /// The system finished a chunk's file transfer (`WatchSync`).
    func finished(_ header: RecordingDayHeader, failed: Bool) {
        let key = Key.chunk(header.recording, seq: header.seq)
        // Acknowledged, erased or lost since: nothing waits for it.
        guard isHeld(key) else { return }
        outbox.transferFinished(key, failed: failed, at: .now)
        // Measured again on the next pass: audio that is gone makes the chunk lost.
        if failed { frameBytes[key] = nil }
        // A failure goes again after its backoff; a delivery frees a place for the backlog's next file.
        pump()
    }

    /// The system finished an event's user info transfer. A failure goes again after its backoff; a delivery gets
    /// `fileAckTimeout` for the phone's acknowledgement. Without this report the outbox still finds the transfer gone
    /// at its next look (`RecordingDayOutbox.userInfoCheckSeconds`), only later.
    func finished(_ envelope: RecordingDayEventEnvelope, failed: Bool) {
        let key = Key.event(envelope.recording, n: envelope.event.n)
        // Acknowledged since: nothing waits for it.
        guard isHeld(key) else { return }
        outbox.transferFinished(key, failed: failed, at: .now)
        pump()
    }

    /// An acknowledgement that came by message, user info or as the reply to an event's message (`WatchSync`).
    func received(_ ack: RecordingDayAck) {
        apply(ack)
        pump()
    }

    // MARK: - A pass

    private func run(handingOff: Bool) {
        let now = Date.now
        let session = WCSession.default
        refreshReachability(session)
        let ledger = currentLedger()
        // Before the gate: days end, age out and go even while the phone doesn't take days yet.
        ledger.closeStaleDays(now: now)
        ledger.ageOut(now: now, keepDays: WatchPhoneCapabilities.keepDays())
        ledger.removeSettled(now: now)
        guard Self.phoneTakesDays, WCSession.isSupported(), session.activationState == .activated,
              session.isCompanionAppInstalled else {
            refreshPending()
            cancelWake()
            return
        }
        let work = waitingWork()
        let files = outstandingFiles(session, waiting: work)
        let queued = queuedEvents(session, waiting: work)
        keepOnly(Set(work.keys))
        let actions = outbox.plan(work.values.map(\.item), now: now, reachable: reachable,
                                  lowPower: ProcessInfo.processInfo.isLowPowerModeEnabled,
                                  suspending: handingOff || now < handOffUntil, inFlight: Set(inFlight.keys),
                                  withSystem: Set(files.keys).union(queued))
        for action in actions {
            guard let item = work[action.key] else { continue }
            perform(action, item, transfer: files[action.key], session: session, now: now)
        }
        refreshPending(work.keys.filter { !$0.isEvent }.count)
        publishState()
        scheduleWake(after: now)
    }

    /// Everything of the watch's day recordings the phone has not acknowledged: each recording's events, then its
    /// chunks. A chunk that can never go (its audio is gone, or no header describes it) becomes a `lost` event here,
    /// so the phone stops waiting for its seq.
    private func waitingWork() -> [Key: Work] {
        var work: [Key: Work] = [:]
        var lost: [UUID: [Int]] = [:]
        for entry in Self.dayEntries() where RecordingDayOutbox.isSendable(entry) {
            for event in entry.dayEvents ?? [] {
                guard let envelope = RecordingDayEventEnvelope(entry: entry, event: event) else { continue }
                let item = RecordingDayOutbox.item(envelope)
                work[item.key] = Work(entry: entry, payload: .event(envelope), item: item)
            }
            for record in entry.segments {
                let key = Key.chunk(entry.id, seq: record.index)
                guard let bytes = measure(record, of: entry, key: key),
                      let item = RecordingDayOutbox.item(record, of: entry.id, frameBytes: bytes) else {
                    lost[entry.id, default: []].append(record.index)
                    continue
                }
                work[key] = Work(entry: entry, payload: .chunk(record), item: item)
            }
        }
        for (id, seqs) in lost { loseChunks(id, seqs: seqs) }
        return work
    }

    /// A chunk's envelope size: measured from its file the first time it is seen (a file-system lookup, not a read:
    /// cheap on main), estimated when the lookup fails although the file is there. nil for a chunk that can't go.
    private func measure(_ record: RecordingSegment, of entry: RecordingEntry, key: Key) -> Int? {
        if let known = frameBytes[key] { return known }
        guard let header = RecordingDayHeader(entry: entry, chunk: record) else { return nil }
        let path = RecordingLibrary.shared.audio(entry.id, record.index).path
        if let size = (try? FileManager.default.attributesOfItem(atPath: path))?[.size] as? Int {
            let bytes = RecordingDayOutbox.frameBytes(audio: size, header: header)
            frameBytes[key] = bytes
            return bytes
        }
        guard FileManager.default.fileExists(atPath: path) else { return nil }
        return RecordingDayOutbox.estimatedFrameBytes(seconds: record.duration)
    }

    /// The waiting chunks the system still holds as a file transfer, one each. A transfer of audio nothing waits for
    /// any more (acknowledged, erased, lost, of a deleted recording) is cancelled, so erased audio doesn't reach the
    /// phone, and so is a second transfer of the same chunk.
    private func outstandingFiles(_ session: WCSession, waiting: [Key: Work]) -> [Key: WCSessionFileTransfer] {
        var files: [Key: WCSessionFileTransfer] = [:]
        for transfer in session.outstandingFileTransfers {
            guard let header = RecordingDayHeader(userInfo: transfer.file.metadata) else { continue }
            let key = Key.chunk(header.recording, seq: header.seq)
            guard waiting[key] != nil, files[key] == nil else {
                transfer.cancel()
                continue
            }
            files[key] = transfer
        }
        return files
    }

    /// The waiting events the system still holds as user info: sent so, or turned into user info by `WatchSync` when
    /// their message failed. One the phone acknowledged meanwhile is cancelled.
    private func queuedEvents(_ session: WCSession, waiting: [Key: Work]) -> Set<Key> {
        var keys = Set<Key>()
        for transfer in session.outstandingUserInfoTransfers {
            guard let data = transfer.userInfo[RecordingDayContract.eventKey] as? Data,
                  let envelope = RecordingDayEventEnvelope(data: data) else { continue }
            let key = Key.event(envelope.recording, n: envelope.event.n)
            guard waiting[key] != nil else {
                transfer.cancel()
                continue
            }
            keys.insert(key)
        }
        return keys
    }

    // MARK: - Sending

    private func perform(_ action: RecordingDayOutbox.Action, _ work: Work, transfer: WCSessionFileTransfer?,
                         session: WCSession, now: Date) {
        let key = action.key
        switch (action, work.payload) {
        case (.cancel, _):
            transfer?.cancel()
            return
        case (.message, .chunk(let record)), (.file, .chunk(let record)):
            guard let header = RecordingDayHeader(entry: work.entry, chunk: record) else {
                loseChunks(key.recording, seqs: [key.number])
                return
            }
            let url = RecordingLibrary.shared.audio(work.entry.id, record.index)
            // A message still out for this chunk is overtaken: its reply's acknowledgement still counts, its error doesn't.
            inFlight[key] = nil
            outbox.sent(action, at: now)
            if case .message = action { sendMessage(key, header: header, audio: url) } else {
                session.transferFile(url, metadata: header.userInfo)
            }
        case (.message, .event(let envelope)):
            guard WatchSync.shared.sendDayEvent(envelope) else { return }
            outbox.sent(action, at: now)
        case (.userInfo, .event(let envelope)):
            guard WatchSync.shared.sendDayEvent(envelope, queued: true) else { return }
            outbox.sent(action, at: now)
        case (.file, .event), (.userInfo, .chunk):
            // The planner never pairs these: events have no file, chunks no user info.
            return
        }
    }

    /// The live path: the envelope is the header and the audio file's bytes, read off main.
    private func sendMessage(_ key: Key, header: RecordingDayHeader, audio url: URL) {
        generation += 1
        let sending = generation
        inFlight[key] = sending
        Task.detached(priority: .utility) { [weak self] in
            let frame = WatchDayTransfer.frame(url: url, header: header)
            await self?.framed(frame, key: key, generation: sending)
        }
    }

    /// A message's payload: the header, then the chunk's audio. nil when the audio can't be read.
    nonisolated static func frame(url: URL, header: RecordingDayHeader) -> Data? {
        guard let audio = try? Data(contentsOf: url) else { return nil } // perf-ok: nonisolated, only called from Task.detached in sendMessage
        return header.frame(audio: audio)
    }

    private func framed(_ frame: Data?, key: Key, generation sending: Int) {
        guard inFlight[key] == sending else { return }
        guard isHeld(key) else {
            // Acknowledged, erased or lost while its audio was read: nothing to send.
            inFlight[key] = nil
            outbox.reset(key)
            pump()
            return
        }
        guard let frame else {
            inFlight[key] = nil
            unreadable(key)
            pump()
            return
        }
        guard RecordingDayOutbox.fitsMessage(frame.count) else {
            // Not under the live limit: with its exact size known, the next pass sends it as a file.
            inFlight[key] = nil
            frameBytes[key] = frame.count
            outbox.reset(key)
            pump()
            return
        }
        deliver(frame, key: key, generation: sending)
    }

    private func deliver(_ frame: Data, key: Key, generation sending: Int) {
        WCSession.default.sendMessageData(frame, replyHandler: { @Sendable reply in
            Task { @MainActor in WatchDayTransfer.shared.replied(reply, key: key, generation: sending) }
        }, errorHandler: { @Sendable _ in
            Task { @MainActor in WatchDayTransfer.shared.failed(key: key, generation: sending) }
        })
    }

    /// The phone answered: the live path works. Its acknowledgement may name other chunks and events too; one that
    /// doesn't name this chunk (the phone couldn't store it) is a failure for it.
    private func replied(_ reply: Data, key: Key, generation sending: Int) {
        outbox.linkAnswered()
        refreshLinkReachable()
        let current = inFlight[key] == sending
        if current { inFlight[key] = nil }
        let ack = RecordingDayAck(data: reply)
        if let ack { apply(ack) }
        let named = ack.map { $0.recording == key.recording && $0.seqs.contains(key.number) } ?? false
        if current, !named { outbox.messageFailed(key, at: .now, answered: true) }
        pump()
    }

    private func failed(key: Key, generation sending: Int) {
        guard inFlight[key] == sending else { return }
        inFlight[key] = nil
        outbox.messageFailed(key, at: .now)
        refreshLinkReachable()
        pump()
    }

    /// A chunk's audio could not be read. Gone: the next pass finds that and reports it lost. Still there: tried again
    /// after a backoff, and reported lost after `maxReadFailures` tries.
    private func unreadable(_ key: Key) {
        frameBytes[key] = nil
        let count = (readFailures[key] ?? 0) + 1
        readFailures[key] = count
        if count >= Self.maxReadFailures {
            loseChunks(key.recording, seqs: [key.number])
        } else {
            outbox.messageFailed(key, at: .now, answered: true)
        }
    }

    // MARK: - The phone's word

    /// Takes what the phone acknowledged off the watch (`RecordingDayWatchLedger.apply`), and a closed day it now has
    /// all of with it.
    private func apply(_ ack: RecordingDayAck) {
        let ledger = currentLedger()
        ledger.apply(ack)
        // Whether the watch still held them or not, nothing about these waits any more.
        forget(ack.seqs.map { Key.chunk(ack.recording, seq: $0) } + ack.eventNumbers.map { Key.event(ack.recording, n: $0) })
        ledger.removeSettled(now: .now)
    }

    /// The phone asks to erase a range of a day recording (docs/assistant/day-recap.md「Erasing」). The capture carries it
    /// out whatever it records: it registers what the writer finished first and closes the chunk being written when the
    /// range reaches into it, saves the request's id and seqs before deleting anything, and answers a repeated request
    /// the same way. nil leaves the request to the phone's resend; `WatchSync` sends the answer.
    private func eraseRequested(_ request: RecordingDayEraseRequest) -> RecordingDayErased? {
        let answer = RecordingCapture.shared.onDayEraseRequest(request)
        // Transfers of the erased chunks are cancelled, and what is left goes on, in the pass this asks for.
        pump()
        return answer
    }

    /// The phone deleted a day recording (docs/assistant/day-recap.md「Deleting」). The capture stops all-day recording when
    /// it records into it, and every chunk and the entry go with a tombstone; confirmed once nothing of it is left (also
    /// when the watch never had it). nil leaves it to the phone's resend; `WatchSync` sends the confirmation.
    private func deleteRequested(_ request: RecordingDayDelete) -> RecordingDayDeleted? {
        let answer = RecordingCapture.shared.onDayDelete(request)
        pump()
        return answer
    }

    // MARK: - Bookkeeping

    /// Nothing about these chunks and events is kept any more.
    private func forget(_ keys: [Key]) {
        outbox.acked(keys)
        for key in keys {
            inFlight[key] = nil
            frameBytes[key] = nil
            readFailures[key] = nil
        }
    }

    /// Drops what is kept about chunks that left without an acknowledgement reaching this transfer (erased, lost, their
    /// day deleted), so the per-chunk tables stay as small as the backlog.
    private func keepOnly(_ waiting: Set<Key>) {
        frameBytes = frameBytes.filter { waiting.contains($0.key) }
        readFailures = readFailures.filter { waiting.contains($0.key) }
    }

    /// Chunks that can never go become `lost` events, kept until the phone acknowledges them.
    private func loseChunks(_ id: UUID, seqs: [Int]) {
        RecordingLibrary.shared.loseDayChunks(id, seqs: seqs)
        forget(seqs.map { Key.chunk(id, seq: $0) })
    }

    /// The ledger over the shared library, with what the capture holds now. The microphone records into a recording
    /// while it records or is opening; the capture holds it while all-day recording is on in it, paused included.
    private func currentLedger() -> RecordingDayWatchLedger {
        let capture = RecordingCapture.shared
        let holding = capture.allDay || capture.state != .idle ? capture.recordingID : nil
        let live = capture.state == .recording || capture.state == .starting
        return RecordingDayWatchLedger(library: .shared, recordingInto: live ? holding : nil, holding: holding)
    }

    // MARK: - State

    /// The session's reachability, and with it whether the capture cuts short chunks for the live path (which also
    /// needs messages not to keep failing).
    private func refreshReachability(_ session: WCSession) {
        let now = WCSession.isSupported() && session.activationState == .activated && session.isReachable
        if now != reachable {
            reachable = now
            outbox.reachabilityChanged(now)
        }
        refreshLinkReachable()
    }

    private func refreshLinkReachable() {
        let live = outbox.liveUp(reachable: reachable)
        let capture = RecordingCapture.shared
        if capture.linkReachable != live { capture.linkReachable = live }
    }

    private func refreshPending(_ count: Int? = nil) {
        let value = count ?? Self.dayEntries().reduce(0) { $0 + $1.segments.count }
        if pending != value { pending = value }
    }

    private func currentState(now: Date) -> RecordingDayWatchState {
        let capture = RecordingCapture.shared
        let library = RecordingLibrary.shared
        let entry = capture.dayRecordingID.flatMap { library.entry($0) } ?? library.openDays(source: .watch).first
        let state = RecordingDayOutbox.watchState(allDay: capture.allDay,
                                                  recording: capture.state == .recording || capture.state == .starting,
                                                  interrupted: capture.interrupted, dayOpen: entry?.closedAt == nil && entry != nil,
                                                  intent: UserDefaults.standard.bool(forKey: RecordingCapture.allDayIntentKey))
        if stateSince?.state != state { stateSince = (state, now) }
        return RecordingDayWatchState(recording: entry?.id, day: entry?.day, state: state,
                                      since: state == .off ? nil : stateSince?.since, next: entry?.nextIndex, pending: pending)
    }

    // MARK: - Wakes

    /// Every library change may be something to send (a chunk saved, an event added, an erase): a pass follows it.
    /// Observation tracks one change at a time, so it is armed again with each.
    private func observeLibrary() {
        withObservationTracking {
            _ = RecordingLibrary.shared.entries
        } onChange: { @Sendable in
            Task { @MainActor in
                WatchDayTransfer.shared.observeLibrary()
                WatchDayTransfer.shared.pump()
            }
        }
    }

    private func scheduleWake(after now: Date) {
        cancelWake()
        guard let at = outbox.nextWake(after: now) else { return }
        let delay = max(Self.minimumWake, at.timeIntervalSince(now))
        wake = Task { [weak self] in
            do { try await Task.sleep(for: .seconds(delay)) } catch { return }
            self?.pump()
        }
    }

    private func cancelWake() {
        wake?.cancel()
        wake = nil
    }

    // MARK: - Helpers

    /// Sending waits for a phone that takes all-day recordings and whose user agreed (`recordingDay` ≥ 1); everything
    /// stays until then.
    private static var phoneTakesDays: Bool { UserDefaults.standard.integer(forKey: WatchSync.phoneDayKey) >= 1 }

    private static func dayEntries() -> [RecordingEntry] {
        RecordingLibrary.shared.entries.filter { $0.isDay && $0.source == .watch }
    }

    /// The watch still holds this chunk (the phone hasn't acknowledged it, and it wasn't erased or lost) or this event
    /// (not acknowledged yet).
    private func isHeld(_ key: Key) -> Bool {
        guard let entry = RecordingLibrary.shared.entry(key.recording) else { return false }
        if key.isEvent { return (entry.dayEvents ?? []).contains { $0.n == key.number } }
        return entry.segments.contains { $0.index == key.number }
    }
}
#endif
