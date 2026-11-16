import SwiftUI

#if os(iOS)
/// One choice of the strip: what its chip draws.
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

/// One list-control surface: choices scroll, while its trailing menu stays in reach. Every choice shows its whole name,
/// at every text size and in every language (#9121); a strip that does not fit scrolls, and an edge with more past it
/// fades (`.edgeFade()`).
struct CapsuleFilterBar<Trailing: View>: View {
    var items: [CapsuleFilterItemData]
    var selection: String
    var choose: (String) -> Void
    /// The strip's identifier and its choices' prefix (`<prefix><id>`): the Agents list's by default.
    var scrollID = "agents.filters.scroll"
    var itemIDPrefix = "agents.filter."
    /// The strip scrolls the chosen item into sight whenever the choice changes, by a tap or by the page (a search
    /// showing All, a landing), and when it appears; by the least it can, so the first items stay in sight as far as
    /// they fit, and clear of the edge's fade (the Inbox's section bar, #9053). Off: a tap centres its item, as the
    /// Agents filters do.
    var revealsSelection = false
    /// The hairline and the trailing menu; off while the menu has nothing to offer (the Inbox's grouping under In
    /// Progress and Done, #9053).
    var showsTrailing = true
    @ViewBuilder var trailing: Trailing
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: Theme.Space.flush) {
            ScrollViewReader { proxy in
                ScrollView(.horizontal) {
                    HStack(spacing: Theme.Space.xs) {
                        ForEach(items) { item in
                            CapsuleFilterItem(item: item, selected: item.id == selection, idPrefix: itemIDPrefix) {
                                choose(item.id)
                                // revealing, the choice's change scrolls (below), from the layout it makes
                                guard !revealsSelection else { return }
                                withAnimation(reduceMotion ? nil : Theme.Motion.standard) {
                                    proxy.scrollTo(item.id, anchor: .center)
                                }
                            }
                            .id(item.id)
                            // What a reveal scrolls to: the chip and a fade's width either side, so the edge's fade
                            // never covers the chosen chip.
                            .background {
                                if revealsSelection {
                                    Color.clear.padding(.horizontal, -Theme.Size.fadeEdge).id(Self.revealID(item.id))
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
                .accessibilityIdentifier(scrollID)
                // The trailing edge fades while more lies past it, the leading edge once the strip has left its start.
                .edgeFade()
                .onAppear { if revealsSelection { reveal(selection, proxy, animated: false) } }
                .onChange(of: selection) { _, chosen in
                    if revealsSelection { reveal(chosen, proxy, animated: true) }
                }
            }
            if showsTrailing {
                Rectangle().fill(Theme.cardStroke)
                    .frame(width: Theme.Size.hairline)
                    .scaledFrame(height: Theme.FilterCapsule.separatorHeight)
                    .accessibilityHidden(true)
                trailing
            }
        }
        .background(Theme.card, in: Capsule())
        .overlay(Capsule().strokeBorder(Theme.cardStroke, lineWidth: Theme.Size.hairline))
        .clipShape(Capsule())
    }

    private static func revealID(_ id: String) -> String { "reveal." + id }

    /// Scrolls to `id` once this update is laid out: on the strip's first appearance, or a choice the page made as it
    /// appeared, its items have no frames yet and an immediate scroll does nothing.
    private func reveal(_ id: String, _ proxy: ScrollViewProxy, animated: Bool) {
        Task { @MainActor in
            withAnimation(animated && !reduceMotion ? Theme.Motion.standard : nil) { proxy.scrollTo(Self.revealID(id)) }
        }
    }
}

/// One choice: its dot or mark, its whole name on one line and its count. Its width is the name's, whether it is chosen or not.
struct CapsuleFilterItem: View {
    var item: CapsuleFilterItemData
    var selected: Bool
    var idPrefix = "agents.filter."
    var action: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

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
                Text(item.title)
                    .lineLimit(1)
                    .fixedSize()
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
        .animation(reduceMotion ? nil : Theme.Motion.standard, value: selected)
        .accessibilityIdentifier(idPrefix + item.id)
        .accessibilityLabel(spokenName)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// The title with its count: a badge's only while it shows one, the exact number past 99 too.
    private var spokenName: String {
        guard let count = item.count else { return item.title }
        guard item.countLook != nil else { return "\(item.title) \(count)" }
        return count > 0 ? item.title + String(localized: ", ") + String(count) : item.title
    }
}
#endif
