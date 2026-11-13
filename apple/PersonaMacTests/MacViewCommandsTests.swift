import Foundation
import Testing
@testable import Persona

/// View › Text Size ⌘+ ⌘- ⌘0 and Go › Previous / Next Agent ⌘[ ⌘] (docs/apple/mac.md「Menus and shortcuts」).
@MainActor
struct MacViewCommandsTests {
    @Test func textSizeSteps() {
        #expect(MacViewCommands.step(.standard, by: 1) == .large)
        #expect(MacViewCommands.step(.large, by: 1) == .large)
        #expect(MacViewCommands.step(.standard, by: -1) == .small)
        #expect(MacViewCommands.step(.small, by: -1) == .small)
    }

    /// View › Refresh ⌘R: the iPhone pulls to refresh the list it is on; the Mac refreshes what the section shows.
    @Test func refreshFollowsTheSection() {
        #expect(MacViewCommands.refreshTarget(.inbox, page: false, mainWindow: true) == .inbox)
        #expect(MacViewCommands.refreshTarget(.assistant, page: false, mainWindow: true) == .assistant)
        #expect(MacViewCommands.refreshTarget(.agents, page: false, mainWindow: true) == .hub)
        #expect(MacViewCommands.refreshTarget(.settings, page: false, mainWindow: true) == .hub)
        // A page that loads its own data (Knowledge, Machines, a chat's Files / Git pane) refreshes itself, as its pull does on iOS.
        for s in [MacSection.knowledge, .machines, .agents, .inbox, .assistant] {
            #expect(MacViewCommands.refreshTarget(s, page: true, mainWindow: true) == .page)
            #expect(MacViewCommands.refreshTarget(s, page: true, mainWindow: false) == .page)
        }
        // Another window in front (a chat window, a sheet, Settings): the main window's section is not what is on screen.
        for s in [MacSection.inbox, .assistant, .knowledge] {
            #expect(MacViewCommands.refreshTarget(s, page: false, mainWindow: false) == .hub)
        }
    }

    /// The pages that refresh themselves, per window: the latest one on screen answers ⌘R; when it goes, the one under it.
    @Test func pageRefreshesStackPerWindow() {
        var r = MacPageRefreshes()
        let kb = UUID(), entry = UUID(), files = UUID()
        r.add(kb, window: 1)
        r.add(entry, window: 1)
        r.add(files, window: 2)
        #expect(r.top(window: 1) == entry)
        #expect(r.top(window: 2) == files)
        #expect(r.top(window: 3) == nil)
        #expect(r.top(window: nil) == nil)
        r.remove(entry)
        #expect(r.top(window: 1) == kb)
        r.move(files, to: 1) // a pane whose window number arrived late or changed
        #expect(r.top(window: 1) == files)
        #expect(r.top(window: 2) == nil)
        r.remove(files); r.remove(kb)
        #expect(r.top(window: 1) == nil)
    }

    private func session(_ id: String, _ state: AgentState, assistant: Bool = false) -> Session {
        try! JSON.decoder.decode(Session.self, from: Data("""
        {"machine_id":"m","session_id":"\(id)","title":"t","cwd":"/w","status":"idle","state":"\(state.rawValue)",
         "started_by_persona":false,"assistant":\(assistant),"pending_interactions":0}
        """.utf8))
    }

    /// The Agents list's order (needs you → unread → working → seen), Persona conversations left out, wrapping.
    @Test func neighboursInTheListsOrder() {
        let list = [session("seen", .idle), session("w", .working), session("n", .needsYou), session("j", .needsYou, assistant: true)]
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        let k = { SessionKey(machineID: "m", sessionID: $0) }
        #expect(MacViewCommands.neighbour(of: k("n"), by: 1, in: list, order: order) == k("w"))
        #expect(MacViewCommands.neighbour(of: k("seen"), by: 1, in: list, order: order) == k("n"))
        #expect(MacViewCommands.neighbour(of: k("n"), by: -1, in: list, order: order) == k("seen"))
        #expect(MacViewCommands.neighbour(of: nil, by: 1, in: list, order: order) == k("n"))
        #expect(MacViewCommands.neighbour(of: nil, by: 1, in: [], order: order) == nil)
    }

    @Test func recentNeighboursFollowActivityAndKeepPinnedAgentsFirst() {
        var seen = session("seen", .idle)
        seen.updatedAt = Date(timeIntervalSince1970: 30)
        var working = session("work", .working)
        working.updatedAt = Date(timeIntervalSince1970: 20)
        var needs = session("needs", .needsYou)
        needs.updatedAt = Date(timeIntervalSince1970: 10)
        let list = [needs, working, seen, session("assistant", .idle, assistant: true)]
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        #expect(MacViewCommands.neighbour(of: nil, by: 1, in: list, order: order, recent: true) == seen.key)
        #expect(MacViewCommands.neighbour(of: seen.key, by: 1, in: list, order: order, recent: true) == working.key)
        let pinned = AgentListOrder(isPinned: { $0 == needs.key }, isMuted: { _ in false })
        #expect(MacViewCommands.neighbour(of: nil, by: 1, in: list, order: pinned, recent: true) == needs.key)
        #expect(MacViewCommands.neighbour(of: needs.key, by: 1, in: list, order: pinned, recent: true) == seen.key)
    }
}
