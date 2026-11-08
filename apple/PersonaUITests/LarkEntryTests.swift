import XCTest

/// The connector setting exposes the native conversation list as an optional home page.
final class LarkEntryTests: XCTestCase {
    @MainActor private func launch(connector: Bool = true) throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs mock Hub") }
        continueAfterFailure = false
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", connector ? "settings" : "assistant", "-demo.on", "NO",
                               "-openConnectorFeishu", connector ? "YES" : "NO",
                               "-integrations.lark.enabled", "NO"]
        app.launchPinned()
        return app
    }

    @MainActor private func assertSwitch(_ toggle: XCUIElement, isOn: Bool) {
        let expected = NSPredicate(format: "value == %@", isOn ? "1" : "0")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: expected, object: toggle)], timeout: 5), .completed)
    }

    @MainActor private func closeConnector(_ app: XCUIApplication) {
        app.navigationBars["飞书"].buttons["BackButton"].tapCenter()
        XCTAssertTrue(app.buttons["settings-close"].waitForExistence(timeout: 8))
        app.buttons["settings-close"].tapCenter()
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }

    @MainActor func testConnectorSwitchExposesMessagesAndReturnsFromChat() throws {
        let app = try launch()
        let toggle = app.switches["feishu-home-enabled"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20))
        XCTAssertTrue(app.staticTexts["已连接"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.secureTextFields.count, 0)
        assertSwitch(toggle, isOn: false)
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        assertSwitch(toggle, isOn: true)
        shot(app, "native-feishu-settings")
        closeConnector(app)
        let messages = app.buttons.matching(identifier: "shell-feishu").firstMatch
        XCTAssertTrue(messages.waitForExistence(timeout: 10))
        XCTAssertEqual(messages.label, "消息")
        messages.tapCenter()
        let chat = app.buttons["feishu-chat-f2"]
        XCTAssertTrue(chat.waitForExistence(timeout: 15))
        shot(app, "native-feishu-list")
        chat.tapCenter()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "The new icon set")).firstMatch.waitForExistence(timeout: 15))
        shot(app, "native-feishu-chat")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.01, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)))
        XCTAssertTrue(chat.waitForExistence(timeout: 8), "chat returns to Messages, not settings or the inbox")
        app.buttons.matching(identifier: "shell-agents").firstMatch.tapCenter()
        XCTAssertTrue(app.buttons["agents-new"].waitForExistence(timeout: 10))
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.15, dy: 0.5)))
        XCTAssertTrue(chat.waitForExistence(timeout: 10), "Agents swipes to the third page")
        // Keep the gesture horizontal on the row: ending at the window midpoint can instead scroll vertically.
        // Starting away from the screen edge must still page back to Agents.
        chat.coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: chat.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)))
        XCTAssertTrue(app.buttons["agents-new"].waitForExistence(timeout: 10), "Messages rows do not inherit Agents swipe actions")
        messages.tapCenter()
        XCTAssertTrue(chat.waitForExistence(timeout: 10))
        app.buttons["feishu-settings"].tapCenter()
        XCTAssertTrue(toggle.waitForExistence(timeout: 8))
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        assertSwitch(toggle, isOn: false)
        XCTAssertTrue(app.navigationBars["飞书"].exists, "turning Messages off leaves settings open")
        closeConnector(app)
        XCTAssertTrue(messages.waitForNonExistence(timeout: 8))
        XCTAssertTrue(app.buttons.matching(identifier: "shell-assistant").firstMatch.exists)
        shot(app, "native-feishu-disabled")
        app.terminate()
    }

    @MainActor func testMessagesPreferenceSurvivesRestart() throws {
        let app = try launch()
        let toggle = app.switches["feishu-home-enabled"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20))
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        app.terminate()
        if let index = app.launchArguments.firstIndex(of: "-integrations.lark.enabled") {
            app.launchArguments.removeSubrange(index...index + 1)
        }
        if let index = app.launchArguments.firstIndex(of: "-tab") { app.launchArguments[index + 1] = "assistant" }
        if let index = app.launchArguments.firstIndex(of: "-openConnectorFeishu") { app.launchArguments[index + 1] = "NO" }
        app.launchPinned()
        let messages = app.buttons.matching(identifier: "shell-feishu").firstMatch
        XCTAssertTrue(messages.waitForExistence(timeout: 20))
        messages.tapCenter()
        XCTAssertTrue(app.buttons["feishu-chat-f2"].waitForExistence(timeout: 15))
        app.buttons["feishu-settings"].tapCenter()
        XCTAssertTrue(toggle.waitForExistence(timeout: 8))
        assertSwitch(toggle, isOn: true)
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        app.terminate()
        app.launchPinned()
        XCTAssertTrue(app.buttons.matching(identifier: "shell-assistant").firstMatch.waitForExistence(timeout: 20))
        XCTAssertFalse(messages.exists)
        app.terminate()
    }

    @MainActor func testExistingShellKeepsItsTwoPages() throws {
        let app = try launch(connector: false)
        XCTAssertTrue(app.buttons.matching(identifier: "shell-assistant").firstMatch.waitForExistence(timeout: 20))
        XCTAssertTrue(app.buttons.matching(identifier: "shell-agents").firstMatch.exists)
        XCTAssertFalse(app.buttons["shell-feishu"].exists)
        app.terminate()
    }
}
