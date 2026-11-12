import SwiftUI
import WidgetKit

/// Persona's Home Screen widgets (docs/apple/home-widgets.md): each draws only the snapshot the app left in the App
/// Group (`HomeSnapshotStore`) and the persona's pictures the Live Activity's faces already hold (`FleetLookStore`).
/// The app reloads a kind's timeline when its part of the snapshot changes; a timeline carries one more entry at the
/// moment the snapshot turns stale, so old numbers step back without a reload.
struct HomeProvider: TimelineProvider {
    var kind: HomeKind

    func placeholder(in context: Context) -> HomeEntry {
        HomeEntry(date: .now, snapshot: HomeSnapshot(paired: true))
    }

    func getSnapshot(in context: Context, completion: @escaping (HomeEntry) -> Void) {
        completion(entry(at: .now, snapshot: HomeSnapshotStore.shared.read()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<HomeEntry>) -> Void) {
        let snapshot = HomeSnapshotStore.shared.read()
        let now = Date.now
        var entries = [entry(at: now, snapshot: snapshot)]
        if let snapshot, snapshot.at > 0 {
            let stale = Date(timeIntervalSince1970: snapshot.at + HomeSnapshot.staleAfter)
            if stale > now { entries.append(entry(at: stale, snapshot: snapshot)) }
        }
        completion(Timeline(entries: entries, policy: .never))
    }

    private func entry(at date: Date, snapshot: HomeSnapshot?) -> HomeEntry {
        var e = HomeEntry(date: date, snapshot: snapshot ?? HomeSnapshot(), pictures: HomeFaces.load())
        e.relevance = HomeRelevance.of(kind, e.context)
        return e
    }
}

/// The persona's pictures for the widgets: a hub package's, one a state, from where the app keeps the Live Activity's
/// (`FleetLookStore`, at the empty states' size); nil for Mochi, which is drawn in code.
enum HomeFaces {
    static func load() -> HomeFacePictures? {
        let store = FleetLookStore.shared
        guard let package = FleetLook.load(store).supported.package else { return nil }
        var pictures = HomeFacePictures()
        let points = HomeWidgetTokens.faceEmpty
        for state in PersonaState.allCases {
            guard let face = store.face(package, state, points: points), face.own,
                  let image = FleetFaces.image(face.png, points: CGFloat(FleetFaces.size(points))) else { continue }
            pictures.states[state.rawValue] = image
        }
        if let avatar = store.read(FleetLookStore.facesDir + "/" + FleetLookStore.folder(package) + "/"
                                   + FleetLookStore.file(FleetLookStore.avatarName, FleetFaces.size(points))) {
            pictures.avatar = FleetFaces.image(avatar, points: CGFloat(FleetFaces.size(points)))
        }
        return pictures.states.isEmpty && pictures.avatar == nil ? nil : pictures
    }
}

/// How the Smart Stack ranks a widget (docs/apple/home-widgets.md「Smart Stack」): first when something waits for you or a
/// recording runs, high when a meeting is close, low otherwise.
enum HomeRelevance {
    static func of(_ kind: HomeKind, _ c: HomeContext) -> TimelineEntryRelevance {
        guard c.snapshot.paired, !c.stale else { return TimelineEntryRelevance(score: 0) }
        let mood = c.mood(kind)
        if kind == .record, c.snapshot.recording != nil { return TimelineEntryRelevance(score: 90) }
        if mood == .needsYou { return TimelineEntryRelevance(score: 100) }
        if kind == .today || kind == .record, let next = c.nextEvent, next.start - c.now < 15 * 60 {
            return TimelineEntryRelevance(score: 70)
        }
        return TimelineEntryRelevance(score: mood == .working ? 30 : 10)
    }
}

/// One kind's widget: its gallery name and line, its sizes, its view.
enum HomeWidgetConfiguration {
    static func make(_ kind: HomeKind) -> some WidgetConfiguration {
        StaticConfiguration(kind: kind.widgetKind, provider: HomeProvider(kind: kind)) { entry in
            HomeWidgetView(kind: kind, entry: entry)
        }
        .configurationDisplayName(Text(kind.title))
        .description(Text(kind.summary))
        .supportedFamilies(kind.families.filter { [.systemSmall, .systemMedium, .systemLarge].contains($0) })
    }
}

/// The first widgets (#5664): Agents, Inbox and Record. Each kind is a type of its own: a `Widget` has no arguments.
struct AgentsHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.agents) }
}

struct InboxHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.inbox) }
}

struct RecordHomeWidget: Widget {
    var body: some WidgetConfiguration { HomeWidgetConfiguration.make(.record) }
}
