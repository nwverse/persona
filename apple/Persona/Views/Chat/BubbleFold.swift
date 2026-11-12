import SwiftUI

// MARK: - Your own long message, folded (#5676)

/// When your own message folds, as the web folds it (`BubbleText` in web/src/views/chat/SegmentView.tsx): past `limit`
/// lines it shows its first `shown`, the last one fading, with Show More at the bubble's bottom. At the accessibility
/// text sizes ten lines are taller than a phone's screen, so there it folds past nine lines to five (the web has no such
/// sizes). The lines are the ones drawn, at the bubble's width.
nonisolated enum BubbleFoldRule: Equatable, Sendable {
    case regular, large

    init(_ size: DynamicTypeSize) { self = size.isAccessibilitySize ? .large : .regular }

    /// Past this many lines it folds (the web's BUBBLE_MAX_LINES).
    var limit: Int { self == .regular ? 14 : 9 }
    /// Folded, this many show.
    var shown: Int { self == .regular ? 10 : 5 }

    /// Characters to a line of the narrowest bubble at the body's default size (wide CJK characters in an iPad's
    /// narrowest split), scaled down as the text grows: a message within `limit` lines even so cannot fold, and is drawn
    /// as before, never measured (the web's NARROWEST_CHARS).
    static let narrowest: CGFloat = 9
    /// The same, at a phone's bubble, for whether it folds before it is measured (the web's CHARS_PER_LINE).
    static let typical: CGFloat = 16
    /// The body's size at the default text size, which `narrowest` and `typical` are for.
    static let bodySize: CGFloat = 17
    /// Folded, a long message lays out only its start: this many characters at most, and its first `limit + 2` lines.
    /// That is past `limit` lines at the widest bubble (the chat's readable width, narrow Latin letters); a start that is
    /// not is drawn whole instead (`BubbleFold`).
    static let startCharacters = 3000

    /// DEBUG `-showWholeBubbles YES`: every bubble whole, as before #5676 (before / after screenshots and measurements
    /// from one build).
    #if DEBUG
    static let whole = UserDefaults.standard.bool(forKey: "showWholeBubbles")
    #else
    static let whole = false
    #endif

    /// Characters to a line, `base` at the default size, at `size` (the body's point size now).
    static func perLine(_ base: CGFloat, size: CGFloat) -> Int { max(1, Int((base * bodySize / max(size, 1)).rounded(.down))) }

    /// How many lines `lengths` (each line's characters) take at `perLine` characters a line.
    static func lines(_ lengths: [Int], perLine: Int) -> Int {
        lengths.reduce(0) { $0 + max(1, ($1 + perLine - 1) / perLine) }
    }

    /// Each line's characters, once per text.
    static func lengths(_ text: String) -> [Int] {
        lengthsMemo(text) { $0.split(separator: "\n", omittingEmptySubsequences: false).map(\.count) }
    }
    private static let lengthsMemo = TextMemo<[Int]>(limit: 400)

    /// The start of `text` a folded bubble draws (`startCharacters`, its first `lines` lines); nil when that is all of it.
    static func start(of text: String, lines: Int) -> String? {
        var count = 0
        var breaks = 0
        var end = text.startIndex
        for i in text.indices {
            if count >= startCharacters { break }
            if text[i] == "\n" {
                breaks += 1
                if breaks >= lines { break }
            }
            count += 1
            end = text.index(after: i)
        }
        return end == text.endIndex ? nil : String(text[..<end])
    }
}

/// What a bubble needs to fold its words: where its open state is kept (`OpenReports`, in the chat's scope), the key its
/// row is pinned by (`chatPinned`), and the bubble's own `folded` (its long press opens it instead of lifting it).
struct BubbleFoldKey {
    var id: String
    var folded: Binding<Bool>
    /// The row's `chatPinned` key: opening and folding keep the bubble's top where it is on screen.
    var pin: String { "bubble-" + id }

    /// Where its open state is kept (`OpenReports`), in the chat's `scope`.
    func openKey(_ scope: String) -> String { scope + "/" + pin }

    /// A long press on the bubble (`messageLongPress(opens:)`): folded, it opens, as Show More does, and takes the press.
    @MainActor func openOnPress(scope: String, pin chat: ChatPin?) -> Bool {
        guard folded.wrappedValue else { return false }
        Self.flip(openKey(scope), pinKey: pin, open: false, pin: chat)
        return true
    }

    /// Opens or folds it in place (`toggleInPlace`), as Show More / Show Less do: its top stays where it is on screen,
    /// at the newest end too (no rise to the top bar, the owner's ask).
    @MainActor static func flip(_ key: String, pinKey: String, open: Bool, pin: ChatPin?) {
        Haptics.tap()
        toggleInPlace(pin, pinKey, animation: Theme.Motion.state, closing: open, rises: false) { OpenReports.shared.set(key, !open) }
    }
}

/// The words of your own bubble, folded past the rule's lines (`BubbleFoldRule`) with Show More / Show Less under them,
/// in the bubble. A message that cannot run past the rule's lines even in the narrowest bubble is drawn as before, with
/// nothing measured; one that can is measured once its words are laid out (`onGeometryChange` on their size, which
/// scrolling does not change), not as it scrolls. Folded, only its start is laid out (`BubbleFoldRule.start`). The lifted
/// copy (`liftCopy`) is always whole.
struct BubbleFold<Words: View>: View {
    var text: String
    var fold: BubbleFoldKey?
    /// The color of Show More: the bubble's link color (`UserBubbleInk.link`).
    var tint: Color
    @ViewBuilder var words: (String) -> Words

    @Environment(\.dynamicTypeSize) private var typeSize
    @Environment(\.liftCopy) private var lifted
    @ScaledMetric(relativeTo: .body) private var bodySize = BubbleFoldRule.bodySize

    var body: some View {
        #if os(macOS)
        words(text) // the Mac app is frozen (docs/apple/mac.md): its bubbles stay whole
        #else
        let rule = BubbleFoldRule(typeSize)
        let lengths = BubbleFoldRule.lengths(text)
        if let fold, !lifted, !BubbleFoldRule.whole,
           BubbleFoldRule.lines(lengths, perLine: BubbleFoldRule.perLine(BubbleFoldRule.narrowest, size: bodySize)) > rule.limit {
            let guess = BubbleFoldRule.lines(lengths, perLine: BubbleFoldRule.perLine(BubbleFoldRule.typical, size: bodySize)) > rule.limit
            Folding(text: text, fold: fold, rule: rule, guess: guess, tint: tint, words: words)
        } else {
            words(text)
        }
        #endif
    }
}

/// A bubble's words that may run past the rule's lines: measured, and cut at `rule.shown` lines while folded.
private struct Folding<Words: View>: View {
    var text: String
    var fold: BubbleFoldKey
    var rule: BubbleFoldRule
    /// Whether it folds by its characters, until it is measured.
    var guess: Bool
    var tint: Color
    var words: (String) -> Words

    /// The drawn words' height, and the height of `rule.shown` body lines (what a folded bubble shows).
    @State private var full: CGFloat = 0
    @State private var cut: CGFloat = 0
    /// Its start reached no further than the rule's lines (narrow letters at a wide bubble): it is drawn whole.
    @State private var whole = false
    @Environment(\.rowMemoScope) private var scope
    @Environment(\.chatPin) private var pin

    private var key: String { fold.openKey(scope) }
    /// Measured: longer than the rule's lines (`full` is that many lines and a half past `cut`'s).
    private var long: Bool {
        guard full > 0, cut > 0 else { return guess }
        return full > cut * (CGFloat(rule.limit) + 0.5) / CGFloat(rule.shown)
    }

    var body: some View {
        let open = OpenReports.shared.isOpen(key)
        let folded = long && !open
        let start = folded && !whole ? BubbleFoldRule.start(of: text, lines: rule.limit + 2) : nil
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            words(start ?? text)
                .fixedSize(horizontal: false, vertical: true)
                .onGeometryChange(for: CGFloat.self) { $0.size.height.rounded() } action: { h in
                    full = h
                    // Only its start was laid out, and it did not run past the rule's lines: draw all of it.
                    if start != nil, cut > 0, h <= cut * (CGFloat(rule.limit) + 0.5) / CGFloat(rule.shown) { whole = true }
                }
                .frame(height: folded && cut > 0 ? cut : nil, alignment: .top)
                .clipped()
                .contentShape(.rect) // what is folded away takes no touches (the words run on past the fold)
                .mask { if folded && cut > 0 { fade } else { Rectangle() } }
                .background(alignment: .top) { probe }
            if long {
                FoldToggle(open: open, prefix: "bubble", tint: tint, showMore: true) {
                    BubbleFoldKey.flip(key, pinKey: fold.pin, open: open, pin: pin)
                }
            }
        }
        // Only a bubble that can fold reports where it is as the chat scrolls (what opening and folding keep in place).
        .chatPinned(fold.pin)
        .onChange(of: folded, initial: true) { _, now in
            if fold.folded.wrappedValue != now { fold.folded.wrappedValue = now }
        }
    }

    /// `rule.shown` lines of body text, unseen: the height the words are cut at (as `LineFold` measures it).
    private var probe: some View {
        Text(verbatim: Array(repeating: "X", count: rule.shown).joined(separator: "\n"))
            .font(Theme.Font.body)
            .lineHeight(.body)
            .fixedSize(horizontal: false, vertical: true)
            .hidden()
            .accessibilityHidden(true)
            .onGeometryChange(for: CGFloat.self) { $0.size.height.rounded() } action: { cut = $0 }
    }

    /// Opaque down to the last line shown, which fades out.
    private var fade: some View {
        VStack(spacing: 0) {
            Rectangle()
            LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom)
                .frame(height: cut / CGFloat(max(rule.shown, 1)))
        }
    }
}
