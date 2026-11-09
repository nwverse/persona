import XCTest

/// Settings › Codex Settings, under Claude Code Settings: the node's Codex options with their values. Against scripts/mock/mock_hub.py
/// (PERSONA_MOCK_HUB).
final class CodexSettingsTests: XCTestCase {
    @MainActor func testCodexSettingsOpen() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", ""]
        app.launchPinned()
        let row = app.buttons["Codex 设置"]
        XCTAssertTrue(row.waitForExistence(timeout: 15), "no Codex 设置 row:\n\(app.debugDescription)")
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/codex-list.png"))
        row.tap()
        XCTAssertTrue(app.staticTexts["什么时候问你"].waitForExistence(timeout: 10), "no Codex options:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["思考强度"].exists)
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/codex-page.png"))
    }
}
