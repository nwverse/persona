import XCTest

/// A new conversation's first message whose start failed (#1368) stays on the page as the chat's failed bubble, with the
/// reason, Resend and Delete, and the composer stays empty. Against scripts/mock/mock_hub.py (TEST_RUNNER_PERSONA_MOCK_HUB),
/// whose `GET /mock/start-fail` cuts the next start off; screenshots in PERSONA_SHOTS, PERSONA_SHOTS_SUFFIX appended.
final class FirstMessageFailedTests: XCTestCase {
    private var app: XCUIApplication!
    private let words = "帮我规划一下周末的行程" // i18n-ignore

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

    private var hub: String? { ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] }

    private func shot(_ name: String) {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        try? XCUIScreen.main.screenshot().pngRepresentation
            .write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name)\(env["PERSONA_SHOTS_SUFFIX"] ?? "").png"))
    }

    /// Sets the mock's next starts (`GET /mock/start-fail`).
    private func mock(_ query: String) throws {
        let url = try XCTUnwrap(URL(string: try XCTUnwrap(hub) + "/mock/start-fail?" + query))
        let done = expectation(description: "mock")
        URLSession.shared.dataTask(with: url) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
    }

    /// The shell's new conversation, its first message sent while the mock cuts the start off.
    @MainActor private func sendAndFail() throws {
        guard let hub else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        try mock("n=1&delay=0")
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                               "-dev.openChat", "", "-newAssistant", "YES", "-showHomeState", "empty"]
        app.launchPinned()
        let field = app.textFields["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 30), app.debugDescription)
        field.tap()
        field.typeText(words)
        app.buttons["shell-composer-send"].tap()
        XCTAssertTrue(failedMark.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts[words].exists, "the words stay on the page")
        XCTAssertFalse((field.value as? String ?? "").contains(words), "the composer stays empty")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "可能已经发出去了")).firstMatch.exists, // i18n-ignore
                      "the reason over the composer")
    }

    private var failedMark: XCUIElement { app.buttons["发送失败，重发或删除"].firstMatch } // i18n-ignore

    @MainActor func testResendStartsTheConversationOnce() throws {
        try sendAndFail()
        shot("failed")
        failedMark.tap()
        let resend = app.buttons["重发"].firstMatch // i18n-ignore
        XCTAssertTrue(resend.waitForExistence(timeout: 5))
        shot("menu")
        try mock("n=0&delay=4") // long enough to see it spin up again
        resend.tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label ENDSWITH %@", "正在准备…")).firstMatch.waitForExistence(timeout: 3), // i18n-ignore
                      "spinning up again")
        XCTAssertFalse(failedMark.exists)
        shot("resending")
        XCTAssertTrue(failedMark.waitForNonExistence(timeout: 15))
        // The conversation opened: the welcome and the spinning-up line are gone, the words are there once.
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label ENDSWITH %@", "正在准备…")).firstMatch.waitForNonExistence(timeout: 15)) // i18n-ignore
        sleep(2)
        XCTAssertEqual(app.staticTexts.matching(NSPredicate(format: "label == %@", words)).count, 1, "shown once")
        shot("resent")
        try mock("n=0&delay=0")
    }

    @MainActor func testDeleteLeavesAnEmptyNewConversation() throws {
        try sendAndFail()
        failedMark.tap()
        let delete = app.buttons["删除"].firstMatch // i18n-ignore
        XCTAssertTrue(delete.waitForExistence(timeout: 5))
        delete.tap()
        XCTAssertTrue(failedMark.waitForNonExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts[words].exists, "gone")
        XCTAssertTrue(app.descendants(matching: .any)["assistant-welcome"].firstMatch.waitForExistence(timeout: 5), "the welcome again")
        shot("deleted")
    }
}
