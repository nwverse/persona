import SwiftUI
import WidgetKit

/// What an All-Day Recording complication draws (`AllDayRecordingComplication`); the watch app compiles this file too,
/// for its debug preview of the faces. Only the view: the widget and its provider stay in the extension.
struct AllDayEntry: TimelineEntry {
    let date: Date
    let snapshot: AllDaySnapshot?

    var face: AllDaySnapshot.Face { AllDaySnapshot.face(snapshot, at: date) }
}

/// Each family says the state in words and a mark, never by color alone; a recording counts its own time
/// (`Text(_:style: .timer)`), so the face needs no reload to stay current.
struct AllDayComplicationView: View {
    let entry: AllDayEntry
    let family: WidgetFamily

    var body: some View {
        let face = entry.face
        content(face)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(Text(face.title))
            .accessibilityValue(face.since.map { Text($0, style: .timer) } ?? Text(verbatim: ""))
            .accessibilityHint(face.hint.map { Text($0) } ?? Text(verbatim: ""))
    }

    @ViewBuilder private func content(_ face: AllDaySnapshot.Face) -> some View {
        switch family {
        case .accessoryCircular:
            ZStack {
                AccessoryWidgetBackground()
                VStack(spacing: 0) {
                    mark(face)
                    if let since = face.since {
                        Text(since, style: .timer).font(WatchFaceTokens.allDayLine)
                            .multilineTextAlignment(.center).lineLimit(1).minimumScaleFactor(WatchFaceTokens.allDayLineMinScale)
                    }
                }
            }
        case .accessoryCorner:
            mark(face).widgetLabel { label(face, short: true) }
        case .accessoryInline:
            Label { label(face, short: false) } icon: { Image(systemName: face.symbol) }
        default:
            HStack(spacing: WatchFaceTokens.allDaySpacing) {
                mark(face)
                VStack(alignment: .leading) {
                    Text(face.title).font(WatchFaceTokens.title).lineLimit(1).minimumScaleFactor(WatchFaceTokens.titleMinScale)
                    detail(face).font(WatchFaceTokens.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
            }
        }
    }

    private func mark(_ face: AllDaySnapshot.Face) -> some View {
        Image(systemName: face.symbol).font(WatchFaceTokens.allDayGlyph).foregroundStyle(face.tone).widgetAccentable()
    }

    /// One line: the running time while recording, else the state's word (`short`: for the corner's curved label).
    @ViewBuilder private func label(_ face: AllDaySnapshot.Face, short: Bool) -> some View {
        if let since = face.since { Text(since, style: .timer) } else { Text(short ? face.shortTitle : face.title) }
    }

    /// Under the title: the running time, what a tap does, or how long a stale recording has been unheard.
    @ViewBuilder private func detail(_ face: AllDaySnapshot.Face) -> some View {
        switch face {
        case .recording(let since): Text(since, style: .timer)
        case .stale(let lastHeard): Text(lastHeard, style: .relative)
        default: Text(face.hint ?? "")
        }
    }
}

extension AllDaySnapshot.Face {
    /// The state's word, short enough for every family. The page's status line ("Paused — tap to continue",
    /// `AllDaySnapshot.State.word(startable:)` in the watch app) won't fit a corner's curve or the inline line in every
    /// language, so the face splits it: this word, and `hint` on the rectangular face's second line and in VoiceOver's
    /// hint.
    var title: String {
        switch self {
        case .off: String(localized: "All-Day Recording")
        case .recording: String(localized: "Recording all day")
        case .paused: String(localized: "Paused")
        case .stopped: String(localized: "Stopped")
        case .stale: String(localized: "Check recording")
        }
    }

    /// The title where little fits (the corner): the mode's short name when off.
    var shortTitle: String {
        if case .off = self { String(localized: "All-Day") } else { title }
    }

    /// What a tap does, where the face has room to say it.
    var hint: String? {
        switch self {
        case .off: String(localized: "Tap to start")
        case .paused, .stopped: String(localized: "Tap to continue")
        case .recording, .stale: nil
        }
    }

    /// The start of the running time; nil unless recording.
    var since: Date? {
        if case .recording(let since) = self { since } else { nil }
    }

    var symbol: String {
        switch self {
        case .off, .recording: WatchFaceTokens.allDaySymbol
        case .paused: WatchFaceTokens.allDayPausedSymbol
        case .stopped, .stale: WatchFaceTokens.allDayAlertSymbol
        }
    }

    /// Red while recording, as the system shows a microphone in use; orange while it waits for a tap or a look.
    var tone: Color {
        switch self {
        case .off: .primary
        case .recording: WatchFaceTokens.recording
        case .paused, .stopped, .stale: WatchFaceTokens.caution
        }
    }
}

#if DEBUG
/// `-openWatchItem allday-complication` in the watch app: every family in every state (off, recording, paused,
/// stopped, a recording gone stale) at a 46 mm face's sizes, on a mock face's grey, since a simulator cannot put a
/// complication on a face from the command line (screenshots).
struct AllDayComplicationPreview: View {
    var body: some View {
        let now = Date.now
        let since = now.addingTimeInterval(-(3 * 3600 + 25 * 60))
        let entries: [AllDayEntry] = [
            AllDayEntry(date: now, snapshot: nil),
            AllDayEntry(date: now, snapshot: AllDaySnapshot(state: .recording, since: since, updatedAt: now)),
            AllDayEntry(date: now, snapshot: AllDaySnapshot(state: .interrupted, since: now, updatedAt: now)),
            AllDayEntry(date: now, snapshot: AllDaySnapshot(state: .stopped, since: now, updatedAt: now)),
            AllDayEntry(date: now, snapshot: AllDaySnapshot(state: .recording, since: since,
                                                            updatedAt: now.addingTimeInterval(-AllDaySnapshot.staleAfter - 60))),
        ]
        let size = WatchFaceTokens.previewRectangular
        ScrollView {
            VStack(alignment: .leading, spacing: WatchFaceTokens.previewSpacing) {
                ForEach(Array(entries.enumerated()), id: \.offset) { _, entry in
                    HStack(spacing: WatchFaceTokens.previewSpacing) {
                        AllDayComplicationView(entry: entry, family: .accessoryCircular)
                            .frame(width: WatchFaceTokens.previewCircular, height: WatchFaceTokens.previewCircular)
                            .background(WatchFaceTokens.previewWash, in: Circle())
                        AllDayComplicationView(entry: entry, family: .accessoryCorner)
                            .frame(width: WatchFaceTokens.previewCorner, height: WatchFaceTokens.previewCorner)
                        AllDayComplicationView(entry: entry, family: .accessoryInline).font(WatchFaceTokens.previewInline).lineLimit(1)
                    }
                    AllDayComplicationView(entry: entry, family: .accessoryRectangular).frame(width: size.width, height: size.height)
                        .background(WatchFaceTokens.previewWash, in: RoundedRectangle(cornerRadius: WatchFaceTokens.previewRadius))
                }
            }
        }
        .accessibilityIdentifier("watch-allday-complication-preview")
    }
}
#endif
