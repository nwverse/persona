import SwiftUI

/// By App (#3756): the agents as someone working in the desktop apps knows them, by the app they run in. A section per
/// agent's desktop app (Claude Code's, Codex's, then any other by name), then Terminal for the rest (the command line, a
/// terminal's panes). Running and offline agents alike, the latest first, as the apps' sidebars list them
/// (`AgentListOrder.recent`); an app with no agent has no section. Which app is the node's `agent_surface`, as the
/// agent's mark draws it (`AgentKind`).
nonisolated struct AppGroup: Identifiable, Sendable {
    /// "app/desktop/<agent>" or "app/terminal": the section's id and its fold key (`dev.folded`).
    var id: String
    /// The app: the agent's mark on its desktop app; nil for Terminal.
    var kind: AgentKind?
    var sessions: [Session]

    /// "Claude Code desktop app", "Terminal".
    var title: String {
        guard let kind else { return String(localized: "Terminal") }
        return String(localized: "\(kind.name) desktop app", comment: "By App's section of an agent's desktop app: Claude Code desktop app")
    }
}

nonisolated enum AppGroups {
    static let terminalID = "app/terminal"
    /// The desktop apps first, in this order; any other after them by name.
    private static let known = ["Claude Code", "Codex"]

    static func build(_ sessions: [Session]) -> [AppGroup] {
        var desktop: [String: AppGroup] = [:]
        var terminal: [Session] = []
        for s in sessions {
            let kind = AgentKind(s)
            guard kind.surface == .desktop else {
                terminal.append(s)
                continue
            }
            let id = "app/desktop/" + kind.name.lowercased()
            desktop[id, default: AppGroup(id: id, kind: kind, sessions: [])].sessions.append(s)
        }
        func rank(_ g: AppGroup) -> Int { known.firstIndex(of: g.kind?.name ?? "") ?? known.count }
        var groups = desktop.values.sorted { a, b in
            rank(a) != rank(b) ? rank(a) < rank(b) : a.id < b.id
        }
        if !terminal.isEmpty {
            groups.append(AppGroup(id: terminalID, kind: nil, sessions: terminal))
        }
        return groups.map { group in
            var group = group
            group.sessions = AgentListOrder.recent(group.sessions)
            return group
        }
    }
}

/// A By App section's title: the app's mark (Terminal's symbol) and name, and its state counts, as a machine's.
struct AppSectionHeader: View {
    var app: AppGroup
    var counts: SpaceGroup
    @Environment(\.compactList) private var compact

    var body: some View {
        CountsHeading {
            if let kind = app.kind {
                AgentMark(kind: kind).accessibilityHidden(true) // the title names it
            } else {
                ThemedIcon(Theme.Icon.terminal).foregroundStyle(.secondary)
            }
            if compact {
                Text(app.title).font(CompactList.sectionTitle).foregroundStyle(Color.primary).textCase(nil).lineLimit(1)
            } else {
                Text(app.title).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).textCase(nil).lineLimit(1)
            }
        } counts: {
            StateCounts(group: counts)
        }
    }
}
