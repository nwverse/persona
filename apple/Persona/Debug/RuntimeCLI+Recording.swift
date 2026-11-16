#if DEBUG && os(iOS)
import Foundation

/// The recording being made (#9417; docs/operations/runtime-cli.md「Commands」): `recording` says what records, and with
/// `action` `stop` ends it as End's End and Write the Minutes does (`MeetingRecorder.stop`), for a script that records a
/// meeting: a confirmation dialog's buttons take no accessibility activation, and `alert` dismisses one without running
/// its SwiftUI action.
extension RuntimeCLI {
    static let recordingCommands: [String: Command] = [
        "recording": { a in recording(a) },
    ]

    private static func recording(_ a: Args) -> Reply {
        let capture = RecordingCapture.shared
        let id = capture.recordingID?.uuidString
        switch a["action"] as? String {
        case nil:
            return ["state": "\(capture.state)", "recording": id ?? NSNull(), "elapsed": capture.elapsed]
        case "stop":
            guard let id else { return fail("nothing is recording") }
            MeetingRecorder.shared.stop(recordingID: id)
            return ["stopped": id]
        case let other?:
            return fail("unknown action \(other): stop")
        }
    }
}
#endif
