#if os(watchOS)
import Foundation
import Observation
import WatchConnectivity

/// Sends the watch's all-day chunks to the phone and keeps each until the phone acknowledges it
/// (docs/assistant/day-recap.md). `RecordingDayOutbox` decides what goes and which way; this carries it out over
/// WatchConnectivity, applies acknowledgements to the library, closes days nobody records into any more, and tells the
/// phone what the watch is doing. Event driven — a saved chunk, a reply, an acknowledgement, a reachability or Low Power
/// change — plus one timer for the next backoff or hold to end, so nothing polls while the wrist is down.
@Observable
final class WatchDayTransfer {
    static let shared = WatchDayTransfer()

    /// Chunks the phone has not acknowledged yet, over every day recording.
    private(set) var pending = 0
    /// The phone's app can be reached for live messages right now.
    private(set) var reachable = false

    private typealias Key = RecordingDayOutbox.Key

    /// A record about to go, with what its header needs.
    private nonisolated struct Outgoing {
        var entry: RecordingEntry
        var record: RecordingSegment
        var marks: [RecordingDayMark]
    }

    /// Consecutive message failures after which the live path counts as down for the chunk length, although the
    /// session still says the phone is reachable (in the background that flag is optimistic).
    private static let maxMessageFailures = 3
    /// Reads of a chunk's audio that may fail before the chunk is reported lost: a file that can't be read is a loss
    /// the watch could not avoid, but one failed read is not.
    private static let maxReadFailures = RecordingDayContract.maxAttempts
    /// Delivered records are pruned, and finished days removed, at most this often.
    private static let pruneInterval: TimeInterval = 60
    /// The phone hears how far the watch has got (`next`, `pending`) at most this often; a new state goes at once.
    private static let progressInterval: TimeInterval = 60
    /// After a hand-off, chunks saved this soon after still go the queued way: the pause's last chunk can reach the
    /// library a moment after the pause itself, from the writer's queue.
    private static let handOffWindow: TimeInterval = 5
    /// The wake timer never fires sooner than this, so times that round to now don't spin.
    private static let minimumWake: TimeInterval = 1

    @ObservationIgnored private var started = false
    @ObservationIgnored private var outbox = RecordingDayOutbox()
    /// Messages being framed or awaiting their reply, by the send they belong to: a reply's error from an older send of
    /// the same chunk (handed off as a file, or erased since) must not count against the newer one.
    @ObservationIgnored private var inFlight: [Key: Int] = [:]
    @ObservationIgnored private var generation = 0
    /// Chunks whose erased marker went out, any way: what was sent before for them no longer counts.
    @ObservationIgnored private var erasedSent: Set<Key> = []
    /// Chunks whose erased marker the system delivered as user info: the phone's app gets it whatever else happens.
    /// Only then does an erased chunk take an acknowledgement that isn't the reply to its own erased marker; before, a
    /// seq-only acknowledgement may be for the audio sent before the erase, and a queued marker can still fail.
    @ObservationIgnored private var erasedDelivered: Set<Key> = []
    /// The marks each chunk's last header carried; its acknowledgement takes exactly those off the watch.
    @ObservationIgnored private var sentMarks: [Key: [RecordingDayMark]] = [:]
    /// Frame sizes: measured from the file once per chunk (a lookup, not a read), exact once framed.
    @ObservationIgnored private var frameBytes: [Key: Int] = [:]
    @ObservationIgnored private var readFailures: [Key: Int] = [:]
    @ObservationIgnored private var messageFailures = 0
    @ObservationIgnored private var handOffUntil = Date.distantPast
    @ObservationIgnored private var pumpScheduled = false
    @ObservationIgnored private var wake: Task<Void, Never>?
    @ObservationIgnored private var lastPrune = Date.distantPast
    @ObservationIgnored private var published: RecordingDayWatchState?
    @ObservationIgnored private var publishedAt = Date.distantPast
    @ObservationIgnored private var stateSince: (state: RecordingDayWatchState.State, since: Date)?
    @ObservationIgnored private var observers: [NSObjectProtocol] = []

    private init() {}

    /// Idempotent; `WatchRecordingTransfer.start()` calls it at launch. The acknowledgement slot is this transfer's own:
    /// the existing recording slots stay with `WatchRecordingTransfer`, which fans their events out to `pump()`.
    func start() {
        guard !started else { return }
        started = true
        WatchSync.shared.onDayAck = { [weak self] ack in self?.received(ack) }
        // Posted on a global queue; delivered on main here.
        observers.append(NotificationCenter.default.addObserver(forName: .NSProcessInfoPowerStateDidChange, object: nil,
                                                                queue: .main) { @Sendable _ in
            MainActor.assumeIsolated { WatchDayTransfer.shared.pump() }
        })
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

    /// The app is about to stop running (a pause, a stop, a halt): every unacknowledged chunk goes to the system now,
    /// audio as a file and markers as user info, messages still awaiting their reply included — a message dies with
    /// the app, a queued transfer reaches the phone anyway. The phone drops what it already holds.
    func handOff() {
        handOffUntil = Date.now.addingTimeInterval(Self.handOffWindow)
        run(handingOff: true)
    }

    /// Closes the open day recordings of past days that nothing records into any more, a day all-day recording is paused
    /// in included: each gets its end marker at the midnight after its day (`RecordingDayOutbox.closingTime`), so the
    /// phone and the hub learn the day is over without waiting for the next start or Continue. Run from the background
    /// refresh after midnight, and before every pass. The end markers are handed off at once, since the app may not run
    /// again soon.
    func closeStaleDays() {
        guard closeExpiredDays(now: .now) else { return }
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

    /// The system finished a chunk's file or a marker's user info transfer (`WatchSync`).
    func finished(_ header: RecordingDayHeader, failed: Bool) {
        let key = Key(recording: header.recording, seq: header.seq)
        // The chunk was acknowledged or pruned since: nothing waits for it.
        guard let record = record(key), !record.delivered else { return }
        guard RecordingDayOutbox.isCurrent(header, for: record) else {
            // Sent before the chunk was erased or lost: it says nothing about the marker waiting now, but held it back
            // while the system had it.
            pump()
            return
        }
        outbox.finished(key, failed: failed, at: .now)
        if failed {
            // Measured again on the next pass: audio that is gone turns the chunk into a lost marker.
            frameBytes[key] = nil
        } else if header.erased {
            // The erase reaches the phone's app now: any acknowledgement of the seq may clear it from here on.
            erasedDelivered.insert(key)
        }
        // A failure goes again after its backoff; a delivery frees a place for the backlog's next file.
        pump()
    }

    /// An acknowledgement that came by message or user info (a chunk sent as a file or a marker).
    func received(_ ack: RecordingDayAck) {
        apply(ack)
        pump()
    }

    // MARK: - A pass

    private func run(handingOff: Bool) {
        let now = Date.now
        let session = WCSession.default
        refreshReachability(session)
        // Before the guards: a day that ended closes even while the phone doesn't take days yet.
        closeExpiredDays(now: now)
        guard Self.phoneTakesDays, WCSession.isSupported(), session.activationState == .activated,
              session.isCompanionAppInstalled else {
            refreshPending()
            cancelWake()
            return
        }
        pruneIfDue(now)
        measureNewAudio()
        let outstanding = outstandingKeys(session)
        let waiting = waitingRecords()
        keepOnly(Set(waiting.keys))
        // An erased chunk whose audio went out before the erase: what was sent no longer counts; its marker goes now.
        for (key, outgoing) in waiting where outgoing.record.erased == true && !erasedSent.contains(key) { supersede(key) }
        let items = waiting.map { key, outgoing in
            RecordingDayOutbox.item(outgoing.record, of: outgoing.entry, frameBytes: frameBytes[key])
        }
        let actions = outbox.plan(items, now: now, reachable: reachable, lowPower: ProcessInfo.processInfo.isLowPowerModeEnabled,
                                  suspending: handingOff || now < handOffUntil, inFlight: Set(inFlight.keys),
                                  outstandingFiles: outstanding)
        for action in actions {
            guard let outgoing = waiting[action.key] else { continue }
            send(action, outgoing, session: session, now: now)
        }
        refreshPending(waiting.count)
        publishState()
        scheduleWake(after: now)
    }

    /// Every unacknowledged record of the watch's day recordings, with the marks each carries.
    private func waitingRecords() -> [Key: Outgoing] {
        var waiting: [Key: Outgoing] = [:]
        for entry in Self.dayEntries() {
            let marks = RecordingDayOutbox.carriedMarks(entry)
            for record in entry.segments where !record.delivered {
                waiting[Key(recording: entry.id, seq: record.index)] = Outgoing(entry: entry, record: record,
                                                                                marks: marks[record.index] ?? [])
            }
        }
        return waiting
    }

    /// Sizes the audio of chunks seen for the first time (a file-system lookup per chunk, not a read: cheap on main),
    /// and turns a chunk whose audio is gone into a `lost` marker. Its record stays until the phone acknowledges that,
    /// so the phone stops waiting for the seq and reports the time as a gap.
    private func measureNewAudio() {
        let library = RecordingLibrary.shared
        var lost: [UUID: Set<Int>] = [:]
        for entry in Self.dayEntries() {
            for record in entry.segments where !record.delivered && !record.isDayMarker {
                let key = Key(recording: entry.id, seq: record.index)
                guard frameBytes[key] == nil else { continue }
                let path = library.audio(entry.id, record.index).path
                if let size = (try? FileManager.default.attributesOfItem(atPath: path))?[.size] as? Int {
                    frameBytes[key] = size + RecordingDayOutbox.frameOverheadBytes
                } else if !FileManager.default.fileExists(atPath: path) {
                    lost[entry.id, default: []].insert(record.index)
                }
            }
        }
        for (id, seqs) in lost { markLost(id, seqs: seqs) }
    }

    /// The waiting chunks the system still holds as a file or user info transfer. Audio no longer needed is cancelled:
    /// the phone acknowledged its chunk, the chunk is gone, or it became a marker after its audio was queued — erased
    /// audio must not reach the phone, and a lost chunk's marker must not wait behind audio that can't go. Markers are
    /// never cancelled: a few hundred bytes, and an erased one must reach the phone whatever acknowledged its seq
    /// meanwhile. A marker sent before its chunk changed (lost, then erased) doesn't hold back the one it is now. An erased
    /// marker still queued from before a relaunch counts as sent, not as delivered.
    private func outstandingKeys(_ session: WCSession) -> Set<Key> {
        var records: [Key: RecordingSegment] = [:]
        for entry in Self.dayEntries() {
            for record in entry.segments { records[Key(recording: entry.id, seq: record.index)] = record }
        }
        var keys = Set<Key>()
        for transfer in session.outstandingFileTransfers {
            guard let header = RecordingDayHeader(userInfo: transfer.file.metadata) else { continue }
            let key = Key(recording: header.recording, seq: header.seq)
            guard let record = records[key], !record.delivered, !record.isDayMarker else {
                transfer.cancel()
                continue
            }
            keys.insert(key)
        }
        for transfer in session.outstandingUserInfoTransfers {
            guard let header = RecordingDayHeader(userInfo: transfer.userInfo) else { continue }
            let key = Key(recording: header.recording, seq: header.seq)
            guard let record = records[key], !record.delivered, RecordingDayOutbox.isCurrent(header, for: record) else { continue }
            if header.erased { erasedSent.insert(key) }
            keys.insert(key)
        }
        return keys
    }

    // MARK: - Sending

    private func send(_ action: RecordingDayOutbox.Action, _ outgoing: Outgoing, session: WCSession, now: Date) {
        let key = action.key
        let header = RecordingDayOutbox.header(for: outgoing.record, in: outgoing.entry, marks: outgoing.marks)
        let url = RecordingLibrary.shared.audio(outgoing.entry.id, outgoing.record.index)
        // A message still out for this chunk is overtaken: its reply's acknowledgement still counts, its error doesn't.
        inFlight[key] = nil
        outbox.sent(action, at: now)
        sentMarks[key] = outgoing.marks
        if header.erased { erasedSent.insert(key) }
        switch action {
        case .message: sendMessage(key, header: header, audio: url)
        case .file: session.transferFile(url, metadata: header.userInfo)
        case .userInfo: session.transferUserInfo(header.userInfo)
        }
    }

    /// The live path: the frame is the header and the audio file's bytes, read off main.
    private func sendMessage(_ key: Key, header: RecordingDayHeader, audio url: URL) {
        generation += 1
        let sending = generation
        inFlight[key] = sending
        guard !header.isMarker else {
            deliver(header.frame(audio: nil), key: key, generation: sending, erased: header.erased)
            return
        }
        Task.detached(priority: .utility) { [weak self] in
            let frame = WatchDayTransfer.frame(url: url, header: header)
            await self?.framed(frame, key: key, generation: sending, header: header)
        }
    }

    /// A message's payload: the header, then the chunk's audio. nil when the audio can't be read.
    nonisolated static func frame(url: URL, header: RecordingDayHeader) -> Data? {
        guard let audio = try? Data(contentsOf: url) else { return nil } // perf-ok: nonisolated, only called from Task.detached in sendMessage
        return header.frame(audio: audio)
    }

    private func framed(_ frame: Data?, key: Key, generation sending: Int, header: RecordingDayHeader) {
        guard inFlight[key] == sending else { return }
        guard let record = record(key), !record.delivered, RecordingDayOutbox.isCurrent(header, for: record) else {
            // Acknowledged, erased or lost while its audio was read: the next pass sends what it is now.
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
        guard frame.count <= RecordingDayContract.maxMessageBytes else {
            // Bigger than a message may be: with its exact size known, the next pass sends it as a file.
            inFlight[key] = nil
            frameBytes[key] = frame.count
            outbox.reset(key)
            pump()
            return
        }
        deliver(frame, key: key, generation: sending, erased: header.erased)
    }

    /// `erased`: the frame is the chunk's erased marker, which the reply then confirms.
    private func deliver(_ frame: Data, key: Key, generation sending: Int, erased: Bool) {
        WCSession.default.sendMessageData(frame, replyHandler: { @Sendable reply in
            Task { @MainActor in WatchDayTransfer.shared.replied(reply, key: key, generation: sending, erased: erased) }
        }, errorHandler: { @Sendable _ in
            Task { @MainActor in WatchDayTransfer.shared.failed(key: key, generation: sending) }
        })
    }

    /// The phone answered: the live path works. Its acknowledgement may name other chunks too; one that doesn't name
    /// this chunk (the phone couldn't store it) is a failure for it.
    private func replied(_ reply: Data, key: Key, generation sending: Int, erased: Bool) {
        messageFailures = 0
        refreshLinkReachable()
        let current = inFlight[key] == sending
        if current { inFlight[key] = nil }
        let accepted = RecordingDayAck(data: reply).map { apply($0, confirmingErase: erased ? key : nil) } ?? []
        if current, !accepted.contains(key) { outbox.finished(key, failed: true, at: .now) }
        pump()
    }

    private func failed(key: Key, generation sending: Int) {
        messageFailures += 1
        refreshLinkReachable()
        guard inFlight[key] == sending else { return }
        inFlight[key] = nil
        outbox.finished(key, failed: true, at: .now)
        pump()
    }

    /// A chunk's audio could not be read. Gone: the next pass finds that and reports it lost. Still there: tried again
    /// after a backoff, and reported lost after `maxReadFailures` tries.
    private func unreadable(_ key: Key) {
        frameBytes[key] = nil
        let count = (readFailures[key] ?? 0) + 1
        readFailures[key] = count
        if count >= Self.maxReadFailures {
            markLost(key.recording, seqs: [key.seq])
        } else {
            outbox.finished(key, failed: true, at: .now)
        }
    }

    // MARK: - Acknowledgements, losses, pruning

    /// Marks what the phone acknowledged as delivered (its audio goes, its record stays a while for an erase) and takes
    /// the marks those chunks carried off the watch. `confirmingErase`: the chunk whose erased marker this is the reply
    /// to. Returns the chunks it accepted.
    @discardableResult private func apply(_ ack: RecordingDayAck, confirmingErase: Key? = nil) -> Set<Key> {
        let library = RecordingLibrary.shared
        let seqs = ack.seqs
        guard let entry = library.entry(ack.recording), entry.isDay, entry.source == .watch else {
            forget(seqs.map { Key(recording: ack.recording, seq: $0) })
            return []
        }
        let records = Dictionary(entry.segments.map { ($0.index, $0) }, uniquingKeysWith: { first, _ in first })
        let carried = RecordingDayOutbox.carriedMarks(entry)
        var accepted: [Key] = []
        var marks: [RecordingDayMark] = []
        for seq in seqs {
            let key = Key(recording: entry.id, seq: seq)
            guard let record = records[seq] else {
                forget([key])
                continue
            }
            // An erased chunk is acknowledged by its erased marker only: anything else may acknowledge the audio sent
            // before the erase.
            guard RecordingDayOutbox.acceptsAck(of: record, confirmsErase: key == confirmingErase,
                                                eraseDelivered: erasedDelivered.contains(key)) else { continue }
            accepted.append(key)
            marks += sentMarks[key] ?? carried[seq] ?? []
        }
        guard !accepted.isEmpty else { return [] }
        library.markDelivered(entry.id, seqs: accepted.map(\.seq), removingMarks: marks)
        forget(accepted)
        pruneIfDue(.now)
        return Set(accepted)
    }

    /// Nothing about these chunks is kept any more.
    private func forget(_ keys: [Key]) {
        outbox.acked(keys)
        for key in keys {
            inFlight[key] = nil
            sentMarks[key] = nil
            erasedSent.remove(key)
            erasedDelivered.remove(key)
            frameBytes[key] = nil
            readFailures[key] = nil
        }
    }

    /// Drops what is kept about chunks that left without an acknowledgement reaching this transfer (their day was
    /// removed, or their record pruned), so the per-chunk tables stay as small as the backlog.
    private func keepOnly(_ waiting: Set<Key>) {
        sentMarks = sentMarks.filter { waiting.contains($0.key) }
        frameBytes = frameBytes.filter { waiting.contains($0.key) }
        readFailures = readFailures.filter { waiting.contains($0.key) }
        erasedSent.formIntersection(waiting)
        erasedDelivered.formIntersection(waiting)
    }

    /// What was sent for a chunk no longer counts: it changed (erased, lost) since.
    private func supersede(_ key: Key) {
        inFlight[key] = nil
        sentMarks[key] = nil
        outbox.reset(key)
    }

    /// Chunks whose audio is gone become `lost` markers, kept until the phone acknowledges them.
    private func markLost(_ id: UUID, seqs: Set<Int>) {
        RecordingLibrary.shared.change(id) { entry in
            for position in entry.segments.indices where seqs.contains(entry.segments[position].index) {
                guard !entry.segments[position].delivered, entry.segments[position].erased != true else { continue }
                entry.segments[position].lost = true
            }
        }
        for seq in seqs {
            let key = Key(recording: id, seq: seq)
            supersede(key)
            frameBytes[key] = nil
            readFailures[key] = nil
        }
    }

    /// Drops records the phone has held longer than an erase could reach back (`keepDeliveredSeconds`), and removes a
    /// closed day once nothing of it is left.
    private func pruneIfDue(_ now: Date) {
        guard now.timeIntervalSince(lastPrune) >= Self.pruneInterval || now < lastPrune else { return }
        lastPrune = now
        let library = RecordingLibrary.shared
        let cutoff = now.addingTimeInterval(-RecordingDayContract.keepDeliveredSeconds)
        for entry in Self.dayEntries() {
            library.pruneDelivered(entry.id, olderThan: cutoff)
            guard let current = library.entry(entry.id), RecordingDayOutbox.isFinished(current) else { continue }
            try? library.remove(entry.id)
        }
    }

    /// Ends the open day recordings of past days: a Stop in the evening, or a pause past midnight, must not leave the day
    /// open until the microphone opens again, which the background refresh after midnight can't wait for. One the
    /// microphone records into is the capture's to roll over at the midnight, and one whose audio still waits for its
    /// save waits for that: its end marker would take that chunk's seq (`RecordingDayOutbox.CaptureHold`). True when one
    /// was closed.
    @discardableResult private func closeExpiredDays(now: Date) -> Bool {
        let library = RecordingLibrary.shared
        var closed = false
        for entry in library.openDays(source: .watch) {
            guard let end = RecordingDayOutbox.closingTime(of: entry, now: now, zone: .autoupdatingCurrent,
                                                            capture: Self.captureHold(on: entry)) else { continue }
            library.closeDay(entry.id, at: end, after: entry.gapReason ?? RecordingDayContract.Reason.stopped)
            closed = true
        }
        return closed
    }

    /// What the capture still does with a day recording. Paused, the writer's next file has the seq the end marker would
    /// take, so a file there is audio that reached it after the pause's cut and isn't saved yet (a lookup, not a read).
    private static func captureHold(on entry: RecordingEntry) -> RecordingDayOutbox.CaptureHold {
        let capture = RecordingCapture.shared
        if capture.holdsAudio(of: entry.id) { return .savingAudio }
        guard capture.recordingID == entry.id, capture.allDay || capture.state != .idle else { return .none }
        guard capture.allDay, capture.state == .paused else { return .recording }
        let next = RecordingLibrary.shared.audio(entry.id, entry.nextIndex).path
        return .paused(unsavedAudio: FileManager.default.fileExists(atPath: next))
    }

    // MARK: - State

    /// The live path counts as up while the session says the phone is reachable and messages haven't kept failing; the
    /// capture cuts short chunks only then.
    private func refreshReachability(_ session: WCSession) {
        let now = WCSession.isSupported() && session.activationState == .activated && session.isReachable
        if now != reachable {
            reachable = now
            messageFailures = 0
            outbox.reachabilityChanged(now)
        }
        refreshLinkReachable()
    }

    private func refreshLinkReachable() {
        let live = reachable && messageFailures < Self.maxMessageFailures
        let capture = RecordingCapture.shared
        if capture.linkReachable != live { capture.linkReachable = live }
    }

    private func refreshPending(_ count: Int? = nil) {
        let value = count ?? Self.dayEntries().reduce(0) { $0 + $1.segments.filter { !$0.delivered }.count }
        if pending != value { pending = value }
    }

    private func currentState(now: Date) -> RecordingDayWatchState {
        let capture = RecordingCapture.shared
        let library = RecordingLibrary.shared
        let entry = capture.dayRecordingID.flatMap { library.entry($0) } ?? library.openDays(source: .watch).first
        let state = RecordingDayOutbox.watchState(allDay: capture.allDay,
                                                  recording: capture.state == .recording || capture.state == .starting,
                                                  interrupted: capture.interrupted, dayOpen: entry?.active == true,
                                                  intent: UserDefaults.standard.bool(forKey: RecordingCapture.allDayIntentKey))
        if stateSince?.state != state { stateSince = (state, now) }
        return RecordingDayWatchState(recording: entry?.id, day: entry?.day, state: state,
                                      since: state == .off ? nil : stateSince?.since, next: entry?.nextIndex, pending: pending)
    }

    // MARK: - Timer

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

    /// Sending waits for a phone that takes all-day recordings (`recordingDay` ≥ 1); the chunks stay until then.
    private static var phoneTakesDays: Bool { UserDefaults.standard.integer(forKey: WatchSync.phoneDayKey) >= 1 }

    private static func dayEntries() -> [RecordingEntry] {
        RecordingLibrary.shared.entries.filter { $0.isDay && $0.source == .watch }
    }

    private func record(_ key: Key) -> RecordingSegment? {
        RecordingLibrary.shared.entry(key.recording)?.segments.first { $0.index == key.seq }
    }
}
#endif
