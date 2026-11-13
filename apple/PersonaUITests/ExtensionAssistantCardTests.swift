import XCTest

/// Checks the unified renderer in a real conversation: persona-view and the old persona-card are both read-only, links still work.
/// With MOCK_EXTENSION_CARDS=1 the screenshots have both kinds of card and a card of code-hosting components; no new DEBUG
/// launch switch.
final class ExtensionAssistantCardTests: XCTestCase {
    @MainActor func testCardsShareTheRendererAndStayReadOnly() throws {
        let env = ProcessInfo.processInfo.environment
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("需要 MOCK_EXTENSION_CARDS=1") }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"]
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for title in ["允许", "Allow"] where springboard.buttons[title].waitForExistence(timeout: 3) {
            springboard.buttons[title].tap()
        }
        // The newest card: UI version 4's code-hosting components, drawn read-only now that cards report version 6
        // (#827); its composer's button can't send from a card, so the composer is left out.
        let header = app.staticTexts["登录页支持“记住我”"]
        XCTAssertTrue(header.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts["web/session.ts"].firstMatch.exists, app.debugDescription)
        XCTAssertFalse(app.buttons["发送"].exists, "a composer whose buttons this card can't use is left out")
        try shot(app, "assistant-code-card")
        let overview = app.staticTexts["本周概览"]
        for _ in 0..<6 where !overview.isHittable { app.swipeDown() }
        XCTAssertTrue(overview.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(app.staticTexts["今天步数"].waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(app.staticTexts["周会摘要"].exists)
        XCTAssertTrue(app.descendants(matching: .any)["plugin-ui-link"].exists || app.staticTexts["查看说明"].exists,
                      "只读卡片的链接保留：\n\(app.debugDescription)")
        // An action with no reply, open or push does nothing in a card: drawn, never enabled.
        for button in [app.buttons["plugin-ui-action-must-not-run"], app.buttons["不应运行"]] where button.exists {
            XCTAssertFalse(button.isEnabled, button.debugDescription)
        }
        try shot(app, "assistant-cards")
    }

    /// The screen as `<name>-<style>-<appearance>.png` in PERSONA_SHOTS, when it is set.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_EXTENSIONS_APPEARANCE"] ?? "light"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir)
            .appending(path: "\(name)-\(UITestStyle.name)-\(appearance).png"))
    }
}
