import XCTest

/// Typing "/" in an agent's composer opens the list of commands over it, as on the web (#2747): it filters as more is
/// typed, and a tap on a row puts the row's text in the box without sending it. The default style's Shell composer.
/// Against scripts/mock/mock_hub.py
/// (PERSONA_MOCK_HUB).
final class SlashMenuTests: XCTestCase {
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
        app.launchArguments = ["-pairURL", pair, "-ui.style", "glass", "-tab", "dev",
                               "-openSession", "studio/a1f3c2d4-0000-4000-8000-000000000003"]
        app.launchPersona()
        return app
    }

    @MainActor func testSlashOpensTheListAndATapFillsTheBox() {
        let app = launch()
        let field = composer(app)
        XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
        field.tap()
        clear(field) // drafts are kept across runs: a "/" after old text opens no list
        field.typeText("/")
        let menu = app.descendants(matching: .any)["slash-menu"].firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 5), "no / list:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["slash-menu./compact"].exists)
        shot(app, "open")

        field.typeText("co") // filters: /compact and /context, not the quick messages
        let deadline = Date.now.addingTimeInterval(3)
        while app.buttons["slash-menu./继续"].exists, Date.now < deadline { usleep(200_000) }
        XCTAssertFalse(app.buttons["slash-menu./继续"].exists, "the list did not filter")
        XCTAssertTrue(app.buttons["slash-menu./context"].exists)

        field.typeText(XCUIKeyboardKey.delete.rawValue + XCUIKeyboardKey.delete.rawValue)
        let row = app.buttons["slash-menu./汇报"]
        XCTAssertTrue(row.waitForExistence(timeout: 5))
        row.tap()
        let picked = "简要汇报一下目前的进展、还剩什么"
        let until = Date.now.addingTimeInterval(5)
        while (field.value as? String) != picked, Date.now < until { usleep(200_000) }
        XCTAssertEqual(field.value as? String, picked, "the tap on /汇报 did not fill the box")
        XCTAssertFalse(menu.exists, "the list stayed open after a pick")
        shot(app, "picked")
        clear(field)
    }

    @MainActor func testACommandPickedStaysInTheBoxWithTheListClosed() {
        let app = launch()
        let field = composer(app)
        XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
        field.tap()
        clear(field)
        field.typeText("/con")
        let row = app.buttons["slash-menu./context"]
        XCTAssertTrue(row.waitForExistence(timeout: 5), "no / list:\n\(app.debugDescription)")
        row.tap()
        let until = Date.now.addingTimeInterval(5)
        while (field.value as? String) != "/context", Date.now < until { usleep(200_000) }
        XCTAssertEqual(field.value as? String, "/context")
        // "/context" is still a lone "/" word: the list must not open again for the draft it was picked into.
        XCTAssertFalse(app.descendants(matching: .any)["slash-menu"].firstMatch.exists, "the list opened again on the picked command")
        clear(field)
    }

    @MainActor private func composer(_ app: XCUIApplication) -> XCUIElement {
        let shell = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        if shell.waitForExistence(timeout: 10) { return shell }
        return app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
    }

    /// Empties the draft: a tap at the field's end puts the caret there (a pick leaves it elsewhere), then deletes.
    @MainActor private func clear(_ field: XCUIElement) {
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        guard let v = field.value as? String, !v.isEmpty, v != field.placeholderValue else { return }
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: v.count + 2))
    }

    /// `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps a screenshot of each step.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("slash-glass-\(name).png"))
    }
}
