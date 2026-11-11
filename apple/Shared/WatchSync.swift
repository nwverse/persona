import Foundation
import os
#if canImport(WatchConnectivity)
import WatchConnectivity
#endif

/// Each group's credential the watch uses, and the groups' names (docs/protocol/multi-hub.md「Watch, Siri, Mac, and demo mode」).
nonisolated struct WatchConfigs: Codable, Sendable, Equatable {
    var configs: [String: ServerConfig]
    var names: [String: String] = [:]
    /// The groups the phone has but could not get the watch a credential for yet (#1185: an older hub, or the hub
    /// not reachable when it asked); the watch asks the user to sync again. nil from an older phone.
    var missing: [String]? = nil
    /// Watch token ids only; absent from older phones. Never the phone's device ids.
    var deviceIDs: [String: String]? = nil
    /// Authenticated phone hello capabilities by group; missing is unknown, an empty array is known unsupported.
    var features: [String: [String]]? = nil
}

nonisolated extension WatchConfigs {
    /// The watch keeps what the phone sent in its keychain: the configs carry hub tokens and Cloudflare Access service
    /// tokens. Builds before this kept them as plain JSON in defaults ("configs", or "config" from before several hubs);
    /// `stored` moves those over.
    static let keychainAccount = "watchConfigs"

    static func stored(_ defaults: UserDefaults = .standard) -> WatchConfigs? {
        switch Keychain.read(account: keychainAccount) {
        case .found(let data): return try? JSONDecoder().decode(Self.self, from: data)
        case .failed: return nil // not readable yet (before the first unlock): leave the old copy for next time
        case .missing: break
        }
        let old = defaults.data(forKey: "configs").flatMap { try? JSONDecoder().decode(Self.self, from: $0) }
            ?? defaults.data(forKey: "config").flatMap { try? JSONDecoder().decode(ServerConfig.self, from: $0) }
                .map { WatchConfigs(configs: ["": $0]) }
        old?.store(defaults)
        return old
    }

    /// Saves to the keychain and drops the plain copy older builds left in defaults.
    func store(_ defaults: UserDefaults = .standard) {
        guard let data = try? JSONEncoder().encode(self), Keychain.save(data, account: Self.keychainAccount) else { return }
        defaults.removeObject(forKey: "configs")
        defaults.removeObject(forKey: "config")
    }
}

nonisolated extension Interaction {
    /// Whether the phone passes it to the watch: not an MCP form or link, which the watch can't fill in, and which a
    /// watch build from before the kind would fail to decode, losing the whole summary with it.
    var showsOnWatch: Bool { kind != .elicitation }
}

/// What the watch needs to render without its own WebSocket: live sessions and pending interactions.

nonisolated struct WatchSummary: Codable, Sendable {
    var sessions: [Session]
    var interactions: [Interaction]
    var machines: [Machine]
    /// Open inbox items that need the user (docs/assistant/inbox.md); nil from an older phone.
    var inbox: [InboxItem]? = nil
    var at: Date = .now

    enum CodingKeys: String, CodingKey { case sessions, interactions, machines, inbox, at }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(sessions.map { NativeSessionEnvelope(session: $0) }, forKey: .sessions)
        try c.encode(interactions, forKey: .interactions)
        try c.encode(machines, forKey: .machines)
        try c.encodeIfPresent(inbox, forKey: .inbox)
        try c.encode(at, forKey: .at)
    }
}

/// What the phone's application context says it takes, kept in the watch's defaults for its views: rolling
/// recording, and all-day recording (docs/assistant/day-recap.md). A value is written only when it changes: the phone
/// sends its whole context again with every summary, every few seconds, and each write redraws every view that reads
/// defaults. Only a full context (the session's, at activation and on every change) says what the phone no longer
/// takes: one without a day key removes it. User info and config replies carry part of it and remove nothing. The
/// rolling key keeps its old rule (an older phone never sends it; nothing removes it).
nonisolated enum WatchPhoneCapabilities {
    static let rollingKey = "recording.phoneRolling"
    static let dayKey = "recording.phoneDay"
    static let dayHubKey = "recording.phoneDayHub"

    /// Applies one context; returns how many values it wrote or removed (0 when nothing changed).
    @discardableResult static func store(_ ctx: [String: Any], full: Bool, in defaults: UserDefaults = .standard) -> Int {
        var writes = 0
        if let rolling = ctx["recordingRolling"] as? Bool, defaults.object(forKey: rollingKey) as? Bool != rolling {
            defaults.set(rolling, forKey: rollingKey)
            writes += 1
        }
        if let version = ctx[RecordingDayContract.phoneKey] as? Int {
            if defaults.object(forKey: dayKey) as? Int != version { defaults.set(version, forKey: dayKey); writes += 1 }
        } else if full, defaults.object(forKey: dayKey) != nil {
            defaults.removeObject(forKey: dayKey)
            writes += 1
        }
        // Absent and false differ: false is a hub without the flag, absent a phone that doesn't say.
        if let hub = ctx[RecordingDayContract.phoneHubKey] as? Bool {
            if defaults.object(forKey: dayHubKey) as? Bool != hub { defaults.set(hub, forKey: dayHubKey); writes += 1 }
        } else if full, defaults.object(forKey: dayHubKey) != nil {
            defaults.removeObject(forKey: dayHubKey)
            writes += 1
        }
        return writes
    }
}

/// The order of the phone's all-day commands ([ext] `sentAtKey`). A command goes as a message, or as user info when the
/// watch app isn't running or the message fails, so one can arrive late, twice, or after a newer one: a "start" whose
/// message fails is queued as user info only once its error comes back, which can be after the "stop" that followed it
/// went out. The phone stamps every command with when it sent it, each later than the one before; the watch acts only
/// on a command newer than the last one it acted on, and recent.
nonisolated enum WatchDayCommandOrder {
    /// The stamp's key in the command: seconds since 1970.
    static let sentAtKey = "recordingDayCommandAt"
    /// A command arriving this long after it was sent is dropped: it was queued while the watch app wasn't running, and
    /// the phone's switch stopped waiting for it long ago.
    static let freshness: TimeInterval = 600
    /// Watch side: the stamp of the last command acted on. Kept in defaults, since user info waits for the app's next
    /// launch; written once per command acted on.
    static let lastKey = "recording.dayCommandAt"

    /// Phone side: the stamps handed out, so a message that failed can tell whether a newer command went out since.
    nonisolated final class Stamps: Sendable {
        private let last = OSAllocatedUnfairLock(initialState: 0.0)

        /// The stamp for a command sent `now`: its time, or just after the previous stamp when the clock stepped back.
        func next(now: Date = .now) -> Double {
            last.withLock { newest in
                newest = max(now.timeIntervalSince1970, newest.nextUp)
                return newest
            }
        }

        /// Whether `stamp` is still the newest command's: false once another command was sent after it.
        func isNewest(_ stamp: Double) -> Bool { last.withLock { $0 == stamp } }
    }

    /// Watch side: whether to act on a command stamped `sentAt`; one acted on becomes the newest. A command without a
    /// stamp can't be ordered and is acted on as it comes.
    static func accept(sentAt: Double?, now: Date = .now, defaults: UserDefaults = .standard) -> Bool {
        guard let sentAt else { return true }
        guard sentAt.isFinite, now.timeIntervalSince1970 - sentAt <= freshness,
              sentAt > defaults.double(forKey: lastKey) else { return false }
        defaults.set(sentAt, forKey: lastKey)
        return true
    }
}

#if canImport(WatchConnectivity)
/// iPhone → Watch sync over WatchConnectivity: the server pairing and a rolling summary.
/// The watch talks to the hub directly for actions (answering, sending), so the phone only seeds it.
final class WatchSync: NSObject, WCSessionDelegate {
    static let shared = WatchSync()

    var onConfig: ((ServerConfig) -> Void)?
    /// Every group's credential (a phone that knows several hubs).
    var onConfigs: ((WatchConfigs) -> Void)?
    var onSummary: ((WatchSummary) -> Void)?
    /// The persona chosen on the phone (the watch shows the Milk Frog only for its own).
    var onPersona: ((String) -> Void)?
    /// Called once the session is activated; the phone pushes its pairing then (pushes before that are dropped).
    var onActivated: (() -> Void)?
    /// iPhone side: answers the watch's "send me the pairing" request.
    var configProvider: (() -> ServerConfig?)?
    var configsProvider: (() -> WatchConfigs?)?
    var onRecording: ((RecordingTransfer) -> Void)?
    /// Watch side: the meetings' minutes the phone relays (`WatchMinutes`).
    var onMinutes: ((WatchMinutes) -> Void)?
    var onRecordingReceipt: ((RecordingReceipt) -> Void)?
    var onRecordingEvent: (() -> Void)?

    // All-day recording (docs/assistant/day-recap.md). The first three run synchronously on WatchConnectivity's queue,
    // so they are not main-actor slots: `RecordingDayReceiver` sets each once, before `activate()`, and never again
    // (nothing synchronizes them). Their closures are `@Sendable` and reach main-actor state only by hopping there.
    /// iPhone side: a chunk or marker sent by `sendMessageData` (a frame, `RecordingDayHeader.unframe`). `reply` takes
    /// the `RecordingDayAck` (JSON) once the chunk is on disk; not calling it leaves the chunk to the watch's resend.
    nonisolated(unsafe) var onDayChunk: (@Sendable (_ frame: Data, _ reply: @escaping @Sendable (Data) -> Void) -> Void)?
    /// iPhone side: a chunk sent by `transferFile`. The system deletes `file` when this returns, so it is moved before;
    /// true when it was taken care of (stored, already held, or for a deleted day), false to leave it to the resend.
    nonisolated(unsafe) var onDayFile: (@Sendable (_ header: RecordingDayHeader, _ file: URL) -> Bool)?
    /// iPhone side: user info (or a message) under `chunkKey` (a marker header) or `lostKey`; true when handled.
    nonisolated(unsafe) var onDayUserInfo: (@Sendable (_ info: [String: Any]) -> Bool)?
    /// iPhone side: the watch's all-day state (`RecordingDayContract.watchStateKey` in its application context).
    var onDayWatchState: ((RecordingDayWatchState) -> Void)?
    /// Watch side: the phone's switch (`RecordingDayContract.Command`). Only commands newer than the last one it got
    /// reach it (`WatchDayCommandOrder`): never a late, repeated or stale one.
    var onDayCommand: ((String) -> Void)?
    /// Watch side: acknowledgements of chunks sent by file or user info.
    var onDayAck: ((RecordingDayAck) -> Void)?

    private var lastSummaryPush = Date.distantPast
    private var pendingSummary: WatchSummary?

    func activate() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    /// Every group's credential for the watch (docs/protocol/multi-hub.md「Watch, Siri, Mac, and demo mode」), what the groups are called and which have none
    /// yet; `config` (the first group's) stays for a watch app from before several hubs. Sent with no credentials too
    /// (#1185): a watch still holding one the phone no longer gives it drops it.
    func push(_ all: WatchConfigs) {
        var values: [String: Any] = ["configs": (try? JSONEncoder().encode(all)) ?? Data()]
        if let first = all.configs[""] ?? all.configs.values.first { values["config"] = (try? JSONEncoder().encode(first)) ?? Data() }
        update(values, removing: values["config"] == nil ? ["config"] : [])
        #if os(iOS) || os(visionOS)
        // Application context only keeps the latest state; user info is queued and delivered even if the watch app
        // was never opened, so a fresh install still gets paired.
        if WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled {
            WCSession.default.transferUserInfo(values)
        }
        #endif
    }

    /// The iPhone's interface language (docs/protocol/i18n.md: the watch follows the phone), as a code.
    func push(language: String) {
        update(["language": language])
    }

    /// The persona chosen on the phone, by id.
    func push(persona: String) {
        update(["persona": persona])
    }

    #if os(iOS) || os(visionOS)
    /// A paired watch with the app on it: the phone gets it credentials only then.
    var watchAppInstalled: Bool { WCSession.isSupported() && WCSession.default.isPaired && WCSession.default.isWatchAppInstalled }
    #endif

    #if os(watchOS)
    /// Watch side: ask the phone for its pairing right now (needs the phone nearby).
    func requestConfig(done: @escaping @MainActor (Bool) -> Void) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isReachable else {
            done(false)
            return
        }
        // Both handlers run on WatchConnectivity's queue: `@Sendable` keeps them off the main actor (BackgroundCallbackTests).
        WCSession.default.sendMessage(["request": "config"], replyHandler: { @Sendable reply in
            nonisolated(unsafe) let r = reply
            Task { @MainActor in
                self.apply(r, full: false)
                done(r["configs"] != nil || r["config"] != nil) // the phone answered with its pairing
            }
        }, errorHandler: { @Sendable _ in Task { @MainActor in done(false) } })
    }
    #endif

    /// Throttled: application context is meant for state, not a stream.
    func push(summary: @autoclosure () -> WatchSummary) {
        #if os(iOS) || os(visionOS)
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled else { return }
        pendingSummary = summary()
        let wait = 2 - Date.now.timeIntervalSince(lastSummaryPush)
        if wait <= 0 { flushSummary() } else {
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(wait))
                self.flushSummary()
            }
        }
        #endif
    }

    private func flushSummary() {
        guard let s = pendingSummary, let data = try? JSON.encoder.encode(s) else { return }
        pendingSummary = nil
        lastSummaryPush = .now
        update(["summary": data])
    }

    /// iPhone side: the meetings' minutes for the watch, when they changed.
    func push(minutes: WatchMinutes) {
        #if os(iOS) || os(visionOS)
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled,
              let data = minutes.data, WCSession.default.applicationContext[WatchMinutes.key] as? Data != data else { return }
        update([WatchMinutes.key: data])
        #endif
    }

    private func update(_ values: [String: Any], removing gone: [String] = []) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated else { return }
        var ctx = WCSession.default.applicationContext
        values.forEach { ctx[$0.key] = $0.value }
        gone.forEach { ctx[$0] = nil }
        try? WCSession.default.updateApplicationContext(ctx)
    }

    #if os(iOS) || os(visionOS)
    /// iPhone side: tells the watch it takes all-day recordings (`recordingDay`, this build's contract version) and
    /// whether its hub lists `recording_day` (`recordingDayHub`). Activation sends them too, from the cached hub flag.
    /// A push that changes nothing is skipped.
    func pushRecordingDay(hub: Bool) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated else { return }
        let ctx = WCSession.default.applicationContext
        guard ctx[RecordingDayContract.phoneKey] as? Int != RecordingDayContract.version
                || ctx[RecordingDayContract.phoneHubKey] as? Bool != hub else { return }
        update(Self.dayCapabilities(hub: hub))
    }

    /// iPhone side: the Recordings page's switch (`RecordingDayContract.Command`). The watch gets it at once while its app
    /// runs, else when it next runs. Only the newest command waits: a queued one is cancelled, and a message that fails
    /// after a newer command went out is not queued. The stamp (`WatchDayCommandOrder`) is what keeps the watch from
    /// acting on an older one that still gets through.
    func sendDayCommand(_ command: String) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled else { return }
        for transfer in WCSession.default.outstandingUserInfoTransfers where transfer.userInfo[RecordingDayContract.commandKey] != nil {
            transfer.cancel()
        }
        let stamp = Self.dayCommandStamps.next()
        Self.deliver([RecordingDayContract.commandKey: command, WatchDayCommandOrder.sentAtKey: stamp],
                     queuesOnFailure: { @Sendable in Self.dayCommandStamps.isNewest(stamp) })
    }

    /// Read by the failure path of `deliver`, on WatchConnectivity's queue.
    private nonisolated static let dayCommandStamps = WatchDayCommandOrder.Stamps()

    /// iPhone side: acknowledges chunks that came by file or user info (a message chunk's acknowledgement is its reply).
    /// Callable from any queue.
    nonisolated func sendDayAck(_ ack: RecordingDayAck) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled else { return }
        Self.deliver([RecordingDayContract.ackKey: ack.data])
    }

    private static func dayCapabilities(hub: Bool) -> [String: Any] {
        [RecordingDayContract.phoneKey: RecordingDayContract.version, RecordingDayContract.phoneHubKey: hub]
    }
    #endif

    #if os(watchOS)
    /// Watch side: the all-day state for the phone's Today row, in this side's application context; skipped when the
    /// phone already has the same.
    func publishDayWatchState(_ state: RecordingDayWatchState) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, let data = state.data,
              WCSession.default.applicationContext[RecordingDayContract.watchStateKey] as? Data != data else { return }
        update([RecordingDayContract.watchStateKey: data])
    }

    /// Watch side: seqs it could not keep (`RecordingDayLost`), queued so the report outlives either app. Callable from
    /// any queue.
    nonisolated func sendDayLost(_ lost: RecordingDayLost) {
        guard WCSession.isSupported(), WCSession.default.activationState == .activated, WCSession.default.isCompanionAppInstalled else { return }
        WCSession.default.transferUserInfo([RecordingDayContract.lostKey: lost.data])
    }
    #endif

    /// A message reaches the other side at once while its app runs; otherwise, or when the message fails, it goes as
    /// user info, queued until that app next runs. `queuesOnFailure` runs when the message failed, on WatchConnectivity's
    /// queue: false drops it instead (a newer one went out since).
    private nonisolated static func deliver(_ message: [String: Any], queuesOnFailure: @escaping @Sendable () -> Bool = { true }) {
        let session = WCSession.default
        guard session.isReachable else {
            session.transferUserInfo(message)
            return
        }
        nonisolated(unsafe) let queued = message
        session.sendMessage(message, replyHandler: nil, errorHandler: { @Sendable _ in
            guard queuesOnFailure() else { return }
            WCSession.default.transferUserInfo(queued)
        })
    }

    /// The iPhone side sets this once it can hold rolling recordings; an older phone never does, so the watch keeps to plain recordings.
    nonisolated static let phoneRollingKey = WatchPhoneCapabilities.rollingKey
    /// Watch side: the phone takes all-day recordings (an Int, the contract version; absent with an older phone), and its
    /// hub lists `recording_day` (a Bool; absent until the phone says).
    nonisolated static let phoneDayKey = WatchPhoneCapabilities.dayKey
    nonisolated static let phoneDayHubKey = WatchPhoneCapabilities.dayHubKey

    /// `full`: the whole application context (at activation, or a new one arrived), which also says what the phone no
    /// longer offers; user info and config replies are partial.
    private func apply(_ ctx: [String: Any], full: Bool) {
        WatchPhoneCapabilities.store(ctx, full: full)
        #if os(watchOS)
        if let language = ctx["language"] as? String {
            LanguageStore.shared.follow(language)
            if AppLanguage.codes.contains(language) {
                LanguageStore.groupDefaults()?.set(language, forKey: LanguageStore.resolvedKey)
            }
        }
        #endif
        if let persona = ctx["persona"] as? String { onPersona?(persona) }
        if let data = ctx["configs"] as? Data, let all = try? JSONDecoder().decode(WatchConfigs.self, from: data) {
            onConfigs?(all)
        } else if let data = ctx["config"] as? Data, let cfg = try? JSONDecoder().decode(ServerConfig.self, from: data) {
            onConfig?(cfg)
        }
        if let data = ctx["summary"] as? Data, let s = try? JSON.decoder.decode(WatchSummary.self, from: data) {
            onSummary?(s)
        }
        if let minutes = WatchMinutes(data: ctx[WatchMinutes.key] as? Data) { onMinutes?(minutes) }
        if let state = RecordingDayWatchState(data: ctx[RecordingDayContract.watchStateKey] as? Data) { onDayWatchState?(state) }
    }

    /// Routes a message's or user info's all-day keys; true when it carried one (it is then nothing else). Payloads are
    /// decoded here, on WatchConnectivity's queue, so only Sendable values cross to the main actor.
    private nonisolated func routeDay(_ info: [String: Any]) -> Bool {
        if info[RecordingDayContract.chunkKey] != nil || info[RecordingDayContract.lostKey] != nil {
            _ = onDayUserInfo?(info)
            return true
        }
        if info[RecordingDayContract.ackKey] != nil {
            if let ack = RecordingDayAck(data: info[RecordingDayContract.ackKey] as? Data) {
                Task { @MainActor in self.onDayAck?(ack) }
            }
            return true
        }
        guard let command = info[RecordingDayContract.commandKey] as? String else { return false }
        let sentAt = info[WatchDayCommandOrder.sentAtKey] as? Double
        // Ordered on the main actor, one command at a time: a late or repeated one is dropped there. A command nobody
        // takes yet doesn't count as the newest.
        Task { @MainActor in
            guard let handle = self.onDayCommand, WatchDayCommandOrder.accept(sentAt: sentAt) else { return }
            handle(command)
        }
        return true
    }

    nonisolated func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState, error: Error?) {
        nonisolated(unsafe) let ctx = session.receivedApplicationContext
        Task { @MainActor in
            self.apply(ctx, full: true)
            #if os(iOS) || os(visionOS)
            var capabilities = Self.dayCapabilities(hub: RecordingDayDefaults.offered([]))
            capabilities["recordingRolling"] = true
            self.update(capabilities)
            #endif
            self.onActivated?()
            self.onRecordingEvent?()
        }
    }

    nonisolated func session(_ session: WCSession, didReceiveUserInfo userInfo: [String: Any] = [:]) {
        // All-day markers, lost reports, acknowledgements and commands first: none of them is a context.
        if routeDay(userInfo) { return }
        let recording = RecordingTransfer(dictionary: userInfo)
        let receipt = RecordingReceipt(dictionary: userInfo)
        nonisolated(unsafe) let copy = userInfo
        Task { @MainActor in
            if let recording { self.onRecording?(recording) }
            else if let receipt { self.onRecordingReceipt?(receipt) }
            else { self.apply(copy, full: false) }
        }
    }

    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        if routeDay(message) {
            replyHandler([:])
            return
        }
        nonisolated(unsafe) let reply = replyHandler
        Task { @MainActor in
            var answer: [String: Any] = [:]
            if let cfg = self.configProvider?(), let data = try? JSONEncoder().encode(cfg) { answer["config"] = data }
            if let all = self.configsProvider?(), let data = try? JSONEncoder().encode(all) { answer["configs"] = data }
            reply(answer)
        }
    }

    /// Messages sent without a reply handler: all-day commands and acknowledgements.
    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any]) {
        _ = routeDay(message)
    }

    /// iPhone side: an all-day chunk on the live path. The receiver stores it, then replies with its acknowledgement;
    /// without a receiver the empty reply acknowledges nothing and the watch sends it again.
    nonisolated func session(_ session: WCSession, didReceiveMessageData messageData: Data, replyHandler: @escaping (Data) -> Void) {
        nonisolated(unsafe) let reply = replyHandler
        guard let receive = onDayChunk else {
            reply(Data())
            return
        }
        receive(messageData) { @Sendable answer in reply(answer) }
    }

    nonisolated func session(_ session: WCSession, didReceiveApplicationContext ctx: [String: Any]) {
        nonisolated(unsafe) let copy = ctx
        Task { @MainActor in self.apply(copy, full: true) }
    }

    nonisolated func sessionReachabilityDidChange(_ session: WCSession) {
        Task { @MainActor in self.onRecordingEvent?() }
    }

    nonisolated func session(_ session: WCSession, didReceive file: WCSessionFile) {
        #if os(iOS) || os(visionOS)
        // An all-day chunk: the receiver takes the file before this returns and acknowledges it itself.
        if let header = RecordingDayHeader(userInfo: file.metadata) {
            _ = onDayFile?(header, file.fileURL)
            return
        }
        guard let transfer = RecordingTransfer(dictionary: file.metadata), let segment = transfer.segment else { return }
        // The system deletes the temporary file once the callback returns: move it synchronously first, then let the main thread update the index.
        let dir = RecordingLibrary.directory.appendingPathComponent(transfer.id.uuidString, isDirectory: true)
        let destination = dir.appendingPathComponent(RecordingSegment.fileName(segment.index))
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            if !FileManager.default.fileExists(atPath: destination.path) {
                try FileManager.default.moveItem(at: file.fileURL, to: destination)
            }
            Task { @MainActor in self.onRecording?(transfer) }
        } catch { /* Not acknowledged; the watch keeps the audio and retries. */ }
        #endif
    }

    nonisolated func session(_ session: WCSession, didFinish fileTransfer: WCSessionFileTransfer, error: Error?) {
        #if os(watchOS)
        let failed = error != nil
        if let header = RecordingDayHeader(userInfo: fileTransfer.file.metadata) {
            Task { @MainActor in WatchDayTransfer.shared.finished(header, failed: failed) }
            return
        }
        guard let transfer = RecordingTransfer(dictionary: fileTransfer.file.metadata) else { return }
        Task { @MainActor in WatchRecordingTransfer.shared.finished(transfer, failed: failed) }
        #endif
    }

    nonisolated func session(_ session: WCSession, didFinish userInfoTransfer: WCSessionUserInfoTransfer, error: Error?) {
        #if os(watchOS)
        let failed = error != nil
        // A marker that went as user info.
        if let header = RecordingDayHeader(userInfo: userInfoTransfer.userInfo) {
            Task { @MainActor in WatchDayTransfer.shared.finished(header, failed: failed) }
            return
        }
        guard let transfer = RecordingTransfer(dictionary: userInfoTransfer.userInfo) else { return }
        Task { @MainActor in WatchRecordingTransfer.shared.finished(transfer, failed: failed) }
        #endif
    }

    func confirmRecording(_ receipt: RecordingReceipt) {
        guard WCSession.default.activationState == .activated else { return }
        WCSession.default.transferUserInfo(receipt.dictionary)
    }

    #if os(iOS) || os(visionOS)
    nonisolated func sessionDidBecomeInactive(_ session: WCSession) {}
    nonisolated func sessionDidDeactivate(_ session: WCSession) { session.activate() }
    #endif
}
#endif
