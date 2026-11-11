#if os(iOS)
import SwiftUI

// The live recording sheet (#4240, docs/design/proposals/meeting-recording.md section 4C and 4D): a sheet that opens by itself
// when a recording starts or is already running, survives being swiped away (recording goes on; the now-recording card is
// the way back), owns a navigation stack, and after Stop cross-fades from the live view to the wrap-up of that recording.

/// Asks the hosts' `.recordingLiveSheet()` to present again: a Live Activity tap, a notification or the drawer, while the
/// Recordings page is already on screen with the sheet swiped away, change the route without the page ever disappearing, so
/// nothing else would tell it. A counter, bumped by `MeetingRecorder.openRecordings` and by the now-recording card.
@MainActor @Observable final class RecordingLivePresenter {
    static let shared = RecordingLivePresenter()
    private(set) var serial = 0
    func open() { serial += 1 }
}

extension View {
    /// Presents the live recording sheet from this page: when a recording begins, when this page appears while one runs,
    /// and whenever `RecordingLivePresenter` is asked. Put it on the whole list of each host (Knowledge's and the Recordings
    /// page), not on `RecordingsSection`, which Knowledge removes while it changes kind and recording goes on.
    func recordingLiveSheet() -> some View { modifier(RecordingLiveSheetPresenter()) }
}

private struct RecordingLiveSheetPresenter: ViewModifier {
    @State private var capture = RecordingCapture.shared
    @State private var presenter = RecordingLivePresenter.shared
    @State private var presented = false
    @State private var visible = false

    private var capturing: Bool { capture.state == .recording || capture.state == .paused }

    func body(content: Content) -> some View {
        content
            .sheet(isPresented: $presented) { RecordingLiveSheet() }
            .onAppear {
                visible = true
                if capturing { presented = true }
            }
            .onDisappear { visible = false }
            // The observed change, not "tapped here": the drawer, Siri and the quick action open the page before the
            // calendar read and the microphone, so it is already on screen and idle when recording begins.
            .onChange(of: capture.state) { old, new in
                if visible, new == .recording, old != .paused { presented = true }
            }
            .onChange(of: presenter.serial) {
                if visible, capturing { presented = true }
            }
    }
}

/// The sheet: the live view while recording, the wrap-up once it stopped. It remembers the recording's id while capture
/// runs, and treats the observed change to idle as the stop, however it came (the button, low storage, a failed write, the
/// audio service reset): `RecordingCapture.stop` clears `recordingID` at once and `MeetingAsk.ended` clears the exchanges,
/// so the exchanges are mirrored while capture runs and the live view fades out from the mirror.
struct RecordingLiveSheet: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var capture = RecordingCapture.shared
    @State private var ask = MeetingAsk.shared
    /// The recording the sheet is about: live, then wrap-up.
    @State private var held: UUID?
    @State private var wrapUp = false
    @State private var exchanges: [MeetingAsk.Exchange] = []

    var body: some View {
        NavigationStack {
            ZStack {
                if wrapUp, let held {
                    RecordingWrapUp(id: held, finish: { dismiss() })
                        .transition(.opacity)
                } else if let held {
                    RecordingLiveView(id: held, exchanges: exchanges, hide: { dismiss() })
                        .transition(.opacity)
                }
            }
            .themedGroupedPage()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar(.hidden, for: .navigationBar)
        }
        .presentationDetents([.large])
        .presentationDragIndicator(.visible)
        .onChange(of: capture.state, initial: true) { old, new in follow(from: old, to: new) }
        .onChange(of: ask.exchanges) { mirror() }
    }

    private func mirror() {
        // After the stop the exchanges are gone from `ask`: what the live view had stays for its fade.
        guard capture.state != .idle, let held else { return }
        exchanges = ask.exchanges(for: held)
    }

    private func follow(from old: RecordingCapture.State, to new: RecordingCapture.State) {
        switch new {
        case .recording, .paused:
            if let id = capture.recordingID, held != id {
                held = id
                wrapUp = false
                exchanges = ask.exchanges(for: id)
            }
        case .idle:
            if held != nil, old == .recording || old == .paused {
                // Stop is confirmed (or the recording ended by itself): success once capture has ended, as the wrap-up appears.
                Haptics.success()
                AccessibilityNotification.Announcement(String(localized: "Recording stopped")).post()
                withAnimation(reduceMotion ? nil : Theme.Motion.reveal) { wrapUp = true }
            } else if held == nil {
                dismiss()
            }
        case .starting:
            break
        }
    }
}

// MARK: The clock

/// The recording's time. The system draws and ticks it (`Text(timerInterval:)`), so the page does not draw again four times
/// a second; paused, the same text is held at the time it stopped at. Reads `RecordingCapture.timeline`, which is not observed.
struct RecordingClock: View {
    var running: Bool
    var font: Font
    @State private var capture = RecordingCapture.shared
    /// When the recording would have started to show the time it shows now (moves forward over a pause).
    @State private var start = Date.now
    @State private var frozenAt: Date?

    var body: some View {
        Text(timerInterval: start...Date.distantFuture, pauseTime: running ? nil : frozenAt, countsDown: false, showsHours: false)
            .font(font)
            .monospacedDigit()
            .lineLimit(1)
            .onChange(of: running, initial: true) {
                start = Date.now.addingTimeInterval(-capture.timeline)
                frozenAt = running ? nil : Date.now
            }
    }

    /// "2 minutes, 5 seconds": the spoken form of a time, for VoiceOver.
    static func spoken(_ seconds: Double) -> String {
        guard seconds.isFinite, seconds >= 0 else { return "" }
        return Duration.seconds(Int(seconds)).formatted(.units(allowed: [.hours, .minutes, .seconds], width: .wide))
    }
}

/// A motion the page makes only when Reduce Motion is off (tokens do not gate themselves, DESIGN_SYSTEM 2.7).
extension View {
    func recordingMotion<V: Equatable>(_ animation: Animation = Theme.Motion.state, reduced: Bool, value: V) -> some View {
        self.animation(reduced ? nil : animation, value: value)
    }
}
#endif
