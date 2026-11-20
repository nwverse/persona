import XCTest

/// The system's edge swipe returns from a task's Agent to its original Assistant conversation.
final class TaskSheetEdgeSwipeUITests: XCTestCase {
    private var hub = ""
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = false
    }

    @MainActor func testEdgeSwipeReturnsFromTaskAgent() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-dev.openChat", "", "-openAssistant", main]
        app.launchPinned()
        app.openActionTray("running")
        let row = app.trayTasks.matching(NSPredicate(format: "label CONTAINS %@", "修小组件横屏崩溃")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 20), app.debugDescription)
        row.tap()
        let hint = app.buttons["task-handoff.return"]
        XCTAssertTrue(hint.waitForExistence(timeout: 10))
        let start = app.coordinate(withNormalizedOffset: CGVector(dx: 0.01, dy: 0.5))
        start.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.5)))
        let running = app.buttons["action-bar.running"]
        let returned = NSPredicate { _, _ in running.exists && running.isHittable && !hint.isHittable }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: returned, object: app)], timeout: 10), .completed,
                       "The edge swipe did not return to the conversation: \(app.debugDescription)")
    }
}
