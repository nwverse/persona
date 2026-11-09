import XCTest

/// The Agents list while the hub's link comes and goes, sampled every few frames' time (`probe.agents-scroll`): the
/// search field stays as it was, pulled out or folded, and the rows do not move. The link used to take a row of room
/// through the window's safe area: folding, the list read an inset of 0 for 100–200 ms and jumped some 210 pt, and
/// now and then (2 in 12) the navigation bar took that for a scroll and folded the search pulled out of it (#413).
/// It floats now (docs/apple/shell.md「连接状态和 toast」).
final class ShellRoomScrollTests: XCTestCase {
    private var app: XCUIApplication!

    struct Place: Decodable {
        var nav, search, inset, row, container, safe: Double
    }

    struct Scroll: Decodable {
        var now: Place?
    }

    /// The link's comings and goings watched per search state: the flicker came 2 times in 12.
    private static let changes = 12

    override func setUp() {
        continueAfterFailure = true
    }

    @MainActor func testTheSearchPulledOutStaysWhileTheLinkComesAndGoes() { watch(pulledOut: true) }

    @MainActor func testTheSearchFoldedStaysFoldedWhileTheLinkComesAndGoes() { watch(pulledOut: false) }

    @MainActor private func watch(pulledOut: Bool) {
        app = XCUIApplication()
        // Fine for 4 s, Connecting… for 3 s, again and again.
        app.launchArguments = ["-demo.on", "YES", "-app.tab", "chats", "-tab", "dev", "-dev.openChat", "",
                               "-openAgentsFresh", "YES", "-probes", "YES", "-showConnectionCycle", "connecting:4:3"]
        app.launchPinned()
        defer { app.terminate() }
        XCTAssertTrue(app.buttons.matching(identifier: "agents.row").firstMatch.waitForExistence(timeout: 15), "no agent in the list")
        sleep(2) // the search field folds itself away once
        if pulledOut { app.revealAgentsSearch() }
        let search = pulledOut ? "pulled out" : "folded"
        sleep(1)
        guard let rest = scroll()?.now else { return XCTFail("no list probe") }
        XCTAssertEqual(rest.search > 0, pulledOut, "the search is not \(search) to begin with: \(rest)")
        var status = app.shellValue("status")
        var changes = 0, samples = 0
        let start = Date()
        while changes < Self.changes, Date().timeIntervalSince(start) < Double(Self.changes) * 4 + 30 {
            usleep(120_000)
            if let s = app.shellValue("status"), s != status { status = s; changes += 1 }
            guard let now = scroll()?.now else { continue }
            samples += 1
            XCTAssertEqual(now.search, rest.search, accuracy: 0.5, "the search field changed to \(now.search) (search \(search), change \(changes), \(status ?? "?"))")
            XCTAssertEqual(now.row, rest.row, accuracy: 1, "the rows moved by \(now.row - rest.row) (search \(search), change \(changes), \(status ?? "?"))")
            XCTAssertEqual(now.safe, rest.safe, accuracy: 0.5, "the navigation container's safe area moved (search \(search))")
        }
        XCTAssertEqual(changes, Self.changes, "the link came and went \(changes) times, not \(Self.changes) (search \(search))")
        XCTAssertGreaterThan(samples, changes * 5, "too few samples")
    }

    @MainActor private func scroll() -> Scroll? { app.probe("agents-scroll", as: Scroll.self, page: "agents") }
}
