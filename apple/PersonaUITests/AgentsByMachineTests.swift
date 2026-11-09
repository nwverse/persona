import XCTest

/// The Agents list split by machine (#2108): By Repository, the default, puts each machine's repositories under its heading, and
/// By Project its spaces; a heading folds its machine; one machine shown (the machine menu) has no heading. Against
/// scripts/mock/mock_hub.py (PERSONA_MOCK_HUB: three machines). The working-tree level is `AgentRepoTreeTests`' and the
/// screenshots' (`MOCK_WORKTREES=1`, docs/operations/testing.md).
final class AgentsByMachineTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.lifetime = .keepAlways
            add(a)
        }
        super.tearDown()
    }

    /// A fresh list (nothing folded, cards, the default grouping unless `grouping`), under Working: the mock's working
    /// agents are on 「服务器」 (shop-api) and MacBook (weather), which keeps the list short.
    @MainActor private func launch(grouping: String? = nil, machine: String = "") throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-dev.openChat", "", "-openAgentsFresh", "YES",
                               "-dev.machineFilter", machine] + (grouping.map { ["-dev.grouping", $0] } ?? [])
        app.launchPinned()
        let working = app.buttons["agents.filter.working"]
        XCTAssertTrue(working.waitForExistence(timeout: 30), "no Agents list")
        working.tap()
    }

    private func heading(_ machine: String) -> XCUIElement {
        app.buttons.matching(identifier: "agents.host").matching(NSPredicate(format: "label BEGINSWITH %@", machine + "，")).firstMatch
    }

    /// A repository's or a space's header (the fold button: its name first).
    private func header(_ name: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ AND identifier != %@", name + "，", "agents.host")).firstMatch
    }

    @MainActor func testRepositoriesUnderTheirMachines() throws {
        try launch()
        let server = heading("服务器"), macbook = heading("MacBook")
        XCTAssertTrue(server.waitForExistence(timeout: 15), "服务器's heading")
        app.bringAboveTabBar(macbook)
        XCTAssertLessThan(server.frame.minY, macbook.frame.minY, "machines in the hub's order")
        XCTAssertTrue(macbook.label.contains("1 个工作中"), macbook.label)
        let weather = header("weather")
        app.bringAboveTabBar(weather)
        XCTAssertLessThan(macbook.frame.minY, weather.frame.minY, "weather under MacBook")
        XCTAssertFalse(weather.label.contains("MacBook"), "a repository under its machine does not name it: \(weather.label)")
        // The repository's one space gives it its actions, in its long-press menu: no ⋯ beside it (#2531).
        XCTAssertFalse(app.buttons["weather 的更多操作"].exists, "a ⋯ beside the repository")

        macbook.tap()
        XCTAssertTrue(heading("MacBook").label.contains("已折叠"), heading("MacBook").label)
        let gone = NSPredicate(format: "exists == false")
        wait(for: [expectation(for: gone, evaluatedWith: header("weather"))], timeout: 5)
        XCTAssertTrue(heading("MacBook").exists, "the folded machine keeps its heading")
        heading("MacBook").tap()
        XCTAssertTrue(header("weather").waitForExistence(timeout: 5), "weather back")
    }

    @MainActor func testSpacesUnderTheirMachines() throws {
        try launch(grouping: "space")
        XCTAssertTrue(heading("服务器").waitForExistence(timeout: 15), "服务器's heading in 按项目 too")
        let weather = header("weather")
        app.bringAboveTabBar(weather)
        XCTAssertLessThan(heading("MacBook").frame.minY, weather.frame.minY, "the space under its machine")
        XCTAssertFalse(weather.label.contains("MacBook"), "a space under its machine does not name it: \(weather.label)")
    }

    @MainActor func testOneMachineShownHasNoHeading() throws {
        try launch(machine: "macbook")
        XCTAssertTrue(header("weather").waitForExistence(timeout: 15), "weather's header")
        XCTAssertFalse(app.buttons.matching(identifier: "agents.host").firstMatch.exists, "no machine heading for one machine")
    }
}
