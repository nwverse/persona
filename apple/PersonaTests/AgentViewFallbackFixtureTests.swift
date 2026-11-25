import XCTest
@testable import Persona
import PersonaWire

/// Replays the shared fixture spec/fixtures/agent-view/cases.json through the app's one fallback for a hub that sends
/// no view (`AgentViewFallback`, docs/plans/plan-typed-core.md 2.4): each session's input as a hub without `agent_view`
/// sends it (no `view`), its Needs you row as the Agents tab decides it (`AgentsNeedsYou.rows`, `TabBadges.place`,
/// through `AgentsNeedsYou.fallbackRows`), and whether the owner muted it, give exactly the view the hub's manager
/// computes (`expected.views`). The same views also read back through the generated `AgentView` and, set on the
/// session as a new hub sends them, are what `Session.agentView` and the badge read.
@MainActor
final class AgentViewFallbackFixtureTests: XCTestCase {
    private static let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private struct Fixture: Decodable {
        var version: Int
        var cases: [Case]
    }

    private struct Case: Decodable {
        var name: String
        var input: Input
        var expected: Expected
    }

    private struct Input: Decodable {
        var sessions: [Session]
        var interactions: [Interaction]
        var mutes: Pins
        var quiet: [String]
    }

    private struct Expected: Decodable {
        var views: [Entry]
    }

    private struct Entry: Decodable {
        var id: String
        var view: AgentView
    }

    private func fixture() throws -> Fixture {
        let data = try Data(contentsOf: Self.repo.appending(path: "spec/fixtures/agent-view/cases.json"))
        return try JSON.decoder.decode(Fixture.self, from: data)
    }

    func testEveryCaseInfersTheHubsView() throws {
        let fixture = try fixture()
        XCTAssertEqual(fixture.version, 1)
        XCTAssertGreaterThanOrEqual(fixture.cases.count, 20)
        for c in fixture.cases {
            let i = c.input
            XCTAssertTrue(i.sessions.allSatisfy { $0.view == nil }, "\(c.name): an input is a hub's row without a view")
            let byKey = Dictionary(i.sessions.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
            let listed = SideFold.fold(i.sessions, groups: [""]).listed
            let pending = i.interactions.sorted { ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast) }
            let waiting = AgentsNeedsYou.waiting(listed)
            let rows = AgentsNeedsYou.rows(cards: AgentsNeedsYou.cards(pending, session: { byKey[$0] }), waiting: waiting)
            func place(_ key: SessionKey) -> TabBadges.Place {
                TabBadges.place(key, session: byKey[key], quiet: i.quiet.contains(AppModel.pinKey(session: key)), mutes: i.mutes)
            }
            let counted = rows.filter { AgentsNeedsYou.counts($0, session: { byKey[$0] }, place: place) }
            let decided = AgentsNeedsYou.fallbackRows(i.sessions, rows: rows, counted: counted, cards: pending)
            XCTAssertEqual(c.expected.views.map(\.id), i.sessions.map { $0.machineID + "/" + $0.sessionID }, "\(c.name): one view per session, in order")
            for (s, want) in zip(i.sessions, c.expected.views) {
                let muted = i.mutes.mutes(session: AppModel.pinKey(session: s.key), space: s.spaceName.map { "\(s.machineID)/\($0)" })
                let got = AgentViewFallback.view(s, row: decided[s.key] ?? AgentViewFallback.Row.noRow, muted: muted)
                XCTAssertEqual(got, want.view, "\(c.name): \(want.id)")
            }
        }
    }

    /// A row the hub sent a view for draws, sorts and counts by it, whatever its legacy state says.
    func testARowWithAViewReadsTheHubs() throws {
        for c in try fixture().cases {
            for (input, want) in zip(c.input.sessions, c.expected.views) {
                var s = input
                s.view = want.view
                XCTAssertEqual(s.agentView, want.view, "\(c.name): \(want.id)")
                XCTAssertEqual(s.agentView(muted: true), want.view, "\(c.name): \(want.id), the hub counted the mute")
                let row = AgentsNeedsYou.rows(cards: [], waiting: [s]).first
                if let row {
                    XCTAssertEqual(AgentsNeedsYou.counts(row, session: { _ in s }, place: { _ in .agents }), want.view.counted == true,
                                   "\(c.name): \(want.id) counts as its view says")
                }
                let data = try JSONEncoder().encode(want.view)
                XCTAssertEqual(try JSONDecoder().decode(AgentView.self, from: data), want.view, "\(c.name): \(want.id) round-trips")
            }
        }
    }

    /// A phase or a reason this app does not know reads as idle and draws the phase's words, never a real value.
    func testAnUnknownPhaseDrawsAsIdle() throws {
        let data = Data(#"{"phase":"sleeping","reason":{"kind":"dreaming"},"rank":45}"#.utf8)
        let view = try JSONDecoder().decode(AgentView.self, from: data)
        XCTAssertEqual(view.phase, .unknown("sleeping"))
        XCTAssertEqual(view.reason, .unknown(kind: "dreaming"))
        XCTAssertEqual(view.section, .idle)
        XCTAssertEqual(view.reasonText(muted: false), AgentPhaseSection.idle.title)
        XCTAssertEqual(view.legacyState, .unknown)
        XCTAssertTrue(view.isQuiet)
    }
}
