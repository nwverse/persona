import XCTest

/// Drives only the dedicated loopback fixture, never the user's assistant conversation.
final class AssistantRichCardTests: XCTestCase {
    @MainActor private var app: XCUIApplication!
    override func setUpWithError() throws { continueAfterFailure = false }

    private func hub() throws -> URL {
        guard let raw = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"], let url = URL(string: raw),
              url.scheme == "http", url.host == "127.0.0.1" else { throw XCTSkip("needs assistant_rich_ui_hub.py") }
        return url
    }
    private func post(_ path: String, body: [String: Bool] = [:]) async throws {
        var request = URLRequest(url: try hub().appendingPathComponent(path))
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (_, response) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
    }
    private func status() async throws -> [String: Any] {
        let (data, _) = try await URLSession.shared.data(from: try hub().appendingPathComponent("mock/rich/status"))
        let object = try JSONSerialization.jsonObject(with: data)
        return try XCTUnwrap(object as? [String: Any])
    }
    private func expectStatus(_ field: String, _ value: Int) async throws {
        let values = try await status()
        XCTAssertEqual((values[field] as? NSNumber)?.intValue, value, "fixture status fields: \(values.keys.sorted())")
    }
    @MainActor private func launch() throws {
        app = XCUIApplication()
        let url = try hub()
        let paired = "persona://pair?url=" + url.absoluteString.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", paired, "-openChatsFresh", "YES", "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001", "-app.tab", "assistant"]
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        if allow.waitForExistence(timeout: 3) { allow.tap() }
    }
    @MainActor private func shot(_ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "rich card \(name) \(UITestStyle.name)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
    @MainActor private func reachHTML(_ button: XCUIElement) {
        let scroll = app.scrollViews.firstMatch
        for _ in 0..<8 {
            let viewport = scroll.frame
            let top = max(viewport.minY, app.buttons["shell-assistant"].frame.maxY)
            if button.exists, button.isHittable, button.frame.minY >= top, button.frame.maxY <= viewport.maxY { return }
            if button.exists, button.frame.minY >= viewport.maxY { scroll.swipeUp() }
            else { scroll.swipeDown() }
        }
        let viewport = scroll.frame
        let top = max(viewport.minY, app.buttons["shell-assistant"].frame.maxY)
        XCTAssertTrue(button.exists && button.isHittable && button.frame.minY >= top && button.frame.maxY <= viewport.maxY,
                      "the HTML button stayed outside the unobstructed conversation viewport")
    }
    @MainActor func testAllSharedSamplesAreVisibleAndCaptured() async throws {
        for sample in ["decision", "compare", "progress", "form", "chart", "html"] {
            try await post("mock/rich/sample/" + sample)
            try launch()
            let card = app.otherElements["assistant-view-card"]
            XCTAssertTrue(card.waitForExistence(timeout: 30), "shared sample \(sample) did not render")
            if sample == "html" {
                let button = app.webViews.buttons["继续讨论"]
                XCTAssertTrue(button.waitForExistence(timeout: 30), "the HTML sample was captured before its page became ready")
                reachHTML(button)
            }
            XCTAssertFalse(app.otherElements["chat-empty-placeholder"].exists, "a feed-only card is not an empty conversation")
            XCTAssertFalse(app.staticTexts["还没有消息"].exists)
            XCTAssertFalse(app.otherElements["chat-loading-placeholder"].exists, "a rendered card must replace the transcript skeleton")
            shot("sample-" + sample)
            app.terminate()
        }
    }

    @MainActor func testAReallyEmptyConversationStillShowsTheEmptyPrompt() async throws {
        try await post("mock/rich/empty")
        try launch()
        defer { app.terminate() }
        XCTAssertTrue(app.staticTexts["还没有消息"].waitForExistence(timeout: 30))
        XCTAssertFalse(app.otherElements["assistant-view-card"].exists)
        XCTAssertFalse(app.otherElements["chat-loading-placeholder"].exists)
        shot("empty-control")
    }

    @MainActor func testHTMLReplyUsesNativeConfirmationAndTheSharedAnswerLock() async throws {
        try await post("mock/rich/sample/html")
        try launch()
        defer { app.terminate() }
        let button = app.webViews.buttons["继续讨论"]
        XCTAssertTrue(button.waitForExistence(timeout: 30), "the allowed HTML sample never entered its sandbox")
        reachHTML(button)
        shot("html-page-ready")
        button.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 5))
        XCTAssertTrue(alert.staticTexts["继续讨论这个方案"].exists)
        shot("html-native-confirmation")
        alert.buttons["发送"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["assistant-view-answer"].waitForExistence(timeout: 10))
        try await expectStatus("answers", 1)
        // Sending scrolls the feed to the user message and releases offscreen HTML; return to the same card first.
        for _ in 0..<6 {
            if button.exists { break }
            app.swipeDown()
        }
        XCTAssertTrue(button.waitForExistence(timeout: 10), "the answered card did not resume when brought back onscreen")
        reachHTML(button)
        button.tap()
        XCTAssertFalse(app.alerts.firstMatch.exists)
        try await expectStatus("posts", 1)
        shot("html-answered")
    }

    @MainActor func testFormConfirmationSendsOnceAndRestoresTheSavedValues() async throws {
        try await post("mock/rich/sample/form")
        try launch()
        defer { app.terminate() }
        let field = app.textFields["plugin-ui-field-task"]
        XCTAssertTrue(field.waitForExistence(timeout: 30))
        field.tap(); field.typeText("reading-task\n")
        let submit = app.buttons["plugin-ui-action-submit"]
        XCTAssertTrue(submit.isEnabled)
        shot("form-before-submit")
        submit.tap()
        shot("form-after-submit")
        let form = app.otherElements.matching(identifier: "assistant-view-card")
            .containing(.textField, identifier: "plugin-ui-field-task").firstMatch
        XCTAssertEqual(form.value as? String, "等待确认")
        let confirm = app.alerts.firstMatch.buttons["assistant-view-confirm"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        shot("form-confirmation")
        confirm.tap()
        XCTAssertTrue(app.staticTexts["assistant-view-answer"].waitForExistence(timeout: 10))
        XCTAssertFalse(submit.isEnabled)
        XCTAssertTrue(app.staticTexts["assistant-card-source"].waitForExistence(timeout: 10))
        try await expectStatus("answers", 1)
        shot("form-saved")
        app.terminate(); try launch()
        XCTAssertTrue(app.staticTexts["assistant-view-answer"].waitForExistence(timeout: 30))
        XCTAssertEqual(app.textFields["plugin-ui-field-task"].value as? String, "reading-task")
        XCTAssertFalse(app.buttons["plugin-ui-action-submit"].isEnabled)
        try await expectStatus("answers", 1)
        shot("form-restored")
    }
    @MainActor func testAnotherDeviceLocksAnInlineCardWithoutAnotherPOST() async throws {
        try await post("mock/rich/sample/decision", body: ["inline": true])
        try launch()
        defer { app.terminate() }
        let choice = app.buttons["plugin-ui-action-choose-b"]
        XCTAssertTrue(choice.waitForExistence(timeout: 30))
        for _ in 0..<50 {
            if (try await status()["connected"] as? Int ?? 0) > 0 { break }
            try await Task.sleep(for: .milliseconds(200))
        }
        try await post("mock/rich/remote-choice")
        XCTAssertTrue(app.staticTexts["assistant-view-answer"].waitForExistence(timeout: 10))
        XCTAssertFalse(choice.isEnabled)
        XCTAssertFalse(app.buttons["plugin-ui-action-choose-a"].isEnabled)
        try await expectStatus("posts", 0)
        shot("inline-other-device")
    }
    @MainActor func testAnUnreceivedUpdateRejectsOldConfirmationAndKeepsTheDraft() async throws {
        try await post("mock/rich/sample/form")
        try launch()
        defer { app.terminate() }
        let field = app.textFields["plugin-ui-field-task"]
        XCTAssertTrue(field.waitForExistence(timeout: 30))
        field.tap(); field.typeText("preserved-draft\n")
        app.buttons["plugin-ui-action-submit"].tap()
        XCTAssertTrue(app.alerts.firstMatch.buttons["assistant-view-confirm"].firstMatch.waitForExistence(timeout: 5))
        try await post("mock/rich/update-silent")
        app.alerts.firstMatch.buttons["assistant-view-confirm"].firstMatch.tap()
        let updated = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "已更新")).firstMatch
        XCTAssertTrue(updated.waitForExistence(timeout: 10))
        try await expectStatus("answers", 0)
        XCTAssertEqual(field.value as? String, "preserved-draft")
        app.buttons["plugin-ui-action-submit"].tap()
        XCTAssertTrue(app.alerts.firstMatch.buttons["assistant-view-confirm"].firstMatch.waitForExistence(timeout: 5))
        app.alerts.firstMatch.buttons["assistant-view-confirm"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["assistant-view-answer"].waitForExistence(timeout: 10))
        try await expectStatus("answers", 1)
        shot("version-conflict-reconfirmed")
    }
}
