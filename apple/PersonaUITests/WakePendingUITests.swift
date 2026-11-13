import XCTest

/// A pending wake with tools but no new answer must not erase the previous answer from a non-live chat (#3169).
/// Run with MOCK_WAKES=1 MOCK_WAKE_PENDING=1 and TEST_RUNNER_PERSONA_MOCK_HUB.
final class WakePendingUITests: XCTestCase {
    private var hub = ""

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = false
    }

    @MainActor func testPreviousAnswerStaysVisibleAndHittable() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/a1f3c2d4-0000-4000-8000-00000000000b"]
        app.launchPinned()
        defer { app.terminate() }
        let answer = app.descendants(matching: .any)
            .matching(NSPredicate(format: "label CONTAINS %@", "测试已经在后台跑了，结束后我告诉你。"))
            .firstMatch
        XCTAssertTrue(answer.waitForExistence(timeout: 20) && answer.isHittable,
                      "the previous answer disappeared before the wake's new words arrived:\n\(app.debugDescription)")
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "wake-pending"
        attachment.lifetime = .keepAlways
        add(attachment)
        if let directory = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let appearance = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
            try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(directory)/wake-pending-\(appearance).png"))
        }
    }
}
