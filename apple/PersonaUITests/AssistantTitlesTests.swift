import XCTest

/// The assistant's conversations titled by the hub 「MMDD | 类型 | 主题」 (docs/assistant/assistant.md「Chat titles」) in the drawer,
/// a topic too long for it cut at the end, and Rename… in a conversation's long-press menu (the user's title is then
/// the hub's for good). Against scripts/mock/mock_hub.py with MOCK_TITLES=1 (PERSONA_MOCK_HUB), which records renames at
/// /mock/renamed.
final class AssistantTitlesTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-newAssistant", "YES"]
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/persona-titles-\(name)\(suffix).png"))
    }

    /// A drawer row, found by the start of its label (title, then its state and preview).
    @MainActor private func row(_ app: XCUIApplication, _ title: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", title)).firstMatch
    }

    @MainActor private func openDrawer(_ app: XCUIApplication) {
        let menu = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 15), "no ☰:\n\(app.debugDescription)")
        menu.tapCenter()
        XCTAssertTrue(row(app, "1029 | 定时 | 早报").waitForExistence(timeout: 5), "no titled conversation in the drawer:\n\(app.debugDescription)")
    }

    @MainActor func testTitlesInTheShellsDrawerAndRename() throws {
        let app = launch()
        openDrawer(app)
        XCTAssertTrue(row(app, "1028 | 研究 | 这个季度读书笔记的主题和书单整理").exists, "the long one")
        shot(app, "shell")

        let dinner = row(app, "1031 | 功能 | 约周五和韩梅梅吃饭")
        XCTAssertTrue(dinner.exists)
        dinner.press(forDuration: 1.0)
        let rename = app.buttons["重命名…"].firstMatch
        XCTAssertTrue(rename.waitForExistence(timeout: 5), "no 重命名… in the menu:\n\(app.debugDescription)")
        rename.tap()
        let field = app.textFields["会话名"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no title field:\n\(app.debugDescription)")
        // The cursor at the end (a tap may land anywhere in the old title), then clear it and type.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        _ = app.keyboards.firstMatch.waitForExistence(timeout: 3)
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 40) + "周五饭局")
        shot(app, "rename")
        // The alert moves up with the keyboard: tap Save once it has settled, again if the tap was lost.
        sleep(1)
        for _ in 0..<3 where app.alerts.firstMatch.exists {
            app.alerts.buttons["保存"].tap()
            _ = app.alerts.firstMatch.waitForNonExistence(timeout: 2)
        }
        XCTAssertFalse(app.alerts.firstMatch.exists, "the alert is still up")
        let deadline = Date().addingTimeInterval(5)
        while try renamed().isEmpty, Date() < deadline { usleep(200_000) }
        let r = try renamed().last
        XCTAssertEqual(r?["title"] as? String, "周五饭局", "the hub did not get the new title")
        XCTAssertEqual(r?["session_id"] as? String, "b2e4d6f8-0000-4000-8000-000000000003")
        openDrawerAgain(app)
        XCTAssertTrue(row(app, "周五饭局").waitForExistence(timeout: 5), "the drawer doesn't show the new title:\n\(app.debugDescription)")
    }

    /// The drawer closes behind the alert; open it again if it did.
    @MainActor private func openDrawerAgain(_ app: XCUIApplication) {
        if !row(app, "1029 | 定时 | 早报").exists { app.buttons["assistant-sidebar-button"].firstMatch.tapCenter() }
    }

    private func renamed() throws -> [[String: Any]] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/renamed")!)
        return (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["renamed"] as? [[String: Any]] ?? []
    }
}
