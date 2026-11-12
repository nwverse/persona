import AppIntents
import SwiftUI
import WidgetKit

/// The modules a widget can show (Edit Widget › Modules on the Persona widget; one widget each besides).
enum HomeModule: String, CaseIterable, Codable, Sendable {
    case agents, today, usage, computers, record, health, inbox

    var title: LocalizedStringKey {
        switch self {
        case .agents: "Agents [Home Widgets]"
        case .today: "Today [Home Widgets]"
        case .usage: "Usage [Home Widgets]"
        case .computers: "Computers [Home Widgets]"
        case .record: "Record [Home Widgets]"
        case .health: "Health [Home Widgets]"
        case .inbox: "Inbox [Home Widgets]"
        }
    }

    var symbol: String {
        switch self {
        case .agents: HomeWidgetTokens.agentsSymbol
        case .today: HomeWidgetTokens.todaySymbol
        case .usage: HomeWidgetTokens.usageSymbol
        case .computers: HomeWidgetTokens.machinesSymbol
        case .record: HomeWidgetTokens.recordSymbol
        case .health: HomeWidgetTokens.healthSymbol
        case .inbox: HomeWidgetTokens.inboxSymbol
        }
    }

    /// Inbox messages and health numbers are the user's own: the system blurs them while the phone is locked.
    var isPrivate: Bool { self == .inbox || self == .health }
}

/// How much room a module has, smallest first:
/// - corner: a quarter of the small Persona widget, around the face (one number);
/// - strip: a quarter of the medium one (a number and a line or two);
/// - card: a small widget, or a quarter of the large Persona widget (a number, lines, a footer);
/// - wide: a medium widget, or a quarter of the extra large Persona widget on iPad;
/// - tall: a large widget.
enum HomeDensity: Sendable { case corner, strip, card, wide, tall }

/// What a module draws from: the snapshot as it is at the entry's date, filtered by the widget's settings.
struct HomeContext {
    var snapshot: HomeSnapshot
    var date: Date
    /// Edit Widget › Agents: nil, all of them.
    var agentKind: HomeSnapshot.AgentKind?
    /// Edit Widget › Computer: nil, every computer.
    var machine: String?
    /// Edit Widget › Account (Usage): nil, the fullest.
    var account: String?
    /// A widget of the module's own (true) or a tile of the Persona widget, which leaves out the footers.
    var roomy = true

    var stale: Bool { snapshot.isStale(at: date) }

    var agentRows: [HomeSnapshot.AgentRow] {
        snapshot.agents.rows.filter { (agentKind == nil || $0.kind == agentKind) && (machine == nil || $0.machine == machine) }
    }

    /// The counts of the rows shown; with no filter, also the ones past the cap.
    var agentCounts: HomeSnapshot.Counts {
        var c = HomeSnapshot.Counts()
        for row in agentRows {
            switch row.state {
            case .needsYou: c.needsYou += 1
            case .working: c.working += 1
            case .done: c.done += 1
            }
        }
        if agentKind == nil, machine == nil {
            c.needsYou += snapshot.agents.more.needsYou
            c.working += snapshot.agents.more.working
            c.done += snapshot.agents.more.done
        }
        return c
    }

    var machines: [HomeSnapshot.Machine] {
        machine.map { id in snapshot.machines.filter { $0.id == id } } ?? snapshot.machines
    }

    var accounts: [HomeSnapshot.Account] {
        let all = snapshot.usage?.accounts ?? []
        guard let account else { return all }
        return all.filter { $0.id == account }
    }

    /// The face a module's corner shows: how that module is.
    func mood(_ module: HomeModule) -> HomeMood {
        if stale || !snapshot.paired { return .offline }
        switch module {
        case .agents:
            return HomeMood.of(agentCounts, answering: agentKind != .coding && snapshot.agents.assistantAnswering, stale: false)
        case .today:
            if let next = nextEvent, next.start - date.timeIntervalSince1970 < 15 * 60 { return .working }
            return (snapshot.today?.overdue ?? 0) > 0 ? .needsYou : .idle
        case .usage:
            let top = accounts.first?.windows.map(\.percent).max() ?? 0
            return top >= HomeWidgetTokens.fullAt ? .needsYou : top >= HomeWidgetTokens.warnAt ? .working : .idle
        case .computers:
            let list = machines
            if !list.isEmpty, list.allSatisfy({ !$0.online }) { return .offline }
            if list.contains(where: { $0.needsYou > 0 }) { return .needsYou }
            return list.contains(where: { $0.working > 0 }) ? .working : .idle
        case .record:
            return snapshot.recording != nil ? .working : .idle
        case .health:
            if let steps = snapshot.health?.steps, let avg = snapshot.health?.stepsAverage, avg > 0, steps >= avg { return .done }
            return .idle
        case .inbox:
            return (snapshot.inbox?.needsYou ?? 0) > 0 ? .needsYou : .idle
        }
    }

    var nextEvent: HomeSnapshot.Event? {
        snapshot.today?.events.first { $0.end > date.timeIntervalSince1970 && !$0.allDay }
    }

    var allDayEvents: [HomeSnapshot.Event] { snapshot.today?.events.filter(\.allDay) ?? [] }
}

/// One module at one density. `outer` is the corner of its tile away from the face (the Persona widget), where its
/// content sits; a module widget of its own passes `.topLeading`.
struct HomeModuleView: View {
    var module: HomeModule
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment = .topLeading

    var body: some View {
        Group {
            switch module {
            case .agents: AgentsModule(density: density, context: context, outer: outer)
            case .today: TodayModule(density: density, context: context, outer: outer)
            case .usage: UsageModule(density: density, context: context, outer: outer)
            case .computers: ComputersModule(density: density, context: context, outer: outer)
            case .record: RecordModule(density: density, context: context, outer: outer)
            case .health: HealthModule(density: density, context: context, outer: outer)
            case .inbox: InboxModule(density: density, context: context, outer: outer)
            }
        }
        .multilineTextAlignment(density == .corner ? outer.text : .leading)
        .privacySensitive(module.isPrivate)
    }
}

/// Text alignment for a corner.
extension Alignment {
    var text: TextAlignment { horizontal == .trailing ? .trailing : .leading }
    var column: HorizontalAlignment { horizontal == .trailing ? .trailing : .leading }
}

/// A number, then its words, aligned to the tile's outer side: the corner and strip shape every module shares.
private struct CornerStack: View {
    var symbol: String
    var numeral: String
    /// Under the number where there is room (a module's own small widget); the Persona widget's corners have none.
    var words: String?
    var tone: StatusTone?
    var color: Color?
    var outer: Alignment
    var font: Font = HomeWidgetTokens.numeral

    var body: some View {
        VStack(alignment: outer.column, spacing: 0) {
            Image(systemName: symbol).font(HomeWidgetTokens.label).homeInk(.soft)
            HomeNumeral(text: numeral, font: font, tone: tone, color: color)
            if let words { Text(verbatim: words).font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.soft) }
        }
        .multilineTextAlignment(outer.text)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: outer)
    }
}

/// A strip's number with its module's mark above it, so two tiles that both say "Needs You" are told apart.
private struct MarkedNumeral: View {
    var symbol: String
    var text: String
    var tone: StatusTone?
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Image(systemName: symbol).font(HomeWidgetTokens.label).homeInk(.soft)
            HomeNumeral(text: text, font: HomeWidgetTokens.numeral, tone: tone)
        }
        .fixedSize()
    }
}

/// How long since, at the entry's date (a timeline's later entries say more): "4m", "6h".
func homeAgo(_ since: Double, now: Date) -> String {
    Duration.seconds(max(60, now.timeIntervalSince1970 - since))
        .formatted(.units(allowed: [.days, .hours, .minutes], width: .narrow, maximumUnitCount: 1))
}

/// Short counts: 1.2K, 3.4M.
func homeShort(_ n: Int) -> String {
    n.formatted(.number.notation(.compactName).precision(.significantDigits(2)))
}

// MARK: - Agents

private struct AgentsModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var counts: HomeSnapshot.Counts { context.agentCounts }

    /// The most urgent category with anyone in it: its number, words and color.
    private var lead: (Int, String, StatusTone)? {
        if counts.needsYou > 0 { return (counts.needsYou, StateName.needsYou.text, HomeWidgetTokens.needsYou) }
        if counts.working > 0 { return (counts.working, StateName.working.text, HomeWidgetTokens.working) }
        if counts.done > 0 { return (counts.done, StateName.done.text, HomeWidgetTokens.done) }
        return nil
    }

    var body: some View {
        switch density {
        case .corner:
            CornerStack(symbol: HomeModule.agents.symbol, numeral: "\(lead?.0 ?? 0)", tone: lead?.2, outer: outer)
        case .strip:
            HStack(spacing: HomeWidgetTokens.gap) {
                if outer.horizontal == .trailing { Spacer(minLength: 0) }
                MarkedNumeral(symbol: HomeModule.agents.symbol, text: "\(lead?.0 ?? 0)", tone: lead?.2)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.hair) {
                    Text(verbatim: lead?.1 ?? interfaceText("All Quiet [Home Widgets]")).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                    if let top = context.agentRows.first {
                        Text(verbatim: top.title).font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.soft)
                    }
                }
                if outer.horizontal != .trailing { Spacer(minLength: 0) }
            }
            .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
        case .card:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight) {
                    HomeNumeral(text: "\(lead?.0 ?? 0)", font: HomeWidgetTokens.numeralLarge, tone: lead?.2)
                    Text(verbatim: lead?.1 ?? interfaceText("All Quiet [Home Widgets]")).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                }
                if let top = context.agentRows.first {
                    AgentLine(row: top, context: context, lines: 2)
                } else {
                    Text("Nothing needs you. [Home Widgets]").font(HomeWidgetTokens.line).lineLimit(2).homeInk(.soft)
                }
                Spacer(minLength: 0)
                if context.roomy { others }
            }
        case .wide:
            HStack(alignment: .top, spacing: HomeWidgetTokens.gap * 1.5) {
                countsColumn
                VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                    ForEach(context.agentRows.prefix(3)) { AgentLine(row: $0, context: context, lines: 1) }
                    if context.agentRows.isEmpty {
                        Text("Nothing needs you. [Home Widgets]").font(HomeWidgetTokens.line).homeInk(.soft)
                    }
                    Spacer(minLength: 0)
                }
            }
        case .tall:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                StateBar(counts: counts)
                ForEach(context.agentRows.prefix(5)) { AgentLine(row: $0, context: context, lines: 1, card: true) }
                if context.agentRows.isEmpty {
                    Text("Nothing needs you. [Home Widgets]").font(HomeWidgetTokens.line).homeInk(.soft)
                }
                Spacer(minLength: 0)
            }
        }
    }

    /// The categories after the lead one, as dots and counts ("● 3 working  ● 1 done").
    @ViewBuilder private var others: some View {
        HStack(spacing: HomeWidgetTokens.gap) {
            if counts.needsYou > 0, lead?.2.dot != HomeWidgetTokens.needsYou.dot {
                HomeCount(tone: HomeWidgetTokens.needsYou, text: StateName.count(.needsYou, counts.needsYou))
            }
            if counts.working > 0, counts.needsYou > 0 {
                HomeCount(tone: HomeWidgetTokens.working, text: StateName.count(.working, counts.working))
            }
            if counts.done > 0, counts.needsYou + counts.working > 0 {
                HomeCount(tone: HomeWidgetTokens.done, text: interfaceText("\(counts.done) done [Home Widgets]"))
            }
        }
    }

    private var countsColumn: some View {
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
            countRow(counts.needsYou, StateName.needsYou.text, HomeWidgetTokens.needsYou)
            countRow(counts.working, StateName.working.text, HomeWidgetTokens.working)
            countRow(counts.done, StateName.done.text, HomeWidgetTokens.done)
        }
        .fixedSize()
    }

    private func countRow(_ n: Int, _ words: String, _ tone: StatusTone) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight) {
            HomeNumeral(text: "\(n)", font: HomeWidgetTokens.numeral, tone: n > 0 ? tone : nil)
                .opacity(n > 0 ? 1 : 0.35)
            Text(verbatim: words).font(HomeWidgetTokens.metaStrong).lineLimit(1).homeInk(n > 0 ? .soft : .faint)
        }
    }
}

/// An agent: its state's dot, its title, and what it waits for or does, with how long.
private struct AgentLine: View {
    var row: HomeSnapshot.AgentRow
    var context: HomeContext
    var lines: Int
    /// On its own soft card (the large widget).
    var card = false
    @Environment(\.widgetRenderingMode) private var mode

    private var tone: StatusTone {
        switch row.state {
        case .needsYou: HomeWidgetTokens.needsYou
        case .working: HomeWidgetTokens.working
        case .done: HomeWidgetTokens.done
        }
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.gap - HomeWidgetTokens.hair) {
            HomeDot(tone: tone).alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + HomeWidgetTokens.hair }
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: row.title).font(HomeWidgetTokens.lineStrong).lineLimit(lines).homeInk(.primary)
                HStack(spacing: HomeWidgetTokens.tight) {
                    if let words = row.ask ?? row.detail {
                        Text(verbatim: words).lineLimit(1).homeTone(tone, text: true)
                    }
                    if let since = row.since {
                        Text(verbatim: homeAgo(since, now: context.date)).lineLimit(1).homeInk(.faint).layoutPriority(1)
                    }
                }
                .font(HomeWidgetTokens.meta)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, card ? HomeWidgetTokens.gap : 0)
        .padding(.vertical, card ? HomeWidgetTokens.tight + HomeWidgetTokens.hair : 0)
        .background {
            if card {
                RoundedRectangle(cornerRadius: HomeWidgetTokens.tileRadius - HomeWidgetTokens.tight, style: .continuous)
                    .fill(mode == .fullColor ? HomeWidgetTokens.tile : HomeWidgetTokens.tileMono)
            }
        }
    }
}

/// The large Agents widget's head: one bar split by state, the numbers under it.
private struct StateBar: View {
    var counts: HomeSnapshot.Counts

    var body: some View {
        let total = max(1, counts.total)
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
            GeometryReader { geo in
                HStack(spacing: HomeWidgetTokens.hair) {
                    segment(counts.needsYou, HomeWidgetTokens.needsYou, geo.size.width, total)
                    segment(counts.working, HomeWidgetTokens.working, geo.size.width, total)
                    segment(counts.done, HomeWidgetTokens.done, geo.size.width, total)
                    if counts.total == 0 { Capsule().homeInk(.faint).opacity(0.3) }
                }
            }
            .frame(height: HomeWidgetTokens.bar + HomeWidgetTokens.hair)
            HStack(spacing: HomeWidgetTokens.gap * 1.5) {
                HomeCount(tone: HomeWidgetTokens.needsYou, text: StateName.count(.needsYou, counts.needsYou))
                HomeCount(tone: HomeWidgetTokens.working, text: StateName.count(.working, counts.working))
                HomeCount(tone: HomeWidgetTokens.done, text: interfaceText("\(counts.done) done [Home Widgets]"))
            }
        }
    }

    @ViewBuilder private func segment(_ n: Int, _ tone: StatusTone, _ width: CGFloat, _ total: Int) -> some View {
        if n > 0 { Capsule().frame(width: max(HomeWidgetTokens.bar, width * CGFloat(n) / CGFloat(total))).homeTone(tone) }
    }
}

// MARK: - Today

private struct TodayModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var today: HomeSnapshot.Today { context.snapshot.today ?? .init() }

    var body: some View {
        switch density {
        case .corner:
            if let next = context.nextEvent {
                VStack(alignment: outer.column, spacing: 0) {
                    Image(systemName: HomeModule.today.symbol).font(HomeWidgetTokens.label).homeInk(.soft)
                    Text(Date(timeIntervalSince1970: next.start), format: .dateTime.hour().minute())
                        .font(HomeWidgetTokens.numeralSmall).lineLimit(1).minimumScaleFactor(HomeWidgetTokens.numeralMinScale)
                        .homeTone(HomeWidgetTokens.event)
                }
                .multilineTextAlignment(outer.text)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: outer)
            } else {
                CornerStack(symbol: HomeWidgetTokens.todoSymbol, numeral: "\(today.todoCount)",
                            color: today.overdue > 0 ? HomeWidgetTokens.usageFull : nil, outer: outer)
            }
        case .strip:
            HStack(alignment: .center, spacing: HomeWidgetTokens.gap) {
                if outer.horizontal == .trailing { Spacer(minLength: 0) }
                if let next = context.nextEvent {
                    VStack(alignment: .leading, spacing: 0) {
                        Text(Date(timeIntervalSince1970: next.start), format: .dateTime.hour().minute())
                            .font(HomeWidgetTokens.numeralSmall).lineLimit(1).homeTone(HomeWidgetTokens.event)
                        Text(verbatim: next.title).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                        todoCount
                    }
                } else {
                    DayBlock(date: context.date, compact: true)
                    todoSummary
                }
                if outer.horizontal != .trailing { Spacer(minLength: 0) }
            }
            .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
        case .card:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                DayBlock(date: context.date, compact: false)
                if let next = context.nextEvent { EventLine(event: next, lines: context.roomy ? 2 : 1) }
                ForEach(today.todos.prefix(context.nextEvent == nil ? 3 : context.roomy ? 1 : 0)) { TodoLine(todo: $0) }
                Spacer(minLength: 0)
                todoSummary
            }
        case .wide:
            HStack(alignment: .top, spacing: HomeWidgetTokens.gap * 1.5) {
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                    DayBlock(date: context.date, compact: false)
                    ForEach(upcoming.prefix(2), id: \.self) { EventLine(event: $0, lines: 1) }
                    if upcoming.isEmpty { Text("No more events today [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.faint) }
                    Spacer(minLength: 0)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                    HomeLabel(title: "To-Dos [Home Widgets]", symbol: HomeWidgetTokens.todoSymbol)
                    ForEach(today.todos.prefix(4)) { TodoLine(todo: $0) }
                    Spacer(minLength: 0)
                    if today.todoCount > 4 { more(today.todoCount - 4) }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        case .tall:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                DayBlock(date: context.date, compact: false)
                HStack(alignment: .top, spacing: HomeWidgetTokens.gap * 1.5) {
                    VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                        HomeLabel(title: "Schedule [Home Widgets]", symbol: HomeWidgetTokens.calendarSymbol)
                        ForEach(context.allDayEvents.prefix(1), id: \.self) { EventLine(event: $0, lines: 1) }
                        ForEach(upcoming.prefix(5), id: \.self) { EventLine(event: $0, lines: 2) }
                        if upcoming.isEmpty { Text("No more events today [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.faint) }
                        Spacer(minLength: 0)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    VStack(alignment: .leading, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                        HomeLabel(title: "To-Dos [Home Widgets]", symbol: HomeWidgetTokens.todoSymbol)
                            .padding(.bottom, HomeWidgetTokens.hair)
                        ForEach(today.todos.prefix(8)) { TodoLine(todo: $0) }
                        Spacer(minLength: 0)
                        if today.todoCount > 8 { more(today.todoCount - 8) }
                        todoSummary
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
    }

    private var upcoming: [HomeSnapshot.Event] {
        today.events.filter { !$0.allDay && $0.end > context.date.timeIntervalSince1970 }
    }

    /// "6 to-dos", its mark red when one is overdue (the strip's short form).
    private var todoCount: some View {
        HStack(spacing: HomeWidgetTokens.tight) {
            Image(systemName: HomeWidgetTokens.todoSymbol).font(HomeWidgetTokens.meta)
                .modifier(TodoTone(overdue: today.overdue > 0))
            Text(verbatim: interfaceText("\(today.todoCount) to-dos [Home Widgets]")).font(HomeWidgetTokens.metaStrong).homeInk(.soft)
        }
        .lineLimit(1)
    }

    private struct TodoTone: ViewModifier {
        var overdue: Bool
        func body(content: Content) -> some View {
            if overdue { content.homeTone(HomeWidgetTokens.usageFull) } else { content.homeInk(.soft) }
        }
    }

    @ViewBuilder private var todoSummary: some View {
        if today.todoCount > 0 {
            HStack(spacing: HomeWidgetTokens.tight) {
                Image(systemName: HomeWidgetTokens.todoSymbol).font(HomeWidgetTokens.meta).homeInk(.soft)
                Text(verbatim: interfaceText("\(today.todoCount) to-dos [Home Widgets]")).font(HomeWidgetTokens.metaStrong).homeInk(.soft)
                if today.overdue > 0 {
                    Text(verbatim: interfaceText("\(today.overdue) overdue [Home Widgets]")).font(HomeWidgetTokens.metaStrong).homeTone(HomeWidgetTokens.usageFull)
                }
            }
            .lineLimit(1)
        } else {
            Text("No to-dos today [Home Widgets]").font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.faint)
        }
    }

    private func more(_ n: Int) -> some View {
        Text(verbatim: interfaceText("\(n) more [Home Widgets]")).font(HomeWidgetTokens.meta).homeInk(.faint)
    }
}

/// The weekday over the day of the month, big: a page of a calendar.
private struct DayBlock: View {
    var date: Date
    var compact: Bool

    var body: some View {
        if compact {
            VStack(alignment: .leading, spacing: 0) {
                Text(date, format: .dateTime.weekday(.abbreviated)).font(HomeWidgetTokens.label).textCase(.uppercase).homeTone(HomeWidgetTokens.event)
                Text(date, format: .dateTime.day()).font(HomeWidgetTokens.day).lineLimit(1).homeInk(.primary)
            }
            .fixedSize()
        } else {
            HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                Text(date, format: .dateTime.day()).font(HomeWidgetTokens.day).lineLimit(1).homeInk(.primary)
                VStack(alignment: .leading, spacing: 0) {
                    Text(date, format: .dateTime.weekday(.wide)).font(HomeWidgetTokens.label).textCase(.uppercase).homeTone(HomeWidgetTokens.event)
                    Text(date, format: .dateTime.month(.wide)).font(HomeWidgetTokens.meta).homeInk(.soft)
                }
                .lineLimit(1)
            }
        }
    }
}

/// An event: a bar in the event color, its time and its title.
private struct EventLine: View {
    var event: HomeSnapshot.Event
    var lines: Int
    /// Start and end on their own column (the large widget's schedule).
    var times = false

    var body: some View {
        HStack(alignment: .top, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
            Capsule().frame(width: HomeWidgetTokens.barThin - 1).homeTone(HomeWidgetTokens.event)
            VStack(alignment: .leading, spacing: 0) {
                if event.allDay {
                    Text("All Day [Home Widgets]").font(HomeWidgetTokens.metaStrong).homeTone(HomeWidgetTokens.event)
                } else {
                    Text(Date(timeIntervalSince1970: event.start)..<Date(timeIntervalSince1970: max(event.start, event.end)),
                         format: .interval.hour().minute())
                        .font(HomeWidgetTokens.metaStrong).homeTone(HomeWidgetTokens.event)
                }
                Text(verbatim: event.title).font(HomeWidgetTokens.lineStrong).lineLimit(lines).homeInk(.primary)
            }
        }
        .fixedSize(horizontal: false, vertical: true)
    }
}

/// A to-do: its open circle (red when overdue) and its title.
private struct TodoLine: View {
    var todo: HomeSnapshot.Todo

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
            Image(systemName: "circle").font(HomeWidgetTokens.meta) // icon: an open to-do's box, as the To-dos page draws it
                .modifier(TodoMark(overdue: todo.overdue))
            Text(verbatim: todo.title).font(HomeWidgetTokens.line).lineLimit(1).homeInk(.primary)
        }
    }

    private struct TodoMark: ViewModifier {
        var overdue: Bool
        func body(content: Content) -> some View {
            if overdue { content.homeTone(HomeWidgetTokens.usageFull) } else { content.homeInk(.soft) }
        }
    }
}

// MARK: - Usage

private struct UsageModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var usage: HomeSnapshot.Usage { context.snapshot.usage ?? .init() }
    private var top: HomeSnapshot.Account? { context.accounts.first }

    var body: some View {
        switch density {
        case .corner:
            VStack(alignment: outer.column, spacing: HomeWidgetTokens.hair) {
                if let w = top?.windows.max(by: { $0.percent < $1.percent }) {
                    ZStack {
                        HomeRing(fraction: w.percent / 100, color: Self.color(w.percent), width: HomeWidgetTokens.ringThin)
                        Text(verbatim: "\(Int(w.percent))").font(HomeWidgetTokens.numeralSmall).minimumScaleFactor(HomeWidgetTokens.numeralMinScale).homeInk(.primary)
                    }
                    .frame(width: HomeWidgetTokens.faceBadge + HomeWidgetTokens.gap * 1.5, height: HomeWidgetTokens.faceBadge + HomeWidgetTokens.gap * 1.5)
                    Text(verbatim: w.label).font(HomeWidgetTokens.meta).homeInk(.soft)
                } else {
                    CornerStack(symbol: HomeModule.usage.symbol, numeral: homeShort(usage.tokensToday), outer: outer)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: outer)
        case .strip:
            HStack(spacing: HomeWidgetTokens.gap) {
                if outer.horizontal == .trailing { Spacer(minLength: 0) }
                if let top { Rings(account: top).frame(width: HomeWidgetTokens.faceMedium * 0.62, height: HomeWidgetTokens.faceMedium * 0.62) }
                VStack(alignment: .leading, spacing: HomeWidgetTokens.hair) {
                    Text(verbatim: top?.name ?? interfaceText("Tokens Today [Home Widgets]")).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                    if let top { WindowWords(account: top) } else {
                        Text(verbatim: homeShort(usage.tokensToday)).font(HomeWidgetTokens.numeralSmall).homeInk(.primary)
                    }
                }
                if outer.horizontal != .trailing { Spacer(minLength: 0) }
            }
            .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
        case .card:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                if let top, let fullest = top.windows.max(by: { $0.percent < $1.percent }) {
                    HStack(alignment: .center, spacing: HomeWidgetTokens.gap) {
                        Rings(account: top, labeled: false).frame(width: HomeWidgetTokens.faceMedium * 0.72, height: HomeWidgetTokens.faceMedium * 0.72)
                        VStack(alignment: .leading, spacing: 0) {
                            HomeNumeral(text: "\(Int(fullest.percent))%", font: HomeWidgetTokens.numeral, color: Self.color(fullest.percent))
                            Text(verbatim: fullest.label).font(HomeWidgetTokens.metaStrong).homeInk(.soft)
                        }
                    }
                    Text(verbatim: top.name).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                    WindowWords(account: top)
                } else {
                    HomeNumeral(text: homeShort(usage.tokensToday), font: HomeWidgetTokens.numeralLarge)
                }
                Spacer(minLength: 0)
                if context.roomy { tokens }
            }
        case .wide:
            HStack(alignment: .top, spacing: HomeWidgetTokens.gap * 1.5) {
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                    HomeNumeral(text: homeShort(usage.tokensToday), font: HomeWidgetTokens.numeralLarge)
                    Text("Tokens Today [Home Widgets]").font(HomeWidgetTokens.metaStrong).lineLimit(1).homeInk(.soft)
                    Spacer(minLength: 0)
                    if let cost = usage.costToday {
                        Text(cost, format: .currency(code: usage.currency)).font(HomeWidgetTokens.numeralSmall).homeInk(.primary)
                        Text("Spent Today [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.soft)
                    }
                }
                .fixedSize(horizontal: true, vertical: false)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                    ForEach(context.accounts.prefix(3)) { AccountBars(account: $0, now: context.date) }
                    if context.accounts.isEmpty { Text("No subscription windows yet [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.faint) }
                    Spacer(minLength: 0)
                }
            }
        case .tall:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 0) {
                        HomeNumeral(text: homeShort(usage.tokensToday), font: HomeWidgetTokens.numeralHero)
                        Text("Tokens Today [Home Widgets]").font(HomeWidgetTokens.metaStrong).homeInk(.soft)
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 0) {
                        HomeNumeral(text: homeShort(usage.tpm), font: HomeWidgetTokens.numeral)
                        Text("Tokens a Minute [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.soft)
                        if let cost = usage.costToday {
                            Text(cost, format: .currency(code: usage.currency)).font(HomeWidgetTokens.lineStrong).homeInk(.primary)
                        }
                    }
                }
                if !usage.hourly.isEmpty { HourBars(hours: usage.hourly, now: context.date) }
                ForEach(context.accounts.prefix(3)) { AccountBars(account: $0, now: context.date) }
                Spacer(minLength: 0)
            }
        }
    }

    @ViewBuilder private var tokens: some View {
        HStack(spacing: HomeWidgetTokens.tight) {
            Text(verbatim: homeShort(usage.tokensToday)).font(HomeWidgetTokens.metaStrong).homeInk(.primary)
            Text("Tokens Today [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.soft)
            if let cost = usage.costToday {
                Text(cost, format: .currency(code: usage.currency)).font(HomeWidgetTokens.metaStrong).homeInk(.primary)
            }
        }
        .lineLimit(1)
    }

    static func color(_ percent: Double) -> Color {
        percent >= HomeWidgetTokens.fullAt ? HomeWidgetTokens.usageFull : percent >= HomeWidgetTokens.warnAt ? HomeWidgetTokens.usageWarn : HomeWidgetTokens.usageCalm
    }

    /// An account's windows as rings inside each other, the shortest inside, the fullest one's percent in the middle.
    struct Rings: View {
        var account: HomeSnapshot.Account
        /// The fullest window's percent in the middle.
        var labeled = true

        var body: some View {
            GeometryReader { geo in
                let side = min(geo.size.width, geo.size.height)
                let windows = account.windows.sorted { $0.minutes > $1.minutes }.prefix(2)
                let w = max(HomeWidgetTokens.ringThin, side * 0.11)
                ZStack {
                    ForEach(Array(windows.enumerated()), id: \.offset) { i, window in
                        HomeRing(fraction: window.percent / 100, color: UsageModule.color(window.percent), width: w)
                            .padding(CGFloat(i) * (w + HomeWidgetTokens.hair))
                    }
                    if labeled, let fullest = account.windows.max(by: { $0.percent < $1.percent }) {
                        VStack(spacing: 0) {
                            Text(verbatim: "\(Int(fullest.percent))%").font(HomeWidgetTokens.numeralSmall).minimumScaleFactor(HomeWidgetTokens.numeralMinScale).homeInk(.primary)
                            Text(verbatim: fullest.label).font(HomeWidgetTokens.meta).homeInk(.soft)
                        }
                        .padding(w * 2)
                    }
                }
                .frame(width: side, height: side)
            }
            .opacity(account.stale ? 0.5 : 1)
        }
    }
}

/// Today's tokens by the hour, midnight to now, the current hour lit.
private struct HourBars: View {
    var hours: [Int]
    var now: Date

    var body: some View {
        let peak = max(1, hours.max() ?? 1)
        let current = Calendar.current.component(.hour, from: now)
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
            HStack(alignment: .bottom, spacing: HomeWidgetTokens.hair) {
                ForEach(0..<24, id: \.self) { h in
                    let n = h < hours.count ? hours[h] : 0
                    Capsule()
                        .frame(maxWidth: .infinity)
                        .frame(height: max(HomeWidgetTokens.hair, HomeWidgetTokens.faceLarge * 0.42 * CGFloat(n) / CGFloat(peak)))
                        .modifier(HourTone(lit: h == current, future: h > current))
                }
            }
            .frame(height: HomeWidgetTokens.faceLarge * 0.42, alignment: .bottom)
            HStack {
                Text(verbatim: "0").font(HomeWidgetTokens.meta).homeInk(.faint)
                Spacer()
                Text(verbatim: "12").font(HomeWidgetTokens.meta).homeInk(.faint)
                Spacer()
                Text(verbatim: "24").font(HomeWidgetTokens.meta).homeInk(.faint)
            }
        }
    }

    private struct HourTone: ViewModifier {
        var lit: Bool
        var future: Bool
        func body(content: Content) -> some View {
            if lit { content.homeTone(HomeWidgetTokens.working) } else { content.homeInk(future ? .faint : .soft).opacity(future ? 0.3 : 0.6) }
        }
    }
}

/// "5h 42% · 7d 18%".
private struct WindowWords: View {
    var account: HomeSnapshot.Account
    var body: some View {
        Text(verbatim: account.windows.sorted { $0.minutes < $1.minutes }.map { "\($0.label) \(Int($0.percent))%" }.joined(separator: " · "))
            .font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.soft)
    }
}

/// An account with a bar a window and when the fullest resets.
private struct AccountBars: View {
    var account: HomeSnapshot.Account
    var now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight - 1) {
            HStack(spacing: HomeWidgetTokens.tight) {
                Text(verbatim: account.name).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                Spacer(minLength: HomeWidgetTokens.tight)
                if account.stale {
                    Text("Out of Date [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.faint)
                } else if let reset = account.windows.max(by: { $0.percent < $1.percent })?.resetsAt {
                    Text("Resets \(Date(timeIntervalSince1970: reset), format: .dateTime.hour().minute()) [Home Widgets]")
                        .font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.faint)
                }
            }
            ForEach(account.windows.sorted { $0.minutes < $1.minutes }, id: \.label) { window in
                HStack(spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                    Text(verbatim: window.label).font(HomeWidgetTokens.metaStrong).frame(width: HomeWidgetTokens.faceBadge * 0.8, alignment: .leading).homeInk(.soft)
                    HomeBar(fraction: window.percent / 100, color: UsageModule.color(window.percent), height: HomeWidgetTokens.barThin)
                    Text(verbatim: "\(Int(window.percent))%").font(HomeWidgetTokens.metaStrong).monospacedDigit()
                        .frame(width: HomeWidgetTokens.faceBadge * 1.1, alignment: .trailing).homeInk(.primary)
                }
            }
        }
        .opacity(account.stale ? 0.55 : 1)
    }
}

// MARK: - Computers

private struct ComputersModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var list: [HomeSnapshot.Machine] { context.machines }
    private var online: Int { list.filter(\.online).count }

    var body: some View {
        switch density {
        case .corner:
            CornerStack(symbol: HomeModule.computers.symbol, numeral: "\(online)/\(list.count)",
                        color: online == 0 && !list.isEmpty ? HomeWidgetTokens.inkFaint : nil, outer: outer)
        case .strip:
            HStack(spacing: HomeWidgetTokens.gap) {
                if outer.horizontal == .trailing { Spacer(minLength: 0) }
                HomeNumeral(text: "\(online)/\(list.count)", font: HomeWidgetTokens.numeralLarge)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                    Text("Online [Home Widgets]").font(HomeWidgetTokens.lineStrong).homeInk(.primary)
                    Pills(list: list)
                }
                if outer.horizontal != .trailing { Spacer(minLength: 0) }
            }
            .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
        case .card:
            if list.count == 1, let one = list.first {
                OneComputer(machine: one)
            } else {
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
                    HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight) {
                        HomeNumeral(text: "\(online)/\(list.count)", font: HomeWidgetTokens.numeralLarge)
                        Text("Online [Home Widgets]").font(HomeWidgetTokens.lineStrong).homeInk(.primary)
                    }
                    ForEach(list.prefix(context.roomy ? 3 : 2)) { MachineLine(machine: $0) }
                    Spacer(minLength: 0)
                }
            }
        case .wide, .tall:
            HStack(alignment: .bottom, spacing: HomeWidgetTokens.gap) {
                ForEach(list.prefix(4)) { MachineColumn(machine: $0) }
                if list.isEmpty { Text("No computers yet [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.faint) }
            }
        }
    }
}

/// What a computer is doing, in words: needs you, working, idle or offline.
private func machineWords(_ m: HomeSnapshot.Machine) -> (String, StatusTone?) {
    if !m.online { return (interfaceText("Offline [Home Widgets]"), nil) }
    if m.needsYou > 0 { return (StateName.count(.needsYou, m.needsYou), HomeWidgetTokens.needsYou) }
    if m.working > 0 { return (StateName.count(.working, m.working), HomeWidgetTokens.working) }
    return (interfaceText("Idle [Home Widgets]"), nil)
}

/// A row of capsules, one a computer: filled when online, its load as the fill's height.
private struct Pills: View {
    var list: [HomeSnapshot.Machine]
    var body: some View {
        HStack(spacing: HomeWidgetTokens.tight) {
            ForEach(list.prefix(6)) { m in
                Capsule()
                    .frame(width: HomeWidgetTokens.dot * 2, height: HomeWidgetTokens.dot + HomeWidgetTokens.tight)
                    .modifier(PillTone(machine: m))
            }
        }
    }

    private struct PillTone: ViewModifier {
        var machine: HomeSnapshot.Machine
        func body(content: Content) -> some View {
            let words = machineWords(machine)
            if let tone = words.1 { content.homeTone(tone) } else { content.homeInk(machine.online ? .soft : .faint).opacity(machine.online ? 1 : 0.4) }
        }
    }
}

private struct MachineLine: View {
    var machine: HomeSnapshot.Machine
    var body: some View {
        let words = machineWords(machine)
        HStack(spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
            Circle().frame(width: HomeWidgetTokens.dot, height: HomeWidgetTokens.dot).modifier(OnlineTone(machine: machine, tone: words.1))
            Text(verbatim: machine.name).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(machine.online ? .primary : .faint)
            Spacer(minLength: HomeWidgetTokens.tight)
            if let tone = words.1 {
                Text(verbatim: "\(machine.needsYou > 0 ? machine.needsYou : machine.working)").font(HomeWidgetTokens.numeralSmall).homeTone(tone, text: true)
            }
        }
    }

    private struct OnlineTone: ViewModifier {
        var machine: HomeSnapshot.Machine
        var tone: StatusTone?
        func body(content: Content) -> some View {
            if let tone { content.homeTone(tone) } else { content.homeInk(machine.online ? .soft : .faint).opacity(machine.online ? 1 : 0.4) }
        }
    }
}

/// A computer as a column: its load as a bar from the bottom, its name and what it is doing.
private struct MachineColumn: View {
    var machine: HomeSnapshot.Machine
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        let words = machineWords(machine)
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
            GeometryReader { geo in
                ZStack(alignment: .bottom) {
                    RoundedRectangle(cornerRadius: HomeWidgetTokens.chipRadius, style: .continuous)
                        .fill(mode == .fullColor ? HomeWidgetTokens.track : HomeWidgetTokens.trackMono)
                    if machine.online {
                        RoundedRectangle(cornerRadius: HomeWidgetTokens.chipRadius, style: .continuous)
                            .frame(height: max(HomeWidgetTokens.bar, geo.size.height * CGFloat(machine.load ?? 0) / 100))
                            .modifier(LoadTone(words: words))
                    }
                }
            }
            Text(verbatim: machine.name).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(machine.online ? .primary : .faint)
            Group {
                if let tone = words.1 { Text(verbatim: words.0).homeTone(tone, text: true) } else { Text(verbatim: words.0).homeInk(.faint) }
            }
            .font(HomeWidgetTokens.meta).lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private struct LoadTone: ViewModifier {
        var words: (String, StatusTone?)
        func body(content: Content) -> some View {
            if let tone = words.1 { content.homeTone(tone) } else { content.homeTone(HomeWidgetTokens.usageCalm) }
        }
    }
}

/// One computer, chosen in Edit Widget: its name, a load ring and its agents.
private struct OneComputer: View {
    var machine: HomeSnapshot.Machine
    var body: some View {
        let words = machineWords(machine)
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
            ZStack {
                HomeRing(fraction: Double(machine.load ?? 0) / 100, color: words.1?.dot ?? HomeWidgetTokens.usageCalm)
                VStack(spacing: 0) {
                    Text(verbatim: "\(machine.load ?? 0)%").font(HomeWidgetTokens.numeralSmall).homeInk(.primary)
                    Text("Load [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.soft)
                }
            }
            .frame(width: HomeWidgetTokens.faceLarge * 0.6, height: HomeWidgetTokens.faceLarge * 0.6)
            .opacity(machine.online ? 1 : 0.4)
            Spacer(minLength: 0)
            Text(verbatim: machine.name).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
            if let tone = words.1 { Text(verbatim: words.0).font(HomeWidgetTokens.meta).homeTone(tone, text: true) } else { Text(verbatim: words.0).font(HomeWidgetTokens.meta).homeInk(.faint) }
        }
    }
}

// MARK: - Record

private struct RecordModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var recording: HomeSnapshot.Recording? { context.snapshot.recording }

    var body: some View {
        switch density {
        case .corner:
            button {
                VStack(alignment: outer.column, spacing: HomeWidgetTokens.tight) {
                    RecordDisc(side: HomeWidgetTokens.faceBadge + HomeWidgetTokens.gap, recording: recording != nil)
                    if let recording {
                        Text(timerInterval: Date(timeIntervalSince1970: recording.started)...Date.distantFuture, countsDown: false)
                            .font(HomeWidgetTokens.metaStrong).monospacedDigit().homeTone(HomeWidgetTokens.recording)
                    } else {
                        Text("Record [Home Widgets]").font(HomeWidgetTokens.metaStrong).lineLimit(1).homeInk(.soft)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: outer)
            }
        case .strip:
            button {
                HStack(spacing: HomeWidgetTokens.gap) {
                    if outer.horizontal == .trailing { Spacer(minLength: 0) }
                    RecordDisc(side: HomeWidgetTokens.faceBadge * 1.4, recording: recording != nil)
                    shortWords
                    if outer.horizontal != .trailing { Spacer(minLength: 0) }
                }
                .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
            }
        case .card:
            button {
                VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                    RecordDisc(side: HomeWidgetTokens.recordSide * 0.85, recording: recording != nil)
                    Spacer(minLength: 0)
                    words(lines: 2)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
        case .wide, .tall:
            HStack(spacing: HomeWidgetTokens.gap * 2) {
                button { RecordDisc(side: HomeWidgetTokens.recordSide * 1.25, recording: recording != nil) }
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                    words(lines: 2)
                    if recording == nil {
                        Text("Audio stays on this iPhone; the transcript goes to Knowledge. [Home Widgets]")
                            .font(HomeWidgetTokens.meta).lineLimit(2).homeInk(.faint)
                    }
                }
                Spacer(minLength: 0)
            }
        }
    }

    /// Start (the Record control's intent, which runs in the app and starts the recording's Live Activity) or Stop.
    @ViewBuilder private func button<Label: View>(@ViewBuilder label: () -> Label) -> some View {
        if let recording {
            Button(intent: StopRecordingIntent(recordingID: recording.id), label: label).buttonStyle(.plain)
        } else {
            Button(intent: StartMeetingRecordingIntent(), label: label).buttonStyle(.plain)
        }
    }

    /// The strip's two short lines: Record and the event it would be named after, or the running time.
    @ViewBuilder private var shortWords: some View {
        VStack(alignment: .leading, spacing: HomeWidgetTokens.hair) {
            if let recording {
                Text(timerInterval: Date(timeIntervalSince1970: recording.started)...Date.distantFuture, countsDown: false)
                    .font(HomeWidgetTokens.numeralSmall).monospacedDigit().homeTone(HomeWidgetTokens.recording)
                Text(verbatim: recording.title).font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.soft)
            } else {
                Text("Record [Home Widgets]").font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                if let now = eventNow { Text(verbatim: now.title).font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.soft) }
            }
        }
    }

    /// The event a recording started now would be named after (MeetingCalendar's: the one under way, or about to be).
    private var eventNow: HomeSnapshot.Event? {
        let t = context.date.timeIntervalSince1970
        return context.snapshot.today?.events.first { !$0.allDay && $0.start <= t + 5 * 60 && $0.end > t }
    }

    @ViewBuilder private func words(lines: Int) -> some View {
        VStack(alignment: .leading, spacing: HomeWidgetTokens.hair) {
            if let recording {
                Text(timerInterval: Date(timeIntervalSince1970: recording.started)...Date.distantFuture, countsDown: false)
                    .font(HomeWidgetTokens.numeral).monospacedDigit().homeTone(HomeWidgetTokens.recording)
                Text(verbatim: recording.title).font(HomeWidgetTokens.lineStrong).lineLimit(lines).homeInk(.primary)
                Text("Tap to stop [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.soft)
            } else {
                Text("Record a Meeting [Home Widgets]").font(HomeWidgetTokens.lineStrong).lineLimit(lines).homeInk(.primary)
                if let now = eventNow {
                    Text(verbatim: interfaceText("Named “\(now.title)” [Home Widgets]")).font(HomeWidgetTokens.meta).lineLimit(lines).homeInk(.soft)
                } else {
                    Text("Named after the event on your calendar now [Home Widgets]").font(HomeWidgetTokens.meta).lineLimit(lines).homeInk(.soft)
                }
            }
        }
    }
}

/// The Record button: the system's recording red with the waveform; a stop square while recording.
private struct RecordDisc: View {
    var side: CGFloat
    var recording: Bool
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        let full = mode == .fullColor
        ZStack {
            Circle().fill(full ? HomeWidgetTokens.recordButton : .white)
            if recording { Circle().strokeBorder(full ? HomeWidgetTokens.recording.opacity(0.35) : .white.opacity(0.4), lineWidth: side * 0.08).padding(-side * 0.1) }
            Image(systemName: recording ? HomeWidgetTokens.stopSymbol : HomeWidgetTokens.recordSymbol)
                .font(.system(size: side * 0.42, weight: .bold))
                .foregroundStyle(full ? Color.white : Color.black)
                .blendMode(full ? .normal : .destinationOut)
        }
        .compositingGroup()
        .frame(width: side, height: side)
        .homeAccent()
        .accessibilityLabel(Text(recording ? "Stop Recording [Home Widgets]" : "Record a Meeting [Home Widgets]"))
    }
}

// MARK: - Health

private struct HealthModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var health: HomeSnapshot.Health { context.snapshot.health ?? .init() }

    var body: some View {
        switch density {
        case .corner:
            CornerStack(symbol: HomeWidgetTokens.stepsSymbol, numeral: health.steps.map(homeShort) ?? "–",
                        color: HomeWidgetTokens.steps, outer: outer)
        case .strip:
            HStack(spacing: HomeWidgetTokens.gap) {
                if outer.horizontal == .trailing { Spacer(minLength: 0) }
                VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                    steps(font: HomeWidgetTokens.numeral)
                    sleep
                }
                .frame(maxWidth: HomeWidgetTokens.faceLarge * 1.2)
                if outer.horizontal != .trailing { Spacer(minLength: 0) }
            }
            .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
        case .card:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                steps(font: HomeWidgetTokens.numeralLarge)
                Spacer(minLength: 0)
                sleep
                if context.roomy { heart }
            }
        case .wide, .tall:
            HStack(alignment: .top, spacing: HomeWidgetTokens.gap * 2) {
                steps(font: HomeWidgetTokens.numeralLarge).frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) { sleep; heart; energy }
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private func steps(font: Font) -> some View {
        VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
            HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight) {
                HomeNumeral(text: health.steps.map { $0.formatted() } ?? "–", font: font, color: HomeWidgetTokens.steps)
                Text("Steps [Home Widgets]").font(HomeWidgetTokens.metaStrong).homeInk(.soft)
            }
            if let steps = health.steps, let avg = health.stepsAverage, avg > 0 {
                let goal = Double(max(steps, avg)) * 1.15
                HomeBar(fraction: Double(steps) / goal, color: HomeWidgetTokens.steps, mark: Double(avg) / goal)
                Text(verbatim: interfaceText("7-day average \(avg.formatted()) [Home Widgets]")).font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.faint)
            }
        }
    }

    @ViewBuilder private var sleep: some View {
        if let hours = health.sleepHours {
            measure(HomeWidgetTokens.sleepSymbol, Duration.seconds(hours * 3600).formatted(.units(allowed: [.hours, .minutes], width: .narrow)),
                    interfaceText("Asleep [Home Widgets]"), HomeWidgetTokens.sleep)
        }
    }

    @ViewBuilder private var heart: some View {
        if let bpm = health.restingHeartRate {
            measure(HomeWidgetTokens.healthSymbol, interfaceText("\(bpm) bpm [Home Widgets]"), interfaceText("Resting [Home Widgets]"), HomeWidgetTokens.heart)
        }
    }

    @ViewBuilder private var energy: some View {
        if let kcal = health.activeEnergy {
            measure(HomeWidgetTokens.energySymbol, interfaceText("\(kcal) kcal [Home Widgets]"), interfaceText("Active [Home Widgets]"), HomeWidgetTokens.steps)
        }
    }

    private func measure(_ symbol: String, _ value: String, _ words: String, _ color: Color) -> some View {
        HStack(spacing: HomeWidgetTokens.tight + HomeWidgetTokens.hair) {
            Image(systemName: symbol).font(HomeWidgetTokens.meta).homeTone(color)
            Text(verbatim: value).font(HomeWidgetTokens.lineStrong).homeInk(.primary)
            Text(verbatim: words).font(HomeWidgetTokens.meta).homeInk(.soft)
        }
        .lineLimit(1)
    }
}

// MARK: - Inbox

private struct InboxModule: View {
    var density: HomeDensity
    var context: HomeContext
    var outer: Alignment

    private var inbox: HomeSnapshot.Inbox { context.snapshot.inbox ?? .init() }

    var body: some View {
        switch density {
        case .corner:
            CornerStack(symbol: HomeModule.inbox.symbol, numeral: "\(inbox.needsYou)",
                        tone: inbox.needsYou > 0 ? HomeWidgetTokens.needsYou : nil, outer: outer)
        case .strip:
            HStack(spacing: HomeWidgetTokens.gap) {
                if outer.horizontal == .trailing { Spacer(minLength: 0) }
                MarkedNumeral(symbol: HomeModule.inbox.symbol, text: "\(inbox.needsYou)", tone: inbox.needsYou > 0 ? HomeWidgetTokens.needsYou : nil)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.hair) {
                    Text(verbatim: StateName.needsYou.text).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                    if let m = inbox.latest.first {
                        Text(senderAndSummary(m)).font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.soft)
                    }
                }
                if outer.horizontal != .trailing { Spacer(minLength: 0) }
            }
            .frame(maxHeight: .infinity, alignment: Alignment(horizontal: .center, vertical: outer.vertical))
        case .card:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight) {
                    HomeNumeral(text: "\(inbox.needsYou)", font: HomeWidgetTokens.numeralLarge, tone: inbox.needsYou > 0 ? HomeWidgetTokens.needsYou : nil)
                    Text(verbatim: StateName.needsYou.text).font(HomeWidgetTokens.lineStrong).homeInk(.primary)
                }
                if let m = inbox.latest.first { MessageLine(message: m, lines: 2) }
                Spacer(minLength: 0)
                if inbox.unread > 0, context.roomy {
                    Text(verbatim: StateName.count(.unread, inbox.unread)).font(HomeWidgetTokens.metaStrong).homeInk(.soft)
                }
            }
        case .wide:
            HStack(alignment: .top, spacing: HomeWidgetTokens.gap * 1.5) {
                VStack(alignment: .leading, spacing: 0) {
                    HomeNumeral(text: "\(inbox.needsYou)", font: HomeWidgetTokens.numeralHero, tone: inbox.needsYou > 0 ? HomeWidgetTokens.needsYou : nil)
                    Text(verbatim: StateName.needsYou.text).font(HomeWidgetTokens.metaStrong).homeInk(.soft)
                    Spacer(minLength: 0)
                    if inbox.unread > 0 { Text(verbatim: StateName.count(.unread, inbox.unread)).font(HomeWidgetTokens.meta).homeInk(.faint) }
                }
                .fixedSize(horizontal: true, vertical: false)
                VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                    ForEach(inbox.latest.prefix(3)) { MessageLine(message: $0, lines: 1, monogram: true) }
                    if inbox.latest.isEmpty { Text("Nothing new [Home Widgets]").font(HomeWidgetTokens.meta).homeInk(.faint) }
                    Spacer(minLength: 0)
                }
            }
        case .tall:
            VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
                HStack(alignment: .firstTextBaseline, spacing: HomeWidgetTokens.tight) {
                    HomeNumeral(text: "\(inbox.needsYou)", font: HomeWidgetTokens.numeralLarge, tone: inbox.needsYou > 0 ? HomeWidgetTokens.needsYou : nil)
                    Text(verbatim: StateName.needsYou.text).font(HomeWidgetTokens.lineStrong).homeInk(.primary)
                    Spacer()
                    if inbox.unread > 0 { Text(verbatim: StateName.count(.unread, inbox.unread)).font(HomeWidgetTokens.meta).homeInk(.faint) }
                }
                ForEach(inbox.latest.prefix(5)) { MessageLine(message: $0, lines: 2, monogram: true) }
                Spacer(minLength: 0)
            }
        }
    }
}

/// "**Alex** Can you confirm…": the sender in bold, then the summary, on one line.
private func senderAndSummary(_ m: HomeSnapshot.Message) -> AttributedString {
    var who = AttributedString(m.sender)
    who.inlinePresentationIntent = .stronglyEmphasized
    return who + AttributedString(" " + m.summary)
}

/// A message: who (a monogram where there is room), what it says, when.
private struct MessageLine: View {
    var message: HomeSnapshot.Message
    var lines: Int
    var monogram = false
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        HStack(alignment: .top, spacing: HomeWidgetTokens.gap - HomeWidgetTokens.hair) {
            if monogram {
                Text(verbatim: String(message.sender.prefix(1)))
                    .font(HomeWidgetTokens.metaStrong)
                    .frame(width: HomeWidgetTokens.faceBadge * 0.85, height: HomeWidgetTokens.faceBadge * 0.85)
                    .background(Circle().fill(mode == .fullColor ? HomeWidgetTokens.track : HomeWidgetTokens.trackMono))
                    .homeInk(.primary)
            }
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: HomeWidgetTokens.tight) {
                    if message.needsYou { HomeDot(tone: HomeWidgetTokens.needsYou) }
                    Text(verbatim: message.sender).font(HomeWidgetTokens.lineStrong).lineLimit(1).homeInk(.primary)
                    Spacer(minLength: HomeWidgetTokens.tight)
                    Text(Date(timeIntervalSince1970: message.at), format: .dateTime.hour().minute()).font(HomeWidgetTokens.meta).homeInk(.faint)
                }
                Text(verbatim: message.summary).font(HomeWidgetTokens.meta).lineLimit(lines).homeInk(.soft)
            }
        }
    }
}
