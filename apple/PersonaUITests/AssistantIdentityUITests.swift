import XCTest

final class AssistantIdentityUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    @MainActor private func launch(_ hubKey: String) throws -> XCUIApplication {
        let hub = try XCTUnwrap(env[hubKey], "Provide an isolated mock hub for this roster size")
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000002", "-dev.openChat", "", "-openAssistantSidebar", "NO"]
        app.launchPinned()
        XCTAssertTrue(app.buttons["assistant-sidebar-button"].waitForExistence(timeout: 20), app.debugDescription)
        return app
    }

    @MainActor func testSingleAssistantKeepsNavigationWithoutIdentity() throws {
        let app = try launch("PERSONA_IDENTITY_SINGLE_HUB")
        let sidebar = app.buttons["assistant-sidebar-button"]
        XCTAssertFalse((sidebar.value as? String ?? "").contains("Persona"))
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "manager-switcher").firstMatch.exists)
        XCTAssertFalse(app.buttons["persona-avatar"].exists)
        sidebar.tap()
        XCTAssertTrue(app.drawerSettingsButton.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.drawerNewChatButton.exists)
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "assistant-identity-heading").firstMatch.exists)
    }

    @MainActor func testMultipleAssistantsKeepIdentityAndSwitching() throws {
        let app = try launch("PERSONA_IDENTITY_MULTIPLE_HUB")
        let switcher = app.descendants(matching: .any).matching(identifier: "manager-switcher").firstMatch
        XCTAssertTrue(switcher.waitForExistence(timeout: 20), app.debugDescription)
        app.buttons["manager-switch-m-3fa21c"].tap()
        XCTAssertTrue(app.buttons["manager-switch-m-8b04d2"].exists)
        app.buttons["assistant-sidebar-button"].tap()
        XCTAssertTrue(app.drawerSettingsButton.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "assistant-identity-heading").firstMatch.exists)
    }
}
