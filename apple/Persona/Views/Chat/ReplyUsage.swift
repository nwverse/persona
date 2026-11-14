import SwiftUI

extension EnvironmentValues {
    /// What each finished reply says under itself, keyed by that reply's item id. Empty until the page has heard from
    /// the catalog, and on every screen that is not this chat.
    @Entry var replyUsage: [String: ReplyUsageView] = [:]
}

/// The label under one finished reply, and the lines a tap opens (#5470, the web's reply bar).
struct ReplyUsageView: Equatable {
    var label: String
    var lines: [String]
    var actionLabel: String
}

/// The clock, then the cost or the token count. A tap opens the same lines the web's card shows.
struct ReplyUsageFooter: View {
    var item: Item
    @Environment(\.replyUsage) private var usage
    @State private var open = false

    var body: some View {
        if let view = usage[item.id] {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                if let ts = item.ts, ts.isMeaningful {
                    Text(ts.formatted(date: .omitted, time: .shortened))
                        .lineLimit(1)
                }
                Button {
                    open = true
                } label: {
                    Text(view.label)
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                }
                .buttonStyle(.plain)
                .accessibilityHint(view.actionLabel)
            }
            .font(Theme.Font.caption)
            .foregroundStyle(.secondary)
            .padding(.top, Theme.Space.xs)
            .accessibilityIdentifier("chat.reply-usage")
            .sheet(isPresented: $open) {
                ReplyUsageDetails(lines: view.lines)
            }
        }
    }
}

/// The card's lines, one under the other. They may wrap; the label above them does not.
private struct ReplyUsageDetails: View {
    var lines: [String]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                    Text(line)
                        .font(Theme.Font.body)
                        .foregroundStyle(Theme.primaryText)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding(Theme.Space.page)
        }
        .themedGroupedPage()
        .presentationDetents([.medium, .large])
    }
}

/// Asks the catalog once per model id and publishes the labels. The book is created once. Each task and each line
/// change stores the latest lines and the current group's client on `box` (a `View` body cannot assign them).
struct ReplyUsageLoader: ViewModifier {
    var version: Int
    var items: [Item]
    var lines: [ChatLine]
    var persona: Bool
    var hideAnswerID: String?
    var canAsk: Bool
    var catalog: @MainActor ([String]) async throws -> [String: ModelPrice?]
    @State private var map: [String: ReplyUsageView] = [:]
    @State private var box = ReplyUsageBox()

    func body(content: Content) -> some View {
        let stamp = ReplyUsageText.stamp(lines, persona: persona, hide: hideAnswerID, language: ReplyUsageText.language)
        content
            .environment(\.replyUsage, map)
            .task(id: "\(version)#\(canAsk)") {
                guard canAsk else { return }
                prepare()
                let exchanges = chatUsage(items)
                box.exchanges = exchanges
                box.book.onUpdate = { map = ReplyUsageText.views(box) }
                box.book.request(ReplyUsageText.catalogIDs(exchanges))
                do {
                    try await box.book.drain()
                } catch {
                    return
                }
                map = ReplyUsageText.views(box)
            }
            .onChange(of: stamp) { _, _ in
                prepare()
                map = ReplyUsageText.views(box)
            }
    }

    /// The latest inputs, read when a price is asked and when the lines change. Not called from `body`.
    private func prepare() {
        box.catalog = catalog
        box.timings = ReplyUsageText.timings(lines)
        box.persona = persona
        box.hide = hideAnswerID
        box.language = ReplyUsageText.language
    }
}

/// The latest inputs the one price book reads. A trailing closure on `PriceBook` would bind to the clock.
private final class ReplyUsageBox {
    var catalog: @MainActor ([String]) async throws -> [String: ModelPrice?] = { _ in throw CancellationError() }
    var timings: [ReplyTiming] = []
    var persona = false
    var hide: String?
    var language: UsageLanguage = .en
    var exchanges: [ExchangeUsage] = []
    lazy var book: PriceBook = PriceBook(ask: { [weak self] ids in
        guard let self else { throw CancellationError() }
        return try await self.catalog(ids)
    })
}

/// The label and the card, from the same strings the web uses.
enum ReplyUsageText {
    static var language: UsageLanguage {
        switch Locale.current.language.languageCode?.identifier {
        case "zh": .zh
        case "ja": .ja
        default: .en
        }
    }

    static func timings(_ lines: [ChatLine]) -> [ReplyTiming] {
        lines.map { line in
            switch line.content {
            case .row(.user): ReplyTiming(isUser: true)
            case .row(.assistant(let item)): ReplyTiming(replyID: item.id)
            case .answerPart(let item, _, let index, let count) where index == count - 1: ReplyTiming(replyID: item.id)
            case .work(_, _, let live, let start, let end): ReplyTiming(live: live, workStart: start, workEnd: end)
            default: ReplyTiming()
            }
        }
    }

    /// What a line change is, so the labels are drawn again without asking the catalog again.
    static func stamp(_ lines: [ChatLine], persona: Bool, hide: String?, language: UsageLanguage) -> Int {
        var hasher = Hasher()
        hasher.combine(persona)
        hasher.combine(hide)
        hasher.combine(language == .zh ? 1 : language == .ja ? 2 : 0)
        for line in lines {
            switch line.content {
            case .row(let row):
                hasher.combine(row.id)
            case .answerPart(let item, _, let index, let count):
                hasher.combine(item.id)
                hasher.combine(index)
                hasher.combine(count)
            case .work(let id, _, let live, let start, let end):
                hasher.combine(id)
                hasher.combine(live)
                hasher.combine(start?.timeIntervalSince1970)
                hasher.combine(end?.timeIntervalSince1970)
            default:
                break
            }
        }
        return hasher.finalize()
    }

    static func catalogIDs(_ exchanges: [ExchangeUsage]) -> [String] {
        var ids: [String] = []
        var seen: Set<String> = []
        for ex in exchanges {
            for id in summarize(ex.usage).map(catalogIds) ?? [] where seen.insert(id).inserted { ids.append(id) }
        }
        return ids
    }

    fileprivate static func views(_ box: ReplyUsageBox) -> [String: ReplyUsageView] {
        let durations = replyUsageMillis(box.timings)
        var out: [String: ReplyUsageView] = [:]
        for ex in box.exchanges {
            guard let summary = summarize(ex.usage), let answer = ex.answerID, answer != box.hide else { continue }
            let ids = catalogIds(summary)
            if !ids.isEmpty, !box.book.ready(ids) { continue }
            let cost = ids.isEmpty ? nil : turnCost(summary.models) { box.book.priceOf($0) }
            guard let label = replyLabel(persona: box.persona, usage: summary, cost: cost) else { continue }
            let priced = cost.flatMap { $0.total > 0 ? $0 : nil }
            let lines = box.persona && priced != nil ? personaLines(priced!) : agentLines(usageCard(summary, cost: cost, ms: durations[answer], language: box.language))
            let action = box.persona
                ? String(localized: "Estimated cost of this reply")
                : String(localized: "Request details")
            out[answer] = ReplyUsageView(label: labelText(label), lines: lines, actionLabel: action)
        }
        return out
    }

    private static func labelText(_ label: ReplyLabel) -> String {
        switch label.kind {
        case .worth: String(localized: "Worth \(label.amount)")
        case .tokens: String(localized: "\(label.amount) tokens")
        case .approx: String(localized: "≈ \(label.amount)")
        }
    }

    /// The Agent card, in the web's order.
    private static func agentLines(_ card: ReplyUsageCard) -> [String] {
        var lines: [String] = []
        if let cost = card.cost {
            lines.append(String(localized: "Request details"))
            lines.append(String(localized: "Token value this turn: \(cost)"))
        }
        if !card.modelCosts.isEmpty {
            lines.append(String(localized: "By model:"))
            for (name, share) in card.modelCosts { lines.append(String(localized: "· \(name) \(share)")) }
        }
        lines.append(String(localized: "Tokens: \(card.total) total · input \(card.input) · output \(card.output)"))
        if let rate = card.rate {
            lines.append(String(localized: "Cache breakdown: read \(card.cacheRead) · write \(card.cacheWrite) · hit rate \(rate)"))
        } else {
            lines.append(String(localized: "Cache breakdown: read \(card.cacheRead) · write \(card.cacheWrite)"))
        }
        if let duration = card.duration { lines.append(String(localized: "Time \(duration)")) }
        if let name = card.singleModel { lines.append(String(localized: "Model: \(name)")) }
        else if card.modelCount > 1 { lines.append(String(localized: "Model: \(card.modelCount) models")) }
        if card.lowCache {
            lines.append(String(localized: "Suggestion: \(String(localized: "Cache hit rate is low; more context was billed again this turn"))"))
        }
        if card.cost == nil { lines.append(String(localized: "Cost unavailable for this turn; showing usage only")) }
        return lines
    }

    /// A Persona conversation: the cost in plain words, and each model's share when there were several.
    private static func personaLines(_ cost: TurnCost) -> [String] {
        var lines = [String(localized: "About \(formatCost(cost.total)) for this reply, estimated at list prices")]
        if cost.models.count >= 2 {
            lines.append(String(localized: "By model:"))
            for m in cost.models { lines.append(String(localized: "· \(replyModelName(m.model)) \(formatCost(m.cost))")) }
        }
        return lines
    }
}
