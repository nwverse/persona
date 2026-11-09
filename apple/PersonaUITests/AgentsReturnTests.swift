import XCTest

/// Going into a chat from the Agents list and back, by Back and by the edge swipe, with the search field folded and
/// pulled out, in the shell (both styles with `TEST_RUNNER_PERSONA_UI_STYLE=beautiful`): on every
/// frame of the way back the list is where it was left. The first row and the navigation bar do not move, the search
/// field stays as it was, and the navigation container keeps its frame and safe area. The shell's room for its top bar
/// was padding around the navigation container, taken away for the chat and given back on the way back: the list came
/// back 36 pt down, the search field opened, and everything sprang up 52 pt after the pop (#340). Read from
/// `probe.agents-scroll` (docs/apple/ios-state.md「探针」).
final class AgentsReturnTests: XCTestCase {
    private var app: XCUIApplication!
    private var page: String?

    struct Place: Decodable {
        var nav, search, inset, row, container, safe: Double
    }

    struct Span: Decodable {
        var min, max: Double
        var size: Double { max - min }
    }

    struct Trip: Decodable {
        var n, frames: Int
        var row, nav, search, inset, container, safe: Span
        var rowStep: Double
    }

    struct Scroll: Decodable {
        var now: Place?
        var left: Place?
        var back: Trip?
    }

    override func setUp() {
        continueAfterFailure = true
    }

    @MainActor func testTheListStaysPutOnTheWayBackInTheShell() { walk() }

    @MainActor private func walk() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-app.tab", "chats", "-tab", "dev",
                               "-dev.openChat", "", "-openAgentsFresh", "YES", "-probes", "YES"]
        app.launchPinned()
        page = "agents"
        let layout = "shell"
        XCTAssertTrue(app.buttons.matching(identifier: "agents.row").firstMatch.waitForExistence(timeout: 15), "no agent in the list")
        sleep(2) // the search field folds itself away once
        var trips = 0
        for search in ["folded", "pulled out"] {
            if search == "pulled out" {
                app.revealAgentsSearch()
                sleep(1)
            }
            for exit in ["返回", "edge swipe"] {
                let what = "\(layout), search \(search), \(exit)"
                guard openChat(what) else { return }
                if exit == "返回" {
                    app.buttons.matching(NSPredicate(format: "label == %@", "返回")).firstMatch.tap()
                } else {
                    let window = app.windows.firstMatch
                    window.coordinate(withNormalizedOffset: CGVector(dx: 0.005, dy: 0.5))
                        .press(forDuration: 0.1, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.5)),
                               withVelocity: .slow, thenHoldForDuration: 0.1)
                }
                trips += 1
                check(what, trip: trips)
            }
        }
    }

    /// Opens the first row a finger can reach.
    @MainActor private func openChat(_ what: String) -> Bool {
        let rows = app.buttons.matching(identifier: "agents.row")
        guard let row = (0..<min(rows.count, 6)).map({ rows.element(boundBy: $0) }).first(where: { $0.isHittable }) else {
            XCTFail("no row to open (\(what))")
            return false
        }
        row.tap()
        let opened = app.buttons.matching(NSPredicate(format: "label == %@", "返回")).firstMatch.waitForExistence(timeout: 10)
        XCTAssertTrue(opened, "the chat did not open (\(what))")
        sleep(1)
        return opened
    }

    @MainActor private func check(_ what: String, trip: Int) {
        var scroll: Scroll?
        let done = eventually("way back \(trip) recorded (\(what))", within: 8) {
            scroll = app.probe("agents-scroll", as: Scroll.self, page: page)
            return (scroll?.back?.n ?? 0) >= trip ? [] : ["back \(scroll?.back?.n ?? 0) of \(trip)"]
        }
        shot(what)
        guard done != nil, let left = scroll?.left, let back = scroll?.back, let now = scroll?.now else { return }
        let seen = "left \(left), back \(back)"
        XCTAssertGreaterThan(back.frames, 10, "too few frames of the way back (\(what)): \(seen)")
        XCTAssertLessThanOrEqual(back.row.size, 1, "the first row moved \(back.row.size) pt on the way back (\(what)): \(seen)")
        XCTAssertLessThanOrEqual(back.rowStep, 1, "the first row jumped \(back.rowStep) pt in a frame (\(what)): \(seen)")
        XCTAssertEqual(back.row.min, left.row, accuracy: 1, "the first row came back elsewhere (\(what)): \(seen)")
        XCTAssertLessThanOrEqual(back.nav.size, 1, "the navigation bar moved \(back.nav.size) pt (\(what)): \(seen)")
        XCTAssertEqual(back.nav.min, left.nav, accuracy: 1, "the navigation bar came back elsewhere (\(what)): \(seen)")
        XCTAssertLessThanOrEqual(back.search.size, 1, "the search field opened or folded on the way back (\(what)): \(seen)")
        XCTAssertEqual(back.search.min, left.search, accuracy: 1, "the search field is not as it was left (\(what)): \(seen)")
        XCTAssertLessThanOrEqual(back.container.size, 0.5, "the navigation container moved (\(what)): \(seen)")
        XCTAssertLessThanOrEqual(back.safe.size, 0.5, "the navigation container's safe area changed (\(what)): \(seen)")
        XCTAssertEqual(now.row, left.row, accuracy: 1, "the first row is not where it was left (\(what)): \(seen)")
        XCTAssertEqual(now.search, left.search, accuracy: 1, "the search field is not as it was left (\(what)): \(seen)")
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "\(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }
}
