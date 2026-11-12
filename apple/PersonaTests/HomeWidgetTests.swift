import Foundation
import Testing
@testable import Persona

/// The Home Screen widgets' snapshot (docs/apple/home-widgets.md): the numbers are the app's own, the file is written only
/// when it changes and read back only in its own shape, and the moods follow the rules the doc gives.
@MainActor
struct HomeWidgetTests {
    private static let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private struct Fixture: Decodable {
        var cases: [Case]
        struct Case: Decodable {
            var name: String
            var input: Input
            var expected: Expected
        }
        struct Input: Decodable {
            var sessions: [Session]
            var interactions: [Interaction]
            var mutes: Pins
            var quiet: [String]
        }
        struct Expected: Decodable {
            var badge: Int
        }
    }

    /// Replays spec/fixtures/needs-you/cases.json: the Agents widget's Needs You rows are the Agents tab's badge, case by
    /// case (one per session, never the muted agents or a quiet task's).
    @Test func needsYouIsTheAgentsBadge() throws {
        let data = try Data(contentsOf: Self.repo.appending(path: "spec/fixtures/needs-you/cases.json"))
        let fixture = try JSON.decoder.decode(Fixture.self, from: data)
        #expect(!fixture.cases.isEmpty)
        for c in fixture.cases {
            let i = c.input
            let byKey = Dictionary(i.sessions.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
            let pending = i.interactions.sorted { ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast) }
            let rows = AgentsNeedsYou.rows(cards: pending, waiting: AgentsNeedsYou.waiting(i.sessions))
            let agents = HomeAgents.build(
                needsYou: rows, sessions: i.sessions,
                place: { TabBadges.place($0, session: byKey[$0], quiet: i.quiet.contains(AppModel.pinKey(session: $0)), mutes: i.mutes) },
                session: { byKey[$0] }, machineName: { _ in nil }, isTask: { _ in false })
            #expect(agents.rows.count { $0.state == .needsYou } == c.expected.badge, "\(c.name)")
        }
    }

    /// Past the cap the rows are counted, not listed, so a full list never says fewer than there are.
    @Test func rowsPastTheCapAreCounted() {
        let sessions = (0..<(HomeSnapshot.Agents.cap + 5)).map { n in
            Self.session("s\(n)", state: "working")
        }
        let agents = HomeAgents.build(needsYou: [], sessions: sessions, place: { _ in .agents }, session: { _ in nil },
                                      machineName: { _ in nil }, isTask: { _ in false })
        #expect(agents.rows.count == HomeSnapshot.Agents.cap)
        let context = HomeContext(snapshot: HomeSnapshot(paired: true, agents: agents), date: .now)
        #expect(context.agentCounts.working == HomeSnapshot.Agents.cap + 5)
    }

    private static func session(_ id: String, state: String) -> Session {
        let json = """
        {"machine_id":"m","session_id":"\(id)","title":"\(id)","cwd":"/tmp/p","status":"idle","state":"\(state)"}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) } catch { fatalError("bad fixture: \(error)") }
    }

    @Test func snapshotReadsBackOnlyInItsShape() throws {
        let dir = FileManager.default.temporaryDirectory.appending(path: "home-widgets-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let store = HomeSnapshotStore(root: dir)
        #expect(store.read() == nil)
        var s = HomeSnapshot(at: 1_000, paired: true)
        s.inbox = .init(needsYou: 2, unread: 5, latest: [.init(id: "m1", sender: "Alex", summary: "Hello", at: 900, needsYou: true)])
        try store.write(try #require(HomeWidgetWriter.encode(s)))
        #expect(store.read() == s)
        s.version += 1
        try store.write(try #require(HomeWidgetWriter.encode(s)))
        #expect(store.read() == nil) // another version's file reads as none
        try store.write(Data("{".utf8))
        #expect(store.read() == nil)
    }

    /// A change in one part reloads that kind's widgets only; the persona and pairing count for every kind.
    @Test func partsTellWhichWidgetsChanged() {
        let a = HomeSnapshot(paired: true)
        func changed(_ b: HomeSnapshot) -> Set<HomeKind> {
            let pa = HomeWidgetWriter.parts(a), pb = HomeWidgetWriter.parts(b)
            return Set(HomeKind.allCases.filter { pa[$0] != pb[$0] })
        }
        var b = a
        b.inbox = .init(needsYou: 1)
        #expect(changed(b) == [.inbox])
        b = a
        b.recording = .init(id: "r", title: "Review", started: 1)
        #expect(changed(b) == [.record])
        b = a
        b.machines = [.init(id: "m", name: "Studio", online: true)]
        #expect(changed(b) == [.agents, .computers]) // the Agents widget names the computer an agent runs on
        b = a
        b.look.name = "Milk Frog"
        #expect(changed(b) == Set(HomeKind.allCases))
    }

    /// Written once; the same content again writes nothing (the file keeps its time); new content writes again.
    @Test func writesOnlyWhatChanged() async throws {
        let dir = FileManager.default.temporaryDirectory.appending(path: "home-widgets-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let store = HomeSnapshotStore(root: dir)
        let writer = HomeWidgetWriter(store: store)
        var source = HomeSnapshot(paired: true)
        writer.source = { source }
        writer.flush()
        let first = try #require(await Self.written(store) { $0.at > 0 })
        writer.flush()
        try await Task.sleep(for: .milliseconds(300))
        #expect(store.read()?.at == first.at)
        source.inbox = .init(needsYou: 3)
        writer.flush()
        let second = try #require(await Self.written(store) { $0.inbox?.needsYou == 3 })
        #expect(second.at >= first.at)
        // A recording that starts writes at once, without a source change.
        writer.recording = .init(id: "r", title: "Review", started: 1)
        #expect(await Self.written(store) { $0.recording?.id == "r" } != nil)
    }

    private static func written(_ store: HomeSnapshotStore, _ ok: (HomeSnapshot) -> Bool) async -> HomeSnapshot? {
        for _ in 0..<100 {
            if let s = store.read(), ok(s) { return s }
            try? await Task.sleep(for: .milliseconds(20))
        }
        return nil
    }

    @Test func moodsFollowTheRules() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        var s = HomeSnapshot(at: now.timeIntervalSince1970, paired: true)
        s.agents.rows = [.init(id: "m/a", title: "A", state: .working)]
        #expect(HomeContext(snapshot: s, date: now).mood(.agents) == .working)
        s.agents.rows.append(.init(id: "m/b", title: "B", state: .needsYou))
        #expect(HomeContext(snapshot: s, date: now).mood(.agents) == .needsYou)
        // Over an hour without a write: offline, whatever the counts.
        #expect(HomeContext(snapshot: s, date: now.addingTimeInterval(HomeSnapshot.staleAfter + 1)).mood(.agents) == .offline)
        s.usage = .init(accounts: [.init(id: "a", name: "Plan", windows: [.init(label: "5h", minutes: 300, percent: 85)])])
        #expect(HomeContext(snapshot: s, date: now).mood(.usage) == .working)
        s.usage?.accounts[0].windows[0].percent = 96
        #expect(HomeContext(snapshot: s, date: now).mood(.usage) == .needsYou)
        s.todos = .init(items: [], count: 0, overdue: 0)
        #expect(HomeContext(snapshot: s, date: now).mood(.todos) == .done)
        s.inbox = .init(needsYou: 1)
        #expect(HomeContext(snapshot: s, date: now).mood(.inbox) == .needsYou)
        #expect(HomeContext(snapshot: HomeSnapshot(), date: now).mood(.agents) == .offline) // not paired
    }
}
