import SwiftUI
import WidgetKit

/// Meeting Recording on the watch face and in the Smart Stack (docs/apple/recording.md「Apple Watch」): a tap opens the
/// watch app straight into a meeting recording (persona://record?meeting=1). Static: it shows the same at all times.
struct MeetingRecordingComplication: Widget {
    static let kind = "persona.watch.meeting-recording"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: Self.kind, provider: MeetingRecordingProvider()) { _ in
            MeetingRecordingComplicationView()
                .containerBackground(WatchFaceTokens.background, for: .widget)
                .widgetURL(URL(string: "persona://record?meeting=1"))
        }
        .configurationDisplayName("Meeting Recording")
        .description("Starts recording a meeting on Apple Watch.")
        .supportedFamilies([.accessoryCircular, .accessoryCorner, .accessoryRectangular, .accessoryInline])
    }
}

struct MeetingRecordingEntry: TimelineEntry { var date: Date }

struct MeetingRecordingProvider: TimelineProvider {
    func placeholder(in context: Context) -> MeetingRecordingEntry { MeetingRecordingEntry(date: .now) }
    func getSnapshot(in context: Context, completion: @escaping (MeetingRecordingEntry) -> Void) { completion(MeetingRecordingEntry(date: .now)) }
    func getTimeline(in context: Context, completion: @escaping (Timeline<MeetingRecordingEntry>) -> Void) {
        completion(Timeline(entries: [MeetingRecordingEntry(date: .now)], policy: .never))
    }
}

struct MeetingRecordingComplicationView: View {
    @Environment(\.widgetFamily) private var family

    var body: some View {
        switch family {
        case .accessoryCircular:
            ZStack {
                AccessoryWidgetBackground()
                Image(systemName: WatchFaceTokens.recordingSymbol).font(WatchFaceTokens.recordingGlyph)
                    .foregroundStyle(WatchFaceTokens.recording)
            }
            .accessibilityLabel(Text("Meeting Recording"))
        case .accessoryCorner:
            Image(systemName: WatchFaceTokens.recordingSymbol).font(WatchFaceTokens.recordingGlyph)
                .foregroundStyle(WatchFaceTokens.recording)
                .widgetLabel { Text("Meeting Recording") }
        case .accessoryInline:
            Label("Meeting Recording", systemImage: WatchFaceTokens.recordingSymbol)
        default:
            HStack(spacing: WatchFaceTokens.previewSpacing) {
                Image(systemName: WatchFaceTokens.recordingSymbol).font(WatchFaceTokens.recordingGlyph)
                    .foregroundStyle(WatchFaceTokens.recording)
                VStack(alignment: .leading) {
                    Text("Meeting Recording").font(WatchFaceTokens.title).lineLimit(1).minimumScaleFactor(WatchFaceTokens.titleMinScale)
                    Text("Tap to record").font(WatchFaceTokens.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
            }
        }
    }
}
