import XCTest

/// The plugins' UI slots (docs/extensions/extensions.md「UI slots」) on the iPhone: Settings opens on the plugin's overview card (a stat
/// grid and a small chart) above its main-navigation entry (with its badge); the entry opens the plugin's page (tabs,
/// a stacked bar chart, a line chart), the card opens the same page; a machine's page ends with the plugin's section
/// for that machine. Against scripts/mock/mock_hub.py with MOCK_EXTENSIONS=1: `TEST_RUNNER_PERSONA_EXTENSIONS_HUB=http://127.0.0.1:<port>`;
/// `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots, named by `TEST_RUNNER_PERSONA_EXTENSIONS_APPEARANCE` and the style.
final class ExtensionSlotsTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    @MainActor private func launch(_ args: [String] = []) throws -> XCUIApplication {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            for label in ["允许", "Allow"] where alert.buttons[label].exists { alert.buttons[label].tap(); return true }
            return false
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "settings"] + args
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let look = env["PERSONA_EXTENSIONS_APPEARANCE"] ?? "light"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "slots-\(name)-\(UITestStyle.name)-\(look).png"))
    }

    @MainActor private func scrollTo(_ el: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<8 where !(el.exists && el.isHittable) { app.swipeUp() }
    }

    @MainActor func testCardEntryAndPage() throws {
        let app = try launch()
        let card = app.descendants(matching: .any)["plugin-card-example-gateway-today"]
        XCTAssertTrue(card.waitForExistence(timeout: 15), "no overview card on 设置:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["今天的用量"].exists, "the card has no title")
        XCTAssertTrue(app.staticTexts["17.0M"].waitForExistence(timeout: 10), "the card's stat grid is not drawn")
        let entry = app.buttons["plugin-nav-example-gateway-usage"]
        XCTAssertTrue(entry.waitForExistence(timeout: 10), "no main-navigation entry:\n\(app.debugDescription)")
        XCTAssertTrue(entry.staticTexts["3"].exists || app.staticTexts["3"].exists, "the entry has no badge")
        sleep(1) // the chart's marks settle
        shot(app, "settings")

        entry.tap()
        XCTAssertTrue(app.staticTexts["按天"].waitForExistence(timeout: 15), "the plugin's page is not drawn:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["输入"].exists && app.staticTexts["输出"].exists, "the stacked chart has no legend")
        shot(app, "page")
        // The range is a segmented: picking one goes to the plugin at once, its new view comes in its place.
        let range = app.descendants(matching: .any)["plugin-ui-segmented-range"].firstMatch
        XCTAssertTrue(range.exists, "no segmented:\n\(app.debugDescription)")
        XCTAssertTrue(range.buttons["7 天"].isSelected)
        range.buttons["30 天"].tap()
        let picked = NSPredicate(format: "isSelected == true")
        expectation(for: picked, evaluatedWith: range.buttons["30 天"])
        waitForExpectations(timeout: 10)
        scrollTo(app.staticTexts["按模型"], in: app)
        shot(app, "page-end")

        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(card.waitForExistence(timeout: 10))
        card.tap()
        XCTAssertTrue(app.staticTexts["按天"].waitForExistence(timeout: 15), "the card did not open its page")
    }

    @MainActor func testMachineSection() throws {
        let app = try launch(["-openMachines", "YES", "-openMachineDetail", "macbook"])
        let section = app.descendants(matching: .any)["plugin-machine-example-gateway-gpu"]
        for _ in 0..<10 where !section.exists { app.swipeUp() }
        XCTAssertTrue(section.waitForExistence(timeout: 15), "no plugin section on the machine's page:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["网关调用"].exists, "the section has no heading")
        XCTAssertTrue(app.staticTexts["214"].waitForExistence(timeout: 10), "the section's stat grid is not drawn")
        for _ in 0..<4 where !app.staticTexts["近 7 天的请求"].isHittable { app.swipeUp() }
        shot(app, "machine")
    }
}
