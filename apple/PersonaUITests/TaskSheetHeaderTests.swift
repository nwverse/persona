import XCTest

/// A task opens the ordinary Agent header and controls. A multi-Agent task opens its most pressing Agent.
/// Against scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_MANAGERS=1.
final class TaskSheetHeaderTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_MANAGERS=1 (PERSONA_MOCK_HUB)")
        }
        try MockHubRoster.requireOneAssistant(hub, lacks: "the app lists only the current one's sessions, so the task's agent "
                                              + "on the MacBook has no available chat")
        self.hub = hub
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/task-sheet-\(appearance)-\(name).png"))
    }

    @MainActor func testTheTaskOpensItsAgentsOwnHeader() throws {
        let multi = ProcessInfo.processInfo.environment["PERSONA_TASK_MULTI"] == "1"
        let app = XCUIApplication()
        app.launchEnvironment["PERSONA_UI_TEST_SESSION"] = "1"
        let url = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", url, "-holdChromeOpen", "YES", "-tab", "assistant", "-dev.openChat", "",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"]
        app.launchPinned()
        app.openActionTray(multi ? "needs-you" : "running")
        let task = app.trayTasks.matching(NSPredicate(format: "label CONTAINS %@", "修小组件横屏崩溃")).firstMatch
        XCTAssertTrue(task.waitForExistence(timeout: 20), app.debugDescription)
        task.tap()
        let title = app.buttons["chat.title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        let expected = multi ? "macbook/a1f3c2d4-0000-4000-8000-000000000002" : "macbook/a1f3c2d4-0000-4000-8000-000000000001"
        let loaded = NSPredicate(format: "value == %@", expected)
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: loaded, object: title)], timeout: 10), .completed,
                       "The Agent header never named the requested session: \(app.debugDescription)")
        XCTAssertTrue(app.buttons["task-handoff.return"].exists)
        XCTAssertFalse(app.buttons["task-detail.fullscreen"].exists)
        XCTAssertTrue(app.buttons["返回"].firstMatch.exists)
        try shot(app, multi ? "urgent-agent" : "agent")
    }
}
