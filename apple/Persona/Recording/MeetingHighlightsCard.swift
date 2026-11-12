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
/// as it was, or the Highlights card once the room set highlights for a meeting started with them, cross-fading in place
/// on `Theme.Motion.state` (at once with Reduce Motion); the folded Live box while the keyboard is up. The page hands it
/// the meeting's highlights as it reads them, so this card observes nothing of `MeetingConversation`, and its rows are
/// listed as it is made (at most four, as `MeetingTopicsRow` lists the topics): the first frame is already the right card,
/// so nothing animates as the page opens or switches layout. Live Transcript's choice and the transcript a highlight's
/// time pushes are the page's, kept while the page is open. A new or changed highlight is never announced.
struct MeetingTopCard: View {
    var entry: RecordingEntry
    var exchanges: [MeetingAsk.Exchange]
    /// The composer has the keyboard (the page passes it at the regular sizes only).
    var writing: Bool
    /// Live Transcript: closed until the owner opens it, then as they left it while the page is open.
    @Binding var open: Bool
    /// The transcript pushed at a highlight's time (the page's `navigationDestination`).
    @Binding var transcriptAt: MeetingTranscriptFocus?
    private let rows: [MeetingHighlights.Row]

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// `highlights`: the room's, as the page read them (none for a meeting started without them).
    init(entry: RecordingEntry, exchanges: [MeetingAsk.Exchange], highlights: [MeetingHighlight], writing: Bool,
         open: Binding<Bool>, transcriptAt: Binding<MeetingTranscriptFocus?>) {
        self.entry = entry
        self.exchanges = exchanges
        self.writing = writing
        _open = open
        _transcriptAt = transcriptAt
        rows = MeetingHighlights.rows(highlights)
    }

    var body: some View {
        let mode = MeetingHighlights.mode(shows: entry.attendance?.highlights == true, hasHighlights: !rows.isEmpty,
                                          writing: writing, accessibilitySize: size.isAccessibilitySize)
        // One over the other while they fade, from the top: in a stack of their own the outgoing card would keep its slot
        // until it is gone, and push the thread down meanwhile. Only a change of the mode animates (the first highlights,
        // the keyboard), never the first frame.
        ZStack(alignment: .top) {
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
                // Mounted only while open: the transcript's lines are read only while they show.
                MeetingHighlightsLines(entry: entry, exchanges: exchanges, open: $open)
            } else {
                MeetingCardRow {
                    MeetingLiveTranscriptToggle(open: $open)
                } trailing: {
                    MeetingFullTranscriptLink(id: entry.id, exchanges: exchanges)
                }
            }
        }
        .padding(.horizontal, Theme.Space.m)
        // No margin at the bottom: the last row's 44 pt controls close the card, as Full Transcript closes the Live box.
        .padding(.top, Theme.Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-highlights")
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
}

/// The open card's part under the highlights: Live Transcript with how far behind on the other side, as "Live" heads the
/// Live box's lines, the Live box's newest lines and Full Transcript. A view of its own, there only while the card is
/// open, so the transcript's lines (every segment sorted and filtered, `MeetingLiveBox.rows`) are read only while they
/// show, and again when the recording changes (P4, P9), as the Live box reads them.
private struct MeetingHighlightsLines: View {
    var entry: RecordingEntry
    var exchanges: [MeetingAsk.Exchange]
    @Binding var open: Bool

    @State private var lines: [LiveRow] = []

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            MeetingCardRow {
                MeetingLiveTranscriptToggle(open: $open)
            } trailing: {
                MeetingLiveBehind(last: lines.last)
            }
            ForEach(lines) { row in MeetingLiveLine(row: row) }
                .transition(.opacity)
            HStack {
                Spacer(minLength: Theme.Space.flush)
                MeetingFullTranscriptLink(id: entry.id, exchanges: exchanges)
            }
        }
        .onChange(of: entry.revision, initial: true) {
            let next = MeetingLiveBox.rows(entry)
            if next != lines { lines = next }
        }
    }
}

/// Live Transcript: opens or closes the newest lines in place; one line, its chevron saying which way it goes.
private struct MeetingLiveTranscriptToggle: View {
    @Binding var open: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Button(action: toggle) {
            HStack(spacing: Theme.Space.xs) {
                // Shrinks a little only where it stands alone and still doesn't fit (a long translation at the largest
                // sizes); beside the other label the row stacks first (`MeetingCardRow`).
                Text("Live Transcript").lineLimit(1).minimumScaleFactor(0.7)
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

    private func toggle() {
        let opening = !open
        withAnimation(reduceMotion ? nil : (opening ? Theme.Motion.reveal : Theme.Motion.conceal)) { open = opening }
    }
}

/// The card's last row: the first on the leading side and the second on the trailing side, each at its full width, while
/// the two fit side by side at that size and in that language; one under the other otherwise, and always at the
/// accessibility sizes, so neither label is ever cut (DESIGN_SYSTEM.md 1.2, rule 3).
private struct MeetingCardRow<Leading: View, Trailing: View>: View {
    @ViewBuilder var leading: Leading
    @ViewBuilder var trailing: Trailing

    @Environment(\.dynamicTypeSize) private var size

    var body: some View {
        if size.isAccessibilitySize {
            stacked
        } else {
            ViewThatFits(in: .horizontal) {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                    leading.fixedSize()
                    Spacer(minLength: Theme.Space.s)
                    trailing.fixedSize()
                }
                stacked
            }
        }
    }

    private var stacked: some View {
        VStack(alignment: .leading, spacing: Theme.Space.flush) { leading; trailing }
    }
}
#endif
