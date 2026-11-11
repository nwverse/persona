import XCTest

/// The queue strip over an agent chat's composer at the largest accessibility text size (#4846): the title has the row's
/// width and a tap on it opens the strip; Send Now sits on the line under it and still asks before sending. At the
/// default size the two share one line, as before. Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB): the widget agent
/// works, a Claude in a terminal, so its queue has Send Now; `/mock/queue?text=…` queues a message for it.
/// PERSONA_SHOTS=<dir> keeps each step, named with PERSONA_APPEARANCE.
final class QueueStripTextSizeUITests: XCTestCase {
    private var hub = ""
    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        continueAfterFailure = true
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        hub = h
        _ = mock("queue?clear=1")
        _ = mock("queue?text=" + "Check the widget in dark mode when you are done".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!)
    }

    override func tearDownWithError() throws {
        _ = mock("queue?clear=1")
    }

    @MainActor func testTheTitleOpensTheStripAtTheLargestTextSize() throws {
        let app = launch("UICTContentSizeCategoryAccessibilityXXXL")
        let (strip, title, send) = parts(app)
        shot(app, "xxxl-queued")
        XCTAssertGreaterThan(title.frame.width, strip.frame.width / 2, "the title has no room: \(title.frame) in \(strip.frame)")
        XCTAssertGreaterThanOrEqual(send.frame.minY, title.frame.maxY - 1, "Send Now is beside the title, not under it: \(send.frame) and \(title.frame)")

        title.tap()
        XCTAssertTrue(app.buttons["queue-item"].firstMatch.waitForExistence(timeout: 5), "a tap on the title did not open the strip:\n\(app.debugDescription)")
        XCTAssertFalse(app.buttons["queue-send-now-confirm"].exists, "a tap on the title asked to send the queue")
        shot(app, "xxxl-open")

        send.tap()
        XCTAssertTrue(app.buttons["queue-send-now-confirm"].firstMatch.waitForExistence(timeout: 5), "Send Now did not ask first:\n\(app.debugDescription)")
        shot(app, "xxxl-send-now")
    }

    @MainActor func testTheTitleAndSendNowShareALineAtTheDefaultSize() throws {
        let app = launch("UICTContentSizeCategoryL")
        let (_, title, send) = parts(app)
        shot(app, "default-queued")
        XCTAssertEqual(send.frame.midY, title.frame.midY, accuracy: title.frame.height / 2, "Send Now left the title's line: \(send.frame) and \(title.frame)")

        title.tap()
        XCTAssertTrue(app.buttons["queue-item"].firstMatch.waitForExistence(timeout: 5), "a tap on the title did not open the strip:\n\(app.debugDescription)")
        shot(app, "default-open")
    }

    // MARK: - Steps

    /// The widget agent's chat at the text size `size` (a UIContentSizeCategory name).
    @MainActor private func launch(_ size: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", widget, "-UIPreferredContentSizeCategoryName", size]
        app.launchPinned()
        return app
    }

    /// The strip, its title and its Send Now.
    @MainActor private func parts(_ app: XCUIApplication) -> (XCUIElement, XCUIElement, XCUIElement) {
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 30), "no queue strip:\n\(app.debugDescription)")
        let title = strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "Claude")).firstMatch
        let send = app.buttons["queue-send-now"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 5), "no title in the strip:\n\(app.debugDescription)")
        XCTAssertTrue(send.waitForExistence(timeout: 5), "no Send Now in the strip:\n\(app.debugDescription)")
        return (strip, title, send)
    }

    // MARK: - Mock

    private func mock(_ path: String) -> Data? {
        guard let url = URL(string: hub + "/mock/" + path) else { return nil }
        return try? Data(contentsOf: url) // the mock on this Mac answers at once
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let look = ProcessInfo.processInfo.environment["PERSONA_APPEARANCE"] ?? "light"
        try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "queue-strip-\(name)-\(look).png"))
    }
}
