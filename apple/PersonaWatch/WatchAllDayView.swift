import SwiftUI

/// All-day recording's page (docs/assistant/day-recap.md): its state in words and a mark, the time it has recorded
/// since it last started, one tap to continue when it is paused or stopped, and pause, bookmark, meeting mark, erase
/// and stop while it records.
struct WatchAllDayView: View {
    @State private var allDay = WatchAllDay.shared
    @State private var capture = RecordingCapture.shared
    /// Absent until the phone says; only a phone that says its hub can't write the recap shows the line.
    @AppStorage(WatchSync.phoneDayHubKey) private var phoneDayHub = true
    @State private var confirming = false

    var body: some View {
        ScrollView {
            VStack(spacing: Theme.Space.m) {
                WatchAllDayStatus(state: allDay.state, since: allDay.since)
                controls
                WatchAllDayFeedbackLine(feedback: allDay.feedback)
                if allDay.waiting > 0 {
                    Label(String(localized: "\(allDay.waiting) waiting for iPhone"), systemImage: Theme.Icon.phone)
                        .font(Theme.Font.caption).foregroundStyle(.secondary)
                }
                if !phoneDayHub {
                    Label("The recap needs a hub update.", systemImage: Theme.Icon.info)
                        .font(Theme.Font.caption).foregroundStyle(.secondary)
                }
                if let error = capture.error {
                    Text(error).font(Theme.Font.caption).foregroundStyle(Theme.cautionText)
                }
                Text("Your iPhone turns what's recorded into text as it arrives, and your hub writes a recap of your day. Audio is deleted 3 days after the recap.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
            }
            .padding(Theme.Space.m)
        }
        .navigationTitle("All-Day Recording")
        .accessibilityIdentifier("watch-allday-page")
        .allDayConsent(isPresented: $confirming)
        // A start asked for from outside (a link, a notification, the phone) that needs the explanation first.
        .onChange(of: allDay.consentPending, initial: true) { _, pending in
            guard pending else { return }
            allDay.consentPending = false
            confirming = true
        }
    }

    @ViewBuilder private var controls: some View {
        if capture.state == .starting {
            ProgressView(String(localized: "Waiting for microphone access")).font(Theme.Font.secondary)
            Button("Cancel") { capture.cancelStarting() }.lineLimit(1)
        } else {
            switch allDay.state {
            case .off:
                Button { start() } label: { Label("Start", systemImage: Theme.Icon.allDay).lineLimit(1) }
                    .buttonStyle(.borderedProminent)
                    .recordingPrimaryGesture()
                    .accessibilityIdentifier("watch-allday-start")
                if allDay.dayOpen { erase }
            case .recording:
                actions(recording: true)
                stop
            case .paused, .interrupted:
                resume
                actions(recording: false)
                stop
            case .stopped:
                // No capture runs to mark a meeting in; what it recorded can still be erased.
                resume
                if allDay.dayOpen { erase }
                stop
            }
        }
    }

    /// Icon-only, each named for VoiceOver. Pause and Bookmark only while recording: a paused day has no audio to
    /// mark; a meeting mark and erasing the last 10 minutes still make sense.
    private func actions(recording: Bool) -> some View {
        VStack(spacing: Theme.Space.s) {
            if recording {
                HStack(spacing: Theme.Space.s) {
                    Button { allDay.pause() } label: { Label("Pause", systemImage: Theme.Icon.pause).labelStyle(.iconOnly) }
                        .accessibilityIdentifier("watch-allday-pause")
                    Button { allDay.addBookmark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin).labelStyle(.iconOnly) }
                        .recordingPrimaryGesture()
                }
            }
            HStack(spacing: Theme.Space.s) {
                Button { allDay.markMeeting() } label: {
                    Label("Mark Meeting", systemImage: Theme.Icon.meetingMark).labelStyle(.iconOnly)
                }
                .accessibilityIdentifier("watch-allday-meeting")
                Button { allDay.eraseRecent() } label: {
                    Label("Erase last 10 minutes", systemImage: Theme.Icon.delete).labelStyle(.iconOnly)
                }
            }
        }
    }

    private var resume: some View {
        Button { start() } label: { Label("Continue", systemImage: Theme.Icon.run).lineLimit(1) }
            .buttonStyle(.borderedProminent)
            .recordingPrimaryGesture()
            .accessibilityIdentifier("watch-allday-continue")
    }

    /// With the microphone off, on its own and so with its words.
    private var erase: some View {
        Button { allDay.eraseRecent() } label: { Label("Erase 10m", systemImage: Theme.Icon.delete).lineLimit(1) }
            .accessibilityIdentifier("watch-allday-erase")
    }

    private var stop: some View {
        Button { allDay.stopForToday() } label: { Label("Stop for Today", systemImage: Theme.Icon.interrupt).lineLimit(1) }
            .tint(Theme.danger)
            .accessibilityIdentifier("watch-allday-stop")
    }

    private func start() {
        if !allDay.requestStart() { confirming = true }
    }
}

/// The home list's All-Day Recording card, shown once the phone takes day recordings (or while the mode is on): one
/// tap starts it (with the explanation the first time) or continues it; while it records, the tap opens its page.
/// Reads only the capture's state, never the library's entries, which change with every chunk.
struct WatchAllDayCard: View {
    @State private var allDay = WatchAllDay.shared
    @AppStorage(WatchSync.phoneDayKey) private var phoneDay = 0
    @State private var confirming = false

    var body: some View {
        if phoneDay > 0 || allDay.state != .off {
            Group {
                if allDay.state == .recording {
                    NavigationLink { WatchAllDayView() } label: { label }
                } else {
                    Button { if !allDay.requestStart() { confirming = true } } label: { label }
                }
            }
            .watchCard()
            .accessibilityIdentifier("watch-allday")
            .allDayConsent(isPresented: $confirming)
        }
    }

    private var label: some View {
        VStack(alignment: .leading, spacing: Theme.Watch.lineSpacing) {
            Label(allDay.state.word ?? String(localized: "All-Day Recording"), systemImage: allDay.state.symbol)
                .font(Theme.Font.secondaryMedium).foregroundStyle(allDay.state.tone).lineLimit(2)
            if allDay.state == .recording, let since = allDay.since {
                Text(timerInterval: since...Date.distantFuture, countsDown: false)
                    .font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
        }
    }
}

/// The Record page while all-day recording runs: its state, a meeting mark (the page's Meeting Recording can't start a
/// second recording), and the way to its page.
struct WatchAllDaySummary: View {
    @State private var allDay = WatchAllDay.shared

    var body: some View {
        VStack(spacing: Theme.Space.m) {
            WatchAllDayStatus(state: allDay.state, since: allDay.since)
            Button { allDay.markMeeting() } label: {
                Label("Mark Meeting", systemImage: Theme.Icon.meetingMark).lineLimit(1)
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("watch-recording-mark-meeting")
            WatchAllDayFeedbackLine(feedback: allDay.feedback)
            NavigationLink { WatchAllDayView() } label: {
                Label("All-Day Recording", systemImage: Theme.Icon.allDay).lineLimit(1)
            }
        }
    }
}

/// The state's words and mark (never its color alone), and while recording the time since it last started, counted
/// by the system rather than redrawn by the app.
struct WatchAllDayStatus: View {
    let state: AllDaySnapshot.State
    let since: Date?

    var body: some View {
        VStack(spacing: Theme.Space.xs) {
            if let word = state.word {
                Label(word, systemImage: state.symbol)
                    .font(Theme.Font.secondaryMedium).foregroundStyle(state.tone).multilineTextAlignment(.center)
            }
            if state == .recording, let since {
                Text(timerInterval: since...Date.distantFuture, countsDown: false)
                    .font(Theme.Font.display.monospacedDigit()).foregroundStyle(Theme.danger)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("watch-allday-status")
    }
}

/// The line confirming the tap just made; it goes after a few seconds.
struct WatchAllDayFeedbackLine: View {
    let feedback: WatchAllDay.Feedback?

    var body: some View {
        if let feedback {
            Label(feedback.text, systemImage: feedback.symbol)
                .font(Theme.Font.caption)
                .foregroundStyle(feedback == .busy ? Theme.cautionText : .secondary)
        }
    }
}

extension View {
    /// The first start's explanation (App Review 5.1.2(i)): what is recorded all day, where it goes, how long the
    /// audio stays, and asking others first.
    func allDayConsent(isPresented: Binding<Bool>) -> some View {
        confirmationDialog("All-Day Recording", isPresented: isPresented, titleVisibility: .visible) {
            Button("Start All-Day Recording") { WatchAllDay.shared.confirmConsent() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Records what's around you all day. Your iPhone turns it into text, and your hub writes a recap of your day from that text. Audio stays on your devices and is deleted 3 days after the recap. Ask others before recording them.")
        }
    }
}

extension AllDaySnapshot.State {
    /// The state in words on the watch's page, card and Record page; nil when off, where the control says it. The
    /// complication says the same in fewer words (`AllDaySnapshot.Face.title` and `hint`).
    var word: String? {
        switch self {
        case .off: nil
        case .recording: String(localized: "Recording all day")
        case .paused: String(localized: "Paused")
        case .interrupted: String(localized: "Paused — tap to continue")
        case .stopped: String(localized: "Stopped — tap to continue")
        }
    }

    var symbol: String {
        switch self {
        case .off, .recording: Theme.Icon.allDay
        case .paused, .interrupted: Theme.Icon.allDayPaused
        case .stopped: Theme.Icon.error
        }
    }

    /// Red while recording, as the clock of any recording; a pause the user chose is quiet, one the system made (or a
    /// stop) asks to be looked at.
    var tone: Color {
        switch self {
        case .off: .primary
        case .recording: Theme.danger
        case .paused: .secondary
        case .interrupted, .stopped: Theme.cautionText
        }
    }
}
