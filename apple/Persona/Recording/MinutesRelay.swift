#if os(iOS)
import Foundation
import UIKit
import WatchConnectivity

/// The meetings' minutes on the Apple Watch (docs/apple/recording.md「Apple Watch」): the phone reads the hub's minutes of
/// its newest meeting recordings and relays a short version (`WatchMinutes`) in the WatchConnectivity application context;
/// the watch never talks to the hub for them. Read whenever the app comes to the front (and every few minutes while it
/// stays there, as long as some are still being written) and whenever a recording's page learns them.
@MainActor final class MinutesRelay {
    static let shared = MinutesRelay()

    private let library = RecordingLibrary.shared
    private var started = false
    private var known: [UUID: WatchMinutes.Meeting] = [:]
    private var loop: Task<Void, Never>?

    /// From `MeetingRecorder.start()` at launch.
    func start() {
        guard !started else { return }
        started = true
        NotificationCenter.default.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in MinutesRelay.shared.follow() }
        }
        NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in MinutesRelay.shared.loop?.cancel(); MinutesRelay.shared.loop = nil }
        }
    }

    /// The meeting recordings the watch hears about: the newest finished ones with text, a week back at most.
    func candidates(now: Date = .now) -> [RecordingEntry] {
        Array(library.entries
            .filter { $0.isMeeting && !$0.active && $0.transcribedAll && RecordingSync.uploadable($0)
                && $0.createdAt > now.addingTimeInterval(-7 * 86_400) }
            .sorted { $0.createdAt > $1.createdAt }
            .prefix(WatchMinutes.maxMeetings))
    }

    /// While the app is in front: reads now, then every three minutes as long as some are still being written.
    private func follow() {
        guard watchListens else { return }
        loop?.cancel()
        loop = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, await self.refresh() else { return }
                try? await Task.sleep(for: .seconds(180))
            }
        }
    }

    /// Reads what isn't ready yet from the hub and relays the lot. True while some are still being written.
    @discardableResult
    func refresh() async -> Bool {
        guard watchListens, let api = AppModel.shared.api, !api.config.isDemo else { return false }
        let meetings = candidates()
        for entry in meetings where known[entry.id]?.state != .ready && RecordingSync.shared.status(entry) == .synced {
            guard let answer = try? await RecordingMinutesModel.fetch(entry.id, api) else { continue }
            known[entry.id] = Self.short(entry, minutes: answer.detail.minutes, status: answer.detail.meta?.state)
        }
        push(meetings)
        return meetings.contains { (known[$0.id]?.state ?? .writing) == .writing }
    }

    /// What a recording's page just read: the watch hears it without a second request.
    func note(_ entry: RecordingEntry, state: RecordingMinutesModel.State) {
        guard entry.isMeeting, watchListens else { return }
        switch state {
        case .ready(let minutes): known[entry.id] = Self.short(entry, minutes: minutes, status: "ready")
        case .failed: known[entry.id] = Self.short(entry, minutes: nil, status: "failed")
        default: return
        }
        push(candidates())
    }

    /// The watch's short version of one meeting.
    nonisolated static func short(_ entry: RecordingEntry, minutes: RecordingMinutes?, status: String?) -> WatchMinutes.Meeting {
        let ready = minutes.map { !$0.isEmpty || status == "ready" } ?? false
        return WatchMinutes.meeting(id: entry.id, title: entry.displayTitle, date: entry.createdAt,
                                    minutes: ready ? minutes : nil, failed: status == "failed")
    }

    private func push(_ meetings: [RecordingEntry]) {
        let list = meetings.map { known[$0.id] ?? Self.short($0, minutes: nil, status: nil) }
        WatchSync.shared.push(minutes: WatchMinutes(meetings: list))
    }

    private var watchListens: Bool {
        WCSession.isSupported() && WCSession.default.activationState == .activated && WCSession.default.isWatchAppInstalled
    }
}
#endif
