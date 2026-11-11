import SwiftUI

/// By App (#3756): the agents as someone working in the desktop apps knows them, by the app they run in. A section per
/// agent's desktop app (Claude's, the Codex App, then any other by name), then Other for every agent outside one (a
/// terminal pane, the command line, a Lark group's topic, a runtime, or a node that does not say). Running and offline
/// agents alike, the latest first, as the apps' sidebars list them (`AgentListOrder.recent`); an app with no agent has no
/// section. Which app is the node's `agent_surface`, as the agent's mark draws it (`AgentKind`).
nonisolated struct AppGroup: Identifiable, Sendable {
    /// "app/desktop/<agent>" or "app/other": the section's id and its fold key (`dev.folded`).
    var id: String
    /// The app: the agent's mark on its desktop app; nil for Other.
    var kind: AgentKind?
    var sessions: [Session]

    /// The app as the rest of the list names it: "Claude desktop app" (a session's source), "Codex App" (the node's
    /// `agent_name`), else "<agent> desktop app"; "Other".
    var title: String {
        switch kind?.name {
        case nil: String(localized: "Other")
        case "Claude Code": String(localized: "Claude desktop app")
        case "Codex": String(localized: "Codex App")
        case let name?: String(localized: "\(name) desktop app", comment: "By App's section of another agent's desktop app: TRAE desktop app")
        }
    }
}

nonisolated enum AppGroups {
    static let otherID = "app/other"
    /// The desktop apps first, in this order; any other after them by name.
    private static let known = ["Claude Code", "Codex"]

    static func build(_ sessions: [Session]) -> [AppGroup] {
        var desktop: [String: AppGroup] = [:]
        var other: [Session] = []
        for s in sessions {
            let kind = AgentKind(s)
            guard kind.surface == .desktop else {
                other.append(s)
                continue
            }
            let id = "app/desktop/" + kind.name.lowercased()
            desktop[id, default: AppGroup(id: id, kind: kind, sessions: [])].sessions.append(s)
        }
        func rank(_ g: AppGroup) -> Int { known.firstIndex(of: g.kind?.name ?? "") ?? known.count }
        var groups = desktop.values.sorted { a, b in
            rank(a) != rank(b) ? rank(a) < rank(b) : a.id < b.id
        }
        if !other.isEmpty {
            groups.append(AppGroup(id: otherID, kind: nil, sessions: other))
        }
        return groups.map { group in
            var group = group
            group.sessions = AgentListOrder.recent(group.sessions)
            return group
        }
    }
}

/// A By App section's title, laid out as a machine's (`MachineSectionHeader`): the app's mark (the agents symbol for
/// Other) and name, its state counts, and how many agents it holds: offline ones too, which the state counts leave out.
struct AppSectionHeader: View {
    var app: AppGroup
    var counts: SpaceGroup
    @Environment(\.compactList) private var compact

    var body: some View {
        CountsHeading {
            if let kind = app.kind {
                AgentMark(kind: kind).accessibilityHidden(true) // the title names it
            } else {
                ThemedIcon(Theme.Icon.agents).foregroundStyle(.secondary)
                    .scaledFrame(width: Theme.Size.markS, height: Theme.Size.markS) // lined up with the marks above
            }
            if compact {
                Text(app.title).font(CompactList.sectionTitle).foregroundStyle(Color.primary).textCase(nil).lineLimit(1)
            } else {
                Text(app.title).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).textCase(nil).lineLimit(1)
            }
        } counts: {
            StateCounts(group: counts)
            CountBadge(count: app.sessions.count, look: .tone(StatusPalette.idle))
        }
    }
}
