import XCTest

/// The Agents list menu's Filter (#6589), as the web's title-row Filter: a submenu first in the menu behind the selected
/// Agents switch, with Harness, Project, Model and Pinned Only (the machine stays further down); each one chosen is a
/// chip over the list that clears it, and Reset clears them all once two are on. Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB).
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps each screen, TEST_RUNNER_PERSONA_SCHEMES picks the appearances (light and dark),
/// TEST_RUNNER_PERSONA_LAUNCH_ARGS adds launch arguments (a text size: -UIPreferredContentSizeCategoryName
/// UICTContentSizeCategoryAccessibilityXXXL) and TEST_RUNNER_PERSONA_SHOT_SUFFIX names those screens apart.
final class AgentsFilterMenuTests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment

    override func setUp() { continueAfterFailure = false }

    @MainActor func testFilterChoicesBecomeChipsThatClearThem() throws {
        guard let hub = env["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        for scheme in (env["PERSONA_SCHEMES"] ?? "light,dark").split(separator: ",").map(String.init) {
            let app = launch(hub, scheme: scheme)
            let project = app.buttons["agents.projectFilter"], harness = app.buttons["agents.harnessFilter"]
            let reset = app.buttons["agents.resetFilters"]
            XCTAssertFalse(project.exists, "nothing chosen: no chip")
            shot(app, "\(scheme)-1-default")

            // the menu behind the selected Agents switch has the web's Filter, first
            openFilter(app)
            for label in ["Harness", "项目", "模型", "只看置顶"] {
                see(item(app, label), "\(label) in the Filter")
            }
            shot(app, "\(scheme)-2-menu")
            item(app, "项目").tap()
            see(app.buttons["blog"], "the blog project in its menu").tap()
            see(project, "the project's chip")
            XCTAssertEqual(project.label, "项目：blog")
            XCTAssertFalse(reset.exists, "one filter: no Reset")

            openFilter(app)
            item(app, "Harness").tap()
            see(app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Claude")).firstMatch, "Claude in the harness menu").tap()
            see(harness, "the harness's chip")
            see(reset, "Reset with two filters on")
            shot(app, "\(scheme)-3-filtered")

            project.tap()
            gone(project, "the chip clears its filter")
            XCTAssertTrue(harness.exists)
            XCTAssertFalse(reset.exists, "one filter left: no Reset")
            harness.tap()
            gone(harness, "the last chip clears its filter")
            app.terminate()
        }
    }

    // MARK: - Helpers

    /// The list menu, then its Filter submenu.
    @MainActor private func openFilter(_ app: XCUIApplication) {
        see(app.buttons["shell-agents"].firstMatch, "the selected Agents switch").tap()
        see(item(app, "筛选"), "the Filter first in the list menu").tap()
    }

    /// A menu row by the start of its label, whatever it is drawn as (Pinned Only is a toggle).
    private func item(_ app: XCUIApplication, _ label: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", label)).firstMatch
    }

    @MainActor private func launch(_ hub: String, scheme: String) -> XCUIApplication {
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            for label in ["允许", "Allow", "好", "OK"] where alert.buttons[label].exists { alert.buttons[label].tap(); return true }
            return false
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-ui.scheme", scheme, "-app.tab", "chats", "-dev.openChat", "", "-tab", "dev",
                               "-openAgentsFresh", "YES"]
            + (env["PERSONA_LAUNCH_ARGS"]?.split(separator: " ").map(String.init) ?? [])
        app.launchPinned()
        dismissSystemAlerts(wait: 4)
        see(app.buttons["agents.filter.all"], "the quick filters", timeout: 15)
        return app
    }

    /// The notification question is SpringBoard's.
    @MainActor private func dismissSystemAlerts(wait: TimeInterval = 1) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        var deadline = Date.now.addingTimeInterval(wait)
        while Date.now < deadline {
            let alert = springboard.alerts.firstMatch
            if alert.waitForExistence(timeout: 0.5) {
                for label in ["允许", "Allow", "好", "OK"] where alert.buttons[label].exists { alert.buttons[label].tap(); break }
                deadline = Date.now.addingTimeInterval(1.5)
            }
        }
    }

    @discardableResult
    private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 10) -> XCUIElement {
        if !e.waitForExistence(timeout: timeout) { XCTFail("\(what) not found") }
        return e
    }

    private func gone(_ e: XCUIElement, _ what: String) {
        let deadline = Date.now.addingTimeInterval(10)
        while e.exists, Date.now < deadline { usleep(200_000) }
        XCTAssertFalse(e.exists, what)
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            let suffix = env["PERSONA_SHOT_SUFFIX"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("agents-filter-\(name)\(suffix).png"))
        }
    }
}
