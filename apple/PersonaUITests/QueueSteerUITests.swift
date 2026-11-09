import XCTest

/// Steering a Claude's queue (#6088): a queued message moves down a place, then Send Now (after its confirmation)
/// gives the agent the whole queue and the strip goes. Against scripts/mock/mock_hub.py with MOCK_QUEUE=1
/// (PERSONA_MOCK_HUB): the widget agent, a Claude in a herdr pane, has two messages queued.
final class QueueSteerUITests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_QUEUE=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/persona-steer-\(name).png"))
    }

    @MainActor func testAQueuedMessageMovesDownThenTheQueueIsSentNow() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001"]
        app.launchPinned()

        let sendNow = app.buttons["queue-send-now"]
        XCTAssertTrue(sendNow.waitForExistence(timeout: 20), "no Send Now on the queue strip:\n\(app.debugDescription)")
        shot(app, "strip")

        // Open the strip, then move the first message down: the hub re-queues them in the new order.
        // The strip's id lands on its disclosure button or on a group around it, depending on the OS.
        let strip = app.descendants(matching: .any).matching(identifier: "queue-strip").firstMatch
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let first = app.buttons.matching(identifier: "queue-item").firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 5), "the strip did not open:\n\(app.debugDescription)")
        XCTAssertTrue(first.label.contains("README"), first.label)
        first.tap()
        let down = app.buttons["queue-move-down"]
        XCTAssertTrue(down.waitForExistence(timeout: 5), "no Move Down in the message's menu:\n\(app.debugDescription)")
        shot(app, "menu")
        down.tap()
        let moved = NSPredicate { _, _ in !first.label.contains("README") }
        wait(for: [XCTNSPredicateExpectation(predicate: moved, object: nil)], timeout: 10)
        XCTAssertTrue(app.buttons.matching(identifier: "queue-item").element(boundBy: 1).label.contains("README"))

        // Send Now asks first, then the queue goes to the agent and the strip with it.
        sendNow.tap()
        let confirm = app.buttons.matching(identifier: "queue-send-now-confirm").firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "no confirmation:\n\(app.debugDescription)")
        shot(app, "confirm")
        confirm.tap()
        let gone = NSPredicate(format: "exists == false")
        wait(for: [XCTNSPredicateExpectation(predicate: gone, object: strip)], timeout: 10)
        shot(app, "sent")
    }
}
