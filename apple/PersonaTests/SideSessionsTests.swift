import Foundation
import Testing
@testable import Persona

/// Side sessions under the session that started them (#5935, `SideSessions`): the web's cases
/// (`web/src/lib/side-sessions.test.ts`) on the same rule, then the Agents list's sections and counts with and
/// without the nesting.
@MainActor struct SideSessionsTests {
    private struct Row: Equatable {
        var id: String
        var parent: String?
    }
    private func r(_ id: String, _ parent: String? = nil) -> Row { Row(id: id, parent: parent) }
    private func arrange(_ rows: [Row]) -> SideSessions.Arranged<Row, String> {
        SideSessions.nestUnderParents(rows, key: \.id, parentKey: \.parent)
    }
    private func ids(_ rows: [Row]) -> [String] { rows.map(\.id) }
    private func nestedIDs(_ out: SideSessions.Arranged<Row, String>) -> [String] {
        out.rows.filter { out.nested.contains($0.id) }.map(\.id)
    }

    // MARK: The web's cases

    @Test func givesTheSameRowsBackWhenNoneNamesAListedParent() {
        let rows = [r("a"), r("b", "gone"), r("c")]
        let out = arrange(rows)
        #expect(out.rows == rows)
        #expect(out.nested.isEmpty)
        #expect(rows.allSatisfy { out.lead(of: $0.id) == $0.id })
    }

    @Test func putsSideSessionsUnderTheirParentTheGroupWhereItsFirstMemberWas() {
        // newest first: a side chat of p is the newest row, p itself is older than q
        let rows = [r("s1", "p"), r("q"), r("s2", "p"), r("p"), r("z")]
        let out = arrange(rows)
        #expect(ids(out.rows) == ["p", "s1", "s2", "q", "z"])
        #expect(nestedIDs(out) == ["s1", "s2"])
        // the group is as recent as s1, its most recent member
        #expect(out.lead(of: "p") == "s1")
        #expect(out.lead(of: "s2") == "s1")
        #expect(out.lead(of: "q") == "q")
    }

    @Test func nestsASideSessionOfASideSessionUnderTheTopmostListedOneOneLevelIn() {
        let out = arrange([r("p"), r("s", "p"), r("g", "s")])
        #expect(ids(out.rows) == ["p", "s", "g"])
        #expect(nestedIDs(out) == ["s", "g"])
    }

    @Test func aRowWhoseParentIsNotListedStaysARoot() {
        let out = arrange([r("g", "s"), r("p")]) // s (g's parent) is on another page
        #expect(ids(out.rows) == ["g", "p"])
        #expect(out.nested.isEmpty)
    }

    @Test func linksNothingInALoopAndKeepsEveryRowOnce() {
        let out = arrange([r("a", "b"), r("b", "a"), r("c", "a"), r("d", "d")])
        #expect(ids(out.rows) == ["a", "b", "c", "d"])
        #expect(out.nested.isEmpty)
    }

    @Test func neitherAddsNorDropsARow() {
        let rows = [r("s1", "p"), r("x", "y"), r("p"), r("y"), r("s2", "p"), r("w", "x")]
        let out = arrange(rows)
        #expect(ids(out.rows).sorted() == ids(rows).sorted())
        #expect(ids(out.rows) == ["p", "s1", "s2", "y", "x", "w"])
    }

    // MARK: Folds and caps keep a group together

    @Test func aGroupStaysOutOfTheFoldWhileAnyMemberOfItDoes() {
        let rows = [r("s1", "p"), r("q"), r("s2", "p"), r("p"), r("z")]
        let out = arrange(rows)
        // only s1 stays (it needs the user, say): its parent and its sibling stay with it
        let parts = out.split { $0.id == "s1" || $0.id == "q" }
        #expect(ids(parts.stay) == ["p", "s1", "s2", "q"])
        #expect(ids(parts.fold) == ["z"])
        // a busy parent keeps its idle side sessions in view too
        #expect(ids(out.split { $0.id == "p" }.stay) == ["p", "s1", "s2"])
    }

    @Test func showAllSplitsNoGroup() {
        let out = arrange([r("p"), r("s1", "p"), r("s2", "p"), r("q")])
        #expect(out.unsplit(1, out.rows, key: \.id) == 3, "the cap goes past p's side sessions")
        #expect(out.unsplit(3, out.rows, key: \.id) == 3)
        #expect(arrange([r("a"), r("b")]).unsplit(1, [r("a"), r("b")], key: \.id) == 1)
    }

    // MARK: Sessions

    private func session(_ id: String, state: String = "idle", parent: String? = nil, minutes: Double = 0, machine: String = "box",
                         cwd: String = "/code/weather", space: String? = "w1", slot: String? = nil, paneIndex: Int? = nil,
                         surface: String? = nil, group: String = "") throws -> Session {
        var fields: [String: Any] = ["machine_id": machine, "session_id": id, "title": id, "cwd": cwd, "status": "idle", "state": state,
                                     "updated_at": ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: 1_800_000_000 - minutes * 60))]
        if let parent { fields["parent_agent_id"] = parent }
        if let space { fields.merge(["space_id": space, "space_name": "weather", "space_kind": "herdr"]) { _, new in new } }
        if let slot { fields.merge(["slot_id": slot, "slot_index": 1]) { _, new in new } }
        if let paneIndex { fields["pane_index"] = paneIndex }
        if let surface { fields.merge(["agent": "claude", "agent_surface": surface]) { _, new in new } }
        var s = try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
        s.group = group
        return s
    }

    @Test func decodesTheParentItIsDrawnUnder() throws {
        let side = try session("side", parent: "main")
        #expect(side.parentAgentID == "main")
        #expect(side.sideParent == SessionKey(machineID: "box", sessionID: "main"))
        #expect(!side.isSubtask, "a side session is no subtask")
        #expect(try session("plain").parentAgentID == nil)
        #expect(try session("empty", parent: "").sideParent == nil)
        #expect(try session("self", parent: "self").sideParent == nil)
        // on its own hub and machine
        #expect(try session("far", parent: "main", group: "hub-b").sideParent == SessionKey(group: "hub-b", machineID: "box", sessionID: "main"))
    }

    /// The mock hub's `MOCK_SIDE_SESSIONS=1`: a review two hours old, a /btw question started from it that needs the
    /// user and a side chat, both newer, and two others.
    private func fixture(nesting: Bool = true) throws -> [Session] {
        let parent = nesting ? "review" : nil
        return [
            try session("btw", state: "needs_you", parent: parent, minutes: 7, space: nil),
            try session("other", state: "working", minutes: 9, slot: "w1:t2"),
            try session("side", parent: parent, minutes: 12, space: nil),
            try session("review", minutes: 120, slot: "w1:t3"),
            try session("old", minutes: 600, slot: "w1:t4"),
        ]
    }

    private func projection(_ sessions: [Session], _ edit: (inout AgentListProjection.Settings) -> Void = { _ in }) -> AgentListProjection {
        var settings = AgentListProjection.Settings()
        edit(&settings)
        return AgentListProjection.build(.init(sessions: sessions, machines: [], settings: settings))
    }

    @Test func aWorkingTreesRowsDrawTheGroupTogetherInViewWithTheSideSessionThatNeedsTheUser() throws {
        let list = projection(try fixture())
        let rows = try #require(list.rows.values.first { $0.all.flatMap(\.sessions).contains { $0.sessionID == "review" } })
        let ids = { (entries: [TabPanes.Entry]) in entries.flatMap(\.sessions).map(\.sessionID) }
        // the group where the /btw question ranks (Needs you first), the review on top, the side sessions a level in
        #expect(ids(rows.all) == ["review", "btw", "side", "other", "old"])
        #expect(rows.nested == [SessionKey(machineID: "box", sessionID: "btw"), SessionKey(machineID: "box", sessionID: "side")])
        // the idle review and side chat stay out of "N more idle" with the /btw question
        #expect(ids(rows.busy) == ["review", "btw", "side", "other"])
        #expect(ids(rows.idle) == ["old"])
        // without the link the rows are as before: the idle ones fold
        let flat = try #require(projection(try fixture(nesting: false)).rows.values.first { !$0.all.isEmpty })
        #expect(ids(flat.busy) == ["btw", "other"])
        #expect(ids(flat.idle) == ["side", "review", "old"])
        #expect(flat.nested.isEmpty)
    }

    @Test func aSideSessionUnderAParentInATabWithSeveralGoesUnderTheTabsRow() throws {
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        let rows = order.rows([
            try session("side", parent: "lead", minutes: 1, space: nil),
            try session("lead", minutes: 30, slot: "w1:t1", paneIndex: 0),
            try session("mate", minutes: 40, slot: "w1:t1", paneIndex: 1),
            try session("alone", minutes: 5, slot: "w1:t2"),
        ], all: false)
        #expect(rows.all.map(\.id) == ["tab|box/herdr:w1/w1:t1", "box/side", "box/alone"])
        #expect(rows.nested == [SessionKey(machineID: "box", sessionID: "side")])
        let side = try session("side"), alone = try session("alone")
        #expect(rows.level(side) == 1 && rows.level(alone) == 0)
    }

    @Test func aParentInAnotherSectionLeavesTheRowWhereItWas() throws {
        // By Status: the /btw question is in Needs You, the review in Seen; neither moves
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        let sessions = try fixture()
        let needs = order.arranged(sessions.filter { order.rank($0) == 0 })
        #expect(needs.rows.map(\.sessionID) == ["btw"] && needs.nested.isEmpty)
        let seen = order.arranged(sessions.filter { order.rank($0) == 7 })
        #expect(seen.rows.map(\.sessionID) == ["review", "side", "old"])
        #expect(seen.nested == [SessionKey(machineID: "box", sessionID: "side")])
    }

    @Test func byMachineKeepsAGroupInOrOutOfIdleTogether() throws {
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        let rows = AgentListRows(order: order, grouping: .machine, isFolded: { _ in false })
        let refs = [MachineRef(group: "", id: "box")]
        #expect(rows.machines(try fixture(), order: refs).map(\.sessionID) == ["review", "btw", "side", "other"])
        #expect(rows.machines(try fixture(nesting: false), order: refs).map(\.sessionID) == ["btw", "other"])
    }

    @Test func byAppNestsInAProjectAndKeepsItsCounts() throws {
        let sessions = [
            try session("btw", state: "needs_you", parent: "review", minutes: 7, space: nil, surface: "desktop"),
            try session("review", minutes: 120, space: nil, surface: "desktop"),
            try session("solo", minutes: 30, space: nil, surface: "desktop"),
        ]
        let app = try #require(AppGroups.build(sessions).first)
        let project = try #require(app.projects.first)
        #expect(project.sessions.map(\.sessionID) == ["review", "btw", "solo"])
        #expect(project.level(sessions[0]) == 1 && project.level(sessions[1]) == 0)
        #expect(app.sessions.count == 3 && project.sessions.count == 3)
    }

    /// Acceptance (#5935): the quick filters' counts, Needs you and every section's count are the same with and
    /// without nesting, in every grouping; nesting only moves rows.
    @Test func everyCountIsTheSameWithAndWithoutNesting() throws {
        let nested = try fixture() + [try session("far", state: "unread", parent: "elsewhere", minutes: 3)]
        let flat = nested.map { s -> Session in
            var s = s
            s.parentAgentID = nil
            return s
        }
        for grouping in ["repo", "space", "app", "recent", "machine", "state", "todo"] {
            for filter in [AgentFilter.all, .needsYou, .unread] {
                let a = projection(nested) { $0.grouping = grouping; $0.filter = filter }
                let b = projection(flat) { $0.grouping = grouping; $0.filter = filter }
                #expect(a.filterCounts == b.filterCounts, "quick filter counts, \(grouping)")
                #expect(AgentsNeedsYou.waiting(a.base).count == AgentsNeedsYou.waiting(b.base).count, "Needs you, \(grouping)")
                #expect(a.filtered.map(\.key) == b.filtered.map(\.key), "the rows listed, \(grouping)")
                #expect(a.unread == b.unread)
                #expect(Set(a.rows.keys) == Set(b.rows.keys), "sections, \(grouping)")
                for (key, rows) in a.rows {
                    let before = try #require(b.rows[key])
                    let keys = { (entries: [TabPanes.Entry]) in entries.flatMap(\.sessions).map(\.key) }
                    #expect(keys(rows.all).count == keys(before.all).count && Set(keys(rows.all)) == Set(keys(before.all)),
                            "a section's rows, \(grouping) \(key)")
                    #expect(keys(rows.busy).count + keys(rows.idle).count == keys(before.all).count, "nothing dropped or doubled")
                }
                for (id, repo) in a.repositories {
                    let before = try #require(b.repositories[id])
                    for state in AgentState.allCases { #expect(repo.counts.count(state) == before.counts.count(state)) }
                }
                for (group, before) in zip(a.active + a.quiet, b.active + b.quiet) {
                    #expect(group.id == before.id && group.sessions.count == before.sessions.count)
                }
                for (app, before) in zip(a.apps, b.apps) {
                    #expect(app.sessions.count == before.sessions.count && app.projects.map(\.sessions.count) == before.projects.map(\.sessions.count))
                }
            }
        }
        // By Status's sections and By Machine's, which the list draws from the same arrangement
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        for rank in AgentListOrder.stateKeys.indices {
            #expect(Set(order.arranged(nested.filter { order.rank($0) == rank }).rows.map(\.key))
                    == Set(order.ordered(flat.filter { order.rank($0) == rank }).map(\.key)))
        }
        let arranged = order.arranged(nested)
        let parts = arranged.split(order.stays)
        #expect(parts.stay.count + parts.fold.count == nested.count)
        #expect(arranged.rows.count == nested.count && Set(arranged.rows.map(\.key)) == Set(nested.map(\.key)))
    }

    // MARK: Side sessions row (#7755)

    private let review = SessionKey(machineID: "box", sessionID: "review")

    @Test func eachNestedRowKnowsTheTopItIsUnder() {
        let out = arrange([r("p"), r("s", "p"), r("g", "s"), r("q")])
        #expect(out.top == ["s": "p", "g": "p"], "one level in, under the topmost listed one")
        #expect(arrange([r("a"), r("b", "gone")]).top.isEmpty)
    }

    @Test func aSideSessionsRowSaysHowManyAndFoldsAllButOneThatNeedsTheUser() throws {
        let list = projection(try fixture())
        let rows = try #require(list.rows.values.first { $0.all.flatMap(\.sessions).contains { $0.sessionID == "review" } })
        let all = rows.all.flatMap(\.sessions)
        let btw = try #require(all.first { $0.sessionID == "btw" }), side = try #require(all.first { $0.sessionID == "side" })
        #expect(rows.sides.under == [review: .init(parent: review, count: 2)])
        // open by default: every row drawn, the row says 2
        let open = rows.sides.folding { _ in false }
        #expect(open.fold(after: review) == .init(id: "side:box/review", count: 2, open: true))
        #expect(all.allSatisfy { !open.hides($0) })
        // folded by its parent's id: the idle side chat goes, the /btw question that needs the user stays, the row still says 2
        let folded = rows.sides.folding { $0 == SideSessions.foldID(self.review) }
        #expect(folded.fold(after: review) == .init(id: "side:box/review", count: 2, open: false))
        #expect(folded.hides(side) && !folded.hides(btw))
        #expect(all.filter { !folded.hides($0) }.map(\.sessionID) == ["review", "btw", "other", "old"])
        // another parent's fold leaves it open; no row follows a session without side sessions
        #expect(rows.sides.folding { $0 == "side:box/other" }.fold(after: review)?.open == true)
        #expect(open.fold(after: btw.key) == nil && open.fold(after: side.key) == nil)
    }

    @Test func aFoldIsKeptPerParentUnderAnIdNoOtherFoldHas() throws {
        #expect(SideSessions.foldID(review) == "side:box/review")
        #expect(SideSessions.foldID(SessionKey(group: "hub-b", machineID: "box", sessionID: "review")) == "side:hub-b|box/review")
        let s = try session("review", slot: "w1:t3")
        let others = ["state/idle", "machine/" + s.machineRef.path, TabPanes.foldID(s), "todo:none", "direction:d1"]
            + SpaceGroup.build([s]).map(\.id) + AppGroups.build([s]).map(\.id)
        for id in others { #expect(!id.hasPrefix("side:"), "\(id)") }
    }

    @Test func aTabsSideSessionsFoldByTheParentInTheTabAndFollowItsLastAgent() throws {
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        let rows = order.rows([
            try session("side", parent: "lead", minutes: 1, space: nil),
            try session("lead", minutes: 30, slot: "w1:t1", paneIndex: 0),
            try session("mate", minutes: 40, slot: "w1:t1", paneIndex: 1),
        ], all: false)
        let lead = SessionKey(machineID: "box", sessionID: "lead"), mate = SessionKey(machineID: "box", sessionID: "mate")
        #expect(rows.sides.under == [mate: .init(parent: lead, count: 1)], "after the tab's last agent, folded by its parent")
        let folded = rows.sides.folding { $0 == SideSessions.foldID(lead) }
        #expect(folded.hides(try session("side")) && folded.fold(after: mate)?.open == false)
    }

    @Test func aParentInAnotherSectionHasNoSideSessionsRow() throws {
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        let sessions = try fixture()
        #expect(order.arranged(sessions.filter { order.rank($0) == 0 }).sides.isEmpty, "Needs You: the /btw question alone")
        #expect(order.arranged(sessions.filter { order.rank($0) == 7 }).sides.under == [review: .init(parent: review, count: 1)])
    }

    @Test func collapseAllFoldsEveryListedParentWithSideSessions() throws {
        #expect(SideSessions.foldIDs(try fixture()) == ["side:box/review"])
        #expect(SideSessions.foldIDs(try fixture(nesting: false)).isEmpty)
        #expect(SideSessions.foldIDs(try fixture().filter { $0.sessionID != "review" }).isEmpty, "a parent not listed has no row")
        #expect(projection(try fixture()).sideFolds == ["side:box/review"])
    }

    /// Acceptance (#7755): folding hides rows, not sessions. Every list a count is read from (the quick filters, Needs
    /// you, the sections and their headers, "N more idle", Show All) is the same folded and open; the rows drawn differ
    /// only by the side sessions folded away, never one that needs the user, and each Side sessions row says the same.
    @Test func everyCountIsTheSameFoldedAndOpen() throws {
        let sessions = try fixture() + [try session("far", state: "unread", parent: "elsewhere", minutes: 3)]
        let order = AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false })
        var checked = 0
        func check(_ list: [Session], _ sides: SideSessions.Groups, _ label: String) {
            let open = sides.folding { _ in false }, shut = sides.folding { _ in true }
            #expect(list.allSatisfy { !open.hides($0) }, "open draws every row, \(label)")
            let gone = list.filter(shut.hides)
            #expect(gone.allSatisfy { sides.top[$0.key] != nil && $0.countedState != .needsYou }, "only side sessions that do not need you, \(label)")
            #expect(list.filter { !shut.hides($0) }.count + gone.count == list.count, "nothing dropped or doubled, \(label)")
            for (key, under) in sides.under {
                #expect(open.fold(after: key)?.count == shut.fold(after: key)?.count, "the row's count, \(label)")
                #expect(under.count == sides.top.values.filter { $0 == key }.count)
                #expect(list.contains { $0.key == key } && !shut.hides(list.first { $0.key == key }!), "its row stays, \(label)")
            }
            checked += sides.under.count
        }
        for grouping in ["repo", "space", "app", "recent", "machine", "state", "todo"] {
            for filter in [AgentFilter.all, .needsYou, .unread] {
                let a = projection(sessions) { $0.grouping = grouping; $0.filter = filter }
                let again = projection(sessions) { $0.grouping = grouping; $0.filter = filter }
                // nothing a fold decides enters the projection the counts come from
                #expect(a.filterCounts == again.filterCounts && a.unread == again.unread && a.filtered.map(\.key) == again.filtered.map(\.key))
                let waiting = AgentsNeedsYou.waiting(a.base)
                for (key, rows) in a.rows {
                    let all = rows.all.flatMap(\.sessions)
                    check(all, rows.sides, "\(grouping) \(key)")
                    let shut = rows.sides.folding { _ in true }
                    #expect(waiting.filter { w in all.contains { $0.key == w.key } }.allSatisfy { !shut.hides($0) }, "Needs you in view")
                    #expect(rows.busy.flatMap(\.sessions).count + rows.idle.flatMap(\.sessions).count == all.count, "N more idle, Show All")
                }
                for app in a.apps {
                    check(app.sessions, app.sides, "\(grouping) \(app.id)")
                    for project in app.projects { check(project.sessions, project.sides, "\(grouping) \(project.id)") }
                }
                // By Status's, By Machine's and Recently Used's sections, arranged where they are drawn
                for rank in AgentListOrder.stateKeys.indices {
                    let arranged = order.arranged(a.unpinned.filter { order.rank($0) == rank })
                    check(arranged.rows, arranged.sides, "state \(rank)")
                }
                let recent = SideSessions.arrange(AgentListOrder.recent(a.unpinned))
                check(recent.rows, recent.sides, "recent")
            }
        }
        #expect(checked > 0, "some section drew a Side sessions row")
    }
}
