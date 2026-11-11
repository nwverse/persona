#if DEBUG
import Foundation

/// `-showAllDaySample recording|paused|interrupted|stopped|off`: all-day recording in that state with no microphone, 3 h
/// 25 min since it last started, 4 chunks waiting for the iPhone (screenshots). Pair it with `-recording.phoneDay 1`
/// (a phone that takes day recordings: the home card and the Record page's button), `-recording.phoneDayHub NO` (the
/// "needs a hub update" line), `-openWatchItem allday` (its page) or `-openWatchItem allday-complication` (every face).
enum WatchAllDaySamples {
    static let day = UUID(uuidString: "00000000-0000-0000-0000-000000000006")!

    @MainActor static func installIfAsked() {
        guard let raw = UserDefaults.standard.string(forKey: "showAllDaySample"),
              let state = AllDaySnapshot.State(rawValue: raw) else { return }
        // First: the capture's launch recovery runs now, not over the sample.
        let capture = RecordingCapture.shared
        let elapsed: Double = 3 * 3600 + 25 * 60
        switch state {
        case .recording, .paused, .interrupted:
            capture.showSample(day, elapsed: elapsed, paused: state == .paused, interrupted: state == .interrupted, allDay: true)
        case .stopped, .off:
            break
        }
        WatchAllDay.shared.showSample(state, since: .now.addingTimeInterval(-elapsed), waiting: 4)
    }
}
#endif
