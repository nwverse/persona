import SwiftUI

#if os(iOS)
/// One list-control surface: choices scroll, while its trailing menu stays in reach.
struct CapsuleFilterItemData: Identifiable {
    var id: String
    var title: String
    var count: Int?
    var tone: StatusTone?
    var symbol: String?
    /// The count drawn as a `CountBadge` of this look (zero hides it, 99+ caps it, and the spoken name says "Title, n"),
    /// as the Inbox's section bar draws it (#9053); nil keeps the plain number the Agents filters show.
    var countLook: CountBadge.Look? = nil
}

struct CapsuleFilterBar<Trailing: View>: View {
    var items: [CapsuleFilterItemData]
    var selection: String
    var choose: (String) -> Void
    /// The strip's identifier and its choices' prefix (`<prefix><id>`): the Agents list's by default.
    var scrollID = "agents.filters.scroll"
    var itemIDPrefix = "agents.filter."
    /// The strip scrolls the chosen item into sight whenever the choice changes, by a tap or by the page (a search
    /// showing All, a landing), and when it appears; by the least it can, so the first items stay in sight as far as
    /// they fit (the Inbox's section bar, #9053). Off: a tap centres its item, as the Agents filters do.
    var revealsSelection = false
    @ViewBuilder var trailing: Trailing
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var focused: String?
    @State private var geometry = FilterStripGeometry()

    var body: some View {
        HStack(spacing: Theme.Space.flush) {
            ScrollViewReader { proxy in
                ScrollView(.horizontal) {
                    HStack(spacing: Theme.Space.xs) {
                        ForEach(items) { item in
                            CapsuleFilterItem(item: item, selected: item.id == selection,
                                              expanded: item.id == selection || item.id == focused, idPrefix: itemIDPrefix) {
                                choose(item.id)
                                // revealing, the choice's change scrolls (below), from the layout it makes
                                guard !revealsSelection else { return }
                                withAnimation(reduceMotion ? nil : Theme.Motion.standard) {
                                    proxy.scrollTo(item.id, anchor: .center)
                                }
                            }
                            .id(item.id)
                            .background {
                                GeometryReader { frame in
                                    Color.clear.preference(key: FilterStripFrames.self,
                                                           value: [item.id: frame.frame(in: .named("filterStrip"))])
                                }
                            }
                        }
                    }
                    .padding(.horizontal, Theme.Space.xs)
                }
                // A horizontal strip must never inherit the navigation bar's vertical content inset.
                .contentMargins(.vertical, 0, for: .scrollContent)
                .scrollIndicators(.hidden)
                .scaledFrame(height: Theme.Size.tapTarget)
                .frame(minHeight: Theme.Size.tapTarget)
                .coordinateSpace(name: "filterStrip")
                .onPreferenceChange(FilterStripFrames.self) { geometry.frames = $0 }
                .onScrollGeometryChange(for: FilterStripViewport.self) {
                    .init(offset: $0.contentOffset.x + $0.contentInsets.leading, width: $0.containerSize.width)
                } action: { _, viewport in geometry.viewport = viewport }
                .onScrollPhaseChange { _, phase in
                    // Pick once after a gesture, not from the widths that disclosure itself changes (P7).
                    guard phase == .idle else { return }
                    withAnimation(reduceMotion ? nil : Theme.Motion.standard) {
                        focused = geometry.centered()
                    }
                }
                .accessibilityIdentifier(scrollID)
                .onAppear { if revealsSelection { reveal(selection, proxy, animated: false) } }
                .onChange(of: selection) { _, chosen in
                    if revealsSelection { reveal(chosen, proxy, animated: true) }
                }
            }
            Rectangle().fill(Theme.cardStroke)
                .frame(width: Theme.Size.hairline)
                .scaledFrame(height: Theme.FilterCapsule.separatorHeight)
                .accessibilityHidden(true)
            trailing
        }
        .background(Theme.card, in: Capsule())
        .overlay(Capsule().strokeBorder(Theme.cardStroke, lineWidth: Theme.Size.hairline))
        .clipShape(Capsule())
    }

    /// Scrolls to `id` once this update is laid out: on the strip's first appearance, or a choice the page made as it
    /// appeared, its items have no frames yet and an immediate scroll does nothing.
    private func reveal(_ id: String, _ proxy: ScrollViewProxy, animated: Bool) {
        Task { @MainActor in
            withAnimation(animated && !reduceMotion ? Theme.Motion.standard : nil) { proxy.scrollTo(id) }
        }
    }
}

private struct CapsuleFilterItem: View {
    var item: CapsuleFilterItemData
    var selected: Bool
    var expanded: Bool
    var idPrefix: String
    var action: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var fullWidth: CGFloat = 0
    @State private var compactWidth: CGFloat = 0

    private var ink: Color { selected ? Theme.accent : .primary }

    var body: some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.xs) {
                if let tone = item.tone {
                    if let symbol = item.symbol {
                        ThemedIcon(symbol).font(Theme.Font.label).foregroundStyle(tone.dot)
                    } else {
                        Circle().fill(tone.dot).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                    }
                }
                Text(expanded ? item.title : FilterCapsuleLabel.compact(item.title))
                    .lineLimit(1)
                    .fixedSize()
                    .frame(width: expanded ? fullWidth : compactWidth, alignment: .leading)
                    .clipped()
                    .contentTransition(.opacity)
                    .background {
                        ZStack {
                            measured(item.title) { fullWidth = $0 }
                            measured(FilterCapsuleLabel.compact(item.title)) { compactWidth = $0 }
                        }
                        .hidden()
                    }
                if let count = item.count {
                    if let look = item.countLook {
                        CountBadge(count: count, look: look)
                    } else {
                        Text("\(count)").monospacedDigit()
                            .foregroundStyle(selected ? ink : .secondary)
                            .contentTransition(.numericText(value: Double(count)))
                            .fixedSize()
                    }
                }
            }
            .font(Theme.Font.secondaryMedium)
            .foregroundStyle(ink)
            .padding(.horizontal, Theme.Space.s)
            .padding(.vertical, Theme.Space.xs)
            .background(selected ? Theme.FilterCapsule.selection : .clear, in: Capsule())
            .padding(.vertical, Theme.Space.xs)
            .scaledFrame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .animation(reduceMotion ? nil : Theme.Motion.standard, value: expanded)
        .animation(reduceMotion ? nil : Theme.Motion.standard, value: selected)
        .accessibilityIdentifier(idPrefix + item.id)
        .accessibilityLabel(spokenName)
        #if DEBUG
        .accessibilityValue(expanded ? Text("Expanded") : Text("Collapsed [Readiness Card ee6046]"))
        #endif
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// The title with its count: a badge's only while it shows one, the exact number past 99 too.
    private var spokenName: String {
        guard let count = item.count else { return item.title }
        guard item.countLook != nil else { return "\(item.title) \(count)" }
        return count > 0 ? item.title + String(localized: ", ") + String(count) : item.title
    }

    private func measured(_ title: String, changed: @escaping (CGFloat) -> Void) -> some View {
        Text(title).font(Theme.Font.secondaryMedium).fixedSize()
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { changed($0) }
    }
}

/// Measurements are not observable: scrolling never redraws the surrounding Agents list.
private final class FilterStripGeometry {
    var frames: [String: CGRect] = [:]
    var viewport: FilterStripViewport?
    init() {}
    func centered() -> String? {
        guard let viewport, viewport.offset > Theme.Space.xs else { return nil }
        return FilterCapsuleLabel.centered(frames: frames, width: viewport.width)
    }
}

private nonisolated struct FilterStripViewport: Equatable {
    var offset: CGFloat
    var width: CGFloat
}

private nonisolated struct FilterStripFrames: PreferenceKey {
    static var defaultValue: [String: CGRect] { [:] }
    static func reduce(value: inout [String: CGRect], nextValue: () -> [String: CGRect]) {
        value.merge(nextValue(), uniquingKeysWith: { _, next in next })
    }
}

nonisolated enum FilterCapsuleLabel {
    static func compact(_ title: String) -> String {
        title.count > 2 ? String(title.prefix(2)) + "…" : title
    }
    static func centered(frames: [String: CGRect], width: CGFloat) -> String? {
        frames.filter { $0.value.maxX > 0 && $0.value.minX < width }
            .min { abs($0.value.midX - width / 2) < abs($1.value.midX - width / 2) }?.key
    }
}
#endif
