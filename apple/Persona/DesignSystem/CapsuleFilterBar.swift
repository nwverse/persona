import SwiftUI

#if os(iOS)
/// One choice of the strip: what its chip draws.
struct CapsuleFilterItemData: Identifiable {
    var id: String
    var title: String
    var count: Int?
    var tone: StatusTone?
    var symbol: String?
}

/// One list-control surface: choices scroll, while its trailing menu stays in reach. Every choice shows its whole name,
/// at every text size and in every language (#9121); a strip that does not fit scrolls, and an edge with more past it
/// fades (`.edgeFade()`).
struct CapsuleFilterBar<Trailing: View>: View {
    var items: [CapsuleFilterItemData]
    var selection: String
    var choose: (String) -> Void
    @ViewBuilder var trailing: Trailing
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: Theme.Space.flush) {
            ScrollViewReader { proxy in
                ScrollView(.horizontal) {
                    HStack(spacing: Theme.Space.xs) {
                        ForEach(items) { item in
                            CapsuleFilterItem(item: item, selected: item.id == selection) {
                                choose(item.id)
                                withAnimation(reduceMotion ? nil : Theme.Motion.standard) {
                                    proxy.scrollTo(item.id, anchor: .center)
                                }
                            }
                            .id(item.id)
                        }
                    }
                    .padding(.horizontal, Theme.Space.xs)
                }
                // A horizontal strip must never inherit the navigation bar's vertical content inset.
                .contentMargins(.vertical, 0, for: .scrollContent)
                .scrollIndicators(.hidden)
                .scaledFrame(height: Theme.Size.tapTarget)
                .frame(minHeight: Theme.Size.tapTarget)
                .accessibilityIdentifier("agents.filters.scroll")
                // The trailing edge fades while more lies past it, the leading edge once the strip has left its start.
                .edgeFade()
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
}

/// One choice: its dot or mark, its whole name on one line and its count. Its width is the name's, whether it is chosen or not.
struct CapsuleFilterItem: View {
    var item: CapsuleFilterItemData
    var selected: Bool
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
                    Text("\(count)").monospacedDigit()
                        .foregroundStyle(selected ? ink : .secondary)
                        .contentTransition(.numericText(value: Double(count)))
                        .fixedSize()
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
        .accessibilityIdentifier("agents.filter." + item.id)
        .accessibilityLabel(item.count.map { "\(item.title) \($0)" } ?? item.title)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}
#endif
