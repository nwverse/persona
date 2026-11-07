import XCTest

/// The real package picker against mock_hub.py with MOCK_PERSONAS=demo,builtin.
final class PersonaPickerTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs PERSONA_MOCK_HUB with MOCK_PERSONAS=demo,builtin")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ selected: String? = "persona") -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", "persona",
                               "-ui.scheme", ProcessInfo.processInfo.environment["PERSONA_LOOK_APPEARANCE"] ?? "light"]
        if let selected { app.launchArguments += ["-openPersona", selected] }
        app.launchPinned()
        let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let deny = board.alerts.buttons.matching(NSPredicate(format: "label IN %@", ["不允许", "Don’t Allow", "Don't Allow"])).firstMatch
        if deny.waitForExistence(timeout: 3) { deny.tap() }
        acceptIconAlert(timeout: 1)
        if !app.buttons["persona-persona"].waitForExistence(timeout: 3) {
            app.terminate()
            app.launchPinned()
        }
        XCTAssertTrue(app.buttons["persona-nwverse"].waitForExistence(timeout: 20), "Milk Frog was not listed")
        XCTAssertTrue(app.buttons["persona-persona"].exists)
        XCTAssertTrue(app.buttons["persona-demo"].waitForExistence(timeout: 20), "Custom package was not listed")
        return app
    }

    @MainActor private func waitSelected(_ e: XCUIElement) {
        let selected = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: e)
        XCTAssertEqual(XCTWaiter.wait(for: [selected], timeout: 20), .completed)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_LOOK_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + ".png"))
        }
    }

    @MainActor func testHubPackagesCanBeChosenAndPersistAcrossLaunches() {
        let app = launch()
        defer { app.terminate() }
        waitSelected(app.buttons["persona-persona"])
        for id in ["persona-visor", "persona-circuit", "persona-pixel", "persona-facet", "persona-blob"] {
            XCTAssertFalse(app.buttons["persona-\(id)"].exists, "removed persona is still offered: \(id)")
        }
        XCTAssertTrue(app.buttons["persona-persona"].label.contains("团子"))
        XCTAssertTrue(app.buttons["persona-nwverse"].label.contains("奶蛙"))
        shot(app, "ios-packages-\(ProcessInfo.processInfo.environment["PERSONA_LOOK_APPEARANCE"] ?? "light")")
        app.buttons["persona-nwverse"].tap()
        waitSelected(app.buttons["persona-nwverse"])
        app.buttons["persona-demo"].tap()
        waitSelected(app.buttons["persona-demo"])
        shot(app, "ios-custom-package-\(ProcessInfo.processInfo.environment["PERSONA_LOOK_APPEARANCE"] ?? "light")")
        app.terminate()
        let relaunched = launch(nil)
        waitSelected(relaunched.buttons["persona-demo"])
        relaunched.buttons["persona-persona"].tap()
        waitSelected(relaunched.buttons["persona-persona"])
    }

    @MainActor func testDeletedSelectionOpensOnMochi() {
        let app = launch("persona-visor")
        defer { app.terminate() }
        waitSelected(app.buttons["persona-persona"])
        XCTAssertFalse(app.buttons["persona-persona-visor"].exists)
        XCTAssertTrue(app.buttons["persona-nwverse"].exists)
    }
}
