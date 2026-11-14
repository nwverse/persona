import XCTest

/// Settings › Appearance › Sidebar (#4206), at regular width on its side (the iPad; the open iPhone Duo with
/// TEST_RUNNER_PERSONA_ORIENTATION=landscape): Separate gives each tab its own page and sidebar, shown or hidden on its
/// own, as before the one sidebar; the choice is kept across launches; Merged brings the one sidebar back. Against
/// scripts/mock/mock_hub.py (PERSONA_MOCK_HUB); PERSONA_SHOTS keeps the screens.
final class SidebarModeTests: XCTestCase {
    private var hub = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .landscapeLeft
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        hub = h
    }

    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    @MainActor func testSeparateIsKeptAndEachTabKeepsItsSidebar() throws {
        // Separate, from Settings › Appearance › Sidebar.
        var app = try launch(["-tab", "settings", "-app.tab", "settings", "-openAppearance", "sidebar"])
        choose(app, "separate")
        shot(app, "sidebar-mode-settings")
        closeSettings(app)
        let agentsTab = tab(app, "Agents")
        XCTAssertTrue(agentsTab.waitForExistence(timeout: 10), "Separate shows no tab bar:\n\(app.debugDescription)")
        XCTAssertFalse(app.descendants(matching: .any)["sidebar-agents-heading"].exists, "the one sidebar is still there")
        app.terminate()

        // Kept across a launch: the tabs again, on Persona's page with its own sidebar.
        app = try launch(["-tab", "assistant", "-app.tab", "assistant", "-assistant.list.closed", "NO", "-agents.list.closed", "NO"])
        XCTAssertTrue(tab(app, "Agents").waitForExistence(timeout: 10), "Separate is not kept across launches:\n\(app.debugDescription)")
        let history = app.descendants(matching: .any)["sidebar-main"].firstMatch
        XCTAssertTrue(history.waitForExistence(timeout: 15), "Persona's page has no sidebar of its own:\n\(app.debugDescription)")
        shot(app, "sidebar-mode-separate-persona")
        app.terminate()

        // Each tab's sidebar on its own: Persona's slid away, Agents' still out.
        app = try launch(["-tab", "assistant", "-app.tab", "assistant"])
        let main = app.descendants(matching: .any)["sidebar-main"].firstMatch
        XCTAssertTrue(main.waitForExistence(timeout: 15))
        sleep(1)
        let width = main.frame.maxX + 8 // past the row's trailing inset, onto the line beside the column
        drag(app, from: width, by: -(width * 0.8))
        XCTAssertTrue(main.until("exists == false OR isHittable == false"), "Persona's sidebar did not slide away")
        tab(app, "Agents").tap()
        XCTAssertTrue(app.collectionViews.firstMatch.waitForExistence(timeout: 10))
        sleep(2)
        let agentsColumn = app.collectionViews.allElementsBoundByIndex.first { $0.frame.minX < 40 && $0.isHittable }
        XCTAssertNotNil(agentsColumn, "Agents' sidebar went with Persona's:\n\(app.debugDescription)")
        shot(app, "sidebar-mode-separate-agents")
        tab(app, "Persona").tap()
        sleep(1)
        XCTAssertFalse(app.descendants(matching: .any)["sidebar-main"].firstMatch.isHittable, "Persona's sidebar came back with Agents'")
        app.terminate()

        // Back to Merged, kept too.
        app = try launch(["-tab", "settings", "-app.tab", "settings", "-openAppearance", "sidebar"])
        choose(app, "merged")
        closeSettings(app)
        app.terminate()
        app = try launch(["-tab", "assistant", "-app.tab", "assistant", "-agents.list.closed", "NO"])
        XCTAssertTrue(app.descendants(matching: .any)["sidebar-agents-heading"].firstMatch.waitForExistence(timeout: 15),
                      "Merged did not bring the one sidebar back:\n\(app.debugDescription)")
        XCTAssertFalse(tab(app, "Agents").exists, "the tab bar stayed in Merged")
    }

    // MARK: -

    @MainActor private func launch(_ extra: [String]) throws -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", ""] + extra
        app.launchPinned()
        guard app.windows.firstMatch.waitForExistence(timeout: 10), app.windows.firstMatch.frame.width > 900 else {
            throw XCTSkip("regular width on its side only (iPad, iPhone Duo open)")
        }
        sleep(2)
        return app
    }

    @MainActor private func choose(_ app: XCUIApplication, _ mode: String) {
        let row = app.descendants(matching: .any)["sidebar-mode-" + mode].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), "no Sidebar page in Settings:\n\(app.debugDescription)")
        row.tap()
        XCTAssertTrue(row.until("isSelected == true"), "\(mode) was not chosen")
    }

    @MainActor private func closeSettings(_ app: XCUIApplication) {
        let close = app.buttons["settings-close"].firstMatch
        if close.waitForExistence(timeout: 5) { close.tap() }
        sleep(2)
    }

    /// The system tab bar's tab (Separate): along the top on the iPad, where XCUITest does not call it a tab bar; a button
    /// of that name in the window's top strip.
    @MainActor private func tab(_ app: XCUIApplication, _ label: String) -> XCUIElement {
        let named = app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != %@", label, "sidebar-agents-heading"))
        return named.allElementsBoundByIndex.first { $0.frame.minY < 100 } ?? app.buttons["no-such-tab-" + label]
    }

    @MainActor private func drag(_ app: XCUIApplication, from x: CGFloat, by dx: CGFloat) {
        let window = app.windows.firstMatch.frame
        let start = app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: window.minX + x, dy: window.midY))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: dx, dy: 0)), withVelocity: .slow, thenHoldForDuration: 0.05)
        sleep(1)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }
}

private extension XCUIElement {
    func until(_ format: String, timeout: TimeInterval = 5) -> Bool {
        XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: format), object: self)], timeout: timeout) == .completed
    }
}
