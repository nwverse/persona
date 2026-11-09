import XCTest

/// quota-relay's sidebar page 「中转账号」 (#4576) opened from the shell's drawer, on the extension's exported
/// fixture: the default view, the Kiro type picked, a search, one account's page. Against scripts/mock/mock_hub.py with
/// `MOCK_EXTENSIONS=1 MOCK_RELAY_ACCOUNTS=apple/PersonaTests/Fixtures/relay-accounts-view.json`:
/// `TEST_RUNNER_PERSONA_RELAY_ACCOUNTS_HUB=http://127.0.0.1:<port>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots,
/// named by `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` and the style.
final class RelayQuotaAccountsPageUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let look = env["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir)
            .appending(path: "relay-quota-\(name)-\(UITestStyle.name)-\(look).png"))
    }

    @MainActor func testTheAccountsPage() throws {
        try XCTSkipIf(UIDevice.current.userInterfaceIdiom == .pad, "the shell's drawer is the phone's")
        guard let hub = env["PERSONA_RELAY_ACCOUNTS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_RELAY_ACCOUNTS (PERSONA_RELAY_ACCOUNTS_HUB)") }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-dev.openChat", ""]
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }

        let face = app.buttons["assistant-sidebar-button"]
        XCTAssertTrue(face.waitForExistence(timeout: 20), app.debugDescription)
        face.tap()
        let entry = app.descendants(matching: .any)["sidebar-extension-quota-relay-sidebar"]
        XCTAssertTrue(entry.waitForExistence(timeout: 20), "the page is not in the drawer:\n\(app.debugDescription)")
        entry.tap()

        // Default: the type picker, the stats, one row per account.
        let picker = app.descendants(matching: .any)["plugin-ui-segmented-type"]
        XCTAssertTrue(picker.waitForExistence(timeout: 15), "the page did not open:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["Claude #000003"].waitForExistence(timeout: 5), "the first account row")
        shot(app, "default")

        // Kiro: the picker sends {type: kiro}, the page lists the Kiro accounts.
        let kiro = picker.buttons["Kiro 61"].exists ? picker.buttons["Kiro 61"] : app.buttons["Kiro 61"]
        XCTAssertTrue(kiro.waitForExistence(timeout: 5), "the Kiro choice:\n\(app.debugDescription)")
        kiro.tap()
        XCTAssertTrue(app.staticTexts["Kiro · Free · 60"].waitForExistence(timeout: 10), "the Kiro view:\n\(app.debugDescription)")
        shot(app, "kiro")

        // Search: what is typed goes to the extension; the text stays in the field once the page answers.
        // The page's `search` is the navigation bar's search field (`.pageSearchable`), not a card's search row.
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "the search field:\n\(app.debugDescription)")
        field.tap()
        field.typeText("kiro-4\n")
        XCTAssertEqual(field.value as? String, "kiro-4", "the search text")
        shot(app, "search")

        // An account: its row pushes the account's page.
        let row = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'plugin-ui-action-account:'")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 5), "an account row:\n\(app.debugDescription)")
        row.tap()
        XCTAssertTrue(app.staticTexts["额度"].waitForExistence(timeout: 10) || app.staticTexts["详情"].exists,
                      "the account page:\n\(app.debugDescription)")
        shot(app, "detail")
    }
}
