import SwiftUI
import WidgetKit

/// What a widget draws at one moment: the snapshot, the persona's pictures, and the settings from Edit Widget.
struct HomeEntry: TimelineEntry {
    var date: Date
    var snapshot: HomeSnapshot
    var pictures: HomeFacePictures?
    /// The Persona widget's modules, in its four places (top left, top right, bottom left, bottom right); two to four.
    var modules: [HomeModule] = HomeEntry.defaultModules
    var agentKind: HomeSnapshot.AgentKind?
    var machine: String?
    var account: String?
    var relevance: TimelineEntryRelevance?

    /// What a new Persona widget shows: something for everyone, Agents for those who have them.
    static let defaultModules: [HomeModule] = [.agents, .today, .inbox, .record]

    var context: HomeContext { HomeContext(snapshot: snapshot, date: date, agentKind: agentKind, machine: machine, account: account) }

    /// The persona's mood for everything it watches: the agents' (all of them), offline when the data stopped coming.
    var mood: HomeMood {
        let all = HomeContext(snapshot: snapshot, date: date)
        guard snapshot.paired else { return .idle }
        return HomeMood.of(all.agentCounts, answering: snapshot.agents.assistantAnswering, stale: all.stale)
    }

    /// The color glowing behind the face: the mood's, else the persona's own.
    var glow: Color? {
        if let tone = mood.tone { return tone.dot }
        if mood == .offline { return nil }
        if let light = snapshot.look.primaryLight, let dark = snapshot.look.primaryDark { return Color(light: light, dark: dark) }
        return HomeWidgetTokens.mochiGlow
    }
}

// MARK: - The Persona widget

/// The persona in the middle, the modules the user chose around it: four tiles with the face where they meet, cut
/// out of them so it stands on the stage. The same shape at every size; the tiles say more as they grow.
struct PersonaWidgetView: View {
    var entry: HomeEntry
    /// The gallery's size (a view outside a widget has no family of its own).
    var size: WidgetFamily?
    @Environment(\.widgetFamily) private var widgetFamily
    private var family: WidgetFamily { size ?? widgetFamily }

    var body: some View {
        switch family {
        case .accessoryCircular: PersonaLockCircular(entry: entry)
        case .accessoryRectangular: PersonaLockRectangular(entry: entry)
        case .accessoryInline: PersonaLockInline(entry: entry)
        case .systemMedium: PersonaHero(entry: entry, density: .strip, face: HomeWidgetTokens.faceMedium)
        case .systemLarge: PersonaHero(entry: entry, density: .card, face: HomeWidgetTokens.faceLarge)
        case .systemExtraLarge: PersonaHero(entry: entry, density: .wide, face: HomeWidgetTokens.faceExtraLarge)
        default: PersonaHero(entry: entry, density: .corner, face: HomeWidgetTokens.faceSmall)
        }
    }
}

struct PersonaHero: View {
    var entry: HomeEntry
    var density: HomeDensity
    var face: CGFloat

    /// The face's clearing: its radius and the stage's ring around it.
    private var clearing: CGFloat { face / 2 + HomeWidgetTokens.faceGap }

    var body: some View {
        Group {
            if entry.snapshot.paired {
                ZStack {
                    tiles
                        .overlay { Circle().frame(width: clearing * 2, height: clearing * 2).blendMode(.destinationOut) }
                        .compositingGroup()
                        // Numbers that stopped coming step back behind the gray face.
                        .opacity(entry.context.stale ? HomeWidgetTokens.staleOpacity : 1)
                    HomeFace(size: face, mood: entry.mood, pictures: entry.pictures)
                }
                .padding(HomeWidgetTokens.gap)
            } else {
                HomeUnpaired(entry: entry, face: face)
            }
        }
        .containerBackground(for: .widget) { HomeStage(glow: entry.glow) }
    }

    private var modules: [HomeModule] {
        let chosen = Array(entry.modules.prefix(4))
        return chosen.count >= 2 ? chosen : HomeEntry.defaultModules
    }

    @ViewBuilder private var tiles: some View {
        let m = modules
        let gap = HomeWidgetTokens.gap - HomeWidgetTokens.hair
        switch m.count {
        case 2:
            HStack(spacing: gap) {
                tile(m[0], .leading)
                tile(m[1], .trailing)
            }
        case 3:
            VStack(spacing: gap) {
                HStack(spacing: gap) { tile(m[0], .topLeading); tile(m[1], .topTrailing) }
                tile(m[2], .bottom)
            }
        default:
            VStack(spacing: gap) {
                HStack(spacing: gap) { tile(m[0], .topLeading); tile(m[1], .topTrailing) }
                HStack(spacing: gap) { tile(m[2], .bottomLeading); tile(m[3], .bottomTrailing) }
            }
        }
    }

    private var tileContext: HomeContext {
        var c = entry.context
        c.roomy = false
        return c
    }

    /// A module's tile, its content kept out of the face's clearing: wide tiles leave the clearing's width on their
    /// inner side, tall ones its height.
    private func tile(_ module: HomeModule, _ outer: Alignment) -> some View {
        let inner = clearing - HomeWidgetTokens.gap / 2
        var edges = EdgeInsets(top: HomeWidgetTokens.tilePadding, leading: HomeWidgetTokens.tilePadding,
                               bottom: HomeWidgetTokens.tilePadding, trailing: HomeWidgetTokens.tilePadding)
        switch density {
        case .strip, .wide:
            if outer.horizontal == .leading { edges.trailing = inner } else if outer.horizontal == .trailing { edges.leading = inner }
        case .card, .tall:
            if outer.vertical == .top { edges.bottom = inner } else if outer.vertical == .bottom { edges.top = inner }
        case .corner:
            break
        }
        let link = HomeLink.url(for: module, entry: entry)
        return HomeTile {
            VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                // Where the tiles say more, they say which module they are: two can both say "Needs You".
                if density == .card || density == .wide { HomeLabel(title: module.title, symbol: module.symbol) }
                HomeModuleView(module: module, density: density, context: tileContext, outer: outer)
            }
            .padding(edges)
        }
        .modifier(TileLink(url: density == .corner ? nil : link))
    }

    private struct TileLink: ViewModifier {
        var url: URL?
        func body(content: Content) -> some View {
            if let url { Link(destination: url) { content } } else { content }
        }
    }
}

/// Before a hub is paired: the persona, and how to start.
struct HomeUnpaired: View {
    var entry: HomeEntry
    var face: CGFloat

    var body: some View {
        VStack(spacing: HomeWidgetTokens.gap) {
            HomeFace(size: face, mood: .idle, pictures: entry.pictures)
            Text("Open Persona to connect [Home Widgets]").font(HomeWidgetTokens.lineStrong)
                .multilineTextAlignment(.center).lineLimit(2).homeInk(.soft)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Where a tap goes: the page the module comes from, the most urgent agent's chat.
enum HomeLink {
    static func url(for module: HomeModule, entry: HomeEntry) -> URL? {
        switch module {
        case .agents:
            if let top = entry.context.agentRows.first {
                let parts = top.id.split(separator: "/", maxSplits: 1).map(String.init)
                if parts.count == 2 { return URL(string: "persona://open?machine=\(parts[0])&session=\(parts[1])") }
            }
            return URL(string: "persona://agents")
        case .today: return URL(string: "persona://todos")
        case .usage, .computers: return URL(string: "persona://machines")
        case .record: return URL(string: "persona://record")
        case .health: return URL(string: "persona://health")
        case .inbox: return URL(string: "persona://inbox")
        }
    }
}

// MARK: - A module's own widget

/// One module as a widget: its name, the persona in the corner saying how that module is, the content as large as
/// the widget allows.
struct ModuleWidgetView: View {
    var module: HomeModule
    var entry: HomeEntry
    var size: WidgetFamily?
    @Environment(\.widgetFamily) private var widgetFamily
    private var family: WidgetFamily { size ?? widgetFamily }

    var body: some View {
        switch family {
        case .accessoryCircular, .accessoryRectangular, .accessoryInline:
            ModuleLockView(module: module, entry: entry, size: family)
        default:
            home
        }
    }

    private var density: HomeDensity {
        switch family {
        case .systemSmall: .card
        case .systemMedium: .wide
        default: .tall
        }
    }

    private var home: some View {
        let context = entry.context
        let mood = context.mood(module)
        return VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
            HStack(alignment: .top) {
                HomeLabel(title: module.title, symbol: module.symbol)
                Spacer(minLength: HomeWidgetTokens.tight)
                HomeFace(size: HomeWidgetTokens.faceBadge, mood: mood, pictures: entry.pictures)
                    .padding(-HomeWidgetTokens.tight)
            }
            .frame(height: HomeWidgetTokens.faceBadge - HomeWidgetTokens.gap)
            if entry.snapshot.paired {
                HomeModuleView(module: module, density: density, context: context)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                if context.stale { HomeStaleNote(since: Date(timeIntervalSince1970: entry.snapshot.at), now: entry.date) }
            } else {
                Spacer(minLength: 0)
                Text("Open Persona to connect [Home Widgets]").font(HomeWidgetTokens.lineStrong).lineLimit(2).homeInk(.soft)
            }
        }
        .widgetURL(HomeLink.url(for: module, entry: entry))
        .containerBackground(for: .widget) { HomeStage(glow: mood.tone?.dot, at: .topTrailing) }
    }
}

// MARK: - Lock Screen

/// The persona's face filling the circle, its state told by the cue.
struct PersonaLockCircular: View {
    var entry: HomeEntry
    var body: some View {
        ZStack {
            AccessoryWidgetBackground()
            HomeFace(size: HomeWidgetTokens.faceLock * 1.3, mood: entry.mood, pictures: entry.pictures, disc: false)
        }
        .widgetURL(URL(string: "persona://open"))
        .containerBackground(for: .widget) { Color.clear }
    }
}

struct PersonaLockRectangular: View {
    var entry: HomeEntry
    var body: some View {
        let context = HomeContext(snapshot: entry.snapshot, date: entry.date)
        HStack(spacing: HomeWidgetTokens.gap) {
            HomeFace(size: HomeWidgetTokens.faceLock, mood: entry.mood, pictures: entry.pictures)
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: PersonaHeadline.title(context)).font(HomeWidgetTokens.lockTitle).lineLimit(1).homeAccent()
                Text(verbatim: PersonaHeadline.line(context)).font(HomeWidgetTokens.lockLine).lineLimit(2).foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .containerBackground(for: .widget) { Color.clear }
    }
}

struct PersonaLockInline: View {
    var entry: HomeEntry
    var body: some View {
        let context = HomeContext(snapshot: entry.snapshot, date: entry.date)
        Label(PersonaHeadline.title(context), systemImage: HomeWidgetTokens.personaSymbol)
            .containerBackground(for: .widget) { Color.clear }
    }
}

/// What the persona says first, in two lines: the most urgent thing, then what it is about.
enum PersonaHeadline {
    static func title(_ c: HomeContext) -> String {
        if !c.snapshot.paired { return interfaceText("Open Persona to connect [Home Widgets]") }
        let n = c.agentCounts
        if n.needsYou > 0 { return StateName.count(.needsYou, n.needsYou) }
        if let inbox = c.snapshot.inbox, inbox.needsYou > 0 { return interfaceText("\(inbox.needsYou) messages need you [Home Widgets]") }
        if n.working > 0 { return StateName.count(.working, n.working) }
        if let next = c.nextEvent { return next.title }
        if n.done > 0 { return interfaceText("\(n.done) done [Home Widgets]") }
        return interfaceText("All Quiet [Home Widgets]")
    }

    static func line(_ c: HomeContext) -> String {
        if let top = c.agentRows.first, c.agentCounts.needsYou > 0 || c.agentCounts.working > 0 {
            return [top.ask ?? top.detail, top.title].compactMap { $0 }.joined(separator: " · ")
        }
        if let next = c.nextEvent {
            return Date(timeIntervalSince1970: next.start).formatted(.dateTime.hour().minute()) + " " + next.title
        }
        return interfaceText("Nothing needs you. [Home Widgets]")
    }
}

/// A module on the Lock Screen: a number in the circle, two lines in the rectangle, one in the line above the clock.
struct ModuleLockView: View {
    var module: HomeModule
    var entry: HomeEntry
    var size: WidgetFamily?
    @Environment(\.widgetFamily) private var widgetFamily
    private var family: WidgetFamily { size ?? widgetFamily }

    var body: some View {
        Group {
            switch family {
            case .accessoryCircular: circular
            case .accessoryInline: Label(inline, systemImage: module.symbol)
            default: rectangular
            }
        }
        .privacySensitive(module.isPrivate)
        .widgetURL(HomeLink.url(for: module, entry: entry))
        .containerBackground(for: .widget) { Color.clear }
    }

    private var context: HomeContext { entry.context }

    @ViewBuilder private var circular: some View {
        switch module {
        case .usage:
            let w = context.accounts.first?.windows.max { $0.percent < $1.percent }
            Gauge(value: min(100, w?.percent ?? 0), in: 0...100) {
                Image(systemName: module.symbol)
            } currentValueLabel: {
                Text(verbatim: "\(Int(w?.percent ?? 0))")
            }
            .gaugeStyle(.accessoryCircularCapacity)
            .homeAccent()
        case .record:
            Button(intent: StartMeetingRecordingIntent()) {
                ZStack {
                    AccessoryWidgetBackground()
                    Image(systemName: HomeWidgetTokens.recordSymbol).font(HomeWidgetTokens.recordMark).homeAccent()
                }
            }
            .buttonStyle(.plain)
        case .health:
            let steps = context.snapshot.health?.steps ?? 0
            let avg = max(1, context.snapshot.health?.stepsAverage ?? 1)
            Gauge(value: min(1, Double(steps) / Double(avg))) {
                Image(systemName: HomeWidgetTokens.stepsSymbol)
            } currentValueLabel: {
                Text(verbatim: homeShort(steps))
            }
            .gaugeStyle(.accessoryCircular)
        default:
            ZStack {
                AccessoryWidgetBackground()
                VStack(spacing: 0) {
                    Image(systemName: module.symbol).font(HomeWidgetTokens.meta)
                    Text(verbatim: circleNumber).font(HomeWidgetTokens.numeral).minimumScaleFactor(HomeWidgetTokens.numeralMinScale).homeAccent()
                }
                .padding(HomeWidgetTokens.tight)
            }
        }
    }

    private var circleNumber: String {
        switch module {
        case .agents:
            let n = context.agentCounts
            return "\(n.needsYou > 0 ? n.needsYou : n.working)"
        case .today:
            return context.date.formatted(.dateTime.day())
        case .computers:
            return "\(context.machines.filter(\.online).count)/\(context.machines.count)"
        case .inbox:
            return "\(context.snapshot.inbox?.needsYou ?? 0)"
        case .usage, .record, .health:
            return ""
        }
    }

    private var rectangular: some View {
        HStack(spacing: HomeWidgetTokens.gap) {
            if module == .record {
                Button(intent: StartMeetingRecordingIntent()) {
                    Image(systemName: HomeWidgetTokens.recordSymbol).font(HomeWidgetTokens.recordMark).homeAccent()
                }
                .buttonStyle(.plain)
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: lockTitle).font(HomeWidgetTokens.lockTitle).lineLimit(1).homeAccent()
                ForEach(lockLines, id: \.self) { Text(verbatim: $0).font(HomeWidgetTokens.lockLine).lineLimit(1).foregroundStyle(.secondary) }
            }
            Spacer(minLength: 0)
        }
    }

    private var lockTitle: String {
        switch module {
        case .agents:
            let n = context.agentCounts
            if n.needsYou > 0 { return StateName.count(.needsYou, n.needsYou) }
            if n.working > 0 { return StateName.count(.working, n.working) }
            return interfaceText("All Quiet [Home Widgets]")
        case .today:
            guard let next = context.nextEvent else { return interfaceText("\(context.snapshot.today?.todoCount ?? 0) to-dos [Home Widgets]") }
            return Date(timeIntervalSince1970: next.start).formatted(.dateTime.hour().minute()) + " " + next.title
        case .usage:
            return context.accounts.first?.name ?? interfaceText("\(homeShort(context.snapshot.usage?.tokensToday ?? 0)) tokens today [Home Widgets]")
        case .computers:
            return interfaceText("\(context.machines.filter(\.online).count) of \(context.machines.count) online [Home Widgets]")
        case .record:
            return interfaceText("Record a Meeting [Home Widgets]")
        case .health:
            return interfaceText("\(context.snapshot.health?.steps ?? 0) steps [Home Widgets]")
        case .inbox:
            return interfaceText("\(context.snapshot.inbox?.needsYou ?? 0) messages need you [Home Widgets]")
        }
    }

    private var lockLines: [String] {
        switch module {
        case .agents:
            return context.agentRows.prefix(1).map { [$0.ask ?? $0.detail, $0.title].compactMap { $0 }.joined(separator: " · ") }
        case .today:
            return [interfaceText("\(context.snapshot.today?.todoCount ?? 0) to-dos [Home Widgets]")]
        case .usage:
            guard let a = context.accounts.first else { return [] }
            return [a.windows.sorted { $0.minutes < $1.minutes }.map { "\($0.label) \(Int($0.percent))%" }.joined(separator: " · ")]
        case .computers:
            return context.machines.prefix(2).map { m in m.name + " · " + (m.online ? (m.working > 0 ? StateName.count(.working, m.working) : interfaceText("Idle [Home Widgets]")) : interfaceText("Offline [Home Widgets]")) }
        case .record:
            return [interfaceText("Named after the event on your calendar now [Home Widgets]")]
        case .health:
            guard let h = context.snapshot.health?.sleepHours else { return [] }
            return [Duration.seconds(h * 3600).formatted(.units(allowed: [.hours, .minutes], width: .narrow)) + " " + interfaceText("Asleep [Home Widgets]")]
        case .inbox:
            return context.snapshot.inbox?.latest.prefix(1).map { $0.sender + ": " + $0.summary } ?? []
        }
    }

    private var inline: String {
        switch module {
        case .agents:
            let n = context.agentCounts
            return [n.needsYou > 0 ? StateName.count(.needsYou, n.needsYou) : nil, n.working > 0 ? StateName.count(.working, n.working) : nil]
                .compactMap { $0 }.joined(separator: " · ").nonEmpty ?? interfaceText("All Quiet [Home Widgets]")
        default:
            return lockTitle
        }
    }
}

private extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
