import XCTest

/// The composer is laid out apart from the page (`LayoutIsland`): typing must still grow it line by line, keep it over
/// the keyboard, and the page must make room for it. Against scripts/mock/mock_hub.py (TEST_RUNNER_PERSONA_MOCK_HUB=…);
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps screenshots of one line and several.
final class ComposerGrowTests: XCTestCase {
    @MainActor func testTypingGrowsTheComposerOverTheKeyboard() throws {
        try grows()
    }

    @MainActor private func grows() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant"]
        app.launchPinned()
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 15), "no composer:\n\(app.debugDescription)")
        field.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard")
        sleep(1)
        // Drafts are kept across runs: start empty.
        if let v = field.value as? String, !v.isEmpty, v != "问 Persona", !v.hasPrefix("问 ") {
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: v.count + 2))
        }
        field.typeText("一行")
        sleep(1)
        let one = field.frame
        shot(app, "composer-1")
        field.typeText("，接着写长一点，让它折到第二行、第三行，看输入框会不会跟着长高，而且一直在键盘上面")
        sleep(1)
        let many = field.frame
        shot(app, "composer-3")
        XCTAssertGreaterThan(many.height, one.height + 20, "the composer grows with its text (one line \(one), more \(many))")
        let keyboard = app.keyboards.firstMatch.frame
        XCTAssertLessThanOrEqual(many.maxY, keyboard.minY + 1, "the composer stays over the keyboard (\(many) vs \(keyboard))")
        // Back to one line: it shrinks again.
        let typed = (field.value as? String) ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: typed.count + 2))
        sleep(1)
        XCTAssertLessThan(field.frame.height, many.height - 20, "the composer shrinks when the text goes (\(field.frame))")
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + ".png"))
    }
}
