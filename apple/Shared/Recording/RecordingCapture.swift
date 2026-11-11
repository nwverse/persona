#if os(iOS) || os(visionOS) || os(watchOS)
import AVFoundation
import Observation
#if os(iOS) || os(visionOS)
import UIKit
#else
import WatchKit
#endif

/// Hourhand's phone / watch session management, sharing the segment engine, pausing, bookmarks and resource protection.
///
/// On the watch it also runs all-day recording (`start(day:)`, docs/assistant/day-recap.md): one recording per local
/// day, cut at midnight without closing the microphone, paused and continued within the day under the same id and the
/// next seq. Every finished segment is registered in the recording its folder names, in the order the writer finished
/// it (`applyWriterEvents`), so a midnight never files one day's audio under the other.
@Observable
final class RecordingCapture {
    static let shared = RecordingCapture()
    enum State { case idle, starting, recording, paused }
    private(set) var state: State = .idle
    private(set) var elapsed: Double = 0
    private(set) var recordingID: UUID?
    private(set) var rolling = false
    /// All-day recording is on: recording, or paused within the day (by the user, a call, a failed resume).
    private(set) var allDay = false
    /// The day recording being recorded or paused; nil outside all-day recording.
    var dayRecordingID: UUID? { allDay ? recordingID : nil }
    var error: String?
    /// Non-blocking hint, e.g. the Bluetooth microphone was not found and the phone mic is used.
    private(set) var notice: String?
    var onSaved: ((UUID) -> Void)?
    /// A recording is about to stop, however it stops (Stop, low storage, a failed write, the audio service reset): while
    /// its clock still runs, so whatever follows it (Persona's voice in a meeting, #2981) can end at the right time.
    @ObservationIgnored var onStopping: ((UUID) -> Void)?
    /// Paused by the system (a phone call, Siri, an alarm), not by the user: it picks up again when the system ends the
    /// interruption, or when the app comes back to the front (`resumeAfterInterruption`).
    private(set) var interrupted = false
    /// The system ended an interruption but the microphone could not be opened again (the app was in the background): the
    /// phone tells the user to come back (a notification), since nothing else will resume it.
    @ObservationIgnored var onInterruptionNotResumed: (() -> Void)?

    // MARK: All-day hooks

    /// UserDefaults Bool: the user wants all-day recording on. Set when it starts, cleared only by the user's Stop, so a
    /// launch can tell a day the app lost (`haltedOnLaunch`) from one the user ended.
    nonisolated static let allDayIntentKey = "recording.allDay.on"
    /// All-day recording stopped recording without the user (a call, a failed resume, the microphone changing, the audio
    /// service resetting, low battery or storage, a failed write): the watch tells the user and offers to continue.
    @ObservationIgnored var onDayHalted: ((RecordingDayHalt) -> Void)?
    /// All-day recording records again after a pause or a halt.
    @ObservationIgnored var onDayResumed: (() -> Void)?
    /// A day recording has something new to send (its id): a chunk, the end of its day, an erase.
    @ObservationIgnored var onDayChunk: ((UUID) -> Void)?
    /// A day recording left open while all-day was on, found at launch: the app ended with it (a crash, the system).
    /// Kept until the watch's all-day controller takes it, after installing `onDayHalted` (`takeHaltedOnLaunch`).
    @ObservationIgnored private(set) var haltedOnLaunch: (id: UUID, reason: RecordingDayHalt)?
    /// Whether the phone takes messages now, pushed in by the day transfer when it changes (never read from the
    /// WatchConnectivity session on every tick): day chunks are short while it does, so the text follows the voice, and
    /// long while they wait for it.
    @ObservationIgnored var linkReachable = false {
        didSet { if allDay, oldValue != linkReachable { writer?.segmentLength = chunkLength } }
    }

    /// The recorded time now, read without observing it (`elapsed` changes four times a second).
    var timeline: Double { writer?.elapsed ?? sampleElapsed }
    @ObservationIgnored private var sampleElapsed: Double = 0
    /// The microphone's recent level, 0...1 (the writer's smoothed, perceptual level, read-only), for the live page's meter.
    /// Not observed: a meter reads it from its own clock, many times a second.
    var inputLevel: Double {
        #if DEBUG
        if writer == nil { return sampleLevel }
        #endif
        return Double(writer?.level ?? 0)
    }
    #if DEBUG
    /// Screenshots and the DEBUG flow (`-showMeetingSample`): the level a recording with no microphone shows.
    @ObservationIgnored var sampleLevel: Double = 0
    #endif
    @ObservationIgnored private var input: RecordingInput = .phone
    /// Persona may answer out loud during this recording (#2981: a meeting it takes part in). The phone's microphone
    /// session then also lets a Bluetooth headset play (A2DP is listening only, so the recording keeps its microphone);
    /// a wired headset and the loudspeaker play anyway. Off for every other recording, whose session stays as it was.
    @ObservationIgnored private(set) var playsAloud = false
    @ObservationIgnored private var writer: RecordingSegmentWriter?
    @ObservationIgnored private var ticker: Task<Void, Never>?
    @ObservationIgnored private var observers: [NSObjectProtocol] = []
    @ObservationIgnored private var sessionObserver: RecordingSessionObserver?
    @ObservationIgnored private var generation = UUID()
    @ObservationIgnored private var activeMicOwner: UUID?
    @ObservationIgnored private var resumeTask: Task<Void, Never>?

    // All-day state, main thread only.
    /// The midnight the writer cuts at next, and the recording named for after it.
    @ObservationIgnored private var dayPlan: DayPlan?
    /// Day recordings named in advance and not made yet (the next day's, made with its first sample).
    @ObservationIgnored private var pendingDays: [UUID: PendingDay] = [:]
    /// Why audio was missing before the next chunk of each day recording (`RecordingSegment.after`), until a chunk
    /// recorded after that gap is saved.
    @ObservationIgnored private var pendingGaps: [UUID: [PendingGap]] = [:]
    /// What the writer reported that could not be saved yet (recording.json failed to write, the next day's entry could
    /// not be made), in order. Tried again before anything newer, and kept across a stop: the writer's log no longer
    /// has it, and its audio is on disk without a record until it is saved.
    @ObservationIgnored private var unregistered: [HeldEvent] = []

    private init() {
        let recovered = RecordingLibrary.shared.recover(source: Self.source)
        // Here rather than as a callback: nothing can have installed `onDayHalted` this early (capture is made on first use).
        haltedOnLaunch = Self.launchHalt(recovered: recovered, entries: RecordingLibrary.shared.entries,
                                         intent: UserDefaults.standard.bool(forKey: Self.allDayIntentKey))
        sessionObserver = RecordingSessionObserver { [weak self] event in self?.handle(event) }
        let center = NotificationCenter.default
        observers.append(center.addObserver(forName: .AVAudioEngineConfigurationChange, object: nil, queue: .main) { @Sendable [weak self] note in
            let engine = note.object.map { ObjectIdentifier($0 as AnyObject) }
            MainActor.assumeIsolated {
                // Every engine posts it (dictation's, a voice call's): only this recording's own engine matters.
                guard let self, self.state == .recording, self.writer?.owns(engine) == true else { return }
                do { try self.writer?.restartAfterConfigurationChange() }
                catch { self.microphoneChangeFailed() }
            }
        })
        observers.append(center.addObserver(forName: AVAudioSession.mediaServicesWereResetNotification, object: nil, queue: .main) { @Sendable [weak self] _ in
            MainActor.assumeIsolated {
                // Paused too (during a call): the engine it would resume is gone with the reset.
                guard let self, self.state == .recording || self.state == .paused else { return }
                self.haltBySystem(.audioReset, error: String(localized: "The system audio service reset. What was finished is saved; start recording again."))
            }
        })
        // The date, the clock or the time zone changed. watchOS has no significant-time-change notification, and a day
        // notification can come late (on wake), so the day check runs again before every continue too.
        for name in [Notification.Name.NSSystemTimeZoneDidChange, .NSSystemClockDidChange, .NSCalendarDayChanged] {
            observers.append(center.addObserver(forName: name, object: nil, queue: .main) { @Sendable [weak self] _ in
                MainActor.assumeIsolated { self?.dayClockChanged() }
            })
        }
    }

    private static var source: RecordingEntry.Source {
        #if os(watchOS)
        .watch
        #else
        .phone
        #endif
    }

    private func configureSession(token: UUID) async throws {
        let session = AVAudioSession.sharedInstance()
        guard generation == token else { return }
        notice = nil
        #if os(iOS) || os(visionOS)
        #if os(iOS)
        if input == .bluetooth {
            try session.setCategory(.playAndRecord, mode: .default, options: [.allowBluetoothHFP])
            try session.setActive(true)
            if let port = await bluetoothInput(session, token: token) {
                guard generation == token else { return }
                try session.setPreferredInput(port)
                return
            }
            guard generation == token else { return }
            notice = String(localized: "No glasses or Bluetooth headset microphone found; using the phone's microphone")
        }
        #endif
        try session.setCategory(.playAndRecord, mode: .default, options: Self.phoneOptions(playsAloud: playsAloud))
        try session.setActive(true)
        #if os(iOS)
        let ports = session.availableInputs ?? []
        if let index = RecordingInput.preferredIndex(ports.map(\.portType), for: .phone) {
            try session.setPreferredInput(ports[index])
        }
        #else
        if let mic = session.availableInputs?.first(where: { $0.portType == .builtInMic }) { try session.setPreferredInput(mic) }
        #endif
        #else
        try session.setCategory(.record, mode: .default)
        try session.setActive(true)
        #endif
    }

    #if os(iOS) || os(visionOS)
    /// The phone microphone's session options: the loudspeaker rather than the earpiece, and a Bluetooth headset for
    /// listening when Persona may speak (`playsAloud`).
    static func phoneOptions(playsAloud: Bool) -> AVAudioSession.CategoryOptions {
        #if os(iOS)
        playsAloud ? [.defaultToSpeaker, .allowBluetoothA2DP] : [.defaultToSpeaker]
        #else
        [.defaultToSpeaker]
        #endif
    }

    /// Persona started or stopped taking part while recording: the same session, with or without the headset for
    /// listening. A Bluetooth microphone's session plays through that headset already.
    func setPlaysAloud(_ on: Bool) {
        guard playsAloud != on else { return }
        playsAloud = on
        guard writer != nil, input == .phone else { return }
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .default, options: Self.phoneOptions(playsAloud: on))
    }
    #endif

    #if os(iOS)
    /// The Bluetooth HFP input appears only after the route settles, so poll for up to two seconds.
    private func bluetoothInput(_ session: AVAudioSession, token: UUID) async -> AVAudioSessionPortDescription? {
        var waited = 0
        while generation == token, !Task.isCancelled {
            let ports = session.availableInputs ?? []
            if let index = RecordingInput.preferredIndex(ports.map(\.portType), for: .bluetooth) { return ports[index] }
            guard waited < 10 else { return nil }
            do { try await Task.sleep(for: .milliseconds(200)) } catch { return nil }
            waited += 1
        }
        return nil
    }

    /// Name of the input the current route records from, shown while recording.
    var inputName: String? {
        guard state == .recording || state == .paused else { return nil }
        return AVAudioSession.sharedInstance().currentRoute.inputs.first?.portName
    }
    #endif

    private static func permission() async -> Bool {
        if #available(iOS 17.0, watchOS 10.0, *) { return await AVAudioApplication.requestRecordPermission() }
        return await withCheckedContinuation { continuation in
            AVAudioSession.sharedInstance().requestRecordPermission { @Sendable allowed in continuation.resume(returning: allowed) }
        }
    }

    /// `title` / `meeting`: what the recording is called and, for a meeting recording, its meeting (docs/apple/recording.md).
    /// `playsAloud`: Persona takes part in this meeting and may answer out loud.
    /// `day`: all-day recording — goes on in today's open day recording (its next seq) or starts one; the consent and
    /// the notification permission are the caller's to have asked for.
    func start(locale: String = "zh-CN", input: RecordingInput = .phone, title: String = "", meeting: RecordingMeeting? = nil, rolling: Bool = false,
               playsAloud: Bool = false, day: Bool = false) async {
        guard state == .idle else { return }
        generation = UUID(); let token = generation
        // One mode at a time: an all-day recording is never also a rolling one.
        let rolling = rolling && !day
        state = .starting; error = nil; notice = nil; self.rolling = rolling
        self.input = input
        self.playsAloud = playsAloud
        let allowed = await Self.permission()
        guard generation == token, state == .starting else { return }
        guard allowed else { failStarting(String(localized: "Allow Persona to use the microphone in Settings")); return }
        if let problem = Self.resourceProblem(starting: true) { failStarting(problem.message); return }
        let micOwner = UUID()
        guard RecordingMicrophone.shared.claim(micOwner) else {
            failStarting(String(localized: "The microphone is in use for dictation. Finish dictating before recording.")); return
        }
        activeMicOwner = micOwner
        do {
            try await configureSession(token: token)
            // Cancelled while Bluetooth discovery was running: cancelStarting() already released the session and mic.
            guard generation == token else { return }
            // Audio an earlier recording finished but could not save: before its recording's next seq is chosen.
            if !unregistered.isEmpty { applyWriterEvents() }
            let zone = Self.zone
            let opened: (entry: RecordingEntry, after: String?)
            if day {
                opened = try openDay(locale: locale, now: .now, zone: zone)
            } else {
                let created = try RecordingLibrary.shared.create(source: Self.source, locale: locale, title: title, meeting: meeting,
                                                                 kind: rolling ? .rolling : .clip)
                opened = (created, nil)
            }
            let entry = opened.entry
            recordingID = entry.id
            let writer = RecordingSegmentWriter(directory: RecordingLibrary.shared.folder(entry.id), firstIndex: day ? nextIndex(of: entry) : 0)
            writer.segmentLength = day ? Self.dayChunkLength(reachable: linkReachable) : Self.segmentLength
            connect(writer)
            // Before the first buffer, so a start a moment before midnight still cuts on the exact sample.
            if day { planBoundary(id: entry.id, day: entry.day, locale: entry.locale, writer: writer, zone: zone) }
            let opening = Date.now
            try writer.start()
            self.writer = writer
            elapsed = 0; state = .recording
            if day { beganDay(entry.id, after: opened.after, since: opening) }
            startTicker()
        } catch {
            guard generation == token else { return }
            if let id = recordingID {
                if day {
                    // The day stays open for the next start. The time until then was not the user's choice: a day made
                    // by this start (no reason yet) gets "stopped", so its next chunk is not sent as a pause; a day
                    // already paused or interrupted keeps the reason its gap began with.
                    RecordingLibrary.shared.change(id) { $0.gapReason = $0.gapReason ?? RecordingDayContract.Reason.stopped }
                } else {
                    RecordingLibrary.shared.change(id) { $0.active = false; $0.expectedSegments = 0 }
                }
            }
            recordingID = nil; state = .idle; self.rolling = false; dayPlan = nil
            releaseAudioSession()
            self.error = String(localized: "Can't open the microphone. Try again later.")
        }
    }

    /// A start that never reached the microphone.
    private func failStarting(_ message: String) {
        state = .idle; rolling = false; error = message
    }

    func cancelStarting() {
        guard state == .starting else { return }
        generation = UUID(); state = .idle; notice = nil; rolling = false
        releaseAudioSession()
    }

    func togglePause() {
        #if DEBUG
        if writer == nil, recordingID != nil, sampleElapsed > 0 {
            if state == .recording { state = .paused } else if state == .paused { state = .recording }
            return
        }
        #endif
        guard let writer else { return }
        if state == .recording {
            interrupted = false
            resumeTask?.cancel(); resumeTask = nil
            writer.pause(); state = .paused
            // What was said until now becomes a chunk, so it reaches the phone during the pause.
            if allDay { pauseDay(gap: RecordingDayContract.Reason.paused) }
        } else if state == .paused {
            if allDay { reconcileDay() }
            resume(writer)
        }
    }

    /// After a phone call, or when the app comes back to the front while one left the recording paused: carries on.
    /// Nothing when the user paused it.
    func resumeAfterInterruption() {
        guard interrupted, state == .paused, let writer else { return }
        if allDay { reconcileDay() }
        resume(writer)
    }

    private func resume(_ writer: RecordingSegmentWriter) {
        guard resumeTask == nil else { return }
        let token = generation
        resumeTask = Task { @MainActor [weak self] in
            defer { self?.resumeTask = nil }
            guard let self else { return }
            do {
                try await self.configureSession(token: token)
                guard self.generation == token, self.writer === writer, self.state == .paused else { return }
                let opening = Date.now
                try writer.resume()
                self.state = .recording; self.interrupted = false; self.error = nil
                if self.allDay { self.dayResumed(since: opening) }
            } catch {
                guard self.generation == token else { return }
                self.error = String(localized: "Can't open the microphone; the recording is still saved")
                // Still `interrupted`: coming back to the app tries again.
                if self.interrupted { self.onInterruptionNotResumed?() }
                if self.allDay { self.onDayHalted?(.resumeFailed) }
            }
        }
    }

    /// Closes the segment being written and goes on in a new one, so what was just said is transcribed now rather than
    /// at the segment's end (#2981: asking Persona about it).
    func flushSegment() {
        guard state == .recording, let writer, writer.cut() != nil else { return }
        applyWriterEvents()
    }

    /// A bookmark. In all-day recording it is on the wall clock (`RecordingDayMark`); otherwise on the timeline.
    func addMark() {
        if allDay {
            addDayMark(RecordingDayMark.bookmark)
            return
        }
        #if DEBUG
        if writer == nil, let id = recordingID, sampleElapsed > 0 {
            let time = sampleElapsed
            RecordingLibrary.shared.change(id) { entry in
                if let last = entry.marks.last, time - last < 1 { return }
                entry.marks.append(time)
            }
            return
        }
        #endif
        guard let id = recordingID, let writer else { return }
        let time = writer.elapsed
        RecordingLibrary.shared.change(id) { entry in
            if let last = entry.marks.last, time - last < 1 { return }
            entry.marks.append(time)
        }
    }

    /// All-day recording: a meeting is going on now. The one microphone can't start a meeting recording beside the day's,
    /// so the day gets a wall-clock mark the recap reads as a meeting. Nothing outside all-day recording.
    func markMeeting() {
        guard allDay else { return }
        addDayMark(RecordingDayMark.meeting)
    }

    /// Erases the last few minutes. A rolling recording erases them everywhere and drops the segment being written when it
    /// finalizes; all-day recording (on, or off with today's day still open) cuts first (`eraseDayRecent`).
    func eraseRecent() {
        if rolling, let id = recordingID {
            let now = Date.now
            RecordingLibrary.shared.erase(id, from: now.addingTimeInterval(-RecordingRolling.eraseMinutes * 60), to: now)
            return
        }
        guard allDay || state == .idle else { return }
        eraseDayRecent()
    }

    /// `reason`: the user's Stop, or the system's (the audio service reset, a failed write, low storage or battery). An
    /// all-day recording's day stays open either way (`endDaySession`). The user's Stop while nothing records (all-day
    /// recording stopped by itself and not continued) still ends their wish to record all day, so the watch stops
    /// offering to continue.
    @discardableResult func stop(reason: RecordingStopReason = .user) -> UUID? {
        if state == .starting { cancelStarting(); return nil }
        #if DEBUG
        if writer == nil, let id = recordingID, sampleElapsed > 0 { return stopSample(id) }
        #endif
        guard let writer, let started = recordingID else {
            if reason == .user, state == .idle { Self.rememberIntent(false) }
            return nil
        }
        onStopping?(started)
        generation = UUID()
        resumeTask?.cancel(); resumeTask = nil
        ticker?.cancel(); ticker = nil
        writer.stop()
        // Everything finished that the main thread has not registered yet (a stop on a segment boundary leaves no tail; a
        // midnight may have been crossed), in order and each in its own recording, before the end is written.
        applyWriterEvents()
        let id = recordingID ?? started
        if allDay {
            endDaySession(id, reason: reason)
        } else {
            // A segment still waiting for its save is counted: the end never leaves out audio that exists.
            let expected = heldNextIndex(id)
            RecordingLibrary.shared.change(id) { entry in
                entry.active = false
                entry.expectedSegments = max(entry.nextIndex, expected)
                entry.endedAt = .now
            }
        }
        self.writer = nil; recordingID = nil; state = .idle; interrupted = false; notice = nil; rolling = false; playsAloud = false
        allDay = false; dayPlan = nil
        forgetDayPlans()
        releaseAudioSession()
        onSaved?(id)
        return id
    }

    /// The launch halt, once: the watch's all-day controller takes it after installing `onDayHalted`.
    func takeHaltedOnLaunch() -> (id: UUID, reason: RecordingDayHalt)? {
        defer { haltedOnLaunch = nil }
        return haltedOnLaunch
    }

    private func releaseAudioSession() {
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setPreferredInput(nil)
        #endif
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        if let owner = activeMicOwner {
            activeMicOwner = nil
            RecordingMicrophone.shared.release(owner)
        }
    }

    private func startTicker() {
        ticker = Task { [weak self] in
            let clock = ContinuousClock()
            var checked = clock.now
            while !Task.isCancelled {
                // Hours in the background (all-day recording): a slower tick there, with the failure checks only.
                let foreground = self?.isAppActive ?? true
                do { try await Task.sleep(for: foreground ? .milliseconds(250) : .seconds(1)) } catch { return }
                guard let self, let writer = self.writer else { return }
                if self.isAppActive { self.elapsed = writer.elapsed }
                writer.segmentLength = self.chunkLength
                // A save that failed gets one more try; still failing, it halts as any failed write does (the stop tries again).
                if !self.unregistered.isEmpty { self.applyWriterEvents() }
                if let failure = writer.failure ?? RecordingLibrary.shared.error ?? self.registrationFailure {
                    self.haltBySystem(.writeFailed, error: failure); return
                }
                if checked.duration(to: clock.now) >= .seconds(5) {
                    checked = clock.now
                    if let problem = Self.resourceProblem(starting: false) { self.haltBySystem(problem.halt, error: problem.message); return }
                }
            }
        }
    }

    /// Finished audio still without a record after another try.
    private var registrationFailure: String? {
        unregistered.isEmpty ? nil : String(localized: "The recording info wasn't saved. Keep the app installed and try again.")
    }

    /// Whether the app is in front. In the background nothing shows `elapsed`, so the watch skips redrawing it there; the
    /// phone keeps its timing as it was.
    private var isAppActive: Bool {
        #if os(watchOS)
        WKApplication.shared().applicationState == .active
        #else
        true
        #endif
    }

    /// The system ended the recording: what was finished is saved, an all-day recording stays on (to continue), and the
    /// watch is told why.
    private func haltBySystem(_ reason: RecordingDayHalt, error message: String) {
        let day = allDay
        stop(reason: .system)
        error = message
        if day { onDayHalted?(reason) }
    }

    // MARK: - Audio session events

    private func handle(_ event: RecordingSessionEvent) {
        switch Self.reaction(to: event, recording: state == .recording && writer != nil, pausedByInterruption: interrupted && state == .paused) {
        case .pause: interruptionBegan()
        case .resume: resumeAfterInterruption()
        case .ignore: break
        }
    }

    /// What a recording does with an audio session event: an interruption pauses a running recording, and its end resumes
    /// one it paused, whatever the system advises (the meeting goes on). Our own deactivation and activation, which the
    /// 27 messages report too, are not interruptions.
    nonisolated enum SessionReaction: Equatable, Sendable { case ignore, pause, resume }

    nonisolated static func reaction(to event: RecordingSessionEvent, recording: Bool, pausedByInterruption: Bool) -> SessionReaction {
        switch event {
        case .interrupted: recording ? .pause : .ignore
        case .interruptionEnded: pausedByInterruption ? .resume : .ignore
        case .deactivatedByApp, .activated: .ignore
        }
    }

    private func interruptionBegan() {
        guard state == .recording, let writer else { return }
        interrupted = true
        resumeTask?.cancel(); resumeTask = nil
        writer.pause(); state = .paused
        // A long call: what was said before it is kept even if the app is stopped before the call ends (a segment
        // still open would be deleted by `recover`). The next segment starts when the recording resumes.
        if allDay {
            pauseDay(gap: RecordingDayContract.Reason.interrupted)
            onDayHalted?(.interrupted)
        } else {
            writer.cut()
            applyWriterEvents()
        }
        if rolling {
            error = String(localized: "Rolling recording paused — tap Continue to resume")
            #if os(watchOS)
            WKInterfaceDevice.current().play(.notification)
            #endif
        }
    }

    /// The input changed and the tap could not be made again: often the route switching to a call before its
    /// interruption arrives, so it pauses as one does, and the call's end (or coming back to the app) picks it up again.
    private func microphoneChangeFailed() {
        guard let writer else { return }
        error = String(localized: "The microphone changed, so recording paused")
        writer.pause(); state = .paused; interrupted = true
        if allDay {
            pauseDay(gap: RecordingDayContract.Reason.interrupted)
            onDayHalted?(.microphoneChanged)
        } else {
            writer.cut()
            applyWriterEvents()
        }
    }

    // MARK: - Registering what the writer did

    /// The writer's callbacks only wake the main thread, in the order they happened (one serial queue for both kinds);
    /// what they report is taken from the writer's log there, so a stop or a cut that runs first leaves nothing to do
    /// twice, and a late wake-up after a stop finds nothing.
    private func connect(_ writer: RecordingSegmentWriter) {
        let wake: @Sendable () -> Void = { [weak self, weak writer] in
            let capture = self, source = writer
            DispatchQueue.main.async { MainActor.assumeIsolated { capture?.writerReported(source) } }
        }
        writer.onSegment = { @Sendable _ in wake() }
        writer.onRotate = { @Sendable _, _ in wake() }
    }

    private func writerReported(_ source: RecordingSegmentWriter?) {
        guard let source, source === writer else { return }
        applyWriterEvents()
    }

    /// Registers what the writer did since the last call, in order: each segment in the recording its folder names (never
    /// `recordingID`, which a midnight moves on), each midnight closing one day and opening the next. Every path that
    /// registers segments comes here: the writer's wake-ups, cuts, pauses, erase, the day check, start and stop. What
    /// could not be saved is kept with everything after it (`unregistered`) and goes first next time; only what was
    /// saved is announced.
    /// - Parameter notify: false while an erase is half done, so nothing is sent before all of it is saved.
    /// - Returns: the recordings that changed.
    @discardableResult
    private func applyWriterEvents(notify: Bool = true) -> [UUID] {
        let fresh = writer.map { source in source.takeEvents().map { HeldEvent(event: $0, source: source) } } ?? []
        let (changed, kept) = Self.applyInOrder(unregistered + fresh, owners: \.event.owners) { apply($0) }
        unregistered = kept
        if notify { announce(changed) }
        return changed
    }

    /// One event of the writer's: the recordings it changed, or nil when it could not be saved.
    private func apply(_ held: HeldEvent) -> [UUID]? {
        switch held.event {
        case .segment(let segment):
            return register(segment)
        case .rotated(let rotation):
            // A midnight from a writer that is gone still files both days, but moves and arms nothing.
            let live = held.source != nil && held.source === writer
            return crossedMidnight(rotation, live: live)
        }
    }

    /// Applies writer events in order. One that can't be saved is kept for the next try, in order, with every later one
    /// that concerns any of its recordings (`owners`: a midnight concerns both days), so a midnight is never applied
    /// ahead of a chunk before it, nor a new day's chunk ahead of its midnight. Other recordings go on. Returns the
    /// recordings changed and what was kept.
    nonisolated static func applyInOrder<Item>(_ items: [Item], owners: (Item) -> [UUID],
                                               _ apply: (Item) -> [UUID]?) -> (changed: [UUID], kept: [Item]) {
        var changed: [UUID] = [], kept: [Item] = [], waiting: Set<UUID> = []
        for item in items {
            let concerned = owners(item)
            guard waiting.isDisjoint(with: concerned), let ids = apply(item) else {
                kept.append(item)
                waiting.formUnion(concerned)
                continue
            }
            for id in ids where !changed.contains(id) { changed.append(id) }
        }
        return (changed, kept)
    }

    /// One finished segment, saved in the recording its folder belongs to. A day chunk carries its level, why audio was
    /// missing before it, and whether it ends the day.
    /// - Returns: the recording changed; empty when there is none to keep it in (deleted meanwhile); nil when the save
    ///   failed, so it is tried again, with the gap reason it would have carried.
    private func register(_ segment: RecordingSegmentWriter.Segment, endsDay: Bool = false) -> [UUID]? {
        guard let id = segment.owner else { return [] }
        let library = RecordingLibrary.shared
        if library.entry(id) == nil, !library.isDeleted(id), !ensureDayEntry(id), pendingDays[id] != nil {
            return nil // the day named in advance could not be made yet
        }
        guard let entry = library.entry(id) else {
            // Deleted meanwhile, or a folder no recording names: there is nothing to keep it in.
            try? FileManager.default.removeItem(at: segment.url)
            return []
        }
        var record = RecordingSegment(index: segment.index, start: segment.start, duration: segment.duration, startedAt: segment.startedAt)
        var gaps: [PendingGap] = []
        if entry.isDay {
            record.level = segment.level
            (record.after, gaps) = Self.gap(endingAt: segment.endedAt, in: pendingGaps[id] ?? [])
            if endsDay { record.end = true }
        }
        library.addSegment(id, record)
        // `addSegment` reports nothing: whether the record is there now tells a failed save from a done one.
        guard Self.isSettled(record, in: library.entry(id)) else { return nil }
        if entry.isDay { pendingGaps[id] = gaps.isEmpty ? nil : gaps }
        return [id]
    }

    /// Whether the library has a finished segment now: its record is there, or it was dropped on purpose (a seq already
    /// pruned, or a clip's audio in a range the user erased). Anything else is a save that failed.
    nonisolated static func isSettled(_ record: RecordingSegment, in entry: RecordingEntry?) -> Bool {
        guard let entry else { return false }
        if entry.segments.contains(where: { $0.index == record.index }) || entry.isPruned(record.index) { return true }
        return !entry.isDay && entry.isErased(record)
    }

    /// Why audio was missing before a day recording's next chunk, from `since`: the moment the microphone opened again
    /// (or the first sample past a midnight it was off at).
    nonisolated struct PendingGap: Equatable, Sendable {
        let reason: String
        let since: Date
    }

    /// The gap a chunk ending at `end` follows: the newest that began before it ended. A chunk finished before the
    /// microphone went off, and saved only after it came back, takes none. Every gap it covers is used up (`rest` is
    /// what is left).
    nonisolated static func gap(endingAt end: Date, in gaps: [PendingGap]) -> (reason: String?, rest: [PendingGap]) {
        let covered = gaps.filter { $0.since < end }
        return (covered.max { $0.since < $1.since }?.reason, gaps.filter { $0.since >= end })
    }

    /// Notes why audio is missing before `id`'s next chunk; a midnight applied again notes its gap once.
    private func noteGap(_ id: UUID, reason: String, since: Date) {
        var gaps = pendingGaps[id] ?? []
        gaps.removeAll { $0.since == since }
        gaps.append(PendingGap(reason: reason, since: since))
        pendingGaps[id] = gaps
    }

    /// Whether audio of `id` is still waiting for its save: nothing may end that recording before it is saved, or the
    /// end marker would take its seq. Capture's own closing of days checks it.
    /// `WatchDayTransfer.closeExpiredDays` counts these ids as live too, as it does `dayRecordingID`.
    func holdsAudio(of id: UUID) -> Bool {
        unregistered.contains { $0.event.owners.contains(id) }
    }

    /// The seq after the newest segment of `id` still waiting for its save (0 when none).
    private func heldNextIndex(_ id: UUID) -> Int {
        unregistered.compactMap { $0.event.segment }.filter { $0.owner == id }.map { $0.index + 1 }.max() ?? 0
    }

    /// The seq a writer going on in `entry` starts at: past every saved record and every segment still to be saved, so
    /// it never writes over their audio.
    private func nextIndex(of entry: RecordingEntry) -> Int {
        max(entry.nextIndex, heldNextIndex(entry.id))
    }

    /// After a stop: the day's plans go, except what events still waiting for a save need (a day named in advance, the
    /// gap before a chunk).
    private func forgetDayPlans() {
        let owners = Set(unregistered.flatMap(\.event.owners))
        pendingDays = pendingDays.filter { owners.contains($0.key) }
        pendingGaps = pendingGaps.filter { owners.contains($0.key) }
    }

    private func announce(_ ids: [UUID]) {
        let library = RecordingLibrary.shared
        for id in ids {
            onSaved?(id)
            if library.entry(id)?.isDay == true { onDayChunk?(id) }
        }
    }

    // MARK: - All-day recording

    /// The day recording a start goes on in: the open one for today (or for a later date, after a flight west) with its
    /// pause's reason for the next chunk, else a new one. Open days already over here are closed first, each at its own
    /// midnight.
    private func openDay(locale: String, now: Date, zone: TimeZone) throws -> (entry: RecordingEntry, after: String?) {
        let library = RecordingLibrary.shared
        let today = RecordingDayCalendar.day(of: now, in: zone)
        closeDays(before: today, now: now, zone: zone)
        // Never a past day left open because audio of it still waits for its save.
        if let open = library.openDays(source: Self.source).first(where: { ($0.day ?? "") >= today }) {
            return (open, open.gapReason ?? RecordingDayContract.Reason.paused)
        }
        let entry = try library.create(source: Self.source, locale: locale, kind: .day, createdAt: now, day: today, timeZone: zone.identifier)
        return (entry, nil)
    }

    /// Ends the open day recordings of dates before `today` with their end marker, at their own midnight: the phone
    /// learns the day is complete, and today starts its own recording.
    private func closeDays(before today: String, now: Date, zone: TimeZone) {
        let library = RecordingLibrary.shared
        var closed: [UUID] = []
        // One with audio still waiting for its save stays open until it is saved (the next start closes it).
        for entry in library.openDays(source: Self.source) where (entry.day ?? "") < today && !holdsAudio(of: entry.id) {
            library.closeDay(entry.id, at: Self.dayEnd(of: entry, zone: zone, notAfter: now),
                             after: entry.gapReason ?? RecordingDayContract.Reason.stopped)
            closed.append(entry.id)
        }
        announce(closed)
    }

    /// Recording within the day began: all-day is on (also after a relaunch), and the first chunk recorded from `since`
    /// (when the microphone opened) says why audio was missing before it when the day went on after a gap.
    private func beganDay(_ id: UUID, after: String?, since: Date) {
        allDay = true
        Self.rememberIntent(true)
        if let after { noteGap(id, reason: after, since: since) }
        if RecordingLibrary.shared.entry(id)?.gapReason != nil { RecordingLibrary.shared.change(id) { $0.gapReason = nil } }
    }

    /// Recording again within the day, from `since`: the next chunk says why audio was missing before it, and the watch
    /// withdraws its "paused" alert.
    private func dayResumed(since: Date) {
        if let id = recordingID, let entry = RecordingLibrary.shared.entry(id) {
            noteGap(id, reason: entry.gapReason ?? RecordingDayContract.Reason.paused, since: since)
            if entry.gapReason != nil { RecordingLibrary.shared.change(id) { $0.gapReason = nil } }
        }
        onDayResumed?()
    }

    /// The microphone went off within the day: what was recorded so far becomes a chunk (an app ended before it comes
    /// back keeps it, and it reaches the phone meanwhile), and the day records why audio is missing after it.
    private func pauseDay(gap: String) {
        writer?.cut()
        applyWriterEvents()
        guard let id = recordingID, RecordingLibrary.shared.entry(id)?.gapReason != gap else { return }
        RecordingLibrary.shared.change(id) { $0.gapReason = gap }
    }

    /// The microphone goes off for now and the day stays open, to continue. The user's Stop pauses the rest of today
    /// (all-day off until started again); the system's keeps all-day on, so the watch offers to continue, and keeps the
    /// reason of a gap already under way (a pause, a call).
    private func endDaySession(_ id: UUID, reason: RecordingStopReason) {
        let library = RecordingLibrary.shared
        let gap = switch reason {
        case .user: RecordingDayContract.Reason.paused
        case .system: library.entry(id)?.gapReason ?? RecordingDayContract.Reason.stopped
        }
        if library.entry(id)?.gapReason != gap { library.change(id) { $0.gapReason = gap } }
        if reason == .user { Self.rememberIntent(false) }
    }

    /// Arms the writer for the end of the recording's day here, naming the recording that follows (made when the first
    /// sample past midnight arrives). `next`: keep a name already given, for a midnight that only moved.
    private func planBoundary(id: UUID, day: String?, locale: String, writer: RecordingSegmentWriter, zone: TimeZone, next: UUID = UUID()) {
        guard let day, let boundary = Self.endOfDay(day, in: zone) else { dayPlan = nil; return }
        writer.setBoundary(at: boundary, nextDirectory: RecordingLibrary.shared.folder(next))
        dayPlan = DayPlan(current: id, next: next, boundary: boundary, locale: locale)
        recordZone(zone, for: id)
    }

    /// Saves the zone a day recording's midnight is now counted in, when that changed (a flight): whoever ends the day
    /// after the microphone leaves it (the transfer's closing of past days, `dayEnd`) then ends it at the midnight the
    /// writer was armed with, never at the old zone's while it still holds later audio.
    private func recordZone(_ zone: TimeZone, for id: UUID) {
        guard let entry = RecordingLibrary.shared.entry(id), let identifier = Self.zoneToRecord(entry, countedIn: zone) else { return }
        RecordingLibrary.shared.change(id) { $0.timeZone = identifier }
    }

    /// The zone identifier to save with a day recording whose midnight is counted in `zone`; nil when it has it already.
    nonisolated static func zoneToRecord(_ entry: RecordingEntry, countedIn zone: TimeZone) -> String? {
        entry.isDay && entry.timeZone != zone.identifier ? zone.identifier : nil
    }

    /// A midnight the writer crossed while recording: the old day ends with the chunk that reaches it (or with an end
    /// marker when no chunk was open), and the next day's recording, named in advance, is made as of its first sample —
    /// its date from that sample, not from the boundary, in case recording came back days later. Safe to apply again:
    /// until the old day is saved as ended it returns nil and is tried again, in order. `live`: from the writer
    /// recording now, which then goes on in the new day.
    private func crossedMidnight(_ rotation: RecordingSegmentWriter.Rotation, live: Bool) -> [UUID]? {
        guard let old = UUID(uuidString: rotation.previous.lastPathComponent),
              let new = UUID(uuidString: rotation.next.lastPathComponent) else { return [] }
        let library = RecordingLibrary.shared
        let zone = Self.zone
        let previous = library.entry(old)
        if let last = rotation.last {
            guard register(last, endsDay: true) != nil else { return nil }
            library.change(old) { entry in
                entry.active = false
                entry.expectedSegments = last.index + 1
                entry.dayEndAt = rotation.boundary
                entry.gapReason = nil
                if entry.endedAt == nil { entry.endedAt = .now }
            }
        } else {
            library.closeDay(old, at: rotation.boundary, after: previous?.gapReason)
        }
        if library.entry(old)?.active == true { return nil }
        pendingDays[old] = nil
        let day = RecordingDayCalendar.day(of: rotation.firstSampleAt, in: zone)
        let locale = previous?.locale ?? RecordingDefaults.locale
        pendingDays[new] = PendingDay(day: day, createdAt: rotation.firstSampleAt, timeZone: zone.identifier, locale: locale)
        // Its first chunk tries again when this fails, and is kept until the entry exists.
        ensureDayEntry(new)
        // Audio came back only after a pause past midnight (the day check normally moves the folder before that).
        if rotation.firstSampleAt.timeIntervalSince(rotation.boundary) > 1 {
            noteGap(new, reason: previous?.gapReason ?? RecordingDayContract.Reason.paused, since: rotation.firstSampleAt)
        }
        if live, let writer {
            recordingID = new
            planBoundary(id: new, day: day, locale: locale, writer: writer, zone: zone)
        }
        return library.entry(new) == nil ? [old] : [old, new]
    }

    /// Makes a day recording named in advance once audio needs it (the first chunk past midnight, a mark just after it);
    /// tried again with the next chunk when saving it failed.
    @discardableResult
    private func ensureDayEntry(_ id: UUID) -> Bool {
        let library = RecordingLibrary.shared
        guard library.entry(id) == nil else { pendingDays[id] = nil; return true }
        guard let pending = pendingDays[id] else { return false }
        do {
            try library.create(source: Self.source, locale: pending.locale, kind: .day, id: id, createdAt: pending.createdAt,
                               day: pending.day, timeZone: pending.timeZone)
            pendingDays[id] = nil
            return true
        } catch {
            self.error = String(localized: "The recording info wasn't saved. Keep the app installed and try again.")
            return false
        }
    }

    /// The date, the clock or the time zone changed: a fresh zone, then the day check. Stopped for today, a day still
    /// on in the new zone takes that zone too, so it ends at this zone's midnight and a start later today goes on in it.
    private func dayClockChanged() {
        NSTimeZone.resetSystemTimeZone()
        if allDay {
            reconcileDay()
            return
        }
        let now = Date.now, zone = Self.zone
        for entry in RecordingLibrary.shared.openDays(source: Self.source) {
            guard let day = entry.day, let end = Self.endOfDay(day, in: zone), now < end else { continue }
            recordZone(zone, for: entry.id)
        }
    }

    /// Keeps the day recording on today's date: before every continue, and when the date, the clock or the time zone
    /// changes (`dayCheck`). What the writer already did is registered first, so a midnight it crossed is never undone.
    private func reconcileDay(now: Date = .now) {
        guard allDay, let writer else { return }
        applyWriterEvents()
        guard let id = recordingID, let entry = RecordingLibrary.shared.entry(id), let day = entry.day else { return }
        let zone = Self.zone
        switch Self.dayCheck(entryDay: day, now: now, zone: zone, running: writer.isRunning, plannedBoundary: dayPlan?.boundary) {
        case .keep(let boundary):
            // Same midnight, another zone (another name for the same offset): saved all the same.
            recordZone(zone, for: id)
            guard dayPlan?.current != id || dayPlan?.boundary != boundary else { return }
            let next = dayPlan.flatMap { $0.current == id ? $0.next : nil } ?? UUID()
            planBoundary(id: id, day: day, locale: entry.locale, writer: writer, zone: zone, next: next)
        case .awaitWriter:
            break
        case .switchNow:
            switchDay(from: entry, now: now, zone: zone, writer: writer)
        }
    }

    /// The day is over here (paused past midnight, a flight east, the clock moved on): the writer moves to today's
    /// recording now — the open one, else a new one starting now — and the old day ends at its own midnight, or now when
    /// that is still ahead. A paused day carries its gap's reason over to the next chunk.
    private func switchDay(from old: RecordingEntry, now: Date, zone: TimeZone, writer: RecordingSegmentWriter) {
        let library = RecordingLibrary.shared
        let today = RecordingDayCalendar.day(of: now, in: zone)
        let gap = writer.isRunning ? nil : old.gapReason ?? RecordingDayContract.Reason.paused
        let target: RecordingEntry
        if let open = library.openDays(source: Self.source).first(where: { $0.id != old.id && ($0.day ?? "") >= today }) {
            target = open
        } else {
            do {
                target = try library.create(source: Self.source, locale: old.locale, kind: .day, createdAt: now, day: today,
                                            timeZone: zone.identifier)
            } catch {
                self.error = String(localized: "The recording info wasn't saved. Keep the app installed and try again.")
                return
            }
        }
        let cut = writer.switchDirectory(to: library.folder(target.id), firstIndex: nextIndex(of: target))
        // The chunk the switch cut, in the old day, before its end marker takes the next seq. Should its save fail, the
        // old day stays open until it is saved, and the next start closes it.
        var changed = applyWriterEvents(notify: false)
        if !holdsAudio(of: old.id) {
            library.closeDay(old.id, at: Self.switchedDayEnd(of: old, zone: zone, now: now, cut: cut), after: gap)
        }
        if let gap, library.entry(target.id)?.gapReason == nil { library.change(target.id) { $0.gapReason = gap } }
        recordingID = target.id
        planBoundary(id: target.id, day: target.day, locale: target.locale, writer: writer, zone: zone)
        // Everything the drain registered goes out with both days, now that their ends and gaps are saved.
        for id in [old.id, target.id] where !changed.contains(id) { changed.append(id) }
        announce(changed)
    }

    /// When a day left by `switchDay` ends: its own midnight, or now when that is still ahead, but never before the end of
    /// the chunk the switch cut from it (buffers still queued at `now` land in that chunk, so it can end a moment later).
    private static func switchedDayEnd(of old: RecordingEntry, zone: TimeZone, now: Date, cut: RecordingSegmentWriter.Segment?) -> Date {
        let end = dayEnd(of: old, zone: zone, notAfter: now)
        guard let cut, cut.owner == old.id else { return end }
        return max(end, cut.endedAt)
    }

    /// A bookmark or meeting mark on the wall clock, kept on the day recording until the chunk carrying it is
    /// acknowledged. A second tap of the same kind within a second adds nothing.
    private func addDayMark(_ label: String) {
        let now = Date.now
        let library = RecordingLibrary.shared
        guard let id = dayRecording(at: now), let entry = library.entry(id) else { return }
        if let last = entry.dayMarks?.last(where: { $0.label == label }), abs(now.timeIntervalSince(last.at)) < 1 { return }
        library.change(id) { $0.dayMarks = ($0.dayMarks ?? []) + [RecordingDayMark(at: now, label: label)] }
    }

    /// The day recording a moment belongs to: today's after the day check, or, a moment past midnight before the writer
    /// reached its first sample there, the one named for after it (a mark must ride on a chunk of its own day).
    private func dayRecording(at now: Date) -> UUID? {
        reconcileDay(now: now)
        guard let id = recordingID else { return nil }
        guard writer != nil, let plan = dayPlan, plan.current == id, now >= plan.boundary else { return id }
        let zone = Self.zone
        if pendingDays[plan.next] == nil {
            pendingDays[plan.next] = PendingDay(day: RecordingDayCalendar.day(of: plan.boundary, in: zone), createdAt: plan.boundary,
                                                timeZone: zone.identifier, locale: plan.locale)
        }
        return ensureDayEntry(plan.next) ? plan.next : id
    }

    /// Erase in all-day recording. The chunk being written is cut first and registered, so it is erased with the rest
    /// rather than finishing later inside the range or leaving its seq without a record. Every day recording the window
    /// touches (both days, just after midnight) then marks its chunks erased, sent or not, and drops its marks there.
    /// Nothing is sent before all of it is saved: an erased chunk never leaves with its audio.
    ///
    /// The audio range ends exactly at the writer's cut (`cutForErase`), never at a later clock reading: the chunk after
    /// the cut starts there, so it keeps all of its audio however long the saves take and however short it turns out
    /// (a pause or a stop right after). Marks are on the wall clock and go up to now.
    private func eraseDayRecent() {
        let now = Date.now
        let cut = writer?.cutForErase()
        var touched = applyWriterEvents(notify: false)
        let from = now.addingTimeInterval(-RecordingRolling.eraseMinutes * 60)
        // No writer (stopped for today): nothing is being recorded, and the next start comes after now.
        let to = min(cut?.through ?? now, now)
        let library = RecordingLibrary.shared
        for entry in library.entries where entry.isDay && entry.source == Self.source
            && entry.createdAt <= now && (entry.dayEndAt ?? .distantFuture) >= from {
            // Paused for longer than the window: no audio in it, only marks.
            let erased = to > from && !library.eraseDay(entry.id, from: from, to: to).isEmpty
            let marked = entry.dayMarks?.contains { $0.at >= from && $0.at <= now } ?? false
            if marked {
                library.change(entry.id) { updated in
                    updated.dayMarks?.removeAll { $0.at >= from && $0.at <= now }
                    if updated.dayMarks?.isEmpty == true { updated.dayMarks = nil }
                }
            }
            if erased || marked, !touched.contains(entry.id) { touched.append(entry.id) }
        }
        announce(touched)
    }

    /// What a day recording does when the date may have changed under it.
    nonisolated enum DayCheck: Equatable, Sendable {
        /// Still the recording's day here: the writer cuts at this midnight.
        case keep(boundary: Date)
        /// Its midnight has passed and the running writer cuts there with its next buffer: leave it, so the cut stays
        /// exact to the sample.
        case awaitWriter
        /// Its day is over here and nothing will cut it: move to today's recording now.
        case switchNow
    }

    /// The day check: `entryDay` is the recording's date, `plannedBoundary` the midnight the writer is armed with. A
    /// zone to the west keeps the recording (its date has not come or is still on), with its midnight there; a zone to
    /// the east, a clock moved on, or a pause past midnight, switches now.
    nonisolated static func dayCheck(entryDay: String, now: Date, zone: TimeZone, running: Bool, plannedBoundary: Date?) -> DayCheck {
        guard let end = endOfDay(entryDay, in: zone) else { return .switchNow }
        if now < end { return .keep(boundary: end) }
        if running, plannedBoundary == end { return .awaitWriter }
        return .switchNow
    }

    /// The midnight that ends a `YYYY-MM-DD` date in a zone (a 23- or 25-hour day across a DST change ends where it does).
    nonisolated static func endOfDay(_ day: String, in zone: TimeZone) -> Date? {
        RecordingDayCalendar.start(of: day, in: zone).map { RecordingDayCalendar.nextMidnight(after: $0, in: zone) }
    }

    /// When a day recording's day ends: its own midnight in the zone it was recorded in, or `now` when that midnight is
    /// still ahead (the clock or the zone moved on) or its date is unreadable.
    private static func dayEnd(of entry: RecordingEntry, zone fallback: TimeZone, notAfter now: Date) -> Date {
        let zone = entry.timeZone.flatMap(TimeZone.init(identifier:)) ?? fallback
        guard let end = entry.day.flatMap({ endOfDay($0, in: zone) }) else { return now }
        return min(now, end)
    }

    /// The day recording to report as halted at launch: the newest of those `recover` left open, when the user wanted
    /// all-day on (an open day with all-day off is one the user stopped for today).
    nonisolated static func launchHalt(recovered: [UUID], entries: [RecordingEntry], intent: Bool) -> (id: UUID, reason: RecordingDayHalt)? {
        guard intent, !recovered.isEmpty else { return nil }
        let open = Set(recovered)
        guard let newest = entries.filter({ $0.isDay && open.contains($0.id) }).max(by: { $0.createdAt < $1.createdAt }) else { return nil }
        return (id: newest.id, reason: RecordingDayHalt.crashed)
    }

    /// The zone days are counted in: the system's, following its changes; recordings keep its identifier.
    private static var zone: TimeZone { .autoupdatingCurrent }

    private static func rememberIntent(_ on: Bool) {
        // Changes only on start and Stop; written only when it does (every write redraws the views reading defaults).
        let defaults = UserDefaults.standard
        if defaults.bool(forKey: allDayIntentKey) != on { defaults.set(on, forKey: allDayIntentKey) }
    }

    private nonisolated struct DayPlan {
        let current: UUID
        let next: UUID
        let boundary: Date
        let locale: String
    }

    private nonisolated struct PendingDay {
        let day: String
        let createdAt: Date
        let timeZone: String
        let locale: String
    }

    /// A writer event taken from its log and not saved yet, with the writer it came from (weak: a stopped writer goes).
    private nonisolated struct HeldEvent {
        let event: RecordingSegmentWriter.Event
        weak var source: RecordingSegmentWriter?
    }

    #if DEBUG
    /// Screenshots (`-showMeetingSample`, `-showAllDaySample`): `id` shows as being recorded `elapsed` seconds in, paused
    /// or paused by a call, as an all-day recording or not, with no microphone (Stop ends it through `stopSample`).
    func showSample(_ id: UUID, elapsed: Double, paused: Bool = false, interrupted: Bool = false, allDay: Bool = false) {
        recordingID = id; self.elapsed = elapsed; sampleElapsed = elapsed
        self.interrupted = interrupted
        self.allDay = allDay
        state = paused || interrupted ? .paused : .recording
    }

    /// The DEBUG flow (`-showMeetingSample flow`): moves a sample recording's clock and level on.
    func advanceSample(elapsed: Double, level: Double) {
        guard writer == nil, state != .idle else { return }
        sampleElapsed = elapsed; self.elapsed = elapsed; sampleLevel = level
    }

    /// Stop for a sample recording: what `stop()` does for a real one after the microphone is closed (the entry ends, or a
    /// day pauses; the same callbacks run), so the wrap-up steps advance as they would.
    private func stopSample(_ id: UUID) -> UUID {
        onStopping?(id)
        if allDay {
            RecordingLibrary.shared.change(id) { $0.gapReason = RecordingDayContract.Reason.paused }
        } else {
            RecordingLibrary.shared.change(id) { entry in
                entry.active = false
                entry.expectedSegments = entry.nextIndex
                entry.endedAt = .now
            }
        }
        recordingID = nil; state = .idle; interrupted = false; notice = nil; sampleElapsed = 0; sampleLevel = 0; allDay = false
        onSaved?(id)
        return id
    }
    #endif

    /// Chunk length now: an all-day recording's follows the phone link, every other recording's as before.
    private var chunkLength: Double { allDay ? Self.dayChunkLength(reachable: linkReachable) : Self.segmentLength }

    private static func dayChunkLength(reachable: Bool) -> Double {
        reachable ? RecordingDayContract.liveChunkSeconds : RecordingDayContract.backlogChunkSeconds
    }

    private static var segmentLength: Double {
        #if os(iOS) || os(visionOS)
        UIApplication.shared.applicationState == .active ? RecordingAudioSpec.liveSegmentSeconds : RecordingAudioSpec.segmentSeconds
        #else
        WatchRecordingTransfer.shared.reachable ? RecordingAudioSpec.liveSegmentSeconds : RecordingAudioSpec.segmentSeconds
        #endif
    }

    /// What stops a recording to protect the device.
    private nonisolated enum ResourceProblem {
        case storage, battery

        var halt: RecordingDayHalt { self == .storage ? .lowStorage : .lowBattery }
        var message: String {
            switch self {
            case .storage: String(localized: "Not enough storage. Saved recordings won't be lost.")
            case .battery: String(localized: "Watch battery is too low. Saved recordings won't be lost.")
            }
        }
    }

    private static func resourceProblem(starting: Bool) -> ResourceProblem? {
        let attributes = try? FileManager.default.attributesOfFileSystem(forPath: NSHomeDirectory())
        let free = (attributes?[.systemFreeSize] as? NSNumber)?.int64Value
        if let free, free < (starting ? 300_000_000 : 150_000_000) { return .storage }
        #if os(watchOS)
        let device = WKInterfaceDevice.current(); device.isBatteryMonitoringEnabled = true
        if device.batteryState == .unplugged, device.batteryLevel >= 0, device.batteryLevel < 0.05 { return .battery }
        #endif
        return nil
    }
}
#endif

/// Why an all-day recording stopped recording without the user (`RecordingCapture.onDayHalted`), for what the watch
/// tells them. Outside the platform check above: the watch's alert policy, in `Shared/`, compiles for the Mac too.
nonisolated enum RecordingDayHalt: String, Sendable {
    /// A call, Siri or another app took the microphone.
    case interrupted
    /// The microphone could not be opened again (the app was in the background when the interruption ended).
    case resumeFailed
    /// The input changed and could not be reopened.
    case microphoneChanged
    /// The system audio service reset.
    case audioReset
    case lowBattery
    case lowStorage
    /// A chunk or the recording's info could not be written.
    case writeFailed
    /// The app ended while recording (found at the next launch).
    case crashed
}

/// Who stopped a recording: the user (an all-day recording pauses for the rest of today), or the system (it stays on,
/// for the user to continue).
nonisolated enum RecordingStopReason: Sendable {
    case user, system
}
