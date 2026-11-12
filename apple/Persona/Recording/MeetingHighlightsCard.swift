#if os(iOS)
import SwiftUI

/// What the meeting page's top card shows (#3862 phase 2b; docs/assistant/meeting-minutes.md「The meeting card」), worked
/// out here and not in a `body` (P4), pure, for the tests: phase 2's Live box while the meeting has no highlights (or
/// started without them), the Highlights card once the room set any, and the folded Live box while the keyboard is up.
nonisolated enum MeetingHighlights {
    nonisolated enum Mode: Equatable, Sendable {
        /// Phase 2's Live box; `folded`: only its newest line, while the keyboard is up.
        case live(folded: Bool)
        case highlights
    }

    /// One highlight as the card lists it: its time in the clock's words, and an identity that stays while the room
    /// sends the list again (its time and words; the same ones twice get their count), so a kept row is not rebuilt (P6).
    nonisolated struct Row: Identifiable, Equatable, Sendable {
        var id: String
        var text: String
        var at: Double
        var end: Double
        /// `RecordingFormat.clock(at)`: from the recording's start.
        var clock: String
    }

    /// The card for a meeting started with the highlights (`shows`, `RecordingAttendance.highlights`) and the rows it has:
    /// highlights once there are any, else the Live box. While the keyboard is up (`writing`, the regular sizes only: at the
    /// accessibility sizes the page scrolls as one and nothing folds), the folded Live box either way, since a typed
    /// question is usually about what was just said.
    static func mode(shows: Bool, hasHighlights: Bool, writing: Bool, accessibilitySize: Bool) -> Mode {
        let folded = writing && !accessibilitySize
        guard shows, hasHighlights, !folded else { return .live(folded: folded) }
        return .highlights
    }

    /// The rows, in the order given (`MeetingConversation.applyHighlights` keeps them earliest first).
    static func rows(_ highlights: [MeetingHighlight]) -> [Row] {
        var used = Set<String>()
        return highlights.map { h in
            let key = "\(RecordingTimeline.millis(h.at))|\(h.text)"
            var id = key, n = 1
            while used.contains(id) {
                n += 1
                id = "\(key)#\(n)"
            }
            used.insert(id)
            return Row(id: id, text: h.text, at: h.at, end: h.end, clock: RecordingFormat.clock(h.at))
        }
    }

    /// The newest highlight: the latest `at`, the last given of those at the same time.
    static func newest(_ rows: [Row]) -> Row? {
        rows.enumerated().max { a, b in a.element.at != b.element.at ? a.element.at < b.element.at : a.offset < b.offset }?.element
    }

    /// The highlights above the card's lines: every one while it is closed, and open at the accessibility sizes (the page
    /// scrolls as one there); open at the regular sizes, where only the thread scrolls, the newest alone.
    static func shown(_ rows: [Row], open: Bool, accessibilitySize: Bool) -> [Row] {
        guard open, !accessibilitySize else { return rows }
        return newest(rows).map { [$0] } ?? []
    }
}

/// The meeting page's top card (#3862 phase 2b; docs/design/DESIGN_SYSTEM.md「Meeting page (#3862)」): phase 2's Live box
/// as it was, or the Highlights card once the room set highlights for a meeting started with them, cross-fading on
/// `Theme.Motion.state` (at once with Reduce Motion); the folded Live box while the keyboard is up. A view of its own, so
/// the highlights are read and listed here when they change (`MeetingHighlights.rows`), and a highlight's time pushes the
/// transcript at its minute. A new or changed highlight is never announced.
struct MeetingTopCard: View {
    var entry: RecordingEntry
    var exchanges: [MeetingAsk.Exchange]
    /// The composer has the keyboard (the page passes it at the regular sizes only).
    var writing: Bool

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var conversation = MeetingConversation.shared
    @State private var rows: [MeetingHighlights.Row] = []
    /// Live Transcript: closed until the owner opens it, then as they left it while the page is open.
    @State private var open = false
    /// The transcript pushed at a highlight's time.
    @State private var transcriptAt: MeetingTranscriptFocus?

    var body: some View {
        let mode = MeetingHighlights.mode(shows: entry.attendance?.highlights == true, hasHighlights: !rows.isEmpty,
                                          writing: writing, accessibilitySize: size.isAccessibilitySize)
        Group {
            switch mode {
            case .live(let folded):
                MeetingLiveBox(entry: entry, exchanges: exchanges, folded: folded)
                    .transition(.opacity)
            case .highlights:
                MeetingHighlightsCard(entry: entry, exchanges: exchanges, rows: rows, open: $open) { row in
                    transcriptAt = MeetingTranscriptFocus(at: row.at, end: row.end)
                }
                .transition(.opacity)
            }
        }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: mode)
        // Here, not on the card: the destination stays while the card turns into the Live box under the pushed page.
        .navigationDestination(item: $transcriptAt) { focus in
            MeetingTranscriptPage(id: entry.id, exchanges: exchanges, focus: focus)
        }
        // Only for a meeting started with them: a read that brings highlights changes the rows, and only then.
        .onChange(of: entry.attendance?.highlights == true ? conversation.meeting(entry.id).highlights : [], initial: true) { _, new in
            let next = MeetingHighlights.rows(new)
            if next != rows { rows = next }
        }
    }
}

/// The Highlights card: "Highlights" and each highlight with its time, earliest first, then Live Transcript, which opens
/// the card in place to the newest lines as the Live box draws them, and Full Transcript, one tap away open or closed.
/// Open at the regular sizes only the newest highlight stays above the lines (only the thread scrolls there).
private struct MeetingHighlightsCard: View {
    var entry: RecordingEntry
    var exchanges: [MeetingAsk.Exchange]
    var rows: [MeetingHighlights.Row]
    @Binding var open: Bool
    var show: (MeetingHighlights.Row) -> Void

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    /// The newest lines Live Transcript opens, read when the recording changes (P4), as the Live box reads them.
    @State private var lines: [LiveRow] = []

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text("Highlights [Meeting]").font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                .accessibilityAddTraits(.isHeader)
            // At the accessibility sizes a time sits under its words: a block's gap between highlights keeps each time
            // with its own words, not the next ones.
            VStack(alignment: .leading, spacing: size.isAccessibilitySize ? Theme.Space.block : Theme.Space.s) {
                ForEach(MeetingHighlights.shown(rows, open: open, accessibilitySize: size.isAccessibilitySize)) { row in
                    highlight(row)
                        .transition(.opacity)
                }
            }
            if open {
                // Live Transcript heads the lines as "Live" heads the Live box's, with how far behind on the other side.
                controls(disclosure, MeetingLiveBehind(last: lines.last))
                ForEach(lines) { row in MeetingLiveLine(row: row) }
                    .transition(.opacity)
                HStack {
                    Spacer(minLength: Theme.Space.flush)
                    MeetingFullTranscriptLink(id: entry.id, exchanges: exchanges)
                }
            } else {
                controls(disclosure, MeetingFullTranscriptLink(id: entry.id, exchanges: exchanges))
            }
        }
        .padding(.horizontal, Theme.Space.m)
        // No margin at the bottom: the last row's 44 pt controls close the card, as Full Transcript closes the Live box.
        .padding(.top, Theme.Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-highlights")
        .onChange(of: entry.revision, initial: true) {
            let next = MeetingLiveBox.rows(entry)
            if next != lines { lines = next }
        }
    }

    /// A highlight: its time as the minutes cards' chip, then its words (`Theme.Recording.highlightLines` lines at most,
    /// cut at the end; VoiceOver reads them whole). At the accessibility sizes the words take what they need and the time
    /// goes under them.
    @ViewBuilder private func highlight(_ row: MeetingHighlights.Row) -> some View {
        let words = Text(row.text).font(Theme.Font.body)
            .lineLimit(size.isAccessibilitySize ? nil : Theme.Recording.highlightLines)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
        Group {
            if size.isAccessibilitySize {
                VStack(alignment: .leading, spacing: Theme.Space.flush) { words; time(row) }
            } else {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) { time(row); words }
            }
        }
        // The words and their time together; the time stays its own button (a `.combine` would hide it from the tests).
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-highlight")
    }

    /// Its time from the recording's start, the minutes cards' time: a tap opens the transcript at its minute, lit.
    private func time(_ row: MeetingHighlights.Row) -> some View {
        Button { show(row) } label: {
            Chip(text: row.clock, look: .tint(Theme.accent), compact: true)
                .scaledFrame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(String(localized: "Show at \(row.clock) in the transcript"))
        .accessibilityIdentifier("meeting-highlight-time")
    }

    /// Live Transcript: opens or closes the newest lines in place; one line, its chevron saying which way it goes.
    private var disclosure: some View {
        Button(action: toggle) {
            HStack(spacing: Theme.Space.xs) {
                Text("Live Transcript").lineLimit(1)
                ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand).font(Theme.Font.captionStrong).accessibilityHidden(true)
            }
            .font(Theme.Font.secondaryStrong)
            .foregroundStyle(Theme.accent)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityValue(open ? Text("Expanded") : Text("Collapsed"))
        .accessibilityIdentifier("meeting-live-transcript")
    }

    /// One row, the first on the leading side and the second on the trailing side; at the accessibility sizes one under
    /// the other, so neither wraps.
    @ViewBuilder private func controls(_ leading: some View, _ trailing: some View) -> some View {
        if size.isAccessibilitySize {
            VStack(alignment: .leading, spacing: Theme.Space.flush) { leading; trailing }
        } else {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                leading
                Spacer(minLength: Theme.Space.s)
                trailing
            }
        }
    }

    private func toggle() {
        let opening = !open
        withAnimation(reduceMotion ? nil : (opening ? Theme.Motion.reveal : Theme.Motion.conceal)) { open = opening }
    }
}
#endif
