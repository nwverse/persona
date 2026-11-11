#if os(iOS)
import BackgroundTasks
import Foundation

/// All-day recording's catch-up (docs/assistant/day-recap.md): a `BGProcessingTask` that transcribes, uploads and applies
/// retention to what the watch sent while the app sat in the background, run by the system while the phone charges and
/// has a network (the upload needs one, C9). Submitted when the app goes to the background and from the receiver as
/// chunks arrive (a day the app is only ever woken by WatchConnectivity never goes through that transition), and again
/// after a run that left work. Submitting replaces the pending request, so asking often costs nothing.
enum RecordingDayCatchUp {
    /// In `BGTaskSchedulerPermittedIdentifiers` (apple/project.yml).
    nonisolated static let identifier = "persona.recording.day-catchup"
    /// The earliest a submitted request may start: chunks keep arriving while the user is about, and the receiver asks at
    /// most this often anyway.
    static let delay: TimeInterval = 15 * 60

    private static var registered = false

    /// From `application(_:didFinishLaunchingWithOptions:)`, once: the system ends an app that registers twice, and
    /// accepts a registration only before launching finishes.
    static func register() {
        guard !registered else { return }
        registered = true
        _ = BGTaskScheduler.shared.register(forTaskWithIdentifier: identifier, using: .main) { @Sendable task in
            // Called on the main queue (`using: .main`), which owns the task from here on.
            nonisolated(unsafe) let task = task
            MainActor.assumeIsolated { run(task) }
        }
    }

    /// Submits (or replaces) the request. Fails quietly where the system has no background tasks (the simulator).
    static func schedule() {
        let request = BGProcessingTaskRequest(identifier: identifier)
        request.requiresExternalPower = true
        request.requiresNetworkConnectivity = true
        request.earliestBeginDate = Date(timeIntervalSinceNow: delay)
        try? BGTaskScheduler.shared.submit(request)
    }

    /// Going to the background: submitted while a day recording from the last week is on the phone (its audio, text or
    /// upload may still need the task); nothing for anyone who never used all-day recording.
    static func scheduleIfNeeded(library: RecordingLibrary = .shared) {
        let since = Date.now.addingTimeInterval(-(RecordingDayContract.audioMaxDays + 1) * RecordingDayRetention.day)
        guard library.entries.contains(where: { $0.isDay && $0.createdAt >= since }) else { return }
        schedule()
    }

    /// The task's work: transcription first (the upload sends its text), then the upload, then retention. True when work
    /// is left for another run. Also the DEBUG `-openDayCatchUp YES` launch, where the simulator never runs the task.
    static func catchUp() async -> Bool {
        await RecordingDayTranscriber.shared.drain()
        guard !Task.isCancelled else { return true }
        let unsent = await RecordingDaySync.shared.drain()
        guard !Task.isCancelled else { return true }
        await RecordingDayKeeper.shared.run(force: true)
        return unsent || RecordingDayTranscriber.shared.hasWork
    }

    private static func run(_ task: BGTask) {
        let work = Task { await catchUp() }
        // Time is up (the user picked up the phone, or the system needs it back): stop; nothing done so far is lost, and
        // an interrupted run counts no try against any chunk.
        task.expirationHandler = { @Sendable in work.cancel() }
        Task {
            let remains = await work.value
            let finished = !work.isCancelled
            if remains || !finished { schedule() }
            task.setTaskCompleted(success: finished)
        }
    }
}
#endif
