#if os(iOS)
import AVFoundation
import Foundation
import Observation
import OSLog
import UIKit

/// Hub transcription on the phone (#3862 phase 5; the contract is docs/assistant/meeting-minutes.md「Phase 5: hub
/// transcription」, the phone's side docs/apple/recording.md「Hub transcription (phase 5)」): the one store the meeting page
/// and the Live Activity read, per recording, and what keeps it.
///
/// - **The mode** a meeting asked for as it started (`RecordingAttendance.transcription`), and the mode in effect, which
///   is the upload `200`'s `transcription` (`MeetingTranscriptionRule.decide`): `hub` only when asked for; a `200` without
///   it is an older manager, and the meeting stays on the phone for good, its uplink stopped.
/// - **The uplink** while the phone records a meeting in hub mode: the recorder's tap into the last 60 seconds in memory
///   from the start (`MeetingAudioUplink`), and the audio socket once the mode is in: `start`, `ready`, the audio from
///   `ready.from`, `role` on every change, `speaking` as Persona starts and stops, `end` at Stop. It runs whatever the role,
///   `off` included; closes while paused and opens again on Continue; tries again after a drop (1 s … 30 s), at once when
///   the network comes back; with **Hub transcription on Wi-Fi only** and no Wi-Fi it opens none. A fatal `error` acts on
///   its code (`MeetingTranscriptionRule.reaction`): one the phone doesn't know stops hub transcription for the meeting,
///   and the phone's own goes on, as it always does in hub mode.
/// - **What the hub says**: its caption (`interim`), its lines (`line` frames, the positions missed while away read with
///   `GET …/live?from=`), the standing `problem`, the gaps, and `transcription` as `GET …/live` and `detail.meta` have it
///   (`liveRead`); what the page shows of them (`shown`).
/// - **The gaps after the meeting**: `PUT …/audio?at=` decoded from the phone's segments until the hub has no gap left or
///   its final pass has started, under the same Wi-Fi rule.
@MainActor @Observable
final class MeetingTranscription {
    static let shared = MeetingTranscription(deps: .live)

    /// The audio socket of the recording being made.
    nonisolated enum Socket: String, Equatable, Sendable {
        /// No socket: the meeting is transcribed on the phone, isn't being recorded here, or its uplink stopped.
        case off
        /// Hub mode asked for; the first upload's `200` isn't in yet (the audio is kept meanwhile).
        case waitingForMode
        /// **Hub transcription on Wi-Fi only**, and no Wi-Fi: no audio leaves the phone; the time becomes gaps.
        case waitingForWiFi
        /// Opened; the hub's `ready` isn't in yet.
        case connecting
        /// Up: the audio goes.
        case ready
        /// It dropped (or there is no network): another comes.
        case down
        /// The recording is paused: closed until it goes on.
        case paused
        /// `end` sent at Stop: the hub's last lines come, then it closes.
        case ending
        /// Closed after the meeting.
        case ended
    }

    /// The gap uploads after the meeting.
    nonisolated enum GapUpload: Equatable, Sendable {
        case idle
        /// **Hub transcription on Wi-Fi only**, and no Wi-Fi.
        case waitingForWiFi
        case uploading
        /// Nothing (more) to upload: no gap left, the final pass started, or the meeting isn't the hub's.
        case done
        /// Not now (the hub or the network away; its code when it said one): the next pass tries again.
        case later(String?)
    }

    /// One meeting as this phone knows it.
    nonisolated struct Meeting: Equatable, Sendable {
        /// The mode the meeting asked for as it started; nil: the hub didn't list `meeting_transcription`.
        var asked: MeetingTranscriptionMode?
        /// The mode in effect, from the upload's `200` (nil until it came).
        var decided: MeetingTranscriptionRule.Decided?
        var socket: Socket = .off
        /// The socket was up (`ready`) at least once: a drop from then on is a fallback, the first connect isn't.
        var everUp = false
        /// The hub's caption being said now.
        var interim: MeetingInterim?
        /// The live log's lines the phone has, by position (`n`): the hub's `line` frames, and what `?from=` read.
        var lines: [MeetingHubLine] = []
        /// The socket's standing problem (`upstream_unavailable`, `upstream_failed`, `no_interim`; an open set).
        var problem: String?
        /// Why the uplink stopped for this meeting (a fatal `error`'s code, or a refused socket's `http_<status>`); nil
        /// while it may run.
        var stopped: String?
        /// The hub stored the audio up to here (`ack`).
        var acked: Double?
        /// What the hub lacks, as it last said (`ready.gaps`, `PUT …/audio`'s answer, `transcription.gaps`).
        var gaps: [MeetingGap] = []
        /// Unacknowledged audio that fell out of the phone's 60 seconds (the phone's own account).
        var lost: [MeetingGap] = []
        /// `transcription` of `GET …/live` / `detail.meta`: the mode, the hub's state, its gaps, `problem` and `fallback`.
        var hub: MeetingTranscriptionView?
        var gapUpload: GapUpload = .idle

        /// The mode in effect; nil until the `200` said.
        var mode: MeetingTranscriptionMode? { decided?.mode }
        /// The hub transcribes this meeting: the live log is its (`POST …/live` is `409 hub_transcribes`).
        var hubMode: Bool { decided?.mode == .hub }
        var socketUp: Bool { socket == .ready }
    }

    /// A stretch Persona speaks in, by who says it: an answer (`MeetingSpeaker`) or the meeting call's voice.
    nonisolated enum Voice: Hashable, Sendable { case answer, call }

    struct Deps {
        var api: () -> APIClient?
        var socket: (URLRequest, APIClient) -> VoiceSocket
        var network: () -> MeetingNetworkKind
        /// The uplink's listener on the recorder's tap (`RecordingCapture.listenUplink`); nil takes it away.
        var listen: ((@Sendable (AVAudioPCMBuffer, Double) -> Void)?) -> Void
        /// The hub has no upload of the recording: it goes again now (`RecordingSync.reupload`).
        var reupload: (UUID) -> Void
        var wait: (Duration) async -> Void
        var store: UserDefaults = .standard
        /// The audio of `piece` from the recording's segments (off the main thread).
        var audio: (RecordingEntry, MeetingGapUpload.Piece) async -> (at: Double, data: Data)? = { _, _ in nil }
        /// Past the hub's retention (`RecordingSync.isExpired`): never uploaded, and its audio goes with it.
        var expired: (RecordingEntry) -> Bool = { _ in false }
        /// The hub has the recording's latest revision (`RecordingSync.status` `synced`): its `complete: true` upload too.
        var synced: (RecordingEntry) -> Bool = { _ in true }
    }

    private(set) var meetings: [UUID: Meeting] = [:]
    /// The meetings in hub mode (`Meeting.hubMode`), kept apart so a view that only asks whether the hub transcribes
    /// (the page's subtitle and Talk line, `MeetingLive`) doesn't redraw with every caption (P5).
    private(set) var hubMeetings: Set<UUID> = []
    /// The speech service a hub-mode meeting's audio goes to, by name, when the owner chose another extension's (the
    /// privacy texts name it): what the start or Settings › Meetings last read (`noteService`); nil for the built-in one.
    private(set) var service: String?
    /// **Hub transcription on Wi-Fi only** (`MeetingTranscriptionSettings.setWiFiOnly`).
    private(set) var wifiOnly: Bool
    /// The recording the uplink belongs to, or last belonged to: what `probe` shows.
    private(set) var current: UUID?

    @ObservationIgnored let deps: Deps
    @ObservationIgnored private let library: RecordingLibrary
    @ObservationIgnored private var uplink: MeetingAudioUplink?
    @ObservationIgnored private var failures = 0
    @ObservationIgnored private var badFrames = 0
    @ObservationIgnored private var retryTask: Task<Void, Never>?
    @ObservationIgnored private var endTask: Task<Void, Never>?
    /// The role the socket was told last (`role` frames go on a change only).
    @ObservationIgnored private var sentRole: MeetingAttendance?
    /// The stretches Persona speaks in now, by voice: one `speaking` on while any is open, off once the last closes.
    @ObservationIgnored private var speakingOpen: [UUID: [Voice: RecordingSpan]] = [:]
    @ObservationIgnored private var readingLines: Set<UUID> = []
    @ObservationIgnored private var lastNetwork: MeetingNetworkKind?
    @ObservationIgnored private var gapTask: Task<Void, Never>?
    @ObservationIgnored private var gapsAgain = false
    /// Recordings with nothing (more) to upload in this run.
    @ObservationIgnored private var gapsDone: Set<UUID> = []
    /// What `follow` was last told: a retry follows it again, with the recording as the library has it then.
    @ObservationIgnored private var followed: (id: UUID?, paused: Bool) = (nil, false)
    @ObservationIgnored private var started = false
    private let log = Logger(subsystem: "persona", category: "meeting-transcription")

    init(deps: Deps, library: RecordingLibrary = .shared) {
        self.deps = deps
        self.library = library
        wifiOnly = deps.store.bool(forKey: MeetingAttendanceDefaults.wifiOnlyKey)
    }

    /// From `MeetingRecorder.start` at launch: hears every upload `200` that named a mode, follows the recording being
    /// made, the network and the app coming back, and uploads the gaps meetings left.
    func start() {
        guard !started else { return }
        started = true
        RecordingSync.shared.onTranscription = { [weak self] entry, answer in self?.uploaded(entry, answer: answer) }
        MeetingNetwork.shared.start()
        observe()
        NotificationCenter.default.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in MeetingTranscription.shared.kickGaps() }
        }
        Task { [weak self] in
            await self?.deps.wait(.seconds(3))
            self?.kickGaps()
        }
    }

    private func observe() {
        withObservationTracking { sync() } onChange: {
            Task { @MainActor in MeetingTranscription.shared.observe() }
        }
    }

    func meeting(_ id: UUID) -> Meeting { meetings[id] ?? Meeting() }

    /// The hub transcribes the meeting: no `POST …/live` (`MeetingLive`), and a heard ask sends no `live_next`.
    func hubTranscribes(_ id: UUID) -> Bool { hubMeetings.contains(id) }

    /// The speech service as the start or the settings read it (`MeetingTranscriptionSettings.service`).
    func noteService(_ name: String?) {
        if service != name { service = name }
    }

    /// What the page and the Live Activity show for `id`, whose role is `role` now.
    func shown(_ id: UUID, role: MeetingAttendance) -> MeetingTranscriptionRule.Shown {
        let m = meeting(id)
        return MeetingTranscriptionRule.shown(hub: m.hubMode, socketUp: m.socketUp, everUp: m.everUp,
                                              down: [.down, .waitingForWiFi].contains(m.socket), problem: m.problem,
                                              stopped: m.stopped != nil, role: role)
    }

    // MARK: The mode

    /// An upload of `entry` that named a mode was stored: its `200`'s `transcription` decides the mode in effect.
    func uploaded(_ entry: RecordingEntry, answer: MeetingTranscriptionRule.Answer) {
        var m = meetings[entry.id] ?? Meeting()
        m.asked = entry.attendance?.transcription
        m.decided = MeetingTranscriptionRule.decide(m.decided, asked: m.asked, answer: answer)
        if m.decided?.olderManager == true, meetings[entry.id]?.decided?.olderManager != true {
            log.notice("transcription: an older manager answered; the meeting stays on the phone")
        }
        set(entry.id, m)
        if !entry.active { kickGaps() }
    }

    // MARK: Following the recording

    /// Brings the uplink in step with the recording being made, its mode, the network and the Wi-Fi rule.
    private func sync() {
        let capture = RecordingCapture.shared
        let state = capture.state
        let id = state == .recording || state == .paused ? capture.recordingID : nil
        follow(id, paused: state == .paused, entry: id.flatMap { library.entry($0) })
        // Read here so a change of either brings the next pass.
        let network = deps.network()
        if lastNetwork != network {
            let came = lastNetwork == MeetingNetworkKind.none || lastNetwork == .metered
            lastNetwork = network
            if came { kickGaps() }
        }
    }

    /// `id` records now (`paused`), as `entry`; nil: nothing does (a socket at its `end` is left to finish).
    func follow(_ id: UUID?, paused: Bool, entry: RecordingEntry?) {
        followed = (id, paused)
        guard let id, let entry, entry.active, entry.roomMeeting, let attendance = entry.attendance,
              attendance.transcription == .hub else {
            if let uplink, meetings[uplink.recording]?.socket != .ending { detach(uplink.recording, to: .off) }
            return
        }
        var m = meetings[id] ?? Meeting()
        m.asked = .hub
        if current != id { current = id }
        // Transcribed on the phone (the hub said so, or an older manager), or the uplink stopped: no audio leaves it.
        if let decided = m.decided, decided.mode != .hub {
            if uplink?.recording == id { detach(id, to: .off) } else { set(id, m.with(socket: .off)) }
            return
        }
        if m.stopped != nil {
            if uplink?.recording == id { detach(id, to: .off) }
            return
        }
        // The tap's audio is kept from the start, whatever comes of the socket: what the hub never got goes up later.
        let uplink = attach(id)
        let network = deps.network()
        let target: Socket
        if m.decided == nil {
            target = .waitingForMode
        } else if paused {
            target = .paused
        } else if network == MeetingNetworkKind.none {
            target = .down
        } else if !MeetingTranscriptionRule.mayStream(wifiOnly: wifiOnly, network: network) {
            target = .waitingForWiFi
        } else {
            target = .ready
        }
        switch target {
        case .ready:
            if [.connecting, .ready, .ending].contains(m.socket) {
                sendRole(attendance, uplink: uplink)
            } else if m.socket == .down, retryTask != nil, lastNetwork != MeetingNetworkKind.none {
                // A try is due later.
            } else {
                retryTask?.cancel()
                retryTask = nil
                connect(id, entry: entry, uplink: uplink, meeting: &m)
            }
        default:
            if [.connecting, .ready].contains(m.socket) { uplink.close() }
            m.socket = target
            if target != .down { retryTask?.cancel(); retryTask = nil }
        }
        m.lost = uplink.lost
        set(id, m)
    }

    /// The uplink of `id`, listening to the tap (another recording's goes first).
    private func attach(_ id: UUID) -> MeetingAudioUplink {
        if let uplink, uplink.recording == id { return uplink }
        if let other = uplink?.recording { detach(other, to: .off) }
        let fresh = MeetingAudioUplink(recording: id)
        uplink = fresh
        failures = 0
        badFrames = 0
        sentRole = nil
        deps.listen { @Sendable buffer, at in fresh.feed(buffer, at: at) }
        return fresh
    }

    /// Closes `id`'s uplink and stops listening; the meeting's socket reads `state`.
    private func detach(_ id: UUID, to state: Socket) {
        retryTask?.cancel()
        retryTask = nil
        endTask?.cancel()
        endTask = nil
        if let uplink, uplink.recording == id {
            deps.listen(nil)
            uplink.close()
            var m = meetings[id] ?? Meeting()
            m.lost = uplink.lost
            m.socket = state
            if state != .ready { m.interim = nil }
            set(id, m)
            self.uplink = nil
        } else if var m = meetings[id], m.socket != state {
            m.socket = state
            set(id, m)
        }
    }

    private func connect(_ id: UUID, entry: RecordingEntry, uplink: MeetingAudioUplink, meeting m: inout Meeting) {
        guard let api = deps.api(), let attendance = entry.attendance else {
            m.socket = .down
            scheduleRetry(id)
            return
        }
        let socket = deps.socket(MeetingAudioEndpoint.request(api, recording: id), api)
        let role = attendance.roles.last ?? RecordingRoleChange(at: 0, attendance: attendance.role)
        let open = speakingOpen[id]?.values.min { $0.start < $1.start }
        uplink.open(socket, timeline: MeetingTimeline(attendance), speaking: open, role: role) { [weak self] gen, event in
            self?.received(event, recording: id, generation: gen)
        }
        sentRole = role.attendance
        m.socket = .connecting
        log.info("transcription: audio socket opening")
    }

    private func sendRole(_ attendance: RecordingAttendance, uplink: MeetingAudioUplink) {
        guard sentRole != attendance.role else { return }
        sentRole = attendance.role
        let at = attendance.roles.last?.at ?? 0
        uplink.send(text: MeetingAudioOut.role(at: at, attendance: attendance.role))
    }

    private func scheduleRetry(_ id: UUID) {
        failures += 1
        let delay = MeetingTranscriptionRule.backoff(failures)
        retryTask?.cancel()
        let wait = deps.wait
        retryTask = Task { [weak self] in
            await wait(.milliseconds(Int(delay * 1000)))
            guard !Task.isCancelled, let self else { return }
            self.retryTask = nil
            let (id, paused) = self.followed
            self.follow(id, paused: paused, entry: id.flatMap { self.library.entry($0) })
        }
    }

    // MARK: The socket

    /// What came on the socket of `generation` (a socket closed or replaced since is ignored).
    func received(_ event: VoiceSocketEvent, recording id: UUID, generation gen: Int) {
        guard let uplink, uplink.recording == id, gen == uplink.currentGeneration else { return }
        switch event {
        case .text(let text): handle(MeetingAudioIn.decode(text), recording: id, uplink: uplink, generation: gen)
        case .data: break
        case .closed(let close): closed(id, status: close.status, uplink: uplink, generation: gen)
        }
    }

    private func handle(_ frame: MeetingAudioIn, recording id: UUID, uplink: MeetingAudioUplink, generation gen: Int) {
        var m = meetings[id] ?? Meeting()
        switch frame {
        case .ready(let from, let gaps, let next):
            uplink.ready(from: from, generation: gen)
            if m.socket != .ending { m.socket = .ready }
            m.everUp = true
            m.gaps = gaps
            // The standing problem comes right after, if any.
            m.problem = nil
            failures = 0
            badFrames = 0
            let known = Self.contiguous(m.lines)
            if next > known { Task { await readLines(id, from: known) } }
        case .ack(let at):
            uplink.ack(at)
            m.acked = at
            m.lost = uplink.lost
        case .interim(let interim):
            m.interim = interim
        case .line(let line):
            m.lines = Self.merged(m.lines, [line])
            // The caption that line finishes is said.
            if let interim = m.interim, interim.start <= line.end { m.interim = nil }
        case .problem(let code):
            m.problem = code
            if code != nil, code != "no_interim" { m.interim = nil }
        case .error(let code):
            log.notice("transcription: the hub ended the audio socket: \(code, privacy: .public)")
            switch MeetingTranscriptionRule.reaction(toError: code) {
            case .stop:
                m.stopped = code.isEmpty ? "unknown" : code
                set(id, m)
                detach(id, to: .off)
                return
            case .uploadFirst:
                deps.reupload(id)
                uplink.close()
                m.socket = .down
                m.interim = nil
                set(id, m)
                scheduleRetry(id)
                return
            case .retry:
                if code == "bad_frame" { badFrames += 1 }
                if badFrames >= MeetingTranscriptionRule.maxBadFrames {
                    m.stopped = code
                    set(id, m)
                    detach(id, to: .off)
                    return
                }
                uplink.close()
                m.socket = .down
                m.interim = nil
                set(id, m)
                scheduleRetry(id)
                return
            }
        case .other:
            return
        }
        set(id, m)
    }

    private func closed(_ id: UUID, status: Int?, uplink: MeetingAudioUplink, generation gen: Int) {
        uplink.dropped(generation: gen)
        var m = meetings[id] ?? Meeting()
        m.interim = nil
        if m.socket == .ending {
            set(id, m)
            detach(id, to: .ended)
            return
        }
        switch MeetingTranscriptionRule.reaction(toClose: status) {
        case .stop:
            m.stopped = "http_\(status ?? 0)"
            set(id, m)
            detach(id, to: .off)
        case .retry, .uploadFirst:
            m.socket = .down
            set(id, m)
            scheduleRetry(id)
        }
    }

    // MARK: Persona speaking

    /// Persona's `voice` began (`on`) or stopped saying something at `at` on the recording's clock, saying `text`: the
    /// hub holds back the sentences of an open stretch until it closes, then marks Persona's echo by its rule.
    func speaking(_ id: UUID, voice: Voice, on: Bool, at: Double, text: String?) {
        var open = speakingOpen[id] ?? [:]
        let wasOpen = !open.isEmpty
        if on { open[voice] = RecordingSpan(start: at, end: at, text: text) } else { open[voice] = nil }
        speakingOpen[id] = open.isEmpty ? nil : open
        guard wasOpen != !open.isEmpty, let uplink, uplink.recording == id,
              [.connecting, .ready].contains(meetings[id]?.socket) else { return }
        uplink.send(text: MeetingAudioOut.speaking(on: !open.isEmpty, at: at, text: text))
    }

    // MARK: Stop

    /// The recording is stopping (`RecordingCapture.onStopping`): `end` goes on an open socket after the last audio, and
    /// the hub's last lines come within 15 seconds before it closes; a socket that isn't up closes now (what the hub lacks
    /// goes up as gaps).
    func ended(_ id: UUID) {
        speakingOpen[id] = nil
        guard let uplink, uplink.recording == id else { return }
        var m = meetings[id] ?? Meeting()
        guard m.socket == .ready, uplink.end() else {
            detach(id, to: m.decided?.mode == .hub ? .ended : .off)
            return
        }
        m.socket = .ending
        set(id, m)
        endTask?.cancel()
        let wait = deps.wait
        endTask = Task { [weak self] in
            await wait(Self.endWait)
            guard !Task.isCancelled, let self, self.meetings[id]?.socket == .ending else { return }
            self.detach(id, to: .ended)
        }
    }

    /// How long the phone waits after `end` for the hub's last lines and its close (the hub's 15 seconds, and some).
    static let endWait: Duration = .seconds(20)

    // MARK: What `GET …/live` says

    /// `transcription` and `interim` of a `GET …/live` read (`MeetingConversation`); `read`: the parts it asked for, of
    /// which an absent one is the hub's word (no mode decided, a manager without it; no caption now).
    func liveRead(_ id: UUID, transcription: MeetingTranscriptionView?, interim: MeetingInterim?, read: Set<String>) {
        var m = meetings[id] ?? Meeting()
        if read.contains("transcription") || transcription != nil { m.hub = transcription }
        // The socket's own frames are fresher while it is up.
        if m.socket != .ready, read.contains("interim") || interim != nil { m.interim = interim }
        set(id, m)
    }

    /// Reads the live log from `from` (`GET …/live?parts=lines&from=`): the positions the socket missed while away.
    private func readLines(_ id: UUID, from start: Int) async {
        guard !readingLines.contains(id), let api = deps.api() else { return }
        readingLines.insert(id)
        defer { readingLines.remove(id) }
        var from = start
        for _ in 0..<20 {
            let data: Data
            do {
                data = try await api.request("GET", MeetingAudioEndpoint.path(id, "live"), query: ["parts": "lines", "from": String(from)])
            } catch {
                return
            }
            guard let read = try? JSON.decoder.decode(LinesRead.self, from: data) else { return }
            var m = meetings[id] ?? Meeting()
            m.lines = Self.merged(m.lines, read.lines)
            set(id, m)
            guard read.more, let last = read.lines.last, last.n >= from else { return }
            from = last.n + 1
        }
    }

    private nonisolated struct LinesRead: Decodable, Sendable {
        var lines: [MeetingHubLine]
        var more: Bool

        enum CodingKeys: String, CodingKey { case lines, more }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            let raw = (try? c.decodeIfPresent([JSONValue].self, forKey: .lines)) ?? []
            lines = raw.compactMap { item in
                guard case .object(let o) = item, let n = MeetingAudioIn.position(o["n"]), let start = o["start"]?.meetingNumber,
                      let end = o["end"]?.meetingNumber, let text = o["text"]?.meetingText else { return nil }
                return MeetingHubLine(n: n, start: start, end: end, text: text, source: o["source"]?.meetingText)
            }
            more = (try? c.decodeIfPresent(Bool.self, forKey: .more)) ?? false
        }
    }

    /// `lines` with `fresh` in their places, by position (a position read again replaces what was there).
    nonisolated static func merged(_ lines: [MeetingHubLine], _ fresh: [MeetingHubLine]) -> [MeetingHubLine] {
        guard !fresh.isEmpty else { return lines }
        var byN = Dictionary(lines.map { ($0.n, $0) }, uniquingKeysWith: { _, b in b })
        for line in fresh { byN[line.n] = line }
        return byN.values.sorted { $0.n < $1.n }
    }

    /// How many positions from 0 the phone holds without a hole.
    nonisolated static func contiguous(_ lines: [MeetingHubLine]) -> Int {
        var n = 0
        for line in lines.sorted(by: { $0.n < $1.n }) {
            guard line.n == n else { break }
            n += 1
        }
        return n
    }

    // MARK: Wi-Fi only

    func wifiOnlyChanged(_ on: Bool) {
        if wifiOnly != on { wifiOnly = on }
        kickGaps()
    }

    // MARK: Gaps after the meeting

    /// Looks at the meetings that ended in hub mode and uploads what their hub lacks, one pass at a time.
    func kickGaps() {
        guard gapTask == nil else { gapsAgain = true; return }
        gapTask = Task { [weak self] in
            guard let self else { return }
            repeat {
                self.gapsAgain = false
                await self.gapPass()
            } while self.gapsAgain
            self.gapTask = nil
        }
    }

    /// Whether `entry` may still need gap uploads (pure, for the tests): a room meeting recorded here that asked for hub
    /// mode, ended, its last revision (the `complete: true` one) stored, within the hub's retention (the phone keeps its
    /// audio at least that long, and never longer), not known to be done or transcribed on the phone.
    nonisolated static func needsGaps(_ entry: RecordingEntry, meeting: Meeting?, expired: Bool, synced: Bool, done: Bool) -> Bool {
        guard !entry.active, entry.roomMeeting, entry.attendance?.transcription == .hub, !expired, !done else { return false }
        if let mode = meeting?.mode, mode != .hub { return false }
        if meeting?.hub?.settled == true { return false }
        return entry.transcribedAll && synced
    }

    private func gapPass() async {
        let due = library.entries.filter {
            Self.needsGaps($0, meeting: meetings[$0.id], expired: deps.expired($0), synced: deps.synced($0), done: gapsDone.contains($0.id))
        }
        for entry in due { await uploadGaps(entry) }
    }

    private func uploadGaps(_ entry: RecordingEntry) async {
        let id = entry.id
        guard let api = deps.api() else { return }
        guard MeetingTranscriptionRule.mayStream(wifiOnly: wifiOnly, network: deps.network()) else {
            setGapUpload(id, .waitingForWiFi)
            return
        }
        // The hub's word first: its mode, its state and what it lacks.
        let view: MeetingTranscriptionView?
        do {
            let data = try await api.request("GET", MeetingAudioEndpoint.path(id, "live"), query: ["parts": "transcription"])
            let value = try? JSON.decoder.decode(JSONValue.self, from: data)
            view = MeetingTranscriptionView(value?["transcription"])
        } catch let error as APIError where [404, 410].contains(error.status) {
            return finishGaps(id)
        } catch {
            return setGapUpload(id, .later(nil))
        }
        var m = meetings[id] ?? Meeting()
        m.hub = view
        set(id, m)
        guard let view, view.mode == .hub, !view.settled else { return finishGaps(id) }
        var gaps = view.gaps
        var tried: [MeetingGapUpload.Piece] = []
        var most = MeetingGapUpload.maxSeconds
        setGapUpload(id, .uploading)
        while let piece = MeetingGapUpload.plan(gaps, held: entry.duration, maxSeconds: most).first(where: { !tried.contains($0) }) {
            guard MeetingTranscriptionRule.mayStream(wifiOnly: wifiOnly, network: deps.network()) else {
                return setGapUpload(id, .waitingForWiFi)
            }
            tried.append(piece)
            guard let audio = await deps.audio(entry, piece), !audio.data.isEmpty else { continue }
            do {
                let data = try await api.putBytes(MeetingAudioEndpoint.path(id, "audio"), query: ["at": MeetingAudioClock.query(audio.at)],
                                                  data: audio.data)
                let value = try? JSON.decoder.decode(JSONValue.self, from: data)
                gaps = MeetingGap.list(value?["gaps"])
                var m = meetings[id] ?? Meeting()
                m.gaps = gaps
                m.hub?.gaps = gaps
                set(id, m)
            } catch let error as APIError {
                switch MeetingGapUpload.reaction(status: error.status, code: error.code) {
                case .done:
                    return finishGaps(id)
                case .uploadFirst:
                    deps.reupload(id)
                    return setGapUpload(id, .later(error.code))
                case .smaller:
                    guard most > 10 else { return finishGaps(id) }
                    most /= 2
                    tried.removeAll { $0 == piece }
                case .later:
                    return setGapUpload(id, .later(error.code))
                }
            } catch {
                return setGapUpload(id, .later(nil))
            }
        }
        finishGaps(id)
    }

    private func finishGaps(_ id: UUID) {
        gapsDone.insert(id)
        setGapUpload(id, .done)
    }

    private func setGapUpload(_ id: UUID, _ state: GapUpload) {
        var m = meetings[id] ?? Meeting()
        m.gapUpload = state
        set(id, m)
    }

    private func set(_ id: UUID, _ m: Meeting) {
        if meetings[id] != m { meetings[id] = m }
        if m.hubMode != hubMeetings.contains(id) {
            if m.hubMode { hubMeetings.insert(id) } else { hubMeetings.remove(id) }
        }
    }

    // MARK: Probe

    /// What a UI test reads (`probe.meeting-transcription`, `-probes YES`): the recording the uplink follows.
    nonisolated struct Probe: Encodable, Equatable, Sendable {
        var recording: String?
        var asked: String?
        var mode: String?
        var socket: String
        var lines: Int
        var interim: String?
        var problem: String?
        var stopped: String?
        var fellBack: Bool
        var phoneLines: Bool
    }

    var probe: Probe {
        guard let id = current else { return Probe(socket: Socket.off.rawValue, lines: 0, fellBack: false, phoneLines: true) }
        let m = meeting(id)
        let shown = shown(id, role: library.entry(id)?.attendance?.role ?? .off)
        return Probe(recording: id.uuidString.lowercased(), asked: m.asked?.rawValue, mode: m.mode?.rawValue, socket: m.socket.rawValue,
                     lines: m.lines.count, interim: m.interim?.text, problem: m.problem, stopped: m.stopped,
                     fellBack: shown.fellBack, phoneLines: shown.phoneLines)
    }
}

private extension MeetingTranscription.Meeting {
    func with(socket: MeetingTranscription.Socket) -> Self {
        var m = self
        m.socket = socket
        return m
    }
}

/// Where the audio socket and the gap uploads go: `GET /api/v1/recordings/{id}/audio` upgraded to a WebSocket through the
/// app's way to the hub (`APIClient.url`, its tunnel when it uses one), with the device's token, as `VoiceEndpoint` opens
/// the voice extension's socket.
nonisolated enum MeetingAudioEndpoint {
    static func path(_ id: UUID, _ route: String) -> String { "/api/v1/recordings/\(id.uuidString.lowercased())/\(route)" }

    static func url(_ api: APIClient, recording: UUID) -> URL {
        let http = api.url(path(recording, "audio"))
        guard var comps = URLComponents(url: http, resolvingAgainstBaseURL: false) else { return http }
        comps.scheme = comps.scheme?.lowercased() == "https" ? "wss" : "ws"
        return comps.url ?? http
    }

    static func request(_ api: APIClient, recording: UUID) -> URLRequest {
        var req = URLRequest(url: url(api, recording: recording), timeoutInterval: 15)
        api.config.authorize(&req)
        return req
    }
}

extension MeetingTranscription.Deps {
    /// The app's: the hub-level group's hub (recordings sync with it), the voice call's socket kind, the recorder's tap.
    static var live: Self {
        Self(api: { AppModel.shared.api },
             socket: { request, api in HubVoiceSocket(request: request, session: api.session) },
             network: { MeetingNetwork.shared.kind },
             listen: { sink in RecordingCapture.shared.listenUplink(sink) },
             reupload: { RecordingSync.shared.reupload($0) },
             wait: { try? await Task.sleep(for: $0) },
             audio: { entry, piece in
                 let folder = RecordingLibrary.shared.folder(entry.id), segments = entry.segments
                 return await Task.detached(priority: .utility) {
                     MeetingSegmentAudio.pcm(folder: folder, segments: segments, at: piece.at, seconds: piece.seconds)
                 }.value
             },
             expired: { RecordingSync.shared.isExpired($0) },
             synced: { RecordingSync.shared.status($0) == .synced })
    }
}

#if DEBUG
import SwiftUI

/// `probe.meeting-transcription` (`-probes YES`): the transcription store as a UI test reads it (`MeetingTranscription.probe`).
struct MeetingTranscriptionProbe: View {
    var body: some View {
        Color.clear
            .frame(width: Theme.Size.hairline, height: Theme.Size.hairline)
            .allowsHitTesting(false)
            .screenProbe("meeting-transcription", MeetingTranscription.shared.probe)
    }
}
#endif
#endif
