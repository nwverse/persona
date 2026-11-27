import XCTest

/// Native Agent console (#17129): three tabs; external conversations live inside Agents, not a fourth tab.
@MainActor final class AgentConsoleUITests: XCTestCase {
    func testThreeTabsAndExternalInsideAgents() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-demoMode", "YES", "-persona.ios.console.enabled", "YES", "-tab", "assistant"]
        app.launch()

        XCTAssertTrue(app.tabBars.buttons["Main Chat"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.tabBars.buttons["Agents"].exists)
        XCTAssertTrue(app.tabBars.buttons["Settings"].exists)
        XCTAssertFalse(app.tabBars.buttons["External"].exists)

        app.tabBars.buttons["Agents"].tap()
        let segments = app.segmentedControls["Agent conversations"]
        XCTAssertTrue(segments.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(segments.buttons["Running"].exists)
        XCTAssertTrue(segments.buttons["External"].exists)
        segments.buttons["External"].tap()
        XCTAssertTrue(segments.buttons["External"].isSelected)

        app.tabBars.buttons["Settings"].tap()
        XCTAssertTrue(app.buttons["settings-templates"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.buttons["settings-more"].exists)
    }

    func testDefaultDoesNotReplaceTheExistingShell() {
        let app = XCUIApplication()
        app.launchArguments = ["-demoMode", "YES", "-persona.ios.console.enabled", "NO", "-tab", "assistant"]
        app.launch()
        XCTAssertFalse(app.tabBars.buttons["Main Chat"].waitForExistence(timeout: 3))
    }
}
