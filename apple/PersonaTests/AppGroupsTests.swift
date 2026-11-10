import Foundation
import Testing
@testable import Persona

/// By App (#3756): the agents in a section per app they run in, as the hub sends them.
struct AppGroupsTests {
    private static func session(_ id: String, agent: String, surface: String?, state: String = "idle", updated: TimeInterval) -> Session {
        let surfaceField = surface.map { #","agent_surface":"\#($0)""# } ?? ""
        let date = ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: updated))
        let json = #"{"machine_id":"m","session_id":"\#(id)","title":"\#(id)","cwd":"/w","status":"\#(state)","state":"\#(state)","agent":"\#(agent)","updated_at":"\#(date)"\#(surfaceField)}"#
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func desktopAppsFirstThenOtherWithOfflineAgentsNewestFirst() {
        let sessions = [
            Self.session("term-claude", agent: "claude", surface: "cli", updated: 50),
            Self.session("codex-old", agent: "codex", surface: "desktop", state: "offline", updated: 10),
            Self.session("claude-offline", agent: "claude", surface: "desktop", state: "offline", updated: 40),
            Self.session("codex-new", agent: "codex", surface: "desktop", state: "working", updated: 30),
            Self.session("claude-new", agent: "claude", surface: "desktop", state: "working", updated: 20),
            Self.session("unknown-client", agent: "codex", surface: nil, updated: 60),
            Self.session("trae-app", agent: "trae", surface: "desktop", updated: 5),
            Self.session("amp-app", agent: "amp", surface: "desktop", updated: 6),
        ]
        let groups = AppGroups.build(sessions)
        #expect(groups.map(\.id) == ["app/desktop/claude code", "app/desktop/codex", "app/desktop/amp", "app/desktop/trae", AppGroups.otherID])
        #expect(groups.map { $0.sessions.map(\.sessionID) } == [
            ["claude-offline", "claude-new"], // offline ones stay, the latest first
            ["codex-new", "codex-old"],
            ["amp-app"], // other desktop apps after Claude's and Codex's, by name
            ["trae-app"],
            ["unknown-client", "term-claude"], // no surface reported: not a desktop app's
        ])
        #expect(groups[0].kind?.surface == .desktop && groups[0].kind?.name == "Claude Code")
        #expect(groups[0].title == String(localized: "Claude desktop app") && groups[1].title == String(localized: "Codex App"))
        let trae = "TRAE"
        #expect(groups[3].title == String(localized: "\(trae) desktop app"))
        #expect(groups[4].kind == nil && groups[4].title == String(localized: "Other"))
    }

    @Test func claudeWhenTheAgentIsUnnamedAndCodexByItsSourceKind() {
        let unnamed = Self.session("unnamed", agent: "", surface: "desktop", updated: 2)
        var byKind = Self.session("by-kind", agent: "", surface: "desktop", updated: 1)
        byKind.sourceKind = "codex_history"
        let groups = AppGroups.build([unnamed, byKind])
        #expect(groups.map(\.id) == ["app/desktop/claude code", "app/desktop/codex"])
        #expect(groups.map { $0.sessions.map(\.sessionID) } == [["unnamed"], ["by-kind"]])
    }

    @Test func agentsUpdatedAtTheSameTimeKeepOneOrder() {
        let a = Self.session("a", agent: "claude", surface: "desktop", updated: 7)
        let b = Self.session("b", agent: "claude", surface: "desktop", updated: 7)
        #expect(AppGroups.build([b, a]).first?.sessions.map(\.sessionID) == AppGroups.build([a, b]).first?.sessions.map(\.sessionID))
    }

    @Test func anAppWithNoAgentHasNoSection() {
        #expect(AppGroups.build([]).isEmpty)
        let onlyDesktop = AppGroups.build([Self.session("a", agent: "claude", surface: "desktop", updated: 1)])
        #expect(onlyDesktop.map(\.id) == ["app/desktop/claude code"])
        let onlyOther = AppGroups.build([Self.session("a", agent: "codex", surface: "cli", updated: 1)])
        #expect(onlyOther.map(\.id) == [AppGroups.otherID])
    }

    @MainActor @Test func byAppIsOfferedAfterByMachineAndTheDefaultStays() {
        #expect(DevGrouping.standard == .repo)
        let offered = DevGrouping.offered(supportsTodos: true)
        #expect(offered.firstIndex(of: .app) == offered.firstIndex(of: .machine).map { $0 + 1 })
        #expect(DevGrouping(rawValue: "app") == .app)
        #expect(DevGrouping.app.title == String(localized: "By App") && DevGrouping.app.symbol == Theme.Icon.byApp)
    }
}
