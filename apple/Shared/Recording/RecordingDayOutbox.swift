import Foundation

/// How the watch's all-day chunks reach the phone (docs/assistant/day-recap.md): which unacknowledged chunk goes now,
/// and which way — a live message, a file the system delivers, or user info for a header-only marker. Pure and
/// `nonisolated`, so the rules are unit tested on the phone's test target while the watch's `WatchDayTransfer` only
/// carries them out over WatchConnectivity. Nothing here drops a chunk by age: only an acknowledgement takes one out.
///
/// The rules, in the order they apply:
/// - Suspending (the app is about to stop running: a pause, a stop, a halt): every chunk not already with the system
///   goes as a file (a marker as user info), messages still awaiting their reply included, since a message dies with
///   the app and a queued transfer doesn't. The phone drops what it already holds.
/// - Reachable: the newest audio, up to `backlogFileSeconds` of it, goes by message, oldest first, at most
///   `maxInFlightMessages` at once; anything older (a backlog) and any frame over `maxMessageBytes` goes as a file, at
///   most `maxOutstandingFiles` with the system at once. In Low Power Mode the messages wait until the oldest of them
///   ended `lowPowerHoldSeconds` ago, then all go together.
/// - Not reachable: audio waits until the oldest chunk ended `backlogFileSeconds` ago, then all of it goes as files
///   (the system delivers them once the phone is back); markers go as user info at once.
/// - A message without an acknowledgement after `ackTimeout`, or with an error, goes again after a backoff
///   (`backoffFirst` doubling to `backoffMax`). A file goes again only after the system reports it failed, or when no
///   acknowledgement came `fileAckTimeout` after it was delivered. Becoming reachable again retries at once.
nonisolated struct RecordingDayOutbox {
    /// One chunk: its day recording and its seq.
    nonisolated struct Key: Hashable, Sendable {
        var recording: UUID
        var seq: Int
    }

    /// A chunk the phone has not acknowledged yet.
    nonisolated struct Item: Sendable, Equatable {
        var key: Key
        /// When its audio ended (a marker: its own time). The Low Power hold and the backlog wait go by it.
        var finishedAt: Date
        var startedAt: Date
        /// Seconds of audio it carries; none for a marker.
        var seconds: Double
        /// The size of its `sendMessageData` frame: header and audio.
        var frameBytes: Int
        /// Header only (erased, lost, the end marker): there is no file to transfer.
        var isMarker: Bool
    }

    nonisolated enum Action: Sendable, Equatable {
        /// `sendMessageData` with the frame; the reply carries the acknowledgement.
        case message(Key)
        /// `transferFile` with the header as metadata.
        case file(Key)
        /// `transferUserInfo` with the header, for a marker.
        case userInfo(Key)

        var key: Key {
            switch self {
            case .message(let key), .file(let key), .userInfo(let key): key
            }
        }
    }

    /// Files and user info handed to the system at once: a long backlog doesn't fill its queue, and more follow as these
    /// finish. A hand-off before suspending ignores it, since nothing would follow until the app runs again.
    static let maxOutstandingFiles = 50
    /// Low Power Mode on the live path: messages wait until the oldest one's end is this old, so its first sample is at
    /// most `lowPowerTransferBatchSeconds` (the contract's five minutes) behind when it leaves.
    static let lowPowerHoldSeconds = RecordingAudioSpec.lowPowerTransferBatchSeconds - RecordingDayContract.liveChunkSeconds
    /// What a frame adds to the audio file: the length prefix and the header JSON, with room for a few marks.
    static let frameOverheadBytes = 1_024
    /// The most marks one header carries: what `RecordingDayHeader.valid` accepts. The phone refuses a header with more,
    /// and never acknowledges it, so a chunk carrying more could never leave the watch.
    static let maxMarksPerHeader = 100

    /// How a chunk last went: by message, or queued with the system (a file or user info).
    private nonisolated enum Route: Sendable, Equatable { case message, queued }
    /// `delivered`: the system says a queued transfer reached the phone; its acknowledgement may still be on its way.
    private nonisolated enum Progress: Sendable, Equatable { case awaiting, delivered(Date), failed }
    private nonisolated struct Send: Sendable, Equatable {
        var route: Route
        var at: Date
        var progress: Progress
    }

    private var sends: [Key: Send] = [:]
    private var failures: [Key: Int] = [:]
    private var retryAt: [Key: Date] = [:]
    /// Low Power Mode: chunks that ended by then are released from the hold.
    private var lowPowerReleased: Date?
    /// The last plan held chunks until then (the Low Power hold, or the backlog wait while unreachable).
    private var heldUntil: Date?

    // MARK: - Planning

    /// What to send now. `items`: every chunk the phone has not acknowledged, in any order. `inFlight`: chunks whose
    /// message is being framed or awaits its reply. `outstandingFiles`: chunks the system still holds as a file or user
    /// info transfer (it keeps them across launches, this doesn't). Actions come oldest first.
    mutating func plan(_ items: [Item], now: Date, reachable: Bool, lowPower: Bool, suspending: Bool,
                       inFlight: Set<Key>, outstandingFiles: Set<Key>) -> [Action] {
        let offered = Set(items.map(\.key))
        for key in Set(sends.keys).union(failures.keys).union(retryAt.keys) where !offered.contains(key) { forget(key) }
        expire(now: now, outstanding: outstandingFiles)
        heldUntil = nil

        let ordered = items.sorted(by: Self.oldestFirst)
        let queued = Set(sends.compactMap { $0.value.route == .queued && $0.value.progress != .failed ? $0.key : nil })
        let withSystem = outstandingFiles.union(queued)
        if suspending {
            return ordered.filter { !withSystem.contains($0.key) }.map { $0.isMarker ? .userInfo($0.key) : .file($0.key) }
        }
        let awaiting = Set(sends.compactMap { $0.value.route == .message && $0.value.progress == .awaiting ? $0.key : nil })
        // A message overdue for its reply stops counting as in flight: it goes again once its backoff ends.
        let flying = inFlight.filter { sends[$0]?.progress != .failed }.union(awaiting).intersection(offered)
        let busy = withSystem.union(flying)
        let ready = ordered.filter { !busy.contains($0.key) && (retryAt[$0.key] ?? .distantPast) <= now }
        let messageSlots = RecordingDayContract.maxInFlightMessages - flying.count
        let fileSlots = Self.maxOutstandingFiles - outstandingFiles.count
        guard reachable else { return planUnreachable(ordered, ready: ready, busy: busy, fileSlots: fileSlots, now: now) }
        let lane = Self.messageLane(ordered, withSystem: withSystem)
        let released = lowPower ? lowPowerRelease(ordered, lane: lane, busy: busy, now: now) : .distantFuture
        return Self.planReachable(ready, lane: lane, releasedThrough: released, messageSlots: messageSlots, fileSlots: fileSlots)
    }

    /// The live path: markers and the newest audio by message, the backlog and oversize frames as files.
    private static func planReachable(_ ready: [Item], lane: Set<Key>, releasedThrough: Date, messageSlots: Int,
                                      fileSlots: Int) -> [Action] {
        var messages = messageSlots
        var files = fileSlots
        var actions: [Action] = []
        for item in ready {
            if item.isMarker {
                // Markers are a few hundred bytes and are never held: an erase should leave the phone at once.
                if item.frameBytes > RecordingDayContract.maxMessageBytes {
                    actions.append(.userInfo(item.key))
                } else if messages > 0 {
                    actions.append(.message(item.key))
                    messages -= 1
                }
            } else if !lane.contains(item.key) {
                if files > 0 {
                    actions.append(.file(item.key))
                    files -= 1
                }
            } else if item.finishedAt <= releasedThrough, messages > 0 {
                actions.append(.message(item.key))
                messages -= 1
            }
        }
        return actions
    }

    /// Out of reach: audio waits until the oldest chunk is `backlogFileSeconds` old, then all of it goes as files the
    /// system delivers once the phone is back; markers go as user info at once.
    private mutating func planUnreachable(_ ordered: [Item], ready: [Item], busy: Set<Key>, fileSlots: Int, now: Date) -> [Action] {
        var released = false
        if let oldest = ordered.filter({ !$0.isMarker && !busy.contains($0.key) }).map(\.finishedAt).min() {
            let due = oldest.addingTimeInterval(RecordingDayContract.backlogFileSeconds)
            if now >= due { released = true } else { hold(until: due) }
        }
        var files = fileSlots
        var actions: [Action] = []
        for item in ready {
            if item.isMarker {
                actions.append(.userInfo(item.key))
            } else if released, files > 0 {
                actions.append(.file(item.key))
                files -= 1
            }
        }
        return actions
    }

    /// The newest audio not already with the system, up to `backlogFileSeconds` of it: the live path drains it by
    /// message, oldest first. Older audio waiting is a backlog for files, and a chunk finished while it drains still
    /// goes at once.
    private static func messageLane(_ ordered: [Item], withSystem: Set<Key>) -> Set<Key> {
        var lane = Set<Key>()
        var seconds = 0.0
        for item in ordered.reversed() where !item.isMarker && !withSystem.contains(item.key)
            && item.frameBytes <= RecordingDayContract.maxMessageBytes {
            seconds += max(0, item.seconds)
            if seconds > RecordingDayContract.backlogFileSeconds { break }
            lane.insert(item.key)
        }
        return lane
    }

    /// Low Power Mode: the end time up to which chunks on the live path may go now. Once the oldest waiting one ended
    /// `lowPowerHoldSeconds` ago, everything finished by then is released and keeps going (a few messages at a time)
    /// while later chunks start a new hold.
    private mutating func lowPowerRelease(_ ordered: [Item], lane: Set<Key>, busy: Set<Key>, now: Date) -> Date {
        let released = lowPowerReleased ?? .distantPast
        let waiting = ordered.filter { lane.contains($0.key) && !busy.contains($0.key) && $0.finishedAt > released }
        guard let oldest = waiting.map(\.finishedAt).min() else { return released }
        let due = oldest.addingTimeInterval(Self.lowPowerHoldSeconds)
        guard now >= due else {
            hold(until: due)
            return released
        }
        lowPowerReleased = now
        return now
    }

    private mutating func hold(until due: Date) {
        heldUntil = min(heldUntil ?? due, due)
    }

    /// Oldest first: by the chunk's start, then by seq. Days sort by their chunks' times, so yesterday's backlog goes
    /// before today's live chunks.
    private static func oldestFirst(_ a: Item, _ b: Item) -> Bool {
        if a.startedAt != b.startedAt { return a.startedAt < b.startedAt }
        if a.key.recording != b.key.recording { return a.key.recording.uuidString < b.key.recording.uuidString }
        return a.key.seq < b.key.seq
    }

    // MARK: - What happened

    /// The transport sent `action` (for a message: committed to it, before its frame is read).
    mutating func sent(_ action: Action, at time: Date) {
        let route: Route = switch action {
        case .message: .message
        case .file, .userInfo: .queued
        }
        sends[action.key] = Send(route: route, at: time, progress: .awaiting)
        retryAt[action.key] = nil
    }

    /// A message failed (an error, or a reply without its acknowledgement), or the system finished a file or user info
    /// transfer. A queued transfer that succeeded now waits `fileAckTimeout` for its acknowledgement; a failure backs
    /// off. A message that went overdue before its error came is not counted twice.
    mutating func finished(_ key: Key, failed: Bool, at time: Date) {
        guard var send = sends[key] else {
            // A transfer queued before the app last ended: the system remembers it, this doesn't.
            sends[key] = Send(route: .queued, at: time, progress: failed ? .awaiting : .delivered(time))
            if failed { fail(key, at: time) }
            return
        }
        if failed {
            guard send.progress != .failed else { return }
            fail(key, at: time)
        } else if send.route == .queued, send.progress == .awaiting {
            send.progress = .delivered(time)
            sends[key] = send
        }
    }

    /// The phone holds these chunks: nothing about them is kept.
    mutating func acked(_ keys: some Sequence<Key>) {
        for key in keys { forget(key) }
    }

    /// What was sent for this chunk no longer counts (it was erased or lost since, or its message never left): it is
    /// planned afresh, without a backoff.
    mutating func reset(_ key: Key) {
        forget(key)
    }

    /// Becoming reachable retries at once: backoffs end and start again from `backoffFirst`, and messages that went
    /// out before the link dropped are sent again rather than waiting for their timeout.
    mutating func reachabilityChanged(_ reachable: Bool) {
        guard reachable else { return }
        retryAt.removeAll()
        failures.removeAll()
        for (key, send) in sends where send.route == .message && send.progress == .awaiting {
            sends[key]?.progress = .failed
        }
    }

    /// When a plan could next do something without a new event (a backoff ends, a reply or an acknowledgement is
    /// overdue, a hold ends); nil when only an event can change anything (a chunk, an acknowledgement, a reachability
    /// change, a transfer finishing).
    func nextWake(after now: Date) -> Date? {
        var times = Array(retryAt.values)
        if let heldUntil { times.append(heldUntil) }
        for send in sends.values {
            switch (send.route, send.progress) {
            case (.message, .awaiting): times.append(send.at.addingTimeInterval(RecordingDayContract.ackTimeout))
            case (.queued, .awaiting): times.append(send.at.addingTimeInterval(RecordingDayContract.fileAckTimeout))
            case (.queued, .delivered(let at)): times.append(at.addingTimeInterval(RecordingDayContract.fileAckTimeout))
            default: break
            }
        }
        return times.filter { $0 > now }.min()
    }

    /// The wait after the `failures`th failure in a row: `backoffFirst`, doubling up to `backoffMax`.
    static func backoff(_ failures: Int) -> Double {
        let doublings = Double(min(max(failures - 1, 0), 30))
        return min(RecordingDayContract.backoffMax, RecordingDayContract.backoffFirst * pow(2, doublings))
    }

    /// Sends whose acknowledgement is overdue count as failed: a message `ackTimeout` after it went, a delivered file
    /// `fileAckTimeout` after the system said so, and a queued one the system no longer holds `fileAckTimeout` after it
    /// went (its result never came). One the system still holds is never overdue: it may wait hours for the phone.
    private mutating func expire(now: Date, outstanding: Set<Key>) {
        for (key, send) in sends {
            let deadline: Date? = switch (send.route, send.progress) {
            case (.message, .awaiting): send.at.addingTimeInterval(RecordingDayContract.ackTimeout)
            case (.queued, .delivered(let at)): at.addingTimeInterval(RecordingDayContract.fileAckTimeout)
            case (.queued, .awaiting): outstanding.contains(key) ? nil : send.at.addingTimeInterval(RecordingDayContract.fileAckTimeout)
            default: nil
            }
            if let deadline, now >= deadline { fail(key, at: deadline) }
        }
    }

    private mutating func fail(_ key: Key, at time: Date) {
        let count = (failures[key] ?? 0) + 1
        failures[key] = count
        retryAt[key] = time.addingTimeInterval(Self.backoff(count))
        sends[key]?.progress = .failed
    }

    private mutating func forget(_ key: Key) {
        sends[key] = nil
        failures[key] = nil
        retryAt[key] = nil
    }
}

// MARK: - The watch's records on the wire

/// What the watch sends for each record of a day recording, decided apart from WatchConnectivity so it is tested with
/// the planner: the item, the header (end flag, marks), which record carries each mark, when a day left open ends, when
/// a finished day can go, and the state the phone's Today row shows.
nonisolated extension RecordingDayOutbox {
    /// The item for an unacknowledged record. `frameBytes`: its frame's size when known (measured or framed), else it is
    /// estimated from the audio's length at the recording's bit rate.
    static func item(_ record: RecordingSegment, of entry: RecordingEntry, frameBytes: Int?) -> Item {
        let startedAt = start(of: record, in: entry)
        let marker = record.isDayMarker
        let bytes = marker ? frameOverheadBytes : frameBytes ?? estimatedFrameBytes(seconds: record.duration)
        return Item(key: Key(recording: entry.id, seq: record.index), finishedAt: end(of: record, in: entry),
                    startedAt: startedAt, seconds: marker ? 0 : max(0, record.duration), frameBytes: bytes, isMarker: marker)
    }

    /// A frame's size before its file was looked at: AAC at the recording's bit rate, the file's container, the header.
    static func estimatedFrameBytes(seconds: Double) -> Int {
        let audio = max(0, seconds.isFinite ? seconds : 0) * Double(RecordingAudioSpec.bitRate) / 8
        return Int(audio.rounded(.up)) + containerOverheadBytes + frameOverheadBytes
    }

    /// An m4a file's boxes around the AAC frames, a little over what the writer's files carry.
    private static let containerOverheadBytes = 2_048

    /// The header a record travels with. The end flag is the record's own (the last chunk before midnight, the end
    /// marker), or it is the last seq of a closed day. Erased and lost records carry no level: nothing of their audio
    /// is left. `marks`: those this record carries (`carriedMarks`, never more than `maxMarksPerHeader`), sent relative
    /// to its start.
    static func header(for record: RecordingSegment, in entry: RecordingEntry, marks: [RecordingDayMark]) -> RecordingDayHeader {
        let startedAt = start(of: record, in: entry)
        let erased = record.erased == true
        let lost = record.lost == true && !erased
        let last = !entry.active && entry.expectedSegments == record.index + 1
        let carried = marks.sorted { $0.at < $1.at }
            .map { RecordingDayHeader.Mark(at: $0.at.timeIntervalSince(startedAt), label: $0.label) }
        return RecordingDayHeader(recording: entry.id, day: day(of: entry), seq: record.index, startedAt: startedAt,
                                  duration: max(0, record.duration), level: erased || lost ? nil : record.level, erased: erased,
                                  end: record.end == true || last, after: record.after, marks: carried.isEmpty ? nil : carried,
                                  recordingStartedAt: entry.createdAt, lost: lost)
    }

    /// Whether an acknowledgement of its seq takes a record off the watch. For an erased record a seq-only
    /// acknowledgement may be for the audio sent before the erase, so it counts only as the reply to the erased
    /// marker's own message (`confirmsErase`), or once the system delivered that marker as user info
    /// (`eraseDelivered`): from then on the erase reaches the phone's app whatever acknowledges the seq. Queued is not
    /// enough, since a queued transfer can still fail.
    static func acceptsAck(of record: RecordingSegment, confirmsErase: Bool, eraseDelivered: Bool) -> Bool {
        record.erased != true || confirmsErase || eraseDelivered
    }

    /// Whether a transfer still speaks for its record: it carried what the record is now. Audio, or a lost marker, sent
    /// before the chunk was erased or lost says nothing about the marker waiting now, and must not hold it back.
    static func isCurrent(_ header: RecordingDayHeader, for record: RecordingSegment) -> Bool {
        let erased = record.erased == true
        return header.erased == erased && header.lost == (record.lost == true && !erased)
    }

    /// Which unacknowledged record carries each of the entry's marks, by seq: the first, in seq order, that ends at or
    /// after the mark and still has room (`maxMarksPerHeader`). So a mark goes with the chunk it falls in, one made
    /// while paused with the next chunk (or the day's end marker), `at` then below zero, and what a full record can't
    /// take with the records after it. A record already acknowledged can't take one any more: a mark it would have
    /// carried goes with the next. A mark no later record can take waits for the next chunk while the day is open. A
    /// closed day gets no more chunks, so such a mark goes with the latest record that has room, `at` then past that
    /// record's end (the phone places a mark by the wall clock either way). Only a closed day with more marks than its
    /// unacknowledged records can carry, a hundred each, leaves some without one. Marks are placed oldest first, so a new
    /// one, made later, never moves one already placed: a chunk sent again carries the marks it carried before.
    static func carriedMarks(_ entry: RecordingEntry) -> [Int: [RecordingDayMark]] {
        guard let marks = entry.dayMarks, !marks.isEmpty else { return [:] }
        let open = entry.segments.filter { !$0.delivered }.sorted { $0.index < $1.index }
        var carried: [Int: [RecordingDayMark]] = [:]
        func hasRoom(_ record: RecordingSegment) -> Bool { (carried[record.index]?.count ?? 0) < maxMarksPerHeader }
        for mark in marks.sorted(by: { $0.at < $1.at }) {
            let next = open.first { end(of: $0, in: entry) >= mark.at && hasRoom($0) }
            guard let carrier = next ?? (entry.active ? nil : open.last(where: hasRoom)) else { continue }
            carried[carrier.index, default: []].append(mark)
        }
        return carried
    }

    /// What the capture still does with a day recording, which decides whether the transfer may end it.
    nonisolated enum CaptureHold: Sendable, Equatable {
        /// Nothing: the microphone is elsewhere or off, and no audio of it waits for its save.
        case none
        /// The microphone records into it, or is opening it: its writer cuts at the midnight, and the capture moves on.
        case recording
        /// Audio of it waits for its save: the end marker would take that chunk's seq.
        case savingAudio
        /// All-day recording is paused in it (by the user, or a call). `unsavedAudio`: the writer has audio at the seq
        /// the end marker would take (a buffer that reached it after the pause's cut, saved at the capture's next day
        /// check).
        case paused(unsavedAudio: Bool)
    }

    /// When an open day recording ends, once its day is over; nil while it lasts, for anything but an open day, and while
    /// the capture still needs it open (`capture`). Over means what the capture means when it starts a day: its date is
    /// before today's in `zone`, the watch's zone now. So a flight west, where the recording's own midnight comes before
    /// the watch's, leaves it open for the capture to go on in, rather than giving the same date a second recording. It
    /// ends at its own midnight (in its own time zone), or now when that is still ahead (a flight east), and never before
    /// the end of a record it already has, so the end marker is the day's last moment.
    ///
    /// A day all-day recording is paused in ends like one nothing records into, once its writer holds nothing of it:
    /// nothing new comes into a paused day, and the capture's day check before the microphone opens again (Continue, a
    /// mark, the date changing) moves on to a recording for today and leaves the closed one as it is
    /// (`RecordingLibrary.closeDay` ends a day once). So a pause past midnight doesn't keep the day open until then.
    static func closingTime(of entry: RecordingEntry, now: Date, zone: TimeZone, capture: CaptureHold = .none) -> Date? {
        switch capture {
        case .recording, .savingAudio, .paused(unsavedAudio: true): return nil
        case .none, .paused(unsavedAudio: false): break
        }
        guard entry.isDay, entry.active else { return nil }
        let own = self.zone(of: entry)
        let date = day(of: entry)
        // `YYYY-MM-DD` sorts as the dates do.
        guard date < RecordingDayCalendar.day(of: now, in: zone) else { return nil }
        let start = RecordingDayCalendar.start(of: date, in: own) ?? entry.createdAt
        let midnight = RecordingDayCalendar.nextMidnight(after: start, in: own)
        let lastEnd = entry.segments.map { end(of: $0, in: entry) }.max() ?? .distantPast
        return max(min(now, midnight), lastEnd)
    }

    /// A closed day recording the phone holds all of: every seq up to its end marker acknowledged, and its records pruned
    /// once an erase could no longer reach them. Nothing is left to send or erase, so the entry itself can go.
    static func isFinished(_ entry: RecordingEntry) -> Bool {
        guard entry.isDay, !entry.active, entry.segments.isEmpty, let expected = entry.expectedSegments else { return false }
        return (entry.prunedBelow ?? 0) >= expected
    }

    /// The state the phone's Today row shows. `allDay`: day mode runs (recording or paused); `recording`: the microphone
    /// is on; `interrupted`: the system paused it; `dayOpen`: a day recording is open; `intent`: the user still wants
    /// all-day recording on, so an open day without the microphone stopped by itself (the system or a crash) rather
    /// than by the user.
    static func watchState(allDay: Bool, recording: Bool, interrupted: Bool, dayOpen: Bool, intent: Bool) -> RecordingDayWatchState.State {
        if allDay { return recording ? .recording : interrupted ? .interrupted : .paused }
        return dayOpen && intent ? .stopped : .off
    }

    private static func zone(of entry: RecordingEntry) -> TimeZone {
        entry.timeZone.flatMap(TimeZone.init(identifier:)) ?? .autoupdatingCurrent
    }

    private static func day(of entry: RecordingEntry) -> String {
        entry.day ?? RecordingDayCalendar.day(of: entry.createdAt, in: zone(of: entry))
    }

    /// A day record's wall-clock start; one from before wall-clock starts were kept is placed on the entry's timeline.
    private static func start(of record: RecordingSegment, in entry: RecordingEntry) -> Date {
        record.startedAt ?? entry.createdAt.addingTimeInterval(record.start)
    }

    private static func end(of record: RecordingSegment, in entry: RecordingEntry) -> Date {
        start(of: record, in: entry).addingTimeInterval(max(0, record.duration.isFinite ? record.duration : 0))
    }
}
