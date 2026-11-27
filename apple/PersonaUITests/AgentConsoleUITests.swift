import XCTest

/// Native Agent console (#17129): three tabs; external conversations live inside Agents, not a fourth tab.
@MainActor final class AgentConsoleUITests: XCTestCase {
    func testThreeTabsAndExternalInsideAgents() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-persona.ios.console.enabled", "YES", "-tab", "assistant"]
        app.launch()

        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 0).waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 1).exists)
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 2).exists)
        XCTAssertEqual(app.tabBars.buttons.count, 3)

        app.tabBars.buttons.element(boundBy: 1).tap()
        let segments = app.segmentedControls.firstMatch
        XCTAssertTrue(segments.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(segments.buttons.element(boundBy: 0).exists)
        XCTAssertTrue(segments.buttons.element(boundBy: 1).exists)
        segments.buttons.element(boundBy: 1).tap()
        XCTAssertTrue(segments.buttons.element(boundBy: 1).isSelected)

        app.tabBars.buttons.element(boundBy: 2).tap()
        XCTAssertTrue(app.buttons["settings-templates"].waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.buttons["settings-more"].exists)
    }

    func testAppliedConsoleSurvivesTerminationAndRelaunch() {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-persona.ios.console.enabled", "YES", "-tab", "assistant"]
        app.launch()
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 0).waitForExistence(timeout: 15), app.debugDescription)
        app.tabBars.buttons.element(boundBy: 1).tap()
        XCTAssertTrue(app.segmentedControls["Agent conversations"].waitForExistence(timeout: 15), app.debugDescription)
        app.terminate()
        app.launchArguments = ["-demo.on", "YES"]
        app.launch()
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 0).waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 1).exists)
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 2).exists)
    }

    func testDefaultDoesNotReplaceTheExistingShell() {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-persona.ios.console.enabled", "NO", "-tab", "assistant"]
        app.launch()
        XCTAssertFalse(app.segmentedControls.firstMatch.waitForExistence(timeout: 3))
    }
}
